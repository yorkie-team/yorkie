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

// Installs this repository's Claude Code hooks for the CURRENT CLONE, as an
// explicit act by the person who owns the machine. Run by `scripts/setup.sh`.
//
// WHY THIS IS NOT A TRACKED .claude/settings.json, which is where it started
// and is the obvious place for it: Claude Code reads project settings out of
// the working tree and runs the commands they name, with no confirmation, at
// SessionStart and before every Edit/Write. A tracked settings.json therefore
// means that checking out a pull-request branch — the ordinary way anyone
// reviews one — and opening a session in that checkout executes whatever that
// branch put in `scripts/hooks/*.sh`. Both the wiring and the code it names
// are ordinary tracked files any contributor can rewrite, and the guard hook
// does not protect either of them. That turns "review this patch" into
// "run this patch", which is not a trade the convenience of automatic wiring
// is worth.
//
// So the install is inverted on both halves:
//
//   1. The WIRING goes to `.claude/settings.local.json`, which is gitignored.
//      A branch cannot introduce, modify or remove it, and a checkout of an
//      untrusted branch wires nothing at all — the state this repository was
//      in before the hooks existed.
//   2. The CODE is SNAPSHOT into `$GIT_DIR/agent-hooks/` and the wiring names
//      those copies. `git checkout` never writes inside `$GIT_DIR`, so the
//      scripts a session runs stay the ones present when a human ran setup,
//      whatever branch the worktree is on.
//
// The cost is staleness: an improved guard reaches a clone only when someone
// re-runs `scripts/setup.sh`. That is the right direction to fail. A stale
// guard still refuses the edits it knew about, CI's codegen-freshness check is
// the backstop that actually closes the set, and the alternative — live code
// from the tree — is the property being removed.

import { spawnSync } from 'node:child_process';
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import path from 'node:path';

import { isDirectRun } from '../direct-run.mjs';

/**
 * The hooks to wire, and the events they answer.
 *
 * `script` names a file in `scripts/hooks/`; the installer copies it and wires
 * the copy. `harness-hooks.test.mjs` asserts each one exists and is
 * executable — a rename makes an entry a no-op that Claude Code never
 * announces.
 */
export const HOOK_WIRING = [
  { event: 'SessionStart', matcher: '', script: 'session-prime.sh' },
  { event: 'PreToolUse', matcher: 'Edit|Write', script: 'guard-generated-files.sh' },
];

/** Directory, relative to `$GIT_DIR`, holding the snapshot the wiring names. */
export const SNAPSHOT_DIRNAME = 'agent-hooks';

/**
 * Does this settings entry belong to us?
 *
 * Matched by the script path rather than by position so that re-running the
 * installer REPLACES the previous wiring instead of stacking a second copy,
 * and so that a clone still carrying the old in-tree wiring
 * (`bash scripts/hooks/...`) is migrated rather than left with both.
 */
function isOurs(command) {
  return (
    typeof command === 'string' &&
    // The boundary has to admit a leading `bash ` as well as a path prefix,
    // which is why it is `[\s/]` and not `/` — the in-tree wiring this
    // migrates from is written `bash scripts/hooks/<name>.sh`.
    // The trailing class admits the closing quote `shellQuote` adds as well
    // as whitespace: without it, re-running setup stops recognising its own
    // previous wiring and stacks a second copy of every hook.
    new RegExp(`(^|[\\s/'"])(${SNAPSHOT_DIRNAME}|scripts/hooks)/[\\w.-]+\\.sh(['"\\s]|$)`).test(command)
  );
}

/**
 * Merge this repository's hook wiring into `settings`, returning a new object.
 *
 * Pure, and everything it does not own is copied through untouched:
 * `settings.local.json` is where a contributor's `permissions.allow` grants
 * live, and an installer that dropped them would be a worse nuisance than no
 * installer.
 */
/**
 * Single-quote a path for the shell Claude Code runs hook commands through.
 *
 * Unquoted, a clone under `~/My Projects/` produces `bash /Users/x/My
 * Projects/.../session-prime.sh`, which the shell splits into two words: the
 * hook fails to start and the guard is silently gone — the failure this whole
 * change exists to refuse. A path containing shell metacharacters is worse
 * than silent.
 */
export function shellQuote(value) {
  return `'${String(value).replace(/'/g, "'\\''")}'`;
}

export function wireHooks(settings, snapshotDir) {
  const next = { ...(settings ?? {}) };
  const hooks = { ...(next.hooks ?? {}) };

  for (const { event, matcher, script } of HOOK_WIRING) {
    const kept = (Array.isArray(hooks[event]) ? hooks[event] : [])
      .map((group) => ({
        ...group,
        hooks: (Array.isArray(group?.hooks) ? group.hooks : []).filter(
          (hook) => !isOurs(hook?.command),
        ),
      }))
      .filter((group) => group.hooks.length > 0);

    hooks[event] = [
      ...kept,
      {
        matcher,
        hooks: [
          { type: 'command', command: `bash ${shellQuote(path.join(snapshotDir, script))}` },
        ],
      },
    ];
  }

  next.hooks = hooks;
  return next;
}

/** `git rev-parse <flag>`, or null when this is not a git checkout. */
function gitPath(flag) {
  const r = spawnSync('git', ['rev-parse', flag], { encoding: 'utf8' });
  if (r.status !== 0) return null;
  return r.stdout.trim() || null;
}

function main() {
  const root = gitPath('--show-toplevel');
  const gitDir = gitPath('--absolute-git-dir');
  if (!root || !gitDir) {
    process.stderr.write('not a git checkout; skipping Claude Code hook install\n');
    process.exit(1);
  }

  const snapshotDir = path.join(gitDir, SNAPSHOT_DIRNAME);
  mkdirSync(snapshotDir, { recursive: true });
  for (const { script } of HOOK_WIRING) {
    const dest = path.join(snapshotDir, script);
    copyFileSync(path.join(root, 'scripts', 'hooks', script), dest);
    chmodSync(dest, 0o755);
  }

  const settingsPath = path.join(root, '.claude', 'settings.local.json');
  let existing = {};
  if (existsSync(settingsPath)) {
    // REFUSE rather than overwrite. This file holds permission grants the
    // contributor wrote by hand; clobbering one because it failed to parse is
    // how an installer earns a reputation.
    try {
      existing = JSON.parse(readFileSync(settingsPath, 'utf8'));
    } catch (err) {
      process.stderr.write(`${settingsPath} is not valid JSON (${err.message}); left alone\n`);
      process.exit(1);
    }
  }

  mkdirSync(path.dirname(settingsPath), { recursive: true });
  writeFileSync(settingsPath, `${JSON.stringify(wireHooks(existing, snapshotDir), null, 2)}\n`);
  process.stdout.write(
    `Claude Code hooks installed from ${path.relative(root, snapshotDir)} ` +
      `into ${path.relative(root, settingsPath)}\n`,
  );
}

if (isDirectRun(import.meta.url)) main();
