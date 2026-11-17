import { validateTaskAccess } from './validation.mjs';
export { validateTaskAccess } from './validation.mjs';
import path from 'node:path';
import os from 'node:os';
import { mkdtemp, mkdir, realpath, rm, stat, access as canAccess } from 'node:fs/promises';

// SDK 0.153.4 flattens structured object keys on dots. Send maps containing
// hostnames and file paths as one TOML table through its raw config interface.
export function configTable(value) {
  if (Array.isArray(value)) return '[' + value.map(configTable).join(',') + ']';
  if (value && typeof value === 'object') return '{' + Object.entries(value).map(([k,v]) => JSON.stringify(k) + '=' + configTable(v)).join(',') + '}';
  return JSON.stringify(value);
}

export async function taskAccessOptions({workspace, access, toolchain, sideEffect, inputRoots = []}, env = process.env) {
  validateTaskAccess({access,toolchain,sideEffect});
  if (!access) return null;
  const hosts = access.hosts ?? [];
  const selected = access.environment ?? [];
  for (const key of selected) if (!env[key]) throw new Error(`Configure the data connection environment variable ${key} on the run host`);
  const roots = [];
  for (const root of [...inputRoots,...(access.readPaths ?? [])]) {
    let resolved;
    try {
      resolved = await realpath(root);
      await canAccess(resolved, 4);
    } catch {
      throw new Error(`Readable input unavailable: ${root}. Check that it exists and the run host can read it.`);
    }
    if (resolved === '/') throw new Error('Readable inputs cannot grant the filesystem root');
    roots.push(resolved);
  }
  let uvExecutable;
  if (toolchain) {
    for (const dir of (env.PATH ?? '').split(path.delimiter).filter(path.isAbsolute)) {
      try { const candidate = await realpath(path.join(dir, 'uv')); await canAccess(candidate, 1); if (!(await stat(candidate)).isFile()) continue; uvExecutable = candidate; break; } catch {}
    }
    if (!uvExecutable) throw new Error('Tool setup requires uv installed on the run host');
  }
  const target = await realpath(workspace);
  const runtime = await realpath(await mkdtemp(path.join(os.tmpdir(), 'hyperplane-sdk-tools-')));
  try {
  await mkdir(path.join(runtime,'tmp'));
  const filesystem = { ':minimal':'read', [target]:'write', [runtime]:'write' };
  if (uvExecutable) filesystem[uvExecutable] = 'read';
  for (const root of roots) if (root !== target && root !== runtime) filesystem[root] = 'read';
  // Preserve executable lookup without passing the API's database credentials,
  // application settings, inherited proxies or shell initialization variables.
  const childEnv = Object.fromEntries(Object.entries(env).filter(([k]) => ['PATH','HOME','USER','LOGNAME','SHELL','LANG','CODEX_HOME','HYPERPLANE_CODEX_EXECUTABLE'].includes(k) || k.startsWith('LC_') || selected.includes(k) || (env.HYPERPLANE_CODEX_EXECUTABLE && k.startsWith('SDK_FIXTURE_'))));
  const uv = path.join(runtime,'venv');
  const set = { TMPDIR:path.join(runtime,'tmp'), TMP:path.join(runtime,'tmp'), TEMP:path.join(runtime,'tmp') };
  if (toolchain) Object.assign(set,{UV_CACHE_DIR:path.join(runtime,'cache'),UV_PYTHON_INSTALL_DIR:path.join(runtime,'python'),UV_PROJECT_ENVIRONMENT:uv,VIRTUAL_ENV:uv,PATH:path.join(uv,'bin')+path.delimiter+(env.PATH || '/usr/bin:/bin')});
  const filters = Object.fromEntries(['PATH','HOME','USER','LOGNAME','SHELL','LANG','LC_*','TMPDIR','TMP','TEMP',...selected,...(toolchain?['UV_CACHE_DIR','UV_PYTHON_INSTALL_DIR','UV_PROJECT_ENVIRONMENT','VIRTUAL_ENV']:[])].map(k=>[k,'include']));
  // No hosts means no network at all: a step that only reads a local file gets
  // the read grant without a destination allowlist standing in for one.
  const network = hosts.length ? {enabled:true,domains:Object.fromEntries(hosts.map(h=>[h,'allow'])),allow_upstream_proxy:false,allow_local_binding:false,enable_socks5_udp:false,dangerously_allow_all_unix_sockets:false,dangerously_allow_non_loopback_proxy:false} : {enabled:false};
  return { runtime,
    clientOptions:{env:childEnv,config:{default_permissions:'hyperplane-task',project_doc_max_bytes:0},configOverrides:[
      'permissions='+configTable({'hyperplane-task':{filesystem,network}}),
      'features.network_proxy='+configTable(hosts.length?{enabled:true,proxy_url:'http://127.0.0.1:0',socks_url:'http://127.0.0.1:0'}:{enabled:false}),
      'shell_environment_policy='+configTable({inherit:'all',ignore_default_excludes:true,filters,set,experimental_use_profile:false}),
      'allow_login_shell=false',
    ]},
    instructions:`${hosts.length ? `Network access is limited to these exact hosts: ${hosts.join(', ')}.` : 'Network access is disabled for this step; work only from the readable inputs below.'} Readable source inputs: ${roots.join(', ') || 'only the current work folder and system tools'}. Connection environment names: ${selected.join(', ') || 'none'}. Use only these references; never print credentials or write them into outputs. Do not publish, send messages or modify remote data. ${toolchain ? `Tool setup is explicitly permitted. Install only in $VIRTUAL_ENV; caches and temporary files must use the supplied UV_* and TMPDIR locations. Use ${JSON.stringify(uvExecutable)} venv "$VIRTUAL_ENV" and ${JSON.stringify(uvExecutable)} pip install --python "$VIRTUAL_ENV/bin/python" with pinned versions. Record the interpreter and package versions in your output. Never install globally or place environments/caches in the output folder.` : 'Tool installation is not authorized. Use available tools; report missing dependencies.'}`,
  };
  } catch (error) { await rm(runtime, {recursive:true,force:true}); throw error; }
}
