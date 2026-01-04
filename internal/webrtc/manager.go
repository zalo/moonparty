package webrtc

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/pion/webrtc/v4"
)

// Manager manages WebRTC peer connections
type Manager struct {
	mu          sync.RWMutex
	api         *webrtc.API
	config      webrtc.Configuration
	connections map[string]*PeerConnection
}

// NewManager creates a new WebRTC manager
func NewManager(iceServers []string, turnUsername, turnCredential string) (*Manager, error) {
	// Build ICE server configuration
	servers := make([]webrtc.ICEServer, 0, len(iceServers))
	for _, url := range iceServers {
		server := webrtc.ICEServer{URLs: []string{url}}
		if turnUsername != "" && (len(url) > 4 && url[:4] == "turn") {
			server.Username = turnUsername
			server.Credential = turnCredential
		}
		servers = append(servers, server)
	}

	config := webrtc.Configuration{
		ICEServers: servers,
	}

	// Create MediaEngine with codec support
	m := &webrtc.MediaEngine{}

	// Register H.264 codec for video
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	// Register Opus codec for audio
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	// Create API with custom MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))

	return &Manager{
		api:         api,
		config:      config,
		connections: make(map[string]*PeerConnection),
	}, nil
}

// CreatePeerConnection creates a new peer connection for a client
func (m *Manager) CreatePeerConnection(peerID string) (*PeerConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create the underlying WebRTC peer connection
	pc, err := m.api.NewPeerConnection(m.config)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %w", err)
	}

	conn := &PeerConnection{
		id:         peerID,
		pc:         pc,
		videoTrack: nil,
		audioTrack: nil,
	}

	// Set up connection state handler
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Peer %s connection state: %s", peerID, state.String())
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			m.RemovePeerConnection(peerID)
		}
	})

	// Set up ICE connection state handler
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("Peer %s ICE state: %s", peerID, state.String())
	})

	m.connections[peerID] = conn
	return conn, nil
}

// GetPeerConnection returns an existing peer connection
func (m *Manager) GetPeerConnection(peerID string) *PeerConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.connections[peerID]
}

// RemovePeerConnection closes and removes a peer connection
func (m *Manager) RemovePeerConnection(peerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn, ok := m.connections[peerID]; ok {
		conn.Close()
		delete(m.connections, peerID)
	}
}

// CloseAll closes all peer connections
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, conn := range m.connections {
		conn.Close()
	}
	m.connections = make(map[string]*PeerConnection)
}

// BroadcastVideo sends video data to all connected peers
func (m *Manager) BroadcastVideo(data []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		conn.SendVideo(data)
	}
}

// BroadcastAudio sends audio data to all connected peers
func (m *Manager) BroadcastAudio(data []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		conn.SendAudio(data)
	}
}

// PeerConnection wraps a WebRTC peer connection
type PeerConnection struct {
	id         string
	pc         *webrtc.PeerConnection
	videoTrack *webrtc.TrackLocalStaticRTP
	audioTrack *webrtc.TrackLocalStaticRTP
	dataChans  map[string]*webrtc.DataChannel
	mu         sync.Mutex

	// Callbacks
	OnInput func(channelID string, data []byte)
}

// SetupTracks initializes video and audio tracks for sending
func (p *PeerConnection) SetupTracks() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Create video track
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"moonparty-video",
	)
	if err != nil {
		return fmt.Errorf("failed to create video track: %w", err)
	}

	if _, err := p.pc.AddTrack(videoTrack); err != nil {
		return fmt.Errorf("failed to add video track: %w", err)
	}
	p.videoTrack = videoTrack

	// Create audio track
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio",
		"moonparty-audio",
	)
	if err != nil {
		return fmt.Errorf("failed to create audio track: %w", err)
	}

	if _, err := p.pc.AddTrack(audioTrack); err != nil {
		return fmt.Errorf("failed to add audio track: %w", err)
	}
	p.audioTrack = audioTrack

	return nil
}

// SetupDataChannels creates data channels for input
func (p *PeerConnection) SetupDataChannels() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.dataChans = make(map[string]*webrtc.DataChannel)

	// Create ordered reliable channel for control messages
	controlDC, err := p.pc.CreateDataChannel("control", &webrtc.DataChannelInit{
		Ordered: boolPtr(true),
	})
	if err != nil {
		return err
	}
	p.dataChans["control"] = controlDC

	// Create unordered unreliable channel for gamepad input (low latency)
	inputDC, err := p.pc.CreateDataChannel("input", &webrtc.DataChannelInit{
		Ordered:        boolPtr(false),
		MaxRetransmits: uint16Ptr(0),
	})
	if err != nil {
		return err
	}
	p.dataChans["input"] = inputDC

	// Set up message handlers
	controlDC.OnMessage(func(msg webrtc.DataChannelMessage) {
		if p.OnInput != nil {
			p.OnInput("control", msg.Data)
		}
	})

	inputDC.OnMessage(func(msg webrtc.DataChannelMessage) {
		if p.OnInput != nil {
			p.OnInput("input", msg.Data)
		}
	})

	return nil
}

// HandleOffer processes an SDP offer and returns an answer
func (p *PeerConnection) HandleOffer(offerSDP string) (string, error) {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP,
	}

	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set remote description: %w", err)
	}

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create answer: %w", err)
	}

	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	<-gatherComplete

	return p.pc.LocalDescription().SDP, nil
}

// CreateOffer creates an SDP offer
func (p *PeerConnection) CreateOffer() (string, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create offer: %w", err)
	}

	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	<-gatherComplete

	return p.pc.LocalDescription().SDP, nil
}

// HandleAnswer processes an SDP answer
func (p *PeerConnection) HandleAnswer(answerSDP string) error {
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}

	return p.pc.SetRemoteDescription(answer)
}

// AddICECandidate adds an ICE candidate
func (p *PeerConnection) AddICECandidate(candidateJSON string) error {
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal([]byte(candidateJSON), &candidate); err != nil {
		return err
	}
	return p.pc.AddICECandidate(candidate)
}

// OnICECandidate sets a callback for new ICE candidates
func (p *PeerConnection) OnICECandidate(fn func(candidate string)) {
	p.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			candidateJSON, _ := json.Marshal(c.ToJSON())
			fn(string(candidateJSON))
		}
	})
}

// SendVideo sends video RTP data
func (p *PeerConnection) SendVideo(data []byte) error {
	p.mu.Lock()
	track := p.videoTrack
	p.mu.Unlock()

	if track == nil {
		return nil
	}

	_, err := track.Write(data)
	return err
}

// SendAudio sends audio RTP data
func (p *PeerConnection) SendAudio(data []byte) error {
	p.mu.Lock()
	track := p.audioTrack
	p.mu.Unlock()

	if track == nil {
		return nil
	}

	_, err := track.Write(data)
	return err
}

// SendControl sends a control message
func (p *PeerConnection) SendControl(data []byte) error {
	p.mu.Lock()
	dc := p.dataChans["control"]
	p.mu.Unlock()

	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
		return nil
	}

	return dc.Send(data)
}

// Close closes the peer connection
func (p *PeerConnection) Close() error {
	return p.pc.Close()
}

// Helper functions
func boolPtr(b bool) *bool {
	return &b
}

func uint16Ptr(n uint16) *uint16 {
	return &n
}
