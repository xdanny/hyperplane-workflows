import path from 'node:path';
import {inventoryFiles} from '../../src/workflows/files.mjs';

const folder=path.resolve(process.argv[2]);
global.gc?.();
const baseline=process.memoryUsage(),peak={...baseline};
const sample=()=>{const current=process.memoryUsage();for(const key of Object.keys(peak))peak[key]=Math.max(peak[key],current[key]);};
const timer=setInterval(sample,1),start=performance.now();
try {
  const files=await inventoryFiles(path.dirname(folder),path.basename(folder));sample();
  console.log(JSON.stringify({files,elapsedMs:performance.now()-start,baseline,peak,delta:Object.fromEntries(Object.keys(peak).map(key=>[key,peak[key]-baseline[key]]))}));
} finally {clearInterval(timer);}
