// Package session manages the lifecycle of agent sessions, including event
// buffering, SSE streaming, and question/permission handling.
package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bborn/workflow/extensions/ty-sandbox/internal/agent"
	"github.com/bborn/workflow/extensions/ty-sandbox/internal/events"
	"github.com/google/uuid"
)

// SessionInfo describes the state of a session.
type SessionInfo struct {
	ID        string       `json:"id"`
	Agent     agent.AgentID `json:"agent"`
	Model     string       `json:"model,omitempty"`
	Status    string       `json:"status"` // "active", "ended"
	CreatedAt time.Time    `json:"created_at"`
	EndedAt   *time.Time   `json:"ended_at,omitempty"`
}

// Session holds the state for a single agent session.
type Session struct {
	mu        sync.RWMutex
	info      SessionInfo
	events    []*events.UniversalEvent
	eventCh   chan *events.UniversalEvent
	listeners []chan *events.UniversalEvent
	questions map[string]*pendingQuestion
	perms     map[string]*pendingPermission
	cancel    context.CancelFunc
}

type pendingQuestion struct {
	event  *events.QuestionEventData
	replyCh chan string
}

type pendingPermission struct {
	event  *events.PermissionEventData
	replyCh chan bool
}

// Manager coordinates sessions across agent adapters.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	registry *agent.Registry
}

// NewManager creates a new session manager.
func NewManager(registry *agent.Registry) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		registry: registry,
	}
}

// CreateSession starts a new agent session.
func (m *Manager) CreateSession(ctx context.Context, sessionID string, cfg agent.SpawnConfig) (*SessionInfo, error) {
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	m.mu.Lock()
	if _, exists := m.sessions[sessionID]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("session already exists: %s", sessionID)
	}
	m.mu.Unlock()

	a, err := m.registry.Get(cfg.Agent)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	eventCh := make(chan *events.UniversalEvent, 256)

	sess := &Session{
		info: SessionInfo{
			ID:        sessionID,
			Agent:     cfg.Agent,
			Model:     cfg.Model,
			Status:    "active",
			CreatedAt: time.Now(),
		},
		eventCh:   eventCh,
		questions: make(map[string]*pendingQuestion),
		perms:     make(map[string]*pendingPermission),
		cancel:    cancel,
	}

	m.mu.Lock()
	m.sessions[sessionID] = sess
	m.mu.Unlock()

	// Start event consumer goroutine
	go m.consumeEvents(sess)

	// Spawn the agent
	if err := a.Spawn(ctx, sessionID, cfg, eventCh); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.sessions, sessionID)
		m.mu.Unlock()
		return nil, fmt.Errorf("spawn agent: %w", err)
	}

	return &sess.info, nil
}

// consumeEvents reads events from the agent and distributes to listeners.
func (m *Manager) consumeEvents(sess *Session) {
	for evt := range sess.eventCh {
		sess.mu.Lock()
		sess.events = append(sess.events, evt)

		// Distribute to all SSE listeners
		for i := len(sess.listeners) - 1; i >= 0; i-- {
			select {
			case sess.listeners[i] <- evt:
			default:
				// Listener is full, remove it
				sess.listeners = append(sess.listeners[:i], sess.listeners[i+1:]...)
			}
		}

		// Mark session as ended when we get a session.ended event
		if evt.Type == events.EventSessionEnded {
			sess.info.Status = "ended"
			now := time.Now()
			sess.info.EndedAt = &now
			// Close all listeners
			for _, l := range sess.listeners {
				close(l)
			}
			sess.listeners = nil
		}

		sess.mu.Unlock()
	}
}

// SendMessage sends a message to an active session.
func (m *Manager) SendMessage(ctx context.Context, sessionID string, message string) error {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return err
	}

	sess.mu.RLock()
	if sess.info.Status != "active" {
		sess.mu.RUnlock()
		return fmt.Errorf("session is not active: %s", sessionID)
	}
	agentID := sess.info.Agent
	sess.mu.RUnlock()

	a, err := m.registry.Get(agentID)
	if err != nil {
		return err
	}

	return a.SendMessage(ctx, sessionID, message)
}

// TerminateSession stops an active session.
func (m *Manager) TerminateSession(ctx context.Context, sessionID string) error {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return err
	}

	sess.mu.RLock()
	agentID := sess.info.Agent
	sess.mu.RUnlock()

	a, err := m.registry.Get(agentID)
	if err != nil {
		return err
	}

	return a.Terminate(ctx, sessionID)
}

// GetEvents returns all events for a session, optionally filtered by sequence.
func (m *Manager) GetEvents(sessionID string, afterSeq *uint64) ([]*events.UniversalEvent, error) {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	sess.mu.RLock()
	defer sess.mu.RUnlock()

	if afterSeq == nil {
		result := make([]*events.UniversalEvent, len(sess.events))
		copy(result, sess.events)
		return result, nil
	}

	var result []*events.UniversalEvent
	for _, e := range sess.events {
		if e.Sequence > *afterSeq {
			result = append(result, e)
		}
	}
	return result, nil
}

// SubscribeEvents returns a channel that receives new events for a session.
// The channel is closed when the session ends.
func (m *Manager) SubscribeEvents(sessionID string) (<-chan *events.UniversalEvent, error) {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	ch := make(chan *events.UniversalEvent, 64)

	sess.mu.Lock()
	defer sess.mu.Unlock()

	// Send buffered events first
	go func() {
		sess.mu.RLock()
		buffered := make([]*events.UniversalEvent, len(sess.events))
		copy(buffered, sess.events)
		sess.mu.RUnlock()

		for _, e := range buffered {
			ch <- e
		}
	}()

	if sess.info.Status == "ended" {
		go func() {
			// Give time for buffered events to be sent
			time.Sleep(100 * time.Millisecond)
			close(ch)
		}()
		return ch, nil
	}

	sess.listeners = append(sess.listeners, ch)
	return ch, nil
}

// ReplyQuestion answers a pending HITL question.
func (m *Manager) ReplyQuestion(sessionID, questionID, answer string) error {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return err
	}

	sess.mu.Lock()
	q, ok := sess.questions[questionID]
	if !ok {
		sess.mu.Unlock()
		return fmt.Errorf("question not found: %s", questionID)
	}
	delete(sess.questions, questionID)
	sess.mu.Unlock()

	q.replyCh <- answer
	return nil
}

// RejectQuestion rejects a pending HITL question.
func (m *Manager) RejectQuestion(sessionID, questionID string) error {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return err
	}

	sess.mu.Lock()
	q, ok := sess.questions[questionID]
	if !ok {
		sess.mu.Unlock()
		return fmt.Errorf("question not found: %s", questionID)
	}
	delete(sess.questions, questionID)
	sess.mu.Unlock()

	close(q.replyCh)
	return nil
}

// ReplyPermission approves or denies a pending tool permission.
func (m *Manager) ReplyPermission(sessionID, permissionID string, allow bool) error {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return err
	}

	sess.mu.Lock()
	p, ok := sess.perms[permissionID]
	if !ok {
		sess.mu.Unlock()
		return fmt.Errorf("permission not found: %s", permissionID)
	}
	delete(sess.perms, permissionID)
	sess.mu.Unlock()

	p.replyCh <- allow
	return nil
}

// ListSessions returns info for all sessions.
func (m *Manager) ListSessions() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var infos []SessionInfo
	for _, s := range m.sessions {
		s.mu.RLock()
		infos = append(infos, s.info)
		s.mu.RUnlock()
	}
	return infos
}

// GetSessionInfo returns info for a single session.
func (m *Manager) GetSessionInfo(sessionID string) (*SessionInfo, error) {
	sess, err := m.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	info := sess.info
	return &info, nil
}

func (m *Manager) getSession(sessionID string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return sess, nil
}
