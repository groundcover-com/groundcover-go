package selfmetrics_test

import (
	"sync"
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/selfmetrics"
)

func TestCountersAccumulate(t *testing.T) {
	m := selfmetrics.New()
	m.IncCaptured()
	m.IncCaptured()
	m.AddSent(5)
	m.AddDropped(selfmetrics.DropOverflow, 3)
	m.AddDropped(selfmetrics.DropSendExhausted, 1)
	m.AddDropped(selfmetrics.DropBeforeSend, 2)
	m.IncRetries()
	m.IncRateLimited()
	m.IncPanicsRecovered()
	m.IncConfigReloads()
	m.IncSubsystemsDisabled()
	m.SetQueuePending(7, 1024)

	s := m.Snapshot()
	if s.Captured != 2 || s.Sent != 5 || s.DroppedOverflow != 3 || s.DroppedSend != 1 || s.DroppedBeforeSend != 2 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if s.Retries != 1 || s.RateLimited != 1 || s.PanicsRecovered != 1 || s.ConfigReloads != 1 || s.SubsystemsDisabled != 1 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if s.QueuePendingItems != 7 || s.QueuePendingBytes != 1024 {
		t.Fatalf("unexpected gauges: %+v", s)
	}

	m.Reset()
	if (m.Snapshot() != selfmetrics.Snapshot{}) {
		t.Fatalf("reset should zero all counters, got %+v", m.Snapshot())
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := selfmetrics.New()
	var wg sync.WaitGroup
	const goroutines, perG = 50, 1000
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perG {
				m.IncCaptured()
			}
		}()
	}
	wg.Wait()
	if got := m.Snapshot().Captured; got != goroutines*perG {
		t.Fatalf("expected %d, got %d", goroutines*perG, got)
	}
}
