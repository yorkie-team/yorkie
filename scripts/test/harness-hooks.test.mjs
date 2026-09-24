// Guards for the local enforcement layer: the Claude Code hooks, their wiring
// in .claude/settings.json, and the workflow step that runs the licence gate.
//
// EVERY FAILURE THIS FILE CATCHES IS SILENT — none of them shows up as a red
// lane, which is the whole reason they are pinned here.
// `guard-generated-files.sh` fails OPEN by design, so a `case` pattern that
// stops matching does not error, it stops guarding. A renamed hook script
// makes its settings.json entry a no-op Claude Code does not announce.
// Dropping session-prime's CI exit changes only what a CI agent is told.
// Deleting the licence step from docs.yml, or filtering that workflow by
// path, leaves the gate resting on a local `make verify` the contributor may
// not be able to run.
//
// Unlike its sibling suites this one reads THIS repository rather than a
// planted tree — the facts being checked are about this tree, and a planted
// copy of them would assert only that the test file is self-consistent. It
// stays read-only: it runs the hook with a payload on stdin and reads files.

import { spawnSync } from 'node:child_process';
import {
  accessSync,
  chmodSync,
  constants,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

// The repository's own guard, not a hand-copied list of variable names —
// copying it is the drift this branch keeps finding. `fixtureGitEnv` strips
// every GIT_* that redirects a command AND pins GIT_DIR/GIT_WORK_TREE at the
// fixture, so no discovery happens at all. Its header records what an
// inherited GIT_INDEX_FILE once did here: a public PR whose diff appeared to
// delete every file in the repository.
import { fixtureGitEnv } from '../agent/git-env.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const GUARD = path.join(REPO, 'scripts', 'hooks', 'guard-generated-files.sh');

/** Run the guard hook with a PreToolUse payload; returns its exit code. */
function guard(filePath) {
  const r = spawnSync('bash', [GUARD], {
    input: JSON.stringify({ tool_input: { file_path: filePath } }),
    encoding: 'utf8',
  });
  return { status: r.status, stderr: r.stderr };
}

/** Every generated Go file actually in the tree, found without asking git. */
function generatedGoFiles() {
  const found = [];
  const walk = (dir) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const abs = path.join(dir, e.name);
      if (e.isDirectory()) walk(abs);
      else if (e.name.endsWith('.pb.go') || e.name.endsWith('.connect.go')) found.push(abs);
    }
  };
  walk(path.join(REPO, 'api'));
  return found;
}

test('the guard refuses every generated Go file present in the tree', () => {
  // DERIVED FROM THE TREE, NOT LISTED HERE. A hard-coded list would keep
  // passing the day `api/yorkie/v2/` appears, while the hook's `case` patterns
  // silently stopped covering it — and because the hook fails open, nothing
  // else would notice.
  const generated = generatedGoFiles();
  assert.ok(generated.length >= 7, `expected the generated set, found ${generated.length}`);
  for (const abs of generated) {
    assert.equal(guard(abs).status, 2, `guard allowed an edit to ${path.relative(REPO, abs)}`);
  }
});

test('the guard allows the files the release procedure hand-edits', () => {
  // MAINTAINING.md §1 has a maintainer bump `version` in each OpenAPI bundle
  // on every release. They are generated too, so the exclusion is easy to
  // "tidy up" into the patterns above; this is what would then break.
  const openapi = path.join(REPO, 'api', 'docs', 'yorkie', 'v1', 'yorkie.openapi.yaml');
  statSync(openapi); // the guard is meaningless if the path moved
  assert.equal(guard(openapi).status, 0);
});

test('the guard allows ordinary and source-of-truth files', () => {
  for (const rel of [
    'pkg/document/document.go',
    'server/server.go',
    'api/yorkie/v1/yorkie.proto',
  ]) {
    assert.equal(guard(path.join(REPO, rel)).status, 0, `guard refused ${rel}`);
  }
});

test('the guard fails open on an unusable payload', () => {
  // A guard that starts refusing everything is a worse outage than one that
  // stops guarding, so every unreadable input has to allow.
  for (const payload of ['', 'not json', '{}', '{"tool_input":{}}']) {
    const r = spawnSync('bash', [GUARD], { input: payload, encoding: 'utf8' });
    assert.equal(r.status, 0, `guard blocked on payload ${JSON.stringify(payload)}`);
  }
});

test('every hook .claude/settings.json names exists and is executable', () => {
  // A rename makes the entry a no-op, and Claude Code does not announce it.
  const settings = JSON.parse(
    readFileSync(path.join(REPO, '.claude', 'settings.json'), 'utf8'),
  );
  const commands = Object.values(settings.hooks ?? {})
    .flat()
    .flatMap((m) => m.hooks ?? [])
    .map((h) => h.command);

  assert.ok(commands.length >= 2, `expected the hooks to be wired, found ${commands.length}`);
  for (const command of commands) {
    const script = command.replace(/^bash\s+/, '').trim();
    const abs = path.join(REPO, script);
    statSync(abs); // throws with the path if it moved
    accessSync(abs, constants.X_OK);
  }
});

test('session-prime says nothing in CI and speaks locally', () => {
  // The guidance it prints is a local multi-commit workflow — plan a task doc,
  // self-review, archive before merge. A CI fix job is told to fix the
  // findings it was handed "and nothing else", and whether
  // `claude-code-action` loads a branch's `.claude/` is unsettled: three
  // workflows delete the directory on the assumption that it does. Refusing
  // under GITHUB_ACTIONS settles it either way, and is easy to drop by
  // accident because nothing fails when it goes.
  const prime = path.join(REPO, 'scripts', 'hooks', 'session-prime.sh');

  const inCi = spawnSync('bash', [prime], {
    encoding: 'utf8',
    env: { ...process.env, GITHUB_ACTIONS: 'true' },
  });
  assert.equal(inCi.status, 0);
  assert.equal(inCi.stdout.trim(), '', 'session-prime must stay silent in CI');

  const local = spawnSync('bash', [prime], {
    encoding: 'utf8',
    env: { ...process.env, GITHUB_ACTIONS: '' },
  });
  assert.equal(local.status, 0);
  assert.match(local.stdout, /WORKFLOW REQUIREMENTS/);
});

test('docs.yml still runs both checks, on every pull request', () => {
  // The licence gate has two homes and neither alone is sufficient: `make
  // verify` needs Node locally, and this workflow is the only one that runs on
  // every PR with no path filter.
  const wf = readFileSync(path.join(REPO, '.github', 'workflows', 'docs.yml'), 'utf8');
  assert.match(wf, /node scripts\/verify-license\.mjs/);
  assert.match(wf, /node scripts\/verify-doc-links\.mjs/);
  // BOTH SPELLINGS. `paths-ignore:` holes the coverage exactly as badly as
  // `paths:`, and the narrower pattern would have missed it.
  assert.doesNotMatch(wf, /^\s*paths(-ignore)?:/m, 'docs.yml must stay unfiltered');
});

test('make verify reaches the licence gate', () => {
  // `verify` is what the git hooks call, so a licence check dropped from its
  // prerequisites is a gate that only CI still holds.
  const mk = readFileSync(path.join(REPO, 'Makefile'), 'utf8');
  const target = mk.split('\n').find((l) => l.startsWith('verify:'));
  assert.ok(target, 'the Makefile has no verify target');
  assert.match(target, /\bverify-license\b/);
  assert.match(mk, /^verify-license:/m, 'verify names a target that does not exist');
});

/**
 * Run `pre-commit` against a throwaway repository.
 *
 * GIT IS ALWAYS ADDRESSED WITH `-C dir`, never through the working directory.
 * The sibling suite's header records why: a suite elsewhere in this
 * organization ran `git init`/`commit`/`checkout` against the CWD, and under a
 * `git worktree` it rewrote that checkout's HEAD and moved two branch refs.
 * Nothing below can see this repository.
 */
function inScratchRepo(body) {
  const dir = mkdtempSync(path.join(tmpdir(), 'pre-commit-'));
  const git = (...args) =>
    spawnSync('git', ['-C', dir, ...args], {
      encoding: 'utf8',
      env: fixtureGitEnv(dir),
    });
  try {
    git('init', '-q', '.');
    git('config', 'user.email', 'test@example.com');
    git('config', 'user.name', 'test');
    return body({ dir, git });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

/** The hook's decision alone: false = "nothing to lint", true = "there is Go". */
function wouldLint({ dir }) {
  // TWO SUBSTITUTIONS, AND THE SECOND IS THE ONE THIS TEST LEARNED THE HARD
  // WAY. Replacing `exec make lint` with a marker is obvious — a scratch repo
  // has no Makefile. But the hook ALSO refuses when golangci-lint is missing,
  // and the `Docs` workflow that runs this suite is a Node-only job with no Go
  // toolchain. Probing only the tail therefore measured "could lint" rather
  // than "decided to lint": the first version passed on a developer machine
  // and failed on the runner, for a reason that had nothing to do with the
  // decision under test.
  //
  // A stub on PATH rather than deleting the check, so the hook runs exactly as
  // written and the guard still covers the refusal path's placement.
  const bin = path.join(dir, '.probe-bin');
  mkdirSync(bin, { recursive: true });
  const stub = path.join(bin, 'golangci-lint');
  writeFileSync(stub, '#!/usr/bin/env bash\nexit 0\n');
  chmodSync(stub, 0o755);

  const src = readFileSync(path.join(REPO, '.githooks', 'pre-commit'), 'utf8')
    .replace(/^exec make lint$/m, 'echo WOULD_LINT');
  const probe = path.join(dir, '.probe-pre-commit');
  writeFileSync(probe, src);

  const r = spawnSync('bash', [probe], {
    cwd: dir,
    encoding: 'utf8',
    // The hook itself runs `git diff --cached`; `git -C` above protects the
    // helper, but nothing protected the hook until this.
    env: fixtureGitEnv(dir, {
      ...process.env,
      PATH: `${bin}${path.delimiter}${process.env.PATH}`,
    }),
  });
  return r.stdout.includes('WOULD_LINT');
}

test('pre-commit refuses when Go is staged and the linter is missing', () => {
  // The other half of the same decision, and the reason the stub above is a
  // stub rather than a deletion: "nothing to lint" and "no linter" must keep
  // producing different answers.
  inScratchRepo(({ dir, git }) => {
    writeFileSync(path.join(dir, 'a.go'), 'package a\n');
    git('add', 'a.go');
    const src = readFileSync(path.join(REPO, '.githooks', 'pre-commit'), 'utf8')
      .replace(/^exec make lint$/m, 'echo WOULD_LINT');
    const probe = path.join(dir, '.probe-pre-commit');
    writeFileSync(probe, src);
    const r = spawnSync('bash', [probe], {
      cwd: dir,
      encoding: 'utf8',
      env: fixtureGitEnv(dir, { ...process.env, PATH: '/usr/bin:/bin' }),
    });
    assert.equal(r.status, 1, 'a missing linter with Go staged must refuse');
    assert.match(r.stderr, /golangci-lint not found/);
  });
});

test('pre-commit lints whenever a commit stages Go, however it stages it', () => {
  // THE REGRESSION: `--diff-filter=ACM` dropped `R`, and git reports a
  // rename-with-edit as a single `R` entry above ~50% similarity. Such a
  // commit stages Go and the hook skipped the lint entirely — the same silent
  // pass this branch exists to close, inside the gate that closes it.
  inScratchRepo(({ dir, git }) => {
    writeFileSync(path.join(dir, 'a.go'), 'package a\nfunc A() {}\n');
    git('add', 'a.go');
    git('commit', '-qm', 'init', '--no-verify');

    // Rename with an edit: reported as R, not as A+D.
    git('mv', 'a.go', 'b.go');
    writeFileSync(path.join(dir, 'b.go'), 'package a\nfunc A() {}\nfunc B() {}\n');
    git('add', 'b.go');
    assert.match(git('diff', '--cached', '--name-status').stdout, /^R/);
    assert.equal(wouldLint({ dir }), true, 'a rename-with-edit must still lint');
  });
});

test('pre-commit lints a staged Go deletion', () => {
  // A deletion cannot introduce a lint violation in its own text, but it
  // breaks compilation for every file that referenced it — which golangci-lint
  // reports. `--diff-filter=ACMR` would have skipped this one.
  inScratchRepo(({ dir, git }) => {
    writeFileSync(path.join(dir, 'a.go'), 'package a\nfunc A() {}\n');
    git('add', 'a.go');
    git('commit', '-qm', 'init', '--no-verify');

    git('rm', '-q', 'a.go');
    assert.equal(wouldLint({ dir }), true, 'a staged deletion must still lint');
  });
});

test('pre-commit skips a commit that stages no Go at all', () => {
  // The other half: an outside contributor fixing a typo must not need the Go
  // toolchain to commit.
  inScratchRepo(({ dir, git }) => {
    writeFileSync(path.join(dir, 'README.md'), '# docs\n');
    git('add', 'README.md');
    assert.equal(wouldLint({ dir }), false, 'a docs-only commit must not lint');
  });
});
