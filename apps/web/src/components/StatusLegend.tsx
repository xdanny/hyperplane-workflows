/**
 * "What these statuses mean" — the one disclosure that replaces ~170 words of
 * boilerplate repeated above the results of every run page (Q-11, C10).
 *
 * Nothing is deleted. The caveats that made the old paragraphs worth having —
 * that completion is not correctness, that a model review is a judgment, that
 * an approval is a person's decision — are all here, said once, under a summary
 * a reader opens when a word puzzles them rather than reading four times a day.
 */

import { CHIP_LABEL, CHIP_MEANING } from '../runLexicon';
import type { RunPresentation } from '../runLexicon';

/** Chip words in the order a reader meets them, not alphabetical. */
const ORDER: RunPresentation[] = ['running', 'waiting', 'passed', 'review_failed', 'check_failed', 'failed', 'stopped'];

export function StatusLegend({ nodeWorkflow = true }: { nodeWorkflow?: boolean }) {
  return (
    <details className="status-legend">
      <summary>What these statuses mean</summary>
      <dl className="status-legend-list">
        {ORDER.map((kind) => (
          <div key={kind} className="status-legend-row">
            <dt><span className={`chip ${kind === 'passed' ? 's-pass' : kind === 'running' ? 's-live' : kind === 'waiting' ? 's-block' : kind.includes('failed') ? 's-fail' : 's-idle'}`}>{CHIP_LABEL[kind]}</span></dt>
            <dd>{CHIP_MEANING[kind]}</dd>
          </div>
        ))}
      </dl>
      <p className="faint">
        A step that saved a document proves a response was saved, not that it is correct. A file-creation
        step records the files it wrote. An attached program check tests exactly what its command asserts.
        An independent model review is a second model’s judgment against stated criteria, and models are
        fallible. Your approval or sign-off records your judgment; it changes no recorded check result.
      </p>
      {nodeWorkflow && (
        <p className="faint">
          Prerequisites decide when a step is ready. Two document, research or review steps can run at once after
          their prerequisites finish; file-creation and approval steps run one at a time. Research steps
          use live web search and independent reviews can verify sources online; document steps have web
          search disabled. External app actions are unavailable.
        </p>
      )}
    </details>
  );
}
