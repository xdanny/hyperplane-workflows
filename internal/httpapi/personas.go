package httpapi

// personas.go — the registered charters, and the reviews they record.
//
// Read-only on the charters themselves, deliberately: a persona is amended in
// git, in its own PR, because changing IDENTITY or RULES invalidates comparison
// with every past run. An API that let a charter be edited would quietly make
// the whole roster unversioned. `make seed` is the only writer.
//
// The one write here is a REVIEW: POST a target, get a recorded, advisory
// verdict. It costs one billed codex call, so it happens only when somebody
// asks — there is no timer behind it (see personas/README.md for what remains).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/xdanny/hyperplane-workflows/internal/persona"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// personaView is the wire shape of a registered charter.
//
// The full charter markdown is NOT included in the list response — it runs to
// hundreds of lines per persona and no surface renders it — but the detail
// response carries it, so "what exactly is this reviewer briefed to do?" is
// answerable without leaving the app.
type personaView struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Bar        string             `json:"bar,omitempty"`
	Competence persona.Competence `json:"competence"`
	Version    string             `json:"version,omitempty"`
	Source     string             `json:"source,omitempty"`
	Charter    string             `json:"charter,omitempty"`
	// Reviews is how many verdicts this persona has recorded. It is the honest
	// answer to "has this reviewer ever actually been run?".
	Reviews int `json:"reviews"`
	// CompetentFor is set only on a ?competentFor= query: why this persona
	// matched. A routing answer nobody can check is the same problem as a
	// verdict nobody can check.
	CompetentFor string `json:"competentFor,omitempty"`
}

func toPersonaView(p store.Persona, counts map[string]int, withCharter bool) personaView {
	var comp persona.Competence
	_ = store.DecodePersonaCompetence(p.Competence, &comp)
	v := personaView{
		ID: p.ID, Name: p.Name, Bar: p.Bar, Competence: comp,
		Version: p.Version, Source: p.Source, Reviews: counts[p.ID],
	}
	if withCharter {
		v.Charter = p.Charter
	}
	return v
}

// handleListPersonas returns the roster.
//
// GET /api/personas
// GET /api/personas?competentFor=skill:<id>  — the routing hook spec AI-12's
// auto-trigger will call. It answers "who is qualified to judge this?" using
// only what a charter NAMED (see persona.Match); a persona whose competence is
// free-text prose about a domain is listed in the roster but never claimed for
// a skill it did not name.
func (s *Server) handleListPersonas(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListPersonas(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	counts := s.personaReviewCounts(r)

	competentFor := r.URL.Query().Get("competentFor")
	if competentFor == "" {
		out := make([]personaView, 0, len(rows))
		for _, p := range rows {
			out = append(out, toPersonaView(p, counts, false))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	target, err := persona.ParseTarget(competentFor)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	out := make([]personaView, 0, len(rows))
	for _, p := range rows {
		view := toPersonaView(p, counts, false)
		if ok, why := persona.Match(view.Competence, target); ok {
			view.CompetentFor = why
			out = append(out, view)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetPersona returns one charter in full.
func (s *Server) handleGetPersona(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPersona(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("persona %q is not registered — run `make seed` to import personas/", r.PathValue("id"))))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, toPersonaView(p, s.personaReviewCounts(r), true))
}

// personaReviewCounts tallies recorded reviews per persona. Best-effort: a
// failure here costs a count, never the roster.
func (s *Server) personaReviewCounts(r *http.Request) map[string]int {
	counts := map[string]int{}
	recs, err := s.store.ListPersonaReviews(r.Context(), "", 0)
	if err != nil {
		return counts
	}
	for _, rec := range recs {
		if rev, err := persona.ParseReview(rec.Doc); err == nil {
			counts[rev.Persona]++
		}
	}
	return counts
}

// handleListPersonaReviews returns recorded verdicts.
//
// GET /api/persona-reviews[?target=skill:<id>][&limit=n]
//
// Insert-only and multiple-per-target by design, newest first: two personas
// disagreeing, or one persona re-reading after a change, is the evidence a
// human wants both halves of.
func (s *Server) handleListPersonaReviews(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	// ?run=<id> is the same question a run page asks — "what has anyone said
	// about THIS run?" — without the caller having to know how a target string
	// is spelled.
	if run := r.URL.Query().Get("run"); run != "" {
		if !safeID(run) {
			writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
			return
		}
		runTarget := persona.Target{Kind: persona.TargetRun, ID: run}.String()
		if target != "" && target != runTarget {
			writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("give either 'run' or 'target', not both")))
			return
		}
		target = runTarget
	}
	if target != "" {
		if _, err := persona.ParseTarget(target); err != nil {
			writeJSON(w, http.StatusBadRequest, errBody(err))
			return
		}
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		limit, _ = strconv.Atoi(l)
	}
	recs, err := s.store.ListPersonaReviews(r.Context(), target, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	out := make([]persona.Review, 0, len(recs))
	for _, rec := range recs {
		rev, err := persona.ParseReview(rec.Doc)
		if err != nil {
			continue
		}
		out = append(out, rev)
	}
	writeJSON(w, http.StatusOK, out)
}

// reviewRequest is the POST body.
type reviewRequest struct {
	// Target is "run:<runId>" or "skill:<skillId>".
	Target string `json:"target"`
	// Model optionally overrides the engine model, including the local engine
	// ("local", "codex-local", "local:<model>").
	Model string `json:"model,omitempty"`
}

// handlePersonaReview runs one persona against one target and records the
// verdict. POST /api/personas/{id}/review
//
// It is synchronous: the caller asked for a judgement and a judgement is what
// comes back. That costs a billed codex call and up to six minutes, which is
// the honest price of the thing being requested — unlike automatic extraction,
// nobody is waiting on this to record something else.
func (s *Server) handlePersonaReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.store.GetPersona(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("persona %q is not registered — run `make seed` to import personas/", id)))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	var req reviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	target, err := persona.ParseTarget(req.Target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}

	ev, status, err := s.personaEvidence(r, target)
	if err != nil {
		writeJSON(w, status, errBody(err))
		return
	}

	cfg := persona.Config{
		SchemasDir: filepath.Join(s.cfg.PlatformRoot, "schemas"),
		TargetRoot: s.cfg.TargetRoot,
		RunDir:     s.personaReviewDir(target),
		Codex:      s.personaCodex,
	}
	// The engine call runs on a context detached from the request: the review is
	// a billed codex call that takes minutes, and a client that times out or
	// hangs up must not cancel it mid-flight — the spend has happened either
	// way, and the verdict is evidence worth recording. The call stays bounded
	// by the adapter's own review timeout.
	rev, err := persona.Run(context.WithoutCancel(r.Context()), s.store, cfg, persona.FromStore(p), ev, persona.Options{Model: req.Model})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

// personaReviewDir is where the review's codex log lands: inside the run's own
// folder for a run target, so the review is filed with the thing it judged.
func (s *Server) personaReviewDir(t persona.Target) string {
	if s.cfg.RunsDir == "" {
		return ""
	}
	if t.Kind == persona.TargetRun {
		if dir, err := safeRunDir(s.cfg.RunsDir, t.ID); err == nil {
			return dir
		}
		return ""
	}
	return filepath.Join(s.cfg.RunsDir, "persona-reviews")
}

// personaEvidence assembles the material for a target, returning the HTTP
// status to use when it cannot.
func (s *Server) personaEvidence(r *http.Request, t persona.Target) (persona.Evidence, int, error) {
	switch t.Kind {
	case persona.TargetSkill:
		ev, err := persona.SkillEvidence(r.Context(), s.store, t.ID)
		if errors.Is(err, store.ErrNotFound) {
			return ev, http.StatusNotFound, fmt.Errorf("skill %q not found", t.ID)
		}
		if err != nil {
			return ev, http.StatusInternalServerError, err
		}
		return ev, http.StatusOK, nil
	case persona.TargetRun:
		return s.runReviewEvidence(r, t.ID)
	}
	return persona.Evidence{}, http.StatusBadRequest, fmt.Errorf("unknown target kind %q", t.Kind)
}

// runReviewEvidence collects SDK checkpoints, output evidence, files, and session logs.
func (s *Server) runReviewEvidence(r *http.Request, runID string) (persona.Evidence, int, error) {
	if !safeID(runID) {
		return persona.Evidence{}, http.StatusBadRequest, fmt.Errorf("invalid run id")
	}
	run, err := s.store.GetRun(r.Context(), runID)
	if errors.Is(err, store.ErrNotFound) {
		return persona.Evidence{}, http.StatusNotFound, fmt.Errorf("run %q not found", runID)
	}
	if err != nil {
		return persona.Evidence{}, http.StatusInternalServerError, err
	}
	facts := persona.RunFacts{Run: run, Failures: sdkFailureEvidence(run)}
	facts.Logs = persona.ReadLogTails(s.cfg.RunsDir, []persona.NodeTail{{RunID: runID, NodeID: "delivery"}})
	facts.Artifacts, facts.ArtifactsTruncated = persona.ListArtifacts(s.cfg.RunsDir, runID)
	// The deliverable itself, not only its name. A reviewer that was handed the
	// step list alone reported "the full note is unavailable to assess" about a
	// run whose note sat in the outputs folder the whole time (S-15).
	facts.Documents = persona.ReadProducedDocuments(s.cfg.RunsDir, run)

	return persona.RunEvidence(facts), http.StatusOK, nil
}
