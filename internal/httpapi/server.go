// Package httpapi exposes the Hyperplane HTTP API. Loopback-only, no auth
// (local-first single user). Routes are added per milestone; M0 ships health.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/brief"
	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/events"
	"github.com/xdanny/hyperplane-workflows/internal/improve"
	"github.com/xdanny/hyperplane-workflows/internal/localmodel"
	"github.com/xdanny/hyperplane-workflows/internal/persona"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Server holds dependencies shared by handlers.
type Server struct {
	cfg       config.Config
	store     *store.Store
	bus       *events.Bus
	runner    *runner
	sdkHealth sdkHealth
	// improveLoop schedules the always-on self-improvement pass and owns the
	// non-overlap lock every pass takes, timer-driven or manual. It is built
	// here but only STARTED by StartLearning, so a test server has no timers.
	improveLoop *improve.Loop
	// personaCodex is the persona-review engine override (see SetPersonaCodex).
	personaCodex persona.CodexFunc
	// briefCodex overrides the engine an outcome brief is written by. Nil in
	// production — the real adapter. It is the same seam internal/brief offers
	// its callers, surfaced here so an endpoint test can exercise the whole
	// write path without spawning codex (see export_test.go).
	briefCodex brief.CodexFunc
}

// New constructs a Server with an in-process event bus and run manager.
func New(cfg config.Config, st *store.Store) *Server {
	bus := events.NewBus()
	r := newRunner(st, bus, cfg.RunsDir)
	s := &Server{cfg: cfg, store: st, bus: bus, runner: r}
	s.improveLoop = newImproveLoop(s, cfg.ImproveInterval)
	return s
}

// Handler builds the HTTP mux with all registered routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// Runs
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("DELETE /api/runs/{id}", s.handleDeleteRun)
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.handleCancelRun)
	mux.HandleFunc("GET /api/runs/{id}/continuation", s.handleReviewContinuation)
	mux.HandleFunc("POST /api/runs/{id}/continuation", s.handleStartContinuation)
	mux.HandleFunc("GET /api/runs/{id}/nodes", s.handleListNodes)
	mux.HandleFunc("GET /api/runs/{id}/events", s.handleListEvents)
	mux.HandleFunc("GET /api/runs/{id}/events/stream", s.handleEventsStream)
	mux.HandleFunc("GET /api/runs/{id}/artifacts", s.handleListArtifacts)

	// Outcome Decision, run grain — an optional annotation on a single attempt.
	// Still recordable, no longer demanded anywhere: the queue asks at journey
	// grain. Insert-only; embedded in GET /api/runs/{id} as `outcome`.
	mux.HandleFunc("POST /api/runs/{id}/outcome", s.handleRecordOutcome)

	// Outcome briefs — the platform's own plain-language account of what a run
	// or a delivery actually produced, written AFTER the fact from
	// authoritative state (see briefs.go and internal/brief).
	mux.HandleFunc("GET /api/runs/{id}/brief", s.handleRunBrief)
	mux.HandleFunc("GET /api/decisions/{id}/brief", s.handleDeliveryBrief)
	// The backfill path. Both triggers only fire forward, so work that finished
	// before briefs existed needs a way to be accounted for that is not
	// "run it again". Synchronous, insert-only, and honest about a run that has
	// not finished or a project with nothing accepted yet.
	mux.HandleFunc("POST /api/runs/{id}/brief", s.handleWriteRunBrief)
	mux.HandleFunc("POST /api/projects/{id}/narrative", s.handleWriteProjectNarrative)

	// Journeys — the delivery's story across runs, derived on every read (see
	// journeys.go). The outcome here is the one the inbox asks for.
	mux.HandleFunc("GET /api/journeys", s.handleListJourneys)
	mux.HandleFunc("GET /api/journeys/{key}", s.handleGetJourney)
	mux.HandleFunc("POST /api/journeys/{key}/outcome", s.handleRecordJourneyOutcome)

	// Token telemetry
	mux.HandleFunc("GET /api/runs/{id}/tokens", s.handleRunTokens)
	mux.HandleFunc("GET /api/stats/tokens", s.handleStatsTokens)

	// SDK session logs
	mux.HandleFunc("GET /api/runs/{id}/log", s.handleGetLog)
	mux.HandleFunc("GET /api/runs/{id}/log/{nodeId}", s.handleGetLog)

	// Trigger
	mux.HandleFunc("POST /api/run", s.handlePostRun)
	mux.HandleFunc("GET /api/workflows", s.handleListSDKWorkflows)
	mux.HandleFunc("GET /api/workflow-checks", s.handleWorkflowChecks)
	mux.HandleFunc("POST /api/workflows", s.handleSaveSDKWorkflow)
	mux.HandleFunc("POST /api/workflows/validate", s.handleValidateSDKWorkflow)
	mux.HandleFunc("GET /api/workflows/{id}/runs", s.handleWorkflowRuns)
	mux.HandleFunc("POST /api/workflows/{id}/attach-runs", s.handleAttachRuns)
	mux.HandleFunc("PUT /api/workflows/{id}", s.handleSaveSDKWorkflow)
	mux.HandleFunc("DELETE /api/workflows/{id}", s.handleDeleteSDKWorkflow)

	// Compose — intent → reviewed SDK workflow proposal
	mux.HandleFunc("GET /api/compose/briefs", s.handleListBriefs)
	mux.HandleFunc("POST /api/compose", s.handleCompose)

	// Backlog orchestrator (M21)
	mux.HandleFunc("POST /api/orchestrate", s.handlePostOrchestrate)
	mux.HandleFunc("POST /api/orchestrate/{id}/cancel", s.handleCancelOrchestrate)
	mux.HandleFunc("GET /api/orchestrate", s.handleListOrchestrate)
	mux.HandleFunc("GET /api/orchestrate/{id}", s.handleGetOrchestrate)

	// Always-on self-improvement (M26)
	mux.HandleFunc("POST /api/improve", s.handlePostImprove)
	mux.HandleFunc("GET /api/improve", s.handleListImprove)
	mux.HandleFunc("GET /api/improve/{id}", s.handleGetImprove)

	// Knowledge
	mux.HandleFunc("GET /api/skills", s.handleListSkills)
	mux.HandleFunc("GET /api/skills/{id}", s.handleGetSkill)
	mux.HandleFunc("POST /api/skills", s.handleUpsertSkill)

	// Skill extraction — a passed run becomes a DRAFT skill, a person approves it
	mux.HandleFunc("POST /api/runs/{id}/extract-skill", s.handleExtractSkill)
	mux.HandleFunc("POST /api/skills/{id}/approve", s.handleApproveSkill)
	mux.HandleFunc("POST /api/skills/{id}/reject", s.handleRejectSkill)

	// Personas — the audit charters in personas/*.md, registered and invocable.
	// Read-only: a charter is amended in git, never through the API. The one
	// write is a REVIEW, which is advisory evidence and gates nothing (AI-12).
	mux.HandleFunc("GET /api/personas", s.handleListPersonas)
	mux.HandleFunc("GET /api/personas/{id}", s.handleGetPersona)
	mux.HandleFunc("POST /api/personas/{id}/review", s.handlePersonaReview)
	mux.HandleFunc("GET /api/persona-reviews", s.handleListPersonaReviews)

	// Decision records (ADR-style, self-organizing knowledge)
	mux.HandleFunc("GET /api/decisions", s.handleListDecisions)
	mux.HandleFunc("GET /api/decisions/{id}", s.handleGetDecision)
	mux.HandleFunc("POST /api/decisions", s.handleCreateDecision)
	mux.HandleFunc("DELETE /api/decisions/{id}", s.handleDeleteDecision)
	// Projects (first-class: a project is a row, not only a tag)
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects/{id}", s.handleGetProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.handlePatchProject)

	// Knowledge graph (graphify): self-organizing graph over knowledge
	mux.HandleFunc("GET /api/graph/knowledge", s.handleKnowledgeGraph)

	// Optimize (M11/M18)
	mux.HandleFunc("POST /api/skills/{id}/optimize", s.handleOptimizeSkill)
	mux.HandleFunc("GET /api/skills/{id}/optimize-runs", s.handleListOptimizeRuns)

	// Inbox / decisions
	mux.HandleFunc("GET /api/inbox", s.handleInbox)
	mux.HandleFunc("POST /api/approvals/{id}/decision", s.handleApprovalDecision)
	mux.HandleFunc("POST /api/selections/{id}/decision", s.handleSelectionDecision)
	mux.HandleFunc("POST /api/inbox/clear-stale", s.handleClearStale)
	mux.HandleFunc("POST /api/inbox/{recordType}/{id}/dismiss", s.handleDismissRecord)
	mux.HandleFunc("GET /api/preferences/{skill}", s.handleListPreferences)

	// Evals
	mux.HandleFunc("POST /api/skills/{id}/evalset/generate", s.handleGenerateEvalset)
	mux.HandleFunc("GET /api/evals", s.handleListEvalsets)
	mux.HandleFunc("GET /api/evals/{id}", s.handleGetEvalset)
	mux.HandleFunc("GET /api/evals/{id}/runs", s.handleListEvalRuns)
	mux.HandleFunc("GET /api/eval-runs/{id}", s.handleGetEvalRun)

	// Knowledge depth (M19)
	mux.HandleFunc("GET /api/tech-radar", s.handleListTechRadar)
	mux.HandleFunc("POST /api/tech-radar", s.handleCreateTechRadar)
	mux.HandleFunc("GET /api/stacks", s.handleListStacks)
	mux.HandleFunc("POST /api/stacks", s.handleCreateStack)
	mux.HandleFunc("GET /api/categories", s.handleListCategories)
	mux.HandleFunc("POST /api/categories", s.handleCreateCategory)
	mux.HandleFunc("GET /api/templates", s.handleListTemplates)
	mux.HandleFunc("POST /api/templates", s.handleCreateTemplate)
	mux.HandleFunc("POST /api/templates/{id}/instantiate", s.handleInstantiateTemplate)

	// Governed local routing (M25)
	mux.HandleFunc("GET /api/routing", s.handleListRouting)
	mux.HandleFunc("POST /api/routing/eval", s.handleRoutingEval)
	mux.HandleFunc("POST /api/routing", s.handleUpsertRouting)

	// Auth + orchestrator terminal (remote access)
	mux.HandleFunc("GET /api/auth/check", s.handleAuthCheck)
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/terminal/ws", s.handleTerminalWS)

	// File serving
	mux.HandleFunc("GET /file/{id}/{path...}", s.handleFile)

	// Knowledge base — unified search (Phase 5) + memory (Phase 3)
	mux.HandleFunc("GET /api/kb", s.handleSearchKB)
	mux.HandleFunc("GET /api/kb/memory", s.handleListMemory)
	mux.HandleFunc("POST /api/kb/memory", s.handleUpsertMemory)
	mux.HandleFunc("DELETE /api/kb/memory/{key}", s.handleDeleteMemory)

	// Board lanes and managed SDK tasks
	mux.HandleFunc("GET /api/lanes", s.handleListLanes)
	mux.HandleFunc("POST /api/lanes", s.handleCreateLane)
	mux.HandleFunc("POST /api/lanes/decompose", s.handleDecomposeLane)
	mux.HandleFunc("GET /api/lanes/{id}/tickets", s.handleGetLaneTickets)
	mux.HandleFunc("POST /api/lanes/{id}/work", s.handleStartLaneWork)
	mux.HandleFunc("POST /api/tickets", s.handleCreateTicket)
	mux.HandleFunc("GET /api/tickets/{id}", s.handleGetTicket)
	mux.HandleFunc("PATCH /api/tickets/{id}", s.handlePatchTicket)

	// The API + artifact routes sit behind the token gate; the static web bundle
	// is public (so the login screen can load) and served as an SPA. Single
	// origin keeps remote access (tunnel) and auth simple.
	authed := s.authMiddleware(mux)
	root := http.NewServeMux()
	root.Handle("/api/", authed)
	root.Handle("/file/", authed)
	root.Handle("/", s.staticHandler())
	return root
}

type healthResponse struct {
	OK          bool   `json:"ok"`
	DB          string `json:"db"`
	Codex       string `json:"codex"`
	CodexDetail string `json:"codexDetail"`
	LocalEngine string `json:"localEngine,omitempty"`
	Time        string `json:"time"`
	Detail      string `json:"detail,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := healthResponse{OK: true, DB: "ok", Codex: "unavailable", CodexDetail: "SDK installation unavailable; run make sdk-install for this backend", Time: time.Now().UTC().Format(time.RFC3339)}
	if err := s.store.Ping(ctx); err != nil {
		resp.OK = false
		resp.DB = "down"
		resp.Detail = err.Error()
	}
	if s.sdkHealth.check(ctx) {
		resp.Codex = "ok"
		resp.CodexDetail = "SDK installation available; authentication and model access are not checked"
	} else {
		resp.OK = false
	}
	// Check local engine reachability (non-blocking: uses its own 4s timeout).
	if ok, info := localmodel.Reachable(ctx); ok {
		resp.LocalEngine = "reachable, model: " + info
	} else {
		resp.LocalEngine = "not reachable: " + info
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// projectFilter reads the ?project= query param and distinguishes "absent"
// (no filter — nil) from "present" (filter to exactly this value, including
// "" for unscoped-only) for handlers that pass it through to a store filter
// built with store.StrPtr semantics (nil vs non-nil pointer).
func projectFilter(r *http.Request) *string {
	if !r.URL.Query().Has("project") {
		return nil
	}
	p := r.URL.Query().Get("project")
	return &p
}

// Shutdown cancels and drains workflow execution before the store is closed.
func (s *Server) Shutdown(ctx context.Context) error { return s.runner.shutdown(ctx) }
