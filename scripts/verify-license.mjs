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
// nothing checked it: `docs/design/agent-command-verbs.md` §4b lists the
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
 * Directories never descended into. Neither holds first-party source: `vendor`
 * and `node_modules` are other people's code under their own licences, and the
 * rest are build output.
 */
const SKIP_DIRS = new Set(['.git', 'node_modules', 'vendor', 'bin', 'binaries']);

/** Every `.go` file under `root`, as paths relative to it, sorted. */
export function goFiles(root) {
  const found = [];
  const walk = (dir) => {
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      const abs = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (SKIP_DIRS.has(entry.name)) continue;
        walk(abs);
      } else if (entry.isFile() && entry.name.endsWith('.go')) {
        found.push(path.relative(root, abs));
      }
    }
  };
  if (!safeIsDirectory(root)) return found;
  walk(root);
  return found.sort();
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
 */
export function collectFindings(repoRoot) {
  const findings = [];
  for (const rel of goFiles(repoRoot)) {
    let content;
    try {
      content = readFileSync(path.join(repoRoot, rel), 'utf8');
    } catch {
      continue;
    }
    if (!hasLicenseHeader(content)) findings.push(`${rel} has no Apache 2.0 header`);
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
    console.log(`${PREFIX} Every Go file carries the Apache 2.0 header.`);
  } else {
    for (const finding of findings) console.log(`${PREFIX}   ${finding}`);
    console.log(`${PREFIX} ${findings.length} file(s) missing the header.`);
    process.exit(1);
  }
}
