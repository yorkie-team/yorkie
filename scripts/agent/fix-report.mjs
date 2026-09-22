// The fixer's own account of what it did: which findings it FIXED, which it
// SKIPPED, and why. Written by the fix agent, read by the next panel round.
//
// WHY IT EXISTS. Until now a fix round left no record of its reasoning. The panel
// saw only the diff, so a finding the fixer addressed in a way the verifier could
// not spot came back, and a finding the fixer knowingly left alone came back
// looking identical to one it had simply missed — the maintainer could not tell
// "the agent tried and failed" from "the agent never noticed". Both are one round
// of cost, and one of them needs a human immediately.
//
// THE SAME TRUST RULE AS rebuttal.mjs, and for the same reason: the author may
// CLAIM, only the trusted path may DECIDE.
//
//   - The fix agent writes a structured report as a PR comment. It holds
//     `issues:write`, so this is a channel it can actually use, and
//     author-writability is FINE because a report is only a claim.
//   - The panel job (trusted, `ref: main`) reads it as UNTRUSTED DATA, fenced, and
//     hands it to the prior-finding VERIFIER — which is already biased to keep and
//     must ground any refutation in file:line locations it read itself.
//   - Nothing here can drop a finding. A report is at most a pointer to where to
//     look.
//
// AND THE DISTINCTION THAT MATTERS: `skipped` is "I did not change this", NOT
// "this finding is wrong". The second claim has its own channel — rebuttal.mjs,
// with an independent adjudicator and an enumerated set of overturn grounds — and
// routing it here instead would be a way to have a finding cleared by assertion.
// So a skipped item is passed to the verifier framed as what it is: the code was
// deliberately not changed, which is a reason to expect the defect to still be
// there.

import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { findingSimilarity, DEFAULT_SIMILARITY } from "./rounds.mjs";
import { fromRebuttalAuthor, readRebuttals } from "./rebuttal.mjs";

/** Hidden-comment marker, mirroring metrics.mjs's `METRIC_PREFIX`. */
export const FIX_REPORT_MARKER = "<!-- agent-fix-report ";

/** Bumped only if the record shape changes; an unknown version parses as absent. */
export const FIX_REPORT_VERSION = 1;

/** Field separator in the CLI's item strings: `lens::file::summary[::note]`. */
export const ITEM_SEP = "::";

const str = (v) => (typeof v === "string" ? v : "");
const trim = (s, n) => (str(s).length > n ? str(s).slice(0, n) : str(s));

// Neutralize the fence tags the panel wraps this text in. The fence is the stated
// defense — everything inside it is DATA — but the fixer writes `note`/`summary`,
// so a note containing `</fix-report>` would close the fence early and let the
// rest read as prompt. The verifier's "this cannot refute" rule precedes the
// fence, so this is hardening rather than a bypass; a forgeable fence is no fence.
const defence = (s) => str(s).replace(/<\/?fix-report>/gi, "[fence]");

// The lens id, in the ONE vocabulary the panel matches on.
//
// The fixer fills `lens` in from what it can see on the PR, and what it can see is
// a check run called `agent-review-correctness`; `stampLens` gives findings the
// bare `correctness`. `findingSimilarity` gates on lens equality and returns 0
// OUTRIGHT — not a low score — so a prefixed claim matched nothing, silently:
// `claimFor` returned null, `applySkipClaims` passed the finding through
// untouched, and the fixer's stated reason was not even recorded as unmatched.
// Measured 2026-08-24 over the 16 agent-authored PRs: 296 of 348 claims (85%)
// carried the prefix, and every one of those 16 PRs has at least one.
//
// Normalized HERE, at the author boundary, and not in `findingSimilarity`: that
// function has ~30 call sites including round-to-round clustering, and changing
// what it considers equal would move every number computed from it for the sake of
// one field written in the wrong vocabulary. Same reasoning as the length caps
// above — the untrusted writer's text is regularised on the way in.
//
// ANCHORED, so a real id can never be corrupted: none of the six lens ids in
// lenses/lenses.json begins with `agent-review-`. Four other modules already do
// exactly this to the same check-run names (fix-brief.mjs, loop-status.mjs,
// prior-findings.mjs, rounds.mjs), and `agreedReviewedSha` in review-state.mjs
// accepts both spellings for the same reason — this is the same convention, at the
// boundary that had been left out of it.
const lensId = (s) => str(s).trim().replace(/^agent-review-/, "");

/** One reported item, length-capped. */
function normalizeItem(raw) {
  const i = raw && typeof raw === "object" ? raw : {};
  return {
    lens: trim(lensId(i.lens), 60),
    file: trim(str(i.file).trim(), 300),
    summary: trim(i.summary, 2000),
    note: trim(i.note, 2000),
  };
}

/**
 * Serialize a report into a hidden payload.
 *
 * Capped HERE rather than on read. The writer is the untrusted party, so a cap
 * applied only on read would still let it post a megabyte comment that every later
 * reader has to download and scan. 40 items per list matches fix-brief.mjs's
 * MAX_ITEMS — a report longer than the work list it answers is not a report.
 */
export function serializeFixReport(rec) {
  const r = rec && typeof rec === "object" ? rec : {};
  const list = (v) => (Array.isArray(v) ? v : []).slice(0, 40).map(normalizeItem);
  const payload = {
    v: FIX_REPORT_VERSION,
    head: trim(str(r.head).trim(), 64),
    fixed: list(r.fixed),
    skipped: list(r.skipped),
  };
  // ESCAPE THE TERMINATOR. metrics.mjs can state that its records "never contain
  // the ` -->` terminator" because it serialises machine-generated fields; every
  // field here is MODEL TEXT copied verbatim from a finding, and this repo's
  // findings quote its own HTML markers constantly (`<!-- agent-metric … -->`,
  // `<!-- agent-review-paged -->`). `JSON.stringify` does not escape `-->`, and
  // `parseFixReportComment`'s non-greedy match stops at the first one — so ONE
  // such item truncated the payload, failed the round-trip guard, and made
  // `cmdPost` post nothing at all. Every other item in the report went with it.
  //
  // `\u002d` is the JSON escape for `-`, so the raw comment no longer contains
  // `-->` while `JSON.parse` still yields the original characters exactly. The
  // value round-trips byte-for-byte; only the transport is neutralised.
  return `${FIX_REPORT_MARKER}${JSON.stringify(payload).replace(/-->/g, "-\\u002d>")} -->`;
}

/**
 * Read one comment body as a fix report, or `null`.
 *
 * `null` for ANY doubt — no marker, non-JSON, wrong version. Unlike a rebuttal,
 * a report cannot clear anything, so the cost of a mis-parse is only a lost
 * pointer; it is still refused, because a half-understood report shown to a
 * verifier as if complete is worse than none.
 *
 * The regex is non-greedy and the payload is parsed as JSON, so a comment that
 * merely CONTAINS the marker text inside prose cannot smuggle a record: the first
 * match wins and anything that is not valid JSON yields null.
 */
export function parseFixReportComment(body) {
  const m = new RegExp(`${FIX_REPORT_MARKER}([\\s\\S]*?) -->`).exec(str(body));
  if (!m) return null;
  let d;
  try {
    d = JSON.parse(m[1]);
  } catch {
    return null;
  }
  if (!d || typeof d !== "object" || Array.isArray(d)) return null;
  if (d.v !== FIX_REPORT_VERSION) return null;
  const list = (v) => (Array.isArray(v) ? v : []).map(normalizeItem);
  return { v: d.v, head: str(d.head), fixed: list(d.fixed), skipped: list(d.skipped) };
}

/**
 * Every fix report on a PR, in comment order. Junk — and anyone else's — is skipped.
 *
 * SAME AUTHOR GATE AS A REBUTTAL, and it matters more here. `readFixReports` pages
 * every comment on the PR, so on a public repo an unauthenticated marker comment
 * would reach the adjudicator — and one report carries up to 80 items, where one
 * rebuttal carries a single claim. That is an 80x amplification of the same
 * channel. `fromRebuttalAuthor` is reused rather than re-derived: both channels
 * have exactly one legitimate writer, the fix agent, and two copies of "who may
 * write" is how one of them ends up wrong.
 */
export function collectFixReports(comments) {
  const out = [];
  for (const c of Array.isArray(comments) ? comments : []) {
    if (!fromRebuttalAuthor(c)) continue;
    const r = parseFixReportComment(c?.body);
    if (r) out.push({ ...r, commentId: c?.id ?? null, createdAt: str(c?.created_at) });
  }
  return out;
}

/**
 * Flatten every report's items into one list, each tagged with its status.
 *
 * Later reports come later, which is what makes "the most recent claim wins" work
 * in `claimFor` below without a timestamp comparison.
 */
export function flattenClaims(reports) {
  const out = [];
  for (const r of Array.isArray(reports) ? reports : []) {
    for (const status of ["fixed", "skipped"]) {
      for (const i of Array.isArray(r?.[status]) ? r[status] : []) {
        // lens+file are what `findingSimilarity` gates on. Without both, a claim
        // can never match anything, so keeping it would only add a row that looks
        // like it was considered. Tested on the NORMALIZED item, because a lens of
        // exactly `agent-review-` reduces to empty and is just as unmatchable.
        const item = normalizeItem(i);
        if (item.lens === "" || item.file === "") continue;
        out.push({ ...item, status, head: str(r.head), createdAt: str(r.createdAt) });
      }
    }
  }
  return out;
}

/**
 * The one claim the fixer made about this finding, or `null`.
 *
 * AMBIGUITY IS REFUSED, exactly as in `matchRebuttal`. `findingSimilarity` already
 * gates on same-lens + same-file, so a tie means the fixer wrote text that
 * describes two of this file's findings equally well — and the honest reading is
 * that it names neither. The LAST claim above threshold wins when scores differ,
 * so a second `@claude fix` round supersedes the first rather than being shadowed
 * by it.
 */
export function claimFor(finding, claims, { threshold = DEFAULT_SIMILARITY } = {}) {
  let best = null;
  let bestScore = 0;
  let tied = false;
  for (const c of Array.isArray(claims) ? claims : []) {
    const score = findingSimilarity(
      { lens: finding?.lens, file: finding?.file, summary: finding?.summary },
      { lens: c.lens, file: c.file, summary: c.summary },
    );
    if (score < threshold) continue;
    if (score > bestScore) {
      best = c;
      bestScore = score;
      tied = false;
    } else if (score === bestScore) {
      // The same item repeated across two reports is not an ambiguity — it is one
      // claim, and the later copy is the one to act on.
      tied = !(best && best.summary === c.summary && best.status === c.status && best.note === c.note);
      if (!tied) best = c;
    }
  }
  return tied ? null : best;
}

/**
 * Every `file:line` in a note, for the adjudicator to read.
 *
 * `groundedIn` locations are what `isOverturningVerdict` requires before a finding
 * can be removed, and the adjudicator supplies those itself — these are the
 * author's POINTERS, the places it says to look. Bounded and shape-checked so a
 * note full of prose does not arrive as ten lines of noise.
 */
export function locationsIn(note) {
  const out = [];
  const re = /\b[\w./-]+\.[A-Za-z][\w]*:\d+\b/g;
  let m;
  while ((m = re.exec(str(note))) !== null && out.length < 10) out.push(m[0]);
  return out;
}

/**
 * How many fix-report claims may buy an adjudicator session in one round.
 *
 * A report covers the WHOLE checklist by construction (the prompt requires one
 * item per finding), so without a cap every still-gating finding would buy a
 * 20-turn Opus session on top of the verifier sessions it already costs — inside
 * the panel job's timeout, which, if exceeded, kills the job and leaves
 * `close-stuck-checks` to mark every lens failed. Claims past the cap are treated
 * exactly like skipped ones: upheld with no session, which is the safe direction.
 */
export const MAX_FIX_ADJUDICATIONS = 5;

/** The most recent report, or null. */
export function latestReport(reports) {
  const list = Array.isArray(reports) ? reports.filter((r) => r && typeof r === "object") : [];
  return list.length ? list[list.length - 1] : null;
}

/**
 * Split the author's claims into the two things the panel does with them.
 *
 * ONLY THE LATEST REPORT COUNTS. Each run reports its whole work list against the
 * findings standing at that moment, so an older report is a statement about code
 * that has since changed — replaying round 1's "I FIXED this" to round 5's
 * adjudicator asks it to judge a claim about a tree it never saw. It also created
 * a tie: two reports naming the same finding with different notes score
 * identically, `matchRebuttal` refuses the ambiguity, and the finding is never
 * adjudicated at all — which silently defeated the `upheldTwice` page this module
 * documents.
 *
 * A GENUINE REBUTTAL WINS over a report about the same finding, for the same
 * reason. Both prompts require an item per checklist entry AND a rebuttal for a
 * finding believed wrong, so a disputed finding always produced both records —
 * and the resulting tie killed the rebuttal channel for exactly the findings it
 * exists to serve. The rebuttal is the argued claim with enumerated grounds; the
 * report is bookkeeping. Keep the rebuttal.
 *
 * Returns `{ adjudicate, skipped, deferred }`:
 *   - `adjudicate` — `fixed` claims, as rebuttal records, capped. These can win.
 *   - `skipped` — claims the author says it did not act on. These cost NO session:
 *     no enumerated ground could ever apply to "I did not do it", so buying a
 *     20-turn adjudication to reach a foregone conclusion spends money to change
 *     nothing. The caller upholds them directly, which still advances the counter
 *     that pages a human on the second skip.
 *   - `deferred` — `fixed` claims past the cap, handled like `skipped`.
 */
export function authorClaims(reports, rebuttals = []) {
  const report = latestReport(reports);
  if (!report) return { adjudicate: [], skipped: [], deferred: [] };
  const claims = flattenClaims([report]);
  const disputed = Array.isArray(rebuttals) ? rebuttals : [];
  const covered = (c) => disputed.some((r) =>
    findingSimilarity({ lens: c.lens, file: c.file, summary: c.summary }, r) >= DEFAULT_SIMILARITY);

  const skipped = [];
  const fixed = [];
  for (const c of claims) {
    if (covered(c)) continue; // the rebuttal speaks for this finding
    (c.status === "fixed" ? fixed : skipped).push(c);
  }
  return {
    adjudicate: toRebuttalRecords(fixed.slice(0, MAX_FIX_ADJUDICATIONS)),
    skipped,
    deferred: fixed.slice(MAX_FIX_ADJUDICATIONS),
  };
}

/**
 * Fix-report items as REBUTTAL RECORDS, so the existing adjudication pass decides
 * them. This is the whole integration, and it is deliberately not more than this.
 *
 * Takes CLAIMS, not reports — `authorClaims` above decides which ones get here,
 * and doing that selection inside this function would put the cap and the
 * rebuttal-precedence rule somewhere no caller can see them.
 *
 * WHY ADJUDICATION AND NOT VERIFICATION. The obvious place to put "the author says
 * they fixed this" is the prior-finding verifier — it is already re-checking the
 * finding against the new code. But `adjudicateFinding`'s doc comment states the
 * rule the panel is built on: the verifier path is the one path that has never
 * been handed author-written text, and the adjudicator exists precisely to be the
 * component that is. A report is author-written text. It goes to the adjudicator.
 *
 * WHAT EACH STATUS CAN ACHIEVE, and the asymmetry is the safety property:
 *   - `fixed` maps onto the real overturn ground `not-present` — the defect is
 *     genuinely gone. The adjudicator still has to read the code and cite
 *     locations, so a false "I fixed it" is upheld and the finding stands. These
 *     are the only claims that reach here.
 *   - `skipped` maps onto NOTHING. OVERTURN_GROUNDS has no entry for "I did not do
 *     it", by design (rebuttal.mjs: "undeliverable is not wrong"), so a skipped
 *     item could only ever be upheld. It is therefore never converted at all —
 *     `authorClaims` routes it to the caller's session-free uphold instead, which
 *     reaches the same answer without buying 20 turns to get there, and still
 *     advances the counter that pages a human on the second skip.
 *
 * The claim text says which status it is, first, so the adjudicator is not left to
 * infer it from a note that may argue for either. Both wordings exist because a
 * capped `fixed` claim is handled like a skip, and the caller may hand either back
 * for rendering.
 */
export function toRebuttalRecords(claims) {
  const out = [];
  for (const c of Array.isArray(claims) ? claims : []) {
    const preamble = c.status === "fixed"
      ? "The author (an automated fix agent) reports that it FIXED this finding. "
        + "Verify from the code whether the defect is genuinely gone; if it is, that is "
        + "`not-present`. A claim of having fixed something is not evidence that it was fixed."
      : "The author (an automated fix agent) reports that it SKIPPED this finding — the code "
        + "was deliberately NOT changed. Not having made a change is not a reason the finding "
        + "is wrong, so this cannot be overturned on that basis; uphold unless the code itself "
        + "shows the finding was never correct.";
    out.push({
      v: 1,
      findingKey: "",
      lens: c.lens,
      file: c.file,
      summary: c.summary,
      claim: `${preamble}\n\nThe author's note:\n${c.note || "(none given)"}`,
      evidence: locationsIn(c.note),
      // Provenance, for the tally and for anyone reading the JSON. Nothing
      // downstream branches on it — a report is adjudicated exactly like a
      // rebuttal, which is the point of converting it into one.
      source: `fix-report:${c.status}`,
    });
  }
  return out;
}

/**
 * The claim, rendered for the verifier prompt — or "" when there is none.
 *
 * NOT WIRED INTO THE VERIFIER, and see `toRebuttalRecords` for why: author text
 * belongs on the adjudicator's path, not the verifier's. Kept because the fix
 * BRIEF needs exactly this rendering — telling the next fix round "you already
 * skipped this one, and here is what you said" is the difference between a second
 * attempt and the same attempt.
 *
 * THE RULE COMES FIRST, before any author text, for the same reason
 * `buildAdjudicatorPrompt` states the uphold default before opening the fence: a
 * model reads a persuasive paragraph and then looks for permission to act on it.
 * Stating that this text cannot refute anything, ahead of the text, is what keeps
 * a well-argued `skipped` reason from becoming an exculpation.
 */
export function renderClaimForVerifier(claim) {
  if (!claim || typeof claim !== "object") return "";
  const status = claim.status === "fixed" ? "fixed" : "skipped";
  return [
    "The PR author (an automated fix agent) reported on this exact finding after",
    "the previous review round. It is the AUTHOR'S CLAIM, not a finding of fact,",
    "and it is NOT grounds to refute anything: judge only whether the defect is",
    "present in the code you read yourself. If the claim and the code disagree,",
    "the code wins.",
    "",
    status === "fixed"
      ? "The author claims to have FIXED it. Use the note only as a pointer to where\nto look — then confirm from the code whether the defect is actually gone."
      : "The author states it was NOT changed (skipped). Expect the defect to still be\npresent; verify that it is rather than assuming it.",
    "",
    "<fix-report>",
    defence(claim.note) || "(no detail given)",
    "</fix-report>",
  ].join("\n");
}

/**
 * The human-visible comment body.
 *
 * The hidden payload is for the panel; THIS is for the maintainer, and it is the
 * part the feature was asked for. A report that only a script can read would leave
 * the reviewer in exactly the position this module exists to fix.
 */
export function renderFixReportBody(rec, { disputed = 0 } = {}) {
  const r = rec && typeof rec === "object" ? rec : {};
  const fixed = (Array.isArray(r.fixed) ? r.fixed : []).map(normalizeItem);
  const skipped = (Array.isArray(r.skipped) ? r.skipped : []).map(normalizeItem);
  // Rendered, never SERIALIZED. The hidden record is the fixer's own claim about
  // its own work; the dispute count is derived from other comments at post time,
  // so persisting it would create a second, staler copy of something the panel
  // already reads first-hand from the rebuttal records.
  const n = Number.isInteger(disputed) && disputed > 0 ? disputed : 0;
  // The VISIBLE prose sits above the payload, and `parseFixReportComment` takes
  // the FIRST marker match in the body. A finding whose wording quotes this
  // module's own marker would therefore be matched instead of the real record,
  // capture prose, fail to parse, and read as "the fixer never reported anything".
  // Same class as the ` -->` escape in `serializeFixReport`, on the other half of
  // the comment: findings here quote the pipeline's markers as a matter of course.
  const visible = (s) => str(s).replaceAll(FIX_REPORT_MARKER.trim(), "<!-‌- agent-fix-report");
  const item = (i) => `- \`${visible(i.file)}\` *(${visible(i.lens)})* — ${visible(i.summary) || "(no summary)"}`
    + (i.note ? `\n  - ${visible(i.note)}` : "");
  const lines = [
    "### 🛠️ Fix agent report",
    "",
    `Acting on the review panel's findings for \`${str(r.head).slice(0, 8) || "the current head"}\`.`,
    "",
  ];
  lines.push(`**Fixed (${fixed.length})**`, "");
  lines.push(fixed.length ? fixed.map(item).join("\n") : "_Nothing._", "");
  lines.push(`**Skipped (${skipped.length})**`, "");
  lines.push(skipped.length ? skipped.map(item).join("\n") : "_Nothing._", "");
  // DISPUTED IS RENDERED EVEN AT ZERO, and that is the whole reason it is here.
  // A dispute is filed by `rebuttal.mjs`, in its own comment, so this report has
  // never mentioned them — which left "the fixer disagreed with nothing" and "the
  // dispute channel silently failed" looking exactly alike. They are not alike:
  // no rebuttal has ever been filed on an agent PR, and a reader had no way to
  // learn whether that is the fixer agreeing or the channel being dead. Say the
  // number. `disputed` is a COUNT, not a list — the rebuttals render themselves,
  // and restating their content here would duplicate the claim the adjudicator
  // reads, in a place nothing parses.
  lines.push(`**Disputed (${n})**`, "");
  lines.push(
    n > 0
      ? (n === 1
          ? "See the ⚖️ dispute comment on this PR — it is a claim an independent adjudicator "
          : `See the ${n} ⚖️ dispute comments on this PR — each is a claim an independent adjudicator `) +
        "decides next round, not a resolution."
      : "_Nothing._",
    "",
  );
  if (skipped.length) {
    // Said plainly, because the opposite reading is the dangerous one: a skipped
    // finding is still open, and "skipped" is not a verdict about its merits.
    lines.push(
      "Skipped findings are **not resolved** — they were not changed. The next review",
      "round re-checks every one of them. A finding the agent believes is *wrong* is",
      "disputed through a rebuttal instead, which an independent adjudicator decides.",
      "",
    );
  }
  return `${lines.join("\n")}\n${serializeFixReport(r)}`;
}

// --- reading ----------------------------------------------------------------

function gh(args) {
  return JSON.parse(execFileSync("gh", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 }));
}

/**
 * Every fix report on a PR. Degrades to `[]`.
 *
 * `issues/{pr}/comments` is a BARE-ARRAY endpoint, so plain `--paginate` is
 * correct here (see gh-checks.mjs for the one that is not) — and pagination
 * matters: an iterating PR exceeds one page of comments, and a missed page
 * silently means "the fixer never reported anything".
 */
export function readFixReports(pr, { api = gh, log = console.error } = {}) {
  try {
    return collectFixReports(api(["api", "--paginate", `repos/{owner}/{repo}/issues/${pr}/comments?per_page=100`]));
  } catch (err) {
    // Degrades to "no reports", which is exactly the pre-existing behaviour: every
    // finding is re-verified with no author context. The panel must never fail
    // because an optional side-channel was unreadable.
    log(`fix-report: could not read comments for #${pr} (${err.message}); using none.`);
    return [];
  }
}

// --- CLI --------------------------------------------------------------------

/** `--flag value` pairs, with repeatable flags collected into arrays. */
function parseArgs(argv) {
  const a = Object.create(null);
  for (let i = 3; i < argv.length; i++) {
    const v = argv[i];
    if (typeof v !== "string" || !v.startsWith("--")) continue;
    const key = v.slice(2);
    const val = argv[i + 1];
    if (val === undefined || val.startsWith("--")) continue;
    if (a[key] === undefined) a[key] = val;
    else a[key] = Array.isArray(a[key]) ? [...a[key], val] : [a[key], val];
    i++;
  }
  return a;
}

const asList = (v) => (v === undefined ? [] : Array.isArray(v) ? v : [v]);

/**
 * Parse one `lens::file::summary[::note]` item string.
 *
 * Splits on the FIRST three separators only, so a summary or note containing `::`
 * survives intact — a finding quoting `Foo::bar` is ordinary, and losing the tail
 * of it would break the similarity match that decides whether the claim is
 * attached to the right finding at all.
 */
export function parseItemString(s) {
  const parts = str(s).split(ITEM_SEP);
  const [lens = "", file = "", summary = "", ...rest] = parts;
  return normalizeItem({ lens, file, summary, note: rest.join(ITEM_SEP) });
}

const USAGE =
  "Usage:\n"
  + "  node fix-report.mjs read <pr> [--out <file>]\n"
  + "  node fix-report.mjs post <pr> [--head <sha>]\n"
  + `      [--fixed "lens${ITEM_SEP}file${ITEM_SEP}summary${ITEM_SEP}what you changed" ...]\n`
  + `      [--skipped "lens${ITEM_SEP}file${ITEM_SEP}summary${ITEM_SEP}why not" ...]`;

/**
 * Post one report, so the fixer never hand-writes the record.
 *
 * A model asked to emit exact JSON inside an HTML comment gets it wrong often
 * enough that the failure would look like "the fixer never reported anything" —
 * silent, and indistinguishable from a fixer that did nothing. `serializeFixReport`
 * is the only writer, so the format cannot drift from the parser that reads it.
 */
function cmdPost(pr, args) {
  const fixed = asList(args.fixed).map(parseItemString);
  const skipped = asList(args.skipped).map(parseItemString);
  if (fixed.length === 0 && skipped.length === 0) {
    console.error(`fix-report post: nothing to report — pass at least one --fixed or --skipped.\n${USAGE}`);
    process.exit(2);
  }
  // An item missing lens, file OR summary still SHOWS in the comment (the
  // maintainer should see what the agent said) but can never match a finding: the
  // first two are what `findingSimilarity` gates on and the third is what it
  // scores. Say so rather than let the agent assume the panel will pick it up —
  // a claim that silently matches nothing is the failure this CLI exists to
  // prevent, and it looks identical to a claim that was considered and rejected.
  const unmatched = [...fixed, ...skipped].filter((i) => i.lens === "" || i.file === "" || i.summary === "").length;
  if (unmatched > 0) {
    console.error(
      `fix-report post: ${unmatched} item(s) are missing lens, file or summary and will NOT be matched`
      + ` to a finding by the next review round. Use`
      + ` "lens${ITEM_SEP}path${ITEM_SEP}the finding's wording${ITEM_SEP}note".`,
    );
  }
  const rec = { head: str(args.head), fixed, skipped };
  // Counted at post time from the rebuttals already on the PR. Best-effort by
  // construction: `readRebuttals` degrades to `[]` on any failure, so an
  // unreadable PR renders "Disputed (0)" — the same thing it rendered before this
  // existed, and never a reason to lose the report itself.
  const body = renderFixReportBody(rec, { disputed: readRebuttals(pr).length });
  // Round-trip before posting. A record this module cannot read back is one the
  // panel will ignore, and the agent would never learn its report went nowhere —
  // the same silent failure the CLI exists to prevent.
  const back = parseFixReportComment(body);
  if (!back || back.fixed.length !== Math.min(fixed.length, 40) || back.skipped.length !== Math.min(skipped.length, 40)) {
    console.error("fix-report post: the record did not round-trip; refusing to post an unreadable report.");
    process.exit(2);
  }
  try {
    execFileSync("gh", ["pr", "comment", String(pr), "--body", body], { encoding: "utf8" });
  } catch (err) {
    // Author-side and best-effort: a report that cannot be posted leaves every
    // finding to be re-verified without context, which is the pre-existing
    // behaviour and not a failure worth reddening the fix job over.
    console.error(`fix-report post: could not comment on #${pr} (${err.message}); continuing without a report.`);
    process.exit(0);
  }
  console.error(`fix-report: posted ${fixed.length} fixed / ${skipped.length} skipped on #${pr}`);
}

function main() {
  const cmd = process.argv[2];
  const args = parseArgs(process.argv);
  const pr = args.pr ?? process.argv[3];
  if (!pr || !/^\d+$/.test(String(pr)) || (cmd !== "read" && cmd !== "post")) {
    console.error(USAGE);
    process.exit(2); // usage error is a tooling error, not a review outcome
  }
  if (cmd === "post") return cmdPost(pr, args);
  const reports = readFixReports(pr);
  const json = JSON.stringify(reports);
  if (args.out) writeFileSync(args.out, json);
  else process.stdout.write(json);
  console.error(`fix-report: ${reports.length} report(s) on #${pr}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
