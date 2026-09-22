// A pool of Claude credentials for the agent pipeline: spread load across
// several accounts, and survive one of them hitting its usage window.
//
// TWO RULES, and the first one is the whole design:
//
//   1. ONE TOKEN PER JOB. Prompt caches are scoped to the account that wrote
//      them, and `createWarmupGate()` (review-panel.mjs) exists to pay for the
//      shared diff prefix exactly once and have every other lens and sample read
//      it. Rotating per CALL gives each token a cold cache, so the panel pays
//      that warm-up once per token instead of once per round — which costs more
//      in input tokens than the distribution saves. So `current()` is stable for
//      the life of the process, and distribution happens ACROSS jobs.
//   2. Switch only on exhaustion. `advance()` forfeits the warm cache for the
//      rest of the job, which is the correct trade only because the alternative
//      is not a cheaper round but no round at all.
//
// Selection is derived from the run id rather than shared state: two jobs never
// coordinate, they just land on different tokens because their run ids differ.
// The cost of that is real and bounded — jobs starting inside the same usage
// window each discover an exhausted token independently, one wasted call each.

/** Highest suffixed slot read from the environment. */
export const MAX_SLOTS = 8;

/** Base name; the unsuffixed variable is slot zero, `_1`..`_MAX_SLOTS` follow. */
export const TOKEN_ENV = "CLAUDE_CODE_OAUTH_TOKEN";

/**
 * Every environment variable the pool occupies.
 *
 * Derived rather than written out, so the list cannot drift from `MAX_SLOTS`.
 * `credentialEnv()` in ask.mjs subtracts these from the child environment: the
 * process that reads the untrusted checkout needs the ONE credential it was
 * given, and handing it the other eight only widens what a bug there could take.
 */
export function poolEnvNames() {
  return [TOKEN_ENV, ...Array.from({ length: MAX_SLOTS }, (_, i) => `${TOKEN_ENV}_${i + 1}`)];
}

/**
 * The env-var suffix for a slot name: `CLAUDE_CODE_OAUTH_TOKEN_3` → `"3"`, and the
 * unsuffixed slot-zero name → `""`. Anything that is not one of THIS pool's names →
 * `null`.
 *
 * Lives here because this module owns `TOKEN_ENV` and `MAX_SLOTS`, so it is the only
 * place that can answer without a second copy of the naming rule drifting from it.
 *
 * The `null` matters. Callers interpolate the result into a `secrets[...]` lookup in a
 * workflow, so a name arriving from an artifact — or from anywhere else — must not be
 * able to aim that lookup at a different secret. The suffix is re-derived from the
 * KNOWN slot list rather than parsed off whatever string turned up.
 */
export function slotSuffix(name) {
  if (typeof name !== "string") return null;
  if (name === TOKEN_ENV) return "";
  for (let i = 1; i <= MAX_SLOTS; i++) {
    if (name === `${TOKEN_ENV}_${i}`) return String(i);
  }
  return null;
}

/**
 * Collect the configured tokens, in slot order.
 *
 * Every workflow passes all MAX_SLOTS variables so that registering a new secret
 * needs no workflow edit, which means UNREGISTERED SLOTS ARRIVE AS "" and empties
 * are the normal case, not an error. They are dropped rather than kept as holes:
 * a "" member would authenticate as nothing and read as a real failover target.
 *
 * De-duplicated because the migration state — keep the unsuffixed secret, also
 * copy it into `_1` — otherwise makes the pool believe it has two accounts and
 * "fail over" from a dead token to the same dead token.
 */
export function readPoolSlots(env = process.env) {
  const names = poolEnvNames();
  const seen = new Set();
  const slots = [];
  for (const name of names) {
    const token = typeof env[name] === "string" ? env[name].trim() : "";
    if (!token || seen.has(token)) continue;
    seen.add(token);
    // `name` travels with the token so a diagnostic can say WHICH secret is bad
    // without printing the credential. Deduping and dropping empties means a
    // position in this array is not its suffix, so "slot 3" would point at the
    // wrong secret; the name is the only thing an operator can act on.
    slots.push({ name, token });
  }
  return slots;
}

/** The tokens alone, in slot order — the pool's own view. */
export function readPoolTokens(env = process.env) {
  return readPoolSlots(env).map((slot) => slot.token);
}

/**
 * Which token this job starts on.
 *
 * `runId` spreads consecutive runs over the pool with no coordination.
 * `runAttempt` shifts the pick, so re-running a job that died on exhaustion does
 * not immediately re-select the token that just closed.
 *
 * Everything degrades to index 0 rather than to NaN: `NaN % n` is NaN, which
 * would index the pool to `undefined` and authenticate as nothing — a confusing
 * failure a long way from its cause. A local run with no GITHUB_RUN_ID gets the
 * first token, which is exactly right for a pool of one.
 */
export function selectStartIndex(size, { runId, runAttempt, shard } = {}) {
  if (!Number.isInteger(size) || size <= 0) return 0;
  // Run ids are ~11 digits today, but taking the tail keeps this exact if they
  // ever outgrow MAX_SAFE_INTEGER, where the parse would start rounding.
  const digits = String(runId ?? "").replace(/\D/g, "").slice(-15);
  const base = digits ? Number(digits) : 0;
  const attempt = Number(String(runAttempt ?? "").replace(/\D/g, "")) || 1;
  return (base + attempt - 1 + shardOffset(shard)) % size;
}

/**
 * A stable number for a per-job discriminator, so SIBLINGS OF ONE RUN differ.
 *
 * `GITHUB_RUN_ID` identifies the workflow RUN, not the job — every leg of a
 * matrix shares it, so run-id-only selection puts every leg of one dispatch on
 * the same credential. That silently defeats the pool for eval-replay, the lane
 * that most wants it: its `max-parallel: 1` exists precisely because concurrent
 * legs contend on one account's rate limit, and legs landing on different
 * accounts is the thing that could eventually lift it.
 *
 * A hash rather than a digit parse because leg identifiers are opaque strings,
 * not numbers. Absent (the ordinary single-job case) contributes nothing, so
 * selection is unchanged for every workflow that does not set it.
 */
export function shardOffset(shard) {
  const text = String(shard ?? "");
  if (!text) return 0;
  let hash = 0;
  for (let i = 0; i < text.length; i++) hash = (hash * 31 + text.charCodeAt(i)) % 100_000_007;
  return hash;
}

/**
 * Build the pool for this process.
 *
 * `current()` is the token every session should use; it changes ONLY when
 * `advance()` retires one. Returns `null` for an empty pool instead of throwing:
 * the SDK's own "no credentials" error names the real problem better than a
 * wrapper's would, so an unconfigured environment should reach it.
 */
export function createTokenPool({
  env = process.env,
  runId = env.GITHUB_RUN_ID,
  runAttempt = env.GITHUB_RUN_ATTEMPT,
  // Set by a workflow whose jobs share one run id — see shardOffset.
  shard = env.CLAUDE_POOL_SHARD,
} = {}) {
  // Slots, not bare tokens: the NAME has to survive so a diagnostic — and the
  // fixer's credential picker — can say WHICH secret is live without printing a
  // credential. `readPoolTokens` is kept for callers that only want the values.
  const slots = readPoolSlots(env);
  const tokens = slots.map((slot) => slot.token);
  const retired = new Set();
  let index = selectStartIndex(tokens.length, { runId, runAttempt, shard });

  const current = () => (tokens.length && !retired.has(index) ? tokens[index] : null);

  return {
    size: tokens.length,
    current,
    retiredCount: () => retired.size,

    /**
     * The env var names of the slots this process has NOT retired, in slot order.
     *
     * Names, never tokens. This exists so one job can tell ANOTHER job which
     * credentials still have an open window — the panel runs the SDK (and so owns
     * the pool), while the fixer runs `claude-code-action`, which takes a single
     * credential and cannot consult a pool. Without a channel between them the
     * fixer falls back to the ambient variable, which is slot zero: the credential
     * `isExhausted` documents as "one the pool has already retired".
     *
     * `retiredSlotNames` is its complement and is reported for the operator: a
     * page that says which accounts closed is actionable, one that says "a lens
     * failed" is not.
     */
    liveSlotNames: () => slots.filter((_, i) => !retired.has(i)).map((slot) => slot.name),
    retiredSlotNames: () => slots.filter((_, i) => retired.has(i)).map((slot) => slot.name),
    /** Every configured slot name, in order — the denominator for a capacity report. */
    slotNames: () => slots.map((slot) => slot.name),

    /**
     * The env var NAME of the slot `current()` would hand out, or null.
     *
     * Exactly `current()`, returning the name instead of the credential. It exists so
     * a workflow can be TOLD which slot to use without the token ever entering a step
     * output: the caller emits the name's suffix and the workflow resolves it through
     * the `secrets` context, so the value stays inside GitHub's masking.
     *
     * This is what lets the `claude-code-action` steps share the pool at all. They
     * take one credential and cannot import this module, so before this they were
     * hardcoded to `secrets.CLAUDE_CODE_OAUTH_TOKEN` — slot ZERO — which pinned every
     * one of them to a single account. Issue #883's implement runs died on it twice
     * (`is_error:true`, 432ms, $0) while the pool-driven fixer was spending 5.7M
     * tokens through a live slot in the same hour.
     */
    currentSlotName: () => (tokens.length && !retired.has(index) ? slots[index].name : null),

    /**
     * Has a CONFIGURED pool been used up?
     *
     * `current()` returns null for two states that must not be confused. An
     * unconfigured pool (`size === 0`) falls back to ambient credential
     * resolution, which is correct and is how a repo with no pool keeps working.
     * A DRAINED pool returning null means every account's window is closed — and
     * falling back there would re-use the ambient token, which in these
     * workflows is slot zero: a credential the pool has already retired. The
     * call then either burns a live round-trip to fail identically, or, if
     * nothing is set at the OS level, reports "not logged in" — sending whoever
     * reads the log after a credential problem they do not have.
     */
    isExhausted: () => tokens.length > 0 && current() === null,

    /**
     * Retire the current token and hand back the next live one, or `null` when
     * the pool is dry — the point at which failing IS the right answer.
     *
     * `deadToken` makes a concurrent report idempotent. The panel runs lenses and
     * samples at once, so several of them hit the same closed window within
     * milliseconds of each other; without this, the second report would retire
     * the healthy token the first one just moved to, and a burst of concurrency
     * would drain the pool in one round.
     */
    advance(reason, deadToken) {
      if (!tokens.length) return null;
      if (deadToken !== undefined && deadToken !== tokens[index]) return current();

      retired.add(index);
      for (let step = 1; step <= tokens.length; step++) {
        const candidate = (index + step) % tokens.length;
        if (retired.has(candidate)) continue;
        index = candidate;
        return tokens[index];
      }
      return null;
    },
  };
}
