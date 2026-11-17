package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

const maxJourneyInbox = 50

func (s *Server) deriveJourneys(ctx context.Context, filter store.RunFilter) ([]journey.Journey, error) {
	filter.TopLevel = true
	if filter.Limit == 0 {
		filter.Limit = store.JourneyRunLimit
	}
	runs, err := s.store.ListRuns(ctx, filter)
	if err != nil {
		return nil, err
	}
	buckets := journey.Group(runs)
	if len(buckets) == 0 {
		return []journey.Journey{}, nil
	}
	namer, _ := s.store.NewRecipeNamer(ctx)
	for i := range buckets {
		buckets[i].RecipeName = namer.Name(buckets[i].Recipe)
	}

	runIDs := journey.RunIDs(buckets)
	ids := make([]string, 0, len(runIDs)+len(buckets))
	for _, id := range runIDs {
		ids = append(ids, store.OutcomeDecisionID(id))
	}
	for _, b := range buckets {
		ids = append(ids, store.JourneyOutcomeDecisionID(b.Key))
		for _, r := range b.Runs {
			ids = append(ids, store.JourneyAttemptOutcomeDecisionID(b.Key, r.RunID))
		}
	}
	decisions, err := s.store.DecisionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	ev := journey.Evidence{RunOutcomes: decisions}

	out := make([]journey.Journey, 0, len(buckets))
	for _, b := range buckets {
		e := ev
		if d, ok := decisions[store.JourneyOutcomeDecisionID(b.Key)]; ok {
			e.JourneyOutcome = &d
		}
		// Buckets are newest-first, including explicit continuation order.
		for _, r := range b.Runs {
			if d, ok := decisions[store.JourneyAttemptOutcomeDecisionID(b.Key, r.RunID)]; ok {
				e.JourneyOutcome = &d
				break
			}
		}
		out = append(out, journey.Build(b, e))
	}
	return out, nil
}
func (s *Server) handleListJourneys(w http.ResponseWriter, r *http.Request) {
	filter := store.RunFilter{Project: projectFilter(r)}
	if lim := r.URL.Query().Get("limit"); lim != "" {
		filter.Limit, _ = strconv.Atoi(lim)
	}
	journeys, err := s.deriveJourneys(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, journeys)
}
func (s *Server) handleGetJourney(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !journey.ValidKey(key) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid journey key")))
		return
	}
	j, err := s.findJourney(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if j == nil {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("journey %q not found", key)))
		return
	}
	writeJSON(w, http.StatusOK, j)
}
func (s *Server) findJourney(ctx context.Context, key string) (*journey.Journey, error) {
	journeys, err := s.deriveJourneys(ctx, store.RunFilter{})
	if err != nil {
		return nil, err
	}
	j, ok := journey.Find(journeys, key)
	if !ok {
		return nil, nil
	}
	return &j, nil
}
func (s *Server) handleRecordJourneyOutcome(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !journey.ValidKey(key) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid journey key")))
		return
	}

	var body struct {
		RunID   string `json:"runId"`
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if strings.TrimSpace(body.RunID) == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'runId' must name the latest attempt you reviewed")))
		return
	}
	verdict := strings.TrimSpace(body.Verdict)
	if !validOutcomeVerdict(verdict) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf(
			"'verdict' must be one of %q, %q, %q", outcomeAccepted, outcomeChangesRequested, outcomeRejected)))
		return
	}
	note := strings.TrimSpace(body.Note)
	if len(note) > maxOutcomeNote {
		note = note[:maxOutcomeNote]
	}

	j, err := s.findJourney(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if j == nil {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("journey %q not found", key)))
		return
	}
	if len(j.Attempts) == 0 || body.RunID != j.Attempts[0].RunID {
		writeJSON(w, http.StatusConflict, errBody(fmt.Errorf("delivery has a different latest attempt — refresh and review it before recording a decision")))
		return
	}
	latest := j.Attempts[0]
	if j.Stage == journey.StageBuilding || j.Stage == journey.StageVerifying {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("delivery is %s — an outcome can only be recorded once the current attempt has finished", j.Stage),
			"stage": j.Stage,
		})
		return
	}
	if verdict == outcomeAccepted && latest.Rehearsal {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     store.RehearsalSentence,
			"rehearsal": true,
			"attempts":  j.AttemptCount,
		})
		return
	}
	if j.RecordedOutcome != nil && latest.DecisionCovered {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "this attempt already has a delivery outcome",
			"outcome": j.RecordedOutcome,
		})
		return
	}
	refs := make([]string, 0, len(j.Attempts)+1)
	refs = append(refs, "journey:"+key)
	for _, a := range j.Attempts {
		refs = append(refs, "run:"+a.RunID)
	}

	now := time.Now().UTC()
	d, err := s.store.CreateDecision(r.Context(), store.Decision{
		ID:        store.JourneyAttemptOutcomeDecisionID(key, latest.RunID),
		Title:     "Delivery · " + j.Name,
		Status:    "accepted",
		Context:   journeyOutcomeContext(*j),
		Decision:  verdict,
		Why:       note,
		Tags:      []string{store.OutcomeTag, verdict, "journey"},
		Refs:      refs,
		Project:   j.Project,
		Source:    "journey:" + key,
		DecidedAt: &now,
	})
	if errors.Is(err, store.ErrExists) {
		existing := s.lookupJourneyOutcome(r.Context(), key, latest.RunID)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "this attempt already has a delivery outcome",
			"outcome": existing,
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if verdict == outcomeAccepted {
		s.learnFromAcceptedDelivery(*j)
	}
	s.briefDeliveryDecision(newestAttempt(*j), d)

	writeJSON(w, http.StatusCreated, journeyOutcomeBody{Key: key, Outcome: journeyOutcome(d, latest.RunID)})
}

type journeyOutcomeBody struct {
	Key string `json:"key"`
	*journey.Outcome
}

func journeyOutcomeContext(j journey.Journey) string {
	ctx := fmt.Sprintf("Delivery %q (%s) after %d attempt(s).", j.Name, j.DAG, j.AttemptCount)
	if j.RecipeName != "" {
		ctx += " Recipe: " + j.RecipeName + "."
	} else if j.Recipe != "" {
		ctx += " Recipe: " + j.Recipe + "."
	}
	if j.Criteria > 0 {
		ctx += fmt.Sprintf(" Latest attempt passed %d/%d configured checks.", j.Proven, j.Criteria)
	}
	if len(j.Attempts) > 0 {
		ctx += " Latest run: " + j.Attempts[0].RunID + " (" + j.Attempts[0].State + ")."
	}
	return ctx
}
func (s *Server) lookupJourneyOutcome(ctx context.Context, key, runID string) *journeyOutcomeBody {
	d, err := s.store.GetDecision(ctx, store.JourneyAttemptOutcomeDecisionID(key, runID))
	if err != nil {
		return nil
	}
	b := journeyOutcomeBody{Key: key, Outcome: journeyOutcome(d, runID)}
	return &b
}
func journeyOutcome(d store.Decision, runID string) *journey.Outcome {
	at := d.CreatedAt
	if d.DecidedAt != nil {
		at = *d.DecidedAt
	}
	return &journey.Outcome{
		Verdict:    d.Decision,
		Note:       d.Why,
		DecidedBy:  outcomeDecider(d),
		DecidedAt:  at,
		DecisionID: d.ID,
		Scope:      "journey",
		RunID:      runID,
	}
}

type runJourneyRef struct {
	Key           string           `json:"key"`
	Name          string           `json:"name"`
	Stage         journey.Stage    `json:"stage"`
	Maturity      journey.Maturity `json:"maturity"`
	AttemptCount  int              `json:"attemptCount"`
	Attempt       int              `json:"attempt"`
	Criteria      int              `json:"criteria"`
	Proven        int              `json:"proven"`
	StandinProven int              `json:"standinProven,omitempty"`
	ProvenAttempt int              `json:"provenAttempt,omitempty"`
	Superseded    bool             `json:"superseded"`
	// SignOff is the policy this run was launched under, so the run page can say
	// whether anyone is being asked to sign it off.
	SignOff         string            `json:"signOff,omitempty"`
	Outcome         *journey.Outcome  `json:"outcome,omitempty"`
	RecordedOutcome *journey.Outcome  `json:"recordedOutcome,omitempty"`
	Coverage        *journey.Coverage `json:"coverage,omitempty"`
	Rehearsal       bool              `json:"rehearsal,omitempty"`
}

func outcomeForAttempt(j journey.Journey, a journey.Attempt) *journey.Outcome {
	if a.DecisionCovered && j.RecordedOutcome != nil {
		return j.RecordedOutcome
	}
	return a.Outcome
}
func (s *Server) journeyForRun(ctx context.Context, run store.Run) *runJourneyRef {
	if run.ParentRun != "" && store.SDKContinuationSource(run) == "" {
		return nil
	}
	journeys, err := s.deriveJourneys(ctx, store.RunFilter{
		Project: store.StrPtr(run.Project),
		DAG:     run.DAG,
	})
	if err != nil {
		return nil
	}
	for _, j := range journeys {
		for _, a := range j.Attempts {
			if a.RunID != run.RunID {
				continue
			}
			return &runJourneyRef{
				Key:             j.Key,
				Name:            j.Name,
				Stage:           j.Stage,
				Maturity:        j.Maturity,
				AttemptCount:    j.AttemptCount,
				Attempt:         a.Number,
				Criteria:        j.Criteria,
				Proven:          j.Proven,
				StandinProven:   j.StandinProven,
				ProvenAttempt:   j.ProvenAttempt,
				Superseded:      a.Superseded,
				SignOff:         a.SignOff,
				Outcome:         outcomeForAttempt(j, a),
				RecordedOutcome: j.RecordedOutcome,
				Coverage:        j.Coverage,
				Rehearsal:       a.Rehearsal,
			}
		}
	}
	return nil
}
