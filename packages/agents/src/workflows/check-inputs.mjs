import { withinTolerance } from './decimal.mjs';
const require=(ok,message)=>{if(!ok)throw new Error(message);};
const text=v=>typeof v==='string'&&!!v.trim();
const unique=(rows,key,label)=>{require(new Set(rows.map(r=>r[key])).size===rows.length,`${label} must be distinct`);};
const distinct=unique;

const shiftUnits={psi:['psi','absolute'],kl:['kl','absolute'],'rate-delta':['points_of_share','absolute'],'category-emergence':['points_of_share','absolute'],'volume-drift':['percent_of_baseline','count','absolute']};
export function tableRows(raw) {
  if(!raw?.trim())return [];
  if(raw.trim().startsWith('[')) {const rows=JSON.parse(raw);require(Array.isArray(rows),'Use table rows');return rows;}
  return raw.split('\n').filter(s=>s.trim()).map(line=>Object.fromEntries(line.replace(/^\s*\d+\.\s*/,'').split('|').map(field=>{const i=field.indexOf(':');require(i>0,'Use a feature table with named fields');return [field.slice(0,i).trim(),field.slice(i+1).trim()];})));
}
export function shiftInputs(inputs) {
  const number=(value,label)=>{require((typeof value==='string'&&value.trim()!==''||typeof value==='number')&&Number.isFinite(Number(value))&&Number(value)>=0,`${label} needs a finite nonnegative number`);return Number(value);};
  const features=tableRows(inputs.features);
  require(features.length>0&&features.length<=32,'Declare between 1 and 32 features with their own tolerances');
  const names=new Set();
  for(const f of features){
    require(f&&typeof f==='object'&&typeof f.feature==='string'&&f.feature.trim()&&!names.has(f.feature),'Features need distinct column names');names.add(f.feature);
    require(['numeric','categorical','timestamp'].includes(f.type)&&typeof f.whyItMatters==='string'&&f.whyItMatters.trim(),`Feature ${f.feature} needs type and downstream consequence`);
    f.tolerance=number(f.tolerance,`Feature ${f.feature} tolerance`);
    f.statistic ||= f.type==='categorical'?'category-emergence':'psi';
    require(['psi','kl','rate-delta','category-emergence','volume-drift'].includes(f.statistic),'Choose an executable shift statistic');
    require(shiftUnits[f.statistic].includes(f.unit),`Feature ${f.feature} needs a compatible unit for ${f.statistic}: ${shiftUnits[f.statistic].join(', ')}`);
    if(f.severity)require(['blocker','major','minor'].includes(f.severity),'Choose blocker, major or minor severity');
    if(f.watchBand!==undefined&&f.watchBand!=='')require(number(f.watchBand,'Watch band')<f.tolerance,'Watch band must be below tolerance');
  }
  const controls=tableRows(inputs.seededControl),seen=new Set();
  require(controls.length<=32,'Use at most 32 controls');
  for(const c of controls){require(c&&names.has(c.feature)&&!seen.has(c.feature),'Each control must name a distinct declared feature');seen.add(c.feature);c.multiple=number(c.multiple,'Control multiple');require(c.multiple>0,'Control multiple must be positive');}
  const selected=controls.length?controls.map(c=>features.find(f=>f.feature===c.feature)):features.filter(f=>f.type==='categorical');
  if(!selected.length)selected.push(features[0]);
  require(selected.every(f=>f.tolerance>0),'Multiplicative controls need positive tolerances; choose controls for positive-tolerance features explicitly');
  return {features,controls};
}

export function qualityKeyColumns(value) {
  const columns = value.split(/[,;]/).map(s => s.trim()).filter(Boolean);
  if (columns[0]?.toLowerCase() === 'none') columns.shift();
  require(columns.length > 0, 'Name key columns, or none followed by candidate columns');
  return columns;
}


const reconciliationUnits=['percent_of_a','percent_of_b','absolute','count','exact'];

export function reconciliationInputs(inputs){
 const measures=tableRows(inputs.measures),tolerances=tableRows(inputs.tolerances),differences=tableRows(inputs.knownDifferences);
 require(measures.length>0&&measures.length<=32,'Declare 1–32 measures');unique(measures,'measure','Measures');unique(tolerances,'measure','Tolerances');unique(differences,'name','Known differences');
 require(tolerances.length===measures.length,'Declare exactly one tolerance per measure');
 for(const m of measures){require(text(m.measure)&&text(m.definitionA)&&text(m.definitionB),'Each measure needs both definitions');const t=tolerances.find(t=>t.measure===m.measure);require(t&&reconciliationUnits.includes(t.unit),'Each measure needs a tolerance with its own unit');if(t.unit==='exact'&&(t.value===undefined||t.value===''))t.value='0';withinTolerance('0','0','0',t);
  if(t.segmentValue!==undefined&&t.segmentValue!==''){require(reconciliationUnits.includes(t.segmentUnit),'A per-segment tolerance needs its own unit');withinTolerance('0','0','0',{value:t.segmentValue,unit:t.segmentUnit});}
  else require(!t.segmentUnit,'A segment unit requires a segment value');
 }
 require(differences.length<=64,'Declare at most 64 named differences');
 for(const d of differences)require(text(d.name)&&['A','B','both'].includes(d.side),'Known differences need a name and side');
 return {measures,tolerances,differences};
}

export function pipelineChangeInputs(inputs){
 const invariants=tableRows(inputs.invariants);require(invariants.length>0&&invariants.length<=32,'Declare 1–32 protected invariants');unique(invariants,'invariant','Invariants');
 for(const r of invariants){
  require(text(r.invariant)&&text(r.measure)&&['number','boolean','text'].includes(r.kind),'Each invariant needs a name, measure and value kind');
  require(['absolute','count','exact','percent_of_baseline'].includes(r.unit),'Each invariant needs its own tolerance unit');
  if(r.kind!=='number')require(r.unit==='exact','Text and boolean invariants require exact equality');
  if(r.unit==='exact'&&(r.tolerance===undefined||r.tolerance===''))r.tolerance='0';
  require(text(r.tolerance),'Declare a numeric tolerance, or zero for exact equality');
  withinTolerance('0','0','0',{value:r.tolerance||'0',unit:r.unit==='percent_of_baseline'?'percent_of_a':r.unit});
 }
 return invariants;
}

export function pipelineMeasures(inputs){
 const rows=tableRows(inputs.measures);require(rows.length>0&&rows.length<=32,'Declare 1–32 measures and their business definitions');distinct(rows,'measure','Measures');
 for(const r of rows){
  require(text(r.measure)&&text(r.definition),'Each measure needs a name and business definition');
  r.unit ||= 'exact';if(r.unit==='exact'&&(r.tolerance===undefined||r.tolerance===''))r.tolerance='0';
  require(['exact','absolute','count','percent_of_control'].includes(r.unit)&&text(r.tolerance),'Each measure needs its own allowed difference and unit');
  withinTolerance('0','0','0',{value:r.tolerance,unit:r.unit==='percent_of_control'?'percent_of_a':r.unit});
 }
 return rows;
}

const families = Object.fromEntries([
  ['AUROC',['AUROC','ROCAUC','AUCROC','AUC']], ['PRAUC',['PRAUC','AUCPR','AUPRC','AVERAGEPRECISION']],
  ['ACCURACY',['ACCURACY','ACC']], ['F1',['F1','F1SCORE']],
].flatMap(([family,names])=>names.map(name=>[name,family])));
export const family = name => families[String(name).toUpperCase().replace(/[^A-Z0-9]/g,'')];
const numberPattern = '[+-]?(?:\\d+(?:\\.\\d*)?|\\.\\d+)(?:[eE][+-]?\\d+)?';

// Decimal comparisons preserve threshold magnitude/direction without epsilon,
// including values supplied as decimal strings beyond binary float precision.
export function decimal(value, label) {
  require(typeof value==='number'&&Number.isFinite(value)||typeof value==='string', `${label} must be finite numeric data`);
  const s=String(value).trim();
  const m=s.match(/^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/);
  require(m && (m[2]||m[3]) && s.length<=128 && Number.isFinite(Number(s)), `${label} must be a finite decimal`);
  const exponent=Number(m[4]||0)-(m[3]||'').length;
  require(Math.abs(exponent)<=1000, `${label} exponent is too large`);
  return {coefficient:BigInt((m[1]==='-'?'-':'')+(m[2]||'0')+(m[3]||'')),exponent};
}

export function declaredMetric(value) {
  require(text(value),'Declare successMetric');
  const raw=value.trim();let cells;
  if(raw.startsWith('[')||raw.startsWith('{')) {
    const parsed=JSON.parse(raw),row=Array.isArray(parsed)?parsed[0]:parsed;
    require(row&&typeof row==='object'&&!Array.isArray(row),'Declare the primary metric in row one');
    cells=Object.fromEntries(Object.entries(row).map(([k,v])=>[k.toLowerCase(),v]));
  } else {
    const first=raw.split('\n').find(line=>line.trim()).replace(/^\s*\d+\.\s*/,'').trim();
    cells={};
    for(const part of first.split(' | ')) {
      const colon=part.indexOf(':');if(colon>=0)cells[part.slice(0,colon).trim().toLowerCase()]=part.slice(colon+1).trim();
    }
    if(!cells.metric) {
      const match=first.match(/^([^<>=]+?)\s*((?:>=|<=|>|<|=).*)?$/);
      cells.metric=match?.[1].trim();cells.threshold??=match?.[2];
    }
  }
  require(text(cells.metric)&&family(cells.metric),'The primary metric must be AUROC, PR-AUC (average precision), accuracy or F1');
  require(typeof cells.threshold==='string'||typeof cells.threshold==='number','Row one must declare a threshold');
  const threshold=String(cells.threshold).trim();
  const match=threshold.match(new RegExp(`^(>=|<=|>|<|=)\\s*(${numberPattern})(?:\\s+[^<>=]*)?$`));
  require(match,'Declare one threshold and comparison in row one');
  const result={metric:cells.metric.trim(),family:family(cells.metric),comparator:match[1],threshold:match[2],operatingPoint:cells.operatingpoint||undefined};
  decimal(result.threshold,'Declared threshold');
  if(['ACCURACY','F1'].includes(result.family)) require(text(result.operatingPoint),'Accuracy and F1 require a declared operating point');
  return result;
}

export function checkPredictionInputs(inputs) {
  require(inputs&&text(inputs.dataDescription),'Describe the evaluated data and any declared holdout hash');
  declaredMetric(inputs.successMetric);
}


// Declaration checks only. Source accessibility and headers remain execution preflight.
const inputValidators = new Map([
  ['data-quality-profile', inputs => qualityKeyColumns(inputs.keyColumns)],
  ['dataset-shift-profile', inputs => { qualityKeyColumns(inputs.keyColumns); shiftInputs(inputs); }],
  ['reconciliation-profile', inputs => { qualityKeyColumns(inputs.keyColumns); reconciliationInputs(inputs); }],
  ['pipeline-change-profile', inputs => { qualityKeyColumns(inputs.keyColumns); pipelineChangeInputs(inputs); }],
  ['data-pipeline-profile', inputs => { qualityKeyColumns(inputs.keyColumns); pipelineMeasures(inputs); }],
  ['prediction-metrics', checkPredictionInputs],
]);
export function validateCheckInputs(id, inputs) {
  const validate = inputValidators.get(id);
  if (!validate) throw new Error(`No declaration validator for check: ${id}`);
  validate(inputs);
}
