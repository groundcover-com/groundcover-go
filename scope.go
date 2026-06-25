package groundcover

import (
	"context"
	"sync"
)

// Scope holds request-level data merged into every event captured with the
// owning context. It sits between global defaults and per-call options in the
// merge precedence.
//
// A Scope is mutable and safe for concurrent use. Middleware installs one fresh,
// isolated Scope per request (see WithIsolatedScope); handlers then mutate that
// same Scope through SetUser / WithScope, and the captured event observes those
// changes without the handler having to thread a new context back.
type Scope struct {
	mu          sync.Mutex
	user        User
	attributes  Attributes
	level       Level
	fingerprint string
	sessionID   string
	anonymousID string
}

// SetUser sets the identity on the scope.
func (s *Scope) SetUser(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user = u
}

// SetAttributes merges attributes into the scope.
func (s *Scope) SetAttributes(a Attributes) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attributes == nil {
		s.attributes = make(Attributes, len(a))
	}
	s.attributes.merge(a)
}

// SetAttribute sets a single attribute on the scope.
func (s *Scope) SetAttribute(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attributes == nil {
		s.attributes = make(Attributes, 1)
	}
	s.attributes[key] = value
}

// SetLevel sets the default severity for events in this scope. Note that the
// scope level never downgrades an intrinsically-fatal event (a recovered panic).
func (s *Scope) SetLevel(l Level) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.level = l
}

// SetFingerprint overrides the grouping fingerprint for events in this scope.
func (s *Scope) SetFingerprint(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fingerprint = fp
}

// SetSessionID sets the session identifier for events in this scope.
func (s *Scope) SetSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

// SetAnonymousID sets the pre-auth anonymous identifier for events in this scope.
func (s *Scope) SetAnonymousID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.anonymousID = id
}

// clone returns a deep copy of the scope (without the source's lock state).
func (s *Scope) clone() *Scope {
	if s == nil {
		return &Scope{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Scope{
		user:        s.user,
		attributes:  s.attributes.clone(),
		level:       s.level,
		fingerprint: s.fingerprint,
		sessionID:   s.sessionID,
		anonymousID: s.anonymousID,
	}
}

// applyTo merges the scope into an event. It runs after global defaults and
// before per-call options.
func (s *Scope) applyTo(e *Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.user.isZero() {
		e.User = s.user
	}
	if len(s.attributes) > 0 {
		if e.Attributes == nil {
			e.Attributes = make(Attributes, len(s.attributes))
		}
		e.Attributes.merge(s.attributes)
	}
	// A scope level fills in / overrides the default, but must never downgrade
	// an intrinsically-fatal event (a recovered panic).
	if s.level.valid() && !e.levelLocked {
		e.Level = s.level
	}
	if s.fingerprint != "" {
		e.Fingerprint = s.fingerprint
	}
	if s.sessionID != "" {
		e.SessionID = s.sessionID
	}
	if s.anonymousID != "" {
		e.AnonymousID = s.anonymousID
	}
}

// scopeKey is the context key type for the request scope.
type scopeKey struct{}

// scopeFromContext returns the scope stored in ctx, or nil if none.
func scopeFromContext(ctx context.Context) *Scope {
	if ctx == nil {
		return nil
	}
	sc, _ := ctx.Value(scopeKey{}).(*Scope)
	return sc
}

// contextWithScope returns a derived context carrying sc.
func contextWithScope(ctx context.Context, sc *Scope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, scopeKey{}, sc)
}

// ensureScope returns the scope already attached to ctx (so mutations are
// visible to whoever else holds the context), creating and attaching a fresh one
// if there is none.
func ensureScope(ctx context.Context) (context.Context, *Scope) {
	if sc := scopeFromContext(ctx); sc != nil {
		return ctx, sc
	}
	sc := &Scope{}
	return contextWithScope(ctx, sc), sc
}

// isolatedScopeContext returns a context carrying a fresh, isolated copy of the
// current scope. Used at request boundaries so per-request mutations never leak
// across requests.
func isolatedScopeContext(ctx context.Context) context.Context {
	return contextWithScope(ctx, scopeFromContext(ctx).clone())
}
