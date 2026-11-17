import csv,json,os,pathlib,copy
from decimal import Decimal
root=pathlib.Path.cwd(); stage=root.name; mode=os.environ.get('SDK_FIXTURE_MODE','pass')
D=Decimal
money=lambda v:format(v,'f')
def source(side):
 p=pathlib.Path(os.environ['SDK_FIXTURE_'+side]);rows=list(csv.DictReader(p.open()))
 for r in rows:r['amount']=D(r['amount'])
 return p,rows
pa,a=source('BASELINE');pb,b=source('CURRENT')
def total(rows):return sum((r['amount'] for r in rows),D(0))
def metadata(p,rows):return dict(identity=str(p),readPath=str(p),verified=True,rowsStored=len(rows),rowsAtGrain=len(rows),periodObserved='Supplied synthetic snapshot; no date column',scanned='full',grainUnique=True,duplicateKeys=0,duplicateKeyAmount='0')
if stage=='profile':
 profile=dict(sourceA=metadata(pa,a),sourceB=metadata(pb,b),independentReadPaths=True,readPathNote='Separate CSV reads',grain={'declared':'one row per id','keyColumns':['id']},measures=[dict(name='revenue',definitionA='Sum amount',definitionB='Sum amount',totalA=money(total(a)),totalB=money(total(b)),observedDelta=money(total(a)-total(b)),rowsA=len(a),rowsB=len(b),droppedA=0,droppedB=0,precision='Exact decimal strings',commensurable=True,note='Same unit')],keyCoverage=dict(onlyInA={'keys':len({r['id'] for r in a}-{r['id'] for r in b})},onlyInB={'keys':len({r['id'] for r in b}-{r['id'] for r in a})},inBoth={'keys':len({r['id'] for r in a}&{r['id'] for r in b})}),segments=[],observedFilters=[],comparabilityHazards=[],openQuestions=[])
 (root/'source-profiles.json').write_text(json.dumps(profile));print('Profiled both sources')
elif stage=='rules':
 p=json.loads((root.parent/'profile/source-profiles.json').read_text());m=p['measures'][0]
 term=dict(id='filter:test',name='Test accounts',predicate="is_test = '1'",side='A',direction='+',source='declared',why='Test accounts excluded by B',expectedAmount=money(total([r for r in a if r['is_test']=='1'])))
 equation=dict(id='revenue',measure='revenue',statement='A = B + test accounts + unexplained',totalA=m['totalA'],totalB=m['totalB'],observedDelta=m['observedDelta'],terms=[term],tolerance={'value':'10','unit':'absolute','segmentValue':'2','segmentUnit':'absolute','source':'Owner declaration'},toleranceOrigin='declared',severity='major',why='Revenue decisions',expressible=True,expectedToBalance=True,profileEvidence='Measured exact CSV totals')
 if mode=='wrong-tolerance':equation['tolerance']['value']='11'
 (root/'recon-rules.json').write_text(json.dumps({'equations':[equation],'segmentPlan':{'dimension':'region','mustSumToTotal':True},'preconditions':[{'id':'unique-a'},{'id':'unique-b'}],'unexpressible':[]}));print('Proposed exact owner rules')
elif stage=='audit':
 p=json.loads((root.parent/'profile/source-profiles.json').read_text());rule=json.loads((root.parent/'rules/recon-rules.json').read_text())['equations'][0]
 def equation(left,right):
  ta,tb=total(left),total(right);selected=[r for r in left if r['is_test']=='1'];explained=total(selected);delta=ta-tb;unexplained=delta-explained
  term=copy.deepcopy(rule['terms'][0]);term.update(amount=money(explained),rowCount=len(selected))
  return dict(id=rule['id'],measure=rule['measure'],severity=rule['severity'],query='Sum each CSV independently and select A test rows',checkedUnit='rows',checkedA=len(left),checkedB=len(right),excludedA={'rows':0,'amount':'0'},excludedB={'rows':0,'amount':'0'},totalA=money(ta),totalB=money(tb),observedDelta=money(delta),terms=[term],explained=money(explained),unexplained=money(unexplained),balanceResidual='0',balances=True,tolerance=rule['tolerance']['value'],toleranceUnit='absolute',withinTolerance=abs(unexplained)<=D(rule['tolerance']['value']))
 result=equation(a,b);segments=[]
 for segment in sorted({r['region'] for r in a+b}):
  r=equation([r for r in a if r['region']==segment],[r for r in b if r['region']==segment]);segments.append(dict(segment=segment,measure='revenue',checkedA=r['checkedA'],checkedB=r['checkedB'],totalA=r['totalA'],totalB=r['totalB'],delta=r['observedDelta'],explained=r['explained'],unexplained=r['unexplained'],tolerance='2',toleranceUnit='absolute',withinTolerance=abs(D(r['unexplained']))<=2))
 result['unexplainedBySegment']=[dict(segment=r['segment'],amount=r['unexplained'],rows=r['checkedA']+r['checkedB']) for r in segments if D(r['unexplained'])!=0]
 controls=[]
 for operation in ['drop-keys','alter-segment']:
  changed=copy.deepcopy(a);candidate=next(r for r in changed if r['id']=='3');segment=candidate['region']
  if operation=='drop-keys':delta=-candidate['amount'];changed.remove(candidate)
  else:delta=D('7');candidate['amount']+=delta
  observed=equation(changed,b);seg=equation([r for r in changed if r['region']==segment],[r for r in b if r['region']==segment])
  controls.append(dict(id=operation,operation=operation,measure='revenue',side='A',source='model-fixture',segment=segment,seededHow='Copy A and perturb id 3, outside named test filter',query='Separate copy; same equation function',rowsBefore=len(a),rowsAfter=len(changed),seededDelta=money(delta),detectedDelta=money(D(observed['unexplained'])-D(result['unexplained'])),detected=True,segmentUnexplainedAfter=seg['unexplained'],result=observed))
 sources={}
 for name in ['A','B']:
  info=copy.deepcopy(p['source'+name]);info['rows']=info.pop('rowsStored');sources[name.lower()]=info
 report=dict(sources=sources,grain=p['grain']['declared'],measures=[result],notRun=[],negativeControls=controls)
 if mode=='bad-counts':result['checkedA']-=1
 if mode=='missing-rule':report['measures']=[]
 if mode=='bad-arithmetic':result['unexplained']='0'
 if mode=='rounded':result['totalA']=money(D(result['totalA'])+D('0.000000000000000000000001'))
 if mode=='wrong-segment-unit':segments[0]['toleranceUnit']='percent_of_a'
 if mode=='cancelled-segments-hidden':result['unexplainedBySegment']=[]
 if mode=='preexisting-gap-control':controls[0]['detectedDelta']=controls[0]['result']['unexplained']
 if mode=='control-absorbed':controls[0]['result']['terms'][0]['amount']=money(D(controls[0]['result']['terms'][0]['amount'])+D(controls[0]['seededDelta']))
 if mode=='control-format':controls[0]['seededHow']={'description':'structured instead of text'}
 if mode=='missing-control':report['negativeControls']=controls[:1]
 (root/'reconciliation-report.json').write_text(json.dumps(report));(root/'delta-table.json').write_text(json.dumps(dict(dimension='region',rows=segments,segmentsSumToTotal=True,segmentSumResidual='0')));print('Executed equation and both controls on copies')
