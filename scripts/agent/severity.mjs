// Shared severity rule for the agent reviewers — ONE source of truth for
// "what blocks a PR". Used by both the standalone verdict reader
// (read-review-verdict.mjs) and the panel orchestrator (review-panel.mjs), so
// the gate can never drift between them.
//
// Scale: critical | major | minor | nit
//   - critical / major → BLOCKING (changes requested)
//   - minor / nit       → non-blocking (informational)
// A verdict is APPROVED iff it has zero critical/major findings.
// Any unrecognized severity is treated as `major` (fail-safe).

import { findingLocation } from "./novelty.mjs";

export const KNOWN = ["critical", "major", "minor", "nit"];
export const BLOCKING = new Set(["critical", "major"]);

/** Normalize an arbitrary severity string; unknown → "major" (fail-safe). */
export function normalizeSeverity(raw) {
  const s = String(raw ?? "").toLowerCase().trim();
  return KNOWN.includes(s) ? s : "major";
}

/**
 * Normalize a raw findings array: `severity` coerced to the known scale, every
 * other field PRESERVED.
 *
 * It used to rebuild each finding as exactly `{severity,file,summary,evidence}`,
 * which silently dropped everything the orchestrator annotates onto a finding
 * after the lens produced it. That was a real bug rather than a tidy contract:
 * `renderSummaryMd` renders from `classify()`'s output, so the
 * "verifier could not settle this" marker added for unresolved verifications
 * never appeared in a single check body — the field was gone by the time the
 * renderer looked for it. Anything that reads a finding downstream of `classify`
 * needs the whole finding, so the safe shape is additive.
 */
export function normalizeFindings(rawFindings) {
  const arr = Array.isArray(rawFindings) ? rawFindings : [];
  return arr.map((f) => ({
    ...(f && typeof f === "object" ? f : {}),
    severity: normalizeSeverity(f?.severity),
    file: f?.file,
    summary: f?.summary,
    evidence: f?.evidence,
  }));
}

/**
 * Decide the check-run conclusion from findings (already normalized or not).
 * Returns { conclusion, approved, blockingCount, findings }.
 */
export function classify(rawFindings) {
  const findings = normalizeFindings(rawFindings);
  const blockingCount = findings.filter((f) => BLOCKING.has(f.severity)).length;
  const approved = blockingCount === 0;
  return { conclusion: approved ? "success" : "failure", approved, blockingCount, findings };
}

/** "1 critical, 0 major, 2 minor, 3 nit" */
export function countsStr(findings) {
  return KNOWN.map((s) => `${findings.filter((f) => f.severity === s).length} ${s}`).join(", ");
}

/**
 * Neutralize `<!--` in a string that is about to be rendered into a body a
 * trusted identity will post. Same ZWNJ-split technique as
 * `scripts/agent/fix-report.mjs` and `scripts/agent/loop-status.mjs`, for the
 * same reason: lens summaries are copied verbatim into a bot-authored PR
 * comment (agent-review-on-demand.yml), and the paged latches trust the bot
 * identity — so author-written prose (an adjudicated dispute's reason, a skip
 * note) must not be able to smuggle a live `<!-- agent-review-paged -->` into
 * that comment. Applied to the NEW author-adjacent strings only; lens/verifier
 * model output keeps today's rendering.
 */
function neutralizeMarkers(text) {
  return String(text ?? "").replace(/<!--/g, "<!-‌-");
}

/**
 * The rendered locator for one finding: `file:line` when a line is known —
 * from the finding's own `line`, or the first same-file `file:line` citation
 * in its evidence (`findingLocation`'s rule) — else the bare file. Harvest's
 * `parsePanelComment` already strips a `:line` suffix from the locator, so the
 * richer form round-trips through the corpus reader unchanged.
 */
function locatorOf(f) {
  const loc = findingLocation(f);
  if (!loc) return "";
  return loc.line ? `${loc.file}:${loc.line}` : loc.file;
}

/**
 * The verifier's per-finding outcome marker. `unsettled` keeps its exact
 * existing wording (it predates `verification` and other renderers read it);
 * `verification` is the reporting-only field `annotateFindings` stamps —
 * "confirmed-high" / "confirmed-low" / "errored". Absent on findings from
 * rounds before the field existed, which renders exactly as today.
 */
function verifierMarker(f) {
  if (f.unsettled) return " _(verifier could not settle this)_";
  if (f.verification === "confirmed-high") return " _(verifier: confirmed, high confidence)_";
  if (f.verification === "confirmed-low") return " _(verifier: confirmed, low confidence)_";
  if (f.verification === "errored") return " _(UNVERIFIED — the verifier session errored)_";
  return "";
}

/**
 * The adjudication sub-bullet for a finding that survived a dispute or a
 * fix-claim this round. The decision integer (`upheld`) has always been
 * carried; the REASON was computed and then discarded from every human
 * surface — this is where it finally lands. `skipped-by-author` is excluded:
 * those get their own section below, where the note reads as what it is.
 */
export function adjudicationNote(f) {
  const a = f.adjudication;
  if (!a || typeof a !== "object" || a.verdict === "skipped-by-author") return "";
  const reason = neutralizeMarkers(String(a.reason ?? "").trim());
  const quoted = reason ? ` — "${reason}"` : "";
  if (a.verdict === "unadjudicated-fix-claim") {
    return `\n  - fix claimed by the author, but the panel re-found this — counts as an upheld dispute${quoted}`;
  }
  // Enumerated verdicts ONLY — never a fallback label. A carried-forward
  // finding's adjudication is exactly `{ upheld: N }`: the output.text carry
  // strips the verdict and reason by design, so "no verdict" is the normal
  // shape of history, not a variant of this round's decision. The ungrounded
  // overturn is the one verdict string adjudicateRebuttals stores that is not
  // its own label ("overturned" on a KEPT finding means the gate rejected the
  // overturn); anything else — absent, "", or a future value — renders
  // nothing, which is what that finding rendered before this existed.
  const label =
    a.verdict === "upheld" ? "**upheld**"
    : a.verdict === "unresolved" ? "**upheld** (the adjudicator could not settle the dispute)"
    : a.verdict === "errored" ? "**upheld** (the adjudicator session errored)"
    : a.verdict === "overturned" ? "**upheld** (the overturn lacked grounded evidence)"
    : null;
  if (!label) return "";
  return `\n  - dispute adjudicated: ${label}${quoted}`;
}

function section(findings, severity, heading) {
  const rows = findings.filter((f) => f.severity === severity);
  if (rows.length === 0) return "";
  const body = rows
    .map((f) => {
      // An `unsettled` finding blocks exactly like any other — the marker is for
      // the human deciding how much to trust it. It means the verifier searched
      // and could not disprove the claim, which is NOT the same as having
      // confirmed it, and printing them identically would read as endorsement.
      const marker = verifierMarker(f);
      // Other wordings of this same defect, folded in by the clustering pass.
      // Printed rather than dropped: merging is a judgement, and a reader — or
      // the fix agent — must be able to see what was merged and disagree. A
      // collapse nobody can inspect is a silent deletion with extra steps.
      const merged = Array.isArray(f.mergedFrom) && f.mergedFrom.length
        ? `\n  <details><summary>also reported as (${f.mergedFrom.length})</summary>\n\n` +
          f.mergedFrom.map((m) => `  - (${m.severity}) ${m.summary ?? "(no summary)"}`).join("\n") +
          "\n  </details>"
        : "";
      const locator = locatorOf(f);
      return `- ${locator ? `\`${locator}\` — ` : ""}${f.summary ?? "(no summary)"}${marker}${adjudicationNote(f)}${merged}`;
    })
    .join("\n");
  return `\n### ${heading} (${rows.length})\n${body}\n`;
}

/**
 * Findings the AUTHOR reported it skipped, with its stated reason — the
 * documented not-yet-built surface from the harness doc's fix-report section.
 * They still gate (each is also listed in its severity section above); this
 * section exists so the skip and its note are readable as a skip instead of
 * looking like a finding the fixer silently ignored. Each skip counts as an
 * upheld dispute toward the standstill bound.
 */
function authorSkipsSection(findings) {
  const rows = findings.filter((f) => f.adjudication?.verdict === "skipped-by-author");
  if (rows.length === 0) return "";
  const body = rows
    .map((f) => {
      const locator = locatorOf(f);
      const note = neutralizeMarkers(String(f.adjudication.reason ?? "").trim());
      return `- ${locator ? `\`${locator}\` — ` : ""}${f.summary ?? "(no summary)"}` +
        (note ? `\n  - author note: "${note}"` : "");
    })
    .join("\n");
  return (
    `\n### Author-reported skips (${rows.length} — still blocking)\n` +
    "_The author explicitly skipped these rather than fixing or disputing them. " +
    "They still gate, and each skip counts as an upheld dispute._\n" +
    `${body}\n`
  );
}

/**
 * Findings the gate looked at and DEMOTED — real, but not caused by this change.
 *
 * Rendered apart from the severity sections and after them, because they are a
 * different kind of statement: the severity sections say "fix this before
 * merging", this one says "this is worth fixing, but not here, and here is the
 * proof it predates you". Each row carries that proof — the base location the
 * code already lives at, or failing that the commit that wrote it — so a reader
 * can check the demotion by eye instead of trusting it. A demotion nobody can
 * audit is just a silent drop with extra steps.
 *
 * This module is deliberately not lane-aware: the caller decides what was
 * demoted and passes it in, so the routing vocabulary stays in one place.
 */
/**
 * Why was this finding demoted? The two gates make OPPOSITE claims about the same
 * code — novelty says "this predates the PR", the surface gate says "a fix round
 * wrote this after the review surface froze" — so they cannot share a heading:
 * printing an out-of-scope finding under "the code already existed" would be a
 * false audit line, and the whole point of these sections is that a reader can
 * check the demotion.
 *
 * Novelty is tested FIRST because `routeFinding` tests it first, so this reports
 * the gate that actually demoted the finding rather than a gate that merely would
 * have. (In practice the two are mutually exclusive: content predating the base
 * also predates the freeze, which makes it in-scope.)
 */
function demotedBy(f) {
  // The surface gate claims ONLY what it actually demoted, and novelty is tested
  // first because `routeFinding` tests it first — so a finding both gates would
  // demote is reported under the one that really did it.
  //
  // Everything else defaults to `relocated`, which is deliberate rather than
  // sloppy. Before the surface gate existed, novelty was the only demotion reason
  // and every entry in `demoted` was relocated by construction; callers (and tests)
  // rely on that and do not all stamp `origin`. Defaulting the other way would
  // silently re-file existing demotions under a new heading, and defaulting to a
  // third "reason unknown" bucket would invent a category that production cannot
  // produce — `routeFinding` demotes for exactly these two reasons.
  if (f?.novelty?.origin === "relocated") return "relocated";
  return f?.surface?.scope === "out-of-scope" ? "out-of-scope" : "relocated";
}

function demotedSection(demoted) {
  const rows = Array.isArray(demoted) ? demoted.filter((f) => f && typeof f === "object") : [];
  if (rows.length === 0) return "";
  const by = (kind) => rows.filter((f) => demotedBy(f) === kind);
  // Every row lands in exactly one section, so nothing can be demoted into silence.
  return relocatedSection(by("relocated")) + outOfScopeSection(by("out-of-scope"));
}

/**
 * Findings on code a FIX ROUND wrote after the review surface froze. See
 * `review-surface.mjs` for why these stop gating: the loop cannot converge while
 * every line written to satisfy a finding becomes new reviewable surface that
 * mints the next one.
 *
 * These are NOT dismissed. They are real findings against real code, and the
 * heading says so — they are simply not this PR's gate to pass, because the PR
 * was scoped to the original implement diff.
 */
function outOfScopeSection(rows) {
  if (rows.length === 0) return "";
  const body = rows
    .map((f) => {
      const where = f.file ? `\`${f.file}\` — ` : "";
      const s = f.surface ?? {};
      // Only claim what a probe established, same rule as the relocated section.
      const proof = s.addedBy
        ? `\n  - added by \`${String(s.addedBy).slice(0, 9)}\`, after the review surface froze`
        : "";
      return `- ${where}${f.summary ?? "(no summary)"}${proof}`;
    })
    .join("\n");
  return (
    `\n### Out of scope — added by a later fix round (${rows.length}, not blocking)\n` +
    "_Confirmed real, and worth fixing. A fix round wrote this code after the " +
    "review surface froze at the original implement diff, so it does not gate this " +
    "PR — otherwise every fix would enlarge what the next round must pass._\n" +
    `${body}\n`
  );
}

function relocatedSection(rows) {
  if (rows.length === 0) return "";
  const body = rows
    .map((f) => {
      const where = f.file ? `\`${f.file}\` — ` : "";
      const n = f.novelty ?? {};
      // Only claim what a probe established. `alsoAt` is a location git matched;
      // `contentSha` is the commit move-aware blame named AND that was checked
      // against the base. Anything else prints no proof line rather than an
      // unbacked assertion — an audit line that can be false is worse than none,
      // because its whole job is to let a reader check the demotion.
      const proof = n.alsoAt
        ? `\n  - this line already exists at \`${n.alsoAt}\``
        : n.contentSha
          ? `\n  - content dates to \`${String(n.contentSha).slice(0, 9)}\`, which predates the base`
          : "";
      return `- ${where}${f.summary ?? "(no summary)"}${proof}`;
    })
    .join("\n");
  return (
    `\n### Relocated code — not written by this change (${rows.length}, not blocking)\n` +
    "_Confirmed real. This change moved these lines here; the code itself already " +
    "existed, so the defect is not this PR's to fix and does not gate it._\n" +
    `${body}\n`
  );
}

/**
 * Render the Markdown check-run body for a set of findings.
 * `advisory: true` marks a NON-GATING lens: its check always reports success, so
 * the body must not claim "changes requested" even when it raised a blocking
 * finding — otherwise a green check would open with a ❌ that contradicts it.
 *
 * `demoted` is the same idea one level down: those findings are excluded from
 * `rawFindings` by the caller, so the header counts what actually gates. Passing
 * them inside `rawFindings` instead would print "❌ 3 blocking" above a green
 * check — the exact contradiction `advisory` exists to prevent.
 */
export function renderSummaryMd(label, rawFindings, summaryText, { advisory = false, demoted = [], unverified = null } = {}) {
  // Render from the NORMALIZED findings so an unknown severity (→ major) is
  // counted and shown as a blocking finding, not omitted or counted as zero.
  const { approved, blockingCount, findings } = classify(rawFindings);
  const header = advisory
    ? `ℹ️ ${label}: **advisory — not gating** — ${blockingCount} critical/major, informational only (${countsStr(findings)}).`
    : approved
      ? `✅ ${label}: **approved** — no critical or major findings (${countsStr(findings)}).`
      : `❌ ${label}: **changes requested** — ${blockingCount} blocking (critical/major) finding(s) (${countsStr(findings)}).`;
  // Stated immediately under the header, before any finding, because it changes
  // how every finding below should be read. An outage makes the panel MORE
  // blocking (the error path keeps findings), so this is not a safety warning —
  // it is a trust one: without it, a wall of unfiltered findings is
  // indistinguishable from a wall of verified defects.
  const unverifiedNote = unverified && unverified.errored > 0
    ? `\n> ⚠️ **The verifier did not run on ${unverified.errored} of ${unverified.sent} blocking finding(s)**` +
      " — those sessions errored (commonly an API/session-limit 429), so those findings are" +
      " UNFILTERED rather than confirmed. Findings are kept when verification fails, which is" +
      " why this reads as a full review. Treat the unverified ones as unreviewed claims.\n"
    : "";
  return (
    `${header}\n${unverifiedNote}\n${summaryText ?? ""}` +
    section(findings, "critical", "Critical") +
    section(findings, "major", "Major") +
    section(findings, "minor", "Minor (non-blocking)") +
    section(findings, "nit", "Nit (non-blocking)") +
    authorSkipsSection(findings) +
    demotedSection(demoted)
  );
}
