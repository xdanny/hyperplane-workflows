package improve

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/optimize"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// counter records how a fake cycle was driven, safely across the loop's
// goroutine and the test's.
type counter struct {
	mu       sync.Mutex
	triggers []string
	block    chan struct{} // when non-nil, the cycle waits on it
}

func (c *counter) cycle(ctx context.Context, trigger string) error {
	c.mu.Lock()
	c.triggers = append(c.triggers, trigger)
	block := c.block
	c.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	return nil
}

func (c *counter) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.triggers...)
}

// waitFor polls until cond holds or the deadline passes. The loop runs in its
// own goroutine, so the alternative is a sleep long enough to be flaky.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// TestLoopDisabledStartsNothing pins the off switch: HYPERPLANE_IMPROVE_INTERVAL=0
// must mean no pass ever fires by itself, not "fires with a default interval".
func TestLoopDisabledStartsNothing(t *testing.T) {
	c := &counter{}
	l := &Loop{Interval: 0, Cycle: c.cycle}
	if l.Enabled() {
		t.Fatal("a zero interval must read as disabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	if got := c.seen(); len(got) != 0 {
		t.Errorf("a disabled loop fired %v", got)
	}
}

// TestLoopFiresOnTick drives the schedule with a hand-fed clock: no waiting on
// a real six-hour interval, and no sleeping to find out whether it fired.
func TestLoopFiresOnTick(t *testing.T) {
	c := &counter{}
	l := &Loop{Interval: time.Hour, Cycle: c.cycle}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticks := make(chan time.Time, 2)
	go l.run(ctx, nil, ticks)

	ticks <- time.Now()
	if !waitFor(t, func() bool { return len(c.seen()) == 1 }) {
		t.Fatalf("no pass ran on the first tick, saw %v", c.seen())
	}
	ticks <- time.Now()
	if !waitFor(t, func() bool { return len(c.seen()) == 2 }) {
		t.Fatalf("no pass ran on the second tick, saw %v", c.seen())
	}
	for _, trigger := range c.seen() {
		if trigger != "timer" {
			t.Errorf("trigger = %q, want timer", trigger)
		}
	}
}

// TestLoopStartupWaitsForHarvestableFeedback covers the "fire soon after boot,
// but only if there is something to learn from" rule. A server restarted five
// times in a morning must not spend five passes proving there is no feedback.
func TestLoopStartupWaitsForHarvestableFeedback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cold := &counter{}
	coldLoop := &Loop{Interval: time.Hour, Cycle: cold.cycle, Ready: func(context.Context) bool { return false }}
	startup := make(chan time.Time, 1)
	go coldLoop.run(ctx, startup, nil)
	startup <- time.Now()
	time.Sleep(30 * time.Millisecond)
	if got := cold.seen(); len(got) != 0 {
		t.Errorf("startup pass fired with nothing to harvest: %v", got)
	}

	warm := &counter{}
	warmLoop := &Loop{Interval: time.Hour, Cycle: warm.cycle, Ready: func(context.Context) bool { return true }}
	startup2 := make(chan time.Time, 1)
	go warmLoop.run(ctx, startup2, nil)
	startup2 <- time.Now()
	if !waitFor(t, func() bool { return len(warm.seen()) == 1 }) {
		t.Fatalf("startup pass did not fire with feedback waiting, saw %v", warm.seen())
	}
	if warm.seen()[0] != "startup" {
		t.Errorf("trigger = %q, want startup", warm.seen()[0])
	}
}

// TestLoopLockRefusesOverlap is the non-overlap guard: while one pass is in
// flight, neither the timer nor the "Run now" button may start a second one
// optimizing the same skills against the same evalsets.
func TestLoopLockRefusesOverlap(t *testing.T) {
	release := make(chan struct{})
	c := &counter{block: release}
	l := &Loop{Interval: time.Hour, Cycle: c.cycle}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		close(started)
		if ran, err := l.TryCycle(ctx, "timer", nil); !ran || err != nil {
			t.Errorf("first pass: ran=%v err=%v, want it to run", ran, err)
		}
	}()
	<-started
	if !waitFor(t, func() bool { running, _ := l.Running(); return running }) {
		t.Fatal("the first pass never took the lock")
	}

	ran, err := l.TryCycle(ctx, "manual", nil)
	if ran {
		t.Error("a second pass ran while the first was still in flight")
	}
	if err != nil {
		t.Errorf("a refused pass is not an error, got %v", err)
	}
	if running, trigger := l.Running(); !running || trigger != "timer" {
		t.Errorf("Running() = %v/%q, want true/timer so the refusal can say why", running, trigger)
	}

	close(release)
	if !waitFor(t, func() bool { running, _ := l.Running(); return !running }) {
		t.Fatal("the lock was not released when the pass finished")
	}

	// Once free, the next pass runs — the lock is a guard, not a latch.
	if ran, err := l.TryCycle(ctx, "manual", nil); !ran || err != nil {
		t.Errorf("pass after release: ran=%v err=%v, want it to run", ran, err)
	}
	if got := c.seen(); len(got) != 2 || got[1] != "manual" {
		t.Errorf("triggers = %v, want [timer manual]", got)
	}
}

// TestTryCycleRunsCallerSuppliedWork proves the manual trigger can bring its own
// configured pass and still be covered by the shared lock.
func TestTryCycleRunsCallerSuppliedWork(t *testing.T) {
	l := &Loop{}
	want := errors.New("boom")
	var called bool
	ran, err := l.TryCycle(context.Background(), "manual", func(context.Context) error {
		called = true
		return want
	})
	if !ran || !called {
		t.Fatalf("caller-supplied work did not run: ran=%v called=%v", ran, called)
	}
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want the cycle's own error", err)
	}
	// A loop with neither a Cycle nor caller work says so instead of pretending.
	if ran, err := l.TryCycle(context.Background(), "timer", nil); ran || err == nil {
		t.Errorf("unconfigured loop: ran=%v err=%v, want false + an error", ran, err)
	}
}

// ---- Harvestable ----------------------------------------------------------

// TestHarvestableShortCircuits: the probe answers "yes" at the first skill with
// enough feedback and does not go on harvesting the rest.
func TestHarvestableShortCircuits(t *testing.T) {
	st := newFakeStore("a", "b", "c")
	var probed []string
	cfg := ImproveConfig{
		// An explicit list keeps the probe order deterministic — the point of
		// the test is that it STOPS, which needs a known sequence.
		Skills:      []string{"a", "b", "c"},
		MinFeedback: 2,
		HarvestFn: func(_ context.Context, _ *store.Store, skillRef string) ([]optimize.FeedbackItem, error) {
			probed = append(probed, skillRef)
			if skillRef == "b" {
				return []optimize.FeedbackItem{{}, {}}, nil
			}
			return nil, nil
		},
	}
	ok, err := Harvestable(context.Background(), st, cfg)
	if err != nil || !ok {
		t.Fatalf("Harvestable = %v, %v; want true, nil", ok, err)
	}
	if len(probed) != 2 {
		t.Errorf("probed %v — the probe should stop at the first skill with feedback", probed)
	}

	// Nothing anywhere reads as "nothing to do", not as an error.
	empty := ImproveConfig{
		Skills:      []string{"a", "b", "c"},
		MinFeedback: 2,
		HarvestFn: func(context.Context, *store.Store, string) ([]optimize.FeedbackItem, error) {
			return nil, nil
		},
	}
	if ok, err := Harvestable(context.Background(), st, empty); ok || err != nil {
		t.Errorf("Harvestable with no feedback = %v, %v; want false, nil", ok, err)
	}
}
