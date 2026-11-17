// Opt-in native permission check; public HTTP requests and synthetic local data.
// Run from any directory with Node 22+ and the pinned SDK dependencies installed.
import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp,mkdir,writeFile,readFile,rm,stat,realpath} from 'node:fs/promises';
import {lookup} from 'node:dns/promises';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {taskAccessOptions} from '../src/task-access.mjs';
const exec=promisify(execFile);
if(process.platform!=='darwin'||process.arch!=='arm64') throw new Error('This probe currently targets the pinned macOS arm64 executable');
const binary=fileURLToPath(new URL('../node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex',import.meta.url));
const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'sdk-native-access-')));
const parent=path.join(root,'workflow'),work=path.join(parent,'work');await mkdir(work,{recursive:true});
const privateFile=path.join(root,'private.txt'),source=path.join(root,'source.txt');
await writeFile(privateFile,'synthetic private fixture');await writeFile(source,'public-source');await writeFile(path.join(parent,'previous.txt'),'previous-output');
const address=(await lookup('example.org',{family:4})).address;
const policy=await taskAccessOptions({workspace:work,inputRoots:[parent],access:{hosts:['example.com'],readPaths:[source],environment:['WORKFLOW_PROBE']}},{...process.env,WORKFLOW_PROBE:'selected-canary',UNSELECTED_PROBE:'not-forwarded'});
// Paths created here contain no quote characters. The program itself is static
// and runs directly under Codex's native sandbox, without a model.
const script=`set -u
printf hello > inside.txt
[ "$(cat '${source}')" = public-source ] && echo SOURCE_READ_ALLOWED
[ "$(cat ../previous.txt)" = previous-output ] && echo PRIOR_READ_ALLOWED
if (printf bad > '${source}') 2>/dev/null; then echo SOURCE_WRITE_ALLOWED; else echo SOURCE_WRITE_DENIED; fi
if cat '${privateFile}' >/dev/null 2>&1; then echo PRIVATE_READ_ALLOWED; else echo PRIVATE_READ_DENIED; fi
if (printf bad > ../outside.txt) 2>/dev/null; then echo PARENT_WRITE_ALLOWED; else echo PARENT_WRITE_DENIED; fi
[ "$WORKFLOW_PROBE" = selected-canary ] && echo SELECTED_PRESENT
[ -z "\${UNSELECTED_PROBE+x}" ] && echo UNSELECTED_ABSENT
curl --max-time 15 -s -o /dev/null -w 'allowed:%{http_code}\\n' https://example.com
curl --max-time 15 -s -o /dev/null -w 'denied:%{http_code}\\n' https://example.org
curl --noproxy '*' --resolve 'example.org:443:${address}' --max-time 15 -s -o /dev/null -w 'direct-ip:%{http_code}\\n' https://example.org
true`;
try {
 const args=['sandbox','-P','hyperplane-task','-C',work,...policy.clientOptions.configOverrides.flatMap(v=>['-c',v]),'/bin/sh','-c',script];
 const {stdout,stderr}=await exec(binary,args,{env:policy.clientOptions.env,timeout:60000});
 const required=['SOURCE_READ_ALLOWED','SOURCE_WRITE_DENIED','PRIOR_READ_ALLOWED','PRIVATE_READ_DENIED','PARENT_WRITE_DENIED','SELECTED_PRESENT','UNSELECTED_ABSENT','allowed:200','denied:000','direct-ip:000'];
 for(const item of required)assert.ok(stdout.includes(item),`Missing ${item}: ${stdout} ${stderr}`);
 assert.equal(await readFile(path.join(work,'inside.txt'),'utf8'),'hello');
 assert.equal(await readFile(source,'utf8'),'public-source');
 await assert.rejects(stat(path.join(parent,'outside.txt')),{code:'ENOENT'});
 console.log(JSON.stringify({passed:true,platform:process.platform,architecture:process.arch,stdout,stderr,directAttemptUsedResolvedIPv4:true},null,2));
} finally {await rm(policy.runtime,{recursive:true,force:true});await rm(root,{recursive:true,force:true});}
