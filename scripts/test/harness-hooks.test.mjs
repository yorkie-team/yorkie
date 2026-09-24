// Guards for the local enforcement layer: the Claude Code hooks, the installer
// that wires them per clone, and the workflow step that runs the licence gate.
//
// EVERY FAILURE THIS FILE CATCHES IS SILENT — none of them shows up as a red
// lane, which is the whole reason they are pinned here.
// `guard-generated-files.sh` fails OPEN by design, so a `case` pattern that
// stops matching does not error, it stops guarding. A renamed hook script
// makes its wiring entry a no-op Claude Code does not announce. Re-tracking
// the wiring in the working tree hands a pull-request branch arbitrary local
// execution and breaks nothing visible.
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
  existsSync,
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
import { fixtureGitEnv, repoScopedEnv } from '../agent/git-env.mjs';
import { HOOK_WIRING, shellQuote, wireHooks } from '../hooks/install.mjs';

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

test('every hook the installer wires exists and is executable', () => {
  // A rename makes the entry a no-op, and Claude Code does not announce it.
  assert.ok(HOOK_WIRING.length >= 2, `expected the hooks to be wired, found ${HOOK_WIRING.length}`);
  for (const { script } of HOOK_WIRING) {
    const abs = path.join(REPO, 'scripts', 'hooks', script);
    statSync(abs); // throws with the path if it moved
    accessSync(abs, constants.X_OK);
  }
});

test('the hook wiring is never tracked in the working tree', () => {
  // THE SECURITY PROPERTY, pinned because nothing else fails when it goes.
  // Claude Code runs the commands a project settings file names, with no
  // confirmation, at SessionStart and before every Edit/Write. A tracked
  // `.claude/settings.json` therefore means checking out a pull-request branch
  // executes that branch's `scripts/hooks/*.sh` — and both halves are ordinary
  // tracked files any contributor can rewrite. The wiring lives in the
  // gitignored `settings.local.json` instead, written by `install.mjs`.
  // ASKS GIT WHAT IS TRACKED, because `.gitignore` is not a security control:
  // `git add -f .claude/settings.local.json` commits it regardless, and a
  // branch that does so ships hook wiring that runs the moment a reviewer
  // checks it out — the exact attack install.mjs closed, one filename over.
  // Checking `existsSync` cannot tell the two apart either, since that file
  // legitimately exists on any clone where setup.sh has been run.
  const tracked = spawnSync('git', ['-C', REPO, 'ls-files', '--', '.claude/'], {
    encoding: 'utf8',
    env: repoScopedEnv(REPO),
  });
  assert.equal(tracked.status, 0, `git ls-files failed: ${tracked.stderr}`);
  const settingsFiles = tracked.stdout
    .split('\n')
    .filter(Boolean)
    .filter((f) => /^\.claude\/settings(\.[\w-]+)?\.json$/.test(f));
  assert.deepEqual(
    settingsFiles,
    [],
    'no .claude/settings*.json may be tracked — Claude Code executes what it names, ' +
      'straight out of a branch checkout. See scripts/hooks/install.mjs.',
  );

  // The ignore entry is still worth pinning: it is what stops the file being
  // committed by accident, which is the common case. It is not what stops it
  // being committed on purpose — the assertion above is.
  const ignore = readFileSync(path.join(REPO, '.gitignore'), 'utf8');
  assert.match(ignore, /^\.claude\/settings\.local\.json$/m);
});

test('the installer wires the snapshot and keeps everything else', () => {
  // Two failures this catches, both silent. Re-running setup must REPLACE the
  // previous wiring rather than stack a second copy of every hook; and the
  // file it merges into is where a contributor's `permissions.allow` grants
  // live, so anything the installer does not own has to survive untouched.
  const snapshot = '/tmp/clone/.git/agent-hooks';
  const existing = {
    permissions: { allow: ['Bash(make lint)'] },
    hooks: {
      SessionStart: [{ matcher: '', hooks: [{ type: 'command', command: 'bash scripts/hooks/session-prime.sh' }] }],
      PreToolUse: [{ matcher: 'Bash', hooks: [{ type: 'command', command: 'echo mine' }] }],
    },
  };

  const once = wireHooks(existing, snapshot);
  assert.deepEqual(once.permissions, existing.permissions, 'unrelated settings must survive');

  const commands = Object.values(once.hooks)
    .flat()
    .flatMap((g) => g.hooks)
    .map((h) => h.command);
  // The stale in-tree wiring is migrated, the unrelated Bash hook is kept.
  assert.ok(commands.includes('echo mine'), "another tool's hook must survive");
  assert.equal(commands.filter((c) => c.includes('scripts/hooks/')).length, 0, 'in-tree wiring must be replaced');
  for (const { script } of HOOK_WIRING) {
    assert.ok(
      commands.includes(`bash ${shellQuote(`${snapshot}/${script}`)}`),
      `${script} must be wired to the snapshot`,
    );
  }

  assert.deepEqual(wireHooks(once, snapshot), once, 'a second install must be a no-op');
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

test('a clone path with a space stays one argument, and stays idempotent', () => {
  // THE BUG THIS PINS. The command is handed to a shell. Unquoted, a clone
  // under `~/My Projects/` becomes two words, the hook never starts, and the
  // guard is silently gone — the failure this whole change exists to refuse.
  //
  // Idempotency is half the test: `isOurs` recognises previous wiring by
  // matching the path, and quoting changes the character that follows `.sh`.
  // Miss that and every re-run of setup.sh stacks another copy of every hook.
  const snapshot = '/Users/someone/My Projects/repo/.git/agent-hooks';
  const once = wireHooks({}, snapshot);
  const commands = Object.values(once.hooks)
    .flat()
    .flatMap((g) => g.hooks)
    .map((h) => h.command);

  for (const c of commands) {
    assert.match(c, /^bash '\/Users\/someone\/My Projects\/.*\.sh'$/, `unquoted: ${c}`);
    // Parsed by a real shell, the path must arrive as ONE argument.
    const script = c.slice('bash '.length);
    const argc = spawnSync('bash', ['-c', `set -- ${script}; echo $#`], { encoding: 'utf8' });
    assert.equal(argc.stdout.trim(), '1', `the shell split the path: ${c}`);
  }

  assert.deepEqual(wireHooks(once, snapshot), once, 'a second install must be a no-op');
});

test('a clone path with shell metacharacters cannot inject', () => {
  const snapshot = "/tmp/repo$(touch /tmp/pwned-by-hook-wiring)/.git/agent-hooks";
  const once = wireHooks({}, snapshot);
  const command = Object.values(once.hooks).flat().flatMap((g) => g.hooks)[0].command;
  const script = command.slice('bash '.length);
  // Single quotes make the substitution inert; echo it rather than run it.
  const out = spawnSync('bash', ['-c', `set -- ${script}; printf '%s' "$1"`], {
    encoding: 'utf8',
  });
  assert.match(out.stdout, /\$\(touch/, 'the metacharacters must survive as literal text');
  assert.equal(existsSync('/tmp/pwned-by-hook-wiring'), false);
});

test('setup.sh installs git hooks from a snapshot, not from the worktree', () => {
  // THE PROPERTY, and it is the same one install.mjs exists for. Pointing
  // `core.hooksPath` at the tracked `.githooks/` makes every hook
  // branch-controlled: a pull request rewrites `pre-commit`, a reviewer checks
  // the branch out and commits, and it runs — reaching the branch's Makefile
  // and Go test code through `make lint` / `make verify`. Closing that for the
  // Claude hooks and leaving it open for the git hooks would be two threat
  // models in one change.
  const setup = readFileSync(path.join(REPO, 'scripts', 'setup.sh'), 'utf8');

  assert.match(setup, /rev-parse --absolute-git-dir/, 'setup.sh must resolve $GIT_DIR');
  // THE COMMAND, not the comment. The paragraph above it explains the change
  // by quoting the old `core.hooksPath ... .githooks` form, so a naive `find`
  // on the setting name reads the argument for the fix as the fix.
  const hooksPath = setup
    .split('\n')
    .map((l) => l.trim())
    .find((l) => l.startsWith('git config core.hooksPath'));
  assert.ok(hooksPath, 'setup.sh no longer configures core.hooksPath');
  assert.doesNotMatch(
    hooksPath,
    /REPO_ROOT|\.githooks"?$/,
    `core.hooksPath must name the $GIT_DIR snapshot, not the worktree: ${hooksPath}`,
  );
  assert.match(hooksPath, /HOOKS_SNAPSHOT/);
});
