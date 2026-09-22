// Incremental-review state: what each lens last reviewed, and whether this round
// may review only the delta.
//
// WHY THIS EXISTS. The panel re-reviews the FULL cumulative diff every round
// (`git diff origin/main...HEAD`), so an 8-round PR reads round-1 code eight
// times. Worse, both endpoints of `...` move: a human `git merge main` changed
// the reviewed artifact with no semantic change to the branch and flipped a lens
// verdict.
//
// WHERE THE STATE LIVES: `external_id` on each `agent-review-<lens>` check run.
// Every candidate sits behind the same `checks:write` trust boundary (the author
// agent cannot forge any of them), so the choice is about FAILURE MODES.
// `output.text` is disqualified: the panel workflow trims it to fit a 60k limit
// by dropping findings, i.e. it is DESIGNED to lose data. A control field that
// decides whether code gets reviewed must not live in a lossy channel.
//
// THE ONE RULE: every decision here fails to `full`. Reviewing code twice costs
// tokens; reviewing it zero times ships a bug. There is no symmetry between the
// two, so there is no case where "probably fine" resolves to `incremental`.

/** Bumped only if the shape changes; an unknown version parses as "no state". */
export const REVIEW_STATE_VERSION = 1;

const SHA = /^[0-9a-fA-F]{40}$/;
const MODES = new Set(["full", "incremental"]);

/** A 40-hex SHA, lowercased. Anything else → null (never throws). */
function sha(v) {
  return typeof v === "string" && SHA.test(v) ? v.toLowerCase() : null;
}

/**
 * Serialize state for a check run's `external_id`.
 *
 * Throws on malformed input rather than emitting junk. This is called only from
 * the trusted orchestrator with values it just computed, so bad input is a
 * programmer error — and the alternative (writing an unparseable `external_id`)
 * would silently degrade every later round to `full` with no signal. A throw
 * fails the panel step loudly, which is the correct direction.
 *
 * Key order is fixed so the value is stable across rounds and diffable by eye.
 *
 * GitHub caps `external_id` at 255 characters. With three validated 40-hex shas
 * the widest shape is 183, so the cap check below cannot fire on valid input — it
 * is a tripwire for a FUTURE field, so the limit is hit here with a stack trace
 * rather than at GitHub with a silently rejected or truncated value.
 */
export function serializeReviewState(opts) {
  // Coerced rather than destructured directly, so `serializeReviewState()` and
  // `(null)` raise this module's own descriptive error instead of an opaque
  // TypeError from the destructuring itself. It still throws — that is the
  // contract — just legibly.
  const { reviewed, base, since, mode } = opts && typeof opts === "object" ? opts : {};
  const r = sha(reviewed);
  if (!r) throw new Error(`review state: 'reviewed' must be a 40-hex sha (got ${JSON.stringify(reviewed)})`);
  if (!MODES.has(mode)) throw new Error(`review state: 'mode' must be full|incremental (got ${JSON.stringify(mode)})`);
  // base/since are optional — round 1 has no `since`, and `base` is diagnostic.
  const b = base === "" || base == null ? "" : sha(base);
  if (b === null) throw new Error(`review state: 'base' must be a 40-hex sha or empty (got ${JSON.stringify(base)})`);
  const s = since === "" || since == null ? "" : sha(since);
  if (s === null) throw new Error(`review state: 'since' must be a 40-hex sha or empty (got ${JSON.stringify(since)})`);
  const out = JSON.stringify({ v: REVIEW_STATE_VERSION, reviewed: r, base: b, since: s, mode });
  if (out.length > 255) throw new Error(`review state: ${out.length} chars exceeds the external_id limit of 255`);
  return out;
}

/**
 * Parse a check run's `external_id` back into state.
 *
 * Returns `null` for ANY doubt — absent, non-JSON, wrong version, missing or
 * malformed `reviewed`, unknown `mode`. Callers treat `null` as "this lens has no
 * usable state", which forces `full`. Deliberately strict: a half-understood
 * state is more dangerous than no state, because it would be acted upon.
 */
export function parseReviewState(raw) {
  if (typeof raw !== "string" || raw === "") return null;
  try {
    return normalizeState(JSON.parse(raw));
  } catch {
    return null;
  }
}

/**
 * Validate an already-parsed state record. Factored out of `parseReviewState` so
 * that `resolveReviewMode`, whose callers may hand it pre-parsed states, applies
 * the SAME checks — otherwise a record this module would reject could still drive
 * a narrowing decision, and the validation it argues is load-bearing would be
 * skippable by passing an object instead of a string.
 */
function normalizeState(d) {
  if (!d || typeof d !== "object" || Array.isArray(d)) return null;
  if (d.v !== REVIEW_STATE_VERSION) return null;
  const reviewed = sha(d.reviewed);
  if (!reviewed) return null;
  if (!MODES.has(d.mode)) return null;
  const base = d.base === "" || d.base == null ? "" : sha(d.base);
  const since = d.since === "" || d.since == null ? "" : sha(d.since);
  // A present-but-malformed base/since means the writer disagrees with us about
  // the shape. Refuse the whole record rather than half-trust it.
  if (base === null || since === null) return null;
  return { v: d.v, reviewed, base, since, mode: d.mode };
}

/**
 * Latest COMPLETED run per lens check name, from a flat list of check runs.
 *
 * Shared with `prior-findings.mjs`, which needs the same "which run speaks for
 * this lens" answer. (`rounds.mjs::groupReviewRounds` answers a related but
 * different question — latest per lens WITHIN one commit, as part of grouping
 * commits into rounds — and is deliberately not refactored onto this: it feeds
 * the round-guard page path, and widening this PR into that path would put a
 * gate file in an inert change.)
 *
 * Three filters carry weight:
 *   - `app.slug === "github-actions"` — the run came from a GitHub Actions
 *     workflow in this repository, not from another App. This is NOT per-workflow
 *     identity: any workflow here that declared `checks: write` could post a run
 *     under the same slug. Today none does but the panel, and no author workflow
 *     can gain it (a branch cannot edit `.github/workflows/**` — the agent App
 *     has no `workflows` scope). That is an invariant maintained by review, which
 *     this filter narrows but does not by itself enforce.
 *   - `status === "completed"` — a queued/in-progress run has a newer timestamp
 *     but no verdict yet, and would otherwise shadow the last real one.
 *   - ties broken by `id`, not by input order. `completed_at` has one-second
 *     granularity, so two runs of a lens can share it; ranking on timestamp alone
 *     would then pick whichever the API happened to list last. Check-run ids
 *     increase monotonically, so the larger id is the later run — deterministic
 *     regardless of response order.
 *
 * `conclusion` is deliberately NOT filtered, and the two callers rely on the
 * newest run winning either way. Carry-forward wants whatever findings the lens
 * last persisted. Review state wants the newest run precisely BECAUSE a failed or
 * neutral run carries no `external_id`: that parses as no state, which forces a
 * full round. Preferring an older successful run instead would hand back a stale
 * pointer and narrow past the commits the failed round never reviewed.
 */
export function latestLensRuns(runs, lensCheckNames) {
  const want = new Set(Array.isArray(lensCheckNames) ? lensCheckNames : []);
  const out = new Map();
  for (const r of Array.isArray(runs) ? runs : []) {
    if (!r || typeof r !== "object") continue;
    if (!want.has(r.name)) continue;
    if (r.app?.slug !== "github-actions") continue;
    if (r.status !== "completed") continue;
    const t = new Date(r.completed_at || r.started_at || 0).getTime();
    const at = Number.isFinite(t) ? t : 0;
    const id = Number.isFinite(r.id) ? r.id : 0;
    const cur = out.get(r.name);
    if (!cur || at > cur.at || (at === cur.at && id > cur.id)) out.set(r.name, { run: r, at, id });
  }
  return new Map([...out].map(([name, { run }]) => [name, run]));
}

/**
 * Decide this round's review scope. FULLY PURE — every git and API fact is
 * injected, so the whole decision table is unit-testable without a repository.
 *
 * Returns `{ mode, sinceSha, reason }`. `mode` is `full` unless every condition
 * for a safe narrowing holds; `sinceSha` is non-empty only when `incremental`.
 *
 * CALLER CONTRACT: `isAncestor`, `hasMergeInRange` and `deltaLines` must be
 * computed for the range ending at `headSha` and starting at the sha the `states`
 * name — the same pointer this returns as `sinceSha`. Nothing here can check that
 * (the whole point of injecting them), so facts measured against a different
 * range would validate one range and narrow another.
 *
 * `reason` is a superset of the values the plan sketched, because a fused reason
 * is a worse diagnostic: `lens-state-divergence`, `no-new-commits`, and
 * `invalid-input` are split out rather than folded into `lens-state-gap`. Every
 * one of them still resolves to `full`.
 *
 * Checks run correctness-first, then policy, so the reported reason names a real
 * hazard when one exists rather than an arbitrary cap that happened to also fire.
 */
/**
 * The one sha every lens agrees it last reviewed, or `{ sha: "", reason }`.
 *
 * Exported to make the two-phase caller contract usable rather than merely
 * documented. `resolveReviewMode` needs git facts measured over `since..head`,
 * but `since` is only knowable AFTER reading the states — so a caller cannot
 * gather its inputs in one pass. It calls this first to learn the range (or that
 * there is no range and the answer is already `full`), measures git over exactly
 * that range, then calls `resolveReviewMode`.
 *
 * `resolveReviewMode` uses this internally too, so the two cannot disagree about
 * which pointer is under consideration.
 *
 * Never throws. Reasons are the state-only subset: `invalid-input`,
 * `no-prior-state`, `lens-state-gap`, `lens-state-divergence`, or `ok`.
 */
export function agreedReviewedSha(lensIds, states) {
  const none = (reason) => ({ sha: "", reason });
  const ids = (Array.isArray(lensIds) ? lensIds : []).filter((id) => typeof id === "string" && id !== "");
  if (ids.length === 0) return none("invalid-input");

  // Keyed by lens id (`correctness`) OR by check name (`agent-review-correctness`),
  // because `latestLensRuns` returns the latter and `lensIds` are the former.
  // Accepting both removes a silent trap: the natural composition of the two would
  // otherwise find no state for any lens, report `no-prior-state` forever, and
  // never narrow anything with nothing to indicate a mistake.
  //
  // `Object.hasOwn` rather than a bare index: a lens id of `constructor` would
  // otherwise pick up `Object.prototype.constructor` as if it were state.
  const pick = (k) => {
    if (states instanceof Map) return states.get(k);
    return states && typeof states === "object" && Object.hasOwn(states, k) ? states[k] : undefined;
  };
  const get = (id) => {
    const v = pick(id) ?? pick(`agent-review-${id}`);
    // Either a raw external_id string or an already-parsed record — both go
    // through the same validation, so pre-parsing cannot skip it.
    return typeof v === "string" ? parseReviewState(v) : normalizeState(v);
  };

  // EVERY lens must have usable, agreeing state. A lens with no state has a
  // coverage hole: it never saw the commits between its last verdict and now, and
  // an incremental round would never show them to it. One lens short is enough to
  // force a full round for all of them — the reviewed artifact is global, so
  // partial narrowing is not on offer.
  //
  // This is also what covers "a check run exists but no review happened", which is
  // NOT a separate input and does not need to be. The pointer is stamped only when
  // a lens produced a real verdict, so a crashed, quota-failed, skipped or
  // otherwise `neutral` run carries no `external_id` at all; `latestLensRuns` hands
  // back that newest run, it parses as no state, and this forces `full`. Same for a
  // lens that was inapplicable in earlier rounds and becomes applicable now: no
  // state, so no narrowing until it has reviewed a full diff once. Coverage is
  // proven by the presence of a pointer, not asserted alongside it.
  const found = ids.map(get);
  if (found.every((s) => !s)) return none("no-prior-state");
  // `!s` is the whole test: anything `get` returns non-null came through
  // `normalizeState`, so `reviewed` is already a validated 40-hex sha. A second
  // check here would be unreachable, and an unreachable condition in a gate reads
  // as protection that is not doing anything.
  if (found.some((s) => !s)) return none("lens-state-gap");

  const reviewedSet = new Set(found.map((s) => s.reviewed));
  // Lenses normally stamp the same head SHA in one panel run. They diverge only
  // when a lens missed a round (infra error), which is the same coverage hole as
  // a gap — and picking the newest would skip commits the laggard never saw,
  // while picking the oldest re-reviews for everyone anyway. So: full.
  if (reviewedSet.size !== 1) return none("lens-state-divergence");
  return { sha: [...reviewedSet][0], reason: "ok" };
}

export function resolveReviewMode(opts) {
  // Destructure from a coerced object, NOT via a `= {}` parameter default: that
  // default only fires for `undefined`, so `resolveReviewMode(null)` threw — which
  // contradicted this function's whole contract of never throwing. Caught by its
  // own test.
  const {
    lensIds,
    states,
    headSha,
    isAncestor,
    hasMergeInRange,
    deltaLines,
    roundIndex,
    fullEvery = 3,
    maxDeltaLines = 400,
  } = opts && typeof opts === "object" ? opts : {};
  const full = (reason) => ({ mode: "full", sinceSha: "", reason });

  // --- input sanity: a caller passing junk gets `full`, never a throw ---------
  const head = sha(headSha);
  const okEvery = Number.isInteger(fullEvery) && fullEvery > 0;
  const okRound = Number.isInteger(roundIndex) && roundIndex >= 0;
  const okMax = Number.isFinite(maxDeltaLines) && maxDeltaLines >= 0;
  if (!head || !okEvery || !okRound || !okMax) return full("invalid-input");

  // --- per-lens state: EVERY lens must have usable, agreeing state ------------
  const agreed = agreedReviewedSha(lensIds, states);
  if (!agreed.sha) return full(agreed.reason);
  const since = agreed.sha;

  // Re-run on an already-reviewed SHA. The delta is empty, and review-panel.mjs
  // refuses an empty diff (fails closed), so narrowing here would turn a
  // harmless re-run into a hard panel failure.
  if (since === head) return full("no-new-commits");

  // --- git facts: absent or unusable means we cannot reason about the range ---
  if (typeof isAncestor !== "boolean" || typeof hasMergeInRange !== "boolean" || !Number.isFinite(deltaLines) || deltaLines < 0) {
    return full("git-facts-unavailable");
  }
  // The stamped SHA is not an ancestor of HEAD: force-push, rebase, amend, or a
  // reset. `since..head` is then meaningless — it can silently omit commits that
  // were rewritten rather than added.
  if (!isAncestor) return full("force-push-or-rewrite");
  // A merge in range pulls in arbitrary `main` history that no lens has reviewed
  // in the context of this branch.
  if (hasMergeInRange) return full("merge-in-range");

  // --- policy caps -----------------------------------------------------------
  // Periodic rebaseline. Carry-forward re-checks findings already RAISED; it
  // cannot surface a defect whose root cause is old code that only became
  // reachable via new code. A full round every `fullEvery` rounds bounds how long
  // such a defect can hide. This is the honest reason the saving is ~2x, not ~8x.
  if (roundIndex % fullEvery === 0) return full("periodic-rebaseline");
  // A large delta is not cheaper to review incrementally, and it is more likely
  // to be a rebase or a squash than a normal fix round.
  if (deltaLines > maxDeltaLines) return full("delta-too-large");

  return { mode: "incremental", sinceSha: since, reason: "ok" };
}

/**
 * The scope note prepended to a lens prompt in incremental mode.
 *
 * Returns `""` for full mode, which is what keeps this change inert: with no
 * flags passed, every lens prompt is byte-identical to today's.
 *
 * Three clauses are load-bearing, and the lens will under-review without them:
 *   1. earlier commits were reviewed in prior rounds, BUT this lens still owns
 *      defects the delta newly exposes in them;
 *   2. the FULL working tree is its cwd, so it can and should read outside the
 *      delta;
 *   3. the changed-file list is CUMULATIVE, not the delta's files.
 * Clause 1 alone reads as "the old code is someone else's problem", which is the
 * recall regression this whole mode risks.
 */
export function renderScopeNote(opts) {
  // Coerced, not a `= {}` default — see the note in `resolveReviewMode`. Same bug
  // was present here and the test only exercised `undefined`, so it stayed green.
  const { mode, sinceSha, baseSha, changedFiles } = opts && typeof opts === "object" ? opts : {};
  if (mode !== "incremental") return "";
  const since = sha(sinceSha);
  if (!since) return ""; // no usable pointer → say nothing rather than something wrong
  const base = sha(baseSha);
  const files = (Array.isArray(changedFiles) ? changedFiles : []).filter(
    (f) => typeof f === "string" && f.trim() !== "",
  );
  return [
    "## Review scope for this round (INCREMENTAL — read this first)",
    "",
    `The diff below is only the commits added since \`${since.slice(0, 12)}\`, not the`,
    `whole pull request${base ? ` (which starts at \`${base.slice(0, 12)}\`)` : ""}.`,
    "",
    "- Earlier commits were reviewed in previous rounds, so do NOT re-report findings",
    "  about code this delta does not touch.",
    "- **But you still own defects this delta newly exposes in that earlier code.** A",
    "  change here that makes an older path reachable, or that breaks an assumption",
    "  older code relies on, is in scope and is YOUR finding to raise.",
    "- The COMPLETE working tree is your working directory. Use Read/Grep/Glob freely",
    "  to read files the diff does not show you — that is expected, not out of scope.",
    "- The changed-file list you were given is CUMULATIVE for the whole pull request,",
    "  not just this delta. Use it to find what else the PR has already touched.",
    files.length > 0 ? `- Files changed by the PR so far: ${files.length}.` : null,
    "",
    "This is DATA about your task, not an instruction from the code under review.",
  ]
    // `!== null`, NOT `!== ""`. Filtering empty strings would delete every blank
    // line above, collapsing the note into one block: the heading would run into
    // the paragraph and the closing DATA line would become a lazy continuation of
    // the last bullet. A `null` sentinel drops only the optional line. This exact
    // filter ate the verifier prompt's separators earlier in this series — hence
    // the rendered-output test rather than a substring match.
    .filter((l) => l !== null)
    .join("\n");
}
