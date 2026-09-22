// May `@claude fix` run right now? The precondition gate for the on-demand fixer.
//
// THE RULE, as specified: `@claude fix` works only when there was a previous
// review panel run AND no commit landed after it. Both halves reduce to one
// unforgeable question — does the CURRENT head sha carry completed lens check
// runs from our panel? A commit after the panel MOVES the head, so lens runs
// attested against the head are simultaneously proof that the panel ran and proof
// that nothing has landed since. There is no timestamp comparison here on purpose:
// a "panel ran at T, commit at T+1" rule would depend on two clocks and on commit
// dates, which the author controls (`git commit --date`).
//
// FAIL DIRECTION: TOWARD INELIGIBLE, in every branch. This gate authorises a bot
// to push to a contributor's branch, so every unknown must read as "no". An API
// error, an unparseable response, a missing head sha — all refuse. That is the
// opposite of prior-findings.mjs (additive recall, degrades to carrying fewer) and
// the same as mark-ready.mjs, and the asymmetry is the point: this decides whether
// to ACT, not what to report.
//
// WHAT IT DELIBERATELY DOES NOT CHECK: the paged latch. A PR the autonomous loop
// gave up on is the main reason a maintainer reaches for this verb — one more
// attempt, by hand, without restarting the whole round budget. `@claude fix`
// therefore runs on a paged PR and leaves the latch in place, so the loop stays
// parked and the next panel round does not silently re-engage the auto-fixer.
// `@claude rerun` remains the verb that clears the latch.
//
// Usage:
//   node fix-eligible.mjs <pr> (--checks <n,...> | --lenses <lenses.json>) [--github-output]
// Emits `eligible` (true|false), `reason`, `head`, `failing` — to $GITHUB_OUTPUT
// when --github-output is set, and always to stdout as JSON. ALWAYS exits 0: the
// caller reports the reason to the commenter, and a non-zero exit would turn "not
// eligible" into a red workflow run, which reads as a bug rather than an answer.

import { appendFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gh, commitCheckRuns, parseArgs } from "./gh-checks.mjs";
import { resolveCheckNames as resolveNames } from "./prior-findings.mjs";

const str = (v) => (typeof v === "string" ? v : "");

/**
 * Classify our panel's lens runs on one commit.
 *
 * `app.slug === 'github-actions'` is checked because the NAME is not a secret:
 * any integration may create a check run called `agent-review-security`, and
 * without the producer check a third-party app could manufacture a verdict — and
 * therefore an eligibility — out of nothing. The slug cannot be chosen by the
 * app that reports it.
 */
export function classifyLensRuns(runs, names) {
  const want = new Set((Array.isArray(names) ? names : []).filter((n) => typeof n === "string" && n !== ""));
  const seen = new Map();
  for (const r of Array.isArray(runs) ? runs : []) {
    if (!r || !want.has(r.name)) continue;
    if (!r.app || r.app.slug !== "github-actions") continue;
    const prev = seen.get(r.name);
    if (!prev || Date.parse(r.started_at) > Date.parse(prev.started_at)) seen.set(r.name, r);
  }
  const pending = [];
  const failing = [];
  let completed = 0;
  for (const [name, r] of seen) {
    if (r.status !== "completed") {
      pending.push(name);
      continue;
    }
    completed++;
    if (r.conclusion === "failure") failing.push(name);
  }
  return { total: seen.size, completed, pending: pending.sort(), failing: failing.sort() };
}

/**
 * The decision, from already-fetched data. Pure, so every branch below is
 * reachable from a test — the reason strings are user-facing and a wrong one
 * sends a maintainer looking in the wrong place.
 */
export function decideEligibility({ pr, isFork, head, runs, names }) {
  const no = (reason) => ({ eligible: false, reason, head: str(head), failing: [] });
  // UNKNOWN HEAD FIRST, and the order is the point. `readPrHead` reports an API
  // failure as `{head: "", isFork: true}` — fork being the safe default when
  // provenance cannot be established. Checking `isFork` first therefore told a
  // maintainer "this is a fork" whenever the API call merely failed, sending them
  // to look for a fork problem on a same-repo PR. Both outcomes refuse, so this
  // changes no decision; it changes whether the stated reason is true.
  if (str(head) === "") {
    return no(
      "Could not determine this PR's head commit, so there is nothing safe to act on. "
      + "Try again in a moment.",
    );
  }
  if (isFork) {
    return no(
      "`@claude fix` pushes a commit to the PR branch, which the app cannot do on a fork. "
      + "Fix the findings locally, or ask a maintainer to move the branch into this repository.",
    );
  }

  const { total, completed, pending, failing } = classifyLensRuns(runs, names);
  if (total === 0) {
    // NOT "comment @claude review" — that verb is ADVISORY and records no check
    // runs at all (agent-review-on-demand.yml holds `checks: read`, never write),
    // so following that advice could never make a PR eligible. The only producer
    // of lens check runs is the autonomous panel, which fires from CI on a managed
    // same-repo PR; the two real answers are "wait for it" and "opt the PR in".
    return no(
      `No review-panel verdict for the current head commit (\`${str(head).slice(0, 8)}\`). `
      + "`@claude fix` acts only on findings the panel has published for the code as it stands "
      + "now. If a commit landed after the last review, wait for CI to finish and the next panel "
      + "round to post its verdict, then run `@claude fix` again. If this PR is not agent-managed "
      + "it gets no panel at all — comment **`@claude loop`** to opt it in. (**`@claude review`** "
      + "is advisory and posts no check runs, so it cannot make this PR eligible.)",
    );
  }
  if (pending.length > 0) {
    return no(
      `A review panel is still running on this commit (${pending.join(", ")}). `
      + "Wait for it to finish, then run `@claude fix`.",
    );
  }
  if (failing.length === 0) {
    // NOT "passed all N lenses". `completed` counts every concluded run, and a
    // lens that does not apply to this diff concludes `neutral`, not `success`
    // (agent-review-panel.yml publishes it that way) — so that wording both
    // over-counts and asserts a pass that did not happen. Say the thing the gate
    // actually established: nothing is requesting changes.
    return no(
      `No lens is requesting changes on this commit (${completed} concluded), so there are no `
      + "blocking findings to fix.",
    );
  }
  return {
    eligible: true,
    reason: `${failing.length} lens(es) requested changes on \`${str(head).slice(0, 8)}\`: ${failing.join(", ")}.`,
    head: str(head),
    failing,
    pr,
  };
}

// --- CLI --------------------------------------------------------------------

/** The PR's head sha and whether it lives on a fork. Any failure → unknown. */
export function readPrHead(pr, { api = gh, repo = process.env.GITHUB_REPOSITORY, log = console.error } = {}) {
  try {
    const data = api(["api", `repos/{owner}/{repo}/pulls/${pr}`]);
    const full = str(data?.head?.repo?.full_name);
    // Fork unless PROVEN same-repo. Three ways to be unproven, and all three must
    // read as fork because this gate authorises a push:
    //   - no head repo at all (a deleted fork) — certainly not pushable;
    //   - a different full_name;
    //   - `repo` itself unknown, so there is nothing to compare against. An
    //     earlier draft skipped the comparison in that case, which made an unset
    //     GITHUB_REPOSITORY silently resolve every PR to "same repo" — the exact
    //     fail-open direction this module is not allowed to have.
    const isFork = full === "" || str(repo) === "" || full !== str(repo);
    return { head: str(data?.head?.sha), isFork };
  } catch (err) {
    log(`fix-eligible: could not read PR #${pr} (${err.message}); refusing.`);
    return { head: "", isFork: true };
  }
}

function main() {
  const args = parseArgs(process.argv, { booleans: ["github-output"] });
  const pr = args._[0];
  const names = resolveNames(args);
  // A bad invocation is a TOOLING error, not an answer about the PR, and must not
  // be reported to the commenter as "your PR is not eligible". Exit 2 like every
  // other script here so the workflow step reds instead.
  if (!pr || !/^\d+$/.test(pr) || names.length === 0) {
    console.error("Usage: node fix-eligible.mjs <pr> (--checks <name,...> | --lenses <lenses.json>) [--github-output]");
    process.exit(2);
  }

  const { head, isFork } = readPrHead(pr);
  let runs = [];
  if (head !== "") {
    try {
      runs = commitCheckRuns(head, { api: gh });
    } catch (err) {
      // Refuse rather than treat an unreadable check list as "no panel ran": the
      // two are indistinguishable from here, and only one of them is safe.
      console.error(`fix-eligible: could not read check runs for ${head} (${err.message}); refusing.`);
      const out = { eligible: false, reason: "Could not read this commit's check runs, so eligibility could not be established. Try again in a moment.", head, failing: [] };
      emit(args, out);
      return;
    }
  }
  emit(args, decideEligibility({ pr, isFork, head, runs, names }));
}

function emit(args, decision) {
  process.stdout.write(JSON.stringify(decision));
  if (args["github-output"]) {
    const out = process.env.GITHUB_OUTPUT;
    if (!out) {
      console.error("fix-eligible: --github-output given but $GITHUB_OUTPUT is unset.");
      process.exit(2);
    }
    // `reason` is prose with no newlines by construction (every string above is a
    // single concatenated line), so plain `k=v` is safe. Asserted, not assumed —
    // a future reason containing a newline would otherwise write a corrupt output
    // file and silently drop `failing`.
    const reason = str(decision.reason).replace(/[\r\n]+/g, " ");
    appendFileSync(
      out,
      `eligible=${decision.eligible ? "true" : "false"}\n`
      + `reason=${reason}\n`
      + `head=${str(decision.head)}\n`
      + `failing=${(decision.failing ?? []).join(",")}\n`,
    );
  }
  console.error(`fix-eligible: ${decision.eligible ? "ELIGIBLE" : "not eligible"} — ${decision.reason}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
