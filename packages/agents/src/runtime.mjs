import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, open, realpath } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const runtimeFingerprintVersion = 1;
const packageRoot = fileURLToPath(new URL('../', import.meta.url));
const fileLimit = 2_000_000;
const totalLimit = 16_000_000;
const allowedPath = /^(runtime-files\.json|checks\.json|package(?:-lock)?\.json|bin\/codex-isolated|src\/[a-z][a-z0-9-]*\.mjs|src\/workflows\/[a-z][a-z0-9_-]*\.(?:mjs|py))$/;
const required = ['runtime-files.json', 'checks.json', 'package.json', 'package-lock.json', 'bin/codex-isolated', 'src/runtime.mjs'];

// Fingerprints package sources and dependency manifests, not installed dependency
// bytes or host binaries. The inventory is explicit; no directory recursion or
// execution of package code is needed to identify a candidate installation.
export async function runtimeFingerprint({ root = packageRoot } = {}) {
  root = await realpath(root);
  async function read(name) {
    const parts = name.split('/');
    for (let i = 1; i <= parts.length; i++) {
      if ((await lstat(path.join(root, ...parts.slice(0, i)))).isSymbolicLink()) throw new Error(`SDK runtime link: ${name}`);
    }
    const file = await open(path.join(root, name), constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      const before = await file.stat({ bigint: true });
      if (!before.isFile() || before.size > BigInt(fileLimit)) throw new Error(`Invalid SDK runtime file: ${name}`);
      const data = Buffer.alloc(Number(before.size));
      let offset = 0;
      while (offset < data.length) {
        const { bytesRead } = await file.read(data, offset, data.length - offset, offset);
        if (!bytesRead) throw new Error(`SDK runtime file changed: ${name}`);
        offset += bytesRead;
      }
      const after = await file.stat({ bigint: true });
      if (before.size !== after.size || before.mtimeNs !== after.mtimeNs || before.ctimeNs !== after.ctimeNs) throw new Error(`SDK runtime file changed: ${name}`);
      return data;
    } finally { await file.close(); }
  }
  const inventoryBytes = await read('runtime-files.json');
  const inventory = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(inventoryBytes));
  if (!inventory || Object.keys(inventory).sort().join(',') !== 'files,version' || inventory.version !== runtimeFingerprintVersion || !Array.isArray(inventory.files) || !inventory.files.length || inventory.files.length > 256) throw new Error('Invalid SDK runtime inventory');
  for (let i = 0; i < inventory.files.length; i++) {
    const name = inventory.files[i];
    if (typeof name !== 'string' || !allowedPath.test(name) || name !== name.trim() || (i > 0 && inventory.files[i - 1] >= name)) throw new Error('Invalid SDK runtime inventory path');
  }
  if (required.some(name => !inventory.files.includes(name))) throw new Error('Missing SDK runtime inventory path');
  const hash = createHash('sha256').update('hyperplane-sdk-runtime-v1\n');
  let total = 0;
  for (const name of inventory.files) {
    const data = name === 'runtime-files.json' ? inventoryBytes : await read(name);
    total += data.length;
    if (total > totalLimit) throw new Error('SDK runtime exceeds fingerprint bounds');
    hash.update(name).update('\0').update(createHash('sha256').update(data).digest('hex')).update('\n');
  }
  return hash.digest('hex');
}
