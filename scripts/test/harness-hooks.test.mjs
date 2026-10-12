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
  realpathSync,
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

test('docs.yml still runs the doc checks, on every pull request', () => {
  const wf = readFileSync(path.join(REPO, '.github', 'workflows', 'docs.yml'), 'utf8');
  assert.match(wf, /node scripts\/verify-doc-links\.mjs/);
  // BOTH DIRECTIONS. The coverage gate has no other home — it is not in `make
  // verify` and ci.yml ignores `**/*.md` — so dropping this step leaves
  // nothing watching whether a new design document was ever indexed.
  assert.match(wf, /node scripts\/verify-doc-index\.mjs/);
  // BOTH SPELLINGS. `paths-ignore:` holes the coverage exactly as badly as
  // `paths:`, and the narrower pattern would have missed it.
  assert.doesNotMatch(wf, /^\s*paths(-ignore)?:/m, 'docs.yml must stay unfiltered');
});

test('ci.yml runs the licence gate, where the CI-fix loop can see it', () => {
  // The licence gate has two homes and neither alone is sufficient: `make
  // verify` needs Node locally, and CI is the only workflow
  // `agent-iterate-ci.yml` subscribes to — a gate that reds anywhere else
  // stops an agent-managed PR with nothing watching it.
  //
  // THE COMMAND MOVED, THE FACT DID NOT. `ci.yml` used to spell
  // `node scripts/verify-license.mjs` in a `run:` line; it now names the
  // `license` LANE, and the command lives in `scripts/ci/run-lanes.mjs`. So
  // this half asserts the invocation, and `run-lanes.test.mjs` asserts the
  // other half — that the lane of that name still runs the licence script and
  // not `make verify-license`, which would fail open where Node is absent.
  // Split across two files because the second half needs the manifest, and
  // this suite deliberately imports nothing that could execute a lane.
  const wf = readFileSync(path.join(REPO, '.github', 'workflows', 'ci.yml'), 'utf8');
  assert.match(wf, /run: node scripts\/ci\/run-lanes\.mjs license\b/);
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
    // A contributor's global `commit.gpgsign` would otherwise make every
    // fixture commit prompt for, or fail on, a signing key.
    git('config', 'commit.gpgsign', 'false');
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
  const bin = stubLinter(dir);

  const r = runHookProbe('pre-commit', {
    dir,
    marker: 'WOULD_LINT',
    env: { PATH: `${bin}${path.delimiter}${process.env.PATH}` },
  });
  return r.stdout.includes('WOULD_LINT');
}

/** A no-op `golangci-lint` on PATH; returns the directory to prepend. */
function stubLinter(dir) {
  const bin = path.join(dir, '.probe-bin');
  mkdirSync(bin, { recursive: true });
  const stub = path.join(bin, 'golangci-lint');
  writeFileSync(stub, '#!/usr/bin/env bash\nexit 0\n');
  chmodSync(stub, 0o755);
  return bin;
}

/**
 * Run one of the git hooks against a scratch repo with its `exec make …` tail
 * replaced by a marker.
 */
function runHookProbe(hook, { dir, marker, env = {} }) {
  const src = readFileSync(path.join(REPO, '.githooks', hook), 'utf8').replace(
    /^exec make (lint|verify)$/m,
    `echo ${marker}`,
  );
  const probe = path.join(dir, `.probe-${hook}`);
  writeFileSync(probe, src);

  return spawnSync('bash', [probe], {
    cwd: dir,
    encoding: 'utf8',
    // The hook itself runs `git diff --cached`; `git -C` above protects the
    // helper, but nothing protected the hook until this.
    env: fixtureGitEnv(dir, { ...process.env, ...env }),
  });
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

/**
 * A scratch upstream plus a clone of it, built without `git clone` so every
 * command can be addressed with `git -C`.
 *
 * NOT `fixtureGitEnv`: it pins GIT_DIR at one directory, and the worktree
 * cases below need git's own discovery to find `.git/worktrees/<name>`.
 * `repoScopedEnv(root)` strips every inherited location variable and sets
 * the discovery ceiling at `root`'s parent, so nothing can climb out of the
 * scratch directory into this repository.
 */
function inScratchClone(body) {
  const root = realpathSync(mkdtempSync(path.join(tmpdir(), 'scratch-clone-')));
  const env = repoScopedEnv(root);
  const at = (cwd) => (...args) => spawnSync('git', ['-C', cwd, ...args], { encoding: 'utf8', env });
  try {
    const upstream = path.join(root, 'upstream');
    const clone = path.join(root, 'clone');
    for (const dir of [upstream, clone]) {
      mkdirSync(dir);
      const git = at(dir);
      git('init', '-q', '-b', 'main', '.');
      git('config', 'user.email', 'test@example.com');
      git('config', 'user.name', 'test');
      git('config', 'commit.gpgsign', 'false');
    }
    at(upstream)('commit', '-qm', 'base', '--allow-empty', '--no-verify');
    const git = at(clone);
    git('remote', 'add', 'origin', upstream);
    git('fetch', '-q', 'origin');
    git('reset', '-q', '--hard', 'origin/main');
    return body({ root, upstream, clone, at, env });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

/**
 * Run a hook from this repository, unmodified, against `dir`.
 *
 * Stubs rather than markers: `make` echoes what it was asked to run, and
 * `golangci-lint` exists so pre-commit's missing-linter refusal is not the
 * answer being measured. The hook is copied outside the checkout so the
 * scratch tree carries nothing but what the case planted.
 */
function runHookIn(hook, dir, env) {
  const bin = path.join(dir, '..', 'probe-bin');
  mkdirSync(bin, { recursive: true });
  for (const [name, body] of [
    ['make', '#!/usr/bin/env bash\necho "RAN make $*"\n'],
    ['golangci-lint', '#!/usr/bin/env bash\nexit 0\n'],
  ]) {
    writeFileSync(path.join(bin, name), body);
    chmodSync(path.join(bin, name), 0o755);
  }
  const hooks = path.join(dir, '..', 'probe-hooks');
  mkdirSync(hooks, { recursive: true });
  writeFileSync(path.join(hooks, hook), readFileSync(path.join(REPO, '.githooks', hook)));
  return spawnSync('bash', [path.join(hooks, hook)], {
    cwd: dir,
    encoding: 'utf8',
    env: { ...env, PATH: `${bin}${path.delimiter}${process.env.PATH}` },
  });
}

/** Copy what setup.sh installs and runs into the upstream, and check it out. */
function plantSetup({ upstream, clone, at }) {
  for (const rel of [
    '.githooks/commit-msg',
    '.githooks/pre-commit',
    '.githooks/pre-push',
    'scripts/setup.sh',
    'scripts/direct-run.mjs',
    ...HOOK_WIRING.map(({ script }) => `scripts/hooks/${script}`),
    'scripts/hooks/install.mjs',
  ]) {
    const dest = path.join(upstream, rel);
    mkdirSync(path.dirname(dest), { recursive: true });
    writeFileSync(dest, readFileSync(path.join(REPO, rel)));
    chmodSync(dest, statSync(path.join(REPO, rel)).mode);
  }
  at(upstream)('add', '-A');
  at(upstream)('commit', '-qm', 'hooks', '--no-verify');
  at(clone)('fetch', '-q', 'origin');
  at(clone)('reset', '-q', '--hard', 'origin/main');
}

function runSetup(cwd, env, args = []) {
  // `--check` is silent under CI, and the Docs workflow that runs this suite
  // sets CI; every case here is about a developer's clone.
  const { CI: _ci, ...local } = env;
  return spawnSync('bash', [path.join(cwd, 'scripts', 'setup.sh'), ...args], {
    cwd,
    encoding: 'utf8',
    env: local,
  });
}

/** Wire `clone` the way setup.sh did before 2026-10: a `$GIT_DIR` snapshot. */
function wireLegacySnapshot({ clone, at }) {
  const legacy = path.join(clone, '.git', 'githooks');
  mkdirSync(legacy, { recursive: true });
  writeFileSync(path.join(legacy, 'pre-commit'), '#!/usr/bin/env bash\nexit 1\n');
  at(clone)('config', 'core.hooksPath', legacy);
  return legacy;
}

/** Where git will look for hooks when run in `cwd`, as an absolute path. */
function hooksDir(at, cwd) {
  return path.resolve(cwd, at(cwd)('rev-parse', '--git-path', 'hooks').stdout.trim());
}

test('setup.sh points core.hooksPath at the tracked .githooks', () => {
  // The wafflebase model: no snapshot to go stale, and a hook change reaches
  // the clone on the next checkout. Run for real in a scratch clone rather
  // than read out of the script's text.
  inScratchClone((ctx) => {
    const { clone, at, env } = ctx;
    plantSetup(ctx);
    const r = runSetup(clone, env);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(at(clone)('config', '--get', 'core.hooksPath').stdout.trim(), '.githooks');
    assert.equal(hooksDir(at, clone), path.join(clone, '.githooks'));
    accessSync(path.join(hooksDir(at, clone), 'pre-push'), constants.X_OK);
    statSync(path.join(clone, '.claude', 'settings.local.json'));
  });
});

test('setup.sh in a worktree wires every worktree to its own .githooks', () => {
  // `core.hooksPath` is shared config, and relative, so each worktree resolves
  // it against its own top level. Removing the worktree setup ran in must not
  // leave the clone without hooks — the failure the old per-worktree snapshot
  // had.
  inScratchClone((ctx) => {
    const { root, clone, at, env } = ctx;
    plantSetup(ctx);
    const wt = path.join(root, 'wt');
    at(clone)('worktree', 'add', '-q', wt, 'origin/main');

    const r = runSetup(wt, env);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(hooksDir(at, wt), path.join(wt, '.githooks'));
    // install.mjs snapshots the Claude Code hooks into the common git dir.
    const settings = readFileSync(path.join(wt, '.claude', 'settings.local.json'), 'utf8');
    assert.doesNotMatch(settings, /worktrees/, 'Claude hooks snapshotted per worktree');

    at(clone)('worktree', 'remove', '--force', wt);
    assert.equal(hooksDir(at, clone), path.join(clone, '.githooks'));
    statSync(path.join(hooksDir(at, clone), 'pre-push'));
  });
});

test('pre-push reaches make verify', () => {
  inScratchClone(({ clone, at, env }) => {
    at(clone)('commit', '-qm', 'mine', '--allow-empty', '--no-verify');
    const r = runHookIn('pre-push', clone, env);
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /RAN make verify/);
  });
});

test('pre-commit reaches make lint', () => {
  inScratchClone(({ clone, at, env }) => {
    at(clone)('commit', '-qm', 'mine', '--allow-empty', '--no-verify');
    writeFileSync(path.join(clone, 'a.go'), 'package a\n');
    at(clone)('add', 'a.go');
    const r = runHookIn('pre-commit', clone, env);
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /RAN make lint/);
  });
});

test('the gates run on a branch carrying commits somebody else wrote', () => {
  // THE DECISION THIS PINS. A trust guard used to refuse here, and an
  // agent-loop branch always carries bot commits, so every maintainer commit
  // and push on one needed an override. The gates now run whatever is
  // checked out, as `make verify` typed by hand would.
  inScratchClone(({ upstream, clone, at, env }) => {
    at(upstream)('checkout', '-qb', 'pr');
    at(upstream)('-c', 'user.email=bot@example.com', 'commit', '-qm', 'theirs',
      '--allow-empty', '--no-verify');
    at(clone)('fetch', '-q', 'origin');
    at(clone)('checkout', '-qb', 'review', 'origin/pr');
    at(clone)('commit', '-qm', 'mine', '--allow-empty', '--no-verify');
    const r = runHookIn('pre-push', clone, env);
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /RAN make verify/);
  });
});

/**
 * Turn the scratch clone into a fork contributor's: `origin` is a fork whose
 * `main` lags one commit behind, and the real repository is `upstream`.
 */
function asStaleFork({ root, upstream, clone, at }) {
  const fork = path.join(root, 'fork');
  at(root)('clone', '-q', '--bare', upstream, fork);
  at(fork)('update-ref', 'refs/heads/main', 'main~1');
  at(clone)('remote', 'set-url', 'origin', fork);
  at(clone)('fetch', '-q', '--prune', 'origin', '+refs/heads/main:refs/remotes/origin/main');
  at(clone)('remote', 'add', 'upstream', upstream);
  at(clone)('fetch', '-q', 'upstream');
}

test('setup.sh compares against upstream/main in a fork', () => {
  // The same stale fork, one layer up: compared against the fork's `main`,
  // current hook sources read as a local edit and setup refused to install
  // the very hooks the default branch ships.
  inScratchClone((ctx) => {
    const { clone, at, env } = ctx;
    plantSetup(ctx);
    asStaleFork(ctx);
    at(clone)('checkout', '-q', '--detach', 'upstream/main');
    const r = runSetup(clone, env);
    assert.equal(r.status, 0, r.stderr);
  });
});

test('setup.sh refuses Claude Code hook sources that differ from origin/main', () => {
  // The re-run inside a reviewed branch would persist that branch's
  // `scripts/hooks/*.sh` as the clone's Claude Code hooks. The git hooks are
  // read live from the worktree, so they are enabled before the refusal and
  // a change to them is not one.
  inScratchClone((ctx) => {
    const { clone, at, env } = ctx;
    plantSetup(ctx);
    writeFileSync(path.join(clone, '.githooks', 'pre-push'), '#!/usr/bin/env bash\nexit 0\n');
    assert.equal(runSetup(clone, env).status, 0, 'a .githooks change must not refuse');
    rmSync(path.join(clone, '.claude'), { recursive: true, force: true });

    writeFileSync(path.join(clone, 'scripts', 'hooks', 'session-prime.sh'), '#!/usr/bin/env bash\n');
    // A refused run changes nothing — not the wiring, not the legacy copy.
    const legacy = wireLegacySnapshot(ctx);
    const r = runSetup(clone, env);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /YORKIE_ALLOW_LOCAL_HOOKS=1/);
    assert.equal(at(clone)('config', '--get', 'core.hooksPath').stdout.trim(), legacy);
    assert.equal(existsSync(legacy), true, 'a refused run must delete nothing');
    assert.equal(existsSync(path.join(clone, '.claude', 'settings.local.json')), false);

    const forced = runSetup(clone, { ...env, YORKIE_ALLOW_LOCAL_HOOKS: '1' });
    assert.equal(forced.status, 0, forced.stderr);
  });
});

test('a real git commit on a bot-authored branch runs the hooks and lands', () => {
  // Through git's own hook dispatch, not `bash <hook>`: setup.sh wires the
  // clone, and `git commit` has to find `.githooks/pre-commit` by itself.
  inScratchClone((ctx) => {
    const { root, upstream, clone, at, env } = ctx;
    plantSetup(ctx);
    at(upstream)('-c', 'user.email=bot@example.com', '-c', 'user.name=bot',
      'commit', '-qm', 'Bot commit', '--allow-empty', '--no-verify');
    at(clone)('fetch', '-q', 'origin');
    at(clone)('reset', '-q', '--hard', 'origin/main');
    assert.equal(runSetup(clone, env).status, 0);

    const bin = path.join(root, 'bin');
    const marker = path.join(root, 'make-ran');
    mkdirSync(bin);
    writeFileSync(path.join(bin, 'make'), `#!/usr/bin/env bash\necho "$*" >> '${marker}'\n`);
    writeFileSync(path.join(bin, 'golangci-lint'), '#!/usr/bin/env bash\nexit 0\n');
    chmodSync(path.join(bin, 'make'), 0o755);
    chmodSync(path.join(bin, 'golangci-lint'), 0o755);

    writeFileSync(path.join(clone, 'a.go'), 'package a\n');
    at(clone)('add', 'a.go');
    const r = spawnSync('git', ['-C', clone, 'commit', '-qm', 'Add a'], {
      encoding: 'utf8',
      env: { ...env, PATH: `${bin}${path.delimiter}${env.PATH}` },
    });
    assert.equal(r.status, 0, r.stderr);
    assert.equal(readFileSync(marker, 'utf8').trim(), 'lint', 'pre-commit did not reach make');
    assert.equal(at(clone)('log', '-1', '--format=%s').stdout.trim(), 'Add a');
  });
});

test('setup.sh --check names what is wrong, and never fails', () => {
  inScratchClone((ctx) => {
    const { clone, at, env } = ctx;
    plantSetup(ctx);

    const unset = runSetup(clone, env, ['--check']);
    assert.equal(unset.status, 0);
    assert.match(unset.stderr, /not installed.*unset/);

    wireLegacySnapshot(ctx);
    const legacy = runSetup(clone, env, ['--check']);
    assert.equal(legacy.status, 0);
    assert.match(legacy.stderr, /legacy hook snapshot/);
    assert.match(legacy.stderr, /trusted-tree guard/);
    assert.match(legacy.stderr, /bash scripts\/setup\.sh/);

    at(clone)('config', 'core.hooksPath', '.githooks');
    const ok = runSetup(clone, env, ['--check']);
    assert.equal(ok.status, 0);
    assert.equal(ok.stderr, '', 'a correctly wired clone must hear nothing');
    assert.equal(at(clone)('config', '--get', 'core.hooksPath').stdout.trim(), '.githooks');
  });
});

test('make lint runs the hook check without letting it fail the target', () => {
  const mk = readFileSync(path.join(REPO, 'Makefile'), 'utf8');
  const recipe = mk.slice(mk.search(/^lint:/m)).split(/\n(?!\t)/)[0];
  assert.match(recipe, /scripts\/setup\.sh --check \|\| true/);
});

test('setup.sh removes the legacy snapshot only when the clone points at it', () => {
  inScratchClone((ctx) => {
    const { clone, at, env } = ctx;
    plantSetup(ctx);
    const legacy = wireLegacySnapshot(ctx);
    const r = runSetup(clone, env);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(existsSync(legacy), false, 'the legacy snapshot must go');
    assert.equal(at(clone)('config', '--get', 'core.hooksPath').stdout.trim(), '.githooks');
  });
  inScratchClone((ctx) => {
    const { clone, at, env } = ctx;
    plantSetup(ctx);
    const legacy = wireLegacySnapshot(ctx);
    at(clone)('config', '--unset', 'core.hooksPath');
    assert.equal(runSetup(clone, env).status, 0);
    assert.equal(existsSync(legacy), true, 'a directory nothing points at is not ours');
  });
});
