import "./fixtures/use-fixture-config.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defaultRoot } from "./manifest.mjs";

// A reader that stops before its input ends (`head -n 1`, `grep -q`, `grep -m`)
// leaves the writer before it in the pipe to die of SIGPIPE when it is still
// writing. Under `pipefail` — set explicitly in these workflows, and implied by
// `shell: bash` — that exit (141) fails the step. Whether it happens depends on
// timing, so a test that runs the step once does not catch it: agent-implement's
// PR step failed this way in one repository's CI and passed in three others.
// Cut a variable instead (`${v%%$'\n'*}`), or read the whole input.
const EARLY_EXIT = [
  [/\|\s*head\s+(-n\s*\d+|-\d+)\b/, "`| head -n N` stops reading early"],
  [/\|\s*grep\s+(-[a-zA-Z]*q[a-zA-Z]*|--quiet|--silent)\b/, "`| grep -q` stops reading at the first match"],
  [/\|\s*grep\s+(-[a-zA-Z]*m\s*\d*|--max-count)\b/, "`| grep -m` stops reading after N matches"],
  [/\|\s*sed\s+(-n\s+)?'?\d+q'?/, "`| sed Nq` stops reading early"],
];

export function earlyExitReaders(text) {
  const found = [];
  text.split("\n").forEach((line, i) => {
    const code = line.replace(/^\s*#.*$/, "");
    for (const [re, why] of EARLY_EXIT) if (re.test(code)) found.push({ line: i + 1, why, text: line.trim() });
  });
  return found;
}

test("earlyExitReaders: refuses each early-exit reader, accepts whole-input readers", () => {
  for (const bad of [
    'T="$(head -c 4000 "$F" | head -n 1 | sed s/a/b/)"',
    "x=$(cat f | head -1)",
    'printf "%s" "$X" | grep -q foo',
    "echo a | grep -qE b",
    "echo a | grep --quiet b",
    "echo a | grep -m 1 b",
    "echo a | sed 1q",
  ]) assert.equal(earlyExitReaders(bad).length, 1, bad);
  for (const ok of [
    'T="$(head -c 4000 "$F")"',
    "T=\"$(printf '%s' \"${T%%$'\\n'*}\" | sed s/a/b/)\"",
    "B=\"$(head -c 400000 \"$F\" | tail -n +2 | sed s/a/b/)\"",
    "grep -q foo file",
    "# a comment that mentions | head -n 1",
    "echo a | grep b",
  ]) assert.deepEqual(earlyExitReaders(ok), [], ok);
});

test("no agent workflow pipes into a reader that stops before its input ends", () => {
  const root = defaultRoot(path.dirname(fileURLToPath(import.meta.url)));
  const dirs = [path.join(root, ".github/workflows"), path.join(root, ".github/actions/agent-toolchain")];
  const files = dirs.filter(existsSync).flatMap((d) =>
    readdirSync(d).filter((f) => /^agent-.*\.ya?ml$/.test(f) || /^action\.ya?ml$/.test(f)).map((f) => path.join(d, f)));
  assert.ok(files.length > 0, "no agent workflow found to check");
  const problems = files.flatMap((f) => earlyExitReaders(readFileSync(f, "utf8")).map((p) => `${path.relative(root, f)}:${p.line}: ${p.why}: ${p.text}`));
  assert.deepEqual(problems, []);
});
