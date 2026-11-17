package store

// declarations_round3_test.go — the groups this round adds, and the consumers
// that were the whole point of adding them.
//
// Every test below is one audit finding written as an assertion. The shape they
// share: a fact was typed into a form once per run because the project had
// nowhere to say it, or was said once on the project and then read by nothing.

import (
	"strings"
	"testing"
)

// round3Declarations is a project that declares the three operational groups and
// a dataset's load semantics, as the form would send them.
const round3Declarations = `{
  "purpose":{"owner":"the analytics lead"},
  "datasets":[{
    "id":"yellow-trips","name":"NYC TLC yellow-cab monthly trip drops",
    "location":"data/yellow_tripdata_2025-*.csv","format":"csv","kind":"fact",
    "loadSemantics":{"mode":"incremental","watermarkColumn":"tpep_pickup_datetime",
                     "lateArrivalWindow":"45 days",
                     "restatementRule":"a late trip restates the month it was taken in"}
  }],
  "schedule":{"expectedCadence":"once a month, when the TLC drop lands",
              "deadline":"09:00 on the fifth working day","timezone":"America/New_York",
              "triggeredBy":"the analytics lead, by hand","pageRef":"analytics-oncall"},
  "warehouse":{"engine":"postgres","databaseSchema":"analytics",
               "connectionRef":"ANALYTICS_DSN",
               "roleRefs":{"read":"ANALYTICS_READER_ROLE","write":"ANALYTICS_WRITER_ROLE"},
               "access":"write"},
  "lineage":{"repoPath":"analytics/models/*.sql","runCommand":"analytics/run.sh",
             "manifestPath":"analytics/manifest.json",
             "prConvention":"one pull request per model, reviewed by the analytics lead"},
  "stakeholders":[{"role":"approves the monthly numbers","name":"the analytics lead","contactRef":"analytics-lead"},
                  {"role":"reads the scorecard","name":"data engineering","contactRef":"data-eng"}],
  "delivery":{"format":"a one-page markdown summary plus the CSV","recipientRef":"data-eng"}
}`

func round3(t *testing.T) ProjectDeclarations {
	t.Helper()
	d, err := ParseDeclarations(JSON(round3Declarations))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return d
}

// TestTheRoundThreeGroupsRoundTrip: a group this build understands must survive
// being parsed and merged, and a group it does not must survive untouched. That
// second half is the reason `declarations` is a document of groups at all.
func TestTheRoundThreeGroupsRoundTrip(t *testing.T) {
	d := round3(t)
	if d.Warehouse.ConnectionRef != "ANALYTICS_DSN" {
		t.Errorf("warehouse.connectionRef did not parse: %+v", d.Warehouse)
	}
	if d.Schedule.TriggeredBy == "" || d.Lineage.RunCommand == "" {
		t.Errorf("schedule/lineage did not parse: %+v %+v", d.Schedule, d.Lineage)
	}
	if d.Datasets[0].LoadSemantics == nil || d.Datasets[0].LoadSemantics.Mode != LoadModeIncremental {
		t.Fatalf("loadSemantics did not parse: %+v", d.Datasets[0])
	}

	merged, err := MergeDeclarations(JSON(round3Declarations), JSON(`{"purpose":{"owner":"someone else"}}`))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	after, err := ParseDeclarations(merged)
	if err != nil {
		t.Fatalf("parse merged: %v", err)
	}
	if after.Warehouse.ConnectionRef != "ANALYTICS_DSN" || after.Schedule.PageRef != "analytics-oncall" {
		t.Errorf("editing one group dropped another: %+v", after)
	}
}

// TestAConnectionStringIsRefusedWhereAVariableNameBelongs is the one field in
// this whole document where a wrong answer is a leak rather than a mistake.
func TestAConnectionStringIsRefusedWhereAVariableNameBelongs(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		refused bool
	}{
		{"a variable name", "ANALYTICS_DSN", false},
		{"a variable name someone typed the dollar onto", "$ANALYTICS_DSN", false},
		{"a whole connection string", "snowflake://marcus:hunter2@acme-eu.snowflakecomputing.com/ANALYTICS", true},
		{"a host and a database", "warehouse.internal/analytics", true},
		{"a sentence", "the analytics connection", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := ValidateDeclarations(ProjectDeclarations{
				Warehouse: WarehouseDecl{ConnectionRef: c.ref},
			})
			refused := false
			for _, p := range problems {
				if strings.HasPrefix(p, "Connection:") {
					refused = true
					if !strings.Contains(p, "declare the NAME of the environment variable") {
						t.Errorf("the refusal does not say what to write instead: %s", p)
					}
					if strings.Contains(p, "hunter2") || strings.Contains(p, c.ref) {
						t.Errorf("the refusal echoed the rejected credential: %s", p)
					}
				}
			}
			if refused != c.refused {
				t.Errorf("refused=%v, want %v (%v)", refused, c.refused, problems)
			}
		})
	}
}

func TestWarehouseRoleCredentialIsRefusedWithoutEchoingIt(t *testing.T) {
	secret := "snowflake://marcus:hunter2@warehouse/role"
	problems := ValidateDeclarations(ProjectDeclarations{Warehouse: WarehouseDecl{
		Access: WarehouseAccessRead, RoleRefs: WarehouseRoleRefs{Read: secret, Write: secret},
	}})
	if !containsSubstring(problems, "Read role:") {
		t.Fatalf("role credential was accepted: %v", problems)
	}
	if strings.Contains(strings.Join(problems, "\n"), "hunter2") || strings.Contains(strings.Join(problems, "\n"), secret) {
		t.Fatalf("role refusal echoed the credential: %v", problems)
	}
}

// TestAccessAndRoleMustAgree: a warehouse declared read-only that also names a
// write role is two answers to one question, and a run inheriting both cannot
// tell which one it is allowed to act on.
func TestAccessAndRoleMustAgree(t *testing.T) {
	problems := ValidateDeclarations(ProjectDeclarations{Warehouse: WarehouseDecl{
		Access: WarehouseAccessRead, RoleRefs: WarehouseRoleRefs{Write: "ANALYTICS_WRITER_ROLE"},
	}})
	if !containsSubstring(problems, "one of the two is wrong") {
		t.Errorf("read access + a write role was accepted: %v", problems)
	}
	if p := ValidateDeclarations(ProjectDeclarations{Warehouse: WarehouseDecl{Access: "readwrite"}}); !containsSubstring(p, "is not an access level") {
		t.Errorf("an access level that is not one was accepted: %v", p)
	}
}

// TestAPagingDestinationIsAReference: the same rule `contactRef` and
// `recipientRef` follow. This platform holds references to people and never
// ways of reaching them.
func TestAPagingDestinationIsAReference(t *testing.T) {
	if p := ValidateDeclarations(ProjectDeclarations{Schedule: ScheduleDecl{PageRef: "analytics-oncall"}}); len(p) != 0 {
		t.Errorf("a rota name was refused: %v", p)
	}
	p := ValidateDeclarations(ProjectDeclarations{Schedule: ScheduleDecl{PageRef: "oncall@example.com"}})
	if !containsSubstring(p, "looks like an address") {
		t.Errorf("an address was accepted as a paging reference: %v", p)
	}
	if strings.Contains(strings.Join(p, "\n"), "oncall@example.com") {
		t.Fatalf("paging refusal echoed the address: %v", p)
	}
}

// TestALoadModeThatIsNotOne is the closed-set check; everything else about a
// load is a caveat, because half a load declaration is still a real one.
func TestALoadModeThatIsNotOne(t *testing.T) {
	d := ProjectDeclarations{Datasets: []DatasetDecl{{
		Name: "trips", LoadSemantics: &LoadSemanticsDecl{Mode: "streaming"},
	}}}
	if !containsSubstring(ValidateDeclarations(d), "is not a load mode") {
		t.Errorf("a load mode that is not one was accepted: %v", ValidateDeclarations(d))
	}

	incomplete := ProjectDeclarations{Datasets: []DatasetDecl{{
		Name: "trips", LoadSemantics: &LoadSemanticsDecl{Mode: LoadModeIncremental, LateArrivalWindow: "45 days"},
	}}}
	if problems := ValidateDeclarations(incomplete); len(problems) != 0 {
		t.Errorf("an incomplete load was refused rather than caveated: %v", problems)
	}
	caveats := DeclarationCaveats(incomplete)
	if !containsSubstring(caveats, "nothing says which rows are new") {
		t.Errorf("an incremental load with no watermark carries no caveat: %v", caveats)
	}
	if !containsSubstring(caveats, "give different numbers for the same month") {
		t.Errorf("a late-arrival window with no restatement rule carries no caveat: %v", caveats)
	}
}

// TestADeadlineWithNoTimezoneSaysSo: "06:00" is a different moment in every
// office, and a deadline nobody can place on a clock cannot be missed or met.
func TestADeadlineWithNoTimezoneSaysSo(t *testing.T) {
	c := DeclarationCaveats(ProjectDeclarations{Schedule: ScheduleDecl{Deadline: "06:00"}})
	if !containsSubstring(c, "different moment in every office") {
		t.Errorf("a deadline with no timezone carries no caveat: %v", c)
	}
	if c := DeclarationCaveats(ProjectDeclarations{Schedule: ScheduleDecl{Deadline: "06:00", Timezone: "America/New_York"}}); len(c) != 0 {
		t.Errorf("a deadline WITH a timezone was caveated: %v", c)
	}
}

// TestTheWarehouseFillsTheTargetStore — M-2. The slot was prose ("postgres,
// schema analytics"), so no run could say which credential reached the store or
// with what rights. It arrives as three references now, and none of them is a
// secret.
func TestTheWarehouseFillsTheTargetStore(t *testing.T) {
	got := PrefillSlots(round3(t))["targetStore"]
	const want = "postgres analytics (connection from $ANALYTICS_DSN, write role $ANALYTICS_WRITER_ROLE)"
	if got.Value != want {
		t.Errorf("targetStore inherited\n  %q\nwant\n  %q", got.Value, want)
	}
	if got.Source != "warehouse" {
		t.Errorf("targetStore does not say where it came from: %q", got.Source)
	}
}

// TestTheScheduleSaysThisPlatformDoesNotSchedule — M-1. A cadence that arrives
// on a form without this sentence reads as a promise the platform made.
func TestTheScheduleSaysThisPlatformDoesNotSchedule(t *testing.T) {
	got := PrefillSlots(round3(t))["refreshDeadline"].Value
	for _, want := range []string{
		"must have landed by 09:00 on the fifth working day America/New_York",
		"started by the analytics lead, by hand",
		"page analytics-oncall when it does not land",
		NoSchedulerHere,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the inherited schedule never says %q:\n%s", want, got)
		}
	}
	if v := PrefillSlots(ProjectDeclarations{})["refreshDeadline"].Value; v != "" {
		t.Errorf("a project that declares no schedule inherited one: %q", v)
	}
}

// TestLoadSemanticsReachTwoSlots — M-13. Full-versus-incremental rides on the
// reference to the dataset; how late a row may arrive and what it does to its
// period is its own slot. Neither is derived by a planning step any more.
func TestLoadSemanticsReachTwoSlots(t *testing.T) {
	prefill := PrefillSlots(round3(t))
	if v := prefill["sources"].Value; !strings.Contains(v, "incremental load on tpep_pickup_datetime") {
		t.Errorf("the dataset reference does not carry how it is loaded:\n%s", v)
	}
	late := prefill["lateArrival"].Value
	if !strings.Contains(late, "rows may arrive up to 45 days late") ||
		!strings.Contains(late, "restates the month it was taken in") {
		t.Errorf("the late-arrival slot did not inherit the load's own words:\n%s", late)
	}
}

func TestScheduleMaxLagAndLateArrivalWording(t *testing.T) {
	d := round3(t)
	d.Schedule.MaxLag = "6 hours"
	d.Datasets[0].LoadSemantics.LateArrivalWindow = "up to 45 days"
	prefill := PrefillSlotsFor(d, "data-pipeline")
	if got := prefill["maxLag"].Value; got != "6 hours" {
		t.Errorf("maxLag = %q", got)
	}
	if got := prefill["lateArrival"].Value; strings.Contains(got, "up to up to") {
		t.Errorf("late-arrival wording doubled up-to: %q", got)
	}
}

func TestMLDeclarationsReachPredictionAndConstraints(t *testing.T) {
	d := ProjectDeclarations{
		LabelAvailability:  LabelAvailabilityDecl{Event: "a chargeback is filed", Lag: "90 days"},
		ServingConstraints: ServingConstraintsDecl{Latency: "under 50 ms", ArtifactSize: "under 50 MB", Residency: "EU only", Cost: "under $10/day"},
	}
	p := PrefillSlotsFor(d, "ml-engineering")
	if got := p["predictionTarget"].Value; got != "Label becomes knowable: a chargeback is filed, 90 days after the prediction" {
		t.Errorf("predictionTarget = %q", got)
	}
	for _, want := range []string{"latency: under 50 ms", "artifact size: under 50 MB", "residency: EU only", "cost: under $10/day"} {
		if !strings.Contains(p["constraints"].Value, want) {
			t.Errorf("constraints missing %q: %q", want, p["constraints"].Value)
		}
	}
}

// TestLineageFillsThePipelineLocation — M-3. The slot was prose, so a change
// recipe could not cite the file it edits or the convention its change lands
// through.
func TestLineageFillsThePipelineLocation(t *testing.T) {
	got := PrefillSlots(round3(t))["pipelineLocation"].Value
	for _, want := range []string{
		"analytics/models/*.sql", "run end to end with analytics/run.sh",
		"manifest analytics/manifest.json", "changes land as one pull request per model",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the inherited pipeline location never says %q:\n%s", want, got)
		}
	}
}

func TestTheDeclaredHandoverPrefillsWorkflowInstructions(t *testing.T) {
	if got := PrefillSlots(round3(t))["handover"].Value; got != "a one-page markdown summary plus the CSV, to data-eng" {
		t.Errorf("the handover slot inherited %q", got)
	}
}

// TestSignOffPrefersTheRoleThatSettlesIt: "Sofia" alone does not say what Sofia
// does, and the reader of a REQUEST panel is asking who accepts this — not who
// is on the list.
func TestSignOffPrefersTheRoleThatSettlesIt(t *testing.T) {
	d := ProjectDeclarations{
		Purpose: PurposeDecl{Owner: "the analytics lead"},
		Stakeholders: []StakeholderDecl{
			{Role: "reads the scorecard", Name: "data engineering"},
			{Role: "signs off the monthly numbers", Name: "Marcus"},
		},
	}
	if got := SignOffRef(d); got != "Marcus (signs off the monthly numbers)" {
		t.Errorf("sign-off is %q — the approver was not preferred over the reader", got)
	}
	// No approving role: the project's own declared owner is the honest answer.
	onlyReaders := ProjectDeclarations{
		Purpose:      PurposeDecl{Owner: "the analytics lead"},
		Stakeholders: []StakeholderDecl{{Role: "reads the scorecard", Name: "data engineering"}},
	}
	if got := SignOffRef(onlyReaders); got != "the analytics lead (the project's owner — no approver is declared)" {
		t.Errorf("sign-off is %q — the declared owner is who settles it when nobody else is named", got)
	}
	firstStakeholder := ProjectDeclarations{
		Stakeholders: []StakeholderDecl{{Role: "reads the scorecard", Name: "data engineering"}},
	}
	if got := SignOffRef(firstStakeholder); got != "data engineering (no approver is declared; first stakeholder)" {
		t.Errorf("sign-off is %q — the first-stakeholder fallback is not labelled", got)
	}
	if got := SignOffRef(ProjectDeclarations{}); got != "" {
		t.Errorf("a project that names nobody produced %q — an unnamed approver is not a blank field, it is a project that has not named one", got)
	}
}

// TestTheRegressionPolicyBecomesAReferenceADraftCanCarry — D-2, half of it.
//
// The policy was declarable for a whole round and read by no surface, so the
// Skill Lab reported "no evalset yet" about a project that had named one. The
// reference travels now; the scoring still does not, and the card says so.
func TestTheRegressionPolicyBecomesAReferenceADraftCanCarry(t *testing.T) {
	d := ProjectDeclarations{RegressionPolicy: RegressionPolicyDecl{
		EvalsetID: "churn-holdout", MinMargin: "0.02 PR-AUC",
	}}
	got := RegressionPolicyStamp(d, "taxi-analytics")
	want := map[string]string{"project": "taxi-analytics", "evalsetId": "churn-holdout", "minMargin": "0.02 PR-AUC"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("stamp[%q] = %q, want %q", k, got[k], v)
		}
	}
	// Half a policy is still a policy, and the half that is missing has to be
	// visible as missing rather than as absent.
	half := RegressionPolicyStamp(ProjectDeclarations{
		RegressionPolicy: RegressionPolicyDecl{EvalsetID: "churn-holdout"}}, "taxi-analytics")
	if half["evalsetId"] != "churn-holdout" {
		t.Errorf("an evalset with no margin stamped nothing: %v", half)
	}
	if _, ok := half["minMargin"]; ok {
		t.Errorf("an undeclared margin was stamped: %v", half)
	}
	if RegressionPolicyStamp(ProjectDeclarations{}, "taxi-analytics") != nil {
		t.Error("a project that declares no policy stamped one — no policy and an unrendered policy are different facts")
	}
}

// TestTheSeededRoundThreeGroupsWouldSaveCleanly guards the document a fresh
// install ships: a seeded declaration the form would refuse teaches the rule by
// failure on the first project anybody opens.
func TestTheSeededRoundThreeGroupsWouldSaveCleanly(t *testing.T) {
	d := round3(t)
	if problems := ValidateDeclarations(d); len(problems) != 0 {
		t.Errorf("the round-3 document would be refused on save: %v", problems)
	}
}

func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
