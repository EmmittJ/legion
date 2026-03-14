package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// SessionStore is a pluggable storage interface for managing session data.
//
// When configured via [WithSessionStore], the connection layer automatically
// handles NewSession, LoadSession, and ListSessions requests using this store,
// so the Agent implementation does not need to implement those methods.
//
// Implementations must be safe for concurrent use.
type SessionStore[T any] interface {
	// Get retrieves a session by its ID. Returns false if not found.
	Get(id SessionID) (T, bool)

	// Set stores a session with the given ID, replacing any existing session.
	Set(id SessionID, session T)

	// Delete removes a session by its ID.
	Delete(id SessionID)

	// List returns all stored session IDs.
	List() []SessionID
}

// MemoryStore is an in-memory implementation of [SessionStore].
//
// Safe for concurrent use. Sessions are lost when the process exits.
type MemoryStore[T any] struct {
	mu       sync.RWMutex
	sessions map[SessionID]T
}

// NewMemoryStore creates a new in-memory session store.
func NewMemoryStore[T any]() *MemoryStore[T] {
	return &MemoryStore[T]{
		sessions: make(map[SessionID]T),
	}
}

func (s *MemoryStore[T]) Get(id SessionID) (T, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *MemoryStore[T]) Set(id SessionID, session T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = session
}

func (s *MemoryStore[T]) Delete(id SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *MemoryStore[T]) List() []SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]SessionID, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	return ids
}

// sessionStoreHandler is a type-erased wrapper that handles session RPC methods
// using a SessionStore. This allows AgentSideConnection to work with any session
// store without being generic itself.
type sessionStoreHandler interface {
	handleNewSession(ctx context.Context, params *NewSessionRequest) (*NewSessionResponse, error)
	handleLoadSession(ctx context.Context, params *LoadSessionRequest) (*LoadSessionResponse, error)
	handleListSessions(ctx context.Context, params *ListSessionsRequest) (*ListSessionsResponse, error)
}

// SessionFactory creates a new session value and its ID when a NewSession request is received.
// Use [GenerateSessionID] for default random ID generation, or provide your own ID scheme.
type SessionFactory[T any] func(ctx context.Context, params *NewSessionRequest) (SessionID, T, error)

// sessionStoreAdapter adapts a typed SessionStore into a sessionStoreHandler.
type sessionStoreAdapter[T any] struct {
	store   SessionStore[T]
	factory SessionFactory[T]
}

func (a *sessionStoreAdapter[T]) handleNewSession(ctx context.Context, params *NewSessionRequest) (*NewSessionResponse, error) {
	id, session, err := a.factory(ctx, params)
	if err != nil {
		return nil, err
	}
	a.store.Set(id, session)
	return &NewSessionResponse{SessionID: id}, nil
}

func (a *sessionStoreAdapter[T]) handleLoadSession(_ context.Context, params *LoadSessionRequest) (*LoadSessionResponse, error) {
	if _, ok := a.store.Get(params.SessionID); !ok {
		return nil, ErrInvalidParams(nil, fmt.Sprintf("session %s not found", params.SessionID))
	}
	return &LoadSessionResponse{}, nil
}

func (a *sessionStoreAdapter[T]) handleListSessions(_ context.Context, _ *ListSessionsRequest) (*ListSessionsResponse, error) {
	ids := a.store.List()
	sessions := make([]SessionInfo, len(ids))
	for i, id := range ids {
		sessions[i] = SessionInfo{SessionID: id}
	}
	return &ListSessionsResponse{Sessions: sessions}, nil
}

// WithSessionStore configures automatic session management using the given store and factory.
//
// The factory function is called for each NewSession request to create the session data.
// The SessionID is generated automatically and passed to the factory.
//
// When configured, the agent does not need to implement [SessionCreator], [SessionLoader],
// or [SessionLister]. If the agent does implement those interfaces, they take priority
// over the store.
//
// Example:
//
//	store := acp.NewMemoryStore[*MySession]()
//	conn := acp.NewAgentSideConnection(agent, reader, writer,
//	    acp.WithSessionStore(store, func(ctx context.Context, params *acp.NewSessionRequest) (acp.SessionID, *MySession, error) {
//	        id := acp.GenerateSessionID()
//	        return id, &MySession{ID: id}, nil
//	    }),
//	)
func WithSessionStore[T any](store SessionStore[T], factory SessionFactory[T]) ConnectionOption {
	return func(c *Connection) {
		c.sessionStore = &sessionStoreAdapter[T]{
			store:   store,
			factory: factory,
		}
	}
}

// GenerateSessionID generates a cryptographically random session ID
// with the format "session_<32 hex chars>".
//
// Use this in your [SessionFactory] for default ID generation,
// or provide your own ID scheme.
func GenerateSessionID() SessionID {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("acp: failed to generate session ID: " + err.Error())
	}
	return SessionID("session_" + hex.EncodeToString(b))
}
