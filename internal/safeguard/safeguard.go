// Package safeguard provides panic guards used at every SDK boundary and around
// every spawned goroutine. The SDK must never crash or destabilize the host
// application: internal panics are recovered, converted into a structured
// report, and swallowed.
package safeguard

import "runtime/debug"

// PanicInfo describes a recovered panic.
type PanicInfo struct {
	// Value is the value passed to panic.
	Value any
	// Stack is the stack trace captured at recovery time.
	Stack []byte
}

// Handler observes a recovered panic. Implementations must not panic; if one
// does, the secondary panic is itself recovered and dropped.
type Handler func(PanicInfo)

// Recover recovers from a panic in the current goroutine and reports it through
// handler. It is intended to be deferred at the top of a function:
//
//	defer safeguard.Recover(handler)
//
// A nil handler simply swallows the panic.
func Recover(handler Handler) {
	if r := recover(); r != nil {
		report(handler, r)
	}
}

// Do runs fn, recovering from any panic. It returns true if fn completed
// without panicking. A recovered panic is reported through handler.
func Do(fn func(), handler Handler) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
			report(handler, r)
		}
	}()
	fn()
	return true
}

// Go runs fn in a new goroutine guarded against panics. A panic in fn is
// recovered and reported through handler instead of crashing the process.
func Go(fn func(), handler Handler) {
	go func() {
		defer Recover(handler)
		fn()
	}()
}

// report invokes handler with the recovered value, guarding against a handler
// that itself panics.
func report(handler Handler, recovered any) {
	if handler == nil {
		return
	}
	stack := debug.Stack()
	defer func() { _ = recover() }()
	handler(PanicInfo{Value: recovered, Stack: stack})
}
