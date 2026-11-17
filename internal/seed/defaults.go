package seed

import (
	"context"
	"fmt"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// DefaultRadar seeds the tech radar with known-good technology choices, each
// linked to the skill that covers it (skill_ref). Entries are upserted by tech
// name so re-running seed is idempotent.
//
// Quadrant taxonomy:
//
//	languages  — programming languages and type systems
//	tools      — dev tools, runtimes, databases, infra primitives
//	techniques — patterns, practices, methodologies
//	platforms  — deployment targets, app platforms, ecosystem layers
//
// Rings: adopt | trial | assess | hold
var DefaultRadar = []store.TechRadar{
	// ── languages ──────────────────────────────────────────────────────────────
	{
		Tech: "Go", Quadrant: "languages", Ring: "adopt",
		SkillRef:    "golang-backend",
		Description: "Primary backend language — stdlib-first, zero-dep. Used for the Hyperplane engine, API, and CLI.",
		Note:        "Pairs with pgx/v5 for Postgres. No ORM. Prefer stdlib net/http (Go 1.22+ method+path routing).",
	},
	{
		Tech: "TypeScript", Quadrant: "languages", Ring: "adopt",
		SkillRef:    "",
		Description: "Default for all frontend work. Strict mode on.",
		Note:        "Used in the web SPA. Evaluate Svelte/Solid at next review.",
	},
	{
		Tech: "Swift", Quadrant: "languages", Ring: "adopt",
		SkillRef:    "swift-app-architect",
		Description: "iOS/macOS native development. SwiftUI + async/await. Used in app-factory spinouts.",
		Note:        "Minimum deployment iOS 17+. Liquid Glass requires iOS 26.",
	},
	{
		Tech: "React", Quadrant: "languages", Ring: "trial",
		SkillRef:    "",
		Description: "UI layer for the Hyperplane web SPA.",
		Note:        "Evaluate Solid or Svelte for future projects — React bundle size is a concern.",
	},

	// ── tools ──────────────────────────────────────────────────────────────────
	{
		Tech: "PostgreSQL", Quadrant: "tools", Ring: "adopt",
		SkillRef:    "postgres-data-modeling",
		Description: "Durable state store with JSONB, pg_trgm, and row-level locking.",
		Note:        "WAL mode, SKIP LOCKED for ticket queues, GIN indexes for fuzzy search.",
	},
	{
		Tech: "Docker", Quadrant: "tools", Ring: "adopt",
		SkillRef:    "",
		Description: "Containerised dev and prod environments. docker compose up for local stack.",
		Note:        "Dockerfile per service. No K8s yet — evaluate when multi-node required.",
	},
	{
		Tech: "node:sqlite", Quadrant: "tools", Ring: "adopt",
		SkillRef:    "",
		Description: "Built-in SQLite for Orchestra (Node >=22.5). Zero npm deps.",
		Note:        "Do NOT replace with better-sqlite3. This is the Orchestra-specific store.",
	},
	{
		Tech: "Supabase", Quadrant: "tools", Ring: "trial",
		SkillRef:    "supabase-backend",
		Description: "Postgres-as-a-service with auth, storage, and edge functions. Used in mobile app backends.",
		Note:        "Evaluate against raw pgx for apps where Supabase Auth adds value.",
	},
	{
		Tech: "pgx/v5", Quadrant: "tools", Ring: "adopt",
		SkillRef:    "postgres-data-modeling",
		Description: "Go Postgres driver used throughout the Hyperplane store layer.",
		Note:        "No ORM. Direct SQL with pgx.Row scanning and pgxpool.Pool.",
	},
	{
		Tech: "Expo", Quadrant: "tools", Ring: "trial",
		SkillRef:    "expo-router-architecture",
		Description: "React Native managed workflow for cross-platform mobile apps.",
		Note:        "Use Expo Router for file-based navigation. Evaluate bare workflow for native modules.",
	},

	// ── techniques ─────────────────────────────────────────────────────────────
	{
		Tech: "On-device RAG", Quadrant: "techniques", Ring: "trial",
		SkillRef:    "on-device-rag",
		Description: "Retrieval-augmented generation running entirely on-device using Foundation Models.",
		Note:        "32k context limit on local models. Use MLX or CoreML for embeddings.",
	},
	{
		Tech: "Foundation Models (on-device)", Quadrant: "techniques", Ring: "trial",
		SkillRef:    "foundation-models-agent",
		Description: "Apple Foundation Models API for on-device inference. Requires iOS 26 / macOS 26.",
		Note:        "Gate behind #available check. Fallback to cloud model for unsupported hardware.",
	},
	{
		Tech: "Foundation Models patterns", Quadrant: "techniques", Ring: "assess",
		SkillRef:    "foundation-models-patterns",
		Description: "Design patterns for agentic on-device workflows: tool use, structured output, streaming.",
		Note:        "Early — API surface is still stabilising. Track WWDC sessions.",
	},
	{
		Tech: "DSPy / RLHF optimize loop", Quadrant: "techniques", Ring: "trial",
		SkillRef:    "",
		Description: "Automated prompt optimization via preference signals (human picks, judge scores).",
		Note:        "Hyperplane's optimize + improve verbs implement this. skill_refinements table tracks lineage.",
	},
	{
		Tech: "SDK workflows", Quadrant: "techniques", Ring: "adopt",
		SkillRef:    "",
		Description: "Ordered SDK steps with editable instructions, optional checks and human approvals.",
		Note:        "SDK sessions own model work. Each task declares its permitted hosts, read paths and environment references.",
	},
	{
		Tech: "npm-deps minimalism", Quadrant: "techniques", Ring: "hold",
		SkillRef:    "",
		Description: "Avoid adding new npm deps. Prefer stdlib or vendored code.",
		Note:        "Breaks the clone-and-run promise. node:sqlite is the canonical zero-dep example.",
	},
	{
		Tech: "Data visualisation (web)", Quadrant: "techniques", Ring: "trial",
		SkillRef:    "data-visualization",
		Description: "Interactive data visualisation patterns for web dashboards.",
		Note:        "Evaluate D3 vs. Observable Plot vs. Recharts for Hyperplane analytics views.",
	},
	{
		Tech: "AI Engineering patterns", Quadrant: "techniques", Ring: "adopt",
		SkillRef:    "ai-engineering",
		Description: "Patterns for building reliable AI-powered systems: evals, guardrails, human-in-the-loop.",
		Note:        "Core to every Hyperplane milestone. Codex adapter flags, gate nodes, judge mode.",
	},
	{
		Tech: "React Native Reanimated", Quadrant: "techniques", Ring: "trial",
		SkillRef:    "react-native-reanimated",
		Description: "Performant animations in React Native via worklet-based gesture+animation library.",
		Note:        "Required for any gesture-driven mobile UI. Pairs with Gesture Handler.",
	},
	{
		Tech: "Mobile haptics + sound design", Quadrant: "techniques", Ring: "trial",
		SkillRef:    "mobile-haptics-sound",
		Description: "iOS/Android haptic feedback and audio feedback design for native feel.",
		Note:        "Use Core Haptics on iOS for custom waveforms. Android: VibrationEffect.",
	},

	// ── platforms ───────────────────────────────────────────────────────────────
	{
		Tech: "SwiftUI + Liquid Glass (iOS 26)", Quadrant: "platforms", Ring: "assess",
		SkillRef:    "liquid-glass-art-director",
		Description: "iOS 26 Liquid Glass design system: glassEffect modifier, GlassEffectContainer, morphing transitions.",
		Note:        "Gate behind #available(iOS 26, *). GlassEffectContainer required for morphing. See WWDC25.",
	},
	{
		Tech: "SwiftUI performance", Quadrant: "platforms", Ring: "adopt",
		SkillRef:    "swiftui-performance",
		Description: "SwiftUI rendering optimisations: equatable, lazy stacks, instruments-driven profiling.",
		Note:        "Always Instruments-first. Avoid @ObservableObject where @Observable (Observation) suffices.",
	},
	{
		Tech: "Swift Charts + DataViz", Quadrant: "platforms", Ring: "trial",
		SkillRef:    "swift-charts-dataviz",
		Description: "Native Swift Charts framework for data visualisation in iOS/macOS apps.",
		Note:        "Composable chart marks. Annotate, scroll, and drill. Check availability: iOS 16+.",
	},
	{
		Tech: "React Native (cross-platform mobile)", Quadrant: "platforms", Ring: "adopt",
		SkillRef:    "react-native-performance",
		Description: "Cross-platform mobile development with near-native performance.",
		Note:        "Use Hermes engine. New Architecture (Fabric + TurboModules) for perf-critical paths.",
	},
	{
		Tech: "Web accessibility (WCAG)", Quadrant: "platforms", Ring: "adopt",
		SkillRef:    "web-accessibility",
		Description: "WCAG 2.2 AA compliance for all web surfaces.",
		Note:        "Semantic HTML first. axe-core in CI. Focus management for SPAs.",
	},
	{
		Tech: "Web art direction", Quadrant: "platforms", Ring: "trial",
		SkillRef:    "web-art-direction",
		Description: "Distinctive visual design for web: typography hierarchy, motion, brand voice.",
		Note:        "Reject generic clean redesigns. Require distinctive concept + art direction per project.",
	},
	{
		Tech: "WebGL + 3D web", Quadrant: "platforms", Ring: "assess",
		SkillRef:    "webgl-3d-web",
		Description: "3D rendering in the browser via WebGL/WebGPU. Three.js or R3F.",
		Note:        "Evaluate for Hyperplane data viz when 2D is insufficient. High implementation cost.",
	},
	{
		Tech: "CSS modern layout", Quadrant: "platforms", Ring: "adopt",
		SkillRef:    "css-modern-layout",
		Description: "CSS Grid, Container Queries, subgrid, and logical properties for responsive layout.",
		Note:        "Container queries over media queries for component-scoped breakpoints.",
	},
}

// DefaultStacks seeds baseline technology stacks.
var DefaultStacks = []store.Stack{
	{
		Name:        "Go + Postgres + Docker",
		Description: "Standard Hyperplane backend stack. Go service, Postgres for state, Dockerised for local dev.",
		Runtime:     "go1.22+",
		Dockerized:  true,
		Notes:       "Use pgx/v5 for Postgres; stdlib http for API. No ORM.",
	},
}

// DefaultCategories seeds standard workflow/skill categories.
var DefaultCategories = []store.Category{
	{Name: "automation", Kind: "all"},
	{Name: "docs", Kind: "all"},
	{Name: "app-factory", Kind: "all"},
	{Name: "planning", Kind: "all"},
	{Name: "research", Kind: "all"},
	{Name: "decision", Kind: "all"},
}

// DefaultTemplates seeds starter workflow templates including domain workflows
// that prove the platform for research, planning, and decision use-cases.
var DefaultTemplates = []store.WorkflowTemplate{
	codeReviewTemplate(),
	tripPlanningTemplate(),
	deepResearchTemplate(),
	interviewPrepTemplate(),
	financeReviewTemplate(),

	marketResearchTemplate(),

	dataPipelineTemplate(),

	mlEngineeringTemplate(),

	pipelineChangeTemplate(),

	dataQualityTemplate(),

	datasetShiftTemplate(),

	reconciliationTemplate(),

	// Recurring work: the same workflow rerun each week or month, with the
	// facts that change between periods declared as inputs.
	competitorBriefTemplate(),
	kpiCommentaryTemplate(),
	plainLanguagePassTemplate(),
	customerFeedbackDigestTemplate(),
	fileLandingAuditTemplate(),
}

// ExampleProject is a project the platform seeds so a fresh install opens with
// real domains to run recipes against, rather than an empty Projects surface.
//
// Only the tag, the description and the declaration document are seeded. The
// display name is left to store.EnsureProject, which INITCAPs the tag exactly
// as the migration backfill does ("taxi-analytics" → "Taxi Analytics"), so a
// seeded row and a row created by tagging a run are indistinguishable.
//
// Description becomes `projects.summary` — "what this project is for", the
// human-owned line. Nothing here ever seeds `projects.narrative`: a fresh
// install has accepted no deliveries, so "what this project has built so far"
// is genuinely empty and says so.
type ExampleProject struct {
	ID          string
	Description string
	// Declarations is the starter declaration document (see
	// store/declarations.go): the datasets the recipes under this project all
	// point at, declared once so their slots inherit rather than being re-typed
	// from memory every run. Seeded only into a project that has none — like
	// the description, it is a starting point, not a claim on the row.
	Declarations string
}

// DefaultProjects are the seeded example projects. They pair with the
// data-engineering recipes: each one is a real dataset with enough substance to
// build a pipeline over, audit, and then change.
var DefaultProjects = []ExampleProject{
	{
		ID:          "taxi-analytics",
		Description: "NYC yellow-cab trip analytics — 24M-row monthly TLC drops; pipelines, audits and changes over data/yellow_tripdata_*.csv",
		// The declaration this project's runs kept re-typing: one dataset, its
		// grain, its (absent) natural key and the closest candidate, the
		// arrival timestamp and its timezone, and the safe-use boundary that
		// was previously copied into all five recipes by hand.
		Declarations: `{
  "purpose": {
    "owner": "the analytics lead who signs off the monthly trip numbers",
    "doneMeans": "The monthly trip mart and its quality scorecard are accepted, and the numbers in them match a total computed without the pipeline.",
    "constraints": "Trip records are not personal data, but pick-up and drop-off zones plus timestamps can identify a rider in a thin zone: report zones as counts and never as an example row. Aggregate reporting only, never per-driver or per-rider decisions."
  },
  "datasets": [
    {
      "id": "yellow-trips",
      "name": "NYC TLC yellow-cab monthly trip drops",
      "location": "data/yellow_tripdata_2025-*.csv",
      "format": "csv",
      "kind": "fact",
      "grain": "one row per completed trip, as landed in the monthly drop",
      "naturalKey": "none",
      "keyCandidate": ["VendorID", "tpep_pickup_datetime", "PULocationID", "DOLocationID", "total_amount"],
      "arrivalTimestampColumn": "tpep_pickup_datetime",
      "timezone": "America/New_York",
      "expectedRowCount": 24083384,
      "piiColumns": [],
      "safeUse": "Aggregate reporting and pipeline triage only; not for enforcement and not for judging individual drivers or riders. Zone examples appear as counts, never as example rows.",
      "loadSemantics": {
        "mode": "incremental",
        "watermarkColumn": "tpep_pickup_datetime",
        "lateArrivalWindow": "45 days",
        "restatementRule": "a late trip restates the month it was taken in, never the month it landed in"
      }
    },
    {
      "id": "taxi-zones",
      "name": "TLC taxi zone lookup",
      "location": "data/taxi_zone_lookup.csv",
      "format": "csv",
      "kind": "lookup",
      "grain": "one row per zone id",
      "keyColumns": ["LocationID"]
    }
  ],
  "environment": {
    "where": "a local workspace with duckdb provisioned by uv; outputs land in the run's artifacts and nothing is written back to the source drops",
    "runtimeBudgetDefault": "the whole run inside 20 minutes; overrun should fail loudly rather than quietly switch to a sample"
  },
  "schedule": {
    "expectedCadence": "once a month, when the TLC drop lands",
    "deadline": "09:00 on the fifth working day",
    "timezone": "America/New_York",
    "triggeredBy": "the analytics lead, by hand — nothing schedules this, here or anywhere else yet",
    "pageRef": "analytics-oncall"
  },
  "warehouse": {
    "engine": "duckdb",
    "databaseSchema": "trips",
    "connectionRef": "TAXI_ANALYTICS_DSN",
    "roleRefs": {
      "read": "TAXI_ANALYTICS_READER_ROLE",
      "write": "TAXI_ANALYTICS_WRITER_ROLE"
    },
    "access": "write"
  },
  "lineage": {
    "repoPath": "analytics/models/*.sql",
    "runCommand": "analytics/run.sh",
    "manifestPath": "analytics/manifest.json",
    "prConvention": "one pull request per model, reviewed by the analytics lead before it lands"
  },
  "stakeholders": [
    {
      "role": "approves the monthly numbers",
      "name": "the analytics lead",
      "contactRef": "analytics-lead"
    },
    {
      "role": "reads the scorecard",
      "name": "the data engineering team",
      "contactRef": "data-eng"
    }
  ],
  "delivery": {
    "format": "a one-page markdown summary plus the per-rule CSV",
    "recipientRef": "data-eng"
  }
}`,
	},
	{
		ID:          "service-reliability",
		Description: "Q1 2026 service telemetry — reliability marts and quality audits over service_reliability_events.csv",
		Declarations: `{
  "purpose": {
    "owner": "the SRE on-call lead",
    "doneMeans": "The Q1 2026 reliability scorecard is accepted by the SRE team."
  },
  "datasets": [
    {
      "id": "service-events",
      "name": "Service reliability events",
      "location": "data/service_reliability_events.csv",
      "format": "csv",
      "kind": "fact",
      "grain": "one row per service event",
      "keyColumns": ["event_id"],
      "arrivalTimestampColumn": "ingested_at",
      "timezone": "UTC",
      "safeUse": "Service-level triage only; not for evaluating individual engineers or teams."
    }
  ]
}`,
	},
}

// declarationsAreEmpty reports whether a project's declaration document holds
// no groups at all — the only state the seeded starter document is written
// into. A person who has declared even one group owns the document from then
// on, and a reseed leaves it exactly as they left it.
func declarationsAreEmpty(raw store.JSON) bool {
	decl, err := store.ParseDeclarations(raw)
	if err != nil {
		// Unparseable is not empty: refuse to overwrite something we cannot read.
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "{}" {
		return true
	}
	return decl.Purpose == store.PurposeDecl{} && len(decl.Datasets) == 0 &&
		decl.Environment == store.EnvironmentDecl{} &&
		// The operational groups count too: a project whose owner has declared
		// only where its warehouse is has still declared something, and a
		// reseed that treated that as empty would overwrite it.
		decl.Schedule == store.ScheduleDecl{} && decl.Warehouse == store.WarehouseDecl{} &&
		decl.Lineage == store.LineageDecl{}
}

// Defaults seeds tech radar, stacks, categories, and templates if they are
// absent (idempotent: existing rows with matching names/tech are skipped or updated).
func Defaults(ctx context.Context, st *store.Store) error {
	// Tech radar — upsert by tech name (idempotent via ON CONFLICT).
	for _, r := range DefaultRadar {
		if _, err := st.UpsertTechRadarEntry(ctx, r); err != nil {
			return fmt.Errorf("seed tech radar %q: %w", r.Tech, err)
		}
	}

	// Stacks.
	existingStacks, err := st.ListStacks(ctx)
	if err != nil {
		return fmt.Errorf("seed defaults: list stacks: %w", err)
	}
	stackSet := map[string]bool{}
	for _, s := range existingStacks {
		stackSet[s.Name] = true
	}
	for _, s := range DefaultStacks {
		if stackSet[s.Name] {
			continue
		}
		if _, err := st.CreateStack(ctx, s); err != nil {
			return fmt.Errorf("seed stack %q: %w", s.Name, err)
		}
	}

	// Categories.
	existingCats, err := st.ListCategories(ctx)
	if err != nil {
		return fmt.Errorf("seed defaults: list categories: %w", err)
	}
	catSet := map[string]bool{}
	for _, c := range existingCats {
		catSet[c.Name] = true
	}
	for _, c := range DefaultCategories {
		if catSet[c.Name] {
			continue
		}
		if _, err := st.CreateCategory(ctx, c); err != nil {
			return fmt.Errorf("seed category %q: %w", c.Name, err)
		}
	}

	// Workflow templates.
	existingTmps, err := st.ListTemplates(ctx)
	if err != nil {
		return fmt.Errorf("seed defaults: list templates: %w", err)
	}
	tmpSet := map[string]bool{}
	for _, t := range existingTmps {
		tmpSet[t.Name] = true
	}
	for _, t := range DefaultTemplates {
		if tmpSet[t.Name] {
			// Shipped recipes stay current on reseed: refresh content in
			// place so slot/criteria fixes reach existing installs. The id —
			// and every run doc stamped with it — is preserved.
			if err := st.RefreshTemplateByName(ctx, t); err != nil {
				return fmt.Errorf("refresh template %q: %w", t.Name, err)
			}
			continue
		}
		if _, err := st.CreateTemplate(ctx, t); err != nil {
			return fmt.Errorf("seed template %q: %w", t.Name, err)
		}
	}

	// Example projects. EnsureProject is insert-only and deliberately does not
	// bump updated_at, so registering an existing project is a no-op — that is
	// what makes reseeding idempotent. The description is a separate, guarded
	// step: it is written ONLY into a row that carries none. A project the user
	// has since described (or cleared on purpose) keeps their words, because a
	// reseed is not an edit and must never overwrite one.
	for _, p := range DefaultProjects {
		if err := st.EnsureProject(ctx, p.ID); err != nil {
			return fmt.Errorf("seed project %q: %w", p.ID, err)
		}
		existing, err := st.GetProject(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("seed project %q: read back: %w", p.ID, err)
		}
		// Each half is seeded only into an EMPTY half, and the row is not
		// touched at all when there is nothing to seed — a reseed is not an
		// edit, and the Projects surface is ordered by updated_at.
		var patch store.ProjectPatch
		if strings.TrimSpace(existing.Summary) == "" {
			desc := p.Description
			patch.Summary = &desc
		}
		if p.Declarations != "" && declarationsAreEmpty(existing.Declarations) {
			decl := store.JSON(p.Declarations)
			patch.Declarations = &decl
		}
		if patch.Summary == nil && patch.Declarations == nil {
			continue
		}
		if _, err := st.UpdateProject(ctx, p.ID, patch); err != nil {
			return fmt.Errorf("seed project %q: describe: %w", p.ID, err)
		}
	}

	return nil
}

// handoverSlot is the "how the finished thing is handed over" slot, with its
// wording in one place.
//
// S-3: a project could declare a hand-over — a format and a recipient reference
// — from round 2 onward, and no form, no plan ledger and no run page ever read
// it. An analyst who had written down that the reconciliation goes to
// #finance-analytics as a one-page summary watched every run under that project
// produce a document addressed to nobody. This slot is where that declaration
// lands, and each recipe's prose node is told to address what it writes to
// whatever fills it.
//
// `what` names the artifact in the recipe's own words ("summary", "scorecard",
// "model card"), so the label is about the thing the reader is about to be
// handed rather than about a category the platform invented.
func handoverSlot(what, example string) store.ContextSlot {
	return store.ContextSlot{
		ID:    "handover",
		Label: "How the finished " + what + " is handed over",
		Hint: "Who receives the finished " + what + " from you, and in what form — for example '" + example + "'. " +
			"Name the recipient by a handle or a list name, never an email address: the platform keeps references to people, not ways of reaching them. " +
			"If the project declares this, it arrives filled in; left blank, the " + what + " is addressed to nobody in particular.",
		Required: false,
		Example:  example,
	}
}
