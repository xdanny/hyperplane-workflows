import test from 'node:test';
import assert from 'node:assert/strict';
import { copyFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';
import { runtimeFingerprint, runtimeFingerprintVersion } from '@hyperplane/agents/runtime';
const source = fileURLToPath(new URL('../', import.meta.url));

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'sdk-fingerprint-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const inventory = JSON.parse(await readFile(path.join(source, 'runtime-files.json'), 'utf8'));
  for (const name of inventory.files) {
    await mkdir(path.dirname(path.join(root, name)), { recursive: true });
    await copyFile(path.join(source, name), path.join(root, name));
  }
  return { root, inventory };
}

test('public runtime fingerprint identifies source bytes and excludes docs/tests/dependency installation', async t => {
  const { root } = await fixture(t);
  assert.equal(runtimeFingerprintVersion, 1);
  const expected = await runtimeFingerprint({ root });
  assert.match(expected, /^[a-f0-9]{64}$/);
  assert.equal(expected, await runtimeFingerprint());
  for (const name of ['README.md', 'test/x.mjs', 'node_modules/x/index.js']) {
    await mkdir(path.dirname(path.join(root, name)), { recursive: true });
    await writeFile(path.join(root, name), 'excluded');
  }
  assert.equal(await runtimeFingerprint({ root }), expected);
  await writeFile(path.join(root, 'src/turn.mjs'), '// runtime change\n');
  assert.notEqual(await runtimeFingerprint({ root }), expected);
});

test('inventory rejects traversal, unsupported paths, duplicates, missing members and oversized inventories', async t => {
  const { root, inventory } = await fixture(t);
  for (const invalid of ['../secret', '/etc/passwd', 'src/../package.json', 'src/index.mjs\n', 'src\\index.mjs', 'test/x.mjs', 'node_modules/x.mjs', 'src/nested/x.mjs']) {
    await writeFile(path.join(root, 'runtime-files.json'), JSON.stringify({ version: 1, files: [...inventory.files, invalid].sort() }));
    await assert.rejects(runtimeFingerprint({ root }), /inventory path/);
  }
  for (const files of [[...inventory.files, inventory.files[0]], inventory.files.slice(1), Array(257).fill('checks.json')]) {
    await writeFile(path.join(root, 'runtime-files.json'), JSON.stringify({ version: 1, files }));
    await assert.rejects(runtimeFingerprint({ root }), /inventory/i);
  }
});

test('runtime file reads reject symlinks and bounded oversized files', async t => {
  const { root } = await fixture(t);
  const target = path.join(root, 'src/turn.mjs');
  await rm(target);
  await symlink(path.join(root, 'src/index.mjs'), target);
  await assert.rejects(runtimeFingerprint({ root }), /link/);
  await rm(target);
  await writeFile(target, Buffer.alloc(2_000_001));
  await assert.rejects(runtimeFingerprint({ root }), /Invalid SDK runtime file/);
});
