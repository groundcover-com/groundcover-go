package groundcover

import (
	"context"
	"testing"
)

func TestScopeSettersAndClone(t *testing.T) {
	parent := &Scope{}
	parent.SetUser(User{ID: "u-1"})
	parent.SetAttribute("a", 1)

	child := parent.clone()
	child.SetUser(User{ID: "u-2"})
	child.SetAttribute("a", 2)
	child.SetAttribute("b", 3)

	if parent.user.ID != "u-1" {
		t.Fatalf("clone mutated parent user: %q", parent.user.ID)
	}
	if parent.attributes["a"] != 1 {
		t.Fatalf("clone mutated parent attributes: %v", parent.attributes["a"])
	}
	if _, ok := parent.attributes["b"]; ok {
		t.Fatal("clone leaked a new key back into parent")
	}
}

func TestScopeApplyToPrecedenceAndFields(t *testing.T) {
	s := &Scope{}
	s.SetUser(User{ID: "u-1"})
	s.SetAttributes(Attributes{"k": "v"})
	s.SetLevel(LevelWarning)
	s.SetFingerprint("fp")
	s.SetSessionID("sess")
	s.SetAnonymousID("anon")

	e := &Event{Level: LevelError}
	s.applyTo(e)

	if e.User.ID != "u-1" || e.Attributes["k"] != "v" {
		t.Fatalf("scope identity/attrs not applied: %+v", e)
	}
	if e.Level != LevelWarning || e.Fingerprint != "fp" || e.SessionID != "sess" || e.AnonymousID != "anon" {
		t.Fatalf("scope scalar fields not applied: %+v", e)
	}
}

func TestScopeApplyToDoesNotClobberWithEmpty(t *testing.T) {
	// An empty scope must not overwrite event fields already set by options.
	s := &Scope{}
	e := &Event{
		User:        User{ID: "keep"},
		Level:       LevelError,
		Fingerprint: "keep-fp",
	}
	s.applyTo(e)
	if e.User.ID != "keep" || e.Fingerprint != "keep-fp" || e.Level != LevelError {
		t.Fatalf("empty scope clobbered event: %+v", e)
	}
}

func TestNilScopeApplyToIsSafe(t *testing.T) {
	var s *Scope
	s.applyTo(&Event{}) // must not panic
}

func TestContextScopeRoundTrip(t *testing.T) {
	if scopeFromContext(context.Background()) != nil {
		t.Fatal("background context should carry no scope")
	}
	if scopeFromContext(nil) != nil { //nolint:staticcheck // explicitly testing nil ctx
		t.Fatal("nil context should yield nil scope")
	}
	ctx, sc := cloneScopeIntoContext(context.Background())
	sc.SetUser(User{ID: "x"})
	got := scopeFromContext(ctx)
	if got == nil || got.user.ID != "x" {
		t.Fatalf("scope not retrievable from context: %+v", got)
	}
}
