package groundcover

import "context"

// Scope holds request-level data merged into every event captured with the
// owning context. It sits between global defaults and per-call options in the
// merge precedence.
type Scope struct {
	user        User
	attributes  Attributes
	level       Level
	fingerprint string
	sessionID   string
	anonymousID string
}

// SetUser sets the identity on the scope.
func (s *Scope) SetUser(u User) { s.user = u }

// SetAttributes merges attributes into the scope.
func (s *Scope) SetAttributes(a Attributes) {
	if s.attributes == nil {
		s.attributes = make(Attributes, len(a))
	}
	s.attributes.merge(a)
}

// SetAttribute sets a single attribute on the scope.
func (s *Scope) SetAttribute(key string, value any) {
	if s.attributes == nil {
		s.attributes = make(Attributes, 1)
	}
	s.attributes[key] = value
}

// SetLevel sets the default severity for events in this scope.
func (s *Scope) SetLevel(l Level) { s.level = l }

// SetFingerprint overrides the grouping fingerprint for events in this scope.
func (s *Scope) SetFingerprint(fp string) { s.fingerprint = fp }

// SetSessionID sets the session identifier for events in this scope.
func (s *Scope) SetSessionID(id string) { s.sessionID = id }

// SetAnonymousID sets the pre-auth anonymous identifier for events in this scope.
func (s *Scope) SetAnonymousID(id string) { s.anonymousID = id }

// clone returns a deep copy of the scope.
func (s *Scope) clone() *Scope {
	if s == nil {
		return &Scope{}
	}
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
	if !s.user.isZero() {
		e.User = s.user
	}
	if len(s.attributes) > 0 {
		if e.Attributes == nil {
			e.Attributes = make(Attributes, len(s.attributes))
		}
		e.Attributes.merge(s.attributes)
	}
	if s.level.valid() {
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

// cloneScopeIntoContext returns a context with a fresh copy of the current scope
// so callers can mutate it without affecting parent contexts.
func cloneScopeIntoContext(ctx context.Context) (context.Context, *Scope) {
	sc := scopeFromContext(ctx).clone()
	return contextWithScope(ctx, sc), sc
}
