package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zalo/moonparty/internal/moonlight"
	"github.com/zalo/moonparty/internal/session"
	"github.com/zalo/moonparty/internal/webrtc"
)

// Server is the main Moonparty server
type Server struct {
	config     *Config
	httpServer *http.Server
	sessions   *session.Manager
	webrtc     *webrtc.Manager
	moonlight  *moonlight.Client
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// New creates a new Moonparty server
func New(cfg *Config) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize Moonlight client
	mlClient := moonlight.NewClient(cfg.SunshineHost, cfg.SunshinePort)

	// Delete existing identity if requested (useful when pairing is stuck)
	if cfg.ForceNewIdentity {
		log.Println("Forcing new client identity generation...")
		mlClient.DeleteIdentity()
	}

	// Initialize WebRTC manager
	webrtcMgr, err := webrtc.NewManager(cfg.ICEServers, cfg.TURNUsername, cfg.TURNCredential)
	if err != nil {
		cancel()
		return nil, err
	}

	// Initialize session manager
	sessionMgr := session.NewManager(cfg.MaxPlayers)

	s := &Server{
		config:    cfg,
		sessions:  sessionMgr,
		webrtc:    webrtcMgr,
		moonlight: mlClient,
		ctx:       ctx,
		cancel:    cancel,
	}

	// Setup HTTP routes
	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

func (s *Server) setupRoutes(mux *http.ServeMux) {
	// API routes
	mux.HandleFunc("/api/session/start", s.handleStartSession)
	mux.HandleFunc("/api/session/join", s.handleJoinSession)
	mux.HandleFunc("/api/session/status", s.handleSessionStatus)
	mux.HandleFunc("/api/session/leave", s.handleLeaveSession)
	mux.HandleFunc("/api/player/promote", s.handlePromotePlayer)
	mux.HandleFunc("/api/player/keyboard", s.handleToggleKeyboard)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/ice-servers", s.handleICEServers)

	// WebSocket for WebRTC signaling
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Serve static files from filesystem
	staticDir := findStaticDir()
	log.Printf("Serving static files from: %s", staticDir)
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))
}

// findStaticDir locates the web/static directory
func findStaticDir() string {
	// Try common locations
	paths := []string{
		"web/static",
		"../web/static",
		"../../web/static",
	}

	// Also try relative to executable
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(exeDir, "web/static"),
			filepath.Join(exeDir, "../web/static"),
		)
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}

	return "web/static" // Default fallback
}

// Run starts the server
func (s *Server) Run() error {
	// Try to pair with Sunshine on startup
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.moonlight.Connect(s.ctx); err != nil {
			log.Printf("Warning: Could not connect to Sunshine: %v", err)
			log.Println("You may need to pair with Sunshine first")
		}
	}()

	log.Printf("Server listening on %s", s.config.ListenAddr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() {
	s.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	s.sessions.CloseAll()
	s.webrtc.CloseAll()
	s.wg.Wait()
}

// API Handlers

func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if there's already an active session
	if s.sessions.HasActiveSession() {
		// Return existing session info for joining
		sess := s.sessions.GetActiveSession()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "existing",
			"session_id": sess.ID,
			"players":    sess.GetPlayerCount(),
			"spectators": sess.GetSpectatorCount(),
		})
		return
	}

	// Start a new streaming session
	sess, err := s.sessions.CreateSession()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Start streaming from Sunshine
	streamCtx, streamCancel := context.WithCancel(s.ctx)
	sess.SetCancelFunc(streamCancel)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.startStreaming(streamCtx, sess); err != nil {
			log.Printf("Streaming error: %v", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "created",
		"session_id": sess.ID,
	})
}

func (s *Server) handleJoinSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := s.sessions.GetActiveSession()
	if sess == nil {
		http.Error(w, "No active session", http.StatusNotFound)
		return
	}

	var req struct {
		Name     string `json:"name"`
		AsPlayer bool   `json:"as_player"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Name = "Anonymous"
		req.AsPlayer = false
	}

	// Add as spectator by default
	peer, err := sess.AddSpectator(req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "joined",
		"session_id": sess.ID,
		"peer_id":    peer.ID,
		"role":       "spectator",
		"players":    sess.GetPlayerCount(),
		"spectators": sess.GetSpectatorCount(),
	})
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	sess := s.sessions.GetActiveSession()
	if sess == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": false,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"active":     true,
		"session_id": sess.ID,
		"players":    sess.GetPlayers(),
		"spectators": sess.GetSpectatorCount(),
		"host":       sess.GetHost(),
	})
}

func (s *Server) handleLeaveSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PeerID string `json:"peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	sess := s.sessions.GetActiveSession()
	if sess == nil {
		http.Error(w, "No active session", http.StatusNotFound)
		return
	}

	sess.RemovePeer(req.PeerID)

	// If host left, close the session
	if sess.GetHost() == nil || sess.GetHost().ID == req.PeerID {
		s.sessions.CloseSession(sess.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "left",
	})
}

func (s *Server) handlePromotePlayer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PeerID string `json:"peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	sess := s.sessions.GetActiveSession()
	if sess == nil {
		http.Error(w, "No active session", http.StatusNotFound)
		return
	}

	slot, err := sess.PromoteToPlayer(req.PeerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "promoted",
		"player_slot":  slot,
		"gamepad_slot": slot,
	})
}

func (s *Server) handleToggleKeyboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PeerID  string `json:"peer_id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	sess := s.sessions.GetActiveSession()
	if sess == nil {
		http.Error(w, "No active session", http.StatusNotFound)
		return
	}

	sess.SetKeyboardEnabled(req.PeerID, req.Enabled)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "updated",
		"enabled": req.Enabled,
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.config.StreamSettings)
	case http.MethodPost:
		var settings StreamSettings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			http.Error(w, "Invalid settings", http.StatusBadRequest)
			return
		}
		s.config.StreamSettings = settings
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleICEServers(w http.ResponseWriter, r *http.Request) {
	servers := make([]map[string]interface{}, 0)
	for _, url := range s.config.ICEServers {
		server := map[string]interface{}{"urls": url}
		if s.config.TURNUsername != "" {
			server["username"] = s.config.TURNUsername
			server["credential"] = s.config.TURNCredential
		}
		servers = append(servers, server)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(servers)
}

// startStreaming initiates the video stream from Sunshine
func (s *Server) startStreaming(ctx context.Context, sess *session.Session) error {
	var stream moonlight.Streamer
	var err error

	// Choose streaming backend
	if s.config.UseLimelight {
		log.Println("Using moonlight-common-c (limelight) backend for streaming")
		stream, err = s.moonlight.StartStreamWithLimelight(ctx,
			s.config.StreamSettings.Width,
			s.config.StreamSettings.Height,
			s.config.StreamSettings.FPS,
			s.config.StreamSettings.Bitrate)
	} else {
		log.Println("Using native Go streaming backend")
		stream, err = s.moonlight.StartStream(ctx,
			s.config.StreamSettings.Width,
			s.config.StreamSettings.Height,
			s.config.StreamSettings.FPS,
			s.config.StreamSettings.Bitrate)
	}

	if err != nil {
		return err
	}
	defer stream.Close()

	// Fan out video/audio to all connected peers
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame := <-stream.VideoFrames():
			// Broadcast video frame to all peers
			s.broadcastVideo(sess, frame)
		case sample := <-stream.AudioSamples():
			// Broadcast audio sample to all peers
			s.broadcastAudio(sess, sample)
		case input := <-sess.InputChannel():
			// Forward input to Sunshine
			stream.SendInput(input)
		}
	}
}

func (s *Server) broadcastVideo(sess *session.Session, frame []byte) {
	peers := sess.GetAllPeers()
	for _, peer := range peers {
		if pc := s.webrtc.GetPeerConnection(peer.ID); pc != nil {
			pc.SendVideo(frame)
		}
	}
}

func (s *Server) broadcastAudio(sess *session.Session, sample []byte) {
	peers := sess.GetAllPeers()
	for _, peer := range peers {
		if pc := s.webrtc.GetPeerConnection(peer.ID); pc != nil {
			pc.SendAudio(sample)
		}
	}
}
