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

// Assert every Go file carries the Apache 2.0 header.
//
// CLAUDE.md requires it and CONTRIBUTING.md shows it, and until this existed
// nothing checked it: `docs/design/agent-command-verbs.md` §4b listed the
// licence header under "enforced by nothing". It had drifted — 17 of 486
// tracked .go files carried no header when this was written, across five
// years, which is what a convention with no lane behind it looks like.
//
// WHAT IT MATCHES. One line: the Apache grant clause. Not the copyright year,
// not the exact comment style, not the wording around it. The repository has
// both `/* */` and `//` header blocks and copyright years from 2020 to 2026,
// and a checker that insisted on one shape would spend its findings on
// formatting rather than on the thing that is legally load-bearing. A file
// that has the clause has the header.
//
// GENERATED FILES ARE IN SCOPE. `buf generate` reproduces the header from the
// .proto, so they pass today; excluding them would mean a plugin change that
// dropped it would go unseen, which is exactly the case worth catching.
//
// WHY IT WALKS THE TREE rather than asking git: so the suite can point it at a
// planted directory, and so a file that is about to be committed is checked
// before it is staged rather than after.

import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { isDirectRun } from './direct-run.mjs';

const PREFIX = '[verify:license]';

/** The grant clause, as it appears in every headered file in the tree. */
export const LICENSE_CLAUSE = 'Licensed under the Apache License, Version 2.0';

/**
 * How far into a file the header may start. Generated files open with a few
 * lines of tool banner before theirs, and a package comment can precede it in
 * hand-written ones; 40 lines clears both without letting the clause hide in
 * the body of a file that does not really have a header.
 */
export const HEADER_SCAN_LINES = 40;

/**
 * Directories never descended into, at ANY depth. Neither holds first-party
 * source: `vendor` and `node_modules` are other people's code under their own
 * licences, and `.git` is not source at all. A nested one is as certain as a
 * root one, so depth does not matter.
 */
const SKIP_DIRS = new Set(['.git', 'node_modules', 'vendor']);

/**
 * Directories skipped ONLY at the tree root: build output, and the sibling
 * checkouts this repository's own workflows stage beside the tree —
 * `agent-review-panel.yml` unpacks `.trusted*`, `ci.yml`'s bench job checks
 * out `repo`, `benchmark-repo` and `load-repo`, and `.worktrees` is
 * gitignored for git-worktree isolation.
 *
 * ROOT-ONLY IS THE POINT. These are ordinary words. Skipping `repo` at any
 * depth would mean a future `pkg/repo/` silently leaving the licence gate —
 * a hole of exactly the kind this file exists to refuse, bought for nothing,
 * since every one of them is staged at the root or not at all.
 */
const SKIP_ROOT_DIRS = new Set([
  'bin',
  'binaries',
  '.worktrees',
  '.trusted',
  '.trusted-agent',
  '.trusted-cred',
  'repo',
  'benchmark-repo',
  'load-repo',
]);

/**
 * Every `.go` file under `root` as paths relative to it, sorted, plus the
 * directories that could not be listed.
 *
 * THE ERRORS ARE RETURNED, NOT SWALLOWED. An unreadable directory used to be
 * a bare `return`: its Go files were never examined, the remaining ones
 * passed, and the run reported success. That is the same silent pass this
 * file refuses at the top level, one directory down — and the narrower it is,
 * the more convincing the green.
 */
export function goFiles(root) {
  const files = [];
  const errors = [];
  const walk = (dir) => {
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch (err) {
      const rel = path.relative(root, dir) || '.';
      errors.push(`${rel} could not be listed (${err.code ?? err.message})`);
      return;
    }
    for (const entry of entries) {
      const abs = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (SKIP_DIRS.has(entry.name)) continue;
        if (dir === root && SKIP_ROOT_DIRS.has(entry.name)) continue;
        walk(abs);
      } else if (entry.isFile() && entry.name.endsWith('.go')) {
        files.push(path.relative(root, abs));
      }
    }
  };
  if (!safeIsDirectory(root)) return { files, errors };
  walk(root);
  files.sort();
  return { files, errors };
}

function safeIsDirectory(target) {
  try {
    return statSync(target).isDirectory();
  } catch {
    return false;
  }
}

/** True iff the file opens with the Apache grant clause. */
export function hasLicenseHeader(content) {
  return content
    .split('\n', HEADER_SCAN_LINES)
    .some((line) => line.includes(LICENSE_CLAUSE));
}

/**
 * One finding per Go file missing the header. Pure over a directory, so the
 * suite can plant a tree and never touch this repository.
 *
 * SCANNING NOTHING IS A FINDING, not a pass. A wrong root — this file moved
 * into `scripts/ci/`, say, so `..` lands on `scripts/` — walks a tree with no
 * Go in it, reports zero missing headers and exits 0. That is a green check
 * with no coverage, which is the failure this repository's own rule names: a
 * check that reports nothing is indistinguishable from a check that found
 * nothing. `verify-doc-links.mjs` guards the same way when its queue is empty.
 */
export function collectFindings(repoRoot) {
  const { files, errors } = goFiles(repoRoot);
  // An unreadable tree is reported as what it is. Falling through to "scanned
  // nothing" would be true but would name the wrong cause.
  if (files.length === 0 && errors.length === 0) {
    return [`no .go files found under ${repoRoot} — this check scanned nothing`];
  }
  const findings = [...errors];
  for (const rel of files) {
    let content;
    try {
      content = readFileSync(path.join(repoRoot, rel), 'utf8');
    } catch (err) {
      // A file the walk found and the read could not open is a gap in the
      // scan, not a file without a header. Say which.
      findings.push(`${rel} could not be read (${err.code ?? err.message})`);
      continue;
    }
    if (!hasLicenseHeader(content)) findings.push(`${rel} has no Apache 2.0 header`);
  }
  return findings;
}

if (isDirectRun(import.meta.url)) {
  const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const checked = goFiles(repoRoot);
  const findings = collectFindings(repoRoot);
  if (findings.length === 0) {
    // The count is in the success line on purpose: "every Go file" is true of
    // a tree with none, and the number is what makes a collapsed scan visible
    // in a log nobody reads closely.
    console.log(`${PREFIX} Every Go file (${checked.files.length}) carries the Apache 2.0 header.`);
  } else {
    for (const finding of findings) console.log(`${PREFIX}   ${finding}`);
    console.log(`${PREFIX} ${findings.length} file(s) missing the header.`);
    process.exit(1);
  }
}
