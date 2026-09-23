// Does `main` require a human approving review that THIS App cannot bypass?
//
// EXTRACTED FROM THE WORKFLOW, and the extraction is the point. This logic lived
// as ~60 lines of `github-script` inside agent-implement.yml, where the only
// thing testing it was a regex asserting somebody had typed the right
// characters. It shipped with `also` referenced and never declared, so three of
// its four refusal paths raised a ReferenceError instead of printing the
// diagnosis — a gate that failed closed and told nobody why, through five review
// rounds, because nothing ever executed it. `actionlint` does not run
// `github-script` bodies and a YAML regex cannot.
//
// So the decision is a pure function over already-fetched API payloads, and its
// caller does nothing but fetch and print.

/** Verdict shape. `ok` false always carries a `reason` a maintainer can act on. */

/**
 * @param {object} input
 * @param {object|null} input.classic  getBranchProtection payload, or null
 * @param {{status?: number}|null} input.classicError  the error it threw, if it did
 * @param {Array|null} input.rules  /rules/branches/{branch} payload, or null if listing failed
 * @param {Array<{id: *, ruleset: object|null}>} input.rulesets  resolved rulesets; `ruleset: null` = unreadable
 * @returns {{ok: boolean, reason: string}}
 */
export function decideProtection({ classic = null, classicError = null, rules = null, rulesets = [] } = {}) {
  // A CLASSIC READ THAT FAILED FOR ANY REASON BUT 404 IS NOT AN ANSWER. 404 is
  // "no classic protection", which a ruleset may still cover. A 403 means the
  // App lacks Administration: read, and reading that as "unprotected, carry on"
  // would invert the whole gate.
  if (classicError && classicError.status && classicError.status !== 404) {
    return {
      ok: false,
      reason:
        `branch protection could not be read (HTTP ${classicError.status}). The App token needs ` +
        "Administration: read — see docs/design/agent-command-verbs.md, Phase I",
    };
  }

  let bypassable = 0;
  let staleApprovals = false;
  const r = classic && classic.required_pull_request_reviews;
  if (r) {
    const required = Number(r.required_approving_review_count || 0);
    const allow = r.bypass_pull_request_allowances || {};
    bypassable =
      (allow.users || []).length + (allow.teams || []).length + (allow.apps || []).length;
    // WITHOUT `require_last_push_approval` AN APPROVAL OUTLIVES THE COMMIT IT
    // APPROVED. The PR-side `fix` and `loop` verbs push to agent branches after
    // review, so a human approval given on commit A stays valid for every commit
    // the bot pushes afterwards — which is the guarantee this gate exists to
    // establish, quietly not held.
    staleApprovals = required >= 1 && !r.require_last_push_approval;
    if (required >= 1 && bypassable === 0 && !staleApprovals) return { ok: true, reason: "" };
  }

  if (rules === null) {
    return {
      ok: false,
      reason:
        "the branch rules for main could not be listed at all, so whether it is protected is " +
        "unknown. Unverified protection is treated as none",
    };
  }

  let unreadable = 0;
  for (const { ruleset } of rulesets) {
    if (!ruleset) {
      unreadable += 1;
      continue;
    }
    // ABSENCE IS NOT EMPTINESS. GitHub omits `bypass_actors` when the caller may
    // not see it — the normal case for a repository-scoped token reading an
    // organization-sourced ruleset — so `(x || []).length === 0` reads "no
    // bypass actors" from a response that simply told us less.
    const listVisible = Array.isArray(ruleset.bypass_actors);
    const noneListed = listVisible && ruleset.bypass_actors.length === 0;
    const cannotBypass = ruleset.current_user_can_bypass === "never";
    const active = ruleset.enforcement === "active";
    const params = ruleset.rules_params || {};
    const fresh = params.require_last_push_approval !== false;
    if (noneListed && cannotBypass && active && fresh) return { ok: true, reason: "" };
    if (!fresh) staleApprovals = true;
    if (listVisible && ruleset.bypass_actors.length > 0) bypassable += ruleset.bypass_actors.length;
  }

  // ORDERED BY WHAT A MAINTAINER CAN ACT ON, and the actionable one is appended
  // rather than ranked away — an earlier revision hid "N actors may bypass it"
  // behind whatever the ruleset path happened to report.
  const also =
    bypassable > 0 ? ` Separately: ${bypassable} actor(s) may bypass the required review on main.` : "";
  if (unreadable > 0) {
    return {
      ok: false,
      reason:
        `main may be protected by ${unreadable} ruleset(s) this token cannot read — an ` +
        "organization-level ruleset is not served by the repository rulesets endpoint. " +
        "Unverified protection is treated as none" + also,
    };
  }
  if (staleApprovals) {
    return {
      ok: false,
      reason:
        "main requires a review but does not require re-approval after the last push, so an " +
        "approval on one commit stays valid for every commit a bot pushes afterwards" + also,
    };
  }
  if (bypassable > 0) {
    return {
      ok: false,
      reason:
        `main requires a review but ${bypassable} actor(s) may bypass it, so it does not ` +
        "guarantee a human approved this",
    };
  }
  return {
    ok: false,
    reason:
      "main is not protected by a required human review that this App cannot bypass " +
      "(neither classic protection nor a ruleset provides one)",
  };
}
