// What counts as evidence that LOCATES something: a `path.ext:line` citation.
//
// Its own module because two callers need the SAME answer and a drifted second
// copy would silently disagree about what evidence is:
//   - `isDroppingVerdict` (review-panel.mjs) — a verifier may only DROP a finding
//     when it cites a location it actually read.
//   - `findingLocation` (novelty.mjs) — needs a file:line to ask git how that
//     code got there; a finding it cannot locate is `unknown`, i.e. still blocking.
//
// It cannot live in either consumer: review-panel.mjs imports novelty.mjs, so a
// definition in novelty.mjs that review-panel.mjs also imported would be fine,
// but the reverse (novelty importing review-panel) is a cycle. A leaf module has
// neither problem.

/**
 * A citation must locate something: `path.ext:line`, anywhere in the string.
 *
 * No `g` flag, so `.test`/`.exec` carry no `lastIndex` state between callers —
 * with three importers sharing one regex object, a `g` flag would make the
 * result depend on who called it last.
 */
export const CITATION = /[^\s:]+\.[A-Za-z0-9_]+:\d+/;

/**
 * Pull the `{file, line}` out of the first citation in a string, or `null`.
 *
 * Returns null — never throws, never guesses — on anything that does not locate
 * a line. That includes a bare filename with no line number, which is rejected
 * deliberately: every consumer treats "no location" as the conservative outcome
 * (keep the finding / cannot judge novelty), so a lenient parse here would buy
 * nothing and cost precision. `line` is returned as a Number and is always >= 1,
 * because `\d+` cannot match a negative and a `:0` citation locates nothing.
 */
export function parseCitation(text) {
  if (typeof text !== "string") return null;
  const m = CITATION.exec(text);
  if (!m) return null;
  return pieces(m[0]);
}

// Prose wrappers to strip off the FRONT of a matched citation. `CITATION`'s leading
// `[^\s:]+` is deliberately permissive about what a path looks like, which means it
// also swallows whatever punctuation abuts the citation — and lens evidence cites in
// prose, so that is usually a `(` or a backtick. `(auth.controller.ts:130` parsed as
// the file `(auth.controller.ts`, which no path comparison could ever match, so the
// finding silently lost its location. Trimming here rather than tightening `CITATION`
// keeps the shared "what counts as evidence" predicate untouched: its other importers
// only ask `.test()` (does this cite anything at all), and narrowing it could turn a
// grounded verdict ungrounded.
//
// A DENYLIST of wrapper characters, not an allowlist of legal path starts. The
// allowlist this replaced (`[^A-Za-z0-9._/@~-]+`) was the wrong shape: it stripped
// anything it had not been told about, so a legitimate filename opening with a
// character outside that set was silently corrupted — `+page.svelte:12` became
// `page.svelte`, which matches nothing, reproducing the exact bug this trim exists to
// fix. SvelteKit's `+page`/`+layout` convention makes that a real filename, not a
// hypothetical. Enumerating the punctuation instead means the trim can only ever
// remove something that is genuinely prose, and an unfamiliar filename passes through
// untouched. Includes `*` for markdown emphasis and the typographic quotes docs pick
// up from editors; deliberately excludes `_` and `-`, which legitimately begin
// filenames.
const PATH_START = /^[([{<"'`*‘’“”]+/;

/** `{file, line}` from one already-matched `path.ext:line` token, or null. */
function pieces(token) {
  const at = token.lastIndexOf(":");
  const file = token.slice(0, at).replace(PATH_START, "");
  const line = Number(token.slice(at + 1));
  if (!Number.isInteger(line) || line < 1) return null;
  // No emptiness/extension guard on `file`, deliberately: `CITATION` only matches a
  // token containing `.` + `[A-Za-z0-9_]+` before the colon, and `.` is inside
  // PATH_START's allowlist, so the trim can never strip past it. `file` therefore
  // always retains at least `.ext`. A guard for that state would be unreachable —
  // it survived its own mutation test, which is how it was found — and an
  // unreachable guard is worse than none: it implies a case that cannot happen and
  // no test can ever hold it honest.
  return { file, line };
}

/**
 * EVERY citation in a string, in the order they appear.
 *
 * `parseCitation` returns only the first, which is the right answer for the
 * grounding checks — "did this verdict cite anything it actually read" needs one
 * citation, not all of them. It is the wrong answer for `findingLocation`, whose
 * job is to locate a finding in a KNOWN file: a lens routinely opens its evidence
 * by citing the call site or the contract it compares against, and only then cites
 * the file the finding is filed under. Taking the first citation and discarding it
 * for naming a different file loses the location entirely.
 *
 * Measured on the 44 blocking findings banked across the open agent PRs: 24 carry
 * a citation naming their own file, and only 7 have it FIRST. So 17 findings — 39%
 * of the total — were unlocatable purely because something else was cited earlier,
 * which left both provenance gates unable to judge them.
 *
 * Never `CITATION` itself: that object is deliberately un-flagged so its three
 * importers cannot poison each other, and adding `g` would break them.
 *
 * The per-call construction is a CONVENTION, not a guard, and the comment says so
 * because the difference was actually measured. `matchAll` iterates an internal
 * clone and never advances `lastIndex`; an `exec` loop advances it but `exec` resets
 * it to 0 on the miss that ends the loop. So a module-level `g` copy is
 * indistinguishable here, and mutations installing one survive by being genuinely
 * equivalent rather than by being untested. What it forecloses is a *partial* scan
 * on a shared regex — a future `break`/early `return` inside the loop would leave
 * `lastIndex` mid-string and silently shorten every later call. Building locally
 * costs one regex against the `git blame` this feeds, so the convention stays; it
 * is just not load-bearing today, and claiming otherwise would be a false comment.
 */
export function parseCitations(text) {
  if (typeof text !== "string" || text === "") return [];
  const scan = new RegExp(CITATION.source, "g");
  const out = [];
  for (const m of text.matchAll(scan)) {
    const p = pieces(m[0]);
    if (p) out.push(p);
  }
  return out;
}
