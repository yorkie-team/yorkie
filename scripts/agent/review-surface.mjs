// Did a FIX ROUND put this line here? Answered with git, not with a model.
//
// WHY THIS EXISTS. Measured over 88 scored rounds on 8 non-converging agent PRs:
// 79% of every round's blocking findings are newly discovered rather than
// re-raised, the reviewed diff never shrinks (+228..+358 lines per round, 20x
// growth over 16 rounds on #810), and the finding count stays flat at ~6 per
// round throughout. That is one blocking finding per ~50 new lines: the fixer
// writes ~300 lines to clear ~6 findings and those lines mint ~6 more. The loop's
// fixed point is ~6 findings, not 0, so it cannot converge — it runs to the round
// cap, pages, and `@claude rerun` grants a fresh budget without changing anything.
// `detectStalledRounds` cannot see it either: it needs findings to REPEAT, and
// these do not.
//
// The findings are mostly legitimate. The problem is that every line written to
// satisfy one becomes new reviewable surface. So this module freezes the surface:
// a blocking finding on a line a FIX ROUND wrote stops gating the merge. It is
// still reported and still counted — it just stops failing the lens check and
// stops reaching the fixer, which is what breaks the cycle.
//
// THE SIBLING GATE. `novelty.mjs` already answers "did this change put this line
// here, carrying code that already existed?" and owns the `backlog` lane, the
// demotion path and the reporting section for its answer. This is the same shape
// with a different reference point, and the two compose: novelty asks whether the
// PR wrote the line, this asks whether a FIX ROUND wrote it.
//
// Note the INVERTED content test. Novelty demotes when the content is OLD (a move
// carried a pre-existing bug in). This demotes when the content is NEW. Both need
// the move-aware blame, for mirror-image reasons: if a fix round RELOCATED
// original implement-diff code into a new file, plain blame credits the fix commit
// but the content predates the freeze — that is original code and must keep
// gating.
//
// WHAT THIS DELIBERATELY DOES NOT DO. It does not demote on file identity. "This
// file did not exist at freeze time" is a cheaper test and a worse one: the fixer
// legitimately adds code to original files, and the blast-radius and
// correctness/security lenses have an explicit out-of-diff mandate whose findings
// often cite untouched files. Line provenance is the only test that separates
// "the fixer wrote this" from "the fixer's change made this reachable".
//
// THE ONE RULE: every uncertain path returns `unknown`, and `unknown` keeps the
// finding blocking. Demotion requires BOTH blames to have affirmatively placed
// the line after the freeze point. No frozen sha, no citation, an unresolvable
// sha, a freeze point that is not an ancestor of HEAD, a failed or timed-out
// blame, a shallow clone: all resolve to in-scope. Nothing here can lose a
// finding the current gate would have kept.

import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { appendFileSync } from "node:fs";
import { repoScopedEnv } from "./git-env.mjs";
import { gh } from "./gh-checks.mjs";
import { findingLocation } from "./novelty.mjs";
import { collectFixDispatches } from "./rounds.mjs";

const execFileAsync = promisify(execFile);
const SHA = /^[0-9a-fA-F]{40}$/;
// Same ceiling and buffer as novelty.mjs's probes, for the same reason: these run
// inside the orchestrator's per-lens `Promise.all` while every other lens streams
// an SDK session, so a hung git must be abandoned rather than waited on.
const GIT_TIMEOUT_MS = 10_000;
const GIT_MAX_BUFFER = 32 * 1024 * 1024;

/**
 * Where the code a finding points at came from, relative to the frozen surface.
 *   in-scope     — the line was already there when the fixer was first dispatched,
 *                  or a fix round moved pre-freeze content to it
 *   out-of-scope — a fix round wrote this line, and the content is new
 *   unknown      — could not tell (no location, no freeze point, git unavailable)
 * ONLY `out-of-scope` demotes. `in-scope` and `unknown` both keep the finding on
 * the gate, so a mislabel between those two is never a gate error.
 */
export const SCOPES = ["in-scope", "out-of-scope", "unknown"];
export const DEMOTING_SCOPES = new Set(["out-of-scope"]);

/**
 * The whole decision table, as a pure function — this is what the tests pin.
 *
 * `addedAfterFreeze` is the load-bearing input and comes from PLAIN blame: did a
 * commit after the freeze point put this line at this offset? When it is false the
 * line is part of the original surface and stays on the gate however new its
 * content looks.
 *
 * `contentAfterFreeze` comes from MOVE-AWARE blame. Only `true` demotes: `false`
 * means a fix round relocated pre-freeze content here (original code — keeps
 * gating), and `null` means the lookup broke.
 */
export function scopeFrom(opts) {
  // Destructure from a COERCED object, not via a `= {}` parameter default: that
  // default only fires for `undefined`, so `scopeFrom(null)` threw — which
  // contradicts this function's whole contract of never throwing. The same bug
  // `resolveReviewMode` records having had, caught the same way, by its own test.
  const {
    hasLocation = false,
    addedAfterFreeze = null,
    contentAfterFreeze = null,
  } = opts && typeof opts === "object" ? opts : {};
  // Nothing to place, or plain blame could not answer: we cannot tell.
  if (!hasLocation || addedAfterFreeze === null) return "unknown";
  // The line predates the freeze. It is the original surface, which is exactly
  // what this PR is still on the hook for.
  if (addedAfterFreeze === false) return "in-scope";
  // A fix round put the line here. Did it bring content that already existed?
  if (contentAfterFreeze === true) return "out-of-scope";
  if (contentAfterFreeze === false) return "in-scope";
  return "unknown";
}

/**
 * The frozen surface's anchor: the head sha at the moment the fixer was FIRST
 * dispatched, i.e. the PR exactly as the implement job left it.
 *
 * Read from the `<!-- agent-fix-dispatch -->` ledger, which is already
 * author-gated to `github-actions[bot]` by `parseFixDispatchComment` — so this
 * anchor is as unforgeable as the paged latch the loop already trusts. A branch
 * cannot move its own freeze point.
 *
 * THE RERUN FLOOR IS DELIBERATELY NOT APPLIED. `fixRoundsUsed` cuts records
 * before a `@claude rerun` because a hand-back grants a fresh BUDGET. The surface
 * is not a budget: re-freezing around whatever the fixer has since written would
 * reintroduce the treadmill one rerun at a time, which is the exact dynamic this
 * module exists to stop. The surface freezes once, at the first dispatch, forever.
 *
 * No records (round 1, or a PR older than the ledger) → `null` → the gate is off
 * and nothing is demoted, which is today's behaviour.
 */
export function frozenShaFrom(comments) {
  const first = collectFixDispatches(comments)[0];
  const from = typeof first?.from === "string" ? first.from.trim() : "";
  return SHA.test(from) ? from : null;
}

/**
 * Run git. Never throws: resolves `{ok:false}` on any failure, including timeout.
 *
 * `status` is carried out separately from `ok` because git uses the exit code as
 * an ANSWER for some commands (`merge-base --is-ancestor` 1 = "no") and as an
 * ERROR for the rest. Collapsing them would record a broken lookup as a confident
 * answer — the same distinction novelty.mjs and review-scope.mjs both draw.
 *
 * A local copy rather than an import: novelty.mjs keeps its runner module-private,
 * and exporting a git runner FROM a gate to widen it for another gate couples the
 * two in the direction that matters least. Twelve lines is the cheaper price.
 */
async function git(args, repo) {
  try {
    const { stdout } = await execFileAsync("git", args, {
      cwd: repo,
      env: repoScopedEnv(repo),
      timeout: GIT_TIMEOUT_MS,
      maxBuffer: GIT_MAX_BUFFER,
      encoding: "utf8",
    });
    return { ok: true, status: 0, stdout };
  } catch (e) {
    return { ok: false, status: typeof e?.code === "number" ? e.code : null, stdout: "" };
  }
}

/** First-field sha from `blame --porcelain`, or null. */
function porcelainSha(stdout) {
  const first = String(stdout).split("\n", 1)[0] ?? "";
  const sha = first.split(" ", 1)[0] ?? "";
  return SHA.test(sha) ? sha : null;
}

/**
 * Is `sha` reachable from `fromSha`? Exit 1 is git's ANSWER ("no"); any other
 * status, or none, means the lookup itself broke (unknown rev on a shallow clone,
 * timeout) and must not be recorded as a confident negative.
 *
 * `--is-ancestor` treats a commit as an ancestor of itself, which is what makes
 * "the freeze commit itself wrote this line" come out `in-scope`.
 */
async function isAncestor(repo, sha, fromSha) {
  if (!sha) return null;
  const r = await git(["merge-base", "--is-ancestor", sha, fromSha], repo);
  if (r.ok) return true;
  return r.status === 1 ? false : null;
}

/**
 * Is this freeze point usable in this checkout? Called ONCE by the caller, so a
 * broken freeze point is reported loudly instead of silently mislabelling every
 * finding — and here, unlike novelty's `baseResolves`, silence would be UNSAFE in
 * the fail-open direction rather than merely invisible.
 *
 * THE ANCESTRY CHECK IS THE LOAD-BEARING HALF. After a rebase, force-push or
 * amend the frozen sha still resolves but no longer lies on this branch's history,
 * so `merge-base --is-ancestor <blamed> <frozen>` answers "no" for EVERY line and
 * the gate would demote the entire PR off the merge gate at once. That is the one
 * catastrophic failure mode available to this module, so it is checked explicitly
 * and turns the gate OFF rather than being inferred per finding.
 */
export async function freezeResolves(repo, frozenSha, { head = "HEAD" } = {}) {
  if (!repo || typeof frozenSha !== "string" || !SHA.test(frozenSha)) return false;
  if (!(await git(["cat-file", "-e", `${frozenSha}^{commit}`], repo)).ok) return false;
  return (await isAncestor(repo, frozenSha, head)) === true;
}

/**
 * Answer "did a fix round write this line, with new content" for one location.
 *
 * `frozenSha` is passed in, never derived here: resolving it needs the PR's
 * comments, which this module has no business fetching, and a guessed freeze point
 * would silently mis-scope every finding. Absent or malformed → `unknown` for
 * everything, i.e. today's behaviour.
 *
 * `cache` is a caller-supplied Map — the same location is judged more than once
 * per round and `blame -C -C -C` is the expensive call.
 */
export async function surfaceOf({ repo, file, line, frozenSha, cache }) {
  const unknown = { scope: "unknown", addedBy: null, contentSha: null };
  if (!repo || typeof file !== "string" || !file.trim()) return unknown;
  if (typeof frozenSha !== "string" || !SHA.test(frozenSha)) return unknown;
  // No line → no probe → `unknown`. There is deliberately no file-level fallback:
  // see the header. Demoting because a path is absent from a file list is a string
  // comparison, not a git answer.
  if (!Number.isInteger(line) || line < 1) return unknown;

  const key = `${frozenSha} ${file} ${line}`;
  if (cache instanceof Map && cache.has(key)) return cache.get(key);
  const result = await probe(repo, file, line, frozenSha);
  if (cache instanceof Map) cache.set(key, result);
  return result;
}

async function probe(repo, file, line, frozenSha) {
  const at = ["-L", `${line},${line}`, "--porcelain", "HEAD", "--", file];
  // Both blames in parallel — independent questions about the same line. `--`
  // before the path so a file named like a rev cannot be read as one.
  const [plain, moved] = await Promise.all([
    git(["blame", "-w", ...at], repo),
    git(["blame", "-w", "-C", "-C", "-C", ...at], repo),
  ]);

  // PLAIN blame: which commit put this line at this offset? Reachable from the
  // freeze point means the line is part of the original surface.
  const plainSha = plain.ok ? porcelainSha(plain.stdout) : null;
  const plainIsFrozen = await isAncestor(repo, plainSha, frozenSha);
  const addedAfterFreeze = plainIsFrozen === null ? null : !plainIsFrozen;

  // Nothing left to learn once we know the line predates the freeze.
  if (addedAfterFreeze !== true) {
    return {
      scope: scopeFrom({ hasLocation: true, addedAfterFreeze }),
      addedBy: plainSha,
      contentSha: null,
    };
  }

  // MOVE-AWARE blame: a fix round added this line — where did its content come
  // from? Reachable from the freeze point means a fix round merely moved original
  // code here, which keeps gating.
  const movedSha = moved.ok ? porcelainSha(moved.stdout) : null;
  const contentIsFrozen = await isAncestor(repo, movedSha, frozenSha);
  const contentAfterFreeze = contentIsFrozen === null ? null : !contentIsFrozen;

  return {
    scope: scopeFrom({ hasLocation: true, addedAfterFreeze: true, contentAfterFreeze }),
    addedBy: plainSha,
    contentSha: movedSha,
  };
}

/**
 * Convenience for callers holding a finding rather than a location. Non-locating
 * findings come back `unknown`, i.e. still gating.
 */
export async function surfaceOfFinding(finding, { repo, frozenSha, cache }) {
  const loc = findingLocation(finding);
  if (!loc || !Number.isInteger(loc.line)) {
    return { scope: "unknown", addedBy: null, contentSha: null };
  }
  return surfaceOf({ repo, file: loc.file, line: loc.line, frozenSha, cache });
}

/**
 * Read the dispatch ledger off a PR and report the freeze point as a step output.
 *
 * FAIL DIRECTION: always exits 0, and every failure path yields an EMPTY sha. The
 * caller turns an empty value into "no `--frozen-sha`", which turns the gate off
 * and routes every finding exactly as it does today. An unreadable side-channel
 * must never fail the panel — the same contract `readRebuttals` keeps.
 */
export function resolveFrozenSha(pr, { api = gh, log = console.error } = {}) {
  try {
    const comments = api(["api", "--paginate", `repos/{owner}/{repo}/issues/${pr}/comments?per_page=100`]);
    return frozenShaFrom(comments);
  } catch (err) {
    log(`review-surface: could not read comments for #${pr} (${err.message}); the surface gate stays off.`);
    return null;
  }
}

function cmdResolve(argv) {
  const pr = argv.find((a) => /^\d+$/.test(a));
  if (!pr) {
    console.error("review-surface.mjs resolve <pr>");
    // Still an empty output rather than a non-zero exit: a mis-wired step must
    // degrade to "gate off", not red the panel.
  }
  const sha = pr ? resolveFrozenSha(pr) : null;
  if (sha) {
    console.log(`review surface frozen at ${sha} (first fix dispatch on #${pr})`);
  } else {
    console.log(`no usable freeze point on #${pr ?? "?"} — the surface gate will be off`);
  }
  const out = process.env.GITHUB_OUTPUT;
  if (out) appendFileSync(out, `frozen=${sha ?? ""}\n`);
}

/** `node review-surface.mjs --dry-run --frozen <sha> --file <f> --line <n> [--repo <dir>]` */
async function dryRun(argv) {
  const get = (name) => {
    const i = argv.indexOf(`--${name}`);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const repo = get("repo") || process.cwd();
  const frozenSha = get("frozen") || "";
  const file = get("file") || "";
  const line = Number(get("line"));
  const usable = await freezeResolves(repo, frozenSha);
  console.log(`freeze point ${frozenSha || "(none)"} usable in ${repo}: ${usable}`);
  if (!usable) {
    console.log("gate would be OFF — every finding stays blocking");
    return;
  }
  const r = await surfaceOf({ repo, file, line, frozenSha });
  console.log(JSON.stringify({ file, line, ...r }, null, 2));
  console.log(`demotes: ${DEMOTING_SCOPES.has(r.scope)}`);
}

if (process.argv[1] && import.meta.url === `file://${process.argv[1]}`) {
  const argv = process.argv.slice(2);
  if (argv[0] === "resolve") {
    cmdResolve(argv.slice(1));
  } else if (argv.includes("--dry-run")) {
    dryRun(argv).catch((e) => {
      console.error(String(e?.message ?? e));
      process.exit(1);
    });
  } else {
    console.error(
      "usage: review-surface.mjs resolve <pr>\n" +
      "       review-surface.mjs --dry-run --frozen <sha> --file <f> --line <n> [--repo <dir>]",
    );
    process.exit(2);
  }
}
