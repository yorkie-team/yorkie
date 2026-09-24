// Guards for the local enforcement layer: the Claude Code hooks, their wiring
// in .claude/settings.json, and the workflow step that runs the licence gate.
//
// EVERY FAILURE THIS FILE CATCHES IS SILENT. `guard-generated-files.sh` fails
// OPEN by design, so a `case` pattern that stops matching does not error — it
// just stops guarding. A renamed hook script makes its settings.json entry a
// no-op that Claude Code does not announce. And deleting the licence step from
// docs.yml leaves `make verify` printing SKIPPED wherever Node is absent, with
// nothing anywhere reporting the loss. None of the three shows up as a red
// lane, which is the whole reason they are pinned here.
//
// Unlike its sibling suites this one reads THIS repository rather than a
// planted tree — the facts being checked are about this tree, and a planted
// copy of them would assert only that the test file is self-consistent. It
// stays read-only: it runs the hook with a payload on stdin and reads files.

import { execFileSync, spawnSync } from 'node:child_process';
import { accessSync, constants, readdirSync, readFileSync, statSync } from 'node:fs';
import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

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

test('docs.yml still runs both unfiltered checks', () => {
  // The licence gate has two homes and neither alone is sufficient: `make
  // verify` announces a SKIP where Node is absent, and this workflow is the
  // only one that runs on every PR with no path filter.
  const wf = readFileSync(path.join(REPO, '.github', 'workflows', 'docs.yml'), 'utf8');
  assert.match(wf, /node scripts\/verify-license\.mjs/);
  assert.match(wf, /node scripts\/verify-doc-links\.mjs/);
  assert.doesNotMatch(wf, /^\s*paths:/m, 'docs.yml must stay unfiltered');
});

test('make verify reaches the licence gate', () => {
  // `verify` is what the git hooks call, so a licence check dropped from its
  // prerequisites is a gate that only CI still holds.
  const mk = readFileSync(path.join(REPO, 'Makefile'), 'utf8');
  const target = mk.split('\n').find((l) => l.startsWith('verify:'));
  assert.ok(target, 'the Makefile has no verify target');
  assert.match(target, /\bverify-license\b/);

  // And the target it names really exists, rather than being a typo that make
  // would report only when someone runs it.
  const help = execFileSync('make', ['help'], { cwd: REPO, encoding: 'utf8' });
  assert.match(help, /verify-license/);
});
