package transport

import (
	"context"
	"errors"
	"os"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/endpointid"
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/host"
	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

// LeaseEnv names the environment variable that carries a hold lease when
// --lease is not given.
const LeaseEnv = "PORTOS_LEASE"

// kindNamespaces are the capability namespaces each hold kind covers.
var kindNamespaces = map[string][]string{
	"motion": {api.INTERFACE_PORTOS_REMOTE_CONTROL},
	"camera": {api.INTERFACE_PORTOS_SNAPSHOT, api.INTERFACE_PORTOS_MEDIA_PLAYER},
}

type resolver struct {
	configDir func() (string, error)
	lease     func() string
	cloud     Sender
	dial      func(ctx context.Context, configDir string) (hostAPI, error)
}

// NewResolver returns the Resolver the commands use. It looks for a running
// session host but never starts one.
func NewResolver(configProvider *config.ManagerProvider, client apiclient.Client, global *flags.GlobalFlags) Resolver {
	return &resolver{
		configDir: func() (string, error) {
			manager, err := configProvider.GetManager()
			if err != nil {
				return "", err
			}
			return manager.ConfigDir(), nil
		},
		lease: func() string {
			if global != nil && global.Lease != "" {
				return global.Lease
			}
			return os.Getenv(LeaseEnv)
		},
		cloud: Cloud(client),
		dial:  dialHost,
	}
}

func dialHost(ctx context.Context, configDir string) (hostAPI, error) {
	return hostclient.Dial(ctx, configDir)
}

// For returns the host sender when a live hold on the endpoint covers the
// namespace, even if no lease was given: the host then answers
// hold_lease_required instead of the cloud racing the hold.
//
// A lease means "I operate under a hold". With a lease and a namespace a hold
// covers, the cloud is never an answer: when no live hold matches, the sender
// refuses with hold_ended (an ended hold on the endpoint is known) or
// hold_not_found (no host, or no hold), so a stale lease cannot drive the
// robot unheld. Everything else goes to the cloud.
func (r *resolver) For(ctx context.Context, endpointID, namespace string) (Sender, error) {
	dir, err := r.configDir()
	if err != nil {
		return nil, err
	}
	lease := r.lease()
	leased := lease != "" && coveredNamespace(namespace)
	hostConn, err := r.dial(ctx, dir)
	if errors.Is(err, hostclient.ErrNotRunning) {
		return r.noLiveHold(leased, endpointID, false), nil
	}
	if err != nil {
		return nil, err
	}
	holds, err := hostConn.ListHolds(ctx)
	if errors.Is(err, hostclient.ErrNotRunning) || errors.Is(err, hostclient.ErrRouteNotServed) {
		return r.noLiveHold(leased, endpointID, false), nil // the host went away, or serves no holds
	}
	if err != nil {
		return nil, err
	}
	_, keyed := endpointid.Key(endpointID)
	ended := false
	for _, hold := range holds {
		if !covers(hold.Kinds, namespace) {
			continue
		}
		if hold.State == host.StateEnded {
			ended = ended || endpointid.Same(hold.EndpointID, endpointID)
			continue
		}
		// An id with no local key (an alias) cannot be matched to a hold, so
		// fail safe toward any live hold: the host refuses with a typed error
		// instead of the cloud racing the hold's session.
		if !keyed || endpointid.Same(hold.EndpointID, endpointID) {
			return &hostSender{host: hostConn, lease: lease}, nil
		}
	}
	return r.noLiveHold(leased, endpointID, ended), nil
}

// noLiveHold is the sender when no live hold matches: the cloud, unless the
// caller holds a lease.
func (r *resolver) noLiveHold(leased bool, endpointID string, ended bool) Sender {
	if !leased {
		return r.cloud
	}
	if ended {
		return &refusingSender{endpointID: endpointID, code: holds.CodeHoldEnded, message: "the hold on " + endpointID + " has ended; open a new hold"}
	}
	return &refusingSender{endpointID: endpointID, code: host.CodeHoldNotFound, message: "no live hold on " + endpointID + " is running; open one with `port session`"}
}

func coveredNamespace(namespace string) bool {
	for _, namespaces := range kindNamespaces {
		for _, ns := range namespaces {
			if ns == namespace {
				return true
			}
		}
	}
	return false
}

func covers(kinds []string, namespace string) bool {
	for _, kind := range kinds {
		for _, ns := range kindNamespaces[kind] {
			if ns == namespace {
				return true
			}
		}
	}
	return false
}
