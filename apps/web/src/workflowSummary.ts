import type { SDKWorkflowNode } from './api';
import { nodeKinds } from './workflowNodePresentation';

/**
 * "2 research · 1 document · 1 independent review" — what a saved workflow is
 * made of, in the kinds a person chose, instead of "4 steps · Documents", which
 * said the same thing about every workflow ever saved.
 */
const summaryNames: Record<keyof typeof nodeKinds, [string, string]> = {
  document: ['document', 'documents'],
  research: ['research step', 'research steps'],
  check: ['independent review', 'independent reviews'],
  approval: ['approval', 'approvals'],
  task: ['file step', 'file steps'],
};

export function stepKindSummary(nodes: SDKWorkflowNode[]): string {
  const order: (keyof typeof nodeKinds)[] = ['research', 'document', 'task', 'check', 'approval'];
  return order.flatMap(kind => {
    const count = nodes.filter(node => (node.kind ?? 'document') === kind).length;
    return count ? [`${count} ${summaryNames[kind][count === 1 ? 0 : 1]}`] : [];
  }).join(' · ');
}

/**
 * The name to show for a template. A slug is an id, not a title: "Market
 * research" is what a person picking from a list can read, and the slug stays
 * available as the option's hover text.
 */
export function templateTitle(template: { name: string; displayName?: string }): string {
  if (template.displayName?.trim()) return template.displayName.trim();
  const words = template.name.replace(/[-_]+/g, ' ').trim();
  if (!words) return template.name;
  return capitaliseTitle(words);
}

/**
 * Acronyms a slug flattens and sentence case then mangles: "kpi-commentary"
 * became "Kpi commentary" and "ml-engineering" became "Ml engineering" in the
 * picker (S-17, D-12). An allowlist, deliberately — guessing that every short
 * word is an acronym would give "Ai" for "a i" and shout at ordinary words.
 */
const ACRONYMS = new Set([
  'ai', 'api', 'crm', 'csv', 'etl', 'kb', 'kpi', 'ml', 'pr', 'qa', 'roi', 'sdk', 'sql', 'ui', 'ux',
]);

/** Sentence case, with recognised acronyms kept upper. */
export function capitaliseTitle(words: string): string {
  return words
    .split(/\s+/)
    .map((word, index) => {
      const bare = word.toLowerCase();
      if (ACRONYMS.has(bare)) return bare.toUpperCase();
      if (index > 0) return word;
      return word.charAt(0).toUpperCase() + word.slice(1);
    })
    .join(' ');
}
