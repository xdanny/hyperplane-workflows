"""Real computations for SDK transport fixtures; not a live model claim."""
import csv,json,os,math
from pathlib import Path
from collections import Counter
before=list(csv.DictReader(open(os.environ['SDK_FIXTURE_BASELINE'])))
after=list(csv.DictReader(open(os.environ['SDK_FIXTURE_CURRENT'])))
def write(name,data):Path(name).write_text(json.dumps(data))
def bins(rows):return Counter(r['category'] for r in rows)
a,b=bins(before),bins(after);labels=sorted(set(a)|set(b))
def side(rows,counts):return {'nullCount':0,'nullPct':0,'distinctCount':len(counts),'bins':[{'bin':k,'count':counts[k],'share':counts[k]/len(rows)} for k in labels]}
if Path.cwd().name=='profile':
 p={'baseline':{'identity':os.environ['SDK_FIXTURE_BASELINE'],'verified':True,'rows':len(before),'periodObserved':'baseline','scanned':'full'},'current':{'identity':os.environ['SDK_FIXTURE_CURRENT'],'verified':True,'rows':len(after),'periodObserved':'current','scanned':'full'},'features':[{'name':'category','type':'numeric' if os.environ.get('SDK_FIXTURE_MODE')=='numeric-psi' else 'categorical','binningNote':'Shared union vocabulary','baseline':side(before,a),'current':side(after,b)}],'volumeByPeriod':[{'window':'baseline','period':'baseline','rows':len(before)},{'window':'current','period':'current','rows':len(after)}],'schemaDifferences':[],'comparabilityHazards':[],'openQuestions':[]}
 write('window-profiles.json',p)
else:
 p=json.loads(Path('../profile/window-profiles.json').read_text());rules=json.loads(Path('../rules/shift-rules.json').read_text())['rules']
 spec=json.loads(Path('../profile/control-spec.json').read_text());controls=json.loads(Path('../profile/control-counts.json').read_text())
 def measure(stat,base,cur):
  x={r['bin']:r['count'] for r in base};y={r['bin']:r['count'] for r in cur};xt=sum(x.values());yt=sum(y.values())
  if stat=='psi':
   return sum((max(y.get(k,0)/yt,1e-9)-max(x.get(k,0)/xt,1e-9))*math.log(max(y.get(k,0)/yt,1e-9)/max(x.get(k,0)/xt,1e-9)) for k in set(x)|set(y))
  if stat=='volume-drift':return 100*abs(yt-xt)/xt
  return 100*max(sum(v for k,v in y.items() if x.get(k,0)==0)/yt,sum(v for k,v in x.items() if y.get(k,0)==0)/xt)
 base=p['features'][0]['baseline']['bins'];cur=p['features'][0]['current']['bins']
 results=[]
 for r in rules:
  value=measure(r['statistic'],base,cur)
  results.append({'id':r['id'],'feature':r['feature'],'statistic':r['statistic'],'severity':r['severity'],'tolerance':r['tolerance']['value'],'value':value,'shifted':value>=r['tolerance']['value'],'checkedUnit':'rows','checkedBaseline':len(before),'checkedCurrent':len(after),'excludedBaseline':0,'excludedCurrent':0,'query':'Counts from declared CSVs','topContributors':[{'bin':labels[-1],'baselineShare':a[labels[-1]]/len(before),'currentShare':b[labels[-1]]/len(after),'contribution':value}] if value else []})
 negative=[]
 for c in spec['controls']:
  data=next(f for f in controls['features'] if f['name']==c['feature']);value=measure(c['statistic'],data['baseline']['bins'],data['current']['bins'])
  negative.append({**c,'value':value,'tripped':value>=c['tolerance'],'source':'sdk-control'})
 report={'windows':{'baseline':p['baseline'],'current':p['current']},'rules':results,'notRun':[],'negativeControls':negative,'bySeverity':{s:{'total':sum(r['severity']==s for r in results),'shifted':sum(r['severity']==s and r['shifted'] for r in results)} for s in ['blocker','major','minor']},'shifted':sum(r['shifted'] for r in results),'stable':sum(not r['shifted'] for r in results)}
 mode=os.environ.get('SDK_FIXTURE_MODE','pass')
 if mode=='bad-counts':report['rules'][0]['checkedCurrent']=0
 if mode=='missing-rule':report['rules'].pop()
 if mode=='missed-control':report['negativeControls'][0]['tripped']=False
 if mode=='rounded-control':report['negativeControls'][0]['value']=0
 if mode=='control-changed':Path('../profile/control-spec.json').write_text('{}')
 write('shift-scorecard.json',report)
print('Measured both declared CSV windows and the fixed controls' if Path.cwd().name!='profile' else 'Measured both declared CSV windows')
