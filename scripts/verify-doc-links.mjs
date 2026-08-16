// Copyright 2026 The Yorkie Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Assert every link in the documentation graph resolves.
//
// The graph is rooted at the files an agent or a new contributor actually
// starts from — CLAUDE.md, AGENTS.md, README.md — and is walked breadth-first
// through markdown links. A target that does not exist on disk is a finding.
//
// WHY ROOT IT, rather than checking every .md in the repository. An unrooted
// sweep spends its findings on documents nobody reaches: a stale link inside a
// file that itself has no path from CLAUDE.md is not a broken promise, it is
// dead weight, and mixing the two teaches everyone to skim the output. Rooting
// the walk also makes the check say something the file list cannot — that the
// path a reader is invited to follow actually goes somewhere.
//
// WHAT THIS IS NOT. It does not demand that every .md be reachable. Coverage —
// "was this file ever introduced to anyone?" — is the opposite question, and
// answering it here would mean either indexing hundreds of archived task
// records or maintaining an exception list that rots. This file is about the
// index's claims; coverage is about its silences.
//
// ARCHIVED TASK RECORDS ARE FROZEN. Their links are traversed for existence
// nowhere: a finished task's todo is a record of what was true when it was
// written, and design docs it cited legitimately move or disappear afterwards.
// Gating them would mean editing history to keep a checker quiet. The archive
// INDEX is still walked, so the rows pointing at those records stay honest.

import { existsSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const PREFIX = '[verify:doc-links]';

/** Repository-root files that seed the walk, in the order they are reported. */
export const ROOTS = ['CLAUDE.md', 'AGENTS.md', 'README.md'];

/** Archived task records: reached, never traversed. Their index is exempt. */
const FROZEN = /^docs\/tasks\/archive\/.*\/[^/]+\.md$/;

/**
 * The prose of a markdown document — outside fenced code, outside HTML
 * comments.
 *
 * A fenced block is an example, not a claim: `[see](./does-not-exist.md)`
 * inside a snippet demonstrating link syntax is not a promise that the file is
 * there. Commented-out rows are excluded for the same reason — a reader never
 * follows them.
 *
 * COMMENTS ARE STRIPPED FIRST, and the order matters. Fences first means a
 * comment containing a ``` line derails fence tracking and everything after it
 * is misclassified. This order's failure mode is the opposite and louder: an
 * unterminated `<!--` inside a fence swallows prose, which drops links and can
 * only ever cause a missed finding on an already-malformed document.
 *
 * Fence matching follows CommonMark closely enough to matter: a fence closes
 * only with its own character, a run at least as long as the opener's, and
 * nothing after it. An info string (` ```js `) opens but never closes.
 *
 * INLINE CODE GOES TOO, and for the same reason one level down. A document
 * explaining this very check writes `` `[board](board/TYPO.md)` `` to show
 * what a typo'd row looks like. Rendered, that is a literal string, not a
 * link — nobody can click it — and reading it as a path claim would demand
 * that a repository create the broken file its own postmortem describes.
 */
function prose(content) {
  const stripped = content
    .replace(/<!--[\s\S]*?-->/g, (comment) => comment.replace(/[^\n]/g, ''))
    // Inline spans only: a run of backticks, its content, and a matching run,
    // all on one line. Fenced blocks start at a line boundary and are handled
    // below, so this cannot eat one.
    .replace(/(`+)(?!`)([^\n]*?[^`])\1(?!`)/g, (span) => span.replace(/[^\n]/g, ''));

  const lines = [];
  let fence = null;
  for (const line of stripped.split('\n')) {
    const match = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (match) {
      const [, run, rest] = match;
      if (fence === null) {
        fence = { char: run[0], length: run.length };
        continue;
      }
      if (run[0] === fence.char && run.length >= fence.length && !rest.trim()) {
        fence = null;
      }
      continue;
    }
    if (fence === null) lines.push(line);
  }
  return lines.join('\n');
}

/**
 * Is `target` a claim about a path, or incidental prose?
 *
 * Markdown's link syntax collides with ordinary writing: `[userId](userId)`,
 * `[x](y)`, and a regex fragment in an unfenced line all parse as links.
 * Treating those as paths produced 78 false findings out of 95 on the first
 * repository this ran against — enough noise to make the gate worthless.
 *
 * A real path claim is explicitly relative, or contains a separator, or ends
 * in an extension. Everything else is left alone. The cost is a missed finding
 * on a bare sibling reference like `[the plan](plan)`; nothing in these
 * repositories writes one, and a quiet miss beats a gate nobody reads.
 */
function isPathClaim(target) {
  return /^\.{1,2}\//.test(target) || target.includes('/') || /\.[a-z0-9]{1,5}$/i.test(target);
}

/**
 * Every link target in a document, as written.
 *
 * Inline links and image embeds both count — a missing screenshot is as broken
 * as a missing document, and only one of the two is visible in review. `@path`
 * imports are included because CLAUDE.md and AGENTS.md pull other files in
 * that way, and those are the roots of this whole graph.
 *
 * Reference definitions and angle-bracket destinations are not read. Nothing
 * here uses them; adding support on speculation would risk a false positive on
 * an honest document.
 */
export function linkTargets(content) {
  const targets = new Set();
  const text = prose(content);

  for (const match of text.matchAll(/!?\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
    const target = match[1].split('#')[0];
    if (!target) continue; // a bare `#anchor`
    if (/^[a-z][a-z0-9+.-]*:/i.test(target)) continue; // http:, mailto:, …
    if (!isPathClaim(target)) continue;
    targets.add(target);
  }

  for (const match of text.matchAll(/(?:^|\s)@([\w./-]+\.md)/gm)) {
    targets.add(match[1]);
  }

  return targets;
}

/**
 * Where `abs` actually lands on disk, or null if nowhere.
 *
 * EXTENSIONLESS TARGETS ARE ROUTES. A documentation site links a sibling page
 * as `./formulas`, and its renderer serves `formulas.md`. Demanding the
 * extension would report nine such links in one package here, every one of
 * which resolves for every reader — and the fix would be to rewrite working
 * navigation to satisfy a checker. So the extension is tried, then the
 * directory index, before anything is called dead.
 */
function resolve(abs) {
  for (const candidate of [abs, `${abs}.md`, path.join(abs, 'index.md')]) {
    if (existsSync(candidate)) return candidate;
  }
  return null;
}

/**
 * Every dead link reachable from the roots of `repoRoot`.
 *
 * Exported and pure over a directory so the suite can point it at a planted
 * tree — the only way to prove a dead link is actually caught.
 */
export function collectFindings(repoRoot) {
  const findings = [];
  const seen = new Set();
  const queue = [];

  for (const root of ROOTS) {
    if (!existsSync(path.resolve(repoRoot, root))) continue;
    seen.add(root);
    queue.push(root);
  }
  if (queue.length === 0) {
    return [`no root document found (looked for ${ROOTS.join(', ')})`];
  }

  while (queue.length > 0) {
    const from = queue.shift();
    if (FROZEN.test(from)) continue;

    const fromAbs = path.resolve(repoRoot, from);
    for (const target of linkTargets(readFileSync(fromAbs, 'utf8'))) {
      // Outside the repository is out of scope, and the guard has to come
      // BEFORE the existence check. A documentation site writes root-relative
      // links — `/sheets/formulas`, `/images/chart.png` — that resolve against
      // the SITE's root, which this script has no way to locate. Checked
      // naively they all fail, and the twenty-odd findings that produces would
      // be pure noise on links that work in every browser.
      const literal = path.resolve(path.dirname(fromAbs), target);
      if (path.relative(repoRoot, literal).startsWith('..')) continue;

      const abs = resolve(literal);
      if (abs === null) {
        findings.push(`${from} links ${target}, which does not exist`);
        continue;
      }
      let rel = path.relative(repoRoot, abs).split(path.sep).join('/');

      // A directory link is satisfied by the directory. Its index, if it has
      // one, is how the walk continues past it.
      if (statSync(abs).isDirectory()) {
        if (!existsSync(path.resolve(abs, 'README.md'))) continue;
        rel = `${rel}/README.md`;
      }

      if (!rel.endsWith('.md') || seen.has(rel)) continue;
      seen.add(rel);
      queue.push(rel);
    }
  }

  return findings;
}

const isDirectRun =
  process.argv[1] &&
  path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url));

if (isDirectRun) {
  const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const findings = collectFindings(repoRoot);
  if (findings.length === 0) {
    console.log(`${PREFIX} Every link reachable from ${ROOTS.join(' / ')} resolves.`);
  } else {
    for (const finding of findings) console.log(`${PREFIX}   ${finding}`);
    console.log(`${PREFIX} ${findings.length} dead link(s) found.`);
    process.exit(1);
  }
}
