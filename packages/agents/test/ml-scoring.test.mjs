import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm, realpath, symlink, link, truncate, readdir, appendFile, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { scorePredictions } from '@hyperplane/agents/ml-scoring';

async function fixture(t, csv) {
  const folder = await realpath(await mkdtemp(path.join(tmpdir(), 'ml-scoring-test-')));
  t.after(() => rm(folder, { recursive: true, force: true }));
  const predictions = path.join(folder, 'predictions.csv');
  await writeFile(predictions, csv);
  return { folder, predictions };
}
const near = (a, b) => assert.ok(Math.abs(a - b) < 1e-12, `${a} != ${b}`);

test('public SDK scorer recomputes hand-worked binary families and input identity', async t => {
  const csv = 'y_true,y_score\n0,0.1\n0,0.4\n1,0.35\n1,0.8\n';
  const {predictions} = await fixture(t, csv);
  for (const [metric, expected] of [['AUROC', .75], ['PR-AUC', 5/6], ['accuracy', .75], ['F1', 2/3]]) {
    const result = await scorePredictions({predictions, declaration: {metric, operatingPoint: 'threshold 0.5'}});
    near(result.value, expected);
    assert.equal(result.n, 4);
    assert.equal(result.positives, 2);
    assert.equal(result.scorer, 'platform');
    assert.equal(result.predictionsSha256, createHash('sha256').update(csv).digest('hex'));
    assert.match(result.scope, /does not establish holdout custody/);
  }
});

test('fixed positive class gives all-negative accuracy 1; undefined metrics are explicit', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score\n0,.1\n0,.2\n');
  assert.equal((await scorePredictions({predictions, declaration:{metric:'ACC',operatingPoint:'.5'}})).value, 1);
  for (const metric of ['AUROC','PRAUC','F1']) {
    await assert.rejects(scorePredictions({predictions, declaration:{metric,operatingPoint:'.5'}}), /requires|undefined/);
  }
  await writeFile(predictions, 'y_true,y_score\n1,.1\n1,.2\n');
  assert.equal((await scorePredictions({predictions,declaration:{metric:'average precision'}})).value, 1);
});

test('tied ranks group AP and AUROC; top decisions break ties by original row order', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score\n1,.5\n0,.5\n1,.5\n0,.5\n');
  for (const metric of ['AUC','AUPRC']) near((await scorePredictions({predictions,declaration:{metric}})).value,.5);
  assert.equal((await scorePredictions({predictions,declaration:{metric:'ACC',operatingPoint:'top 1'}})).value,.75);
  const half = await scorePredictions({predictions,declaration:{metric:'ACC',operatingPoint:'top 12.5%'}});
  assert.equal(half.decisionPolicy.selected,0); // 0.5 -> 0, explicit half-even
  const three = await scorePredictions({predictions,declaration:{metric:'ACC',operatingPoint:'top 37.5%'}});
  assert.equal(three.decisionPolicy.selected,2); // 1.5 -> 2
});

test('slice decisions retain global operating point, including quoted commas and newlines', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score,slice\n1,.9,"high,\npriority"\n0,.8,"high,\npriority"\n0,.2,low\n0,.1,low\n');
  const report = await scorePredictions({predictions,declaration:[{metric:'accuracy',operatingPoint:'top 50%'},{metric:'ignored'}]});
  assert.equal(report.value,.75);
  assert.deepEqual(report.slices.map(s=>[s.slice,s.n,s.value]), [['high,\npriority',2,.5],['low',2,1]]);
  const ranked = await scorePredictions({predictions,declaration:JSON.stringify([{metric:'roc_auc'}])});
  assert.equal(ranked.slices[1].value,null);
  assert.match(ranked.slices[1].reason,/both binary classes/);
  assert.equal(ranked.slices.length,2);
});

test('strict threshold remains strict and carried declaration row one is authoritative', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score\n0,.5\n1,.6\n');
  assert.equal((await scorePredictions({predictions,declaration:'1. metric: accuracy | threshold: > 0.9 | operatingPoint: > 0.5\n2. metric: F1'})).value,1);
  assert.equal((await scorePredictions({predictions,declaration:{metric:'accuracy',operatingPoint:'>= 0.5'}})).value,.5);
});

test('rejects malformed CSV, nonfinite values, other labels and resource overruns', async t => {
  const {predictions} = await fixture(t, '');
  for (const [csv, reason] of [
    ['y_true,y_score\n0,NaN\n',/finite/], ['y_true,y_score\n1,Infinity\n',/finite/],
    ['y_true,y_score\nNaN,.1\n',/finite 0 or 1/], ['y_true,y_score\n2,.1\n',/finite 0 or 1/],
    ['y_true,y_score\n-1,.1\n',/finite 0 or 1/], ['y_true,y_score\n0.5,.1\n',/finite 0 or 1/],
    ['y_true,y_score\n1,1e400\n',/finite/], ['y_true,y_score\n0\n',/columns/],
    ['y_true,y_score,other\n0,.1,x\n',/header/], ['y_true,y_score\n',/at least one/],
    ['y_true,y_score\n0,".1\n',/unexpected end/],
    ['y_true,y_score\n0,'+'1'.repeat(65537)+'\n',/64 KiB/],
    ['y_true,y_score,slice\n0,.1,'+'s'.repeat(513)+'\n',/512 UTF-8/],
    ['y_true,y_score,slice\n'+Array.from({length:513},(_,i)=>`0,.1,s${i}`).join('\n'),/512 slices/],
  ]) {
    await writeFile(predictions,csv);
    await assert.rejects(scorePredictions({predictions,declaration:{metric:'accuracy',operatingPoint:'.5'}}),reason);
  }
});

test('rejects ambiguous or impossible operating points and unknown metrics', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score\n0,.1\n1,.9\n');
  for (const operatingPoint of [undefined, 'approximately threshold .5', 'top 101%', 'top -1%', 'top 3', 'threshold NaN', 'top 1 or 2']) {
    await assert.rejects(scorePredictions({predictions,declaration:{metric:'accuracy',operatingPoint}}), /operating point|Operating point|percentage|Top count/);
  }
  await assert.rejects(scorePredictions({predictions,declaration:{metric:'RMSE'}}),/Unsupported metric/);
});

test('rejects symlink/hardlink/ancestor aliases and files above 2 GB', async t => {
  const {folder,predictions} = await fixture(t, 'y_true,y_score\n0,.1\n');
  const run = p => scorePredictions({predictions:p,declaration:{metric:'accuracy',operatingPoint:'.5'}});
  const alias = path.join(folder,'alias.csv');
  await symlink(predictions,alias);
  await assert.rejects(run(alias));
  const directoryAlias = path.join(folder,'alias');
  await symlink(folder,directoryAlias);
  await assert.rejects(run(path.join(directoryAlias,'predictions.csv')));
  await link(predictions,path.join(folder,'hard.csv'));
  await assert.rejects(run(predictions),/unlinked/);
  await rm(path.join(folder,'hard.csv'));
  await truncate(predictions,2_000_000_001);
  await assert.rejects(run(predictions),/2 GB/);
});

test('abort kills private scorer and removes scratch files', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score\n'+ '0,.1\n'.repeat(500_000));
  const before = new Set((await readdir(tmpdir())).filter(n=>n.startsWith(`hyperplane-ml-score-${process.pid}-`)));
  const controller = new AbortController();
  const pending = scorePredictions({predictions,declaration:{metric:'accuracy',operatingPoint:'.5'},signal:controller.signal});
  const timeout = setTimeout(()=>controller.abort(new Error('stop scorer')),80);
  try { await assert.rejects(pending,/stop scorer/); } finally { clearTimeout(timeout); }
  const after = (await readdir(tmpdir())).filter(n=>n.startsWith(`hyperplane-ml-score-${process.pid}-`)&&!before.has(n));
  assert.deepEqual(after,[]);
  controller.abort();
  await assert.rejects(scorePredictions({predictions,declaration:{metric:'accuracy'},signal:controller.signal}),/stop scorer/);
});


test('rejects source changes while disk-backed scoring is running', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score\n'+ '0,.1\n1,.9\n'.repeat(250_000));
  const before = new Set((await readdir(tmpdir())).filter(n=>n.startsWith(`hyperplane-ml-score-${process.pid}-`)));
  const pending = scorePredictions({predictions,declaration:{metric:'AUROC'}});
  // Attach rejection handling immediately while observing the private database creation.
  const outcome = pending.then(result=>({result}),error=>({error}));
  let observed = false;
  for (let i=0;i<400;i++) {
    const current = (await readdir(tmpdir())).filter(n=>n.startsWith(`hyperplane-ml-score-${process.pid}-`)&&!before.has(n));
    for (const folder of current) {
      if (await stat(path.join(tmpdir(),folder,'scores.sqlite')).catch(()=>null)) observed=true;
    }
    if (observed) break;
    await new Promise(resolve=>setTimeout(resolve,5));
  }
  assert.equal(observed,true,'observe the active SQLite scorer before modifying input');
  await appendFile(predictions,'0,.2\n');
  const {error} = await outcome;
  assert.match(error?.message ?? '',/Predictions changed during scoring/);
});


test('ranking metrics retain descriptive operating metadata without treating it as a cutoff', async t => {
  const {predictions} = await fixture(t, 'y_true,y_score\n0,.1\n1,.9\n');
  for (const metric of ['AUROC', 'PR-AUC']) {
    const report = await scorePredictions({predictions,declaration:{metric,operatingPoint:'the review list is read top-down'}});
    assert.equal(report.value,1);
    assert.equal(report.operatingPoint,'the review list is read top-down');
    assert.equal(report.decisionPolicy,null);
  }
  await assert.rejects(scorePredictions({predictions,declaration:{metric:'RMSE'}}),/RMSE/);
});
