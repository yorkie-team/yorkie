// The credential probe and the infra refund are decided in tested scripts, but
// their safety rests on WHERE the workflow runs them. These pin that.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SRC = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-review-panel.yml"), "utf8")
  .split("\n").filter((l) => !/^\s*#/.test(l)).join("\n");

function job(name) {
  const start = SRC.indexOf(`\n  ${name}:\n`);
  assert.ok(start >= 0, `no job ${name}`);
  const next = SRC.slice(start + 1).search(/\n {2}[a-z][a-z-]*:\n/);
  return next < 0 ? SRC.slice(start) : SRC.slice(start, start + 1 + next);
}
function step(jobText, name) {
  const at = jobText.indexOf(`      - name: ${name}\n`);
  assert.ok(at >= 0, `no step named ${JSON.stringify(name)}`);
  const next = jobText.indexOf("\n      - ", at + 1);
  return { at, text: jobText.slice(at, next < 0 ? undefined : next) };
}

test("the probe holds every pool secret, so it runs before any branch code is on disk", () => {
  const fix = job("fix");
  const probe = step(fix, "Pick a live fixer credential");
  assert.match(probe.text, /--probe/);
  for (const n of ["", "_1", "_8"]) {
    assert.match(probe.text, new RegExp(`CLAUDE_CODE_OAUTH_TOKEN${n}: \\$\\{\\{ secrets\\.CLAUDE_CODE_OAUTH_TOKEN${n} \\}\\}`));
  }
  // TRUSTED copy, staged from main before the checkout.
  assert.match(probe.text, /"\$RUNNER_TEMP\/agent-tools\/pick-fix-credential\.mjs"/);
  const branchCheckout = fix.indexOf("ref: ${{ github.event.workflow_run.head_branch }}");
  assert.ok(branchCheckout > 0, "could not find the branch checkout");
  assert.ok(probe.at < branchCheckout, "the probe must run before the branch is checked out");
  assert.ok(probe.at < step(fix, "Generate the agent's narrow token").at, "and before the agent's token exists");
  assert.ok(step(fix, "Stage the trusted agent scripts").at < probe.at, "after the trusted scripts are staged");
  // The round is still recorded after the probe has decided, and only if it found one.
  const record = step(fix, "Record the fix-round dispatch");
  assert.ok(probe.at < record.at);
  assert.match(record.text, /steps\.cred\.outputs\.available != 'false'/);
});

test("an infra failure is paged by fix-report with its cause, and `stalled` stands down for it", () => {
  const report = job("fix-report");
  const infra = step(report, "Page an infrastructure failure");
  // Only a FAILED fixer that is KNOWN to have pushed nothing — an unread head is
  // `stalled`'s case, paged generically as before.
  assert.match(infra.text, /needs\.fix\.outputs\.fixer == 'failure' &&\s+steps\.after\.outcome == 'success' && steps\.after\.outputs\.advanced == 'false'/);
  // `steps.after` swallows a failed `gh api` read (`|| echo ''`) and then says
  // `advanced=false`, so its outcome alone cannot prove "nothing was pushed":
  // both heads must have been READ.
  assert.match(infra.text, /steps\.after\.outputs\.sha != '' && needs\.fix\.outputs\.before != ''/);
  // A latch the gate believes: github-actions[bot].
  assert.match(infra.text, /GH_TOKEN: \$\{\{ secrets\.GITHUB_TOKEN \}\}/);
  assert.doesNotMatch(infra.text, /continue-on-error/, "a page that failed to post must red the job for `stalled`");
  assert.match(report, /infra_paged: \$\{\{ steps\.infra\.outputs\.paged \}\}/);
  // `stalled` still pages a failed fixer — unless fix-report already did.
  const stalled = job("stalled");
  assert.match(stalled, /\(needs\.fix\.result == 'failure' && needs\.fix-report\.outputs\.infra_paged != 'true'\)/);
  assert.match(stalled, /needs\.fix-report\.result == 'failure'/);
});
