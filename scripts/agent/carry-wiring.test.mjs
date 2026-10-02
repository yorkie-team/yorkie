// The carry/reuse path is decided in tested scripts, but it only takes effect
// through the panel workflow's step conditions — and a condition that drifted
// would either re-review every carried round (silently, at full cost) or, worse,
// skip the panel with nothing written in its place. These pin the wiring.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SRC = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-review-panel.yml"), "utf8");

/** The `review-panel` job's text, with full-line comments stripped. */
const JOB = (() => {
  const start = SRC.indexOf("\n  review-panel:\n");
  const end = SRC.indexOf("\n  promote:\n", start);
  assert.ok(start > 0 && end > start, "could not find the review-panel job");
  return SRC.slice(start, end).split("\n").filter((l) => !/^\s*#/.test(l)).join("\n");
})();

/** One step's text, from its `- name:` line to the next step. */
function step(name) {
  const at = JOB.indexOf(`      - name: ${name}\n`);
  assert.ok(at >= 0, `no step named ${JSON.stringify(name)}`);
  const next = JOB.indexOf("\n      - ", at + 1);
  return { at, text: JOB.slice(at, next < 0 ? undefined : next) };
}

test("carry wiring: the steps run in the order the decision needs", () => {
  const order = [
    "Fingerprint the PR diff",
    "Resolve review scope",
    "Carry the prior verdicts",
    // AFTER the carry: on a reuse the source IS this head, and an in_progress
    // run created first would be the newest run per lens.
    "Mark lens checks in progress",
    "Run review panel",
    "Post per-lens check runs",
  ].map((n) => [n, step(n).at]);
  for (let i = 1; i < order.length; i++) {
    assert.ok(order[i - 1][1] < order[i][1], `${order[i - 1][0]} must run before ${order[i][0]}`);
  }
});

test("carry wiring: only a successful carry skips the panel", () => {
  // `!= 'true'`, never `== 'false'`: a crashed or skipped carry step leaves the
  // output EMPTY, and the panel must still run.
  for (const name of ["Run review panel", "Mark lens checks in progress", "Record the deferred findings", "Post the deferred-findings check run", "Post this round's findings"]) {
    assert.match(step(name).text, /steps\.carry\.outputs\.ok != 'true'/, `${name} must skip only on ok == 'true'`);
  }
  const carry = step("Carry the prior verdicts").text;
  assert.match(carry, /continue-on-error: true/, "a bug in the carry must cost a review, not a blocked PR");
  assert.match(carry, /steps\.scope\.outputs\.mode == 'carry' \|\| steps\.scope\.outputs\.mode == 'reuse'/);
});

test("carry wiring: the fingerprint reaches both the scope decision and the stamped state", () => {
  assert.match(step("Resolve review scope").text, /--fingerprint "\$DIFF_FP"/);
  assert.match(step("Run review panel").text, /--diff-fingerprint "\$DIFF_FP"/);
  assert.match(step("Carry the prior verdicts").text, /--fingerprint "\$DIFF_FP"/);
  // Unfiltered: a generated-file change must break the fingerprint too.
  const fp = step("Fingerprint the PR diff").text;
  assert.match(fp, /git patch-id --verbatim/);
  assert.doesNotMatch(fp, /:\(exclude\)/);
});

test("carry wiring: the post step publishes the carried findings verbatim and labels the title", () => {
  const post = step("Post per-lens check runs").text;
  assert.match(post, /carried-text\.json/);
  // Read ONLY on a carried round: files a half-finished carry left behind must
  // never replace the findings a reviewed round just produced.
  assert.match(post, /if \(carriedFrom\) \{\s+try \{ carriedText = fs\.readFileSync\(`\.agent-review\/\$\{lens\.id\}\/carried-text\.json`/);
  assert.match(post, /CARRIED_FROM: \$\{\{ steps\.carry\.outputs\.ok == 'true' && steps\.scope\.outputs\.source \|\| '' \}\}/);
  assert.match(post, /\(carried from \$\{carriedFrom\.slice\(0, 12\)\}\)/);
});
