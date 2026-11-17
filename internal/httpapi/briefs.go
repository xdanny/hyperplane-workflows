package httpapi

// briefs.go — the outcome briefs, and the hook that writes one when a human
// records a verdict.
//
// Reading is trivial: the latest brief for a subject, with the two honesty
// flags (who wrote it, and whether its numbers were verified) on the wire, so
// no surface can render a brief without being able to badge it.
//
// The WRITE here is the delivery brief. It is the one moment a run's account
// can include the thing that matters most and is never available while the run
// is executing: what a person decided about it. Recording the verdict also
// regenerates the project narrative, because "what this project has built so
// far" changes exactly when a delivery is accepted and at no other time.
//
// Both are fire-and-forget on a detached context. A person recording a verdict
// waits for their verdict to be recorded, never for prose about it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/brief"
	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// briefTimeout bounds one background brief write. Generous: it covers an engine
// call, its one retry, and the project narrative that follows a delivery.
const briefTimeout = 12 * time.Minute

// briefView is the wire shape of a recorded brief.
//
// The four parts are separate fields rather than one blob because they are
// separately labelled in the UI, and because a reader who only wants "what did
// I actually get" should not have to parse prose to find it.
type briefView struct {
	ID           string `json:"id"`
	SubjectKind  string `json:"subjectKind"`
	SubjectID    string `json:"subjectId"`
	Project      string `json:"project,omitempty"`
	WhatItBuilds string `json:"whatItBuilds,omitempty"`
	WhatHappened string `json:"whatHappened,omitempty"`
	TrueOutcome  string `json:"trueOutcome,omitempty"`
	NextSteps    string `json:"nextSteps,omitempty"`
	// Grounded is false when the brief carries a number the platform could not
	// find in the state it was written from. The UI MUST show this — an
	// unverified figure that renders like a measured one is the whole hazard.
	Grounded bool `json:"grounded"`
	// UngroundedNumbers are those tokens, so the badge can name them.
	UngroundedNumbers []string `json:"ungroundedNumbers,omitempty"`
	// Engine is "codex" or "fallback". A fallback brief is the platform's own
	// deterministic account: fewer facts, no interpretation.
	Engine    string    `json:"engine"`
	CreatedAt time.Time `json:"createdAt"`
}

func toBriefView(row store.Brief) briefView {
	d := brief.ParseDoc(row.Body)
	v := briefView{
		ID: row.ID, SubjectKind: row.SubjectKind, SubjectID: row.SubjectID,
		Project:      row.Project,
		WhatItBuilds: d.WhatItBuilds, WhatHappened: d.WhatHappened,
		TrueOutcome: d.TrueOutcome, NextSteps: d.NextSteps,
		Grounded: row.Grounded, Engine: row.Engine, CreatedAt: row.CreatedAt,
	}
	// Space-separated, never comma-separated: "9,412" is one token.
	v.UngroundedNumbers = strings.Fields(row.UngroundedNumbers)
	return v
}

// handleRunBrief returns the latest outcome brief for a run.
// GET /api/runs/{id}/brief
//
// 404 when none has been written yet — which is the honest answer for a run
// that is still going, and for one that finished before this existed.
func (s *Server) handleRunBrief(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	s.writeLatestBrief(w, r, store.BriefSubjectRun, id)
}

// handleDeliveryBrief returns the latest brief for a DELIVERY — the account
// written once a human recorded a verdict.
// GET /api/decisions/{id}/brief
//
// The id is the outcome decision's ("outcome-<runId>"), because that is what
// the decision surfaces already hold; the run id itself is accepted too, since
// a delivery brief is filed under the run it is about and asking a caller to
// re-derive the prefix would be ceremony.
func (s *Server) handleDeliveryBrief(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runID := strings.TrimPrefix(id, store.OutcomeDecisionID(""))
	if !safeID(runID) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid decision id")))
		return
	}
	s.writeLatestBrief(w, r, store.BriefSubjectDelivery, runID)
}

func (s *Server) writeLatestBrief(w http.ResponseWriter, r *http.Request, kind, subjectID string) {
	row, err := s.store.LatestBrief(r.Context(), kind, subjectID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("no %s brief has been written for %q", kind, subjectID)))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, toBriefView(row))
}

// ---- writing one on demand -------------------------------------------------

// handleWriteRunBrief writes a brief for a run that does not have one, or a
// fresh one for a run whose state has moved since the last was written.
// POST /api/runs/{id}/brief   body: {"subject":"run"|"delivery"}  (default run)
//
// This is the backfill path, and the reason it exists is that the automatic
// triggers only fire forward: a run that finished before briefs existed would
// otherwise have to be re-run to get an account of itself, which is absurd for
// work that is already done and already on record.
//
// It is SYNCHRONOUS, unlike both triggers. Somebody asked for a brief and a
// brief is what comes back — the same reasoning a persona review is
// synchronous. It costs one billed engine call (two if the first draft carries
// an invented number), or none at all when HYPERPLANE_BRIEFS is off, in which
// case the deterministic brief is written instead.
//
// It never overwrites: every brief is a new row and the latest wins for
// display, so re-running this on the same run is safe and the earlier account
// stays readable.
func (s *Server) handleWriteRunBrief(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}

	// An empty body means "the run brief" — the common case for a backfill.
	var body struct {
		Subject string `json:"subject"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	kind := strings.TrimSpace(body.Subject)
	if kind == "" {
		kind = store.BriefSubjectRun
	}
	if kind != store.BriefSubjectRun && kind != store.BriefSubjectDelivery {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf(
			"'subject' must be %q or %q", store.BriefSubjectRun, store.BriefSubjectDelivery)))
		return
	}

	run, err := s.store.GetRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("run %q not found", id)))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	// A run still going has no outcome to state. Writing one anyway would be
	// this feature doing the exact thing it exists to stop.
	if !terminalRunState(run.State) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("run is %s — a brief states what a run produced, so it can only be written once the run has finished", run.State),
			"state": run.State,
		})
		return
	}

	doc, err := brief.Collect(r.Context(), s.store, id, kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	// A DELIVERY brief is an account that includes a person's verdict. Without
	// one there is nothing a delivery brief could say that the run brief does
	// not, and filing a second copy under a name that implies a decision would
	// be inventing the decision.
	if kind == store.BriefSubjectDelivery && doc.Delivery == nil {
		writeJSON(w, http.StatusConflict, errBody(fmt.Errorf(
			"no delivery decision is recorded on run %q — record one, or ask for the run brief instead", id)))
		return
	}

	row, err := brief.Write(r.Context(), s.store, s.briefConfig(), doc)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, toBriefView(row))
}

// narrativeResponse is what the project-narrative endpoint returns: the brief
// that was written, and the exact text that landed on the project card.
type narrativeResponse struct {
	Brief briefView `json:"brief"`
	// Narrative is what was written to projects.narrative — the projection of
	// the brief, not a second account of its own. It is deliberately NOT
	// projects.summary: that field is the owner's "what this project is for"
	// and nothing here ever writes it.
	Narrative string `json:"narrative"`
	// Deliveries is how many accepted deliveries the narrative was written
	// from, so a reader can tell a rich narrative from a thin one.
	Deliveries int `json:"deliveries"`
}

// handleWriteProjectNarrative regenerates "what this project has built so far"
// from the briefs of its accepted deliveries.
// POST /api/projects/{id}/narrative
//
// The other half of the backfill: acceptance regenerates the narrative going
// forward, and this is how a project whose deliveries were accepted before
// briefs existed gets one without re-deciding anything.
//
// 409 when the project has no accepted delivery with a brief. Nothing is
// written in that case: an empty `projects.narrative` already says that nothing
// has been accepted, and storing a sentence to that effect would only add a
// value with no source (see brief.ErrNoAcceptedDeliveries).
func (s *Server) handleWriteProjectNarrative(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid project id")))
		return
	}

	row, err := brief.WriteProject(r.Context(), s.store, s.briefConfig(), id)
	if errors.Is(err, brief.ErrNoAcceptedDeliveries) {
		writeJSON(w, http.StatusConflict, errBody(fmt.Errorf(
			"project %q has no accepted delivery with a brief — accept a delivery, or write its brief first (POST /api/runs/{id}/brief)", id)))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	resp := narrativeResponse{Brief: toBriefView(row)}
	if p, perr := s.store.GetProject(r.Context(), id); perr == nil {
		resp.Narrative = p.Narrative
	}
	if doc, derr := brief.CollectProject(r.Context(), s.store, id); derr == nil {
		resp.Deliveries = len(doc.Sources)
	}
	writeJSON(w, http.StatusCreated, resp)
}

// ---- the write hook -------------------------------------------------------

// briefConfig resolves the paths and the switch one brief needs.
//
// briefCodex is the engine seam: nil in production, so the real adapter runs.
// Tests set it (see export_test.go) for the same reason the engine's own tests
// fake an agent — a test that spawns codex is a test that bills money and
// depends on a subprocess.
func (s *Server) briefConfig() brief.Config {
	return brief.Config{
		SchemasDir: filepath.Join(s.cfg.PlatformRoot, "schemas"),
		TargetRoot: s.cfg.TargetRoot,
		RunsDir:    s.cfg.RunsDir,
		Enabled:    s.cfg.Briefs,
		Codex:      s.briefCodex,
	}
}

// briefDeliveryDecision is the hook both outcome handlers call once a verdict
// is on record: write the delivery's brief, then regenerate the narrative of
// the project it belongs to.
//
// runID is the attempt the verdict is about — for a journey-grain decision, the
// newest attempt, which is the one the delivery currently IS.
func (s *Server) briefDeliveryDecision(runID string, decision store.Decision) {
	if runID == "" || s.store == nil {
		return
	}
	cfg := s.briefConfig()
	st := s.store

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[brief] panic writing the delivery brief for %s: %v", runID, rec)
			}
		}()
		// Detached: the request is over the moment the verdict is written.
		ctx, cancel := context.WithTimeout(context.Background(), briefTimeout)
		defer cancel()

		doc, err := brief.Collect(ctx, st, runID, store.BriefSubjectDelivery)
		if err != nil {
			log.Printf("[brief] delivery %s: collect: %v", runID, err)
			return
		}
		// A JOURNEY-grain verdict is recorded against the delivery, not the run,
		// so the run's own outcome lookup finds nothing. Carry the decision that
		// actually happened rather than letting the brief say "awaiting
		// decision" about a delivery somebody just decided.
		if doc.Delivery == nil {
			doc.Delivery = brief.DeliveryFrom(decision)
		}
		if decision.Project != "" {
			doc.Project = decision.Project
		}
		if _, err := brief.Write(ctx, st, cfg, doc); err != nil {
			log.Printf("[brief] delivery %s: write: %v", runID, err)
			return
		}

		// The project narrative is downstream of acceptance and of nothing else:
		// a rejected delivery is a real outcome with its own brief, but it is not
		// something the project built.
		if decision.Decision != outcomeAccepted || doc.Project == "" {
			return
		}
		if _, err := brief.WriteProject(ctx, st, cfg, doc.Project); err != nil {
			log.Printf("[brief] project %s: %v", doc.Project, err)
		}
	}()
}

// newestAttempt is the run a journey-grain verdict is about: the delivery's
// current attempt. Empty when the journey somehow has none, in which case no
// brief is written rather than one about an invented run.
func newestAttempt(j journey.Journey) string {
	if len(j.Attempts) == 0 {
		return ""
	}
	return j.Attempts[0].RunID
}
