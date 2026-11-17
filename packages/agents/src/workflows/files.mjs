import { constants } from 'node:fs';
import { lstat, open, opendir } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';

// Data stays on disk. Reports read by application code have a separate small cap.
// Keep data quotas aligned with internal/sdkruntime/sdk_files.go.
export const maxFileBytes = 2_000_000_000;
export const maxStepBytes = 8_000_000_000;
export const maxReportBytes = 1_000_000;
const chunkBytes = 256 * 1024;
const same = (a, b) => ['dev', 'ino', 'size', 'mtimeNs', 'ctimeNs', 'nlink'].every(key => a[key] === b[key]);
const regular = (info, limit) => info.isFile() && info.nlink === 1n && info.size <= BigInt(limit);
const flags = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK;

// Links and special files are not deliverables. Re-inventorying detects added,
// removed and changed files. Metadata checks also reject changes during a read.
export async function inventoryFiles(workspace, directory, { signal } = {}) {
  const files = [], pending = [], directories = [];
  let bytes = 0, entries = 0;
  const buffer = Buffer.alloc(chunkBytes);
  async function visit(relative) {
    signal?.throwIfAborted();
    if (++entries > 4096 || Buffer.byteLength(relative) > 1024 || relative.split(path.sep).length > 32) throw new Error('Work output tree is too large or deep');
    const absolute = path.join(workspace, relative);
    const info = await lstat(absolute, { bigint: true });
    if (info.isDirectory() && !info.isSymbolicLink()) {
      const names = [];
      for await (const entry of await opendir(absolute)) {
        signal?.throwIfAborted();
        names.push(entry.name);
        if (entries + names.length > 4096) throw new Error('Work output tree is too large or deep');
      }
      directories.push({absolute, info});
      for (const name of names.sort()) {
        if (name.includes('\\')) throw new Error('Output filenames must be portable');
        await visit(path.join(relative, name));
      }
      return;
    }
    if (!regular(info, maxFileBytes)) throw new Error('Work outputs must be regular, unlinked files up to 2 GB');
    if (pending.length >= 1000 || (bytes += Number(info.size)) > maxStepBytes) throw new Error('Work outputs exceed 1000 files or 8 GB');
    pending.push({relative, absolute, info});
  }
  const root = await lstat(path.join(workspace, directory));
  if (!root.isDirectory() || root.isSymbolicLink()) throw new Error('Work output folder changed');
  await visit(directory);
  for (const {relative, absolute, info} of pending) {
    signal?.throwIfAborted();
    const hash = await hashRegularFile(absolute, info, buffer, signal);
    files.push({ path: relative.split(path.sep).join('/'), bytes: Number(info.size), sha256: hash });
  }
  for (const {absolute, info} of directories) {
    signal?.throwIfAborted();
    if (!same(info, await lstat(absolute, { bigint: true }))) throw new Error('Work output folder changed during inspection');
  }
  return files;
}

// A command-start event may arrive while that command is creating other files.
// Pin only the named program then; its output tree is sealed after completion.
export async function hashFile(folder, name, { signal, maxBytes = maxFileBytes } = {}) {
  signal?.throwIfAborted();
  if (!name || path.basename(name) !== name || ['.', '..'].includes(name) || name.includes('\\')) throw new Error('Use a filename in the current folder');
  const root = await lstat(folder);
  if (!root.isDirectory() || root.isSymbolicLink()) throw new Error('Work output folder changed');
  const absolute = path.join(folder, name), info = await lstat(absolute, { bigint: true });
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 0 || maxBytes > maxFileBytes || !regular(info, maxBytes)) throw new Error('File exceeds its declared bound or is not a regular, unlinked file');
  return hashRegularFile(absolute, info, Buffer.alloc(chunkBytes), signal);
}

async function hashRegularFile(absolute, info, buffer, signal) {
  if (!regular(info, maxFileBytes)) throw new Error('Work outputs must be regular, unlinked files up to 2 GB');
  const file = await open(absolute, flags);
  try {
    const current = await file.stat({ bigint: true });
    if (!regular(current, maxFileBytes) || !same(current, info)) throw new Error('Work output changed during inspection');
    const hash = await streamHash(file, Number(current.size), buffer, signal);
    if (hash !== await streamHash(file, Number(current.size), buffer, signal)) throw new Error('Work output changed during inspection');
    if (!same(current, await file.stat({ bigint: true })) || !same(current, await lstat(absolute, { bigint: true }))) throw new Error('Work output changed during inspection');
    return hash;
  } finally { await file.close(); }
}

// Two passes protect against mixed reads when filesystem timestamps are coarse.
// This is a consistency check, not an immutable snapshot against other processes.
async function streamHash(file, size, buffer, signal) {
  const hash = createHash('sha256');
  let length = 0;
  while (true) {
    signal?.throwIfAborted();
    const { bytesRead } = await file.read(buffer, 0, Math.min(buffer.length, size - length + 1), length);
    if (!bytesRead) break;
    length += bytesRead;
    if (length > size) throw new Error('Work output grew during inspection');
    hash.update(buffer.subarray(0, bytesRead));
  }
  signal?.throwIfAborted();
  if (length !== size) throw new Error('Work output changed during inspection');
  return hash.digest('hex');
}

// Small application-read JSON stays bounded even when the surrounding data is large.
export async function readJSON(folder, name, { signal } = {}) {
  signal?.throwIfAborted();
  const absolute = path.join(folder, name), file = await open(absolute, flags);
  try {
    const before = await file.stat({ bigint: true });
    if (!regular(before, maxReportBytes)) throw new Error(`Report ${name} must be a regular, unlinked file up to 1 MB`);
    const buffer = Buffer.alloc(Number(before.size) + 1);
    let length = 0;
    while (length < buffer.length) {
      signal?.throwIfAborted();
      const { bytesRead } = await file.read(buffer, length, buffer.length - length, null);
      if (!bytesRead) break;
      length += bytesRead;
    }
    signal?.throwIfAborted();
    if (length !== Number(before.size) || !same(before, await file.stat({ bigint: true })) || !same(before, await lstat(absolute, { bigint: true }))) throw new Error(`Report ${name} changed during inspection`);
    const hash = createHash('sha256').update(buffer.subarray(0, length)).digest('hex');
    if (hash !== await streamHash(file, length, Buffer.alloc(chunkBytes), signal) || !same(before, await file.stat({ bigint: true })) || !same(before, await lstat(absolute, { bigint: true }))) throw new Error(`Report ${name} changed during inspection`);
    return JSON.parse(buffer.subarray(0, length).toString('utf8'));
  } finally { await file.close(); }
}

export async function verifyFiles(workspace, progress, options) {
  for (const step of progress) {
    options?.signal?.throwIfAborted();
    if (step.document !== undefined) {
      const doc = step.document;
      if (!doc || doc.path !== step.id + '.md' || !Number.isSafeInteger(doc.bytes) || doc.bytes < 1 || doc.bytes > maxReportBytes ||
          typeof doc.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(doc.sha256) || Object.keys(doc).some(key => !['path', 'bytes', 'sha256'].includes(key))) throw new Error('Invalid document inventory');
      const actual = await hashFile(workspace, doc.path, { ...options, maxBytes: doc.bytes });
      if (actual !== doc.sha256 || (await lstat(path.join(workspace, doc.path))).size !== doc.bytes) throw new Error(`Document changed: ${step.title}`);
    }
    if (!step.files) continue;
    if (JSON.stringify(await inventoryFiles(workspace, step.id, options)) !== JSON.stringify(step.files)) throw new Error(`Checked work outputs changed: ${step.title}`);
  }
}
