"""Deterministic data program used by SDK transport fixtures, never a model claim."""
import csv, json, os
from pathlib import Path
from collections import Counter
source=Path(os.environ['SDK_FIXTURE_DATA'])
rows=list(csv.DictReader(source.open()))
stage=Path.cwd().name
mode=os.environ.get('SDK_FIXTURE_MODE','pass')
if stage=='profile':
 p={'dataset':{'name':str(source),'format':'csv','verified':True,'rowCount':len(rows),'scanned':'full','sampleNote':''},'columns':[{'name':k,'type':'text','nullCount':sum(not r[k] for r in rows),'nullPct':100*sum(not r[k] for r in rows)/len(rows),'distinctCount':len(set(r[k] for r in rows))} for k in rows[0]],'candidateKeys':[{'columns':['id'],'unique':False,'duplicateRows':1}], 'duplicateRows':0,'referentialOrphans':[],'driftSignals':[],'expectationEvidence':[{'expectation':'IDs are unique','status':'contradicted','observed':'1 duplicate among 4 rows'},{'expectation':'Amounts are nonnegative','status':'contradicted','observed':'1 negative among 4 rows'}],'issues':['Duplicate ID and negative amount'],'openQuestions':[]}
 Path('data-profile.json').write_text(json.dumps(p))
else:
 approved=json.loads(Path('../rules/quality-rules.json').read_text())['rules']
 ids=Counter(r['id'] for r in rows)
 counts={'unique-id':sum(n-1 for n in ids.values()),'present-id':sum(not r['id'] for r in rows),'nonnegative':sum(float(r['amount'])<0 for r in rows)}
 results=[{'id':r['id'],'rule':r['rule'],'severity':r['severity'],'class':'violation','checked':len(rows),'checkedUnit':'rows','violations':counts[r['id']],'rate':counts[r['id']]/len(rows),'pass':counts[r['id']]<=r['threshold']['value'],'query':r['assertion'],'missingEvidence':''} for r in approved]
 negative=[{'id':'negative-fixture','amount':'-1'}]
 detected=sum(float(r['amount'])<0 for r in negative)
 report={'dataset':{'identity':str(source),'rows':len(rows),'scanned':'full','sampleNote':''},'rules':results,'notRun':[],'negativeCase':{'rule':'nonnegative','seededRows':len(negative),'detectedRows':detected,'tripped':detected==len(negative)},'bySeverity':{k:{'total':sum(r['severity']==k for r in results),'failed':sum(r['severity']==k and not r['pass'] for r in results)} for k in ['blocker','major','minor']},'byClass':{k:{'total':sum(r['class']==k for r in results),'failed':sum(r['class']==k and not r['pass'] for r in results)} for k in ['violation','evidence-gap']},'passed':sum(r['pass'] for r in results),'failed':sum(not r['pass'] for r in results)}
 if mode=='bad-counts':report['rules'][0]['rate']=0
 if mode=='missing-rule':report['rules'].pop()
 if mode=='missed-negative':report['negativeCase']['detectedRows']=0
 Path('quality-scorecard.json').write_text(json.dumps(report))
print('Measured full input: '+str(len(rows))+' rows')
