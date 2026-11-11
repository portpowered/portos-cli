package commands

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/portpowered/portos-cli/internal/logging"
)

type fakeSessionHost struct {
	opened  []hostclient.OpenHoldRequest
	holds   []hostclient.Hold
	release string
}

func (f *fakeSessionHost) OpenHold(_ context.Context, req hostclient.OpenHoldRequest) (hostclient.OpenedHold, error) {
	f.opened = append(f.opened, req)
	h := hostclient.Hold{ID: "hold-1", EndpointID: req.EndpointID, Kinds: req.Kinds, State: "live"}
	return hostclient.OpenedHold{HoldStatus: h, Lease: "lease-abc"}, nil
}
func (f *fakeSessionHost) GetHold(context.Context, string) (hostclient.Hold, error) {
	return f.holds[0], nil
}
func (f *fakeSessionHost) ListHolds(context.Context) ([]hostclient.Hold, error) { return f.holds, nil }
func (f *fakeSessionHost) ReleaseHold(_ context.Context, id string) (hostclient.Hold, error) {
	f.release = id
	return hostclient.Hold{ID: id, State: "ended"}, nil
}

func newTestSessionCommand(host *fakeSessionHost) (*SessionCommand, *recordingFormatter) {
	formatter := &recordingFormatter{json: true}
	connect := func(context.Context, string) (SessionHost, error) { return host, nil }
	return &SessionCommand{
		formatter: formatter,
		logger:    logging.NewLoggerRef(),
		configDir: func() (string, error) { return "dir", nil },
		ensure:    connect,
		dial:      connect,
	}, formatter
}

func TestSessionHoldPrintsLease(t *testing.T) {
	host := &fakeSessionHost{}
	cmd, formatter := newTestSessionCommand(host)
	if err := cmd.Hold(t.Context(), HoldParams{EndpointID: "ep/1", Motion: true, DeadMan: 10 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	if len(host.opened) != 1 || host.opened[0].DeadManSeconds != 600 || len(host.opened[0].Kinds) != 1 || host.opened[0].Kinds[0] != "motion" {
		t.Fatalf("opened = %+v", host.opened)
	}
	encoded, err := json.Marshal(formatter.printed[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["holdId"] != "hold-1" || got["lease"] != "lease-abc" || got["endpointId"] != "ep/1" {
		t.Fatalf("printed = %s", encoded)
	}
	if pose, present := got["pose"]; !present || pose != nil {
		t.Fatalf("pose = %v (present %v), want an explicit null", pose, present)
	}
}

func TestSessionHoldNeedsAKind(t *testing.T) {
	cmd, _ := newTestSessionCommand(&fakeSessionHost{})
	if err := cmd.Hold(t.Context(), HoldParams{EndpointID: "ep/1"}); err == nil {
		t.Fatal("expected an error without --motion")
	}
}

func TestSessionStatusAndRelease(t *testing.T) {
	host := &fakeSessionHost{holds: []hostclient.Hold{{ID: "hold-1", EndpointID: "ep/1", State: "ended"}}}
	cmd, formatter := newTestSessionCommand(host)
	if err := cmd.Status(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if result := formatter.printed[0].(SessionStatusResult); len(result.Holds) != 1 {
		t.Fatalf("status = %+v", result)
	}
	if err := cmd.Release(t.Context(), "hold-1"); err != nil {
		t.Fatal(err)
	}
	if host.release != "hold-1" {
		t.Fatalf("released %q", host.release)
	}
}
