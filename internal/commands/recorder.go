package commands

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
	"go.uber.org/zap"
)

const (
	rtcSessionNamespace      = "port1/systems/zero/capability-interfaces/rtc-session"
	rtcSessionInitiate       = "initiate-session-with-offer"
	rtcAnswerGenerated       = "answer-generated-for-session"
	rtcSessionCleanupTimeout = 5 * time.Second
	iceGatherTimeout         = 30 * time.Second
	frameWaitTimeout         = 15 * time.Second
)

// Recorder establishes RTC sessions and records video, and optionally audio, to temp files.
// Callers (snapshot, record) are responsible for resolving endpoint IDs and session domain;
// the caller must delete the returned temp file when done.
type Recorder struct {
	apiClient apiclient.Client
	logger    *logging.LoggerRef
	// iceServers is overridden by tests so they never reach the public STUN servers.
	iceServers []webrtc.ICEServer
}

// NewRecorder creates a new RTC recorder.
func NewRecorder(apiClient apiclient.Client, logger *logging.LoggerRef) *Recorder {
	return &Recorder{apiClient: apiClient, logger: logger, iceServers: []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"stun:stun1.l.google.com:19302"}},
	}}
}

// RecorderParams are inputs for Run.
type RecorderParams struct {
	EndpointID    string
	SessionDomain string
	SessionID     string
	Duration      time.Duration
	// Audio also offers a receive-only Opus transceiver. A device that sends no audio is not an
	// error: the result simply has no audio file.
	Audio bool
	// CheckVideoCodec, when set, vets the negotiated video codec ("H264" or "VP8") before the
	// capture starts, so an output container that cannot hold it fails fast.
	CheckVideoCodec func(codec string) error
	// RelayOnly connects only through the Port OS relay, for networks without UDP. Without it the
	// relay is offered alongside STUN, and a relay that cannot be reached is only a warning.
	RelayOnly bool
}

// iceConfiguration offers the STUN servers plus the Port OS relay. A relay fetch failure is a
// warning, unless relayOnly makes the relay the only way to connect.
func (r *Recorder) iceConfiguration(ctx context.Context, relayOnly bool) (webrtc.Configuration, error) {
	servers := append([]webrtc.ICEServer(nil), r.iceServers...)
	credentials, err := r.apiClient.Relay().Credentials(ctx)
	if err == nil && len(credentials.IceServers) == 0 {
		err = errors.New("the relay returned no servers")
	}
	if err != nil {
		if relayOnly {
			return webrtc.Configuration{}, fmt.Errorf("relay unavailable: %w", err)
		}
		r.logger.Logger().Warn("relay unavailable: connecting without it", zap.Error(err))
		return webrtc.Configuration{ICEServers: servers}, nil
	}
	relay := make([]webrtc.ICEServer, 0, len(credentials.IceServers))
	for _, server := range credentials.IceServers {
		relay = append(relay, webrtc.ICEServer{URLs: server.Urls, Username: server.Username, Credential: server.Credential})
	}
	if relayOnly {
		return webrtc.Configuration{ICEServers: relay, ICETransportPolicy: webrtc.ICETransportPolicyRelay}, nil
	}
	return webrtc.Configuration{ICEServers: append(servers, relay...)}, nil
}

// peerConnectionAPI reaches the relay through the API's relay tunnel when the API is reached
// through an HTTP proxy, since such networks (agent sandboxes, corporate egress) usually refuse
// direct TCP to the relay. Pion dials every TURN-over-TCP server through the proxy dialer.
func (r *Recorder) peerConnectionAPI(ctx context.Context) *webrtc.API {
	relay := r.apiClient.Relay()
	if relay == nil || !relay.Proxied() {
		return webrtc.NewAPI()
	}
	r.logger.Logger().Info("API is behind an HTTP proxy: reaching the relay through the relay tunnel")
	settings := webrtc.SettingEngine{}
	settings.SetICEProxyDialer(tunnelDialer{ctx: ctx, relay: relay})
	return webrtc.NewAPI(webrtc.WithSettingEngine(settings))
}

// tunnelDialer is the ICE proxy dialer. The tunnel always leads to the Port OS relay, whose TURN
// URL is the only TCP TURN server the recorder offers, so the address is not needed.
type tunnelDialer struct {
	ctx   context.Context
	relay apiclient.RelayClient
}

func (d tunnelDialer) Dial(_, _ string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(d.ctx, iceGatherTimeout)
	defer cancel()
	return d.relay.Tunnel(ctx)
}

// Run establishes an RTC session (non-trickle ICE), records video for the given duration to a temp file, then disconnects.
// Returns the path to the temp file; the caller must remove it when done.
func (r *Recorder) Run(ctx context.Context, params RecorderParams) (*RecorderResult, error) {
	if params.EndpointID == "" || params.SessionID == "" {
		return nil, fmt.Errorf("endpoint ID and session ID are required")
	}
	if params.Duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}

	config, err := r.iceConfiguration(ctx, params.RelayOnly)
	if err != nil {
		return nil, err
	}
	peerConnection, err := r.peerConnectionAPI(ctx).NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}
	recordCtx, cancelRecord := context.WithCancel(ctx)
	st := newRecordState(params)
	delivered := false
	defer st.shutdown(cancelRecord, peerConnection, &delivered)

	_, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		return nil, fmt.Errorf("add video transceiver: %w", err)
	}

	if params.Audio {
		if err = addOpusReceiver(peerConnection); err != nil {
			return nil, err
		}
	}

	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		r.logger.Logger().Info("connection state changed", zap.String("state", state.String()))
	})

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		r.handleTrack(recordCtx, st, track)
	})

	offerSDP, err := gatherOffer(ctx, peerConnection)
	if err != nil {
		return nil, err
	}

	req := recordingSessionRequest(params, offerSDP)

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rtcSessionCleanupTimeout)
		defer cancel()
		if err := r.sendDisconnect(cleanupCtx, params.EndpointID, params.SessionID); err != nil {
			r.logger.Logger().Warn("disconnect recording session", zap.Error(err))
		}
	}()
	response, err := r.apiClient.Messages().Send(ctx, req)
	if err != nil {
		r.logger.Logger().Error("failed to send initiate session", zap.Error(err))
		return nil, fmt.Errorf("send initiate session: %w", err)
	}
	if err = r.applyAnswer(peerConnection, response); err != nil {
		return nil, err
	}

	r.logger.Logger().Info("remote description set, waiting for recorded video")
	timer := time.NewTimer(frameWaitTimeout)
	defer timer.Stop()
	res, err := waitForRecording(ctx, st.startedCh, st.recordCh, timer.C)
	if err != nil {
		return nil, fmt.Errorf("recording video: %w", err)
	}
	result, err := st.finish(ctx, res)
	if err != nil {
		return nil, err
	}
	delivered = true
	return result, nil
}

func gatherOffer(ctx context.Context, pc *webrtc.PeerConnection) (string, error) {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("create offer: %w", err)
	}
	if err = pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set local description: %w", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(iceGatherTimeout):
		return "", fmt.Errorf("ICE gathering timeout")
	case <-gatherComplete:
	}
	return pc.LocalDescription().SDP, nil
}

func (r *Recorder) applyAnswer(pc *webrtc.PeerConnection, response *api.MessageRequest) error {
	answerSDP, err := extractAnswerSDP(response)
	if err != nil {
		r.logger.Logger().Error("failed to get answer from response", zap.Error(err))
		return fmt.Errorf("get answer from response: %w", err)
	}

	r.logger.Logger().Debug("setting remote description")
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}); err != nil {
		r.logger.Logger().Error("failed to set remote description", zap.Error(err))
		return fmt.Errorf("set remote description: %w", err)
	}
	return nil
}

func (r *Recorder) sendDisconnect(ctx context.Context, endpointID, sessionID string) error {
	msgType := rtcSessionNamespace + "/disconnect-session"
	req := &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{
			Type: api.EndpointTarget,
			Id:   endpointID,
		},
		Message: api.Message{
			Type: &msgType,
			Header: &api.MessageHeader{
				Namespace: rtcSessionNamespace,
				Name:      "disconnect-session",
				Version:   "1.0",
			},
			Body: map[string]interface{}{
				"sessionId": sessionID,
			},
		},
	}
	response, err := r.apiClient.Messages().Send(ctx, req)
	if err != nil {
		return err
	}
	return messageRequestFailure(response)
}

// extractAnswerSDP extracts the SDP string from the durable request outcomes when the response
// message is answer-generated-for-session (Body.answer.value may be string or map with "sdp").
func extractAnswerSDP(response *api.MessageRequest) (string, error) {
	if response == nil {
		return "", fmt.Errorf("response has no message request")
	}
	if response.Status != "SUCCEEDED" || len(response.Errors) > 0 {
		return "", fmt.Errorf("message request %s ended %s; recording outcome is unconfirmed", response.Id, response.Status)
	}
	var msg *api.Message
	for _, result := range response.Results {
		if result.Message == nil {
			continue
		}
		if msg != nil {
			return "", fmt.Errorf("message request has more than one response")
		}
		msg = result.Message
	}
	if msg == nil {
		return "", fmt.Errorf("response has no message")
	}

	if msg.Header == nil {
		return "", fmt.Errorf("response message has no header")
	}
	ns := msg.Header.Namespace
	name := msg.Header.Name
	if ns != rtcSessionNamespace || name != rtcAnswerGenerated {
		return "", fmt.Errorf("unexpected response message: %s/%s", ns, name)
	}
	body := msg.Body
	if body == nil {
		return "", fmt.Errorf("response message has no body")
	}
	answerVal, ok := body["answer"]
	if !ok {
		return "", fmt.Errorf("response body has no answer")
	}
	answerMap, ok := answerVal.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("answer is not an object")
	}
	value := answerMap["value"]
	if value == nil {
		return "", fmt.Errorf("answer has no value")
	}
	switch v := value.(type) {
	case string:
		return v, nil
	case map[string]interface{}:
		if s, ok := v["sdp"].(string); ok {
			return s, nil
		}
		return "", fmt.Errorf("answer.value has no sdp string")
	default:
		return "", fmt.Errorf("answer.value has unexpected type")
	}
}

func recordingSessionRequest(params RecorderParams, offerSDP string) *api.CreateMessageRequest {
	msgType := rtcSessionNamespace + "/" + rtcSessionInitiate
	return &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{
			Type: api.EndpointTarget,
			Id:   params.EndpointID,
		},
		Message: api.Message{
			Type: &msgType,
			Header: &api.MessageHeader{
				Namespace: rtcSessionNamespace,
				Name:      rtcSessionInitiate,
				Version:   "1.0",
			},
			Body: map[string]interface{}{
				"sessionId":     params.SessionID,
				"sessionDomain": params.SessionDomain,
				// The provider lease starts before its answer is delivered. Allow
				// remote ICE gathering as well as first-frame startup and capture.
				"sessionDuration": int(math.Ceil((params.Duration + iceGatherTimeout + frameWaitTimeout + rtcSessionCleanupTimeout).Seconds())),
				"offer": map[string]interface{}{
					"format": api.RtcOfferFormatSdp,
					"value":  offerSDP,
				},
			},
		},
		ResponseType: func() *api.CreateMessageRequestResponseType { t := api.SyncResponseType; return &t }(),
	}

}
