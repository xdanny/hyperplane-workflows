package sdkplanning

import (
	"context"
	"encoding/json"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCancelledSDKProposalStopsTheOwnedProcess(t *testing.T) {
	exe, err := filepath.Abs("../codex/testdata/plan")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", exe)
	t.Setenv("SDK_FIXTURE_MODE", "hang")
	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	t.Setenv("SDK_FIXTURE_CALLS", calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Propose(ctx, root, "Choose a cycling app"); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(calls); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(calls); err != nil {
		t.Fatal("fixture never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled proposal succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SDK proposal process survived cancellation")
	}
}

func TestInvalidBriefNeverStartsSDK(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	for _, brief := range []string{" ", strings.Repeat("x", 16001)} {
		if _, err := Propose(context.Background(), root, brief); err == nil {
			t.Fatal("invalid brief accepted")
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid input created planning files")
	}
}

func TestProposalPreservesExplicitDependencyBranches(t *testing.T) {
	raw := []byte(`{"name":"Independent audits","nodes":[{"id":"join","title":"Join","instruction":"Combine audits","kind":"document","dependsOn":["left","right"]},{"id":"left","title":"Left","instruction":"First perspective","kind":"document","dependsOn":[]},{"id":"right","title":"Right","instruction":"Second perspective","kind":"document","dependsOn":[]}],"unsupported":[],"recipe":"","slots":[]}`)
	plan, err := parseProposal(t.Context(), raw, "Compare independent perspectives")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes[0].DependsOn) != 2 || plan.Nodes[1].DependsOn == nil || len(plan.Nodes[1].DependsOn) != 0 {
		t.Fatalf("proposal lost branches: %+v", plan.Nodes)
	}
}

func TestShippedRecipeProposalsPreserveCompleteDefinition(t *testing.T) {
	exe, err := filepath.Abs("../codex/testdata/plan")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", exe)
	t.Setenv("SDK_FIXTURE_MODE", "builtin-pass")
	for _, recipe := range checkedRecipes() {
		t.Run(recipe.Name, func(t *testing.T) {
			// Use concrete template examples now that SDK input semantics are
			// validated before a proposal is returned.
			values := map[string]string{}
			for _, slot := range recipe.ContextSlots {
				values[slot.ID] = slot.Example
			}

			raw, _ := json.Marshal(values)
			t.Setenv("SDK_FIXTURE_INPUTS", string(raw))
			t.Setenv("SDK_FIXTURE_RECIPE", recipe.Name)
			plan, err := Propose(context.Background(), t.TempDir(), "Use these declarations: "+string(raw))
			if err != nil {
				t.Fatal(err)
			}
			filled, err := store.FillSlots(recipe.Spec, recipe.ContextSlots, values)
			if err != nil {
				t.Fatal(err)
			}
			var expected store.SDKWorkflow
			if err = json.Unmarshal(filled, &expected); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(expected.Nodes)
			got, _ := json.Marshal(plan.Nodes)
			if string(got) != string(want) {
				t.Fatal("proposal dropped or rewrote shipped stages, criteria or input declarations")
			}
			if !strings.Contains(plan.Instruction, expected.Instruction) {
				t.Fatal("proposal lost the recipe's context and constraints")
			}
		})
	}
}

func TestProposedTableInputsCannotSilentlyLoseBusinessDefinitions(t *testing.T) {
	exe, err := filepath.Abs("../codex/testdata/plan")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", exe)
	t.Setenv("SDK_FIXTURE_MODE", "builtin-pass")
	t.Setenv("SDK_FIXTURE_RECIPE", "data-pipeline")
	cases := []struct {
		name, measures string
		valid          bool
	}{
		{"complete", `[{"measure":"net","definition":"Sum amounts excluding test orders","unit":"exact","tolerance":"0"}]`, true},
		{"missing definition", `[{"measure":"net","unit":"exact","tolerance":"0"}]`, false},
		{"unrecognized business column", `[{"measure":"net","definition":"Sum amounts","exclusion":"test orders","unit":"exact","tolerance":"0"}]`, false},
		{"invalid table shape", `{"measure":"net","definition":"Sum amounts"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inputs := map[string]string{"sources": "/declared/orders.csv", "targetStore": "SQLite", "grain": "daily orders", "keyColumns": "order_day", "measures": c.measures, "maxLag": "0 days behind source", "safeUse": "synthetic; no emails"}
			raw, _ := json.Marshal(inputs)
			t.Setenv("SDK_FIXTURE_INPUTS", string(raw))
			plan, err := Propose(context.Background(), t.TempDir(), "Use these declarations: "+string(raw))
			if (err == nil) != c.valid {
				t.Fatalf("valid=%v error=%v", c.valid, err)
			}
			if c.valid && !strings.Contains(plan.Nodes[0].Inputs["measures"], "excluding test orders") {
				t.Fatal("business definition was lost")
			}
		})
	}
}

func TestGeneralFileWorkProposalUsesTaskWithoutInventingChecks(t *testing.T) {
	raw := []byte(`{"name":"Prepare files","recipe":"","slots":[],"nodes":[{"id":"write","title":"Write a CSV","instruction":"Create orders.csv using the rows in the goal.","kind":"task"}],"unsupported":[]}`)
	plan, err := parseProposal(context.Background(), raw, "Create orders.csv with one row: id=1, amount=5")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0].Kind != "task" || plan.Nodes[0].Check != nil || plan.Nodes[0].Acceptance != "" || plan.Nodes[0].Access != nil {
		t.Fatalf("unexpected task proposal: %+v", plan)
	}
	var schema struct{ Properties map[string]json.RawMessage }
	if err := json.Unmarshal(proposalSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schema.Properties["nodes"]), `"task"`) {
		t.Fatal("schema cannot propose general work")
	}
}

func TestProposedTaskCannotDeclareChecksOrPermissions(t *testing.T) {
	for _, field := range []string{`"check":[]`, `"check":["true"]`, `"check":null`, `"acceptance":"data-quality-profile"`, `"acceptance":""`, `"inputs":{}`, `"access":null`, `"access":{"hosts":["example.com"]}`, `"toolchain":false`, `"sideEffect":true`} {
		t.Run(field, func(t *testing.T) {
			raw := []byte(`{"name":"Prepare files","recipe":"","slots":[],"nodes":[{"id":"write","title":"Write","instruction":"Write a file","kind":"task",` + field + `}],"unsupported":[]}`)
			if _, err := parseProposal(context.Background(), raw, "Write a file"); err == nil {
				t.Fatal("accepted model-supplied execution configuration")
			}
		})
	}
}

func TestMLProposalRetainsDeclaredSourcesAndDoesNotGrantPermissions(t *testing.T) {
	values := []slotValue{{"task", "Evaluate baseline"}, {"predictionTarget", "Future binary outcome"}, {"dataDescription", "/data/accounts.csv; holdout hash as supplied"}, {"splitPolicy", "Disjoint entity IDs"}, {"successMetric", `[{"metric":"AUROC","threshold":">= 0.72"}]`}}
	brief := "Evaluate a baseline with /data/accounts.csv; holdout hash as supplied. Disjoint entity IDs. AUROC >= 0.72."
	_, nodes, err := expandRecipe(context.Background(), brief, "ml-engineering", values)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 7 || nodes[2].Kind != "approval" || nodes[3].Acceptance != "prediction-metrics" {
		t.Fatal("ML stages or trusted metric check lost")
	}
	for _, node := range nodes {
		if node.Access != nil || node.Toolchain != nil || node.SideEffect != nil {
			t.Fatal("proposal granted execution permissions")
		}
	}
	values[2].Value = "invented holdout sha256 1234"
	if _, _, err := expandRecipe(context.Background(), brief, "ml-engineering", values); err == nil {
		t.Fatal("invented ML source identity accepted")
	}
}

// What Hyperplane can and cannot do is the application's statement, not a model
// judgment: a schedule in the goal used to end the conversation with wording
// that changed run to run and left nothing to do about it.
func TestScheduleFindingIsTheApplicationsOwnSentence(t *testing.T) {
	steps := `"nodes":[{"id":"write","title":"Write the brief","instruction":"Summarise the week","kind":"document","dependsOn":[]}]`
	raw := []byte(`{"name":"Weekly brief","recipe":"","slots":[],` + steps + `,"unsupported":["Scheduling a weekly run is not supported."]}`)
	plan, err := parseProposal(t.Context(), raw, "Every Monday, write a competitor brief")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unsupported) != 1 || plan.Unsupported[0] != ScheduleFinding {
		t.Fatalf("the schedule finding is not the application's own sentence: %q", plan.Unsupported)
	}
	// The steps survive: a person can still run this workflow themselves.
	if len(plan.Nodes) != 1 {
		t.Fatalf("a schedule in the goal suppressed the proposed steps: %+v", plan.Nodes)
	}

	// A goal with no recurrence in it gets no scheduling sentence, and a subject
	// that merely mentions a schedule is not a request to recur.
	for _, brief := range []string{"Write a competitor brief", "Summarise the release schedule"} {
		quiet, err := parseProposal(t.Context(), []byte(`{"name":"Brief","recipe":"","slots":[],`+steps+`,"unsupported":[]}`), brief)
		if err != nil {
			t.Fatal(err)
		}
		if len(quiet.Unsupported) != 0 {
			t.Fatalf("%q produced a scheduling finding: %q", brief, quiet.Unsupported)
		}
	}
}

// Research reaches live sources by design. Reporting that as a missing grant
// sent a person to configure something that is already true.
func TestResearchStepsNeverReportMissingNetworkAccess(t *testing.T) {
	raw := []byte(`{"name":"Market scan","recipe":"","slots":[],"nodes":[{"id":"scan","title":"Scan the market","instruction":"Find current competitors","kind":"research","dependsOn":[]}],"unsupported":["Network access must be configured for the research step.","An external app is required to publish the result."]}`)
	plan, err := parseProposal(t.Context(), raw, "Scan the market")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unsupported) != 1 || !strings.Contains(plan.Unsupported[0], "external app") {
		t.Fatalf("network access was still reported as missing for research: %q", plan.Unsupported)
	}
}
