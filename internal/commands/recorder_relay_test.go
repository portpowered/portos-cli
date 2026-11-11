package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type relayClient struct {
	apiclient.Client
	credentials *api.RelayCredentials
	err         error
}

func (c relayClient) Relay() apiclient.RelayClient { return c }

func (c relayClient) Credentials(context.Context) (*api.RelayCredentials, error) {
	return c.credentials, c.err
}

var stun = webrtc.ICEServer{URLs: []string{"stun:stun.example.test:19302"}}

func relayCredentials() *api.RelayCredentials {
	return &api.RelayCredentials{IceServers: []api.RelayIceServer{{Urls: []string{"turn:relay.example.test:443?transport=tcp"}, Username: "1794358800:alice", Credential: "c3VwZXI="}}}
}

func relayRecorder(client apiclient.Client) (*Recorder, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.WarnLevel)
	logger := logging.NewLoggerRef()
	logger.Set(zap.New(core))
	recorder := NewRecorder(client, logger)
	recorder.iceServers = []webrtc.ICEServer{stun}
	return recorder, logs
}

func TestRecorderAddsRelayServers(t *testing.T) {
	recorder, logs := relayRecorder(relayClient{credentials: relayCredentials()})
	config, err := recorder.iceConfiguration(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if config.ICETransportPolicy != webrtc.ICETransportPolicyAll || len(config.ICEServers) != 2 {
		t.Fatalf("want STUN plus the relay, got %+v", config)
	}
	relay := config.ICEServers[1]
	if relay.URLs[0] != "turn:relay.example.test:443?transport=tcp" || relay.Username != "1794358800:alice" || relay.Credential != "c3VwZXI=" {
		t.Fatalf("relay server not carried: %+v", relay)
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected warnings: %v", logs.All())
	}
}

func TestRecorderFallsBackToStunWhenRelayUnavailable(t *testing.T) {
	for name, client := range map[string]relayClient{
		"error":      {err: errors.New("the media relay is not configured")},
		"no servers": {credentials: &api.RelayCredentials{}},
	} {
		t.Run(name, func(t *testing.T) {
			recorder, logs := relayRecorder(client)
			config, err := recorder.iceConfiguration(t.Context(), false)
			if err != nil {
				t.Fatal(err)
			}
			if len(config.ICEServers) != 1 || config.ICEServers[0].URLs[0] != stun.URLs[0] {
				t.Fatalf("want STUN only, got %+v", config.ICEServers)
			}
			if logs.Len() != 1 || !strings.HasPrefix(logs.All()[0].Message, "relay unavailable") {
				t.Fatalf("want one relay unavailable warning, got %v", logs.All())
			}
		})
	}
}

func TestRelayOnlyFailsWithoutRelay(t *testing.T) {
	recorder, _ := relayRecorder(relayClient{err: errors.New("the media relay is not configured")})
	_, err := recorder.iceConfiguration(t.Context(), true)
	if err == nil || !strings.Contains(err.Error(), "relay unavailable") {
		t.Fatalf("want a relay unavailable error, got %v", err)
	}
	_, err = recorder.Run(t.Context(), RecorderParams{EndpointID: "camera", SessionID: "test", Duration: 1, RelayOnly: true})
	if err == nil || !strings.Contains(err.Error(), "relay unavailable") {
		t.Fatalf("relay-only recording did not fail before connecting: %v", err)
	}
}

func TestRelayOnlyUsesOnlyTheRelay(t *testing.T) {
	recorder, _ := relayRecorder(relayClient{credentials: relayCredentials()})
	config, err := recorder.iceConfiguration(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if config.ICETransportPolicy != webrtc.ICETransportPolicyRelay || len(config.ICEServers) != 1 || config.ICEServers[0].URLs[0] != "turn:relay.example.test:443?transport=tcp" {
		t.Fatalf("want the relay alone with relay policy, got %+v", config)
	}
}
