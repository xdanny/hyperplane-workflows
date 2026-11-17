import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rm } from 'node:fs/promises';
import path from 'node:path';
import { runtimeFingerprint, runtimeFingerprintVersion } from './runtime.mjs';
import { validateNodes } from './validation.mjs';
import { verifyFiles } from './workflows/files.mjs';

const require = (ok, message) => { if (!ok) throw new Error(message); };
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object'
  ? Object.fromEntries(Object.keys(value).sort().filter(key => value[key] !== undefined).map(key => [key, canonical(value[key])])) : value;

// Workspace placement is deliberately excluded: continuation uses a fresh folder.
export function definitionFingerprint({ instruction, nodes, model = 'gpt-6-astra', maxTokens = Infinity, maxCorrections = 0 }) {
  validateNodes(instruction, nodes);
  require(typeof model === 'string' && model.trim(), 'Name the workflow model');
  require(maxTokens === Infinity || Number.isSafeInteger(maxTokens) && maxTokens > 0, 'Workflow token budget must be a positive integer');
  require(Number.isInteger(maxCorrections) && maxCorrections >= 0 && maxCorrections <= 5, 'Invalid workflow correction limit');
  const definition = canonical({ instruction, nodes, model, maxTokens: maxTokens === Infinity ? 'unbounded' : maxTokens, maxCorrections });
  return createHash('sha256').update('hyperplane-workflow-definition-v1\n').update(JSON.stringify(definition)).digest('hex');
}

async function directory(folder) {
  require(typeof folder === 'string' && path.isAbsolute(folder) && path.normalize(folder) === folder, 'Use an absolute workflow folder');
  const info = await lstat(folder);
  require(info.isDirectory() && !info.isSymbolicLink(), 'Workflow folder must be a real directory');
  return realpath(folder);
}

async function planFor(args) {
  args.signal?.throwIfAborted();
  const fingerprint = await runtimeFingerprint();
  const definition = definitionFingerprint(args);
  const { checkpoint, nodes } = args;
  require(!nodes.some(node => node.dependsOn != null), 'Workflows with explicit dependencies cannot be continued yet; start a new workflow');
  require(checkpoint?.phase === 'interrupted', 'Only an interrupted workflow can be continued');
  require(checkpoint.runtimeFingerprint === fingerprint && checkpoint.runtimeFingerprintVersion === runtimeFingerprintVersion, 'The SDK runtime changed; start a new workflow');
  require(checkpoint.definitionFingerprint === definition, 'The workflow definition or execution settings changed; start a new workflow');
  require(Array.isArray(checkpoint.nodes) && checkpoint.nodes.length === nodes.length, 'The checkpoint does not identify every workflow step');
  const usage = checkpoint.usage;
  require(usage && ['input_tokens', 'cached_input_tokens', 'output_tokens'].every(key => Number.isSafeInteger(usage[key]) && usage[key] >= 0), 'The checkpoint has no reliable recorded usage');
  require(Number.isSafeInteger(checkpoint.attempts) && checkpoint.attempts >= 0, 'The checkpoint has no reliable recorded attempt count');
  const baseline = checkpoint.reuseThrough ?? 0;
  require(Number.isInteger(baseline) && baseline >= 0 && baseline <= nodes.length, 'Invalid reusable prefix boundary');
  const completed = [];
  let restart = null;
  for (let index = 0; index < nodes.length; index++) {
    const node = nodes[index], saved = checkpoint.nodes[index];
    require(saved?.id === node.id && saved.title === node.title, 'Checkpoint steps differ from the workflow definition');
    const step = { id: node.id, title: node.title, kind: node.kind ?? 'document', index };
    if (saved.phase === 'passed' || index < baseline) {
      require(restart === null, 'Only a contiguous completed prefix can be reused');
      const renewing = index < baseline && node.kind === 'approval' && ['pending', 'approval_pending'].includes(saved.phase);
      require(saved.phase === 'passed' || renewing, 'Only renewed approvals can interrupt a reusable prefix');
      if (!renewing) require(saved.document, `Completed step ${node.title} has no pinned document`);
      else require(saved.document === undefined && saved.decision === undefined, 'A renewed approval cannot reuse its old decision');
      if (node.kind === 'task') require(Array.isArray(saved.files) && saved.files.length > 0 && saved.files.length <= 1000, `Completed task ${node.title} has no pinned files`);
      else require(saved.files === undefined, `Unexpected task files in ${node.title}`);
      completed.push(step);
    } else {
      require(['pending', 'running', 'verifying', 'correction_pending', 'saving', 'failed', 'approval_pending', 'rejected'].includes(saved.phase), `Unknown checkpoint phase for ${node.title}`);
      if (restart === null) {
        restart = step;
        if (node.kind === 'task' && saved.phase !== 'pending' && (node.check !== undefined || node.access !== undefined || node.toolchain || node.sideEffect)) {
          throw new Error(`Reconcile possible external effects of ${node.title} before starting a new workflow; this task cannot be restarted automatically`);
        }
      } else require(saved.phase === 'pending', 'The checkpoint contains work beyond the interrupted step');
    }
  }
  args.signal?.throwIfAborted();
  const seed = { phase: 'interrupted', runtimeFingerprint: fingerprint, runtimeFingerprintVersion, definitionFingerprint: definition,
    reuseThrough: completed.length, usage: { ...usage }, attempts: checkpoint.attempts,
    nodes: nodes.map((node, index) => index < completed.length && node.kind !== 'approval' ? structuredClone(checkpoint.nodes[index])
      : { id: node.id, title: node.title, phase: 'pending', ...(node.kind === 'task' ? { verification: node.check === undefined && node.acceptance === undefined ? 'not_requested' : 'pending' } : {}) }) };
  return { checkpoint: seed, completed, reused: completed.filter(step => step.kind !== 'approval'), restart,
    renewApprovals: completed.filter(step => step.kind === 'approval'), usage: { ...usage }, attempts: checkpoint.attempts,
    runtimeFingerprint: fingerprint, definitionFingerprint: definition };
}

export async function inspectContinuation(args) {
  const plan = await planFor(args);
  const source = await directory(args.sourceWorkspace);
  await verifyFiles(source, plan.completed.map(step => args.checkpoint.nodes[step.index]), { signal: args.signal });
  return plan;
}

// Internal execution guard: approval documents are intentionally absent in the
// prepared folder. Every copied nonapproval output is reverified before any turn.
export async function preparedContinuation({ workspace, ...args }) {
  const plan = await planFor(args);
  workspace = await directory(workspace);
  const expected = new Set(plan.reused.flatMap(step => [step.id + '.md', ...(step.kind === 'task' ? [step.id] : [])]));
  require((await readdir(workspace)).every(name => expected.has(name)), 'Continuation folder contains uncommitted or unexpected files');
  await verifyFiles(workspace, plan.reused.map(step => args.checkpoint.nodes[step.index]), { signal: args.signal });
  return plan;
}

export async function prepareContinuation({ workspace, ...args }) {
  args = { ...args, sourceWorkspace: await directory(args.sourceWorkspace) };
  const plan = await inspectContinuation(args);
  workspace = await directory(workspace);
  for (const [a, b] of [[args.sourceWorkspace, workspace], [workspace, args.sourceWorkspace]]) {
    const relative = path.relative(a, b);
    require(relative === '..' || relative.startsWith('..' + path.sep) || path.isAbsolute(relative), 'Continuation needs a separate fresh workflow folder');
  }
  require((await readdir(workspace)).length === 0, 'Continuation needs an empty workflow folder');
  const created = [];
  const directories = new Set();
  async function parent(relative) {
    const parts = relative.split('/').slice(0, -1);
    for (let i = 1; i <= parts.length; i++) {
      const folder = path.join(workspace, ...parts.slice(0, i));
      if (!directories.has(folder)) { await mkdir(folder, { mode: 0o700 }); directories.add(folder); }
    }
  }
  try {
    for (const step of plan.reused) {
      const saved = args.checkpoint.nodes[step.index];
      for (const file of [saved.document, ...(saved.files ?? [])]) {
        args.signal?.throwIfAborted();
        require(typeof file.path === 'string' && (file.path === step.id + '.md' || file.path.startsWith(step.id + '/')) && file.path.split('/').every(part => part && part !== '.' && part !== '..' && !part.includes('\\')), 'Invalid continuation artifact path');
        await parent(file.path);
        const source = await open(path.join(args.sourceWorkspace, file.path), constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
        let target;
        try {
          const sourceStat = await source.stat();
          require(sourceStat.isFile() && sourceStat.nlink === 1 && sourceStat.size === file.bytes, `Source artifact changed: ${file.path}`);
          // Preserve observed owner execution permission for task artifacts only.
          // Permission bits are not part of the recorded content pins.
          const mode = 0o600 | (file.path === step.id + '.md' ? 0 : sourceStat.mode & 0o100);
          target = await open(path.join(workspace, file.path), 'wx', 0o600);
          created.push(file.path);
          const hash = createHash('sha256'), buffer = Buffer.alloc(256 * 1024);
          let offset = 0;
          while (offset < file.bytes) {
            args.signal?.throwIfAborted();
            const { bytesRead } = await source.read(buffer, 0, Math.min(buffer.length, file.bytes - offset), offset);
            require(bytesRead > 0, `Source artifact changed: ${file.path}`);
            hash.update(buffer.subarray(0, bytesRead));
            let written = 0;
            while (written < bytesRead) {
              args.signal?.throwIfAborted();
              const { bytesWritten } = await target.write(buffer, written, bytesRead - written, offset + written);
              require(bytesWritten > 0, 'Could not copy continuation artifact');
              written += bytesWritten;
            }
            offset += bytesRead;
          }
          require(hash.digest('hex') === file.sha256, `Source artifact changed: ${file.path}`);
          await target.chmod(mode);
        } finally { await target?.close(); await source.close(); }
      }
    }
    await verifyFiles(args.sourceWorkspace, plan.completed.map(step => args.checkpoint.nodes[step.index]), { signal: args.signal });
    await preparedContinuation({ ...args, workspace });
    return plan;
  } catch (error) {
    for (const file of created.reverse()) await rm(path.join(workspace, file), { force: true });
    for (const folder of [...directories].reverse()) await rm(folder, { recursive: true, force: true });
    throw error;
  }
}
