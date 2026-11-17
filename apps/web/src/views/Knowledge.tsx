import { useState, useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, isDraftSkill, regressionPolicySentence, skillLineage, supportLabel, learnedFromLabel, refinementCounts } from '../api';
import type { Skill, TechRadar, Stack, Category, WorkflowTemplate, Decision, ImproveRun, PersonaView, PersonaReview } from '../api';
import { SkillLab } from '../components/SkillLab';
import { KnowledgeGraph } from '../components/KnowledgeGraph';
import { ProjectSelector } from '../components/ProjectSelector';
import { PersonaVerdict } from '../components/PersonaVerdict';
import { GeneratePracticeTest } from '../components/GeneratePracticeTest';
import { useProjectScope } from '../hooks/useProjectScope';
import { useSelectedRun } from '../hooks/useSelectedRun';
import { gateSentence, useApprovalGate } from '../hooks/useApprovalGate';
import type { ApprovalGate } from '../hooks/useApprovalGate';

type KnowledgeTab = 'registry' | 'skill-lab' | 'radar' | 'stacks' | 'categories' | 'templates' | 'decisions' | 'graph';

const DECISION_STATUS_ORDER = ['proposed', 'accepted', 'superseded', 'deprecated'] as const;

function decisionStatusClass(status: string) {
  switch (status) {
    case 'accepted':   return 's-pass';
    case 'proposed':   return 's-live';
    case 'superseded': return 's-idle';
    case 'deprecated': return 's-fail';
    default:           return 's-idle';
  }
}

const RING_ORDER = ['adopt', 'trial', 'assess', 'hold'] as const;

function ringClass(ring: string) {
  switch (ring) {
    case 'adopt':  return 'radar-adopt';
    case 'trial':  return 'radar-trial';
    case 'assess': return 'radar-assess';
    case 'hold':   return 'radar-hold';
    default:       return '';
  }
}

// ── Radar tab ─────────────────────────────────────────────────────────────

function RadarTab() {
  const { data, isLoading } = useQuery<TechRadar[]>({
    queryKey: ['tech-radar'],
    queryFn: () => api.techRadar(),
  });
  const entries = Array.isArray(data) ? data : [];

  if (isLoading) return <div className="empty"><span>loading radar…</span></div>;
  if (entries.length === 0) {
    return (
      <div className="empty">
        <span className="mark">◎</span>
        <span>No tech radar entries yet.</span>
      </div>
    );
  }

  const byRing = RING_ORDER.map((ring) => ({
    ring,
    items: entries.filter((e) => e.ring === ring),
  })).filter((g) => g.items.length > 0);

  return (
    <div>
      {byRing.map(({ ring, items }) => (
        <div key={ring}>
          <div className="radar-ring-head">
            <span className={`chip radar-ring-label ${ringClass(ring)}`}>{ring}</span>
          </div>
          <div className="panel" style={{ marginBottom: 'var(--s4)' }}>
            {items.map((entry) => (
              <div key={entry.id} className="kn-row">
                <div className="kn-top">
                  <span className="kn-id">{entry.tech}</span>
                  <span className="kn-cat">{entry.quadrant}</span>
                </div>
                {entry.note && <div className="kn-desc">{entry.note}</div>}
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// ── Stacks tab ─────────────────────────────────────────────────────────────

function StacksTab() {
  const { data, isLoading } = useQuery<Stack[]>({
    queryKey: ['stacks'],
    queryFn: () => api.stacks(),
  });
  const stacks = Array.isArray(data) ? data : [];

  if (isLoading) return <div className="empty"><span>loading stacks…</span></div>;
  if (stacks.length === 0) {
    return (
      <div className="empty">
        <span className="mark">◫</span>
        <span>No stacks defined yet.</span>
      </div>
    );
  }

  return (
    <div className="stack-grid">
      {stacks.map((stack) => (
        <div key={stack.id} className="panel stack-card">
          <div className="stack-card-head">
            <span className="stack-card-name">{stack.name}</span>
            {stack.dockerized && (
              <span className="chip s-pass" style={{ fontSize: 'var(--t-xs)' }}>docker</span>
            )}
          </div>
          {stack.runtime && (
            <span className="stack-card-runtime">{stack.runtime}</span>
          )}
          {stack.description && (
            <p className="stack-card-desc">{stack.description}</p>
          )}
          {stack.notes && (
            <p className="stack-card-desc" style={{ fontStyle: 'italic' }}>{stack.notes}</p>
          )}
        </div>
      ))}
    </div>
  );
}

// ── Categories tab ─────────────────────────────────────────────────────────

function CategoriesTab() {
  const { data, isLoading } = useQuery<Category[]>({
    queryKey: ['categories'],
    queryFn: () => api.categories(),
  });
  const cats = Array.isArray(data) ? data : [];

  if (isLoading) return <div className="empty"><span>loading categories…</span></div>;
  if (cats.length === 0) {
    return (
      <div className="empty">
        <span className="mark">⊞</span>
        <span>No categories defined yet.</span>
      </div>
    );
  }

  return (
    <div className="panel">
      {cats.map((cat) => (
        <div key={cat.id} className="cat-row">
          <div className="cat-row-left">
            <span className="cat-row-name">{cat.name}</span>
            <span className="cat-row-kind">{cat.kind}</span>
          </div>
          <span className="cat-row-count mono">{cat.count ?? 0}</span>
        </div>
      ))}
    </div>
  );
}

// ── Templates tab ──────────────────────────────────────────────────────────

function TemplatesTab() {
  const { data, isLoading } = useQuery<WorkflowTemplate[]>({
    queryKey: ['templates'],
    queryFn: () => api.templates(),
  });
  const tmpls = Array.isArray(data) ? data : [];

  if (isLoading) return <div className="empty"><span>loading templates…</span></div>;
  if (tmpls.length === 0) {
    return (
      <div className="empty">
        <span className="mark">⬡</span>
        <span>No workflow templates yet.</span>
      </div>
    );
  }

  return (
    <div className="tmpl-grid">
      {tmpls.map((tmpl) => (
        <div key={tmpl.id} className="panel tmpl-card">
          <div className="tmpl-card-head">
            {/* Same name as the registry and the recipe chip, slug on the hover
                — one workflow should not carry three names (Q-24). */}
            <span className="tmpl-card-name" title={tmpl.name}>
              {tmpl.displayName || tmpl.name}
            </span>
          </div>
          {tmpl.description && (
            <p className="tmpl-card-desc">{tmpl.description}</p>
          )}
          <div className="tmpl-card-foot">
            <Link
              className="btn btn--sm btn--primary"
              to="/workflows"
              state={{ brief: tmpl.description || tmpl.displayName || tmpl.name, templateId: (tmpl.spec as { kind?: string })?.kind === 'nodes' ? tmpl.id : undefined }}
            >
              {(tmpl.spec as { kind?: string })?.kind === 'nodes' ? 'Open workflow template' : 'Use as task brief'}
            </Link>
          </div>
        </div>
      ))}
    </div>
  );
}

// ── Decisions tab ────────────────────────────────────────────────────────────

function DecisionsTab() {
  const [q, setQ] = useState('');
  // Shared with Runs and the Board — one project scope for the whole app.
  const { project, setProject } = useProjectScope();
  const { data, isLoading } = useQuery<Decision[]>({
    queryKey: ['decisions', project],
    queryFn: () => api.decisions(project),
  });
  const all = Array.isArray(data) ? data : [];
  const needle = q.trim().toLowerCase();
  const decisions = needle
    ? all.filter((d) =>
        `${d.id} ${d.title} ${d.context ?? ''} ${d.decision ?? ''} ${d.why ?? ''} ${(d.tags ?? []).join(' ')}`
          .toLowerCase()
          .includes(needle),
      )
    : all;

  const byStatus = DECISION_STATUS_ORDER.map((status) => ({
    status,
    items: decisions.filter((d) => d.status === status),
  })).filter((g) => g.items.length > 0);
  const other = decisions.filter((d) => !DECISION_STATUS_ORDER.includes(d.status as never));
  if (other.length > 0) byStatus.push({ status: 'other' as never, items: other });

  return (
    <div>
      <ProjectSelector value={project} onChange={setProject} />
      <div className="dec-toolbar">
        <input
          type="search"
          className="input"
          placeholder="Search decisions…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label="Search decisions"
        />

      </div>

      {isLoading ? (
        <div className="empty"><span>loading decisions…</span></div>
      ) : decisions.length === 0 ? (
        <div className="empty">
          <span className="mark">⚖</span>
          <span>{q ? `No decisions matching "${q}"` : 'No decision records yet.'}</span>
        </div>
      ) : (
        byStatus.map(({ status, items }) => (
          <div key={status} style={{ marginBottom: 'var(--s5)' }}>
            <div className="section-head">
              <span className="eyebrow">{status} <span className="mono">{items.length}</span></span>
            </div>
            <div className="dec-grid">
              {items.map((d) => (
                <div key={d.id} className="panel dec-card">
                  <div className="dec-card-head">
                    <span className="dec-id mono">{d.id}</span>
                    <span className={`chip ${decisionStatusClass(d.status)}`} style={{ fontSize: 'var(--t-xs)' }}>
                      {d.status}
                    </span>
                  </div>
                  <div className="dec-title">{d.title}</div>
                  {d.decision && <p className="dec-body"><b>Decision:</b> {d.decision}</p>}
                  {d.why && <p className="dec-body dec-muted"><b>Why:</b> {d.why}</p>}
                  {d.consequences && <p className="dec-body dec-muted"><b>Consequences:</b> {d.consequences}</p>}
                  {(d.refs?.length || d.tags?.length) ? (
                    <div className="dec-chips">
                      {d.refs?.map((r) => <span key={r} className="dec-ref mono">↳ {r}</span>)}
                      {d.tags?.map((t) => <span key={t} className="dec-tag">{t}</span>)}
                    </div>
                  ) : null}
                </div>
              ))}
            </div>
          </div>
        ))
      )}
    </div>
  );
}

// ── Audit personas registry (AI-12) ─────────────────────────────────────────

/**
 * One registered audit charter. There is no write API for a persona — the
 * charter lives in `personas/*.md` in git, and `make seed` is what registers
 * it — so this row is read-only: name, bar, what it is competent to review,
 * and how many verdicts it has recorded so far.
 */
function PersonaRow({
  persona,
  displayNames,
}: {
  persona: PersonaView;
  /** id → the name that thing goes by elsewhere in the platform. A charter
   *  routes on ids; a reader should not have to. */
  displayNames?: Map<string, string>;
}) {
  const c = persona.competence;
  // One id can be named in two bullets — Sofia's charter names `market-research`
  // as a research skill and again as a recipe — and printing it twice reads as
  // two separate claims. The routing lists stay separate; the reader sees one.
  const named = [...new Set([...(c.skills ?? []), ...(c.processes ?? [])])];
  // The name where one is known, the id where it is not: "competent for:
  // market-research, pr/dataset-shift-audit" named two things the rest of the
  // platform calls something else.
  const namedForReading = named.map((id) => displayNames?.get(id) ?? id);
  // The subtitle was a 12-character content hash. It is a real fact about the
  // charter — and it is a checksum, not a name, so it belongs on the hover
  // beside the file it hashes.
  const subtitle = persona.source || (persona.version ? `charter ${persona.version}` : '');
  // The charter's own bullets, kept whole. Falls back to the derived free-text
  // labels for a charter registered before bullets were imported.
  const bullets = c.bullets ?? [];
  return (
    <div className="kn-row">
      <div className="kn-top">
        <span className="kn-id">{persona.name}</span>
        {subtitle && (
          <span
            className="kn-cat"
            title={persona.version ? `Charter content version ${persona.version}` : undefined}
          >
            {subtitle}
          </span>
        )}
        <span className="mono faint" style={{ marginLeft: 'auto' }}>
          {persona.reviews} review{persona.reviews === 1 ? '' : 's'} recorded
        </span>
      </div>
      {persona.bar && <div className="kn-desc">&ldquo;{persona.bar}&rdquo;</div>}
      <div className="kn-meta mono" style={{ flexDirection: 'column', alignItems: 'flex-start', gap: '4px' }}>
        {named.length > 0 && (
          <span title={`ids: ${named.join(', ')}`}>competent for: {namedForReading.join(', ')}</span>
        )}
        {c.wording && <span>competent to judge the wording of any instruction</span>}
        {bullets.length > 0 ? (
          <div className="faint">
            <span>also says it can judge (not used to route reviews):</span>
            <ul className="kn-competence-bullets">
              {bullets.map((b, i) => (
                <li key={i}>{b}</li>
              ))}
            </ul>
          </div>
        ) : (
          c.domains && c.domains.length > 0 && (
            <span className="faint">
              also says it can judge (not used to route reviews): {c.domains.join(', ')}
            </span>
          )
        )}
      </div>
    </div>
  );
}

/** Registry lifecycle → status chip color. */
// ── Skills registry ────────────────────────────────────────────────────────
//
// The registry is where skills are authored and where extracted DRAFTS are
// approved. A draft never reaches a codex prompt, so this approve/reject pair is
// the gate between "a run worked" and "the platform knows how to do that".

interface SkillFormValues {
  id: string;
  name: string;
  category: string;
  reasoning: string;
  tags: string;
  instruction: string;
  doc: string;
}

/** Reasoning effort a skill asks codex for. '' means "leave it to the node".
 *  These are the efforts the engine actually accepts (see ExecContext.reasoning). */
const REASONING_OPTIONS = [
  { value: '', label: 'default' },
  { value: 'low', label: 'low' },
  { value: 'medium', label: 'medium' },
  { value: 'high', label: 'high' },
];

function readTags(metadata: Record<string, unknown> | undefined): string[] {
  const raw = metadata?.tags;
  if (!Array.isArray(raw)) return [];
  return raw.filter((t): t is string => typeof t === 'string');
}

function toFormValues(skill?: Skill): SkillFormValues {
  const meta = skill?.metadata;
  return {
    id: skill?.id ?? '',
    name: skill?.name ?? '',
    category: skill?.category ?? '',
    // Carried through the form on purpose: the save is a full-row upsert, so a
    // field the form drops is a field the save ERASES — seeded skills carry
    // reasoning: "high" and an unrelated edit used to silently downgrade them.
    reasoning: skill?.reasoning ?? '',
    tags: readTags(meta).join(', '),
    instruction: skill?.instruction ?? '',
    doc: typeof meta?.doc === 'string' ? meta.doc : '',
  };
}

/** kebab-cases a title into a usable skill id. */
function slugifyId(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
}

/** First sentence of the instruction — what the skill card and search index show. */
function firstSentence(s: string, max = 220): string {
  const flat = s.replace(/\s+/g, ' ').trim();
  const end = flat.indexOf('. ');
  const head = end > 0 ? flat.slice(0, end + 1) : flat;
  return head.length > max ? `${head.slice(0, max)}…` : head;
}

function SkillForm({ skill, onClose }: { skill?: Skill; onClose: () => void }) {
  const queryClient = useQueryClient();
  const editing = Boolean(skill);
  const [values, setValues] = useState<SkillFormValues>(() => toFormValues(skill));
  const [error, setError] = useState('');

  const save = useMutation({
    mutationFn: (next: Skill) => api.saveSkill(next),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] });
      onClose();
    },
    onError: (err) => setError(err instanceof Error ? err.message : 'Save failed'),
  });

  const set =
    (key: keyof SkillFormValues) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
      setValues((v) => ({ ...v, [key]: e.target.value }));

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const id = (values.id.trim() || slugifyId(values.name)).trim();
    if (!id || !values.name.trim() || !values.instruction.trim()) {
      setError('id, title, and instruction are required');
      return;
    }
    const tags = values.tags
      .split(',')
      .map((t) => t.trim().toLowerCase())
      .filter(Boolean);
    // Carry the rest of the metadata forward — status, sourceRun, confidence and
    // anything else extraction wrote must survive an edit.
    const metadata: Record<string, unknown> = { ...(skill?.metadata ?? {}) };
    if (values.doc.trim()) metadata.doc = values.doc.trim();
    else delete metadata.doc;
    if (tags.length > 0) metadata.tags = tags;
    else delete metadata.tags;

    setError('');
    // Every column of store.Skill except the server-owned updatedAt: dropping
    // one here does not leave it alone, it clears it (the save is an upsert with
    // ON CONFLICT ... SET every field).
    save.mutate({
      id,
      name: values.name.trim(),
      category: values.category.trim(),
      reasoning: values.reasoning,
      description: skill?.description || firstSentence(values.instruction),
      instruction: values.instruction.trim(),
      metadata,
    });
  }

  return (
    <form className="panel kn-form" onSubmit={submit}>
      <div className="kn-form-head">
        <span className="eyebrow">{editing ? `Edit ${skill?.id}` : 'New skill'}</span>
      </div>

      <div className="kn-form-grid">
        <label className="kn-field">
          <span className="kn-field-label">Title</span>
          <input
            className="input"
            value={values.name}
            onChange={set('name')}
            placeholder="Competitive teardown"
            aria-label="Skill title"
          />
        </label>
        <label className="kn-field">
          <span className="kn-field-label">Domain</span>
          <input
            className="input"
            value={values.category}
            onChange={set('category')}
            placeholder="research"
            aria-label="Skill domain"
          />
        </label>
        {!editing && (
          <label className="kn-field">
            <span className="kn-field-label">Id</span>
            <input
              className="input mono"
              value={values.id}
              onChange={set('id')}
              placeholder={slugifyId(values.name) || 'competitive-teardown'}
              aria-label="Skill id"
            />
          </label>
        )}
        <label className="kn-field">
          <span className="kn-field-label">Reasoning</span>
          <select
            className="input"
            value={values.reasoning}
            onChange={set('reasoning')}
            aria-label="Skill reasoning effort"
          >
            {REASONING_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <label className="kn-field">
          <span className="kn-field-label">Tags</span>
          <input
            className="input"
            value={values.tags}
            onChange={set('tags')}
            placeholder="research, teardown"
            aria-label="Skill tags"
          />
        </label>
      </div>

      <label className="kn-field">
        <span className="kn-field-label">Instruction</span>
        <textarea
          className="input kn-textarea"
          rows={5}
          value={values.instruction}
          onChange={set('instruction')}
          placeholder="The standalone instruction an agent receives when this skill is invoked."
          aria-label="Skill instruction"
        />
      </label>

      <label className="kn-field">
        <span className="kn-field-label">Doc</span>
        <textarea
          className="input kn-textarea"
          rows={6}
          value={values.doc}
          onChange={set('doc')}
          placeholder="Fuller markdown reference — loaded into the session as a context file."
          aria-label="Skill doc"
        />
      </label>

      {error && <div className="kn-feedback">{error}</div>}

      <div className="kn-form-actions">
        <button type="button" className="btn btn--sm" onClick={onClose} aria-label="Cancel skill edit">
          Cancel
        </button>
        <button type="submit" className="btn btn--sm btn--primary" disabled={save.isPending} aria-label="Save skill">
          {save.isPending ? 'Saving…' : 'Save skill'}
        </button>
      </div>
    </form>
  );
}

function SkillRow({
  skill,
  onEdit,
  refinements,
  gate,
  reviews,
}: {
  skill: Skill;
  onEdit: () => void;
  refinements: number;
  /** Whether the evalset rule refuses approval here, and what to say if so. */
  gate: ApprovalGate;
  /** Persona reviews recorded for this skill, newest first. Advisory evidence
   *  only — see PersonaVerdict; never gates the Approve button below. */
  reviews: PersonaReview[];
}) {
  const queryClient = useQueryClient();
  const { openRun } = useSelectedRun();
  const [error, setError] = useState('');
  const draft = isDraftSkill(skill);
  const lineage = skillLineage(skill);
  // How much is behind this skill, in place of the confidence scalar that used
  // to sit here. "confidence 0.90" was a number nobody could check; the run
  // count is one anybody can, and it says plainly when it is only one.
  const support = supportLabel(lineage);
  // Where this skill came from, in the terms a person recognises: the delivery
  // they accepted when there was one, else the run it was extracted from. It
  // shows on APPROVED skills too — lineage is not a draft-only fact, and "we
  // learned this from your work" is exactly what makes an approved skill
  // trustworthy later.
  const learnedFrom = learnedFromLabel(skill);
  // The bar the project it was learned under sets for a change like this one.
  // It is stated where the draft is read, and it states its own limit: the
  // reference is recorded, the re-scoring is not implemented.
  const policy = regressionPolicySentence(lineage);

  const refresh = () => {
    setError('');
    void queryClient.invalidateQueries({ queryKey: ['skills'] });
  };
  const onError = (err: unknown) => setError(err instanceof Error ? err.message : 'Failed');

  const approve = useMutation({ mutationFn: () => api.approveSkill(skill.id), onSuccess: refresh, onError });
  const reject = useMutation({ mutationFn: () => api.rejectSkill(skill.id), onSuccess: refresh, onError });
  const busy = approve.isPending || reject.isPending;

  return (
    <div className="kn-row">
      <div className="kn-top">
        <span className="kn-id">{skill.name}</span>
        {skill.category && <span className="kn-cat">{skill.category}</span>}
        {draft && (
          <span className="chip s-block" style={{ fontSize: 'var(--t-xs)' }}>
            draft
          </span>
        )}
      </div>
      {skill.description && <div className="kn-desc">{skill.description}</div>}
      {(learnedFrom || refinements > 0 || support || lineage.approvedWithoutEvalset) && (
        <div className="kn-meta mono">
          {/* Provenance has to be FOLLOWABLE: the run it was learned from opens
              the run, not a runs list to hunt through. Skills with no recorded
              run (hand-written, or imported) keep the plain label. */}
          {learnedFrom && (
            lineage.sourceRun ? (
              <span>
                learned from{' '}
                <button
                  type="button"
                  className="kn-lineage-link"
                  onClick={() => openRun(lineage.sourceRun!)}
                  aria-label={`Open run ${lineage.sourceRun}, which this skill was learned from`}
                  title={lineage.sourceRun}
                >
                  {learnedFrom} ↗
                </button>
              </span>
            ) : (
              <span>learned from {learnedFrom}</span>
            )
          )}
          {support && <span className="kn-support">{support}</span>}
          {refinements > 0 && <span>refined ×{refinements} by the improvement loop</span>}
          {lineage.approvedWithoutEvalset && <span className="kn-support">approved without an evalset</span>}
        </div>
      )}
      {policy && (
        <p className="faint" style={{ fontSize: 'var(--t-sm)' }}>This draft {policy}</p>
      )}
      {/* Said in full, not as a meta chip: "learned from run …research" beside a
          working link is exactly what made this draft look like evidence. The
          run it points at planned a graph and executed nothing. */}
      {lineage.sourceRunRehearsal && (
        <p className="kn-feedback">
          Learned from a REHEARSAL — its outputs were stand-ins; this draft has no real evidence.
        </p>
      )}
      {/* A learned skill with no quotes was extracted before evidence pinning
          existed. That is a fact about the draft, not an absence to render as
          blank space — and it is exactly what a reviewer needs to know before
          approving it. */}
      {lineage.evidence.length === 0 && lineage.supportRuns > 0 && (
        <p className="kn-evidence-none faint">
          No pinned evidence — extracted before evidence pinning. Its grounds have to be read
          in the run itself.
        </p>
      )}
      {/* The excerpts extraction pinned — the lines in the runs that made it
          name this method. Each opens its node, so the claim stays checkable
          long after the proposal was approved. */}
      {lineage.evidence.length > 0 && (
        <ul className="kn-evidence">
          {lineage.evidence.map((ev, i) => {
            const runId = ev.runId ?? lineage.sourceRun;
            return (
              <li key={`${ev.nodeId}-${i}`} className="kn-evidence-item">
                <q className="kn-evidence-quote">{ev.quote}</q>
                {runId ? (
                  <button
                    type="button"
                    className="btn btn--sm btn--ghost mono"
                    onClick={() => openRun(runId)}
                    aria-label={`Open node ${ev.nodeId} of run ${runId}`}
                  >
                    {ev.nodeId} ↗
                  </button>
                ) : (
                  <span className="mono kn-evidence-node">{ev.nodeId}</span>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {/* A competent reader's judgement, beside the measured evidence above —
          never a second gate. See PersonaVerdict. */}
      <PersonaVerdict reviews={reviews} />
      {/* The evalset rule belongs on the surface that holds the button, not
          only in Skill Lab's detail view: this is where someone decides. The
          server's 409 stays as the backstop. */}
      {draft && gate.blocked && (
        <div className="kn-feedback">
          {gate.reason}
          {gate.command && (
            <>
              {': '}
              <code className="mono">{gate.command}</code>
            </>
          )}
        </div>
      )}
      {/* The same generator, from the other surface that holds an Approve
          button. A rule whose only remedy is a terminal is a rule the UI
          cannot honestly enforce (D-04). */}
      {draft && gate.blocked && gate.command && <GeneratePracticeTest skillId={skill.id} />}
      {error && <div className="kn-feedback">{error}</div>}
      <div className="kn-actions">
        <button className="btn btn--sm btn--ghost" onClick={onEdit} aria-label={`Edit skill ${skill.id}`}>
          Edit
        </button>
        {draft && (
          <>
            <button
              className="btn btn--sm btn--primary"
              disabled={busy || gate.blocked}
              onClick={() => approve.mutate()}
              title={gate.blocked ? gateSentence(gate) : undefined}
              aria-label={
                gate.blocked
                  ? `Approve is blocked for ${skill.id}: ${gateSentence(gate)}`
                  : `Approve draft skill ${skill.id}`
              }
            >
              {approve.isPending ? 'Approving…' : 'Approve'}
            </button>
            <button
              className="btn btn--sm"
              disabled={busy}
              onClick={() => reject.mutate()}
              aria-label={`Reject draft skill ${skill.id}`}
            >
              {reject.isPending ? 'Rejecting…' : 'Reject'}
            </button>
          </>
        )}
      </div>
    </div>
  );
}

// ── Knowledge view ─────────────────────────────────────────────────────────

type SkillEditorState = { mode: 'new' } | { mode: 'edit'; skill: Skill };

export function Knowledge() {
  const [activeTab, setActiveTab] = useState<KnowledgeTab>('registry');
  const [editing, setEditing] = useState<SkillEditorState | null>(null);
  const [rawSearch, setRawSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      setDebouncedSearch(rawSearch);
    }, 300);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [rawSearch]);

  const { data: skills, isLoading: skillsLoading } = useQuery<Skill[]>({
    queryKey: ['skills', debouncedSearch],
    queryFn: () => api.skills(debouncedSearch || undefined),
  });

  // The improvement loop's history is the only record of which skills it has
  // actually changed, so the registry reads it to show each skill's refinement
  // count next to where it was learned from — one lineage, both halves.
  const { data: improveRuns } = useQuery<ImproveRun[]>({
    queryKey: ['improveRuns'],
    queryFn: () => api.improveRuns(),
  });

  // The registered audit personas (see personas/*.md), and every persona
  // review ever recorded — fetched ONCE here and sliced per skill row below,
  // rather than one request per row.
  const { data: personas, isLoading: personasLoading } = useQuery<PersonaView[]>({
    queryKey: ['personas'],
    queryFn: () => api.personas(),
  });
  const { data: personaReviews } = useQuery<PersonaReview[]>({
    queryKey: ['personaReviews'],
    queryFn: () => api.personaReviews(),
  });

  // The registry is the second surface that carries an Approve button, so it
  // asks the evalset rule for itself rather than discovering it from a 409.
  const approvalGate = useApprovalGate();

  const safeSkills = Array.isArray(skills) ? skills : [];
  const safePersonas = Array.isArray(personas) ? personas : [];
  const safePersonaReviews = Array.isArray(personaReviews) ? personaReviews : [];
  const draftCount = safeSkills.filter(isDraftSkill).length;
  const refinements = refinementCounts(Array.isArray(improveRuns) ? improveRuns : []);

  // id → the name that thing is called everywhere else, for the persona cards'
  // "competent for:" list. Built from what this page already fetched.
  const displayNames = new Map<string, string>();
  for (const s of safeSkills) if (s.name) displayNames.set(s.id, s.name);

  const tabs: { id: KnowledgeTab; label: string }[] = [
    { id: 'registry',  label: 'Registry' },
    { id: 'skill-lab', label: 'Skill Lab' },
    { id: 'radar',     label: 'Radar' },
    { id: 'stacks',    label: 'Stacks' },
    { id: 'categories', label: 'Categories' },
    { id: 'templates', label: 'Templates' },
    { id: 'decisions', label: 'Decisions' },
    { id: 'graph', label: 'Knowledge map' },
  ];

  return (
    <div className="view">
      <div className="view-head">
        <span className="eyebrow">Knowledge</span>
        <h1 className="h1">Knowledge</h1>
      </div>

      {/* Tab bar */}
      <div className="kn-tabs" role="tablist" aria-label="Knowledge sections">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            role="tab"
            aria-selected={activeTab === tab.id}
            className={`kn-tab${activeTab === tab.id ? ' kn-tab--active' : ''}`}
            onClick={() => setActiveTab(tab.id)}
            aria-label={tab.label}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {activeTab === 'skill-lab' ? (
        <SkillLab skills={safeSkills} skillsLoading={skillsLoading} />
      ) : activeTab === 'radar' ? (
        <RadarTab />
      ) : activeTab === 'stacks' ? (
        <StacksTab />
      ) : activeTab === 'categories' ? (
        <CategoriesTab />
      ) : activeTab === 'templates' ? (
        <TemplatesTab />
      ) : activeTab === 'decisions' ? (
        <DecisionsTab />
      ) : activeTab === 'graph' ? (
        <KnowledgeGraph />
      ) : (
        <>
          <div className="search-wrap">
            <input
              type="search"
              className="input"
              placeholder="Search skills…"
              value={rawSearch}
              onChange={(e) => setRawSearch(e.target.value)}
              aria-label="Search skills"
            />
          </div>

          {/* Skills section */}
          <div style={{ marginBottom: 'var(--s6)' }}>
            <div className="section-head kn-section-head">
              <span className="eyebrow">
                Skills <span className="mono">{skillsLoading ? '…' : safeSkills.length}</span>
                {draftCount > 0 && (
                  <span className="chip s-block kn-draft-count" style={{ fontSize: 'var(--t-xs)' }}>
                    {draftCount} draft
                  </span>
                )}
              </span>
              {editing === null && (
                <button
                  className="btn btn--sm"
                  onClick={() => setEditing({ mode: 'new' })}
                  aria-label="Create a new skill"
                >
                  New skill
                </button>
              )}
            </div>

            {editing && (
              <SkillForm
                key={editing.mode === 'edit' ? editing.skill.id : 'new'}
                skill={editing.mode === 'edit' ? editing.skill : undefined}
                onClose={() => setEditing(null)}
              />
            )}

            {skillsLoading ? (
              <div className="empty"><span>loading skills…</span></div>
            ) : safeSkills.length === 0 ? (
              <div className="empty">
                <span className="mark">✦</span>
                <span>{rawSearch ? `No skills matching "${rawSearch}"` : 'No skills found.'}</span>
              </div>
            ) : (
              <div className="panel">
                {safeSkills.map((skill) => (
                  <SkillRow
                    key={skill.id}
                    skill={skill}
                    onEdit={() => setEditing({ mode: 'edit', skill })}
                    refinements={refinements[skill.id] ?? 0}
                    gate={approvalGate(skill)}
                    reviews={safePersonaReviews.filter((r) => r.target === `skill:${skill.id}`)}
                  />
                ))}
              </div>
            )}
          </div>

          {/* Audit personas section (AI-12) — the registered charters from
              personas/*.md, read-only here: a charter is amended in git, never
              through the app. This panel is the roster; a review is STARTED
              from the CLI or the API (`hyperplane persona review <id> --target
              skill:<id>`), and its verdict comes back onto the cards above. */}
          <div style={{ marginTop: 'var(--s6)' }}>
            <div className="section-head">
              <span className="eyebrow">Personas <span className="mono">{personasLoading ? '…' : safePersonas.length}</span></span>
            </div>

            {personasLoading ? (
              <div className="empty"><span>loading personas…</span></div>
            ) : safePersonas.length === 0 ? (
              <div className="empty">
                <span>
                  No personas registered yet. Personas are imported from the repository&rsquo;s{' '}
                  <code className="mono">personas/</code> folder — in a terminal at the repository
                  root, run <code className="mono">make seed</code>.
                </span>
              </div>
            ) : (
              <div className="panel">
                {safePersonas.map((persona) => (
                  <PersonaRow key={persona.id} persona={persona} displayNames={displayNames} />
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
