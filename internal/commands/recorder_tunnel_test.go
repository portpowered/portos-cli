package commands

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/turn/v4"
	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
)

const tunnelTURNUser, tunnelTURNPassword, tunnelTURNRealm = "1794358800:alice", "secret", "portos"

// startTURNServer runs a TURN-over-TCP server on loopback. It allocates UDP relays on a routable
// local address: the camera's ICE agent gathers no loopback candidates, and on Windows its
// sockets cannot send to 127.0.0.1.
func startTURNServer(t *testing.T) string {
	t.Helper()
	probe, err := net.Dial("udp4", "192.0.2.1:9") // no packet is sent; this only picks the route
	if err != nil {
		t.Skipf("no routable IPv4 address for the relay: %v", err)
	}
	relayIP := probe.LocalAddr().(*net.UDPAddr).IP
	_ = probe.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := turn.NewServer(turn.ServerConfig{
		Realm: tunnelTURNRealm,
		AuthHandler: func(username, realm string, _ net.Addr) ([]byte, bool) {
			return turn.GenerateAuthKey(username, realm, tunnelTURNPassword), username == tunnelTURNUser
		},
		ListenerConfigs: []turn.ListenerConfig{{
			Listener:              listener,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: relayIP, Address: relayIP.String()},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().String()
}

// startTunnelServer stands in for GET /relay-tunnel: it upgrades and pipes the socket to relayAddr.
func startTunnelServer(t *testing.T, relayAddr string) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay, err := net.Dial("tcp", relayAddr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			_ = relay.Close()
			return
		}
		tunnel := apiclient.NewTunnelConn(socket)
		go func() { _, _ = io.Copy(relay, tunnel); _ = relay.Close() }()
		_, _ = io.Copy(tunnel, relay)
		_ = tunnel.Close()
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

// tunnelRelay is a relay client whose API sits behind a proxy, so the recorder must tunnel.
type tunnelRelay struct {
	turnAddr  string
	tunnelURL string
	dials     *atomic.Int32
}

func (r tunnelRelay) Credentials(context.Context) (*api.RelayCredentials, error) {
	url := "turn:" + r.turnAddr + "?transport=tcp"
	return &api.RelayCredentials{IceServers: []api.RelayIceServer{{Urls: []string{url}, Username: tunnelTURNUser, Credential: tunnelTURNPassword}}}, nil
}

func (r tunnelRelay) Proxied() bool { return true }

func (r tunnelRelay) Tunnel(ctx context.Context) (net.Conn, error) {
	r.dials.Add(1)
	socket, _, err := websocket.DefaultDialer.DialContext(ctx, r.tunnelURL, nil)
	if err != nil {
		return nil, err
	}
	return apiclient.NewTunnelConn(socket), nil
}

type tunneledCameraClient struct {
	cameraClient
	relay tunnelRelay
}

func (c tunneledCameraClient) Relay() apiclient.RelayClient { return c.relay }

// A sandbox behind an HTTP proxy cannot reach the relay over TCP; with the relay alone allowed,
// the capture still connects because every TURN byte goes through the tunnel.
func TestRecorderBehindAProxyReachesTheRelayThroughTheTunnel(t *testing.T) {
	turnAddr := startTURNServer(t)
	relay := tunnelRelay{turnAddr: turnAddr, tunnelURL: startTunnelServer(t, turnAddr), dials: &atomic.Int32{}}
	camera := &pionCamera{src: syntheticSource(4, 25), withAudio: true, t: t}
	recorder := NewRecorder(tunneledCameraClient{cameraClient: cameraClient{camera: camera}, relay: relay}, logging.NewLoggerRef())
	recorder.iceServers = nil
	result, err := recorder.Run(t.Context(), RecorderParams{
		EndpointID: "camera", SessionID: "test", SessionDomain: "cli",
		Duration: 2 * time.Second, Audio: true, RelayOnly: true,
	})
	if err != nil {
		t.Fatalf("relay-only capture through the tunnel: %v", err)
	}
	defer result.Remove()
	if relay.dials.Load() == 0 {
		t.Fatal("the recorder never opened the relay tunnel")
	}
	if result.TempPath == "" || !result.HasAudio() {
		t.Fatalf("result=%+v, want video and audio", result)
	}
}
