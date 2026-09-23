import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { decideProtection } from "./branch-protection.mjs";

// This gate decides whether a bot may open pull requests against this
// repository. It previously lived as ~60 lines inside a `github-script` block
// and shipped with an identifier referenced and never declared, so three of its
// four refusal paths threw a ReferenceError instead of printing the diagnosis.
// It failed closed, which is the right direction, and told nobody why — through
// five review rounds, because the only thing that ever read it was a regex
// asserting somebody had typed the right characters. Every test here EXECUTES
// it.

const ok = (over = {}) => ({
  classic: {
    required_pull_request_reviews: {
      required_approving_review_count: 1,
      require_last_push_approval: true,
      ...over,
    },
  },
  rules: [],
});

test("classic protection passes only when it actually guarantees a fresh human approval", () => {
  assert.equal(decideProtection(ok()).ok, true);

  // No review required at all.
  assert.equal(decideProtection(ok({ required_approving_review_count: 0 })).ok, false);

  // AN APPROVAL THAT OUTLIVES THE COMMIT IT APPROVED IS NOT A CONTROL. The
  // PR-side `fix` and `loop` verbs push to agent branches after review, so
  // without `require_last_push_approval` a human approval given on commit A
  // stays valid for every commit the bot pushes afterwards.
  const stale = decideProtection(ok({ require_last_push_approval: false }));
  assert.equal(stale.ok, false);
  assert.match(stale.reason, /re-approval after the last push/);

  // Someone may merge without the approval.
  for (const who of ["users", "teams", "apps"]) {
    const v = decideProtection(ok({ bypass_pull_request_allowances: { [who]: [{ id: 1 }] } }));
    assert.equal(v.ok, false, who);
    assert.match(v.reason, /may bypass it/);
  }
});

test("a question it could not ask is never answered 'unprotected'", () => {
  // 403 = the App lacks Administration: read. Reporting that as "main is not
  // protected" sends a maintainer to fix a setting that is already correct.
  const denied = decideProtection({ classicError: { status: 403 } });
  assert.equal(denied.ok, false);
  assert.match(denied.reason, /could not be read \(HTTP 403\).*Administration: read/s);

  // 404 is a real answer — no classic protection — and falls through to rulesets.
  const none = decideProtection({ classic: null, classicError: { status: 404 }, rules: [] });
  assert.equal(none.ok, false);
  assert.match(none.reason, /not protected by a required human review/);

  // The rules listing itself failing is not an answer either.
  const blind = decideProtection({ classic: null, classicError: { status: 404 }, rules: null });
  assert.equal(blind.ok, false);
  assert.match(blind.reason, /could not be listed at all/);
});

test("a ruleset counts only when every part of it is visible and binding", () => {
  const ruleset = (over = {}) => ({
    rules: [{ type: "pull_request", ruleset_id: 7 }],
    rulesets: [{
      id: 7,
      ruleset: {
        name: "main",
        enforcement: "active",
        bypass_actors: [],
        current_user_can_bypass: "never",
        ...over,
      },
    }],
    classic: null,
    classicError: { status: 404 },
  });
  assert.equal(decideProtection(ruleset()).ok, true);

  // ABSENCE IS NOT EMPTINESS. GitHub omits `bypass_actors` when the caller may
  // not see it — the normal case for a repository-scoped token reading an
  // organization-sourced ruleset — so treating a missing list as an empty one
  // made the ruleset most likely to exempt this App report itself unexempted.
  const hidden = ruleset();
  delete hidden.rulesets[0].ruleset.bypass_actors;
  assert.equal(decideProtection(hidden).ok, false);

  assert.equal(decideProtection(ruleset({ bypass_actors: [{ actor_id: 1 }] })).ok, false);
  assert.equal(decideProtection(ruleset({ current_user_can_bypass: "always" })).ok, false);
  assert.equal(decideProtection(ruleset({ enforcement: "evaluate" })).ok, false);

  // One unreadable ruleset must not be reported as "no protection exists".
  const unreadable = ruleset();
  unreadable.rulesets[0].ruleset = null;
  const v = decideProtection(unreadable);
  assert.equal(v.ok, false);
  assert.match(v.reason, /this token cannot read/);
});

test("every refusal produces a reason — the defect that started this", () => {
  // The inline version threw on three of four refusal paths. Exercise every
  // branch and require a non-empty, actionable string from each.
  const cases = [
    { classicError: { status: 403 } },
    { classic: null, classicError: { status: 404 }, rules: null },
    { classic: null, classicError: { status: 404 }, rules: [] },
    ok({ require_last_push_approval: false }),
    ok({ bypass_pull_request_allowances: { apps: [{ id: 1 }] } }),
    ok({ required_approving_review_count: 0 }),
    {},
  ];
  for (const input of cases) {
    const v = decideProtection(input);
    assert.equal(typeof v.reason, "string", JSON.stringify(input));
    if (!v.ok) assert.ok(v.reason.length > 20, `unhelpful refusal for ${JSON.stringify(input)}`);
  }
});

test("the workflow calls the module rather than carrying its own copy", () => {
  // The whole point of the extraction. An inline reimplementation would be
  // untested again by construction.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const wf = readFileSync(
    path.join(HERE, "..", "..", ".github", "workflows", "agent-implement.yml"),
    "utf8",
  );
  assert.match(wf, /node \.\/scripts\/agent\/check-protection\.mjs main/);
  assert.ok(
    !/getBranchProtection/.test(wf),
    "the workflow must not carry its own copy of the protection logic",
  );
});
