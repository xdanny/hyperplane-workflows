import { qualityKeyColumns } from './check-inputs.mjs';
export { qualityKeyColumns } from './check-inputs.mjs';
import { getCheck } from './check-catalog.mjs';
import {readJSON as readReport} from './files.mjs';
import { open, glob, stat } from 'node:fs/promises';
import path from 'node:path';
import { checkedProgram } from './checked-program.mjs';

export const qualityChecks = ['data-quality-profile', 'data-quality-rules', 'data-quality-audit'];
const require = (condition, message) => { if (!condition) throw new Error(message); };
const text = value => typeof value === 'string' && Boolean(value.trim());
const count = value => Number.isSafeInteger(value) && value >= 0;
const list = (value, label) => { require(Array.isArray(value), `${label} must be an array`); return value; };

// This is a checked SDK consumer, not another scheduler. Generated programs run
// through SDK command tools in the step sandbox. Host checks read bounded files;
// they never execute generated programs outside that sandbox.
// The built-in check owns its output contract for templates and described nodes alike.


export function qualityAcceptance(kind, workspace, previous, inputs, { signal } = {}) {
  const readJSON = (folder, name) => readReport(folder, name, { signal });
  require(qualityChecks.includes(kind), 'Unknown data-quality acceptance');
  const prior = acceptance => previous.findLast(n => n.acceptance === acceptance)?.workspace;
  const executes = kind !== 'data-quality-rules';
  const program = checkedProgram(workspace, 'checks.sh', {signal});
  const profileFolder = kind === 'data-quality-profile' ? workspace : prior('data-quality-profile');
  require(profileFolder, 'Measure the source before proposing or executing quality rules');
  const rulesFolder = kind === 'data-quality-rules' ? workspace : prior('data-quality-rules');
  if (kind === 'data-quality-audit') require(rulesFolder, 'Propose quality rules before execution');
  const relative = folder => path.relative(workspace, folder).split(path.sep).join('/') || '.';
  const sources = kind === 'data-quality-profile' ? '' : `Read ${relative(profileFolder)}/data-profile.json. `
    + (kind === 'data-quality-audit' ? `Execute the approved rules from ${relative(rulesFolder)}/quality-rules.json. ` : '');
  return {
    description: (executes ? program.description : 'Do not execute rules before approval.')
      + '\n' + sources + getCheck(kind).description
      + '\nThe SDK supplies these required output fields and resolved source paths for this built-in check. Every quality-rules.json rule must also contain expectations:[string], listing the exact profile expectations it covers. Each profile expectation must appear in a rule or in unexpressible with a reason. Only executable rules belong in rules; place non-executable expectations in unexpressible. Perform only the current step; original sources are read-only and external access or tool setup requires explicit step permissions.',
    onEvent: executes ? program.onEvent : async () => {},
    async verify() {
      try {
        if (executes) await program.verify();
        if (kind === 'data-quality-profile') {
          const p = await readJSON(workspace, 'data-profile.json');
          require(p.dataset?.verified === true && count(p.dataset.rowCount) && p.dataset.rowCount > 0, 'Profile must identify verified nonempty source data');
          require(['full', 'sample'].includes(p.dataset.scanned) && (p.dataset.scanned !== 'sample' || text(p.dataset.sampleNote)), 'State full scan or explain the sample');
          const declared = qualityKeyColumns(inputs.keyColumns);
          const cols = list(p.columns, 'columns');
          require(declared.every(k => cols.some(c => c.name === k)), 'Declared key columns missing from the measured profile'); require(cols.length > 0, 'Profile needs columns');
          require(new Set(cols.map(c => c.name)).size === cols.length, 'Duplicate profile columns');
          for (const c of cols) require(text(c.name) && text(c.type) && count(c.nullCount) && c.nullCount <= p.dataset.rowCount && count(c.distinctCount) && c.distinctCount <= p.dataset.rowCount && Number.isFinite(c.nullPct), 'Invalid column counts');
          for (const k of list(p.candidateKeys, 'candidateKeys')) require(list(k.columns, 'key columns').length > 0 && k.columns.every(c => cols.some(x => x.name === c)) && typeof k.unique === 'boolean' && count(k.duplicateRows) && k.unique === (k.duplicateRows === 0), 'Invalid candidate key evidence');
          require(count(p.duplicateRows), 'Profile needs duplicate row count');
          for (const name of ['referentialOrphans', 'driftSignals', 'issues', 'openQuestions']) list(p[name], name);
          const beliefs = list(p.expectationEvidence, 'expectationEvidence'); require(beliefs.length > 0, 'Record evidence for owner expectations');
          for (const b of beliefs) require(text(b.expectation) && ['supported', 'contradicted', 'not-checkable'].includes(b.status) && text(b.observed), 'Invalid expectation evidence');
        } else {
          const profileFolder = prior('data-quality-profile'); require(profileFolder, 'A measured profile must precede quality rules');
          const p = await readJSON(profileFolder, 'data-profile.json');
          const ruleFolder = kind === 'data-quality-rules' ? workspace : prior('data-quality-rules');
          require(ruleFolder, 'Approved quality rules must precede execution');
          const rules = await readJSON(ruleFolder, 'quality-rules.json');
          const approved = list(rules.rules, 'rules');
          require(approved.length > 0 && new Set(approved.map(r => r.id)).size === approved.length, 'Need at least one unique executable rule');
          for (const r of approved) {
            require(/^[a-z][a-z0-9-]{0,63}$/.test(r.id) && ['blocker','major','minor'].includes(r.severity), 'Invalid rule identity or severity');
            for (const key of ['rule','dimension','assertion','scope','why','profileEvidence','evidenceRequired']) require(text(r[key]), `Rule ${r.id} needs ${key}`);
            require(['count','rate'].includes(r.threshold?.kind) && Number.isFinite(r.threshold.value) && r.threshold.value >= 0 && (r.threshold.kind !== 'rate' || r.threshold.value <= 1) && text(r.threshold.justification), 'Rules need explicit count/rate thresholds and justification');
            require(r.expressible === true && typeof r.expectedToFail === 'boolean', 'Only executable rules belong in rules; state expectedToFail');
            list(r.expectations, 'rule expectations');
          }
          const missing = list(rules.unexpressible, 'unexpressible');
          for (const b of missing) require(text(b.expectation) && text(b.why), 'Unexpressible expectations need a reason');
          for (const b of p.expectationEvidence) require(approved.some(r => r.expectations.includes(b.expectation)) || missing.some(r => r.expectation === b.expectation), `Expectation dropped: ${b.expectation}`);
          if (kind === 'data-quality-audit') {
            const report = await readJSON(workspace, 'quality-scorecard.json');
            require(count(report.dataset?.rows) && report.dataset.rows > 0 && text(report.dataset.identity) && ['full','sample'].includes(report.dataset.scanned) && (report.dataset.scanned !== 'sample' || text(report.dataset.sampleNote)), 'Scorecard needs source identity and scan scope');
            require(list(report.notRun, 'notRun').length === 0, 'Every approved rule must execute');
            const results = list(report.rules, 'scorecard rules');
            require(results.length === approved.length && new Set(results.map(r => r.id)).size === results.length, 'Every approved rule must appear exactly once');
            const severity = Object.fromEntries(['blocker','major','minor'].map(k => [k,{failed:0,total:0}]));
            const classes = Object.fromEntries(['violation','evidence-gap'].map(k => [k,{failed:0,total:0}]));
            for (const r of results) {
              const rule = approved.find(a => a.id === r.id); require(rule && r.severity === rule.severity, 'Rule missing or approved severity changed');
              require(count(r.checked) && r.checked > 0 && count(r.violations) && r.violations <= r.checked && text(r.checkedUnit) && text(r.query), 'Each result needs real counts, units and query');
              const rate = r.violations / r.checked;
              require(Number.isFinite(r.rate) && Math.abs(r.rate-rate) < 1e-10, 'Violation rate disagrees with counts');
              require(r.pass === ((rule.threshold.kind === 'count' ? r.violations : rate) <= rule.threshold.value), 'Verdict disagrees with approved threshold');
              require(['violation','evidence-gap'].includes(r.class) && (r.class !== 'evidence-gap' || text(r.missingEvidence)), 'Classify missing evidence explicitly');
              for (const x of [severity[r.severity], classes[r.class]]) { x.total++; if (!r.pass) x.failed++; }
            }
            for (const [name, expected] of [['bySeverity',severity],['byClass',classes]]) for (const [key, value] of Object.entries(expected)) require(report[name]?.[key]?.failed === value.failed && report[name]?.[key]?.total === value.total, `${name} rollup disagrees with rules`);
            require(report.passed === results.filter(r => r.pass).length && report.failed === results.filter(r => !r.pass).length, 'Overall scorecard counts disagree');
            const negative = report.negativeCase;
            require(negative?.tripped === true && approved.some(r => r.id === negative.rule) && count(negative.seededRows) && negative.seededRows > 0 && count(negative.detectedRows) && negative.detectedRows >= negative.seededRows, 'A seeded violation of an approved rule must be detected');
          }
        }
        return { passed: true, findings: [] };
      } catch (error) { return { passed: false, findings: [error.message] }; }
    },
  };
}

// Preserve the retired recipe's pre-model input gate. Local files must be
// readable and declared keys must exist in their real header. Table references
// receive presence validation; the SDK and independent review check access.
export async function checkQualityInputs(inputs, {keysMustExist=true} = {}) {
  const keys = qualityKeyColumns(inputs.keyColumns);
  const source = inputs.dataset.trim();
  let patterns;
  try { patterns = (await stat(source)).isFile() && /\.(csv|tsv|txt)$/i.test(source) ? [source] : []; } catch {
    patterns = [...source.matchAll(/"([^"\n]+\.(?:csv|tsv|txt))"|'([^'\n]+\.(?:csv|tsv|txt))'|([^\s(),;]+\.(?:csv|tsv|txt))/gi)].map(m => m[1] || m[2] || m[3]);
  }
  for (const pattern of patterns) {
    require(path.isAbsolute(pattern), 'Use absolute source-file paths so saved workflows do not depend on the server working directory');
    let found = false;
    for await (const filename of glob(pattern)) {
      require((await stat(filename)).isFile(), 'Source must be a regular file');
      const file = await open(filename, 'r');
      let header;
      try { const buffer = Buffer.alloc(65536); const {bytesRead} = await file.read(buffer,0,buffer.length,0); header = buffer.subarray(0,bytesRead).toString('utf8').split(/\r?\n/)[0]; } finally { await file.close(); }
      const delimiter = /\.tsv$/i.test(filename) ? '\t' : ',';
      const columns = []; let field = '', quoted = false;
      for (let i=0; i<header.length; i++) {
        const c=header[i];
        if (c==='"') { if (quoted && header[i+1]==='"') { field+='"'; i++; } else quoted=!quoted; }
        else if (c===delimiter && !quoted) { columns.push(field.trim()); field=''; }
        else field+=c;
      }
      require(!quoted, 'Cannot parse the source header within 64 KB'); columns.push(field.trim());
      require(!keysMustExist || keys.every(k => columns.includes(k)), 'Key columns not found in the declared source header');
      found = true;
      break; // Match the original input gate: first readable header per source.
    }
    require(found, 'The declared source file cannot be read');
  }
}
