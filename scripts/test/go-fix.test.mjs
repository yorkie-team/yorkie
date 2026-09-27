// Guards for `scripts/go-fix.sh` — the entire body of CI's `modernize` lane
// and of `make verify-modernize`, so its exit status IS the lane's verdict.
//
// THE FAILURE THIS FILE EXISTS FOR IS A GREEN LANE THAT CHECKED NOTHING. An
// earlier version of this gate read `[ -z "$(go fix -diff)" ]`, and a tree
// that did not compile produced empty output and so read as "settled" — the
// lane passed while covering zero files. The fix has to thread a needle the
// status alone cannot: the analyzer drivers `go fix` is built on exit non-zero
// whenever they report ANYTHING, so "a rewrite is pending" and "the tree is
// broken" arrive with the same non-zero status, distinguishable only by
// whether stdout carries a diff. Get that backwards in either direction and
// the gate either fails open on a broken tree or can never apply anything.
//
// Those three branches are what the tests below pin, one scenario each, plus
// the two hard errors that keep the derived tag set honest.
//
// `go` is stubbed on PATH rather than driven for real: the `Docs` workflow
// that runs this suite is a Node-only job with no Go toolchain, and a real
// toolchain could not be made to report "pending rewrites with a non-zero
// status" on demand anyway. The script therefore runs exactly as written —
// only the tool whose status semantics are under test is replaced.

import { spawnSync } from 'node:child_process';
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

// The repository's own guard, not a hand-copied list of variable names. git
// exports its location variables into every command it runs, so a fixture
// inheriting them operates on whatever `GIT_DIR` names — and `go-fix.sh`
// opens with `cd "$(git rev-parse --show-toplevel)"`, which is precisely a
// command that decides its repository from the environment rather than `cwd`.
import { fixtureGitEnv } from '../agent/git-env.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const SCRIPT = path.join(REPO, 'scripts', 'go-fix.sh');

// One file per `//go:build` line. Between them they exercise every arm of the
// tag derivation: a plain tag, a tag ANDed with the one platform the script
// analyses, and the two words it must drop — `ignore` (no build includes the
// file; turning it on would put a `package main` beside its library) and
// `go1.N` (a version the toolchain satisfies by itself).
const BUILD_LINES = ['integration', 'bench && amd64', 'ignore', 'go1.25'];

/** The tag list `BUILD_LINES` must produce: `sort -u`'d, platforms dropped. */
const TAGS = 'bench,integration';

/**
 * A `go` on PATH whose `fix` reports whatever the scenario asks for.
 *
 * `STUB_DIFF_SEQ` is a `:`-separated token per `-diff` call, the last token
 * repeating forever — which is what lets one variable describe both "settles
 * on the second pass" and "never settles". `STUB_APPLY_EXIT` is the status of
 * the applying (no `-diff`) pass, the one line 100's `|| true` absorbs.
 *
 * Tokens: `clean` (nothing, exit 0), `pending` (a diff, exit 3 — the
 * ambiguous case), `pending-ok` (a diff, exit 0), `broken` (nothing on stdout,
 * a compiler error on stderr, exit 1).
 */
function stubGo(state) {
  const bin = path.join(state, 'bin');
  mkdirSync(bin, { recursive: true });
  const stub = path.join(bin, 'go');
  writeFileSync(
    stub,
    `#!/usr/bin/env bash
set -u
printf '%s\\n' "$*" >>"$STUB_STATE/calls"

# \`go tool dist list\`: the platform vocabulary the script filters tags against.
if [ "\${1:-}" = tool ]; then
  printf '%s\\n' linux/amd64 darwin/arm64 js/wasm
  exit 0
fi

diff=0
for a in "$@"; do [ "$a" = -diff ] && diff=1; done

if [ "$diff" = 0 ]; then
  printf '%s\\n' "$*" >>"$STUB_STATE/applies"
  exit "\${STUB_APPLY_EXIT:-0}"
fi

n=$(cat "$STUB_STATE/diffs" 2>/dev/null || printf 0)
n=$((n + 1))
printf '%s' "$n" >"$STUB_STATE/diffs"
IFS=: read -r -a seq <<<"$STUB_DIFF_SEQ"
i=$((n - 1))
[ "$i" -ge "\${#seq[@]}" ] && i=$(( \${#seq[@]} - 1 ))

case "\${seq[$i]}" in
  clean) exit 0 ;;
  pending) printf -- '--- a/f0.go\\n+++ b/f0.go\\n'; exit 3 ;;
  pending-ok) printf -- '--- a/f0.go\\n+++ b/f0.go\\n'; exit 0 ;;
  broken) printf '%s\\n' 'f0.go:3:1: undefined: nope' >&2; exit 1 ;;
  *) printf '%s\\n' "stub: unknown token \${seq[$i]}" >&2; exit 111 ;;
esac
`,
  );
  chmodSync(stub, 0o755);
  return bin;
}

/**
 * A throwaway git repository with `BUILD_LINES` staged, plus the stub, handed
 * to `body` as a `run(subcommand, opts)` that invokes the real script.
 *
 * Staged and not merely written: `git grep` searches tracked files, so an
 * unstaged fixture would yield an empty word list and `set -o pipefail` would
 * kill the script before it reached anything under test.
 */
function withFixture(body, { buildLines = BUILD_LINES } = {}) {
  const dir = realpathSync(mkdtempSync(path.join(tmpdir(), 'go-fix-')));
  const state = path.join(dir, '.stub');
  mkdirSync(state, { recursive: true });
  const bin = stubGo(state);
  try {
    const git = (...args) =>
      spawnSync('git', ['-C', dir, ...args], { encoding: 'utf8', env: fixtureGitEnv(dir) });
    git('init', '-q', '.');
    buildLines.forEach((line, i) => {
      writeFileSync(path.join(dir, `f${i}.go`), `//go:build ${line}\n\npackage p\n`);
    });
    git('add', '-A');

    const run = (subcommand, { seq = 'clean', applyExit = '0' } = {}) =>
      spawnSync('bash', [SCRIPT, subcommand], {
        cwd: dir,
        encoding: 'utf8',
        env: {
          ...fixtureGitEnv(dir),
          PATH: `${bin}${path.delimiter}${process.env.PATH}`,
          STUB_STATE: state,
          STUB_DIFF_SEQ: seq,
          STUB_APPLY_EXIT: applyExit,
        },
      });

    const lines = (name) => {
      const file = path.join(state, name);
      if (!existsSync(file)) return [];
      return readFileSync(file, 'utf8').split('\n').filter(Boolean);
    };

    return body({ dir, run, calls: () => lines('calls'), applies: () => lines('applies') });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test('check passes only when `go fix -diff` reports nothing', () => {
  withFixture(({ run, calls }) => {
    const r = run('check', { seq: 'clean' });
    assert.equal(r.status, 0, r.stderr);
    assert.equal(r.stdout, '');
    // The tag set is derived, not listed: `ignore` and `go1.25` dropped,
    // `amd64` recognised as the platform it is rather than passed as a tag.
    assert.ok(
      calls().some((c) => c.includes(`fix -tags ${TAGS} -diff ./...`)),
      calls().join('\n'),
    );
  });
});

test('check fails on pending rewrites even though the status is ambiguous', () => {
  withFixture(({ run }) => {
    const r = run('check', { seq: 'pending' });
    assert.equal(r.status, 1);
    assert.match(r.stdout, /\+\+\+ b\/f0\.go/);
    assert.match(r.stderr, /rewrites pending under tags \[bench,integration\]/);
    // The distinction that matters: a diff is NOT reported as a broken tree,
    // or `make modernize` would be advertised as the fix for a compile error.
    assert.doesNotMatch(r.stderr, /failed under tags/);
  });
});

test('check fails closed when the tree does not compile', () => {
  withFixture(({ run }) => {
    const r = run('check', { seq: 'broken' });
    // THE REGRESSION THIS WHOLE FILE IS FOR: empty output plus a non-zero
    // status must not read as "settled".
    assert.equal(r.status, 1);
    assert.match(r.stderr, /'go fix -diff' failed under tags \[bench,integration\]/);
    // The compiler's own words, or the lane says only that something failed.
    assert.match(r.stderr, /undefined: nope/);
    assert.doesNotMatch(r.stderr, /rewrites pending/);
  });
});

test('apply rewrites despite `go fix` exiting non-zero for having rewritten', () => {
  withFixture(({ run, applies }) => {
    // The applying pass exits 1 — reporting its rewrite, not failing. Without
    // line 100's `|| true`, `set -e` would abort here and the lane would fail
    // on the very tree it had just fixed.
    const r = run('apply', { seq: 'pending:clean', applyExit: '1' });
    assert.equal(r.status, 0, r.stderr);
    assert.equal(applies().length, 1, applies().join('\n'));
  });
});

test('apply does no work when nothing is pending', () => {
  withFixture(({ run, applies }) => {
    const r = run('apply', { seq: 'clean' });
    assert.equal(r.status, 0, r.stderr);
    assert.deepEqual(applies(), []);
  });
});

test('apply gives up after three passes rather than looping', () => {
  withFixture(({ run, applies }) => {
    const r = run('apply', { seq: 'pending' });
    assert.equal(r.status, 1);
    assert.match(r.stderr, /did not settle after 3 passes/);
    assert.equal(applies().length, 3, applies().join('\n'));
  });
});

test('apply fails closed when the tree stops compiling mid-run', () => {
  withFixture(({ run }) => {
    const r = run('apply', { seq: 'pending:broken' });
    assert.equal(r.status, 1);
    // Named as the compile failure it is — reporting "did not settle" would
    // send the next reader looking for a rewrite loop that is not there.
    assert.match(r.stderr, /'go fix -diff' failed under tags/);
    assert.doesNotMatch(r.stderr, /did not settle/);
  });
});

test('a negated build tag is refused rather than silently uncovered', () => {
  withFixture(
    ({ run, calls }) => {
      const r = run('check', { seq: 'clean' });
      assert.equal(r.status, 1);
      assert.match(r.stderr, /negates a tag/);
      // Refused BEFORE analysing, or the lane would report a pass over a file
      // set the tag list excludes.
      assert.deepEqual(
        calls().filter((c) => c.startsWith('fix ')),
        [],
      );
    },
    { buildLines: ['integration', '!race'] },
  );
});

test('a second platform is refused rather than analysed as a tag', () => {
  withFixture(
    ({ run }) => {
      const r = run('check', { seq: 'clean' });
      assert.equal(r.status, 1);
      assert.match(r.stderr, /names platform 'darwin'/);
    },
    { buildLines: ['integration', 'darwin'] },
  );
});

test('an unknown subcommand is a usage error, not a silent pass', () => {
  withFixture(({ run }) => {
    const r = run('', { seq: 'clean' });
    assert.equal(r.status, 2);
    assert.match(r.stderr, /usage:/);
  });
});
