package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/zalo/moonparty/internal/moonlight"
	"github.com/zalo/moonparty/internal/session"
	mwebrtc "github.com/zalo/moonparty/internal/webrtc"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for simplicity
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// WebSocket message types
type WSMessageType string

const (
	// Client -> Server
	WSMsgOffer        WSMessageType = "offer"
	WSMsgAnswer       WSMessageType = "answer"
	WSMsgCandidate    WSMessageType = "candidate"
	WSMsgInput        WSMessageType = "input"
	WSMsgJoinAsPlayer WSMessageType = "join_as_player"
	WSMsgLeave        WSMessageType = "leave"

	// Server -> Client
	WSMsgSessionInfo  WSMessageType = "session_info"
	WSMsgPlayerSlot   WSMessageType = "player_slot"
	WSMsgPeerJoined   WSMessageType = "peer_joined"
	WSMsgPeerLeft     WSMessageType = "peer_left"
	WSMsgError        WSMessageType = "error"
	WSMsgICECandidate WSMessageType = "ice_candidate"
)

// WSMessage is the WebSocket message envelope
type WSMessage struct {
	Type    WSMessageType   `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// InputPayload represents input data from the client
type InputPayload struct {
	InputType string `json:"input_type"` // "keyboard", "mouse", "mouse_rel", "gamepad"
	Data      []byte `json:"data"`
}

// wsClient represents a connected WebSocket client
type wsClient struct {
	conn    *websocket.Conn
	peerID  string
	send    chan []byte
	server  *Server
	mu      sync.Mutex
	closed  bool
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	// Get or create session
	sess := s.sessions.GetActiveSession()
	if sess == nil {
		// No active session - this client will be the host
		sess, err = s.sessions.CreateSession()
		if err != nil {
			conn.WriteJSON(WSMessage{Type: WSMsgError, Payload: jsonRaw(map[string]string{"error": err.Error()})})
			conn.Close()
			return
		}

		// Start streaming
		go func() {
			if err := s.startStreaming(s.ctx, sess); err != nil {
				log.Printf("Streaming error: %v", err)
			}
		}()
	}

	// Determine if this is a new player or joining existing session
	var peer *session.Peer
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "Player"
	}

	host := sess.GetHost()
	if host != nil {
		// Subsequent connections are spectators
		peer, err = sess.AddSpectator(name)
		if err != nil {
			conn.WriteJSON(WSMessage{Type: WSMsgError, Payload: jsonRaw(map[string]string{"error": err.Error()})})
			conn.Close()
			return
		}
	} else {
		// First connection is the host (already added by CreateSession)
		peer = sess.GetHost()
	}

	if peer == nil {
		conn.WriteJSON(WSMessage{Type: WSMsgError, Payload: jsonRaw(map[string]string{"error": "failed to get peer"})})
		conn.Close()
		return
	}

	client := &wsClient{
		conn:   conn,
		peerID: peer.ID,
		send:   make(chan []byte, 256),
		server: s,
	}

	// Create WebRTC peer connection
	pc, err := s.webrtc.CreatePeerConnection(peer.ID)
	if err != nil {
		log.Printf("Failed to create peer connection: %v", err)
		conn.Close()
		return
	}

	// Setup tracks and data channels
	if err := pc.SetupTracks(); err != nil {
		log.Printf("Failed to setup tracks: %v", err)
		conn.Close()
		return
	}

	if err := pc.SetupDataChannels(); err != nil {
		log.Printf("Failed to setup data channels: %v", err)
		conn.Close()
		return
	}

	// Handle input from this peer
	pc.OnInput = func(channelID string, data []byte) {
		s.handlePeerInput(peer.ID, channelID, data)
	}

	// Forward ICE candidates to client
	pc.OnICECandidate(func(candidate string) {
		client.sendJSON(WSMessage{
			Type:    WSMsgICECandidate,
			Payload: jsonRaw(map[string]string{"candidate": candidate}),
		})
	})

	// Send session info to client
	client.sendJSON(WSMessage{
		Type: WSMsgSessionInfo,
		Payload: jsonRaw(map[string]interface{}{
			"session_id": sess.ID,
			"peer_id":    peer.ID,
			"role":       peer.Role,
			"slot":       peer.PlayerSlot,
			"players":    sess.GetPlayers(),
			"is_host":    peer.Role == session.RoleHost,
		}),
	})

	// Start client handlers
	go client.writePump()
	go client.readPump(sess, peer, pc)
}

func (c *wsClient) readPump(sess *session.Session, peer *session.Peer, pc *mwebrtc.PeerConnection) {
	defer func() {
		if activeSess := c.server.sessions.GetActiveSession(); activeSess != nil {
			activeSess.RemovePeer(c.peerID)
		}
		c.server.webrtc.RemovePeerConnection(c.peerID)
		c.conn.Close()
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Invalid message: %v", err)
			continue
		}

		c.handleMessage(msg, sess, peer, pc)
	}
}

func (c *wsClient) handleMessage(msg WSMessage, sess *session.Session, peer *session.Peer, pc *mwebrtc.PeerConnection) {
	switch msg.Type {
	case WSMsgOffer:
		var payload struct {
			SDP string `json:"sdp"`
		}
		json.Unmarshal(msg.Payload, &payload)

		answer, err := pc.HandleOffer(payload.SDP)
		if err != nil {
			c.sendJSON(WSMessage{Type: WSMsgError, Payload: jsonRaw(map[string]string{"error": err.Error()})})
			return
		}

		c.sendJSON(WSMessage{
			Type:    WSMsgAnswer,
			Payload: jsonRaw(map[string]string{"sdp": answer}),
		})

	case WSMsgAnswer:
		var payload struct {
			SDP string `json:"sdp"`
		}
		json.Unmarshal(msg.Payload, &payload)

		if err := pc.HandleAnswer(payload.SDP); err != nil {
			c.sendJSON(WSMessage{Type: WSMsgError, Payload: jsonRaw(map[string]string{"error": err.Error()})})
		}

	case WSMsgCandidate:
		var payload struct {
			Candidate string `json:"candidate"`
		}
		json.Unmarshal(msg.Payload, &payload)

		if err := pc.AddICECandidate(payload.Candidate); err != nil {
			log.Printf("Failed to add ICE candidate: %v", err)
		}

	case WSMsgInput:
		var payload InputPayload
		json.Unmarshal(msg.Payload, &payload)

		c.server.handlePeerInput(peer.ID, payload.InputType, payload.Data)

	case WSMsgJoinAsPlayer:
		slot, err := sess.PromoteToPlayer(peer.ID)
		if err != nil {
			c.sendJSON(WSMessage{Type: WSMsgError, Payload: jsonRaw(map[string]string{"error": err.Error()})})
			return
		}

		c.sendJSON(WSMessage{
			Type:    WSMsgPlayerSlot,
			Payload: jsonRaw(map[string]int{"slot": slot}),
		})

		// Broadcast to others
		c.server.broadcastSessionUpdate(sess)

	case WSMsgLeave:
		sess.RemovePeer(peer.ID)
		c.server.broadcastSessionUpdate(sess)
	}
}

func (c *wsClient) writePump() {
	defer c.conn.Close()

	for message := range c.send {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}

		if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
}

func (c *wsClient) sendJSON(msg WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	select {
	case c.send <- data:
	default:
		// Buffer full, close connection
		c.closed = true
		close(c.send)
	}
}

func (s *Server) handlePeerInput(peerID, inputType string, data []byte) {
	sess := s.sessions.GetActiveSession()
	if sess == nil {
		return
	}

	// Determine input type
	var iType moonlight.InputType
	switch inputType {
	case "keyboard":
		iType = moonlight.InputTypeKeyboard
	case "mouse":
		iType = moonlight.InputTypeMouse
	case "mouse_rel":
		iType = moonlight.InputTypeMouseRelative
	case "gamepad", "input":
		iType = moonlight.InputTypeGamepad
	default:
		return
	}

	// Check if peer can send this input type
	if !sess.CanSendInput(peerID, iType) {
		return
	}

	// Get player slot for gamepad mapping
	slot := sess.GetPlayerSlot(peerID)
	if slot < 0 {
		return
	}

	// Queue input for sending to Sunshine
	sess.SendInput(moonlight.InputPacket{
		Type:       iType,
		PeerID:     peerID,
		PlayerSlot: slot,
		Data:       data,
	})
}

func (s *Server) broadcastSessionUpdate(sess *session.Session) {
	// This would broadcast to all connected WebSocket clients
	// Implementation depends on maintaining a list of all ws clients
}

func jsonRaw(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
