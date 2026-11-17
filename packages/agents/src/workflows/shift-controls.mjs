const require = (ok, message) => { if (!ok) throw new Error(message); };
const finite = n => typeof n === 'number' && Number.isFinite(n);
export const shifted = (value, tolerance) => tolerance === 0 ? value > 0 : value >= tolerance;

// These are synthetic weighted bins, not observed row counts. Keeping fractional
// weights avoids rounding a small requested distortion out of existence.
export function controlValue(statistic, unit, baseline, current) {
  for (const bins of [baseline,current]) require(bins.length > 0 && bins.every(b=>typeof b.bin==='string' && finite(b.count) && b.count>=0) && new Set(bins.map(b=>b.bin)).size===bins.length, 'Control bins need unique names and finite nonnegative weights');
  const a = baseline.reduce((n,b)=>n+b.count,0), b = current.reduce((n,b)=>n+b.count,0);
  require(a>0 && b>0, 'Control windows need positive total weight');
  const names=[...new Set([...baseline,...current].map(b=>b.bin))];
  const p=names.map(n=>(baseline.find(b=>b.bin===n)?.count??0)/a), q=names.map(n=>(current.find(b=>b.bin===n)?.count??0)/b);
  if(statistic==='psi') { require(['psi','absolute'].includes(unit),'PSI controls require psi or absolute units');return p.reduce((s,v,i)=>{const x=Math.max(v,1e-9),y=Math.max(q[i],1e-9);return s+(y-x)*Math.log(y/x);},0); }
  if(statistic==='kl') { require(['kl','absolute'].includes(unit),'KL controls require kl or absolute units');return q.reduce((s,v,i)=>s+(v===0?0:v*Math.log(v/Math.max(p[i],1e-9))),0); }
  if(statistic==='volume-drift') { require(['percent_of_baseline','count','absolute'].includes(unit),'Volume controls require percent_of_baseline or count/absolute units');return unit==='percent_of_baseline'?100*Math.abs(b-a)/a:Math.abs(b-a); }
  if(statistic==='category-emergence') {
    require(['points_of_share','absolute'].includes(unit),'Category controls require points_of_share or absolute units');
    const emerged=q.reduce((s,v,i)=>s+(p[i]===0?v:0),0),vanished=p.reduce((s,v,i)=>s+(q[i]===0?v:0),0);
    return Math.max(emerged,vanished)*(unit==='points_of_share'?100:1);
  }
  if(statistic==='rate-delta') { require(['points_of_share','absolute'].includes(unit),'Rate controls require points_of_share or absolute units');return Math.abs(q.at(-1)-p.at(-1))*(unit==='points_of_share'?100:1); }
  throw new Error('Unsupported control statistic');
}

export function shiftControls(profile, features, declared) {
  const selected=declared.length?declared:features.filter(f=>f.type==='categorical').map(f=>({feature:f.feature,multiple:1.25}));
  if(!selected.length) selected.push({feature:features[0].feature,multiple:1.25});
  const declaredBy=declared.length?'owner':'platform default (1.25x)';
  const controls=[],bins=[];
  for(const c of selected){
    const f=features.find(f=>f.feature===c.feature),p=profile.features.find(f=>f.name===c.feature);
    require(f && p && p.baseline?.bins && p.current?.bins,`Control feature ${c.feature} must be declared and comparably profiled`);
    const target=f.tolerance*c.multiple,statistic=f.statistic || (f.type==='categorical'?'category-emergence':'psi');
    require(f.tolerance>0 && finite(target) && target>0,'A multiplicative control needs a positive finite tolerance and target');
    const base=p.baseline.bins.map(b=>({bin:b.bin,count:b.count}));
    const original=p.current.bins.map(b=>({bin:b.bin,count:b.count}));
    const total=original.reduce((n,b)=>n+b.count,0),baseTotal=base.reduce((n,b)=>n+b.count,0);
    require(total>0 && baseTotal>0,'Control needs measured positive window counts');
    let current,method;
    if(statistic==='category-emergence'){
      const share=target/(f.unit==='points_of_share'?100:1);
      require(share>0 && share<1,'Requested emergence control is not a reachable share');
      require(![...base,...original].some(b=>b.bin==='__seeded_control__'),'Reserved synthetic control category occurs in source bins');
      // Isolate the controlled movement from unrelated real-window shifts.
      current=base.map(b=>({bin:b.bin,count:total*(b.count/baseTotal)*(1-share)}));
      current.push({bin:'__seeded_control__',count:total*share});
      method='Copy the measured current total as synthetic weights, recenter its vocabulary to baseline proportions, then inject the declared new-category share. This isolates the control from real-window shifts.';
    }else if(statistic==='volume-drift'){
      const wanted=f.unit==='percent_of_baseline'?baseTotal*(1+target/100):baseTotal+target;
      current=original.map(b=>({bin:b.bin,count:b.count*wanted/total}));
      method='Rescale a copy of current weighted bins to the requested increase over baseline total weight. Control windows use equal exposure; period-normalized rules must preserve that exposure in their control path.';
    }else{
      require(JSON.stringify(base.map(b=>b.bin))===JSON.stringify(original.map(b=>b.bin)),'Numeric/rate controls require shared bin labels and ordering');
      const normalizedBase=base.map(b=>b.count/baseTotal),start=original.map(b=>b.count/total);
      const make=v=>v.map((count,i)=>({bin:base[i].bin,count:count*total}));
      const metric=v=>controlValue(statistic,f.unit,base,make(v));
      const initial=metric(start);
      let left=start,right;
      if(initial>=target) right=normalizedBase;
      else {
        const vertices=start.map((_,i)=>start.map((_,j)=>i===j?1:0));
        right=vertices.sort((a,b)=>metric(b)-metric(a))[0];
        require(metric(right)>=target,`Requested ${statistic} control is unreachable with these baseline bins`);
      }
      let lo=0,hi=1;
      for(let i=0;i<80;i++){
        const mid=(lo+hi)/2,v=left.map((x,j)=>x+(right[j]-x)*mid),value=metric(v);
        if((initial>=target && value<=target)||(initial<target && value>=target)) hi=mid;else lo=mid;
      }
      const fraction=initial>=target?lo:hi;
      current=make(left.map((x,i)=>Math.max(0,x+(right[i]-x)*fraction)));
      method='Calibrate a convex mixture of copied current weights and a valid baseline/endpoint distribution. Weights never become negative. For rate-delta the final shared bin is the positive event; the rule must use that same event.';
    }
    const achieved=controlValue(statistic,f.unit,base,current),expectTrip=c.multiple>=1;
    require(Math.abs(achieved-target)<=1e-8*Math.max(1,target),'Emitted control weights do not reach the requested target');
    // At the exact threshold account for representational roundoff, without
    // changing the declared threshold or accepting an actually reversed verdict.
    const actualTrip=shifted(achieved,f.tolerance);
    require(actualTrip===expectTrip,'Requested control lies on an unrepresentable verdict boundary');
    const control={feature:f.feature,statistic,tolerance:f.tolerance,unit:f.unit,multiple:c.multiple,target,achieved,expectTrip,declaredBy,method,representation:'synthetic weighted bins',positiveBin:statistic==='rate-delta'?base.at(-1).bin:undefined};
    controls.push(control);
    bins.push({name:f.feature,baseline:{bins:base},current:{bins:current.map(b=>({...b,share:b.count/current.reduce((n,x)=>n+x.count,0)}))}});
  }
  return {spec:{generatedBy:'sdk-control',declaredBy,controls},counts:{features:bins}};
}
