package safeguard_test

import (
	"sync"
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/safeguard"
)

func TestRecoverSwallowsPanic(t *testing.T) {
	var got safeguard.PanicInfo
	func() {
		defer safeguard.Recover(func(info safeguard.PanicInfo) { got = info })
		panic("boom")
	}()
	if got.Value != "boom" {
		t.Fatalf("expected recovered value %q, got %v", "boom", got.Value)
	}
	if len(got.Stack) == 0 {
		t.Fatal("expected a non-empty stack trace")
	}
}

func TestRecoverNilHandler(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil handler should not re-panic, got %v", r)
		}
	}()
	func() {
		defer safeguard.Recover(nil)
		panic("ignored")
	}()
}

func TestDoReportsResult(t *testing.T) {
	if ok := safeguard.Do(func() {}, nil); !ok {
		t.Fatal("Do should return true when fn does not panic")
	}
	called := false
	ok := safeguard.Do(func() { panic("x") }, func(safeguard.PanicInfo) { called = true })
	if ok {
		t.Fatal("Do should return false when fn panics")
	}
	if !called {
		t.Fatal("handler should have been called")
	}
}

func TestHandlerThatPanicsIsContained(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a panicking handler must be contained, got %v", r)
		}
	}()
	safeguard.Do(func() { panic("first") }, func(safeguard.PanicInfo) { panic("handler blew up") })
}

func TestGoRecovers(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	var got any
	safeguard.Go(func() { panic("async") }, func(info safeguard.PanicInfo) {
		got = info.Value
		wg.Done()
	})
	wg.Wait()
	if got != "async" {
		t.Fatalf("expected async panic value, got %v", got)
	}
}
