#!/usr/bin/env node
// Internal UX quality gate (deterministic, zero-dep).
//
// Hyperplane holds generated apps to a UX bar; it must hold its OWN UI to the same
// discipline. This lints apps/web/src for the design-system rules that keep the UI
// distinctive + accessible — the always-on half of the UX gate. The codex VISUAL
// panel (screenshots judged on craft/hierarchy/interaction lenses) is the other
// half and runs via `hyperplane ux-review` when codex is available.
//
// Rules (errors fail the gate):
//   1. No hardcoded colors in components — use theme tokens (var(--…)). theme.css
//      is the ONE place literal colors are defined.
//   2. If the CSS animates (animation/transition/@keyframes), it MUST honor
//      prefers-reduced-motion at least once.
//   3. Icon/emoji-only <button> must carry an aria-label (screen-reader text).
// Warnings (reported, don't fail): thin :focus-visible coverage vs :hover.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, basename } from 'node:path';

const ROOT = new URL('..', import.meta.url).pathname;
const SRC = join(ROOT, 'apps/web/src');

function walk(dir) {
  const out = [];
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) out.push(...walk(p));
    else if (/\.(tsx|ts|css)$/.test(e)) out.push(p);
  }
  return out;
}

// Flag OPAQUE hex literals in components (the real "should be a token" smell).
// rgba()/hsla() overlays, shadows and tints are legitimately literal, so we don't
// flag them; and token-DEFINITION files (theme.css / styles.css / any :root token
// block) are where literals are supposed to live.
const COLOR_RE = /#[0-9a-fA-F]{3,8}\b/g;
const errors = [];
const warnings = [];
let hoverCount = 0;
let focusVisibleCount = 0;
let animates = false;
let reducedMotion = false;

for (const file of walk(SRC)) {
  const rel = relative(ROOT, file);
  const src = readFileSync(file, 'utf8');
  // Token-definition files are where literal colors belong: theme.css, styles.css,
  // or any CSS that defines a :root token block.
  const isTokenDef =
    basename(file) === 'theme.css' ||
    basename(file) === 'styles.css' ||
    (file.endsWith('.css') && /:root\s*\{[\s\S]*?--[\w-]+\s*:/.test(src));

  // 1. hardcoded colors (skip token-definition files)
  if (!isTokenDef) {
    src.split('\n').forEach((line, i) => {
      // ignore comment lines + lines that are clearly defining a CSS var fallback is still a literal → flag
      if (/^\s*(\/\/|\/\*|\*)/.test(line)) return;
      const m = line.match(COLOR_RE);
      if (m) {
        // allow #fff/#000 only inside theme.css; everywhere else require a token
        errors.push(`${rel}:${i + 1}  hardcoded color ${m[0]} — use a theme token var(--…)`);
      }
    });
  }

  // 2. motion accounting (CSS)
  if (file.endsWith('.css')) {
    if (/@keyframes|animation:|transition:/.test(src)) animates = true;
    if (/prefers-reduced-motion/.test(src)) reducedMotion = true;
    hoverCount += (src.match(/:hover/g) || []).length;
    focusVisibleCount += (src.match(/:focus-visible/g) || []).length;
  }

  // 3. icon-only buttons need aria-label (heuristic over JSX)
  if (file.endsWith('.tsx')) {
    // <button …>{glyph}</button> with no aria-label/title and tiny text content
    const btnRe = /<button\b([^>]*)>([\s\S]{0,40}?)<\/button>/g;
    let mm;
    while ((mm = btnRe.exec(src))) {
      const attrs = mm[1];
      const inner = mm[2].replace(/\{[^}]*\}/g, '').replace(/<[^>]*>/g, '').trim();
      const hasLabel = /aria-label|aria-labelledby|title=/.test(attrs);
      const looksIconOnly = inner.length <= 2 && !/[a-zA-Z]{2,}/.test(inner);
      if (looksIconOnly && !hasLabel) {
        const lineNo = src.slice(0, mm.index).split('\n').length;
        errors.push(`${rel}:${lineNo}  icon/emoji-only <button> needs an aria-label`);
      }
    }
  }
}

if (animates && !reducedMotion) {
  errors.push('apps/web/src: CSS animates but no @media (prefers-reduced-motion: reduce) found');
}
if (hoverCount > 0 && focusVisibleCount < hoverCount * 0.5) {
  warnings.push(`thin keyboard focus coverage: ${focusVisibleCount} :focus-visible vs ${hoverCount} :hover`);
}

for (const w of warnings) console.log(`  ⚠ ${w}`);
if (errors.length) {
  console.error(`\nUX GATE FAILED — ${errors.length} issue(s):`);
  for (const e of errors) console.error(`  ✗ ${e}`);
  process.exit(1);
}
console.log(`UX GATE PASSED (${warnings.length} warning(s))`);
