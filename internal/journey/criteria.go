package journey

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// maxFailureReason bounds the reason in BYTES. It was 200 RUNES, which cut the
// first FAIL finding mid-sentence while the Inbox said it was showing "the
// whole finding, not a card-sized slice of it" (inbox_context.go). The bound
// that remains is a storage-and-transport one, not an editorial one: a reason
// is only shortened when it is genuinely enormous, and it then says so.
const maxFailureReason = 4096

// CriterionStatus describes an explicitly configured check, never ordinary file
// completion. Model reviews remain fallible; Proven means its check passed.
type CriterionStatus struct {
	ID         string
	Title      string
	Basis      string
	Derived    bool
	ProofNode  string
	ProofState string
	Proven     bool
	Rehearsal  bool
}

func (c CriterionStatus) ProofLabel() string {
	if c.Rehearsal && c.Proven {
		return "check passed in rehearsal"
	}
	if c.Proven {
		return "check passed"
	}
	if c.ProofState == "" || c.ProofState == "pending" {
		return "not checked"
	}
	return c.ProofState
}

type SDKRunEvidence struct {
	Criteria      []CriterionStatus
	CurrentNode   string
	FailureReason string
	Phase         string
	ContinuedFrom string
}

// InspectRun uses only this run's frozen request and committed SDK checkpoint.
// Descendant runs, graph node names and model-authored criteria are not evidence.
func InspectRun(run store.Run) SDKRunEvidence {
	out := SDKRunEvidence{ContinuedFrom: store.SDKContinuationSource(run)}
	var launch struct {
		Runtime string `json:"runtime"`
		Request struct {
			Workflow string   `json:"workflow"`
			Check    []string `json:"check"`
			Nodes    []struct {
				ID         string   `json:"id"`
				Title      string   `json:"title"`
				Kind       string   `json:"kind"`
				Check      []string `json:"check"`
				Acceptance string   `json:"acceptance"`
			} `json:"nodes"`
		} `json:"request"`
	}
	var doc struct {
		SDK struct {
			Phase        string   `json:"phase"`
			CurrentNode  string   `json:"currentNode"`
			Error        string   `json:"error"`
			Findings     []string `json:"findings"`
			Verification string   `json:"verification"`
			Nodes        []struct {
				ID           string   `json:"id"`
				Phase        string   `json:"phase"`
				Verification string   `json:"verification"`
				Findings     []string `json:"findings"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	if json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" || json.Unmarshal(run.Doc, &doc) != nil {
		return out
	}
	out.Phase = doc.SDK.Phase
	out.CurrentNode = doc.SDK.CurrentNode
	reason := strings.TrimSpace(doc.SDK.Error)
	if reason == "" {
		reason = strings.Join(doc.SDK.Findings, "; ")
	}
	if launch.Request.Workflow == "node-workflow" {
		states := map[string]int{}
		for i, node := range doc.SDK.Nodes {
			if _, exists := states[node.ID]; exists {
				return SDKRunEvidence{ContinuedFrom: out.ContinuedFrom}
			}
			states[node.ID] = i
		}
		for _, node := range launch.Request.Nodes {
			i, found := states[node.ID]
			phase, verification := "pending", ""
			if found {
				phase = doc.SDK.Nodes[i].Phase
				verification = doc.SDK.Nodes[i].Verification
				if node.ID == out.CurrentNode {
					if reason == "" {
						reason = strings.Join(doc.SDK.Nodes[i].Findings, "; ")
					}
					if phase == "verifying" || node.Kind == "check" && phase == "running" {
						out.Phase = "verifying"
					}
				}
			}
			checkedTask := node.Kind == "task" && (len(node.Check) > 0 || node.Acceptance != "")
			if node.Kind != "check" && !checkedTask {
				continue
			}
			passed := phase == "passed" && (node.Kind == "check" || verification == "passed")
			basis := "program_check"
			if node.Kind == "check" {
				basis = "model_review"
			}
			out.Criteria = append(out.Criteria, CriterionStatus{ID: node.ID, Title: node.Title, Basis: basis, ProofNode: node.ID, ProofState: phase, Proven: passed, Rehearsal: store.IsRehearsal(run)})
		}
	} else if launch.Request.Workflow == "app-ideator" || launch.Request.Workflow == "checked-task" && len(launch.Request.Check) > 0 {
		out.Criteria = []CriterionStatus{{ID: "delivery", Title: "Configured acceptance check", Basis: "program_check", ProofNode: "delivery", ProofState: doc.SDK.Phase, Proven: doc.SDK.Phase == "passed" && doc.SDK.Verification == "passed", Rehearsal: store.IsRehearsal(run)}}
	}
	out.FailureReason = boundReason(reason)
	return out
}

// boundReason collapses whitespace and caps the reason at maxFailureReason
// bytes, cutting only on a rune boundary. The ellipsis is appended ONLY when
// text was actually removed, so a reader can tell a complete finding from a
// clipped one instead of seeing "…" on every failure.
func boundReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) <= maxFailureReason {
		return reason
	}
	cut := maxFailureReason
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut] + "…"
}

func CriteriaStatusForRun(run store.Run) []CriterionStatus { return InspectRun(run).Criteria }
