package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
)

// Installation probes involve no model call. Cache across health polling, while
// launches continue to verify the runtime independently at admission.
//
// The two cache lifetimes are deliberately different. A working installation
// does not stop working from one poll to the next, so a positive answer is
// worth keeping. A negative one is usually a cold start losing a race with its
// own two-second probe, and caching that for half a minute told a person their
// SDK was unavailable long after it was answering. A failure is retried once
// before it is believed, and believed only briefly.
const (
	sdkHealthPositiveTTL = 30 * time.Second
	sdkHealthNegativeTTL = 5 * time.Second
)

type sdkHealth struct {
	mu        sync.Mutex
	checkedAt time.Time
	available bool
}

func (h *sdkHealth) check(ctx context.Context) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	ttl := sdkHealthNegativeTTL
	if h.available {
		ttl = sdkHealthPositiveTTL
	}
	if !h.checkedAt.IsZero() && time.Since(h.checkedAt) < ttl {
		return h.available
	}
	available := codex.CheckInstallation(ctx) == nil
	if !available && ctx.Err() == nil {
		available = codex.CheckInstallation(ctx) == nil
	}
	if ctx.Err() == nil {
		h.available, h.checkedAt = available, time.Now()
	}
	return available
}
