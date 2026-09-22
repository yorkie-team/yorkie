import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { checkPassed, allRequiredPassed, ciRunDecision, ciConclusion, ciRunToRerun, ciRunToAwait, definesCi, CHECK_PRODUCER_APP_SLUG, CI_DEFINING_PATHS, CI_WORKFLOW_PATH, CI_WORKFLOW_FILE, DEFAULT_REVIEW_CHECKS } from "./checks.mjs";

// `DEFAULT_REVIEW_CHECKS` is the ONE lens list in the repo that does not derive
// itself from lenses.json, so it is the one that silently rots when a lens is
// added. Assert it against the REAL manifest — this test is the reason the
// constant lives in checks.mjs at all (mark-ready.mjs is a CLI with top-level
// process.exit and cannot be imported).
test("DEFAULT_REVIEW_CHECKS covers every ALWAYS-APPLICABLE blocking lens", () => {
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const lenses = JSON.parse(readFileSync(path.join(HERE, "lenses", "lenses.json"), "utf8"));
  const isBlocking = (l) => String(l.gating ?? "blocking") === "blocking";
  const alwaysOn = (l) => {
    const g = l.appliesWhen ?? ["**"];
    return g.length === 0 || g.includes("**");
  };
  // This used to require EVERY blocking lens, which made the default gate
  // unsatisfiable for any PR that legitimately skipped a path-scoped one: a
  // skipped lens posts `neutral`, and checkPassed only accepts `success`. So a
  // docs-only PR could never satisfy agent-review-test-adequacy, and once a
  // `docs` lens existed, a code-only PR could never satisfy agent-review-docs
  // either. Only the always-on lenses belong here.
  assert.deepEqual(
    [...DEFAULT_REVIEW_CHECKS].sort(),
    lenses.filter((l) => isBlocking(l) && alwaysOn(l)).map((l) => `agent-review-${l.id}`).sort(),
    "DEFAULT_REVIEW_CHECKS must list exactly the blocking lenses whose appliesWhen is '**'",
  );
  // A path-scoped lens here would reintroduce the unsatisfiable-gate bug.
  for (const l of lenses.filter((x) => isBlocking(x) && !alwaysOn(x))) {
    assert.ok(
      !DEFAULT_REVIEW_CHECKS.includes(`agent-review-${l.id}`),
      `path-scoped lens ${l.id} must not be a default required check — it is neutral whenever it does not apply`,
    );
  }
  assert.ok(DEFAULT_REVIEW_CHECKS.length > 0, "mark-ready fails closed on an empty required set");
  // Advisory lenses must NOT be required — the ready gate would wait forever on
  // a check that is posted as `neutral` and never `success`.
  for (const l of lenses.filter((x) => String(x.gating ?? "blocking") !== "blocking")) {
    assert.ok(
      !DEFAULT_REVIEW_CHECKS.includes(`agent-review-${l.id}`),
      `advisory lens ${l.id} must not be a required default check`,
    );
  }
});

// The `app` block is part of the fixture, not decoration: `checkPassed` refuses
// a run whose producer is not GitHub Actions, so a fixture without it asserts
// nothing about the gate it is aimed at.
const APP = { slug: CHECK_PRODUCER_APP_SLUG };
const succ = (name, t = "2026-07-21T10:00:00Z") => ({ name, conclusion: "success", started_at: t, app: APP });
const fail = (name, t = "2026-07-21T10:00:00Z") => ({ name, conclusion: "failure", started_at: t, app: APP });

test("checkPassed: a check run from ANOTHER App is not evidence", () => {
  // THE FORGERY THIS CLOSES. A check-run NAME is not reserved: any integration
  // installed on the repo may create `agent-review-security` and conclude it
  // `success`, so gate 2 — "an independent reviewer approved this" — was
  // satisfiable by anything holding a Checks API token. `app.slug` is set by
  // GitHub from the installation and cannot be chosen by the app reporting it.
  const forged = (name, t) => ({ name, conclusion: "success", started_at: t, app: { slug: "some-other-app" } });

  assert.equal(checkPassed([forged("a", "2026-07-21T10:00:00Z")], "a"), false, "a green run from another App is not a pass");
  // ...and it must be dropped BEFORE the newest-wins sort, or a forged run with
  // a later timestamp shadows the real verdict.
  assert.equal(
    checkPassed([succ("a", "2026-07-21T10:00:00Z"), forged("a", "2026-07-21T23:00:00Z")], "a"),
    true,
    "the real (older) run still speaks for the lens",
  );
  assert.equal(
    checkPassed([fail("a", "2026-07-21T10:00:00Z"), forged("a", "2026-07-21T23:00:00Z")], "a"),
    false,
    "a forged run must not overturn a real failure",
  );
  // A run with no `app` at all is not evidence either — fail closed on a
  // response shape the reader does not recognise.
  assert.equal(checkPassed([{ name: "a", conclusion: "success", started_at: "2026-07-21T10:00:00Z" }], "a"), false);
  assert.equal(checkPassed([{ name: "a", conclusion: "success", app: {} }], "a"), false);
  // And the same through the aggregate the ready gate actually calls.
  assert.equal(allRequiredPassed([forged("x", "2026-07-21T10:00:00Z")], ["x"]).allPassed, false);
});

test("checkPassed: missing check → false; latest run wins", () => {
  assert.equal(checkPassed([], "a"), false);
  assert.equal(checkPassed([succ("a")], "a"), true);
  assert.equal(checkPassed([fail("a")], "a"), false);
  // fail then success (newer) → passed
  assert.equal(checkPassed([fail("a", "2026-07-21T10:00:00Z"), succ("a", "2026-07-21T11:00:00Z")], "a"), true);
  // success then fail (newer) → not passed
  assert.equal(checkPassed([succ("a", "2026-07-21T10:00:00Z"), fail("a", "2026-07-21T11:00:00Z")], "a"), false);
});

test("allRequiredPassed: all present+success → pass; any failing or MISSING → block", () => {
  const req = ["x", "y", "z"];
  assert.equal(allRequiredPassed([succ("x"), succ("y"), succ("z")], req).allPassed, true);
  assert.equal(allRequiredPassed([succ("x"), fail("y"), succ("z")], req).allPassed, false);
  // partial set: 'z' never posted → block
  const partial = allRequiredPassed([succ("x"), succ("y")], req);
  assert.equal(partial.allPassed, false);
  assert.equal(partial.perCheck.z, false);
});

test("allRequiredPassed: an EMPTY required set is vacuously true", () => {
  // `[].every` is true, so a required set of [] "passes" with ZERO evidence.
  // This is the fail-open mark-ready.mjs guards against: it refuses to promote
  // on an empty required-check set unless --allow-no-checks is passed.
  assert.equal(allRequiredPassed([], []).allPassed, true);
  assert.equal(allRequiredPassed([fail("x")], []).allPassed, true);
});

// --- the CI conclusion, read late instead of at trigger time ---------------

test("ciRunDecision: proceed only on a COMPLETED success", () => {
  assert.equal(ciRunDecision({ status: "completed", conclusion: "success" }), "proceed");
  // Still running, in every spelling GitHub uses. `wait` is not `proceed`: a
  // caller that treated it as one would push a review fix while CI is mid-flight
  // and the CI arm might still claim this branch.
  for (const status of ["queued", "in_progress", "requested", "waiting", "pending"]) {
    assert.equal(ciRunDecision({ status, conclusion: null }), "wait", status);
  }
  // A conclusion that arrives before `completed` does not shortcut the wait —
  // status is the authority on whether the run is over.
  assert.equal(ciRunDecision({ status: "in_progress", conclusion: "success" }), "wait");
});

test("ciRunDecision: everything that is not a success is a skip, not a page", () => {
  // The pushing jobs stay out of it, which leaves the PR exactly where the old
  // `conclusion == 'success'` trigger left it. `failure` in particular is NOT a
  // stall — agent-iterate-ci.yml fires on precisely that and owns the branch.
  for (const conclusion of ["failure", "cancelled", "timed_out", "neutral", "action_required", "stale", "skipped", null, undefined, ""]) {
    assert.equal(ciRunDecision({ status: "completed", conclusion }), "skip", String(conclusion));
  }
  // Junk fails CLOSED. This value comes from an API response the workflow does
  // not control, and the expensive, irreversible thing downstream is a push.
  for (const junk of [null, undefined, "completed", 7, [], true]) {
    assert.equal(ciRunDecision(junk), "skip", JSON.stringify(junk) ?? "undefined");
  }
  // An object with nothing on it is not "still running" — no status means no
  // evidence, and no evidence must not become an indefinite poll.
  assert.equal(ciRunDecision({}), "wait");
});

test("ciRunDecision: the three outcomes are exhaustive and disjoint", () => {
  // The YAML mirrors this rule inline (github-script steps cannot import local
  // modules — no checkout in that job), so the contract has to be pinned
  // somewhere runnable. A fourth return value would silently mean "not
  // proceed, not wait" to a caller written against three.
  const seen = new Set();
  for (const status of ["completed", "in_progress", "queued", undefined]) {
    for (const conclusion of ["success", "failure", null, undefined]) {
      seen.add(ciRunDecision({ status, conclusion }));
    }
  }
  seen.add(ciRunDecision(null));
  assert.deepEqual([...seen].sort(), ["proceed", "skip", "wait"]);
});

test("the panel starts with CI, admits re-runs, and mirrors ciRunDecision", () => {
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const yml = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-review-panel.yml"), "utf8");
  // Code lines only. The comments around this change quote both the old
  // `types: [completed]` trigger and the `conclusion == 'success'` clause it
  // replaced, to say what moved where — and a whole-file grep would read that
  // explanation as the thing it warns about. Same trap as #630 and #640.
  const code = yml.split("\n").filter((l) => !/^\s*(#|\/\/)/.test(l)).join("\n");

  // `requested` keeps the panel starting when CI STARTS — the latency property.
  assert.match(code, /types: \[requested/, "the panel must start when CI starts");
  // `completed` is subscribed too, and this used to be forbidden here on the
  // grounds that both events fire for every run and the panel costs ~$12. That
  // reasoning held for a FRESH run and missed re-runs entirely: `requested` fires
  // when a run is CREATED, so a re-run emits only `completed` — and `@claude
  // rerun`'s whole mechanism is `reRunWorkflow` on the PR's CI run. With
  // `requested` alone it re-ran CI and re-engaged nothing, twice, on #632 and #648,
  // while reporting that the panel would run again.
  assert.match(code, /types: \[requested, completed\]/, "re-runs emit only `completed`");
  // What actually prevents the doubling is the gate, not the subscription: a fresh
  // run's `completed` carries run_attempt 1 and is refused, so exactly one panel
  // starts per CI run either way.
  assert.match(
    code,
    /github\.event\.action == 'requested' \|\|\s*\n?\s*github\.event\.workflow_run\.run_attempt > 1/,
    "the gate must admit `completed` only for re-runs, or every round doubles",
  );

  // THE CONCURRENCY GROUP MUST AGREE WITH THE GATE, and they are written
  // separately — a suffix expression and a job `if:` — so this evaluates both.
  //
  // Why it matters more than tidiness: `concurrency` is claimed at RUN CREATION,
  // before any `if:` runs. With one undifferentiated group, a fresh CI run's
  // `completed` event creates a second run that cancels the panel the `requested`
  // event started ~13 minutes earlier, mid-review — then refuses the event itself
  // and skips every job. No verdicts, and `stalled` is `!cancelled()` so no page.
  // The review control would stop working, silently, on every PR.
  //
  // So refused runs must land in a DIFFERENT group from working ones, and "refused"
  // has to mean the same thing in both places.
  {
    const gateIf = (yml.match(/^ {2}gate:\n(?:.*\n)*? {4}if: >-\n((?: {6}.*\n)+)/m) || [])[1];
    assert.ok(gateIf, "could not extract the gate's if: expression");
    // `.+` not `\S+`: the group is one folded line containing spaces inside `${{ }}`.
    const group = (yml.match(/group: >-\n\s*(.+)/) || [])[1];
    assert.ok(group, "could not extract the concurrency group");

    const toJs = (s) =>
      s.replace(/github\.event\.workflow_run\.run_attempt/g, "ATTEMPT")
       .replace(/github\.event\.action/g, "ACTION")
       .replace(/github\.event\.workflow_run\.head_repository\.full_name/g, "'r'")
       .replace(/github\.repository/g, "'r'")
       .replace(/github\.event\.workflow_run\.head_branch/g, "'b'")
       .replace(/github\.event\.workflow_run\.path/g, "PATH")
       .replace(/vars\.AGENT_PIPELINE_ENABLED/g, "'true'");
    const admits = new Function("ACTION", "ATTEMPT", "PATH", `return (${toJs(gateIf)});`);
    const suffix = (group.match(/\$\{\{ ([^}]*'noop'[^}]*) \}\}\s*$/) || [])[1];
    assert.ok(suffix, "the group must carry a noop/active partition suffix");
    const partition = new Function("ACTION", "ATTEMPT", "PATH", `return (${toJs(suffix)});`);

    // A run produced by a DIFFERENT file that merely calls itself "CI" is in the
    // matrix too: the trigger's `workflows:` filter matches display names, so
    // such a run reaches this workflow and — because `concurrency` is claimed at
    // run creation, before any `if:` — could otherwise take the `active` group
    // and cancel a legitimate panel mid-review while its own gate refuses it.
    const paths = [CI_WORKFLOW_PATH, ".github/workflows/pwn.yml"];
    for (const wfPath of paths) {
      for (const [action, attempt] of [["requested", 1], ["requested", 2], ["completed", 1], ["completed", 2]]) {
        const works = Boolean(admits(action, attempt, wfPath));
        const lane = partition(action, attempt, wfPath);
        assert.equal(
          lane,
          works ? "active" : "noop",
          `${wfPath} ${action}/attempt ${attempt}: gate ${works ? "admits" : "refuses"} but the group says ${lane}`,
        );
      }
    }
    // ...and the path clause must actually be doing something in both places.
    assert.ok(
      !admits("requested", 1, ".github/workflows/pwn.yml"),
      "the gate must refuse a run from any file other than the CI workflow",
    );
    assert.equal(
      partition("requested", 1, ".github/workflows/pwn.yml"),
      "noop",
      "a forged run must not share the concurrency group that real panels cancel each other in",
    );
    // And prove the partition is not degenerate — a group that always says "active"
    // would pass a same-answer check while restoring the cancellation bug.
    assert.equal(partition("requested", 1, CI_WORKFLOW_PATH), "active");
    assert.equal(partition("completed", 1, CI_WORKFLOW_PATH), "noop");
  }

  // The inline copy of the rule. A `github-script` step has no checkout and
  // cannot import checks.mjs, so this logic necessarily exists twice; pinning
  // the copy is what keeps it ONE rule rather than two that drift.
  assert.match(code, /if \(!run \|\| typeof run !== 'object' \|\| Array\.isArray\(run\)\) return 'skip';/);
  assert.match(code, /if \(run\.status !== 'completed'\) return 'wait';/);
  assert.match(code, /return run\.conclusion === 'success' \? 'proceed' : 'skip';/);

  // Both PUSHING jobs consume it. This is the mutex the old trigger enforced:
  // agent-iterate-ci.yml owns a red CI, these two own a green one, and exactly
  // one of them proceeds per CI run. A job that pushed without this clause could
  // commit to a branch the CI-fix arm is also committing to.
  const gated = code.match(/needs\.ci\.outputs\.conclusion == 'success'/g) || [];
  assert.equal(gated.length, 2, "promote and fix must each require a green CI");
  for (const job of ["promote", "fix"]) {
    assert.match(code, new RegExp(`${job}:\\n(.|\\n)*?needs: \\[review-panel, ci\\]`),
      `${job} must depend on the ci job`);
  }
});

// A FIX ROUND KILLED BY ITS OWN 45-MINUTE WALL MUST STILL CONVERGE THE PR.
//
// `timeout-minutes` makes GitHub report the job `cancelled`, not `failure`, and
// both stall detectors read `failure`: the `stalled` net tests
// `needs.fix.result == 'failure'` behind a `!cancelled()`, and the no-commit
// page used to carry nothing but `steps.guard.outputs.proceed == 'true'` — which
// keeps GitHub's implicit `success()`, false once the step before it was
// cancelled. So the `agent:fixing` label written fourteen steps earlier stayed,
// and since that label is what the pipeline reads as "a round is in flight",
// nothing re-triggered. #1047, #1052 and #1053 all stopped there on 2026-09-08,
// silently, after #1042 did on 2026-09-07.
//
// The condition is the whole fix, so it is pinned here as a truth table rather
// than a grep: every row is a real outcome the `fix` job produces, and the two
// that must NOT page are what keeps this from double-paging against `stalled`.
test("the no-commit page fires on a timed-out fixer, and only where `stalled` won't", () => {
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const yml = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-review-panel.yml"), "utf8");

  // The step's own block, so a same-named phrase in a comment elsewhere in this
  // 2.4k-line file cannot satisfy the assertions below.
  const block = (name) => {
    const at = yml.indexOf(`- name: ${name}\n`);
    assert.ok(at > 0, `no step named ${name}`);
    const rest = yml.slice(at);
    const end = rest.slice(1).search(/\n {6}- (name|uses):/);
    return end === -1 ? rest : rest.slice(0, end + 1);
  };

  // `steps.fixer.outcome` reads empty unless the fixer step is actually
  // ID'd — the condition would then be vacuously false on every timeout and
  // this whole fix would be a no-op that still passes a grep for the outcome.
  assert.match(block("Address panel findings"), /^\s*id: fixer$/m,
    "the fixer step must carry `id: fixer` for its outcome to be readable");

  const cond = (block("Page if the fix produced no commit").match(/^\s*if: >-\n((?: {9,}.*\n)+)/m) || [])[1];
  assert.ok(cond, "could not extract the no-commit page's if: expression");

  // `always()` and not `!failure()`: the runner is only documented to keep
  // running a step through a cancellation when the condition is always-true, and
  // this step has to run precisely when the job is being cancelled.
  assert.match(cond, /always\(\)/, "the step must survive the job's own cancellation");

  const pages = new Function("PROCEED", "FIXER", "CRED", `return Boolean(${cond
    .replace(/always\(\)/g, "true")
    .replace(/steps\.guard\.outputs\.proceed/g, "PROCEED")
    .replace(/steps\.fixer\.outcome/g, "FIXER")
    .replace(/steps\.cred\.outputs\.available/g, "CRED")});`);

  for (const [why, args, expected] of [
    ["the fixer finished — the head check decides", ["true", "success", "true"], true],
    // THE REGRESSION this step's condition was rewritten for.
    ["the job's own wall killed the fixer", ["true", "cancelled", "true"], true],
    ["the wall hit and the credential picker was absent", ["true", "cancelled", ""], true],
    // Every row below reports `outcome: skipped` or `failure`, and each already has
    // a pager that says something TRUER than this step could. Paging here as well
    // would comment twice and latch `agent:blocked` from two places.
    ["the fixer hard-errored — `stalled` pages", ["true", "failure", "true"], false],
    ["a setup step failed, so the fixer was skipped — `stalled` pages", ["true", "skipped", ""], false],
    // The dedicated no-credential page owns this one: it knows no round was spent
    // and that a usage window commonly reopens on its own, so this step's "did not
    // converge within its turn budget / re-run with @claude fix" would contradict
    // it in both cause and remedy.
    ["no live credential — its own page owns it", ["true", "skipped", "false"], false],
    ["the round guard held or paged, so no round was spent", ["false", "skipped", ""], false],
  ]) {
    assert.equal(pages(...args), expected,
      `${why}: expected the no-commit page to ${expected ? "run" : "be skipped"}`);
  }

  // ...and because it is excluded, THAT page must write the state itself. It used
  // to inherit `agent:blocked` from this step as a side effect, so removing the
  // case without moving the label would strand the no-credential path on
  // `agent:fixing` — the same silent dead-end this whole change closes, reached a
  // different way.
  assert.match(block("Page — no live credential for the fixer"),
    /set-state\.mjs" "\$PR" blocked/,
    "the no-credential page must set the state, now that the generic pager skips it");

  // A CI RE-RUN IS THE ONE SUPERSEDE THE HEAD CHECK CANNOT SEE, and paging
  // through it is worse than silence: the page body is the `<!-- agent-review-paged
  // -->` latch the `gate` job refuses every later panel run on, and `@claude rerun`
  // deletes that latch BEFORE it re-runs CI — so a page from the cancellation grace
  // window lands after the cleanup and freezes the round the operator just started.
  // Without this the fix above would trade a silent dead-end for a louder one.
  const page = block("Page if the fix produced no commit");
  assert.match(page, /CI_ATTEMPT: \$\{\{ github\.event\.workflow_run\.run_attempt \}\}/,
    "the page must know which CI attempt this round reviewed");
  assert.match(page, /NOW_ATTEMPT.*actions\/runs\/\$CI_RUN_ID.*run_attempt/s,
    "the page must read the CI run's CURRENT attempt to detect a re-run");
  assert.match(page, /"\$NOW_ATTEMPT" != "\$CI_ATTEMPT"[\s\S]*?exit 0/,
    "a re-run must suppress the page, not just be logged");
  // Reading it needs a scope the App token used for the comment does not carry.
  const fixPerms = (yml.match(/^ {2}fix:\n(?:.*\n)*? {4}permissions:\n((?: {6}.*\n)+)/m) || [])[1];
  assert.ok(fixPerms, "could not extract the fix job's permissions");
  assert.match(fixPerms, /^ {6}actions: read/m,
    "the fix job needs `actions: read` for the re-run check");

  // THE WALL'S LENGTH IS WRITTEN IN THREE PLACES and cannot be read from any
  // expression context, so a step cannot ask its own job how long it had. The page
  // states the number to a human and tells them the retry path shares it, so a
  // raise applied to one copy and not the others is a page that lies about both
  // how long the round got and how long the retry will get. Pin all three.
  const fixWall = (yml.match(/^ {2}fix:\n(?:.*\n)*? {4}timeout-minutes: (\d+)$/m) || [])[1];
  assert.ok(fixWall, "could not read the fix job's timeout-minutes");
  const stated = [...page.matchAll(/(\d+)[ -]minutes?\b/g)].map((m) => m[1]);
  assert.ok(stated.length >= 2, "the cancelled cause line must state the wall and the retry path's wall");
  for (const n of stated) {
    assert.equal(n, fixWall,
      `the page says ${n} minutes but the fix job's timeout-minutes is ${fixWall}`);
  }
  // `agent-fix.yml` is what the page tells a human to retry on, so its wall has to
  // be the one the page promises. A tighter wall there would refuse exactly the
  // rounds the loop could not finish either.
  const fixYml = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-fix.yml"), "utf8");
  const onDemandWall = (fixYml.match(/^ {2}fix:\n(?:.*\n)*? {4}timeout-minutes: (\d+)$/m) || [])[1];
  assert.equal(onDemandWall, fixWall,
    "agent-fix.yml's fix wall must match the autonomous one the page points away from");

  // And `stalled` keeps its `!cancelled()`. It is not the bug — it is what stops
  // a run cancelled by the concurrency guard from paging over a FRESHER round,
  // and it is deliberately left alone because the step above now owns the
  // timeout. Deleting it to "also catch cancelled" would restore #648's
  // spurious latch and double-page every timeout on top.
  const stalledIf = (yml.match(/^ {2}stalled:\n(?:.*\n)*? {4}if: >-\n((?: {6}.*\n)+)/m) || [])[1];
  assert.ok(stalledIf, "could not extract the stalled job's if: expression");
  assert.match(stalledIf, /!cancelled\(\)/,
    "stalled must keep `!cancelled()` — a superseded panel must not page");
  assert.ok(!/needs\.fix\.result == 'cancelled'/.test(stalledIf),
    "a cancelled fix job is the no-commit page's case; claiming it here double-pages");

  // ...but every OTHER job's `cancelled` must be listed, and this is the pair of
  // facts that makes it safe: GitHub reports a job killed by its own
  // `timeout-minutes` as `cancelled`, never `failure`, and `!cancelled()` above
  // is false whenever the WORKFLOW was cancelled — the superseded case. So a
  // `cancelled` reaching this expression is a job that hit its own wall, and
  // nothing else pages for it. The panel's is the one that matters: its wall is
  // 45 minutes against an orchestrator its own comments call a ~35 minute run.
  for (const job of ["review-panel", "deps", "ci", "promote"]) {
    assert.ok(
      new RegExp(`needs\\.${job}\\.result == 'cancelled'`).test(stalledIf),
      `a ${job} job killed by its own timeout-minutes reports 'cancelled' and would page nobody`,
    );
  }

  // And the CI conclusions nothing else owns. `agent-iterate-ci.yml` gates on
  // `conclusion == 'failure'`, so an ordinary red CI is its business — but a run
  // a maintainer cancels from the Actions UI, or one that times out, concludes
  // neither `success` nor `failure`, leaves the `ci` job GREEN, and is owned by
  // no one.
  assert.match(
    stalledIf,
    /needs\.ci\.outputs\.conclusion != 'success'/,
    "a CI run concluding neither success nor failure must page — nothing else watches for it",
  );
  assert.match(stalledIf, /needs\.ci\.outputs\.conclusion != 'failure'/,
    "...but not on an ordinary red CI, which agent-iterate-ci.yml owns");
  // The clause is only sound because `conclusion` is a declared output that the
  // `ci` job always sets when it succeeds. An undeclared one reads as "" and
  // would page on every clean run.
  assert.match(yml, /^ {4}outputs:\n {6}conclusion: /m,
    "the ci job must declare `conclusion`, or the guard above pages on every clean run");
});

// --- "@claude fix": routing, gate order, and reporting ----------------------

// Workflow text with FULL-LINE `#` comments stripped. These assertions are about
// what the workflow DOES, and every one of them first failed against a header
// comment that merely described the opposite workflow's gate — the same
// false-positive shape as matching prose for code.
const WF = (name) =>
  readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..", ".github", "workflows", name), "utf8")
    .split("\n")
    .filter((l) => !/^\s*#/.test(l))
    .join("\n");

test("the `fix` verb reaches exactly one workflow: issues -> implement, PRs -> fix", () => {
  // Both are `issue_comment` workflows keyed on the SAME verb, so the surface
  // predicates must partition rather than merely differ. If either drifted, an
  // "@claude fix" on an issue would also start the on-demand fixer (which would
  // then refuse for having no PR) or, worse, both would run on a PR.
  //
  // agent-implement.yml is the ISSUE half and is not ported here
  // (docs/design/agent-command-verbs.md defers issue → PR past every phase). The
  // half that can be asserted without it is the one that matters more: agent-fix
  // must claim PRs and ONLY PRs, so the issue half cannot collide with it when
  // it eventually lands. Asserting that unconditionally is what makes this a
  // guard rather than a note.
  const wfDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..", ".github", "workflows");
  const fix = WF("agent-fix.yml");
  assert.ok(
    /\n\s+github\.event\.issue\.pull_request &&/.test(fix),
    "agent-fix must be PR-only (an unnegated issue.pull_request guard)",
  );
  assert.equal(/!github\.event\.issue\.pull_request/.test(fix), false, "agent-fix must not also claim issues");

  const pair = [["fix", fix]];
  if (existsSync(path.join(wfDir, "agent-implement.yml"))) {
    const implement = WF("agent-implement.yml");
    assert.match(implement, /!github\.event\.issue\.pull_request/, "implement must be ISSUE-only");
    pair.push(["implement", implement]);
  }
  for (const [name, wf] of pair) {
    assert.match(wf, /command\.mjs "\$BODY"/, `${name} must route through the shared command parser`);
    assert.match(wf, /AGENT_PIPELINE_ENABLED == 'true'/, `${name} must honour the pipeline kill switch`);
  }
});

test("nothing runs on an App token without first checking the App exists", () => {
  // ONE SWITCH, TWO CREDENTIAL REGIMES. `AGENT_PIPELINE_ENABLED` turns every
  // workflow here on at once, but `review` and `summarize` post with
  // GITHUB_TOKEN and need no App — so a repository legitimately runs those
  // while `AGENT_APP_ID` is still unset.
  //
  // In that state an unguarded mint FAILS, and the failure is not contained:
  // `agent-loop`/`agent-rerun` die at their second step, leaving a red X in
  // Actions and nothing in the PR thread for the person who typed the verb;
  // and the panel's `promote` job failing makes `stalled` page a human and
  // write the terminal `agent:blocked` latch on an otherwise clean PR. An
  // absent credential would be reported as the loop giving up.
  //
  // So every step that consumes an App token must be conditional on something —
  // usually the `app.outputs.configured` check, sometimes an eligibility or
  // placeholder gate that already implies it. The assertion is deliberately
  // "has a condition", not "has THIS condition": the shapes differ per
  // workflow, and what must never exist is an unconditional consumer.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  const offenders = [];
  let checked = 0;

  for (const file of readdirSync(dir).filter((f) => f.startsWith("agent-") && f.endsWith(".yml"))) {
    const lines = readFileSync(path.join(dir, file), "utf8").split("\n");
    // Walk steps: a step starts at `      - ` and runs to the next one.
    for (let i = 0; i < lines.length; i++) {
      if (!/^ {6}- /.test(lines[i])) continue;
      let end = i + 1;
      while (end < lines.length && !/^ {6}- /.test(lines[end]) && !/^ {2}\S/.test(lines[end])) end++;
      const step = lines.slice(i, end);
      const code = step.filter((l) => !/^\s*#/.test(l));
      if (!code.some((l) => l.includes("steps.app-token.outputs.token"))) continue;
      checked++;
      if (!code.some((l) => /^\s+if:/.test(l))) {
        offenders.push(`${file}:${i + 1} ${step[0].trim()}`);
      }
    }
  }

  assert.ok(checked > 0, "no App-token consumer found — this guard would be vacuous");
  assert.deepEqual(
    offenders,
    [],
    `these steps consume an App token unconditionally:\n  ${offenders.join("\n  ")}`,
  );
});

test("each verb that needs the App answers the commenter when it is missing", () => {
  // The guard above stops the job dying; this one stops it dying SILENTLY. A
  // verb typed by a maintainer must produce a comment either way — a skipped
  // job with a green tick reads as "handled" for a request nothing acted on.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  for (const file of ["agent-loop.yml", "agent-rerun.yml", "agent-fix.yml"]) {
    const text = readFileSync(path.join(dir, file), "utf8");
    assert.match(text, /id: app\b/, `${file}: no App-presence check`);
    assert.match(
      text,
      /needs the `yorkie-team-agent` GitHub App/,
      `${file}: must tell the commenter the App is missing, not just skip`,
    );
    // ...and it must come after the trust gate, so an account without write
    // access cannot make the bot post.
    assert.ok(
      text.indexOf("getCollaboratorPermissionLevel") < text.indexOf("id: app\n"),
      `${file}: the App check must follow the permission check`,
    );
  }
});

test("every App-token mint is pinned and narrowed, and none can push workflows", () => {
  // THE GUARANTEE THIS ENFORCES IS ONE THE PIPELINE PRINTS TO USERS.
  // agent-fix.yml's refusal message tells a contributor the token is minted
  // without `workflows: write` "so a fix agent can never rewrite the lanes that
  // grade it". That is a property of every mint, and it was true of four of the
  // six: the CI arm and the reply arm were ported with no `permission-*` lines
  // at all, so their tokens carried the App installation's FULL scope into the
  // two jobs that check out untrusted branch code and run an agent beside the
  // token. Nothing would have caught the seventh.
  //
  // Three properties per mint:
  //   1. SHA-pinned. This action handles the App private key, so a movable tag
  //      is a supply-chain hole with the worst possible payload.
  //   2. Narrowed at all. An omitted permission block is not "the defaults" —
  //      it is everything the installation was granted.
  //   3. No `workflows` permission, which is the sentence above.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  let mints = 0;

  for (const file of readdirSync(dir).filter((f) => f.endsWith(".yml"))) {
    const text = readFileSync(path.join(dir, file), "utf8");
    const lines = text.split("\n");
    for (let i = 0; i < lines.length; i++) {
      const m = /uses: actions\/create-github-app-token@(\S+)/.exec(lines[i]);
      if (!m) continue;
      mints++;
      assert.match(
        m[1],
        /^[0-9a-f]{40}$/,
        `${file}:${i + 1}: the App-token action must be pinned to a commit SHA, got ${m[1]}`,
      );
      // The step's `with:` block: to the next line at or left of this step's
      // own indent that starts a new step.
      const indent = lines[i].search(/\S/);
      let end = i + 1;
      while (end < lines.length) {
        const l = lines[end];
        if (l.trim() && l.search(/\S/) <= indent && /^\s*- /.test(l)) break;
        end++;
      }
      const step = lines.slice(i, end).filter((l) => !/^\s*#/.test(l)).join("\n");
      const perms = [...step.matchAll(/^\s*permission-([a-z-]+):/gm)].map((x) => x[1]);
      assert.ok(
        perms.length > 0,
        `${file}:${i + 1}: mints an App token with no permission-* narrowing — it carries the installation's full scope`,
      );
      assert.ok(
        !perms.includes("workflows"),
        `${file}:${i + 1}: grants permission-workflows, which is exactly what agent-fix.yml promises users no agent token can do`,
      );
    }
  }

  assert.ok(mints >= 4, `expected the App-token mints to be found, saw ${mints}`);
});

test("every job that runs a pipeline script pins its Node", () => {
  // REGRESSION GUARD, for a mistake made twice in one branch.
  //
  // Swapping the fixer jobs from a package manager to Go meant deleting their
  // `setup-node` steps. The edit matched on the step name and removed every one
  // in agent-review-panel.yml — but only one belonged to the fixer. The others
  // served the job that runs `npm ci`, the job that runs the panel, and the
  // promotion gate; two more went the same way in the `fix` and `stalled` jobs.
  // Nothing failed: the YAML stayed valid, the suite stayed green, and those
  // jobs would have run on whatever Node the runner image happens to ship.
  //
  // PER JOB, not per file, because that is the granularity the bug had: the
  // panel kept a `setup-node` the whole time and still had three jobs without
  // one.
  //
  // ONE EXEMPTION, and it is a property rather than a list of files: a job whose
  // only scripts import nothing outside `node:` builtins runs correctly on the
  // runner's own Node, which is why the routers and the loop/rerun/summarize
  // arms carry no setup step. The allow-list below is checked against the real
  // module graph by the test after this one, so a script that grows a dependency
  // fails there rather than silently widening this exemption.
  const BUILTIN_ONLY = new Set(["command.mjs", "checks.mjs", "loop-status.mjs"]);
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  const offenders = [];
  let checked = 0;

  for (const file of readdirSync(dir).filter((f) => f.startsWith("agent-") && f.endsWith(".yml"))) {
    const lines = readFileSync(path.join(dir, file), "utf8").split("\n");
    let job = null;
    const jobs = new Map();
    for (const line of lines) {
      const m = /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(line);
      if (m) { job = m[1]; jobs.set(job, []); continue; }
      if (job) jobs.get(job).push(line);
    }
    for (const [name, body] of jobs) {
      // Prose does not run. A YAML comment explaining why `npm ci` is there
      // matched as an invocation, and reported the step that immediately
      // follows its own pin as out of order.
      const code = body.filter((l) => !/^\s*#/.test(l));
      const text = code.join("\n");
      const scripts = [...text.matchAll(/\bnode [^\n]*?([a-z-]+\.mjs)/g)].map((m) => m[1]);
      const needsPin = /\bnpm (ci|test)\b/.test(text) || scripts.some((f) => !BUILTIN_ONLY.has(f));
      if (!needsPin) continue;
      checked++;
      const setup = code.findIndex((l) => /uses: actions\/setup-node@/.test(l));
      if (setup < 0) { offenders.push(`${file}:${name} (no setup-node)`); continue; }
      // ORDER, not just presence. agent-iterate-ci.yml had a `setup-node` gated
      // on its fixing branch while its paging branch ran three scripts under the
      // opposite condition — so the job contained one and still had steps
      // running on the runner's own Node. Requiring the pin to come first makes
      // a conditional one insufficient by construction.
      const firstUse = code.findIndex((l) => /\bnode [^\n]*\.mjs|\bnpm (ci|test)\b/.test(l));
      if (firstUse >= 0 && setup > firstUse) {
        offenders.push(`${file}:${name} (setup-node comes after the first script it should pin)`);
      }
    }
  }

  assert.ok(checked > 0, "no job was found needing a pinned Node — this guard would be vacuous");
  assert.deepEqual(
    offenders,
    [],
    `these jobs run a pipeline script on an unpinned Node:\n  ${offenders.join("\n  ")}`,
  );
});

test("the scripts exempted from the Node pin really import only builtins", () => {
  // The exemption above is only sound while it is true. A dependency added to
  // any of these would run on the runner's Node with no node_modules beside it
  // — a module-not-found in a job whose whole purpose is to route or to page.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const IMPORT = /^\s*import\s[^;]*?from\s+["']([^"']+)["']/gm;
  for (const entry of ["command.mjs", "checks.mjs", "loop-status.mjs"]) {
    const seen = new Set();
    const queue = [entry];
    while (queue.length) {
      const f = queue.shift();
      if (seen.has(f)) continue;
      seen.add(f);
      const text = readFileSync(path.join(HERE, f), "utf8");
      for (const m of text.matchAll(IMPORT)) {
        const spec = m[1];
        if (spec.startsWith("node:")) continue;
        assert.ok(
          spec.startsWith("./"),
          `${entry} reaches the package dependency ${spec} via ${f} — it can no longer run without node_modules`,
        );
        queue.push(spec.slice(2));
      }
    }
  }
});

test("every workflow that runs a pipeline script sets GH_REPO", () => {
  // REGRESSION GUARD for an outage this suite did not catch.
  //
  // `gh` expands `{owner}/{repo}` by shelling out to git, so it needs a git
  // remote in the working directory. When the pipeline moved to its own repo,
  // the trusted checkout began landing in a scratch path that is deleted after
  // the move — which removed the only .git in the workspace. Every step running
  // before the PR-branch checkout then failed with:
  //
  //   unable to expand placeholder in path: failed to run git:
  //   fatal: not a git repository (or any of the parent directories): .git
  //
  // That killed the fix job, and would have killed promote too (mark-ready.mjs
  // uses the same placeholder). It was invisible here because the scripts are
  // fine — the missing thing was the ENVIRONMENT they run in, which no unit test
  // observes. Hence a workflow-level assertion.
  //
  // Workflow level, not step level: a new step that shells out to gh is exactly
  // how this comes back, and only a workflow-level default covers steps nobody
  // has written yet.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  const offenders = [];
  let checked = 0;
  for (const file of readdirSync(dir).filter((f) => f.startsWith("agent-") && f.endsWith(".yml"))) {
    const text = readFileSync(path.join(dir, file), "utf8");
    // Only workflows that actually INVOKE pipeline code can hit this. Naming
    // the directory is not invoking it: the test lane lists `scripts/agent/**`
    // as a path filter and a working-directory, shells out to `gh` nowhere, and
    // being told to declare a repository for a CLI it never runs teaches the
    // next reader that this env block is decoration.
    if (!/\bnode .*scripts\/agent\/|\bnode .*agent-tools\/|\bgh /.test(text)) continue;
    checked += 1;
    // A workflow-level `env:` block is at column 0; a job-level one is indented.
    const wfEnv = /^env:\n(?:[ \t]+.*\n|\n)*?[ \t]+GH_REPO:/m.test(text);
    if (!wfEnv) offenders.push(file);
  }
  assert.ok(checked > 0, "expected to find workflows that invoke pipeline scripts");
  assert.deepEqual(
    offenders,
    [],
    `these workflows run pipeline scripts without a workflow-level GH_REPO:\n  ${offenders.join("\n  ")}`,
  );
});

test("agent-fix decides eligibility on TRUSTED main, before the branch checkout", () => {
  // The gate authorises a bot push and the brief becomes the agent's prompt. Both
  // must be computed by main's code — a branch that could supply either would be
  // choosing whether it gets fixed and what the fixer is told to do.
  const wf = WF("agent-fix.yml");
  // Anchored on the step NAME, not on a ref literal: the trusted source is this
  // repository's own `main`, and a bare `ref: main` is not unique to this step.
  // The step's name is what stays stable, and it is unique to this job — the
  // router's checkout in the `route` job would otherwise match first.
  const trustedCheckout = wf.indexOf("- name: Check out trusted main");
  const gate = wf.indexOf("fix-eligible.mjs");
  const brief = wf.indexOf("fix-brief.mjs");
  const branchCheckout = wf.indexOf("ref: ${{ steps.pr.outputs.branch }}");
  for (const [label, i] of [["trusted checkout", trustedCheckout], ["gate", gate], ["brief", brief], ["branch checkout", branchCheckout]]) {
    assert.ok(i > 0, `agent-fix.yml must contain the ${label} step`);
  }
  assert.ok(trustedCheckout < gate, "eligibility must be decided from the trusted checkout");
  assert.ok(gate < brief, "the brief is only built once eligibility passed");
  assert.ok(brief < branchCheckout, "the prompt must be fixed before untrusted code is on disk");
});

test("agent-fix reports cost and outcome even when the agent step fails", () => {
  // A fix run that dies at turn 1 on a 429 is precisely the run whose cost and
  // reason a maintainer needs. `continue-on-error` on the agent is what lets the
  // reporting steps run, so the final step has to re-red the job.
  const wf = WF("agent-fix.yml");
  // Path-agnostic: the CLI is invoked from the staged trusted copy, so the match
  // is on the subcommand, not on where the script happens to live.
  assert.match(wf, /metrics\.mjs"? effort/, "the separate fix-effort comment must be posted");
  const effortStep = wf.slice(wf.indexOf("Post fix-agent effort comment"));
  assert.match(effortStep.slice(0, 200), /if: always\(\)/, "the effort comment must survive a failed agent");
  assert.match(wf, /core\.setFailed\('The fix agent failed/, "a swallowed agent failure must still red the job");
  // A push FOLLOWED by a failure (e.g. the agent dies during `fix-report.mjs post`)
  // makes both flags true and reds the job. Without an explicit combined branch the
  // `advanced`-first message would claim "✅ pushed" next to that red X and point at
  // a report comment that was never filed. The combined case must be handled first.
  assert.match(wf, /else if \(advanced && failed\)/, "the partial push-then-fail outcome must be its own branch");
  assert.ok(
    wf.indexOf("advanced && failed") < wf.indexOf("} else if (advanced) {"),
    "the combined branch must precede the advanced-only branch, or it can never be reached",
  );
});

test("no token-bearing job sets itself up by running the branch's build files", () => {
  // Every one of these jobs checks out the PR branch (UNTRUSTED) with an App
  // token already in the environment. A setup step that executes
  // branch-authored build instructions therefore hands a credential to code the
  // branch wrote. `make tools` is exactly that shape — five `go install` lines
  // the PR can rewrite — which is why the linter is installed from a version
  // pinned in the workflow instead.
  //
  // This is the Go-shaped version of the `--ignore-scripts` rule the pipeline
  // this was ported from applies to its package manager. Scoped to SETUP: the
  // agent itself runs branch code by design, and holds the token by design,
  // because it has to push. What must not happen is the job reaching that point
  // having already run the branch's Makefile for its own convenience.
  const TOOL_PIN = /go install github\.com\/golangci\/golangci-lint\/v2\/cmd\/golangci-lint@v[0-9.]+/;
  for (const name of ["agent-fix.yml", "agent-review-panel.yml", "agent-review-reply.yml"]) {
    const wf = WF(name);
    if (!/actions\/setup-go/.test(wf)) continue;
    assert.match(wf, TOOL_PIN, `${name}: the linter version must be pinned in the workflow`);
    // `run: make tools` — the invocation, not the word in a comment, which the
    // rationale above legitimately contains.
    assert.ok(
      !/^\s*run: make tools\s*$/m.test(wf),
      `${name}: sets up by running the branch's \`make tools\` while holding a token`,
    );
  }
});

test("agent-fix is maintainers-only and refuses bot-authored comments", () => {
  const wf = WF("agent-fix.yml");
  // Structural, not a marker string: `user.type` is set by GitHub and cannot be
  // chosen by the commenter, unlike author_association which is only a hint.
  assert.match(wf, /github\.event\.comment\.user\.type != 'Bot'/);
  assert.match(wf, /getCollaboratorPermissionLevel/);
  assert.match(wf, /\['admin', 'maintain', 'write'\]\.includes\(data\.permission\)/);
});

test("both fixers read from the SAME brief builder and write the SAME report format", () => {
  // The duplicate-prompt-input rot this extraction exists to prevent: if either
  // workflow grows its own copy, the two fixers silently diverge.
  const panel = WF("agent-review-panel.yml");
  const fix = WF("agent-fix.yml");
  assert.match(panel, /fix-brief\.mjs/, "the autonomous loop must use the shared brief builder");
  assert.match(fix, /steps\.brief\.outputs\.checklist/);
  assert.equal(/core\.setOutput\('checklist'/.test(panel), false, "the inline checklist builder must be gone");
  for (const [name, wf] of [["panel", panel], ["fix", fix]]) {
    assert.match(wf, /fix-report\.mjs post/, `the ${name} fixer must be told to file a report`);
  }
  // ...and the panel must actually READ them back, or the loop half is inert.
  assert.match(panel, /fix-report\.mjs read/);
  assert.match(panel, /--fix-reports/);

  // The ADVISORY panel too, or it reaches a different verdict than the gating one
  // for the same code — and a maintainer reading it is told something the merge
  // gate disagrees with.
  const onDemand = WF("agent-review-on-demand.yml");
  assert.match(onDemand, /fix-report\.mjs read/);
  assert.match(onDemand, /--fix-reports/);
});

test("the on-demand reader uses THIS workflow's PR output, not the panel's", () => {
  // The reader was copied from agent-review-panel.yml, where the number comes from
  // a `pr` STEP. This workflow has no such step: `steps.pr.outputs.number`
  // evaluated to "" so the `if` was permanently false and the step never ran —
  // fail-safe and completely invisible. Every `PR:` binding in the review job must
  // resolve to something this workflow actually produces.
  const wf = WF("agent-review-on-demand.yml");
  assert.equal(/steps\.pr\.outputs\.number/.test(wf), false, "no reference to a step this workflow lacks");
  const reader = wf.slice(wf.indexOf("Read fix-agent reports"), wf.indexOf("Run review panel"));
  assert.match(reader, /PR: \$\{\{ needs\.authorize\.outputs\.pr \}\}/);
  // And it must not be gated on an expression that can never be true.
  assert.equal(/if: steps\.pr\.outputs/.test(reader), false);
});

test("agent-fix re-verifies the commit AFTER checkout, closing the eligibility TOCTOU", () => {
  // The gate proves sha H carries the verdict; the checkout takes the branch TIP,
  // minutes later (app token, placeholder, brief, toolchain setup). Without a
  // re-check, an author pushing in that window has the fixer edit and push on top
  // of a commit the panel never reviewed — the exact thing the precondition exists
  // to prevent.
  const wf = WF("agent-fix.yml");
  const checkout = wf.indexOf("ref: ${{ steps.pr.outputs.branch }}");
  const recheck = wf.indexOf("Re-verify the checked-out commit");
  const agent = wf.indexOf("Address panel findings");
  assert.ok(recheck > checkout, "the re-check must run after the branch checkout");
  assert.ok(recheck < agent, "and before the agent edits anything");
  const step = wf.slice(recheck, agent);
  assert.match(step, /steps\.eligible\.outputs\.head/, "it must compare against the GATED sha");
  assert.match(step, /git rev-parse HEAD/);
  assert.match(step, /exit 1/, "a moved branch must refuse, not warn");
});

test("agent-fix always answers the commenter, even when the gate step itself fails", () => {
  // fix-eligible.mjs exits 2 on a broken invocation. Under the implicit success()
  // the refusal step was skipped along with everything downstream, stranding
  // "🤖 Working on @claude fix…" beside a red X forever.
  const wf = WF("agent-fix.yml");
  const refusal = wf.slice(wf.indexOf("- name: Explain the refusal"));
  const head = refusal.slice(0, 400);
  assert.match(head, /always\(\)/, "the refusal must not inherit the implicit success()");
  // EVERY gate that can stop the work must be listed here. The eligibility
  // check is one; the "CI is red, the CI-fix arm owns this branch" gate is the
  // other, and a refusal it does not cover strands the placeholder exactly as
  // the eligibility one used to.
  assert.match(head, /steps\.eligible\.outputs\.eligible != 'true'/);
  assert.match(head, /steps\.ci-clear\.outputs\.clear != 'true'/,
    "a CI-red refusal must reach the commenter too, or the placeholder is stranded");
  // THE GATE ASKS "KNOWN GREEN", NOT "KNOWN RED". An earlier revision tested
  // `completed && conclusion === 'failure'`, so a queued or in-progress run
  // answered "clear" — and that window is exactly what the gate covers: CI
  // concludes failure moments later, the CI arm fires, and two fixers push one
  // branch. Same fail-toward-refusal direction as fix-eligible.mjs.
  // JS comments stripped as well as YAML ones: the rationale below the gate
  // quotes the wrong predicate in order to explain why it is wrong, and an
  // assertion that reads prose as code fails on the explanation.
  const gate = wf
    .slice(wf.indexOf("id: ci-clear"), wf.indexOf("- name: Explain the refusal"))
    .split("\n")
    .filter((l) => !/^\s*\/\//.test(l))
    .join("\n");
  assert.match(gate, /conclusion === 'success'/, "the gate must require a green conclusion");
  assert.ok(
    !/conclusion === 'failure'/.test(gate),
    "asking whether CI is RED lets a still-running run pass as clear",
  );
  // THE ABSENT-RUN BRANCH IS DELIBERATE, and pinned because it has already been
  // read as a bug once. The gate asks whether agent-iterate-ci.yml's fixer could
  // own this branch, not whether the code is good — and that arm fires only on a
  // `workflow_run` of CI, so no run means it cannot have started. Refusing here
  // would make `@claude fix` permanently unusable on a docs-only PR, which
  // produces no CI run ever (`ci.yml` ignores `**/*.md`) and which the blocking
  // `docs` lens is the only thing gating.
  assert.match(gate, /if \(!runs\.length\)/, "the no-run case must be handled explicitly");
  const noRun = gate.slice(gate.indexOf("if (!runs.length)"), gate.indexOf("const newest"));
  assert.match(noRun, /setOutput\('clear', 'true'\)/,
    "an absent CI run must read as CLEAR — the CI-fix arm cannot have fired without one");
  assert.match(refusal.slice(0, 2600), /eligibility check could not complete/, "an empty reason must still say something");
  assert.match(refusal.slice(0, 2600), /the CI-fix arm owns that state/, "...and the CI-red refusal must say which arm has the branch");
});

test("CI_WORKFLOW_PATH names a workflow file that actually exists", () => {
  // The gate matches CI runs on this path instead of on the run's display name,
  // which is what makes gate 1 unforgeable — a second file cannot claim the
  // path. The cost is that a typo, or renaming ci.yml, silently makes the gate
  // unsatisfiable for every PR: the workflow-scoped query would find nothing and read as
  // "CI has not run". Assert the file is there, and that it is the one whose
  // runs are named "CI".
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const abs = path.join(HERE, "..", "..", CI_WORKFLOW_PATH);
  const src = readFileSync(abs, "utf8"); // throws if the path is wrong
  assert.match(src, /^name:\s*CI\s*$/m, "ci.yml must still be the workflow whose runs are named CI");
});




test("CI_DEFINING_PATHS covers the surface CI's behaviour is read from", () => {
  // Gate 1b refuses a PR that supplies any part of the CI definition.
  //
  // NO SECOND LIST TO MIRROR HERE, and that is a fact about this repository
  // rather than an omission. Upstream asserts this list is a superset of its
  // `harness.config.json` `ciConfig`, which exists because a changed path there
  // can SHRINK a CI run — so a PR editing it could grade its own homework. This
  // repository's `ci.yml` filter declares `build: '**'`, so the lint/build/test
  // job is unconditional and no path can shrink it; the tag-gated jobs it can
  // narrow (bench, complex-test, load-test) are not what a gate reads. The
  // property the upstream mirror protects is structurally absent, so what is
  // left to assert is that the matcher classifies the real surface.
  for (const p of [
    ".github/workflows/ci.yml",
    ".github/workflows/nested/whatever.yml",
    ".github/actions/setup/action.yml",
    ".github/CODEOWNERS",
    // The lanes: `ci.yml` runs `make lint` and `make build`, and what those do
    // lives here.
    "Makefile",
    ".golangci.yml",
    "codecov.yml",
    "go.mod",
    "go.sum",
    "buf.gen.yaml",
    "buf.work.yaml",
    "api/buf.yaml",
    "api/buf.gen.yaml",
    "build/docker/docker-compose.yml",
    "build/docker/sharding/docker-compose.yml",
    "scripts/ci/parse-bench.js",
    "scripts/verify-doc-links.mjs",
  ]) {
    assert.equal(definesCi(p), true, `${p} defines what CI does and must be refused by gate 1b`);
  }

  // ...and does not swallow the code under review. A gate that refuses every PR
  // is the same outage as one that refuses none, arrived at from the other side.
  for (const p of [
    "pkg/document/crdt/tree.go",
    "server/backend/database/mongo/client.go",
    "test/integration/document_test.go",
    "api/yorkie/v1/resources.proto",
    "docs/design/tree.md",
    "README.md",
  ]) {
    assert.equal(definesCi(p), false, `${p} is code or prose under review, not the CI definition`);
  }
});

test("every workflow_run consumer CI can drive gates on the CI workflow's PATH", () => {
  // `workflow_run`'s `workflows:` filter matches a run's DISPLAY NAME, which is
  // only a `name:` key and is not unique — so a second file saying `name: CI`
  // reaches every one of these workflows. Each therefore has to check the file
  // that produced the run, and each does it in a job `if:` that gates by
  // SKIPPING, which is invisible when it breaks.
  //
  // ONE test over all three, because the clause is one decision: the panel's
  // (whose `fix` job pushes to the PR branch), agent-iterate-ci's (whose fixer
  // holds contents: write and is driven by CI concluding `failure`), and
  // ci-report's (which comments on the PR). Only the panel's was pinned before,
  // so the other two could be deleted with every test still green.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  // Every non-path expression is substituted with a value that SATISFIES the
  // clause, so `path` is the only free variable left and the assertions below
  // are about it alone.
  const toJs = (s) =>
    s
      .replace(/github\.event\.workflow_run\.path/g, "PATH")
      .replace(/github\.event\.workflow_run\.head_repository\.full_name/g, "'r'")
      .replace(/github\.repository/g, "'r'")
      .replace(/github\.event\.workflow_run\.conclusion/g, "'failure'")
      .replace(/github\.event\.workflow_run\.event/g, "'pull_request'")
      .replace(/github\.event\.workflow_run\.run_attempt/g, "1")
      .replace(/github\.event\.action/g, "'requested'")
      .replace(/vars\.AGENT_PIPELINE_ENABLED/g, "'true'");

  // Only the consumers this repository installs. Upstream also lists
  // agent-iterate-ci.yml and ci-report.yml; neither is ported
  // (docs/design/agent-command-verbs.md), and a guard that reads a file which is
  // not there fails for the wrong reason. Filtering by presence keeps every
  // installed consumer covered and re-arms when another lands.
  const consumers = [
    ["agent-review-panel.yml", "gate"],
    ["agent-iterate-ci.yml", "gate"],
    ["ci-report.yml", "report"],
  ].filter(([f]) => existsSync(path.join(HERE, "..", "..", ".github", "workflows", f)));
  assert.ok(consumers.length > 0, "no workflow_run consumer found — this guard would be vacuous");
  for (const [file, job] of consumers) {
    const yml = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", file), "utf8");
    // The trigger has to be the display-name one, or this assertion is aimed at
    // the wrong thing.
    assert.match(yml, /workflows: \["CI"\]/, `${file} must still consume CI by display name`);
    const expr = (yml.match(new RegExp(`^ {2}${job}:\\n(?:.*\\n)*? {4}if: >-\\n((?: {6}.*\\n)+)`, "m")) || [])[1];
    assert.ok(expr, `could not extract ${file}'s ${job} job if: expression`);
    const admits = new Function("PATH", `return (${toJs(expr)});`);

    assert.ok(admits(CI_WORKFLOW_PATH), `${file} must still admit a real CI run`);
    assert.ok(
      !admits(".github/workflows/pwn.yml"),
      `${file} admits a run from another file that merely calls itself 'CI'`,
    );
    // An absent field must not read as a match either — `undefined == ''` is
    // false in JS but `${{ }}` renders a missing field as the empty string, and
    // both spellings have to refuse.
    for (const absent of ["", undefined, null]) {
      assert.ok(!admits(absent), `${file} must refuse a payload with no workflow_run.path (${absent})`);
    }
  }

  // Upstream additionally guards capture-collect.yml here, which consumes the
  // PANEL by display name and whose job holds a write PAT for another
  // repository. That workflow is not ported (docs/design/agent-command-verbs.md)
  // and no workflow here holds a cross-repository credential, so there is
  // nothing of that shape left to guard. The loop above covers every installed
  // workflow_run consumer, which is the property that survives.
});

test("no two ci.yml triggers can race for the same PR head SHA", () => {
  // THE INVARIANT `ciConclusion` RESTS ON — stated precisely, because an earlier
  // revision of this test overclaimed it as "exactly one run per head SHA".
  // That is false: closing and reopening a PR files a second `pull_request` run
  // for the same commit. Newest-wins is CORRECT there, because the later run is
  // a fresh execution of the same file at the same tree and supersedes the
  // earlier one.
  //
  // What must not happen is two runs from DIFFERENT triggers for one PR head,
  // where "newest" is arbitrary rather than superseding. `push` restricted to
  // `main` never fires for a PR branch, and a `merge_group` run carries the
  // speculative merge commit. Widen `push`, or add a trigger that fires on a PR
  // head, and this fails — making the choice conscious instead of silent.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const yml = readFileSync(path.join(HERE, "..", "..", CI_WORKFLOW_PATH), "utf8");
  // To the next TOP-LEVEL key, whatever it is. Slicing to a named one (`permissions:`)
  // silently ran to end-of-file in a workflow that declares none, and the "events"
  // it then found were job names.
  const from = yml.indexOf("\non:");
  const rest = yml.slice(from + 1);
  const nextKey = rest.slice(3).search(/^[a-z_]+:/m);
  const on = rest.slice(0, nextKey < 0 ? undefined : nextKey + 3);
  const code = on.split("\n").filter((l) => !/^\s*#/.test(l)).join("\n");

  const events = [...code.matchAll(/^ {2}([a-z_]+):/gm)].map((m) => m[1]);
  assert.deepEqual(
    events.sort(),
    ["pull_request", "push"],
    "a new CI trigger may fire for a PR head SHA — see ciConclusion before adding one",
  );
  assert.match(
    code.slice(code.indexOf("push:"), code.indexOf("pull_request:")),
    /branches: \[["']?main["']?\]/,
    "push must stay restricted to main, or every PR branch push races pull_request",
  );
});

test("ciConclusion: the newest run wins, and an unfinished one is 'not yet'", () => {
  // Identity is NOT decided here any more — the caller scopes its query with
  // `/actions/workflows/ci.yml/runs`, so GitHub resolves which workflow file
  // produced these runs. An earlier revision filtered on `path` in JS and
  // stripped an `@ref` suffix, which let `ci.yml@pwn.yml` through; the test for
  // that lives with CI_WORKFLOW_FILE below.
  const r = (id, conclusion) => ({ id, conclusion });

  assert.equal(ciConclusion([]), null, "no runs at all");
  assert.equal(ciConclusion(undefined), null, "a missing list is not a crash");

  assert.equal(ciConclusion([r(1, "success")]), "success");
  assert.equal(ciConclusion([r(1, "failure")]), "failure");
  assert.equal(ciConclusion([r(1, "cancelled")]), "failure", "only success is success");

  // Newest by id, in either input order — ids are assigned in creation order,
  // so this needs no date parsing and cannot be reordered by a bad timestamp.
  assert.equal(ciConclusion([r(1, "success"), r(9, "failure")]), "failure");
  assert.equal(ciConclusion([r(9, "failure"), r(1, "success")]), "failure");
  assert.equal(ciConclusion([r(9, "success"), r(1, "failure")]), "success");

  // A SHA legitimately carries two runs after a close/reopen; the later one is
  // a fresh execution of the same file and supersedes the earlier.
  assert.equal(ciConclusion([r(1, "failure"), r(9, "success")]), "success");

  // The newest still running → not known yet, whatever the older ones say.
  assert.equal(ciConclusion([r(1, "success"), r(9, null)]), null);
  assert.equal(ciConclusion([r(9, null)]), null);
});

test("the CI readers scope by workflow FILE, never by parsing a run's path", () => {
  // THE REGRESSION THIS EXISTS FOR. A revision matched `r.path` in JS and
  // stripped an `@ref` suffix for called workflows — a shape CI does not have —
  // with `path.split("@")[0]`. `.github/workflows/ci.yml@pwn.yml` is a legal
  // filename that Actions runs (it ends in .yml), and it matched, re-opening the
  // forgery the check exists to close. Server-side scoping cannot be spoofed by
  // a filename.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  // CODE LINES ONLY. The comments explaining this regression quote the very
  // expression they warn about, and a whole-file grep would read the warning as
  // the thing it warns about — the same trap as the panel-trigger test above.
  const codeOf = (file) =>
    readFileSync(path.join(HERE, file), "utf8")
      .split("\n")
      .filter((l) => !/^\s*(\/\/|\*|\/\*)/.test(l))
      .join("\n");

  for (const file of ["mark-ready.mjs", "set-state.mjs"]) {
    const src = codeOf(file);
    assert.match(
      src,
      /actions\/workflows\/\$\{CI_WORKFLOW_FILE\}\/runs\?head_sha=/,
      `${file} must ask the API for the CI workflow's runs, not for every run on the SHA`,
    );
    assert.ok(
      !/actions\/runs\?head_sha=/.test(src),
      `${file} must not fetch every workflow run for the SHA and filter in JS`,
    );
    assert.ok(!/split\("@"\)/.test(src), `${file} must not parse a run path`);
  }
  assert.ok(!/split\("@"\)/.test(codeOf("checks.mjs")), "checks.mjs must not parse a run path either");
  assert.equal(CI_WORKFLOW_FILE, "ci.yml");
  assert.ok(CI_WORKFLOW_PATH.endsWith("/" + CI_WORKFLOW_FILE), "the two constants must name the same file");
});

test("ciRunToRerun: the newest COMPLETED run, and only that one", () => {
  const run = (id, status, conclusion) => ({ id, status, conclusion });

  assert.equal(ciRunToRerun([]), null, "nothing to re-run");
  assert.equal(ciRunToRerun(undefined), null, "a missing list is not a crash");

  // Exactly one, even when several are red. Re-running the others would erode
  // agent-iterate-ci's attempt bound — it counts current `failure` conclusions,
  // and a re-run REPLACES one — and emit a `workflow_run` completion per run
  // into a cancel-in-progress group, cancelling the fixer mid-push.
  assert.equal(
    ciRunToRerun([run(1, "completed", "failure"), run(3, "completed", "failure"), run(2, "completed", "success")]).id,
    3,
    "the newest completed run, regardless of how many are red",
  );

  // An in-flight run cannot be re-run (422) and emits its own completion event,
  // so the newest COMPLETED one is the target even when a newer one is running.
  assert.equal(ciRunToRerun([run(9, "in_progress", null), run(4, "completed", "success")]).id, 4);
  assert.equal(ciRunToRerun([run(9, "queued", null)]), null, "nothing completed yet");
});

test("ciRunToAwait: the newest run that is NOT completed", () => {
  const run = (id, status, conclusion) => ({ id, status, conclusion });

  assert.equal(ciRunToAwait([]), null, "nothing to wait for");
  assert.equal(ciRunToAwait(undefined), null, "a missing list is not a crash");
  assert.equal(ciRunToAwait([run(1, "completed", "success")]), null, "a finished run is not waited for");

  // The case #1047 and #1052 died on: a run in flight and nothing completed, so
  // `ciRunToRerun` is null and the verb used to stop there. Answering with the
  // in-flight run is what lets the caller wait and then produce the
  // `run_attempt > 1` completion the review panel re-engages on.
  assert.equal(ciRunToAwait([run(7, "in_progress", null)]).id, 7);
  assert.equal(ciRunToAwait([run(9, "queued", null), run(4, "completed", "success")]).id, 9);

  // A status this pipeline has never heard of reads as "still going". The two
  // selections partition the listing, so nothing can fall between them.
  assert.equal(ciRunToAwait([run(3, "waiting", null)]).id, 3, "an unknown status is not 'nothing here'");
  for (const fixture of [
    [run(1, "completed", "success")],
    [run(2, "in_progress", null), run(1, "completed", "success")],
    [run(2, "pending", null), run(1, "queued", null)],
    [run(3, "requested", null)],
  ]) {
    const rerun = ciRunToRerun(fixture);
    const await_ = ciRunToAwait(fixture);
    assert.ok(rerun || await_, `a non-empty listing must select something: ${JSON.stringify(fixture)}`);
    assert.notEqual(rerun?.id ?? "r", await_?.id ?? "a", "one run cannot be both");
  }
});

test("agent-rerun / agent-loop mirror ciRunToRerun + ciRunToAwait inline, and the copies agree", () => {
  // Both re-run steps run BEFORE any checkout, so they cannot import checks.mjs
  // — the same constraint that makes agent-review-panel.yml mirror
  // `ciRunDecision` inline. The rule therefore exists twice; pinning the copy is
  // what keeps it ONE rule.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const run = (id, status, conclusion) => ({ id, status, conclusion });
  const fixtures = [
    [],
    [run(1, "completed", "success")],
    [run(2, "completed", "failure"), run(1, "completed", "success")],
    [run(3, "completed", "success"), run(2, "completed", "cancelled")],
    [run(9, "in_progress", null), run(1, "completed", "success")],
    [run(1, "queued", null)],
    // The page-2 case: 101 runs is impossible to reach through `per_page: 1`, so
    // this fixture is what distinguishes a paginated listing from a truncated one
    // once the selection is EXTRACTED from the YAML below rather than re-typed.
    [...Array.from({ length: 100 }, (_, i) => run(i + 1, "completed", "failure")), run(500, "completed", "success")],
  ];

  for (const file of ["agent-rerun.yml", "agent-loop.yml"]) {
    const yml = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", file), "utf8");
    const at = yml.indexOf("workflow_id: 'ci.yml'");
    assert.notEqual(at, -1, `${file} must still re-run CI by workflow id`);
    // Wide enough to hold the whole re-run step: both selections, the wait, and
    // the two guards after it.
    const block = yml.slice(at - 400, at + 5000);

    // Exactly one reRunWorkflow call, not a loop over a selection.
    assert.ok(!/for \(const run of/.test(block), `${file}: fanning out cancels the fixer and erodes the attempt bound`);
    assert.equal(
      (yml.match(/await github\.rest\.actions\.reRunWorkflow\(\{ owner, repo, run_id: target\.id \}\)/g) || []).length,
      1,
      `${file} re-runs exactly one run, and re-runs whatever the selection chose`,
    );
    // The LISTING has to be able to see past the first run. `per_page: 1` — what
    // this step did before — hands the selection a one-element list, so "the
    // newest completed run" silently degrades to "the newest run, if it happens
    // to be finished" and `@claude rerun` does nothing while a run is in flight.
    // Not covered by extracting the selection below: the selection is correct on
    // whatever list it is given, and the bug is in the list.
    assert.match(block, /github\.paginate\(github\.rest\.actions\.listWorkflowRuns/, `${file} must paginate the listing`);
    assert.match(block, /per_page: 100/, `${file} must request full pages`);
    assert.ok(!/per_page: 1[,\s}]/.test(block), `${file}: per_page: 1 cannot see the newest COMPLETED run`);

    // THE COPY, EXTRACTED FROM THE YAML AND RUN — not re-typed here. A hand copy
    // of the rule inside this test can only ever prove checks.mjs agrees with the
    // test file: it cannot fail because of anything in agent-loop.yml or
    // agent-rerun.yml, which is the one thing this test exists to check.
    const src = block.match(/const completed = [\s\S]*?const run = completed\.reduce\(.*?\);/);
    assert.ok(src, `${file}: could not extract the inline run-selection from the workflow`);
    const mirror = new Function("all", `${src[0]}\nreturn run;`);

    // The SECOND selection, added after #1047/#1052 sat unreviewed for five
    // hours: with nothing completed, the verb used to report "the panel will
    // engage on the next CI run" and there was no next CI run.
    const awaitSrc = block.match(/const pending = [\s\S]*?const awaited = pending\.reduce\(.*?\);/);
    assert.ok(awaitSrc, `${file}: could not extract the inline in-flight selection from the workflow`);
    const awaitMirror = new Function("all", `${awaitSrc[0]}\nreturn awaited;`);

    for (const fixture of fixtures) {
      assert.deepEqual(
        mirror(fixture)?.id ?? null,
        ciRunToRerun(fixture)?.id ?? null,
        `${file}'s inline mirror disagrees with ciRunToRerun on ${JSON.stringify(fixture).slice(0, 120)}`,
      );
      assert.deepEqual(
        awaitMirror(fixture)?.id ?? null,
        ciRunToAwait(fixture)?.id ?? null,
        `${file}'s inline mirror disagrees with ciRunToAwait on ${JSON.stringify(fixture).slice(0, 120)}`,
      );
    }
    // And prove the extracted code is not a constant-null stub that trivially
    // agrees on nothing — at least one fixture must select a run.
    assert.equal(mirror(fixtures[1])?.id, 1, `${file}'s extracted mirror must actually select a run`);
    assert.equal(awaitMirror(fixtures[5])?.id, 1, `${file}'s extracted in-flight mirror must actually select a run`);

    // The three things the wait is only correct WITH. Each removed one is a
    // distinct regression: no poll → the dead end returns; no head check → CI
    // re-runs against a stale sha; no failure check → a second
    // `completed/failure` event cancels agent-iterate-ci's fixer mid-push,
    // which is how #648 lost a round.
    assert.match(block, /getWorkflowRun\(\{ owner, repo, run_id: awaited\.id \}\)/, `${file} must poll the in-flight run`);
    assert.match(block, /fresh\.head\.sha !== pr\.head\.sha/, `${file} must not re-run CI for a sha that is no longer the head`);
    assert.match(block, /target\.conclusion === 'failure'/, `${file} must leave a red run to the CI-fix arm`);

    // The wall and the wait live in two places and no expression context can
    // read a job's own timeout, so a raised wait with an unraised wall would
    // kill the job mid-poll — latch cleared, nothing re-run, nothing said.
    const waitMinutes = Number(block.match(/Date\.now\(\) \+ (\d+) \* 60 \* 1000/)?.[1]);
    assert.ok(waitMinutes > 0, `${file}: could not read the wait budget`);
    const walls = [...yml.matchAll(/timeout-minutes: (\d+)/g)].map((m) => Number(m[1]));
    assert.ok(
      Math.max(...walls) > waitMinutes,
      `${file}: the job wall (${Math.max(...walls)}m) must exceed the ${waitMinutes}m wait`,
    );
  }
});

test("the App-presence check tests BOTH secrets, not just the id", () => {
  // `create-github-app-token` needs an id AND a private key, and the two are
  // registered in two separate operations — the key being the half that gets
  // rotated. A check that reads only the id therefore reports "configured" for
  // a repository that cannot mint a token, and the failure surfaces in the mint
  // step: past the commenter-facing arm, past the stand-down, as a red job with
  // a token error in it. That is precisely the outcome the check was added to
  // prevent, moved one step later.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  let checks = 0;

  for (const file of readdirSync(dir).filter((f) => f.startsWith("agent-") && f.endsWith(".yml"))) {
    const lines = readFileSync(path.join(dir, file), "utf8").split("\n");
    for (let i = 0; i < lines.length; i++) {
      if (!/^\s+id: app$/.test(lines[i])) continue;
      let end = i + 1;
      while (end < lines.length && !/^ {6}- /.test(lines[end])) end++;
      const step = lines.slice(i, end).filter((l) => !/^\s*#/.test(l)).join("\n");
      checks++;
      assert.match(step, /AGENT_APP_ID/, `${file}: the check does not read AGENT_APP_ID`);
      assert.match(step, /AGENT_APP_PRIVATE_KEY/, `${file}: the check ignores AGENT_APP_PRIVATE_KEY`);
      assert.match(
        step,
        /\[ -n "\$APP_ID" \] && \[ -n "\$APP_KEY" \]/,
        `${file}: both secrets must be REQUIRED, not merely read`,
      );
    }
  }
  assert.ok(checks >= 5, `expected an App-presence check per App-backed verb, found ${checks}`);
});

test("no shell step hides a command substitution inside a double-quoted echo", () => {
  // Backticks are markdown in a `github-script` body and COMMAND SUBSTITUTION in
  // a `run:` block. Two notice lines carried `@claude fix` / `@claude rerun` in
  // backticks inside double quotes, so the runner tried to execute them: the
  // annotation rendered with a hole where the verb should be, stderr carried a
  // "command not found", and the step still exited 0 — a message about a
  // misconfiguration, itself quietly malformed. The rule is mechanical, so pin
  // it mechanically rather than trusting the next author to remember which
  // quoting regime a given block is in.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  const offenders = [];

  for (const file of readdirSync(dir).filter((f) => f.endsWith(".yml"))) {
    const lines = readFileSync(path.join(dir, file), "utf8").split("\n");
    let inRun = false;
    let runIndent = 0;
    for (let i = 0; i < lines.length; i++) {
      const m = /^(\s*)(?:-\s+)?run: \|/.exec(lines[i]);
      if (m) {
        inRun = true;
        runIndent = m[1].length;
        continue;
      }
      if (!inRun) continue;
      if (lines[i].trim() !== "" && (lines[i].length - lines[i].trimStart().length) <= runIndent) {
        inRun = false;
        continue;
      }
      if (/^\s*#/.test(lines[i])) continue;
      // Walk the line tracking quote state. Only an UNESCAPED backtick inside
      // double quotes substitutes: `\\`` is a literal backtick (the workflows
      // use it deliberately, to put markdown code spans in comment bodies built
      // by shell), and a backtick inside '…' is literal too.
      let dq = false;
      let sq = false;
      for (let c = 0; c < lines[i].length; c++) {
        const ch = lines[i][c];
        if (ch === "\\" && dq) {
          c++;
          continue;
        }
        if (ch === "'" && !dq) sq = !sq;
        else if (ch === '"' && !sq) dq = !dq;
        else if (ch === "`" && dq && !sq) {
          offenders.push(`${file}:${i + 1} ${lines[i].trim()}`);
          break;
        }
      }
    }
  }

  assert.deepEqual(offenders, [], `backticks inside a double-quoted shell string:\n  ${offenders.join("\n  ")}`);
});

test("the CI-fix arm's attempts guard cannot run without the App", () => {
  // THE STAND-DOWN WAS ONLY TWO STEPS DEEP. `agent-iterate-ci` is
  // `workflow_run`-triggered, so it has no commenter to answer and skips
  // silently by design — but only the token mint and the checkout were gated.
  // The attempts guard ran regardless, and it is the step that decides
  // everything: below the limit it set `proceed=true` and the entire fixing arm
  // ran against a workspace that was never checked out; at the limit it wrote
  // the PAGED LATCH and `agent:blocked` — terminal, human-only state — onto a PR
  // whose run had just announced it was standing down. Gating the guard leaves
  // every `steps.guard.outputs.*` empty, so the arm really does nothing.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const text = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-iterate-ci.yml"), "utf8");
  const lines = text.split("\n");
  const at = lines.findIndex((l) => /^\s+id: guard$/.test(l));
  assert.ok(at > 0, "no `id: guard` step in agent-iterate-ci.yml");

  let end = at + 1;
  while (end < lines.length && !/^ {6}- /.test(lines[end])) end++;
  const step = lines.slice(at, end).filter((l) => !/^\s*#/.test(l)).join("\n");
  assert.match(
    step,
    /if: steps\.app\.outputs\.configured == 'true'/,
    "the attempts guard must be gated on the App-presence check",
  );
});
