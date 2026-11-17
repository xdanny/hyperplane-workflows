import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export class PredictionInputError extends Error {
  constructor(message) { super(message); this.name = 'PredictionInputError'; }
}
export class ScorerInfrastructureError extends Error {
  constructor(message, options) { super(message, options); this.name = 'ScorerInfrastructureError'; }
}

const scorer = fileURLToPath(new URL('./ml_scoring.py', import.meta.url));

/** Recompute binary metrics from submitted predictions, not holdout custody or leakage.
 * declaration is {metric, operatingPoint?}, a table array/JSON, or carried row-one text.
 * Requires Python 3 with stdlib SQLite. Input: regular unlinked CSV, <=2 GB,
 * <=64 KiB per physical line/field, <=512 slices with <=512 UTF-8 bytes per name.
 * SQLite and its sorting stay on disk; each scratch file is capped at 8 GB.
 * There is no aggregate scratch quota; journals/sort runs can coexist.
 * A private snapshot prevents mixed reads.
 * Ranking ties group together for AUROC/AP. Top-k ties use CSV row order;
 * percentages use round-half-even. Slice decisions use the global selection.
 */
export async function scorePredictions({ predictions, declaration, signal } = {}) {
  signal?.throwIfAborted();
  if (typeof predictions !== 'string' || !path.isAbsolute(predictions)) throw new PredictionInputError('Predictions must be an absolute CSV path');
  const request = JSON.stringify({ predictions, declaration });
  if (Buffer.byteLength(request) > 65536) throw new PredictionInputError('Scoring declaration exceeds 64 KiB');
  let folder;
  try {
    folder = await mkdtemp(path.join(tmpdir(), `hyperplane-ml-score-${process.pid}-`));
    signal?.throwIfAborted();
    return await new Promise((resolve, reject) => {
      const child = spawn('python3', ['-I', scorer, folder], { stdio: ['pipe', 'pipe', 'pipe'], cwd: folder,
        env: { PATH: process.env.PATH, TMPDIR: folder, SQLITE_TMPDIR: folder, LANG: 'C.UTF-8' } });
      let output = '', error = '', failure;
      const abort = () => { failure = signal.reason ?? new Error('Scoring aborted'); child.kill('SIGKILL'); };
      signal?.addEventListener('abort', abort, { once: true });
      if (signal?.aborted) abort();
      child.stdout.on('data', chunk => {
        if (Buffer.byteLength(output) + chunk.length > 1_000_000) {
          failure = new Error('Scoring report exceeds 1 MB'); child.kill('SIGKILL');
        } else output += chunk.toString('utf8');
      });
      child.stderr.on('data', chunk => { error = (error + chunk.toString('utf8')).slice(0, 8192); });
      child.on('error', err => { failure = err; });
      child.stdin.on('error', err => { if (err.code !== 'EPIPE') failure ??= err; });
      child.on('close', code => {
        signal?.removeEventListener('abort', abort);
        if (failure) reject(failure);
        else if (code === 2 && error.startsWith('PREDICTION_INPUT_INVALID: ')) reject(new PredictionInputError(error.slice('PREDICTION_INPUT_INVALID: '.length).trim()));
        else if (code !== 0) reject(new ScorerInfrastructureError(error.trim() || `Scorer exited ${code}`));
        else {
          try {
            const report = JSON.parse(output);
            if (!report || report.scorer !== 'platform' || !['AUROC','PRAUC','ACCURACY','F1'].includes(report.family) || typeof report.metric !== 'string' || !Number.isFinite(report.value) || !Number.isSafeInteger(report.n) || report.n < 1 || !Number.isSafeInteger(report.positives) || report.positives < 0 || report.positives > report.n || !/^[a-f0-9]{64}$/.test(report.predictionsSha256) || !Array.isArray(report.slices)) throw new Error('Invalid report');
            resolve(report);
          } catch { reject(new ScorerInfrastructureError('Scorer returned an invalid report')); }
        }
      });
      child.stdin.end(request);
    });
  } catch (error) {
    signal?.throwIfAborted();
    if (error instanceof PredictionInputError || error instanceof ScorerInfrastructureError) throw error;
    throw new ScorerInfrastructureError(error.message, { cause: error });
  } finally {
    if (folder) {
      try { await rm(folder, { recursive: true, force: true }); }
      catch (error) { throw new ScorerInfrastructureError('Scorer scratch cleanup failed', { cause: error }); }
    }
  }
}
