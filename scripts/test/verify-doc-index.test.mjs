// Tests for the documentation coverage gate and the task-staleness notice.
//
// Everything here runs against a PLANTED TREE under the OS temp directory,
// created per test and removed after, for the reason `verify-doc-links.test.mjs`
// records: a suite that can reach the checkout it is run from is worse than no
// suite. Nothing below shells out at all.
//
// THE TWO CHECKS ARE TESTED THROUGH DIFFERENT ENTRY POINTS on purpose. If a
// staleness notice could reach `collectFindings`, the gate would start
// refusing the last commit of every task — so the separation is asserted, not
// assumed.

import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';

import {
  ACTIVE_TASKS,
  collectFindings,
  collectStaleTasks,
  designDocs,
  hasUncheckedBox,
  mentions,
  scriptEntries,
} from '../verify-doc-index.mjs';

/**
 * Build a throwaway repository from a {path: contents} map and hand its root
 * to `body`. A path ending in `/` is planted as an empty directory, which the
 * `scripts/` area needs: a directory entry is a thing to be indexed.
 */
function withTree(files, body) {
  const root = mkdtempSync(path.join(tmpdir(), 'doc-index-'));
  try {
    for (const [rel, contents] of Object.entries(files)) {
      const abs = path.join(root, rel);
      if (rel.endsWith('/')) {
        mkdirSync(abs, { recursive: true });
        continue;
      }
      mkdirSync(path.dirname(abs), { recursive: true });
      writeFileSync(abs, contents);
    }
    return body(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

// --- CHECK 1: design documents ---------------------------------------------

test('a design document the index links is covered', () => {
  withTree(
    {
      'docs/design/README.md': '- [Tree](tree.md): the tree CRDT',
      'docs/design/tree.md': '# Tree',
      'scripts/README.md': 'nothing here',
    },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('a design document nothing links is reported, naming the file and the index', () => {
  withTree(
    {
      'docs/design/README.md': '- [Tree](tree.md)',
      'docs/design/tree.md': '# Tree',
      'docs/design/orphan.md': '# Nobody sent you',
      'scripts/README.md': 'nothing here',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /^docs\/design\/orphan\.md is not linked from docs\/design\/README\.md$/);
    },
  );
});

test('a design document named only inside a fence is not indexed', () => {
  // THE POINT OF REUSING linkTargets. A README that shows `[x](orphan.md)` as
  // an EXAMPLE of a row has not introduced the document — nobody can click it.
  withTree(
    {
      'docs/design/README.md': 'Add a row like this:\n\n```md\n- [Orphan](orphan.md)\n```\n',
      'docs/design/orphan.md': '# Orphan',
      'scripts/README.md': 'nothing here',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /orphan\.md/);
    },
  );
});

test('the index does not have to index itself', () => {
  withTree(
    { 'docs/design/README.md': 'no rows yet', 'scripts/README.md': 'x' },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('links out of the design directory are not mistaken for coverage', () => {
  // The real README links `media/*.png` and `../../CONTRIBUTING.md`. Resolving
  // those to a basename would make `docs/design/CONTRIBUTING.md` "indexed".
  withTree(
    {
      'docs/design/README.md': '[flow](../../CONTRIBUTING.md) ![chart](media/tree.png)',
      'docs/design/tree.png': 'not the chart',
      'docs/design/tree.md': '# Tree',
      'scripts/README.md': 'x',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /docs\/design\/tree\.md/);
    },
  );
});

test('a missing design index is a finding, not a silent skip', () => {
  withTree(
    { 'docs/design/tree.md': '# Tree', 'scripts/README.md': 'x' },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /docs\/design\/README\.md is missing/);
    },
  );
});

// --- CHECK 1: scripts entries ----------------------------------------------

test('a script the README names in inline code is covered', () => {
  // Backticks, not a link: this is how every row in the real README is
  // written, and running the link extractor over it would see nothing.
  withTree(
    {
      'scripts/README.md': 'Run `setup.sh` once per clone.',
      'scripts/setup.sh': '#!/bin/sh\n',
    },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('a script nothing mentions is reported', () => {
  withTree(
    {
      'scripts/README.md': 'Run `setup.sh` once per clone.',
      'scripts/setup.sh': '#!/bin/sh\n',
      'scripts/undocumented.mjs': '// hello',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /^scripts\/undocumented\.mjs is not mentioned in scripts\/README\.md$/);
    },
  );
});

test('a directory is reported by name with a slash', () => {
  withTree({ 'scripts/README.md': 'nothing', 'scripts/agent/': '' }, (root) => {
    const findings = collectFindings(root);
    assert.equal(findings.length, 1);
    assert.match(findings[0], /^scripts\/agent\/ is not mentioned/);
  });
});

test('a hyphenated neighbour does not count as mentioning a directory', () => {
  // THE FINDING THIS GATE ACTUALLY MADE. `scripts/README.md` names
  // `$GIT_DIR/agent-hooks/` and `agent-review-panel.yml`, so a substring
  // search for `agent` passes on a README that never mentions `scripts/agent/`.
  withTree(
    {
      'scripts/README.md': 'Snapshotted into `$GIT_DIR/agent-hooks/` by `agent-review-panel.yml`.',
      'scripts/agent/': '',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /scripts\/agent\//);
    },
  );
});

test('a path-spelled mention counts, because it is one', () => {
  withTree({ 'scripts/README.md': 'See `scripts/agent/` for the pipeline.', 'scripts/agent/': '' }, (root) =>
    assert.deepEqual(collectFindings(root), []),
  );
});

test('dot entries are tool state, not something to index', () => {
  withTree({ 'scripts/README.md': 'nothing', 'scripts/.cache/': '' }, (root) =>
    assert.deepEqual(collectFindings(root), []),
  );
});

test('a missing scripts index is a finding, not a silent skip', () => {
  withTree({ 'scripts/setup.sh': '#!/bin/sh\n' }, (root) => {
    const findings = collectFindings(root);
    assert.equal(findings.length, 1);
    assert.match(findings[0], /scripts\/README\.md is missing/);
  });
});

test('both areas report, so one clean area cannot cover for the other', () => {
  withTree(
    {
      'docs/design/README.md': 'no rows',
      'docs/design/orphan.md': '# Orphan',
      'scripts/README.md': 'no rows',
      'scripts/undocumented.mjs': '// hello',
    },
    (root) => assert.equal(collectFindings(root).length, 2),
  );
});

test('a tree with no indexed area says so rather than passing silently', () => {
  // A wrong root — this file moved one directory down, say — walks a tree with
  // neither area in it, finds nothing and exits 0. That is a green check with
  // no coverage, which is the failure the sibling gates both refuse.
  withTree({ 'README.md': '# Something else' }, (root) => {
    const findings = collectFindings(root);
    assert.equal(findings.length, 1);
    assert.match(findings[0], /scanned nothing/);
  });
});

test('mentions anchors on the left only', () => {
  assert.equal(mentions('see `hooks/`', 'hooks/'), true);
  assert.equal(mentions('see `hooks/install.mjs`', 'hooks/'), true);
  assert.equal(mentions('see `.githooks/`', 'hooks/'), false);
  assert.equal(mentions('see `agent-hooks/`', 'hooks/'), false);
  assert.equal(mentions('see `scripts/hooks/`', 'hooks/'), true);
  assert.equal(mentions('see `tasks-index.sh`', 'index.sh'), false);
});

test('the entry listers exclude the index and sort', () => {
  withTree(
    {
      'docs/design/README.md': '',
      'docs/design/b.md': '',
      'docs/design/a.md': '',
      'docs/design/notes.txt': '',
      'scripts/README.md': '',
      'scripts/z.sh': '',
      'scripts/a/': '',
    },
    (root) => {
      assert.deepEqual(designDocs(root), ['a.md', 'b.md']);
      assert.deepEqual(scriptEntries(root), ['a/', 'z.sh']);
    },
  );
});

// --- CHECK 2: staleness -----------------------------------------------------

test('a todo with an unchecked box is still in progress', () => {
  withTree(
    { [`${ACTIVE_TASKS}/20260101-x-todo.md`]: '**Created**: 2026-01-01\n\n- [x] one\n- [ ] two\n' },
    (root) => assert.deepEqual(collectStaleTasks(root), []),
  );
});

test('a todo with every box checked is a finished task nobody archived', () => {
  withTree(
    { [`${ACTIVE_TASKS}/20260101-x-todo.md`]: '**Created**: 2026-01-01\n\n- [x] one\n- [x] two\n' },
    (root) => {
      const notices = collectStaleTasks(root);
      assert.equal(notices.length, 1);
      assert.match(notices[0], /20260101-x-todo\.md has every box checked$/);
    },
  );
});

test('a finished todo with no Created line is named as the double problem it is', () => {
  // `tasks-archive.sh` warns and leaves such a todo behind, so it would drift
  // even after somebody ran the archiver.
  withTree({ [`${ACTIVE_TASKS}/20260101-x-todo.md`]: '# x\n\n- [x] one\n' }, (root) => {
    const notices = collectStaleTasks(root);
    assert.equal(notices.length, 1);
    assert.match(notices[0], /no \*\*Created\*\* line/);
  });
});

test('only todos are examined, not their lessons files', () => {
  withTree(
    {
      [`${ACTIVE_TASKS}/20260101-x-todo.md`]: '**Created**: 2026-01-01\n\n- [ ] one\n',
      [`${ACTIVE_TASKS}/20260101-x-lessons.md`]: 'Nothing to check here.\n',
      [`${ACTIVE_TASKS}/README.md`]: 'Hand-written prose.\n',
    },
    (root) => assert.deepEqual(collectStaleTasks(root), []),
  );
});

test('a tree with no active directory reports nothing', () => {
  withTree({ 'scripts/README.md': 'x' }, (root) => assert.deepEqual(collectStaleTasks(root), []));
});

test('a stale task never reaches the gate', () => {
  // THE SEPARATION, asserted. `CLAUDE.md` archives a task pair before merge,
  // after CI is green, so a fully-checked todo in `active/` is the normal state
  // of the pull request that finished it. If this leaked into collectFindings,
  // the gate would refuse the last commit of every task.
  withTree(
    {
      'scripts/README.md': 'x',
      [`${ACTIVE_TASKS}/20260101-x-todo.md`]: '**Created**: 2026-01-01\n\n- [x] done\n',
    },
    (root) => {
      assert.equal(collectStaleTasks(root).length, 1);
      assert.deepEqual(collectFindings(root), []);
    },
  );
});

test('hasUncheckedBox agrees with tasks-archive.sh, quoted syntax and all', () => {
  assert.equal(hasUncheckedBox('- [ ] a'), true);
  assert.equal(hasUncheckedBox('  - [ ] nested'), true);
  assert.equal(hasUncheckedBox('- [x] a'), false);
  // The shared blind spot, pinned so it is a decision rather than a surprise:
  // the archiver's grep is unanchored too, so a todo that quotes the syntax in
  // prose reads as unfinished to both. Diverging here would produce notices
  // naming files the archiver refuses to move.
  assert.equal(hasUncheckedBox('a todo with no `- [ ]` left is finished'), true);
});
