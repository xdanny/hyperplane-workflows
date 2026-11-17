// Package sdkruntime owns durable SDK workflow launches, checkpoints and
// approvals. The SDK worker owns model turns; this package has no graph scheduler.
package sdkruntime

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/events"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

var ErrWorkspaceBusy = errors.New("task target overlaps another active writer")

// Options contains the host settings used by SDK workflows.
type Options struct {
	SourceWorkflowID string
	WorkflowName     string
	// Slots are the reusable-input values this launch accepted, recorded on the
	// run so a later reader can see WHICH week, file or question this run
	// answered. They are already substituted into the submitted definition.
	Slots map[string]string
	// SlotLabels are the human labels for those inputs, recorded with the frozen
	// request so every surface can name a value the way the person was asked for it.
	SlotLabels map[string]string
	// SourceRevision is the saved revision the submitted definition was loaded
	// from. It is provenance, not a claim that a saved revision executed: an
	// edited definition launched from the editor carries no revision.
	SourceRevision int64
	// TemplateID records the shipped template a run was prepared from.
	TemplateID string
	// SignOff is the saved workflow's sign-off policy AS IT READ AT LAUNCH. It is
	// captured on the run so a later edit to the workflow never restates the
	// terms an older run was started under.
	SignOff      string
	continuation *continuationSource
	ticket       *store.SDKTicketAdmission
	// Started receives exactly one acknowledgement after startup is persisted,
	// or the error that prevented startup. Asynchronous callers must receive it.
	Started    chan<- error
	RunID      string
	Goal       string
	Project    string
	TargetRoot string
	RunsDir    string
	Inputs     map[string]string
	Bus        *events.Bus
	// TokenBudget bounds total reported usage between turns. A nonpositive value
	// uses HYPERPLANE_RUN_TOKEN_BUDGET, then the SDK workflow default.
	TokenBudget int
	AutoRepair  bool
}

// Result identifies the persisted run and its terminal state.
type Result struct {
	RunID string
	State string
}

// runMetadata classifies a stored run without describing executable graph nodes.
type runMetadata struct {
	Type      string
	RiskLevel string
}

// NewRunID includes a nonce so concurrent launches never share a run folder.
func NewRunID(name string) string {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%s-%x", time.Now().Format("20060102-150405.000"), name, nonce)
}

func tokenBudgetFromEnv() int {
	var budget int
	fmt.Sscanf(os.Getenv("HYPERPLANE_RUN_TOKEN_BUDGET"), "%d", &budget)
	return budget
}
