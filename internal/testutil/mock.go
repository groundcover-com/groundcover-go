// Package testutil provides shared test seams: an injectable mock sender and a
// controllable clock. It is intended for use by the SDK's own tests only.
package testutil

import (
	"context"
	"sync"
)

// MockSender is an in-memory Sender. It records every body it receives and can
// be programmed to return errors via Responder.
type MockSender struct {
	mu sync.Mutex
	// Responder, if set, is consulted for each call with the zero-based call
	// index and the body. A nil return means success. If Responder is nil, all
	// calls succeed.
	Responder func(call int, body []byte) error

	calls  int
	bodies [][]byte
}

// Send records the body and returns the programmed result.
func (m *MockSender) Send(_ context.Context, body []byte) error {
	m.mu.Lock()
	call := m.calls
	m.calls++
	cp := make([]byte, len(body))
	copy(cp, body)
	m.bodies = append(m.bodies, cp)
	responder := m.Responder
	m.mu.Unlock()

	if responder != nil {
		return responder(call, body)
	}
	return nil
}

// Calls returns the number of Send invocations.
func (m *MockSender) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// Bodies returns copies of all received bodies in order.
func (m *MockSender) Bodies() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.bodies))
	copy(out, m.bodies)
	return out
}
