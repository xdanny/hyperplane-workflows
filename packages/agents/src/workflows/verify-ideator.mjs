import { open } from 'node:fs/promises';
import { constants } from 'node:fs';
import path from 'node:path';

// Output names are fixed and the trusted parent directory is not writable by
// the worker. Open without following a symlink, then inspect that same handle.
export async function verifyIdeator(workspace) {
  const findings = [];
  const files = {};
  for (const name of ['ideas.json', 'brief.md']) {
    let handle;
    try {
      handle = await open(path.join(workspace, name), constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
      const stat = await handle.stat();
      if (!stat.isFile() || stat.size > 1_000_000) { findings.push(`${name} must be a regular file below 1 MB`); continue; }
      const buffer = Buffer.alloc(1_000_001);
      const { bytesRead } = await handle.read(buffer);
      if (bytesRead > 1_000_000) { findings.push(`${name} exceeds 1 MB`); continue; }
      files[name] = buffer.subarray(0, bytesRead).toString('utf8');
    } catch (error) {
      if (!['ENOENT', 'ELOOP'].includes(error.code)) throw error;
      findings.push(`${name} is missing or is a symlink`);
    } finally { await handle?.close(); }
  }
  let ideas;
  if (Object.hasOwn(files, 'ideas.json')) {
    try { ideas = JSON.parse(files['ideas.json'])?.ideas; }
    catch { findings.push('ideas.json must contain valid JSON'); }
    if (!Array.isArray(ideas) || ideas.length !== 3) findings.push('ideas.json must contain exactly three ideas');
    else {
      const titles = ideas.map((idea) => typeof idea?.title === 'string' ? idea.title.trim().toLowerCase() : '');
      if (new Set(titles).size !== 3) findings.push('Idea titles must be distinct');
      for (const idea of ideas) {
        for (const field of ['title', 'user', 'problem', 'firstVersion']) {
          if (typeof idea?.[field] !== 'string' || idea[field].trim().length < 3) findings.push(`Each idea needs a meaningful ${field}`);
        }
        if (typeof idea?.title === 'string' && !files['brief.md']?.includes(idea.title)) findings.push(`The brief must compare ${idea.title}`);
      }
    }
  }
  if (!files['brief.md'] || files['brief.md'].trim().length < 150) findings.push('brief.md must contain a substantive comparison (at least 150 characters)');
  if (files['brief.md'] && !/unvalidated|hypothes/i.test(files['brief.md'])) findings.push('The brief must explicitly mark demand as unvalidated');
  return { passed: findings.length === 0, findings };
}
