/* Renders an arbitrary workflow output richly — the heart of the decision surface
   and the run output panels. Detects the common shapes (ideas, spec, text, shell,
   gate verdict) and falls back to readable JSON. */

function asObj(v: unknown): Record<string, unknown> | null {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
}
function str(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined;
}

/** Best-effort human label + summary for a candidate/idea object. */
export function cardTitle(o: Record<string, unknown>, i: number): string {
  return str(o.title) ?? str(o.name) ?? str(o.label) ?? str(o.id) ?? `Option ${i + 1}`;
}
function cardSummary(o: Record<string, unknown>): string | undefined {
  return str(o.rationale) ?? str(o.summary) ?? str(o.description) ?? str(o.killerDifferentiator);
}

function IdeaCards({ items }: { items: Record<string, unknown>[] }) {
  return (
    <div className="idea-grid">
      {items.slice(0, 12).map((o, i) => (
        <div className="idea" key={i}>
          <div className="idea-title">{cardTitle(o, i)}</div>
          {cardSummary(o) && <div className="idea-sum">{cardSummary(o)}</div>}
          <div className="idea-chips">
            {str(o.impact) && <span className="tag">impact: {str(o.impact)}</span>}
            {str(o.effort) && <span className="tag">effort: {str(o.effort)}</span>}
          </div>
        </div>
      ))}
    </div>
  );
}

export function OutputView({ value }: { value: unknown }) {
  const o = asObj(value);

  // Ideas / concepts
  const ideas = o?.ideas;
  if (Array.isArray(ideas) && ideas.every((x) => typeof x === 'object')) {
    return <IdeaCards items={ideas as Record<string, unknown>[]} />;
  }
  if (Array.isArray(value) && value.every((x) => x && typeof x === 'object')) {
    return <IdeaCards items={value as Record<string, unknown>[]} />;
  }

  // Gate verdict
  if (o && typeof o.pass === 'boolean') {
    return (
      <div className="verdict">
        <span className={`chip ${o.pass ? 's-pass' : 's-fail'}`}>
          <span className="dot" />{o.pass ? 'pass' : 'fail'}
        </span>
        {str(o.reasons) && <span className="muted">{str(o.reasons)}</span>}
      </div>
    );
  }

  // Plain text / shell
  const text = str(o?.text) ?? str(o?.stdout) ?? str(value);
  if (text) return <pre className="out-text">{text.slice(0, 1500)}</pre>;

  // Fallback: readable JSON
  return <pre className="out-json">{JSON.stringify(value, null, 2).slice(0, 1500)}</pre>;
}
