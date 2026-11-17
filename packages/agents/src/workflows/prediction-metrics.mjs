import { family, decimal, declaredMetric, checkPredictionInputs } from './check-inputs.mjs';
export { checkPredictionInputs } from './check-inputs.mjs';
import { lstat, realpath, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { getCheck } from './check-catalog.mjs';
import { checkedProgram } from './checked-program.mjs';
import { hashFile, readJSON } from './files.mjs';
import { scorePredictions, ScorerInfrastructureError } from './ml-scoring.mjs';

export const predictionChecks = ['prediction-metrics'];
const require = (ok, message) => { if (!ok) throw new Error(message); };
const text = value => typeof value === 'string' && Boolean(value.trim());
function compare(a,b) {
  const exponent=Math.min(a.exponent,b.exponent);
  const left=a.coefficient*10n**BigInt(a.exponent-exponent),right=b.coefficient*10n**BigInt(b.exponent-exponent);
  return left<right?-1:left>right?1:0;
}
function agrees(a,b) {
  const exponent=Math.min(a.exponent,b.exponent,-6);
  const delta=a.coefficient*10n**BigInt(a.exponent-exponent)-b.coefficient*10n**BigInt(b.exponent-exponent);
  return (delta<0n?-delta:delta)<=10n**BigInt(-6-exponent);
}
function holdoutHashes(description) {
  const segments=description.split(/[;.]\s+|\n/),held=segments.filter(s=>/\b(?:hold|held)[- ]?out\b/i.test(s));
  return [...new Set((held.length?held:segments).flatMap(s=>[...s.matchAll(/\b([0-9a-f]{64})\b/ig)].map(m=>m[1].toLowerCase())))];
}
function reportFingerprint(report) {
  // Prefer the explicit evaluation set. A report may also describe its full
  // source dataset with a different hash; that is not the holdout fingerprint.
  const values=[];
  for(const name of ['evalset','holdout','dataset','split']) {
    const block=report[name];
    if(block&&typeof block==='object') for(const key of ['sha256','contentHash','fingerprint']) if(block[key]!==undefined) values.push(block[key]);
    if(values.length) break;
  }
  if(report.evalsetSha256!==undefined)values.push(report.evalsetSha256);
  require(values.length,'The harness did not record which holdout it scored: add evalset.sha256');
  require(values.every(v=>typeof v==='string'&&/^[a-f0-9]{64}$/i.test(v)),'Reported holdout fingerprints must be SHA256 values');
  require(new Set(values.map(v=>v.toLowerCase())).size===1,'Conflicting reported holdout fingerprints');
  return values[0].toLowerCase();
}
export function predictionAcceptance(kind, workspace, _previous, inputs, {signal}={}) {
  require(kind==='prediction-metrics','Unknown prediction check');
  checkPredictionInputs(inputs);
  const declared=declaredMetric(inputs.successMetric),program=checkedProgram(workspace,'checks.sh',{signal});
  let accepted;
  return {
    description:program.description+'\n'+getCheck(kind).description,
    onEvent:program.onEvent,
    async verify() {
      accepted=undefined;
      try {
        signal?.throwIfAborted();
        await program.verify();
        require(!(await lstat(path.join(workspace,'score-report.json')).catch(error=>{if(error.code==='ENOENT')return null;throw error;})), 'score-report.json is reserved for the host; remove the model-written file');
        const report=await readJSON(workspace,'eval-report.json',{signal}),pm=report?.primaryMetric;
        require(pm&&text(pm.name)&&family(pm.name)===declared.family,`Declared ${declared.metric} on row one; reported ${pm?.name}: not the same ruler`);
        require(pm.comparator===declared.comparator,'Reported comparator differs from the declared direction');
        require(compare(decimal(pm.threshold,'Reported threshold'),decimal(declared.threshold,'Declared threshold'))===0,'The harness set its own bar: reported threshold differs from row one');
        decimal(pm.value,'Reported metric value');
        require(pm.pass===true,'The primary metric must report its actual passing result');
        const scored=await scorePredictions({predictions:path.join(await realpath(workspace),'predictions.csv'),declaration:{metric:declared.metric,operatingPoint:declared.operatingPoint},signal});
        require(scored.family===declared.family,'The scorer and report use different rulers');
        require(agrees(decimal(scored.value,'Scored metric value'),decimal(pm.value,'Reported metric value')),'The harness number is not evidence: it differs from the independently recomputed value');
        const comparison=compare(decimal(scored.value,'Scored metric value'),decimal(declared.threshold,'Declared threshold'));
        require(({'>=':comparison>=0,'>':comparison>0,'<=':comparison<=0,'<':comparison<0,'=':comparison===0})[declared.comparator],`Recomputed ${declared.metric} ${scored.value} does not clear ${declared.comparator} ${declared.threshold}`);
        require(await hashFile(workspace,'predictions.csv',{signal})===scored.predictionsSha256,'Predictions changed after scoring');
        if(report.predictionsSha256!==undefined) require(report.predictionsSha256===scored.predictionsSha256,'Reported prediction identity differs from the scored file');
        const got=reportFingerprint(report),declaredHashes=holdoutHashes(inputs.dataDescription);
        require(!declaredHashes.length||declaredHashes.includes(got),'Evaluated on a different holdout than the one declared');
        if(report.evalset?.rows!==undefined) require(report.evalset.rows===scored.n,'Reported evaluation population differs from prediction row count');
        const leakage=report.leakageChecks;
        require(Array.isArray(leakage)&&leakage.length>0&&leakage.every(c=>c&&text(c.id)&&c.pass===true),'Every reported leakage check must have an identity and pass');
        require(new Set(leakage.map(c=>c.id)).size===leakage.length,'Leakage check identities must be distinct');
        const controls=leakage.filter(c=>c.expectedTrip!==undefined);
        require(controls.some(c=>c.expectedTrip===true),'Record a seeded leakage control with expectedTrip:true and tripped:true');
        require(controls.every(c=>typeof c.expectedTrip==='boolean'&&typeof c.tripped==='boolean'&&c.expectedTrip===c.tripped&&text(c.evidence)),'Every leakage control needs matching expectedTrip/tripped booleans and evidence');
        await program.verify();
        accepted={...scored,binding:{declaredMetric:declared.metric,comparator:declared.comparator,threshold:declared.threshold,reportedValue:pm.value,
          reportedEvalsetSha256:got,declaredHoldoutHashes:declaredHashes,declaredHoldoutMatched:declaredHashes.length?true:null,leakageControlIDs:controls.map(c=>c.id)},
          limitations:{holdoutCustody:'not_verified',modelProvenance:'not_verified',leakageControlExecution:'reported_by_harness',
            explanation:'Recomputed submitted labels and scores; matching reported holdout hashes and reported control outcomes do not establish label custody, source isolation, actual split correctness or use of a frozen model.'}};
        return {passed:true,findings:[]};
      } catch(error) {
        signal?.throwIfAborted();
        if (error instanceof ScorerInfrastructureError || (error.code && !['ENOENT','ELOOP','ENOTDIR','EACCES'].includes(error.code))) throw error;
        return {passed:false,findings:[error.message]};
      }
    },
    async seal() {
      require(accepted,'Prediction acceptance must pass before publishing its host report');
      signal?.throwIfAborted();
      await program.verify();
      await writeFile(path.join(workspace,'score-report.json'),JSON.stringify(accepted,null,2)+'\n',{flag:'wx',mode:0o600});
    },
  };
}
