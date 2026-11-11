package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/portpowered/portos-cli/internal/wire"
	"github.com/portpowered/portos-cli/test/mocks"
)

// runMove runs `msg move` and returns the JSON result and
// every message request the command sent, in order.
func runMove(t *testing.T, instructions ...string) (map[string]interface{}, []map[string]interface{}) {
	t.Helper()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	var captured [][]byte
	messageMock := &moveRoundTripper{capture: func(body []byte) { captured = append(captured, body) }}
	mockClient := setupMockClientWithRoutes(map[string]http.RoundTripper{
		"/message-requests": messageMock,
	})

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	args := append([]string{"msg", "move", "port1/principals/user123/endpoints/ep1"}, instructions...)
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, append(args, "--json")...))

	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("failed to execute move command: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(testWriter.StdoutString()), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput: %s", err, testWriter.StdoutString())
	}
	requests := make([]map[string]interface{}, len(captured))
	for i, body := range captured {
		if err := json.Unmarshal(body, &requests[i]); err != nil {
			t.Fatalf("failed to parse captured request: %v", err)
		}
	}
	return result, requests
}

// moveRoundTripper answers each message request SUCCEEDED, with a measured
// move-response for moves, as the server does for a SYNC send.
type moveRoundTripper struct {
	capture func([]byte)
}

func (m *moveRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	m.capture(body)
	var sent struct {
		Message struct {
			Header struct {
				Name string `json:"name"`
			} `json:"header"`
		} `json:"message"`
	}
	_ = json.Unmarshal(body, &sent)
	results := `[{"routeId":"route-1","targetId":"ep1"}]`
	if sent.Message.Header.Name == "move" {
		results = `[{"routeId":"route-1","targetId":"ep1","message":{"header":{"namespace":"port1/systems/zero/capability-interfaces/remote-control","name":"move-response","version":"1.0"},"body":{"distanceMetres":0.05,"turnedDegrees":0,"pose":{"point":{"type":"Point","coordinates":[1,2]},"relativeToGroupRouteId":"map-1","observedAt":"2026-11-10T09:00:00Z"}}}}]`
	}
	return mocks.NewJSONMockRoundTripper(200, `{"id":"request","status":"SUCCEEDED","results":`+results+`,"errors":[],"deadline":"2026-10-27T00:00:30Z","syncDeadlineSeconds":30}`).RoundTrip(req)
}

func requestHeader(t *testing.T, request map[string]interface{}) map[string]interface{} {
	t.Helper()
	header := request["message"].(map[string]interface{})["header"].(map[string]interface{})
	if header["namespace"] != "port1/systems/zero/capability-interfaces/remote-control" {
		t.Errorf("expected remote-control namespace, got %v", header["namespace"])
	}
	return header
}

func requestBody(request map[string]interface{}) map[string]interface{} {
	body, _ := request["message"].(map[string]interface{})["body"].(map[string]interface{})
	return body
}

// requireSession checks the requests are start-session, one move per
// instruction numbered from 1, then end-session, and returns the move bodies.
func requireSession(t *testing.T, requests []map[string]interface{}, moves int) []map[string]interface{} {
	t.Helper()
	if len(requests) != moves+2 {
		t.Fatalf("expected %d requests (start, %d moves, end), got %d", moves+2, moves, len(requests))
	}
	if name := requestHeader(t, requests[0])["name"]; name != "start-session" {
		t.Errorf("expected first message start-session, got %v", name)
	}
	if name := requestHeader(t, requests[len(requests)-1])["name"]; name != "end-session" {
		t.Errorf("expected last message end-session, got %v", name)
	}
	bodies := make([]map[string]interface{}, moves)
	for i := 0; i < moves; i++ {
		request := requests[i+1]
		if name := requestHeader(t, request)["name"]; name != "move" {
			t.Errorf("expected message %d to be move, got %v", i+1, name)
		}
		bodies[i] = requestBody(request)
		if seq := bodies[i]["seqNum"].(float64); seq != float64(i+1) {
			t.Errorf("expected seqNum %d, got %v", i+1, seq)
		}
	}
	return bodies
}

func TestMoveCommandForward(t *testing.T) {
	result, requests := runMove(t, "forward 0.05m")
	if result["endpoint_id"] != "port1/principals/user123/endpoints/ep1" {
		t.Errorf("expected endpoint_id in result, got %v", result["endpoint_id"])
	}
	replies, ok := result["replies"].([]interface{})
	if !ok || len(replies) != 1 || replies[0].(map[string]interface{})["distanceMetres"] != 0.05 {
		t.Errorf("expected one measured reply, got %v", result["replies"])
	}
	moves := requireSession(t, requests, 1)
	if v := moves[0]["velocity"].(float64); v <= 0 {
		t.Errorf("expected positive velocity for forward, got %v", v)
	}
}

func TestMoveCommandWithRotation(t *testing.T) {
	_, requests := runMove(t, "rotate 10")
	moves := requireSession(t, requests, 1)
	if v := moves[0]["velocity"].(float64); v != 0 {
		t.Errorf("expected zero velocity for rotation, got %v", v)
	}
	if o := moves[0]["omega"].(float64); o == 0 {
		t.Error("expected non-zero omega for rotation")
	}
}

func TestMoveCommandMultiStep(t *testing.T) {
	// Multiple args get joined: "forward" + "rotate" + "backward"
	_, requests := runMove(t, "forward 0.05m", "rotate 10", "backward 0.05m")
	moves := requireSession(t, requests, 3)
	if moves[2]["velocity"].(float64) >= 0 {
		t.Errorf("expected negative velocity for backward, got %v", moves[2]["velocity"])
	}
}

func TestMoveCommandInvalidGrammar(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	mockClient := setupMockClientWithRoutes(map[string]http.RoundTripper{
		"/message-requests": mocks.NewJSONMockRoundTripper(200, `{"id":"request","status":"QUEUED","results":[],"errors":[],"deadline":"2026-10-27T00:00:30Z","syncDeadlineSeconds":30}`),
	})

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "msg", "move", "port1/principals/user123/endpoints/ep1", "invalid-direction 1m"))

	err = rootCmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid grammar, but got none")
	}
}

func TestMoveCommandMissingArgs(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "msg", "move"))

	err = rootCmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for missing arguments, but got none")
	}
}
