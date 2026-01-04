package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zalo/moonparty/internal/moonlight"
)

// Role represents a participant's role in the session
type Role string

const (
	RoleHost      Role = "host"
	RolePlayer    Role = "player"
	RoleSpectator Role = "spectator"
)

// Peer represents a connected participant
type Peer struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Role            Role      `json:"role"`
	PlayerSlot      int       `json:"player_slot"` // 0-3 for players, -1 for spectators
	JoinedAt        time.Time `json:"joined_at"`
	KeyboardEnabled bool      `json:"keyboard_enabled"` // Only host can toggle this for other players
}

// Session represents an active streaming session
type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`

	mu         sync.RWMutex
	peers      map[string]*Peer
	playerSlot [4]*Peer // Fixed 4 player slots
	host       *Peer
	cancelFunc context.CancelFunc
	inputChan  chan moonlight.InputPacket
	maxPlayers int

	// Callbacks for session events
	onPeerJoined   func(*Peer)
	onPeerLeft     func(*Peer)
	onRoleChanged  func(*Peer, Role)
}

// NewSession creates a new streaming session
func NewSession(maxPlayers int) *Session {
	return &Session{
		ID:         uuid.New().String()[:8], // Short ID for easy sharing
		CreatedAt:  time.Now(),
		peers:      make(map[string]*Peer),
		inputChan:  make(chan moonlight.InputPacket, 256),
		maxPlayers: maxPlayers,
	}
}

// AddHost adds the first user as the host (Player 1)
func (s *Session) AddHost(name string) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.host != nil {
		return nil, errors.New("session already has a host")
	}

	peer := &Peer{
		ID:              uuid.New().String(),
		Name:           name,
		Role:           RoleHost,
		PlayerSlot:     0,
		JoinedAt:       time.Now(),
		KeyboardEnabled: true, // Host always has keyboard
	}

	s.peers[peer.ID] = peer
	s.playerSlot[0] = peer
	s.host = peer

	if s.onPeerJoined != nil {
		go s.onPeerJoined(peer)
	}

	return peer, nil
}

// AddSpectator adds a new spectator to the session
func (s *Session) AddSpectator(name string) (*Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	peer := &Peer{
		ID:              uuid.New().String(),
		Name:           name,
		Role:           RoleSpectator,
		PlayerSlot:     -1,
		JoinedAt:       time.Now(),
		KeyboardEnabled: false,
	}

	s.peers[peer.ID] = peer

	if s.onPeerJoined != nil {
		go s.onPeerJoined(peer)
	}

	return peer, nil
}

// PromoteToPlayer promotes a spectator to an active player
func (s *Session) PromoteToPlayer(peerID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	peer, ok := s.peers[peerID]
	if !ok {
		return -1, errors.New("peer not found")
	}

	if peer.Role == RoleHost || peer.Role == RolePlayer {
		return peer.PlayerSlot, nil // Already a player
	}

	// Find an available slot (1-3, since 0 is host)
	slot := -1
	for i := 1; i < s.maxPlayers && i < 4; i++ {
		if s.playerSlot[i] == nil {
			slot = i
			break
		}
	}

	if slot == -1 {
		return -1, errors.New("no player slots available")
	}

	peer.Role = RolePlayer
	peer.PlayerSlot = slot
	s.playerSlot[slot] = peer

	if s.onRoleChanged != nil {
		go s.onRoleChanged(peer, RolePlayer)
	}

	return slot, nil
}

// DemoteToSpectator demotes a player back to spectator
func (s *Session) DemoteToSpectator(peerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	peer, ok := s.peers[peerID]
	if !ok {
		return errors.New("peer not found")
	}

	if peer.Role == RoleHost {
		return errors.New("cannot demote host")
	}

	if peer.Role == RoleSpectator {
		return nil // Already a spectator
	}

	// Free the slot
	if peer.PlayerSlot >= 0 && peer.PlayerSlot < 4 {
		s.playerSlot[peer.PlayerSlot] = nil
	}

	peer.Role = RoleSpectator
	peer.PlayerSlot = -1
	peer.KeyboardEnabled = false

	if s.onRoleChanged != nil {
		go s.onRoleChanged(peer, RoleSpectator)
	}

	return nil
}

// RemovePeer removes a peer from the session
func (s *Session) RemovePeer(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	peer, ok := s.peers[peerID]
	if !ok {
		return
	}

	// Free player slot if applicable
	if peer.PlayerSlot >= 0 && peer.PlayerSlot < 4 {
		s.playerSlot[peer.PlayerSlot] = nil
	}

	delete(s.peers, peerID)

	if s.onPeerLeft != nil {
		go s.onPeerLeft(peer)
	}
}

// SetKeyboardEnabled toggles keyboard input for a player
func (s *Session) SetKeyboardEnabled(peerID string, enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	peer, ok := s.peers[peerID]
	if !ok {
		return
	}

	// Only non-host players can have keyboard toggled
	if peer.Role == RoleHost {
		return
	}

	peer.KeyboardEnabled = enabled
}

// GetPeer returns a peer by ID
func (s *Session) GetPeer(peerID string) *Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.peers[peerID]
}

// GetHost returns the session host
func (s *Session) GetHost() *Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.host
}

// GetPlayers returns all active players
func (s *Session) GetPlayers() []*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	players := make([]*Peer, 0, 4)
	for _, p := range s.playerSlot {
		if p != nil {
			players = append(players, p)
		}
	}
	return players
}

// GetPlayerCount returns the number of active players
func (s *Session) GetPlayerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, p := range s.playerSlot {
		if p != nil {
			count++
		}
	}
	return count
}

// GetSpectatorCount returns the number of spectators
func (s *Session) GetSpectatorCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, p := range s.peers {
		if p.Role == RoleSpectator {
			count++
		}
	}
	return count
}

// GetAllPeers returns all connected peers
func (s *Session) GetAllPeers() []*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	return peers
}

// InputChannel returns the channel for input packets
func (s *Session) InputChannel() <-chan moonlight.InputPacket {
	return s.inputChan
}

// SendInput queues an input packet for sending to Sunshine
func (s *Session) SendInput(input moonlight.InputPacket) {
	select {
	case s.inputChan <- input:
	default:
		// Drop input if buffer is full
	}
}

// SetCancelFunc sets the cancel function for the stream
func (s *Session) SetCancelFunc(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelFunc = cancel
}

// Close terminates the session
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	close(s.inputChan)
}

// OnPeerJoined sets a callback for peer join events
func (s *Session) OnPeerJoined(fn func(*Peer)) {
	s.onPeerJoined = fn
}

// OnPeerLeft sets a callback for peer leave events
func (s *Session) OnPeerLeft(fn func(*Peer)) {
	s.onPeerLeft = fn
}

// OnRoleChanged sets a callback for role change events
func (s *Session) OnRoleChanged(fn func(*Peer, Role)) {
	s.onRoleChanged = fn
}

// CanSendInput checks if a peer can send the given input type
func (s *Session) CanSendInput(peerID string, inputType moonlight.InputType) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	peer, ok := s.peers[peerID]
	if !ok {
		return false
	}

	// Spectators cannot send any input
	if peer.Role == RoleSpectator {
		return false
	}

	// Check input type permissions
	switch inputType {
	case moonlight.InputTypeKeyboard, moonlight.InputTypeMouse, moonlight.InputTypeMouseRelative:
		// Only host or players with keyboard enabled
		return peer.Role == RoleHost || peer.KeyboardEnabled
	case moonlight.InputTypeGamepad:
		// All players can send gamepad
		return peer.Role == RoleHost || peer.Role == RolePlayer
	default:
		return false
	}
}

// GetPlayerSlot returns the gamepad slot for a peer's input
func (s *Session) GetPlayerSlot(peerID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	peer, ok := s.peers[peerID]
	if !ok {
		return -1
	}

	return peer.PlayerSlot
}
