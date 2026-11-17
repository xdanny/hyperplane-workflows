import csv,json,os,pathlib,shutil,subprocess,copy,hashlib
from decimal import Decimal as D
root=pathlib.Path.cwd();stage=root.name;mode=os.environ.get('SDK_FIXTURE_MODE','pass')
source=pathlib.Path(os.environ['SDK_FIXTURE_BASELINE']);original=source.parent/'pipeline.py'
location=str(original)+'; input '+str(source)
owner=[dict(invariant='Row count',measure='rows',kind='number',unit='exact',tolerance='0'),dict(invariant='US revenue',measure='us_net',kind='number',unit='absolute',tolerance='0.01')]
if mode.startswith('coupled'):
 owner.append(dict(invariant='Twice US revenue',measure='2 * us_net',kind='number',unit='absolute',tolerance='0.02'))
def read(p):return list(csv.DictReader(p.open()))
def measurements(rows):
 total=sum((D(r['net']) for r in rows if r['region']=='US'),D(0))
 return {'Row count':str(len(rows)),'US revenue':str(total),'Twice US revenue':str(2*total)}
def check(rows,b):
 values=measurements(rows);out=[]
 for d in owner:
  expected=next(r['baseline'] for r in b['invariants'] if r['invariant']==d['invariant']);v=values[d['invariant']];delta=D(v)-D(expected)
  out.append(dict(d,expected=expected,observed=v,delta=str(delta),pass_=abs(delta)<=D(d['tolerance']),query='Read actual result.csv: count rows or sum US net',derivationPath='csv.DictReader of executed pipeline output'))
  out[-1]['pass']=out[-1].pop('pass_')
 return out
independent_value=str(sum((D(line.split(',')[2])-D(line.split(',')[3]) for line in source.read_text().splitlines()[1:] if line.split(',')[1]=='US'),D(0)))
independent=dict(invariant='US revenue',value=independent_value,path='Separate raw-text parser of original source, without pipeline import',query='Sum gross minus refund for US source lines')
if stage=='profile':
 work=root/'baseline';work.mkdir(exist_ok=True);shutil.copyfile(original,work/'pipeline.py');shutil.copyfile(source,work/'orders.csv');subprocess.run(['python3','pipeline.py'],cwd=work,check=True)
 rows=read(work/'result.csv');values=measurements(rows)
 b=dict(pipeline=dict(location=location,command='python3 pipeline.py',executed=True,models=[dict(name='orders',rowCount=len(rows),file='baseline/result.csv')]),snapshot=dict(identity=hashlib.sha256(source.read_bytes()).hexdigest(),capturedAt='2026-09-06 fixture capture'),invariants=[dict(d,baseline=values[d['invariant']],measurable=True,query='Query actual baseline result.csv',derivationPath='csv.DictReader of executed pipeline output') for d in owner],grainCheck=dict(grain='one row per order id',keyColumns=['id'],duplicateRows=0,nullKeyRows=0),independentControl=independent,consumers=['Revenue dashboard'],issues=[])
 if mode=='premature-implementation':(root/'changed').mkdir(exist_ok=True)
 if mode=='baseline-not-executed':b['pipeline']['executed']=False
 if mode=='missing-baseline-output':(work/'result.csv').unlink()
 if mode=='unmeasurable':b['invariants'][0]['measurable']=False
 if mode=='independent-disagrees':b['independentControl']['value']='999'
 (root/'baseline-report.json').write_text(json.dumps(b));print('Executed baseline original pipeline on copied original data')
elif stage=='rules':
 b=json.loads((root.parent/'profile/baseline-report.json').read_text())
 p=dict(impact=[dict(object='orders.net',kind='column',change='Convert EU refunds with fx',classification='compatible',why='US rates equal one')],migrationSteps=[dict(step='Convert refunds',command='Use reviewed models/pipeline.py',reversible=True,verifyAfter='Run regression')],rollback=dict(steps=['Restore captured baseline/pipeline.py'],dataLoss='None for a local deliverable',safeWindow='Before publication'),invariantImpact=[dict(d,baseline=next(r['baseline'] for r in b['invariants'] if r['invariant']==d['invariant']),expected='unchanged',why='No row filter; US fx remains one') for d in owner],newBehaviour=dict(statement='EU net uses converted refunds',assertion="net for id 2 equals '80'",falseOnBaseline='Baseline id 2 net is 90'),consumerNotes=['Revenue dashboard uses net'],safeUseNotes='No personal identifiers beyond synthetic id',rejectedAlternatives=[])
 p['negativeCase']=dict(change='Copied output without EU id 2',expectedFailures=['Row count'])
 if mode.startswith('coupled'):
  p['negativeCase']=dict(change='Decrease the copied first US net by 1',expectedFailures=['US revenue','Twice US revenue'])
 if mode=='negative-plan-missing':p.pop('negativeCase')
 if mode=='negative-plan-empty':p['negativeCase']['expectedFailures']=[]
 if mode=='negative-plan-duplicate':p['negativeCase']['expectedFailures']=['Row count','Row count']
 if mode=='negative-plan-unknown':p['negativeCase']['expectedFailures']=['Unknown invariant']
 if mode=='wrong-tolerance':p['invariantImpact'][0]['tolerance']='1'
 if mode=='missing-invariant':p['invariantImpact']=p['invariantImpact'][1:]
 if mode=='wrong-baseline':p['invariantImpact'][0]['baseline']='4'
 (root/'change-plan.json').write_text(json.dumps(p));print('Planned exact invariant-preserving change')
elif stage=='audit':
 b=json.loads((root.parent/'profile/baseline-report.json').read_text());p=json.loads((root.parent/'rules/change-plan.json').read_text())
 work=root/'models';work.mkdir(exist_ok=True);code=original.read_text();code=code.replace("D(r['refund'])", "D(r['refund'])*D(r['fx'])")
 if mode=='no-op':code=original.read_text()
 (work/'pipeline.py').write_text(code);shutil.copyfile(source,work/'orders.csv');subprocess.run(['python3','pipeline.py'],cwd=work,check=True)
 rows=read(work/'result.csv');before=read(root.parent/'profile/baseline/result.csv');baseline_assert=next(r['net'] for r in before if r['id']=='2')=='80';after_assert=next(r['net'] for r in rows if r['id']=='2')=='80'
 # Run the same checker over copied output; related totals may fail together.
 broken=[r for r in copy.deepcopy(rows) if r['id']!='2']
 if mode.startswith('coupled'):
  broken=copy.deepcopy(rows);broken[0]['net']=str(D(broken[0]['net'])-1)
 if mode in ['negative-extra-failure','negative-retarget']:broken=[r for r in copy.deepcopy(rows) if r['id']!='1']
 with (root/'negative-output.csv').open('w',newline='') as f:
  writer=csv.DictWriter(f,fieldnames=['id','region','net']);writer.writeheader();writer.writerows(broken)
 negative=check(read(root/'negative-output.csv'),b);real=check(rows,b)
 r=dict(baselineRef='../profile/baseline-report.json',snapshotIdentity=b['snapshot']['identity'],invariants=real,changeAssertion=dict(statement=p['newBehaviour']['statement'],assertion=p['newBehaviour']['assertion'],observed='Evaluate id 2 on both actual outputs',passOnBaseline=baseline_assert,passOnChanged=after_assert,pass_=not baseline_assert and after_assert),grainCheck=dict(grain='one row per order id',keyColumns=['id'],duplicateRows=0,nullKeyRows=0,pass_=True),independentControl=independent,negativeCase=dict(id='broken-invariant-fixture',whatWasBroken=p['negativeCase']['change'],query='Read negative-output.csv; run same check()',tripped=any(not x['pass'] for x in negative),invariants=negative),consumersChecked=[dict(consumer='Revenue dashboard',result='Protected US net unchanged; EU net now 80')],passed=sum(x['pass'] for x in real),failed=sum(not x['pass'] for x in real))
 for k in ['grainCheck','changeAssertion']:r[k]['pass']=r[k].pop('pass_')
 if mode=='wrong-counts':r['passed']+=4
 if mode=='wrong-delta':real[0]['delta']='1'
 if mode=='altered-expected':real[0]['expected']='4'
 if mode=='source-drift':r['snapshotIdentity']='different'
 if mode=='grain-failure':r['grainCheck']['duplicateRows']=1
 if mode=='negative-passes':r['negativeCase']['invariants']=copy.deepcopy(real)
 if mode=='wrong-negative-target':r['negativeCase']['whatWasBroken']='Unapproved mutation'
 if mode=='negative-forged-verdict':r['negativeCase']['invariants'][0]['pass']=True
 if mode=='coupled-missing-failure':
  r['negativeCase']['invariants'][2]=copy.deepcopy(real[2])
 if mode=='coupled-report-retarget':
  r['negativeCase']['invariants'][2]=copy.deepcopy(real[2]);r['negativeCase']['expectedFailures']=['US revenue']
 if mode=='negative-retarget':r['negativeCase']['expectedFailures']=['US revenue']
 if mode=='missing-consumer':r['consumersChecked']=[]
 manifest=dict(models=[dict(name='orders',file='models/pipeline.py',status='modified',changedColumns=['net'])],migrationSteps=p['migrationSteps'],rollback=p['rollback'])
 if mode=='altered-rollback':manifest['rollback']={}
 (root/'regression-report.json').write_text(json.dumps(r));(root/'manifest.json').write_text(json.dumps(manifest));print('Executed changed pipeline, both behavior assertions, and same invariant checker on negative output')
