// Decide whether THIS review round may look at only the delta, and hand the
// answer to the workflow as step outputs.
//
// This is the caller `review-state.mjs` was written for. It owns the two things
// that module deliberately cannot do: read each lens's last-reviewed pointer from
// the checks API, and measure the commit range with git. `resolveReviewMode` then
// makes the decision, purely, from those facts.
//
// Usage:
//   node review-scope.mjs <pr> --head <sha> --lenses <lenses.json>
//                              --changed-files <f> [--repo <dir>]
//                              [--full-every N] [--max-delta-lines N]
//
// Outputs (appended to $GITHUB_OUTPUT when set, always logged):
//   mode=full|incremental   since=<sha40 or empty>   reason=<why>   rounds=<n>
//
// FAIL DIRECTION — the whole file. This ALWAYS exits 0 with a usable mode, and
// EVERY failure path resolves to `mode=full`. Unlike the rest of the pipeline,
// failing closed here would be wrong: a full review is always correct, merely more
// expensive, so there is no reason for a cost optimiser to be able to take the
// panel down. A bad argument, an unreachable API, a shallow clone and an outright
// crash all produce the same safe answer, distinguished only by `reason`.
//
// TWO-PHASE, because the inputs are circular: the git facts must be measured over
// `since..head`, and `since` is only knowable after reading the pointers. So we ask
// `agreedReviewedSha` first, and only measure git when there is a range to measure.
//
// Requires the `gh` CLI authenticated via GH_TOKEN / GITHUB_TOKEN, and a checkout
// with enough history for `merge-base --is-ancestor` (the panel job uses
// `fetch-depth: 0`).

import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { appendFileSync, readFileSync } from "node:fs";
import { agreedReviewedSha, resolveReviewMode, latestLensRuns } from "./review-state.mjs";
import { gh, prCommitsWithCheckRuns, allCheckRuns, parseArgs } from "./gh-checks.mjs";
import { lensApplies } from "./review-panel.mjs";
import { groupReviewRounds } from "./rounds.mjs";

const execFileAsync = promisify(execFile);
const SHA = /^[0-9a-fA-F]{40}$/;
// Same ceiling as novelty.mjs's git probes. These four commands are cheap; a
// repository state that makes one hang is one we should stop waiting for and
// review in full.
const GIT_TIMEOUT_MS = 10_000;

/**
 * One git command. `status` is carried out separately from `ok` because git uses
 * the exit code as an ANSWER for some commands (`--is-ancestor` 1 = "no") and as
 * an ERROR for the rest. Collapsing them would record a broken lookup as a
 * confident answer — the same distinction novelty.mjs draws, for the same reason.
 *
 * Deliberately a local copy rather than an import from novelty.mjs: that module is
 * a gate (it decides whether a finding blocks), and widening its exports for an
 * unrelated consumer couples the two. The shared `gh` calls in gh-checks.mjs are
 * deduplicated because getting those wrong is SILENT; getting this wrong yields
 * `null`, which resolves to `full`.
 */
async function git(args, repo) {
  try {
    const { stdout } = await execFileAsync("git", args, {
      cwd: repo,
      timeout: GIT_TIMEOUT_MS,
      maxBuffer: 32 * 1024 * 1024,
      encoding: "utf8",
    });
    return { ok: true, status: 0, stdout };
  } catch (e) {
    return { ok: false, status: typeof e?.code === "number" ? e.code : null, stdout: "" };
  }
}

/**
 * The three git facts `resolveReviewMode` needs about `since..head`.
 *
 * Any fact this cannot establish comes back `null`, which `resolveReviewMode`
 * reads as `git-facts-unavailable` and resolves to `full`. `run` is injected so
 * every branch below is testable without building repositories.
 *
 * `deltaLines` is `null` — not 0 — when a numstat row is non-numeric (a binary
 * file). Counting binary changes as zero lines would UNDER-count the delta, and
 * since the delta size is an upper bound that permits narrowing, under-counting is
 * the fail-open direction.
 */
export async function gitFacts({ since, head, repo, run = git }) {
  const unknown = { isAncestor: null, hasMergeInRange: null, deltaLines: null };
  if (typeof since !== "string" || !SHA.test(since)) return unknown;
  if (typeof head !== "string" || !SHA.test(head)) return unknown;

  // Does the pointer still name a commit here? A pointer that does not resolve is
  // a rewritten or garbage-collected history, and every fact below would be
  // meaningless.
  if (!(await run(["cat-file", "-e", `${since}^{commit}`], repo)).ok) return unknown;

  const anc = await run(["merge-base", "--is-ancestor", since, head], repo);
  // 0 = yes, 1 = git's ANSWER ("no", i.e. force-push/rebase/amend), anything else
  // = the lookup broke and must not read as a confident "no".
  const isAncestor = anc.status === 0 ? true : anc.status === 1 ? false : null;
  if (isAncestor === null) return unknown;

  const merges = await run(["rev-list", "--merges", `${since}..${head}`], repo);
  if (!merges.ok) return unknown;
  const hasMergeInRange = merges.stdout.trim() !== "";

  const stat = await run(["diff", "--numstat", since, head], repo);
  if (!stat.ok) return unknown;
  let deltaLines = 0;
  for (const line of stat.stdout.split("\n")) {
    if (line.trim() === "") continue;
    const [added, deleted] = line.split("\t");
    const a = Number(added), d = Number(deleted);
    if (!Number.isFinite(a) || !Number.isFinite(d)) return { isAncestor, hasMergeInRange, deltaLines: null };
    deltaLines += a + d;
  }
  return { isAncestor, hasMergeInRange, deltaLines };
}

/**
 * Lens ids that will actually review this round, from the manifest and the
 * CUMULATIVE changed-file list.
 *
 * Only these are required to have a pointer. Including a lens that is not running
 * would demand state it never wrote, so every round would report `lens-state-gap`
 * and nothing would ever narrow. Applicability is monotonic in the cumulative list,
 * and a lens that becomes applicable later has no pointer — which forces `full`,
 * so it reads a whole diff before it can ever narrow.
 *
 * Advisory (non-blocking) lenses are included: gating is not the question here.
 * They read the same reviewed artifact, so narrowing hides code from them too.
 */
export function reviewingLensIds(manifest, changedFiles) {
  return (Array.isArray(manifest) ? manifest : [])
    .filter((l) => l && typeof l.id === "string" && l.id !== "")
    .filter((l) => lensApplies(l, Array.isArray(changedFiles) ? changedFiles : []))
    .map((l) => l.id);
}

/**
 * The whole decision, with the API and git injected. Exported so the TWO-PHASE
 * composition is executed by tests, not just its parts: the hazard this shape
 * exists to avoid is measuring git over one range and narrowing to another, and
 * `resolveReviewMode` cannot detect that — it is handed the facts.
 *
 * Never throws; every failure is a `full` decision with a naming `reason`.
 */
export async function decideScope(opts) {
  const {
    pr, head, manifest, changedFiles, repo,
    fullEvery = 3, maxDeltaLines = 400,
    api = gh, run = git, log = console.error,
  } = opts && typeof opts === "object" ? opts : {};
  const full = (reason, rounds = 0) => ({ mode: "full", sinceSha: "", reason, rounds });

  if (!/^\d+$/.test(String(pr ?? "")) || typeof head !== "string" || !SHA.test(head)) {
    log(`review-scope: need a PR number and a 40-hex head (got pr=${JSON.stringify(pr)}, head=${JSON.stringify(head)}).`);
    return full("invalid-input");
  }
  const lensIds = reviewingLensIds(manifest, changedFiles);
  const lensCheckNames = lensIds.map((id) => `agent-review-${id}`);

  const commits = prCommitsWithCheckRuns(pr, { api, log });
  // The round count drives the periodic full rebaseline. `groupReviewRounds` needs
  // no `output.text` to COUNT rounds (a commit with any lens run is a round,
  // `partial` or not), so no per-run back-fill is paid for here.
  const rounds = groupReviewRounds(commits, lensCheckNames).length;
  // The pointer lives in `external_id`; `resolveReviewMode` parses and validates it.
  const states = new Map(
    [...latestLensRuns(allCheckRuns(commits), lensCheckNames)].map(([name, r]) => [name, r?.external_id]),
  );

  // Phase 1: is there a range at all? Measuring git before knowing `since` would
  // measure the wrong range — see the caller contract on `resolveReviewMode`.
  const agreed = agreedReviewedSha(lensIds, states);
  if (!agreed.sha) {
    log(`review-scope: no usable prior pointer (${agreed.reason}).`);
    return full(agreed.reason, rounds);
  }

  // Phase 2: measure exactly `agreed.sha..head`, then decide.
  const facts = await gitFacts({ since: agreed.sha, head, repo, run });
  const decision = resolveReviewMode({
    lensIds, states, headSha: head, roundIndex: rounds, fullEvery, maxDeltaLines, ...facts,
  });
  log(
    `review-scope: ${decision.mode} (${decision.reason}) — round ${rounds}, ${lensIds.length} lens(es), ` +
      `delta ${facts.deltaLines ?? "?"} lines since ${agreed.sha.slice(0, 12)}`,
  );
  return { ...decision, rounds };
}

function readLines(file) {
  try {
    return readFileSync(file, "utf8").split("\n").map((s) => s.trim()).filter(Boolean);
  } catch {
    return [];
  }
}

function setOutput(name, value) {
  const line = `${name}=${value}`;
  console.error(`  ${line}`);
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${line}\n`);
}

function emit({ mode, sinceSha, reason, rounds }) {
  // `since` is only ever non-empty for `incremental`, and the workflow keys the
  // narrowed `git diff` off `mode`. Emitting both keeps the pair inseparable:
  // review-panel.mjs REFUSES `--since-sha` without `--review-mode incremental`
  // and vice versa, so a half-passed pair fails the panel loudly rather than
  // reviewing a fragment as if it were the whole PR.
  setOutput("mode", mode);
  setOutput("since", mode === "incremental" ? sinceSha : "");
  setOutput("reason", reason);
  setOutput("rounds", String(rounds ?? 0));
}

async function main() {
  const args = parseArgs(process.argv);
  const num = (v, dflt) => {
    const n = Number(v);
    return Number.isFinite(n) && n > 0 ? n : dflt;
  };
  let manifest = null;
  try {
    manifest = JSON.parse(readFileSync(args.lenses, "utf8"));
  } catch (err) {
    // Left null, so `reviewingLensIds` yields no lenses and the decision is
    // `invalid-input` → full. Logged, because "we never narrowed" and "we narrowed
    // and it saved nothing" look identical from the outside otherwise.
    console.error(`review-scope: could not read --lenses '${args.lenses}' (${err.message}).`);
  }
  emit(
    await decideScope({
      pr: args._[0],
      head: typeof args.head === "string" ? args.head.trim() : "",
      manifest,
      changedFiles: readLines(args["changed-files"]),
      repo: args.repo || process.cwd(),
      fullEvery: num(args["full-every"], 3),
      maxDeltaLines: num(args["max-delta-lines"], 400),
    }),
  );
}

// A crash must not take the panel down: report `full` and exit 0. There is no
// failure of this script that a full review does not correctly absorb.
if (process.argv[1] && process.argv[1].endsWith("review-scope.mjs")) {
  main().catch((err) => {
    console.error(`review-scope: crashed (${err.message}) — reviewing in full.`);
    emit({ mode: "full", sinceSha: "", reason: "scope-error", rounds: 0 });
  });
}
