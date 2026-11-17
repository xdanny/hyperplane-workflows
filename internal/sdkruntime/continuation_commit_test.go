package sdkruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestContinuationCommitUnknownPreservesCopiedEvidence(t *testing.T) {
	st := runtimeStore(t)
	opts, calls, source := interruptedWorkflow(t, st, false)
	review, err := ReviewContinuation(t.Context(), st, opts.RunsDir, source.RunID)
	if err != nil || !review.Eligible {
		t.Fatalf("review: %+v %v", review, err)
	}
	original, err := os.ReadFile(filepath.Join(opts.RunsDir, source.RunID, "nodes/delivery/OUTPUTS/explore.md"))
	if err != nil {
		t.Fatal(err)
	}
	beforeCalls, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	child := Options{RunID: NewRunID("continuation-commit-unknown"), RunsDir: opts.RunsDir}

	// This deferred constraint fails only at the target child's admission Commit.
	// It exercises conservative handling of an unacknowledged commit, not an
	// actual lost network acknowledgement: this injected transaction rolls back.
	if _, err := st.Pool.Exec(t.Context(), `CREATE FUNCTION runtime_continuation_commit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected runtime continuation commit failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := st.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS runtime_continuation_commit_failure ON runs`); err != nil {
			t.Error(err)
		}
		if _, err := st.Pool.Exec(ctx, `DROP FUNCTION IF EXISTS runtime_continuation_commit_failure()`); err != nil {
			t.Error(err)
		}
	})
	trigger := fmt.Sprintf(`CREATE CONSTRAINT TRIGGER runtime_continuation_commit_failure AFTER INSERT ON runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.run_id = '%s') EXECUTE FUNCTION runtime_continuation_commit_failure()`, strings.ReplaceAll(child.RunID, "'", "''"))
	if _, err := st.Pool.Exec(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err = RunContinuation(ctx, st, child, source.RunID, review.ReviewToken)
	if !errors.Is(err, store.ErrContinuationCommitUnknown) || !strings.Contains(err.Error(), "injected runtime continuation commit failure") {
		t.Fatalf("commit uncertainty/cause lost: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(opts.RunsDir, child.RunID, "nodes/delivery/OUTPUTS/explore.md"))
	if err != nil || !bytes.Equal(copied, original) {
		t.Fatalf("uncertain commit discarded or changed copied evidence: %q %v", copied, err)
	}
	afterCalls, err := os.ReadFile(calls)
	if err != nil || !bytes.Equal(afterCalls, beforeCalls) {
		t.Fatalf("SDK started despite failed admission: %q => %q %v", beforeCalls, afterCalls, err)
	}
	if _, err := st.GetRun(t.Context(), child.RunID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("injected commit failure persisted child: %v", err)
	}
	after, err := st.GetRun(t.Context(), source.RunID)
	if err != nil || !bytes.Equal(after.Doc, source.Doc) || after.State != "interrupted" {
		t.Fatalf("injected commit failure changed source receipt/claim: %s %v", after.Doc, err)
	}
}
