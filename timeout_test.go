package groundcover

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
)

func TestFlushTimeoutAndCloseTimeout(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	c.CaptureError(context.Background(), errors.New("e"))
	if err := c.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("FlushTimeout: %v", err)
	}
	if c.Stats().Sent != 1 {
		t.Fatalf("expected 1 sent after FlushTimeout, got %d", c.Stats().Sent)
	}
	if err := c.CloseTimeout(2 * time.Second); err != nil {
		t.Fatalf("CloseTimeout: %v", err)
	}
}

func TestTimeoutHelpersOnDisabledClient(t *testing.T) {
	c, err := New(Config{Disabled: true})
	if err != nil {
		t.Fatalf("new disabled: %v", err)
	}
	if err := c.FlushTimeout(time.Second); err != nil {
		t.Fatalf("disabled FlushTimeout should be nil, got %v", err)
	}
	if err := c.CloseTimeout(time.Second); err != nil {
		t.Fatalf("disabled CloseTimeout should be nil, got %v", err)
	}
}
