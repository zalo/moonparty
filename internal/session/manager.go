package session

import (
	"errors"
	"sync"
)

// Manager manages all active sessions
type Manager struct {
	mu         sync.RWMutex
	sessions   map[string]*Session
	active     *Session // Currently only one session at a time
	maxPlayers int
}

// NewManager creates a new session manager
func NewManager(maxPlayers int) *Manager {
	if maxPlayers <= 0 || maxPlayers > 4 {
		maxPlayers = 4
	}

	return &Manager{
		sessions:   make(map[string]*Session),
		maxPlayers: maxPlayers,
	}
}

// CreateSession creates a new streaming session
func (m *Manager) CreateSession() (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active != nil {
		return nil, errors.New("a session is already active")
	}

	sess := NewSession(m.maxPlayers)
	m.sessions[sess.ID] = sess
	m.active = sess

	// Add host as Player 1
	_, err := sess.AddHost("Host")
	if err != nil {
		delete(m.sessions, sess.ID)
		m.active = nil
		return nil, err
	}

	return sess, nil
}

// GetSession returns a session by ID
func (m *Manager) GetSession(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.sessions[id]
}

// GetActiveSession returns the current active session
func (m *Manager) GetActiveSession() *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.active
}

// HasActiveSession checks if there's an active session
func (m *Manager) HasActiveSession() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.active != nil
}

// CloseSession terminates and removes a session
func (m *Manager) CloseSession(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[id]
	if !ok {
		return
	}

	sess.Close()
	delete(m.sessions, id)

	if m.active != nil && m.active.ID == id {
		m.active = nil
	}
}

// CloseAll terminates all sessions
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sess := range m.sessions {
		sess.Close()
	}

	m.sessions = make(map[string]*Session)
	m.active = nil
}

// ListSessions returns all active sessions
func (m *Manager) ListSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}
