package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/logging"
	"go.uber.org/zap"
)

// mockWebSocketClient implements apiclient.WebSocketClient for testing
type mockWebSocketClient struct {
	mu            sync.Mutex
	connectErr    error
	connectCount  int
	messages      [][]byte
	readIdx       int
	readErr       error
	readBlock     chan struct{} // if non-nil, ReadMessage blocks until closed
	writeMessages [][]byte
	closed        bool
}

func (m *mockWebSocketClient) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectCount++
	if m.connectErr != nil {
		return m.connectErr
	}
	m.closed = false
	return nil
}

func (m *mockWebSocketClient) ReadMessage() ([]byte, error) {
	if m.readBlock != nil {
		<-m.readBlock
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readErr != nil {
		return nil, m.readErr
	}
	if m.readIdx < len(m.messages) {
		msg := m.messages[m.readIdx]
		m.readIdx++
		return msg, nil
	}
	return nil, fmt.Errorf("connection closed")
}

func (m *mockWebSocketClient) WriteMessage(msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeMessages = append(m.writeMessages, msg)
	return nil
}

func (m *mockWebSocketClient) StartPingLoop(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (m *mockWebSocketClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// mockAPIClient implements apiclient.Client for testing
type mockAPIClient struct {
	ws apiclient.WebSocketClient
}

func (m *mockAPIClient) Endpoints() apiclient.EndpointsClient  { return nil }
func (m *mockAPIClient) Messages() apiclient.MessagesClient    { return nil }
func (m *mockAPIClient) Relay() apiclient.RelayClient          { return nil }
func (m *mockAPIClient) Rules() apiclient.RulesClient          { return nil }
func (m *mockAPIClient) RulesFor(string) apiclient.RulesClient { return nil }
func (m *mockAPIClient) WebSocket() apiclient.WebSocketClient  { return m.ws }

// mockFormatter captures output
type mockFormatter struct {
	mu      sync.Mutex
	outputs []interface{}
	json    bool
	quiet   bool
}

func (f *mockFormatter) Print(data interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputs = append(f.outputs, data)
	return nil
}
func (f *mockFormatter) PrintError(err error) error { return nil }
func (f *mockFormatter) IsJSON() bool               { return f.json }
func (f *mockFormatter) IsQuiet() bool              { return f.quiet }

// mockAuthService implements auth.Service
type mockServeAuthService struct{}

func (m *mockServeAuthService) GetAccessToken(ctx context.Context) (string, error) {
	return "test-token", nil
}
func (m *mockServeAuthService) SetToken(ctx context.Context, token string) error  { return nil }
func (m *mockServeAuthService) IsAuthenticated(ctx context.Context) (bool, error) { return true, nil }
func (m *mockServeAuthService) Login(ctx context.Context, clientID string, handler auth.LoginMessageHandler) error {
	return nil
}
func (m *mockServeAuthService) BrowserLogin(ctx context.Context, handler auth.LoginMessageHandler) error {
	return nil
}
func (m *mockServeAuthService) RefreshToken(ctx context.Context) error { return nil }
func (m *mockServeAuthService) GetPrincipalPathFromToken(ctx context.Context) (string, string, string, error) {
	return "port1", "users", "test", nil
}

func newTestLogger() *logging.LoggerRef {
	ref := logging.NewLoggerRef()
	ref.Set(zap.NewNop())
	return ref
}

func TestServeCommand_ContextCancellation(t *testing.T) {
	ws := &mockWebSocketClient{
		readBlock: make(chan struct{}),
	}
	api := &mockAPIClient{ws: ws}
	formatter := &mockFormatter{}

	cmd := NewServeCommand(api, formatter, &mockServeAuthService{}, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- cmd.Serve(ctx, ServeParams{})
	}()

	// Give time for connection to establish
	time.Sleep(50 * time.Millisecond)
	cancel()
	close(ws.readBlock)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error on context cancellation, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for serve to stop")
	}
}

func TestServeCommand_MessageReceived(t *testing.T) {
	ws := &mockWebSocketClient{
		messages: [][]byte{[]byte(`{"type":"event","data":"test"}`)},
	}
	api := &mockAPIClient{ws: ws}
	formatter := &mockFormatter{}

	cmd := NewServeCommand(api, formatter, &mockServeAuthService{}, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- cmd.Serve(ctx, ServeParams{})
	}()

	// Wait for message to be processed and reconnect to happen
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	formatter.mu.Lock()
	defer formatter.mu.Unlock()

	// Check that the message was printed
	found := false
	for _, o := range formatter.outputs {
		if s, ok := o.(string); ok && s == "{\"type\":\"event\",\"data\":\"test\"}\n" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected message to be printed, got outputs: %v", formatter.outputs)
	}
}

func TestServeCommand_JSONOutput(t *testing.T) {
	ws := &mockWebSocketClient{
		messages: [][]byte{[]byte(`{"type":"event"}`)},
	}
	api := &mockAPIClient{ws: ws}
	formatter := &mockFormatter{json: true}

	cmd := NewServeCommand(api, formatter, &mockServeAuthService{}, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- cmd.Serve(ctx, ServeParams{})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	formatter.mu.Lock()
	defer formatter.mu.Unlock()

	// Find a ServeEvent in the outputs
	found := false
	for _, o := range formatter.outputs {
		if evt, ok := o.(ServeEvent); ok {
			var msg map[string]interface{}
			if err := json.Unmarshal(evt.Message, &msg); err == nil {
				if msg["type"] == "event" {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Errorf("expected ServeEvent with JSON message, got outputs: %v", formatter.outputs)
	}
}

func TestServeCommand_ReconnectsOnDisconnect(t *testing.T) {
	ws := &mockWebSocketClient{
		// Return error immediately to trigger reconnect
		readErr: fmt.Errorf("connection reset"),
	}
	api := &mockAPIClient{ws: ws}
	formatter := &mockFormatter{}

	cmd := NewServeCommand(api, formatter, &mockServeAuthService{}, newTestLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Serve(ctx, ServeParams{})
	}()

	// Wait for at least 2 connect attempts (initial + 1 reconnect)
	time.Sleep(1500 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	ws.mu.Lock()
	count := ws.connectCount
	ws.mu.Unlock()

	if count < 2 {
		t.Errorf("expected at least 2 connect attempts (reconnection), got %d", count)
	}
}

func TestServeCommand_ConnectFailureTriggersReconnect(t *testing.T) {
	ws := &mockWebSocketClient{
		connectErr: fmt.Errorf("connection refused"),
	}
	api := &mockAPIClient{ws: ws}
	formatter := &mockFormatter{}

	cmd := NewServeCommand(api, formatter, &mockServeAuthService{}, newTestLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Serve(ctx, ServeParams{})
	}()

	time.Sleep(1500 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	ws.mu.Lock()
	count := ws.connectCount
	ws.mu.Unlock()

	if count < 2 {
		t.Errorf("expected at least 2 connect attempts on connection failure, got %d", count)
	}
}

func TestServeEvent_JSON(t *testing.T) {
	event := ServeEvent{
		Timestamp: "2026-03-21T12:00:00Z",
		Message:   json.RawMessage(`{"type":"test"}`),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal ServeEvent: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result["timestamp"] != "2026-03-21T12:00:00Z" {
		t.Errorf("expected timestamp '2026-03-21T12:00:00Z', got %q", result["timestamp"])
	}

	msg, ok := result["message"].(map[string]interface{})
	if !ok {
		t.Fatal("expected message to be an object")
	}
	if msg["type"] != "test" {
		t.Errorf("expected message.type 'test', got %q", msg["type"])
	}
}
