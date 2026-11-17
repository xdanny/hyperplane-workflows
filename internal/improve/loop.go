package improve

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// The improve loop was always described as "always-on" and never was: M26 built
// the pass, and then nothing ran it. `hyperplane improve --once` and POST
// /api/improve both need somebody to decide it is time. A self-improving system
// whose self-improvement is a chore is just a chore.
//
// Loop is the missing half: a timer inside the API process that runs the same
// pass on a schedule. It changes WHO decides it is time, and nothing else — the
// pass still uses explicitly associated SDK observations and preferences, scores variants on
// the skill's evalset, and still promotes only a variant that won.

// Defaults for the schedule.
const (
	// DefaultInterval is how often the loop runs when the environment does not
	// say otherwise. Feedback accumulates over sessions, not minutes, and every
	// pass can spend billed engine time — so the rhythm is "a few times a day",
	// not "constantly".
	DefaultInterval = 6 * time.Hour
	// DefaultStartupDelay is how long after boot the first pass fires, and only
	// if there is feedback worth harvesting. Long enough to stay out of the way
	// of whatever the person who just started the server is doing.
	DefaultStartupDelay = 2 * time.Minute
)

// Loop schedules improve passes. The zero value is disabled; a Loop with
// Interval <= 0 never fires on a timer but still serves as the non-overlap lock
// for manually triggered passes.
type Loop struct {
	// Interval is the gap between passes. <= 0 disables the timer.
	Interval time.Duration
	// StartupDelay is how long after Start the first pass fires. <= 0 uses
	// DefaultStartupDelay.
	StartupDelay time.Duration
	// Cycle runs one pass. Required — injected so the schedule can be tested
	// without a database or a billed engine.
	Cycle func(ctx context.Context, trigger string) error
	// Ready gates the STARTUP pass only: it answers "is there anything to
	// harvest?", so a server that is restarted often does not burn a pass each
	// time. Nil ⇒ the startup pass always fires. Scheduled passes are not gated
	// — a pass that finds nothing is a fact worth recording.
	Ready func(ctx context.Context) bool
	// Log receives one line per scheduling decision. Nil ⇒ silent.
	Log func(format string, args ...any)

	mu      sync.Mutex
	running bool
	// lastTrigger/lastStart describe the pass currently or most recently run,
	// for the caller that wants to say why a manual request was refused.
	lastTrigger string
	lastStart   time.Time
}

// Enabled reports whether the timer will fire at all.
func (l *Loop) Enabled() bool { return l.Interval > 0 }

// Running reports whether a pass is in flight, and what triggered it.
func (l *Loop) Running() (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running, l.lastTrigger
}

// TryCycle runs one pass unless one is already in flight, in which case it
// returns false and does nothing. A nil cycle runs the loop's own Cycle.
//
// This is the non-overlap guard, and it covers the manual trigger too: the
// timer and the "Run now" button take the same lock, so a person clicking
// during a scheduled pass cannot start a second one that optimizes the same
// skills against the same evalsets at the same time.
func (l *Loop) TryCycle(ctx context.Context, trigger string, cycle func(context.Context) error) (bool, error) {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return false, nil
	}
	l.running = true
	l.lastTrigger = trigger
	l.lastStart = time.Now()
	l.mu.Unlock()

	defer func() {
		l.mu.Lock()
		l.running = false
		l.mu.Unlock()
	}()

	if cycle == nil {
		if l.Cycle == nil {
			return false, fmt.Errorf("improve loop: no cycle configured")
		}
		cycle = func(ctx context.Context) error { return l.Cycle(ctx, trigger) }
	}
	return true, cycle(ctx)
}

// Start runs the loop until ctx is done. It returns immediately; the schedule
// lives in a goroutine. A disabled loop logs why and starts nothing.
func (l *Loop) Start(ctx context.Context) {
	if !l.Enabled() {
		l.logf("improve loop: disabled (set HYPERPLANE_IMPROVE_INTERVAL to enable)")
		return
	}
	delay := l.StartupDelay
	if delay <= 0 {
		delay = DefaultStartupDelay
	}
	l.logf("improve loop: every %s, first check in %s", l.Interval, delay)

	startup := time.NewTimer(delay)
	ticker := time.NewTicker(l.Interval)
	go func() {
		defer startup.Stop()
		defer ticker.Stop()
		l.run(ctx, startup.C, ticker.C)
	}()
}

// run is the schedule itself, taking its clocks as arguments so a test can
// drive it without waiting on real time.
func (l *Loop) run(ctx context.Context, startup, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-startup:
			if l.Ready != nil && !l.Ready(ctx) {
				l.logf("improve loop: nothing to harvest yet — waiting for the next cycle")
				continue
			}
			l.fire(ctx, "startup")
		case <-ticks:
			l.fire(ctx, "timer")
		}
	}
}

// fire runs one pass and logs what happened, including the case that matters
// most for trust: a pass skipped because another was still running.
func (l *Loop) fire(ctx context.Context, trigger string) {
	ran, err := l.TryCycle(ctx, trigger, nil)
	switch {
	case !ran:
		l.logf("improve loop: %s pass skipped — a pass is already running", trigger)
	case err != nil:
		l.logf("improve loop: %s pass failed: %v", trigger, err)
	default:
		l.logf("improve loop: %s pass complete", trigger)
	}
}

func (l *Loop) logf(format string, args ...any) {
	if l.Log != nil {
		l.Log(format, args...)
	}
}

// Harvestable reports whether ANY skill has accumulated enough feedback for a
// pass to have something to do. It short-circuits on the first skill that
// qualifies, so the common answer ("no") costs one harvest per skill and the
// useful answer costs one.
//
// It exists so a restart does not spend a pass proving there is nothing to
// learn — see Loop.Ready.
func Harvestable(ctx context.Context, st Store, cfg ImproveConfig) (bool, error) {
	harvestFn := cfg.HarvestFn
	var realSt *store.Store
	if harvestFn == nil {
		rs, ok := st.(*store.Store)
		if !ok {
			return false, fmt.Errorf("improve: HarvestFn must be set when st is not *store.Store")
		}
		realSt = rs
		harvestFn = realHarvest
	}

	skillIDs := cfg.Skills
	if len(skillIDs) == 0 {
		skills, err := st.ListSkills(ctx)
		if err != nil {
			return false, fmt.Errorf("improve: list skills: %w", err)
		}
		for _, sk := range skills {
			skillIDs = append(skillIDs, sk.ID)
		}
	}
	minFB := cfg.minFeedback()
	for _, id := range skillIDs {
		feedback, err := harvestFn(ctx, realSt, id)
		if err != nil {
			// One unreadable skill is not an answer about the rest.
			continue
		}
		if len(feedback) >= minFB {
			return true, nil
		}
	}
	return false, nil
}
