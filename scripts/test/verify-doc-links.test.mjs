// Tests for the documentation link gate.
//
// Everything here runs against a PLANTED TREE under the OS temp directory,
// created per test and removed after. Nothing touches the repository, and no
// test shells out to git. That is not incidental: the sibling suite in another
// repository in this organization performs `git init` / `commit` / `checkout`
// against the CURRENT WORKING DIRECTORY, and running it from a `git worktree`
// rewrote that checkout's HEAD and moved two branch refs onto planted commits.
// A test that can destroy the tree it is run from is worse than no test.

import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';

import { collectFindings, linkTargets } from '../verify-doc-links.mjs';

/**
 * Build a throwaway repository from a {path: contents} map and hand its root
 * to `body`. Removed afterwards even if the assertion throws.
 */
function withTree(files, body) {
  const root = mkdtempSync(path.join(tmpdir(), 'doc-links-'));
  try {
    for (const [rel, contents] of Object.entries(files)) {
      const abs = path.join(root, rel);
      mkdirSync(path.dirname(abs), { recursive: true });
      writeFileSync(abs, contents);
    }
    return body(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test('a tree whose links all resolve has no findings', () => {
  withTree(
    {
      'CLAUDE.md': 'See [the design](docs/design.md).',
      'docs/design.md': 'Back to [the root](../CLAUDE.md).',
    },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('a link to a missing file is reported, naming the source and the target', () => {
  withTree({ 'CLAUDE.md': 'See [the plan](docs/gone.md).' }, (root) => {
    const findings = collectFindings(root);
    assert.equal(findings.length, 1);
    assert.match(findings[0], /^CLAUDE\.md links docs\/gone\.md/);
  });
});

test('the walk is transitive: a dead link two documents deep is still found', () => {
  withTree(
    {
      'CLAUDE.md': '[one](a.md)',
      'a.md': '[two](b.md)',
      'b.md': '[three](nowhere.md)',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /^b\.md links nowhere\.md/);
    },
  );
});

test('a link inside a fenced block is an example, not a claim', () => {
  withTree(
    { 'CLAUDE.md': 'Write it like this:\n\n```md\n[x](does-not-exist.md)\n```\n' },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('a fence opened with an info string does not close on its own opener', () => {
  withTree(
    { 'CLAUDE.md': '```js\n// [x](missing-a.md)\n[y](missing-b.md)\n```\n' },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('a link inside inline code is a literal string nobody can click', () => {
  withTree(
    { 'CLAUDE.md': 'A typo row looks like `[board](board/TYPO.md)` in the index.' },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('a commented-out row is invisible to the reader and to the gate', () => {
  withTree(
    { 'CLAUDE.md': '<!-- [old](removed.md) -->\nNothing to see.' },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('an extensionless target resolves through .md, as a docs site serves it', () => {
  withTree(
    {
      'CLAUDE.md': '[guide](docs/guide)',
      'docs/guide.md': 'Here.',
    },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('an extensionless target also resolves through a directory index', () => {
  withTree(
    {
      'CLAUDE.md': '[section](docs/section)',
      'docs/section/index.md': 'Here.',
    },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('an extensionless target that resolves to nothing is still reported', () => {
  withTree({ 'CLAUDE.md': '[guide](docs/guide)' }, (root) => {
    const findings = collectFindings(root);
    assert.equal(findings.length, 1);
    assert.match(findings[0], /docs\/guide/);
  });
});

test('a directory link is satisfied by the directory, and the walk continues through its README', () => {
  withTree(
    {
      'CLAUDE.md': '[scripts](scripts/)',
      'scripts/README.md': '[helper](helper.md)',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /^scripts\/README\.md links helper\.md/);
    },
  );
});

test('a site-absolute target is skipped: its root is not this repository', () => {
  withTree({ 'CLAUDE.md': '[page](/sheets/formulas)' }, (root) =>
    assert.deepEqual(collectFindings(root), []),
  );
});

test('external schemes are not paths', () => {
  withTree(
    { 'CLAUDE.md': '[site](https://example.com/x.md) [mail](mailto:a@b.c)' },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('a target that escapes the repository root is out of scope', () => {
  withTree({ 'CLAUDE.md': '[outside](../elsewhere.md)' }, (root) =>
    assert.deepEqual(collectFindings(root), []),
  );
});

test('an archived task record is reached but never walked', () => {
  withTree(
    {
      'CLAUDE.md': '[tasks](docs/tasks/archive/README.md)',
      'docs/tasks/archive/README.md': '[a finished task](2026/08/done-todo.md)',
      'docs/tasks/archive/2026/08/done-todo.md': 'Cited [a design](../../../../design/gone.md) that has since moved.',
    },
    (root) => assert.deepEqual(collectFindings(root), []),
  );
});

test('the archive index itself is walked, so a row pointing at nothing is caught', () => {
  withTree(
    {
      'CLAUDE.md': '[tasks](docs/tasks/archive/README.md)',
      'docs/tasks/archive/README.md': '[a finished task](2026/08/missing-todo.md)',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /archive\/README\.md links 2026\/08\/missing-todo\.md/);
    },
  );
});

test('an active task record is walked — only the archive is frozen', () => {
  withTree(
    {
      'CLAUDE.md': '[current](docs/tasks/active/now-todo.md)',
      'docs/tasks/active/now-todo.md': '[spec](../../design/gone.md)',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /^docs\/tasks\/active\/now-todo\.md links/);
    },
  );
});

test('an @path import is followed, which is how the root documents pull others in', () => {
  withTree(
    {
      'CLAUDE.md': 'See @docs/rules.md for the rules.',
      'docs/rules.md': '[detail](detail.md)',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /^docs\/rules\.md links detail\.md/);
    },
  );
});

test('a missing image is as broken as a missing document', () => {
  withTree({ 'README.md': '![a chart](docs/media/chart.png)' }, (root) => {
    const findings = collectFindings(root);
    assert.equal(findings.length, 1);
    assert.match(findings[0], /docs\/media\/chart\.png/);
  });
});

test('every root document seeds the walk, not just the first one present', () => {
  withTree(
    {
      'CLAUDE.md': 'Nothing here.',
      'README.md': '[gone](gone.md)',
    },
    (root) => {
      const findings = collectFindings(root);
      assert.equal(findings.length, 1);
      assert.match(findings[0], /^README\.md links gone\.md/);
    },
  );
});

test('a tree with no root document says so rather than passing silently', () => {
  withTree({ 'docs/orphan.md': '[gone](gone.md)' }, (root) => {
    const findings = collectFindings(root);
    assert.equal(findings.length, 1);
    assert.match(findings[0], /no root document/);
  });
});

test('a cycle terminates', () => {
  withTree({ 'CLAUDE.md': '[b](b.md)', 'b.md': '[a](CLAUDE.md)' }, (root) =>
    assert.deepEqual(collectFindings(root), []),
  );
});

test('linkTargets keeps path claims and drops prose that merely looks like a link', () => {
  const targets = linkTargets(
    '[a](./rel) [b](dir/file.md) [c](file.md) [d](userId) [e](x) [f](#anchor)',
  );
  assert.deepEqual([...targets].sort(), ['./rel', 'dir/file.md', 'file.md']);
});
