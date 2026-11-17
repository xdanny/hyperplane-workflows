/* DocumentViewer — renders prose/structured documents instead of raw JSON.
   Detection logic:
     - MIME text/markdown (output.mime === 'text/markdown')
     - JSON with top-level key 'report' | 'document' | 'sections' → titled sections
     - JSON with 'days' | 'itinerary' → day-by-day cards
     - JSON with 'steps' | 'checklist' → checkable list (display-only)
     - JSON with 'summary' (string) → prose block
   Falls back to null if the output doesn't match any document shape.

   Rendering: hand-rolled minimal safe markdown (headings, bold/italic, lists,
   links, tables, code fences, blockquotes). `renderMarkdown` is exported — the
   ArtifactReader renders .md artifacts through the same transformer.
   Zero new deps. All styles from theme tokens. */

import { useState } from 'react';

// ── Types ─────────────────────────────────────────────────────────────────────

export type DocMode = 'document' | 'json';

interface Section {
  heading: string;
  body: string;
}

interface DayEntry {
  day?: string | number;
  date?: string;
  title?: string;
  activities?: string[] | string;
  notes?: string;
  [key: string]: unknown;
}

interface StepEntry {
  step?: string | number;
  label?: string;
  text?: string;
  title?: string;
  done?: boolean;
  [key: string]: unknown;
}

export type DocShape =
  | { kind: 'sections'; title?: string; summary?: string; sections: Section[] }
  | { kind: 'itinerary'; title?: string; days: DayEntry[] }
  | { kind: 'checklist'; title?: string; steps: StepEntry[] }
  | { kind: 'prose'; title?: string; text: string }
  | { kind: 'markdown'; text: string };

// ── Detection ─────────────────────────────────────────────────────────────────

function asStr(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined;
}

function asStrArr(v: unknown): string[] | undefined {
  if (!Array.isArray(v)) return undefined;
  if (v.every((x) => typeof x === 'string')) return v as string[];
  return undefined;
}

function asObjArr(v: unknown): Record<string, unknown>[] | undefined {
  if (!Array.isArray(v)) return undefined;
  if (v.every((x) => x && typeof x === 'object' && !Array.isArray(x))) {
    return v as Record<string, unknown>[];
  }
  return undefined;
}

/**
 * Attempt to detect a document shape from an arbitrary output value.
 * Returns null when no document shape is detected (caller should use OutputView).
 */
export function detectDocShape(value: unknown): DocShape | null {
  // 1. Plain string — treat as markdown
  if (typeof value === 'string' && value.trim().length > 0) {
    return { kind: 'markdown', text: value };
  }

  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const o = value as Record<string, unknown>;

  // 2. Explicit mime flag
  if (o.mime === 'text/markdown') {
    const text = asStr(o.text) ?? asStr(o.content) ?? asStr(o.body);
    if (text) return { kind: 'markdown', text };
  }

  // 3. report / document / sections shape
  const sectionsRaw = o.sections ?? (o.report as Record<string, unknown> | undefined)?.sections ?? (o.document as Record<string, unknown> | undefined)?.sections;
  if (Array.isArray(sectionsRaw) && sectionsRaw.length > 0) {
    const sections: Section[] = sectionsRaw
      .filter((s): s is Record<string, unknown> => s && typeof s === 'object')
      .map((s) => ({
        heading: asStr(s.heading) ?? asStr(s.title) ?? asStr(s.name) ?? '',
        body: asStr(s.body) ?? asStr(s.content) ?? asStr(s.text) ?? '',
      }))
      .filter((s) => s.heading || s.body);
    if (sections.length > 0) {
      const root = (o.report as Record<string, unknown> | undefined) ??
                   (o.document as Record<string, unknown> | undefined) ?? o;
      return {
        kind: 'sections',
        title: asStr(root.title) ?? asStr(o.title) ?? asStr(o.name),
        summary: asStr(root.summary) ?? asStr(o.summary),
        sections,
      };
    }
  }

  // 4. days / itinerary
  const daysRaw = o.days ?? o.itinerary;
  const days = asObjArr(daysRaw);
  if (days && days.length > 0) {
    return {
      kind: 'itinerary',
      title: asStr(o.title) ?? asStr(o.name),
      days: days as DayEntry[],
    };
  }

  // 5. steps / checklist
  const stepsRaw = o.steps ?? o.checklist;
  const steps = asObjArr(stepsRaw) ?? asStrArr(stepsRaw)?.map((s, i) => ({ step: i + 1, text: s }));
  if (steps && steps.length > 0) {
    return {
      kind: 'checklist',
      title: asStr(o.title) ?? asStr(o.name),
      steps: steps as StepEntry[],
    };
  }

  // 6. top-level summary string (prose block)
  const summaryText = asStr(o.summary) ?? asStr(o.text) ?? asStr(o.content) ?? asStr(o.prose);
  if (summaryText && summaryText.length > 40) {
    return {
      kind: 'prose',
      title: asStr(o.title) ?? asStr(o.name),
      text: summaryText,
    };
  }

  return null;
}

// ── Safe markdown renderer ────────────────────────────────────────────────────

/** Escape HTML — prevents XSS in untrusted model output. */
function escHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#x27;');
}

/**
 * A markdown link may only carry a scheme we know is inert. Anything else
 * (javascript:, data:, vbscript:, a bare unknown scheme) renders as literal
 * text — never as an href.
 */
function safeHref(url: string): string | null {
  const u = url.trim();
  if (u === '') return null;
  if (/^https?:\/\//i.test(u) || /^mailto:/i.test(u)) return u;
  // In-app / relative targets: /file/…, ./x, ../x, #anchor.
  if (/^[#/]/.test(u) || /^\.{1,2}\//.test(u)) return u;
  return null;
}

/**
 * Inline markdown → HTML. Escapes first, so every branch below operates on
 * already-safe text; code spans and links are parked in `holds` so emphasis
 * rules cannot chew through a URL or a code sample.
 */
function inlineFormat(s: string): string {
  const holds: string[] = [];
  const hold = (html: string): string => {
    holds.push(html);
    return `\u0000${holds.length - 1}\u0000`;
  };

  let t = escHtml(s);

  // Inline code `code` — parked before emphasis so `a_b_c` stays literal.
  t = t.replace(/`([^`\n]+?)`/g, (_m, code: string) => hold(`<code>${code}</code>`));

  // Links [text](url) — an unsafe scheme falls through as plain text.
  t = t.replace(/\[([^\]\n]*)\]\(([^)\s]+)\)/g, (m: string, text: string, url: string) => {
    const href = safeHref(url);
    if (!href) return m;
    return hold(`<a class="dv-link" href="${href}" target="_blank" rel="noopener noreferrer">${text || href}</a>`);
  });

  t = t.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  t = t.replace(/__(.+?)__/g, '<strong>$1</strong>');
  t = t.replace(/~~(.+?)~~/g, '<del>$1</del>');
  t = t.replace(/\*([^*\n]+?)\*/g, '<em>$1</em>');
  t = t.replace(/(?<![a-zA-Z])_([^_\n]+?)_(?![a-zA-Z])/g, '<em>$1</em>');

  return t.replace(/\u0000(\d+)\u0000/g, (_m, i: string) => holds[Number(i)] ?? '');
}

/** Splits one markdown table row into trimmed cells. */
function tableCells(line: string): string[] {
  let s = line.trim();
  if (s.startsWith('|')) s = s.slice(1);
  if (s.endsWith('|')) s = s.slice(0, -1);
  return s.split('|').map((c) => c.trim());
}

/** A table's separator row: only pipes, dashes, colons and spaces — and at
 *  least one dash, so a plain `| a | b |` row is never mistaken for one. */
function isTableSeparator(line: string): boolean {
  const s = line.trim();
  return s.includes('-') && /^\|?[\s:|-]+\|?$/.test(s);
}

/** Reads `:---:` style alignment off a separator cell. */
function cellAlign(sep: string): string {
  const s = sep.trim();
  const left = s.startsWith(':');
  const right = s.endsWith(':');
  if (left && right) return ' style="text-align:center"';
  if (right) return ' style="text-align:right"';
  return '';
}

/**
 * Minimal markdown→HTML transformer. Hand-rolled on purpose: the golden rule is
 * minimal deps, and this covers the subset that actually shows up in Hyperplane
 * artifacts and model output.
 *
 * Handles: headings (# … ######), **bold**, *italic*, ~~strike~~, `code`,
 * fenced code blocks, pipe tables (with alignment), links, blockquotes,
 * horizontal rules, task list items, unordered/ordered lists, and blank-line
 * paragraphs. No images, no raw HTML passthrough — everything is escaped.
 */
export function renderMarkdown(raw: string): string {
  const lines = raw.replace(/\r\n?/g, '\n').split('\n');
  const out: string[] = [];
  let inUL = false;
  let inOL = false;
  let inP = false;

  function closeList() {
    if (inUL) { out.push('</ul>'); inUL = false; }
    if (inOL) { out.push('</ol>'); inOL = false; }
  }
  function closeP() {
    if (inP) { out.push('</p>'); inP = false; }
  }
  function closeBlocks() {
    closeList();
    closeP();
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trimEnd();

    // Fenced code block ```lang … ``` — content is escaped, never formatted.
    const fence = /^\s*```+\s*[\w+#.-]*\s*$/.exec(line);
    if (fence) {
      closeBlocks();
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```+\s*$/.test(lines[i])) {
        body.push(lines[i]);
        i++;
      }
      out.push(`<pre class="dv-pre"><code>${escHtml(body.join('\n'))}</code></pre>`);
      continue;
    }

    // Pipe table — needs a header row followed by a separator row.
    if (/^\s*\|.*\|\s*$/.test(line) && i + 1 < lines.length && isTableSeparator(lines[i + 1])) {
      closeBlocks();
      const head = tableCells(line);
      const aligns = tableCells(lines[i + 1]).map(cellAlign);
      i += 2;
      const body: string[][] = [];
      while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
        body.push(tableCells(lines[i]));
        i++;
      }
      i--; // the outer loop advances past the last consumed line
      const th = head.map((c, j) => `<th${aligns[j] ?? ''}>${inlineFormat(c)}</th>`).join('');
      const rows = body
        .map((r) => `<tr>${r.map((c, j) => `<td${aligns[j] ?? ''}>${inlineFormat(c)}</td>`).join('')}</tr>`)
        .join('');
      out.push(
        `<div class="dv-table-wrap"><table class="dv-table"><thead><tr>${th}</tr></thead><tbody>${rows}</tbody></table></div>`,
      );
      continue;
    }

    // Horizontal rule
    if (/^\s*([-*_])\1{2,}\s*$/.test(line)) {
      closeBlocks();
      out.push('<hr class="dv-hr">');
      continue;
    }

    // Blockquote — consecutive `>` lines collapse into one quote.
    const bq = /^>\s?(.*)$/.exec(line);
    if (bq) {
      closeBlocks();
      const quoted: string[] = [bq[1]];
      while (i + 1 < lines.length) {
        const nxt = /^>\s?(.*)$/.exec(lines[i + 1].trimEnd());
        if (!nxt) break;
        quoted.push(nxt[1]);
        i++;
      }
      out.push(`<blockquote class="dv-bq">${inlineFormat(quoted.join(' '))}</blockquote>`);
      continue;
    }

    // Headings (# … ######)
    const head = /^(#{1,6}) (.+)$/.exec(line);
    if (head) {
      closeBlocks();
      const level = Math.min(head[1].length + 1, 6); // # → h2, so the page keeps one h1
      out.push(`<h${level} class="dv-h${head[1].length}">${inlineFormat(head[2])}</h${level}>`);
      continue;
    }

    // Unordered list (including `- [ ]` / `- [x]` task items)
    const ulMatch = /^\s*[-*+] (.+)$/.exec(line);
    if (ulMatch) {
      closeP();
      if (!inUL) { if (inOL) { out.push('</ol>'); inOL = false; } out.push('<ul class="dv-ul">'); inUL = true; }
      const task = /^\[([ xX])\]\s+(.*)$/.exec(ulMatch[1]);
      if (task) {
        const done = task[1].toLowerCase() === 'x';
        out.push(
          `<li class="dv-task${done ? ' dv-task--done' : ''}"><span class="dv-task-box" aria-hidden="true">${done ? '☑' : '☐'}</span>${inlineFormat(task[2])}</li>`,
        );
      } else {
        out.push(`<li>${inlineFormat(ulMatch[1])}</li>`);
      }
      continue;
    }

    // Ordered list
    const olMatch = /^\s*\d+\. (.+)$/.exec(line);
    if (olMatch) {
      closeP();
      if (!inOL) { if (inUL) { out.push('</ul>'); inUL = false; } out.push('<ol class="dv-ol">'); inOL = true; }
      out.push(`<li>${inlineFormat(olMatch[1])}</li>`);
      continue;
    }

    // Blank line
    if (line.trim() === '') {
      closeBlocks();
      continue;
    }

    // Paragraph text
    closeList();
    if (!inP) { out.push('<p class="dv-p">'); inP = true; }
    else out.push('<br>');
    out.push(inlineFormat(line));
  }
  closeBlocks();
  return out.join('');
}

// ── Sub-renderers ─────────────────────────────────────────────────────────────

function SectionsView({ doc }: { doc: Extract<DocShape, { kind: 'sections' }> }) {
  return (
    <div className="dv-sections">
      {doc.summary && (
        <div className="dv-summary">
          <p className="dv-summary-text">{doc.summary}</p>
        </div>
      )}
      {doc.sections.map((s, i) => (
        <div key={i} className="dv-section">
          {s.heading && <h3 className="dv-section-heading">{s.heading}</h3>}
          <div
            className="dv-body"
            // eslint-disable-next-line react/no-danger
            dangerouslySetInnerHTML={{ __html: renderMarkdown(s.body) }}
          />
        </div>
      ))}
    </div>
  );
}

function ItineraryView({ doc }: { doc: Extract<DocShape, { kind: 'itinerary' }> }) {
  return (
    <div className="dv-itinerary">
      {doc.days.map((d, i) => {
        const dayLabel = asStr(d.day as unknown) ?? asStr(d.date as unknown) ?? `Day ${i + 1}`;
        const title = asStr(d.title as unknown);
        const activities = d.activities
          ? (Array.isArray(d.activities) ? d.activities : [d.activities]).map(String)
          : [];
        const notes = asStr(d.notes as unknown);
        return (
          <div key={i} className="dv-day-card">
            <div className="dv-day-header">
              <span className="dv-day-label mono">{dayLabel}</span>
              {title && <span className="dv-day-title">{title}</span>}
            </div>
            {activities.length > 0 && (
              <ul className="dv-day-activities">
                {activities.map((a, j) => <li key={j}>{a}</li>)}
              </ul>
            )}
            {notes && <p className="dv-day-notes">{notes}</p>}
          </div>
        );
      })}
    </div>
  );
}

function ChecklistView({ doc }: { doc: Extract<DocShape, { kind: 'checklist' }> }) {
  return (
    <ol className="dv-checklist">
      {doc.steps.map((s, i) => {
        const text = asStr(s.text as unknown) ?? asStr(s.label as unknown) ?? asStr(s.title as unknown) ?? '';
        const isDone = Boolean(s.done);
        return (
          <li key={i} className={`dv-checklist-item${isDone ? ' dv-checklist-item--done' : ''}`}>
            <span className="dv-checklist-num mono">{i + 1}</span>
            <span className="dv-checklist-text">{text}</span>
          </li>
        );
      })}
    </ol>
  );
}

function ProseView({ doc }: { doc: Extract<DocShape, { kind: 'prose' }> }) {
  return (
    <div
      className="dv-body"
      // eslint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: renderMarkdown(doc.text) }}
    />
  );
}

function MarkdownView({ doc }: { doc: Extract<DocShape, { kind: 'markdown' }> }) {
  return (
    <div
      className="dv-body"
      // eslint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: renderMarkdown(doc.text) }}
    />
  );
}

// ── DocumentViewer ────────────────────────────────────────────────────────────

interface DocumentViewerProps {
  value: unknown;
  /** Controlled mode — caller can pin to 'json' to show raw */
  mode?: DocMode;
  onModeChange?: (m: DocMode) => void;
  /** Whether to show the mode-toggle and copy/expand controls (default: true) */
  showControls?: boolean;
}

export function DocumentViewer({ value, mode, onModeChange, showControls = true }: DocumentViewerProps) {
  const [internalMode, setInternalMode] = useState<DocMode>('document');
  const [copied, setCopied] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const activeMode = mode ?? internalMode;
  const setMode = onModeChange ?? setInternalMode;

  const docOrNull = detectDocShape(value);

  // If no document shape detected, return null — caller should use OutputView
  if (!docOrNull) return null;

  // Re-bind as non-nullable so closures below can see the narrowed type
  const doc: DocShape = docOrNull;

  const docTitle =
    doc.kind !== 'markdown'
      ? (doc as { title?: string }).title
      : undefined;

  // Derive plain text for clipboard — doc is non-null here
  function toMarkdown(): string {
    switch (doc.kind) {
      case 'markdown': return doc.text;
      case 'prose':    return (docTitle ? `# ${docTitle}\n\n` : '') + doc.text;
      case 'sections':
        return (
          (docTitle ? `# ${docTitle}\n\n` : '') +
          (doc.summary ? doc.summary + '\n\n' : '') +
          doc.sections.map((s: Section) => `## ${s.heading}\n\n${s.body}`).join('\n\n')
        );
      case 'itinerary':
        return (
          (docTitle ? `# ${docTitle}\n\n` : '') +
          doc.days.map((d: DayEntry, i: number) => {
            const lbl = asStr(d.day as unknown) ?? asStr(d.date as unknown) ?? `Day ${i + 1}`;
            const acts = d.activities
              ? (Array.isArray(d.activities) ? d.activities : [d.activities]).map(String)
              : [];
            return `## ${lbl}${asStr(d.title as unknown) ? ': ' + asStr(d.title as unknown) : ''}\n` +
              acts.map((a: string) => `- ${a}`).join('\n') +
              (asStr(d.notes as unknown) ? '\n\n' + asStr(d.notes as unknown) : '');
          }).join('\n\n')
        );
      case 'checklist':
        return (
          (docTitle ? `# ${docTitle}\n\n` : '') +
          doc.steps.map((s: StepEntry, i: number) => {
            const t = asStr(s.text as unknown) ?? asStr(s.label as unknown) ?? '';
            return `${i + 1}. ${t}`;
          }).join('\n')
        );
    }
  }

  const handleCopy = () => {
    void navigator.clipboard.writeText(toMarkdown()).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  const handleExpand = () => setExpanded((v) => !v);

  return (
    <>
      {expanded && (
        <div
          className="dv-overlay"
          role="dialog"
          aria-modal="true"
          aria-label="Document full-screen view"
          onClick={(e) => { if (e.target === e.currentTarget) setExpanded(false); }}
        >
          <div className="dv-overlay-inner">
            <div className="dv-overlay-toolbar">
              {docTitle && <span className="dv-overlay-title">{docTitle}</span>}
              <button
                className="btn btn--ghost btn--sm"
                onClick={handleCopy}
                aria-label="Copy document as Markdown"
              >
                {copied ? 'copied ✓' : 'Copy as Markdown'}
              </button>
              <button
                className="btn btn--ghost btn--sm"
                onClick={() => setExpanded(false)}
                aria-label="Close full-screen view"
              >
                ✕
              </button>
            </div>
            <div className="dv-scroll">
              {docTitle && <h2 className="dv-title">{docTitle}</h2>}
              {doc.kind === 'sections'   && <SectionsView doc={doc} />}
              {doc.kind === 'itinerary'  && <ItineraryView doc={doc} />}
              {doc.kind === 'checklist'  && <ChecklistView doc={doc} />}
              {doc.kind === 'prose'      && <ProseView doc={doc} />}
              {doc.kind === 'markdown'   && <MarkdownView doc={doc} />}
            </div>
          </div>
        </div>
      )}

      <div className="dv-wrap">
        {showControls && (
          <div className="dv-toolbar">
            <div className="dv-mode-toggle" role="group" aria-label="View mode">
              <button
                className={`dv-mode-btn${activeMode === 'document' ? ' dv-mode-btn--active' : ''}`}
                onClick={() => setMode('document')}
                aria-label="Document view"
                aria-pressed={activeMode === 'document'}
              >
                Doc
              </button>
              <button
                className={`dv-mode-btn${activeMode === 'json' ? ' dv-mode-btn--active' : ''}`}
                onClick={() => setMode('json')}
                aria-label="Raw JSON view"
                aria-pressed={activeMode === 'json'}
              >
                JSON
              </button>
            </div>
            {activeMode === 'document' && (
              <>
                <button
                  className="btn btn--ghost btn--sm"
                  onClick={handleCopy}
                  aria-label="Copy document as Markdown"
                >
                  {copied ? 'copied ✓' : 'Copy as Markdown'}
                </button>
                <button
                  className="btn btn--ghost btn--sm"
                  onClick={handleExpand}
                  aria-label="Expand to full screen"
                >
                  ⤢
                </button>
              </>
            )}
          </div>
        )}

        {activeMode === 'json' ? (
          <pre className="out-json">{JSON.stringify(value, null, 2).slice(0, 4000)}</pre>
        ) : (
          <div className="dv-document">
            {docTitle && <h2 className="dv-title">{docTitle}</h2>}
            {doc.kind === 'sections'   && <SectionsView doc={doc} />}
            {doc.kind === 'itinerary'  && <ItineraryView doc={doc} />}
            {doc.kind === 'checklist'  && <ChecklistView doc={doc} />}
            {doc.kind === 'prose'      && <ProseView doc={doc} />}
            {doc.kind === 'markdown'   && <MarkdownView doc={doc} />}
          </div>
        )}
      </div>
    </>
  );
}
