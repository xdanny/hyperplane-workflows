package store

import (
	"encoding/json"
	"time"
)

// JSON is a convenience alias for free-form jsonb columns.
type JSON = json.RawMessage

// StrPtr returns a pointer to s, for callers building an optional string
// filter (e.g. RunFilter.Project, ListLanes, ListDecisions) where nil must
// mean "no filter" and a non-nil pointer — including one to "" — means
// "filter to exactly this value".
func StrPtr(s string) *string { return &s }

// RecipeKey identifies the SDK template that prepared a run.
const RecipeKey = "recipe"

// Run is the top-level execution record. doc holds the full status snapshot;
// the promoted columns make the run list cheap to query.
type Run struct {
	RunID     string    `json:"runId"`
	DAG       string    `json:"dag"`
	Goal      string    `json:"goal,omitempty"`
	Project   string    `json:"project,omitempty"`
	State     string    `json:"state"`
	Mode      string    `json:"mode,omitempty"`
	Type      string    `json:"type,omitempty"`
	Domain    string    `json:"domain,omitempty"`
	RiskLevel string    `json:"riskLevel,omitempty"`
	ParentRun string    `json:"parentRun,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	EndedAt   time.Time `json:"endedAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	OwnerPID  int       `json:"ownerPid,omitempty"`
	Options   JSON      `json:"options,omitempty"`
	Summary   JSON      `json:"summary,omitempty"`
	Metadata  JSON      `json:"metadata,omitempty"`
	Doc       JSON      `json:"doc,omitempty"`

	// RecipeName is RESOLVED AT READ TIME from doc.recipe by RecipeNamer — the
	// name a person reads for the recipe this run came from. It is not a column
	// and is never written: a recipe renamed today should not leave every run
	// it ever produced calling it by its old name.
	//
	// Empty when the run came from no recipe, or when the read did not ask for
	// it — a caller that does not resolve names still sends the same run it
	// always did.
	RecipeName string `json:"recipeName,omitempty"`
}

// Node is a per-node state record within a run.
type Node struct {
	RunID     string    `json:"runId"`
	NodeID    string    `json:"nodeId"`
	Kind      string    `json:"kind"`
	Role      string    `json:"role,omitempty"`
	State     string    `json:"state"`
	Attempt   int       `json:"attempt"`
	Score     *float64  `json:"score,omitempty"`
	Error     string    `json:"error,omitempty"`
	Output    JSON      `json:"output,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	Doc       JSON      `json:"doc,omitempty"`
}

// Event is an append-only journal entry with a per-run monotonic seq.
type Event struct {
	RunID string    `json:"runId"`
	Seq   int64     `json:"seq"`
	TS    time.Time `json:"ts"`
	Level string    `json:"level"`
	Type  string    `json:"type"`
	Node  string    `json:"node,omitempty"`
	Data  JSON      `json:"data,omitempty"`
}

// Record is a global record: approvals, selections, notifications, proposals, drafts.
type Record struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	State     string    `json:"state,omitempty"`
	GateKey   string    `json:"gateKey,omitempty"`
	RunID     string    `json:"runId,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Consumed  bool      `json:"consumed"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Doc       JSON      `json:"doc,omitempty"`
}

// Skill is a reusable instruction package.
type Skill struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Category    string    `json:"category,omitempty"`
	Reasoning   string    `json:"reasoning,omitempty"`
	Instruction string    `json:"instruction"`
	Metadata    JSON      `json:"metadata,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

// ContextBundle is a reusable collection of files/images with why-metadata.
type ContextBundle struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Items       JSON      `json:"items"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

// Connector is a versioned connector contract.
type Connector struct {
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	Manifest    JSON      `json:"manifest"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

// Preference is a DSPy/RLHF signal captured from a selection gate or judge.
type Preference struct {
	ID         int64     `json:"id,omitempty"`
	SkillID    string    `json:"skillId"`
	Source     string    `json:"source"`
	WinnerID   string    `json:"winnerId,omitempty"`
	Candidates JSON      `json:"candidates"`
	RunID      string    `json:"runId,omitempty"`
	CreatedAt  time.Time `json:"createdAt,omitempty"`
}

// RunFilter narrows ListRuns.
type RunFilter struct {
	Runtime string // exact execution runtime from the immutable launch record
	State   string
	DAG     string
	Type    string
	// Project distinguishes "no filter" (nil) from "filter to exactly this
	// value" (non-nil, including a pointer to ""  for unscoped-only). Build
	// with StrPtr.
	Project *string
	Limit   int
	// ByStart orders newest-STARTED first (ties broken by run id) instead of
	// most recently updated. The runs list shows the started column, and a list
	// sorted by a column it does not show reads as scrambled.
	ByStart bool
	Parent  string // filter to children of this run; empty = no parent filter
	// TopLevel selects user-facing deliveries, including explicitly bound SDK
	// continuations. Ordinary nested and batch children remain excluded.
	TopLevel bool
	// Recipe narrows to runs instantiated from one recipe (doc.recipe). It is
	// what makes "the same delivery, attempted before" a query rather than a
	// guess — the DAG name alone is shared by every unrelated run of it.
	Recipe string
}

// Lane is a swim-lane grouping tickets for one idea or app.
type Lane struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	TargetRoot  string    `json:"targetRoot"`
	Project     string    `json:"project,omitempty"`
	OriginRun   string    `json:"originRun,omitempty"` // run this lane was decomposed from, if any
	CreatedAt   time.Time `json:"createdAt"`
}

// TicketCounts holds a per-status summary of tickets in a lane.
type TicketCounts struct {
	Todo       int `json:"todo"`
	InProgress int `json:"inProgress"`
	Review     int `json:"review"`
	Done       int `json:"done"`
	Blocked    int `json:"blocked"`
}

// LaneWithCounts is a Lane plus its ticket status counts.
type LaneWithCounts struct {
	Lane
	Counts TicketCounts `json:"counts"`
}

// Ticket is a unit of build work within a Lane.
type Ticket struct {
	SDKRunID      string    `json:"sdkRunId,omitempty"`
	ID            string    `json:"id"`
	LaneID        string    `json:"laneId"`
	Title         string    `json:"title"`
	Detail        string    `json:"detail"`
	Status        string    `json:"status"`
	Priority      int       `json:"priority"`
	Assignee      string    `json:"assignee"`
	Deps          []string  `json:"deps"`
	Output        string    `json:"output"`
	Error         string    `json:"error"`
	Attempts      int       `json:"attempts"`
	ReviewVerdict string    `json:"reviewVerdict,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
