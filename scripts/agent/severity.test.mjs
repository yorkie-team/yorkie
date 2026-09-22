import { test } from "node:test";
import assert from "node:assert/strict";
import { normalizeSeverity, classify, renderSummaryMd, normalizeFindings } from "./severity.mjs";

test("normalizeSeverity: known values pass through, unknown → major (fail-safe)", () => {
  for (const s of ["critical", "major", "minor", "nit"]) assert.equal(normalizeSeverity(s), s);
  assert.equal(normalizeSeverity("MAJOR"), "major");
  assert.equal(normalizeSeverity("bogus"), "major");
  assert.equal(normalizeSeverity(undefined), "major");
});

test("classify: blocks iff a critical/major survives", () => {
  assert.equal(classify([]).conclusion, "success");
  assert.equal(classify([{ severity: "minor" }, { severity: "nit" }]).conclusion, "success");
  assert.equal(classify([{ severity: "major" }]).conclusion, "failure");
  assert.equal(classify([{ severity: "critical" }]).conclusion, "failure");
  // boundary: exactly zero blockers = approved
  const r = classify([{ severity: "minor" }]);
  assert.equal(r.blockingCount, 0);
  assert.equal(r.approved, true);
  // unknown severity is treated as major → blocks
  assert.equal(classify([{ severity: "weird" }]).conclusion, "failure");
});

test("renderSummaryMd: unknown severity is normalized to major and shown (not omitted)", () => {
  const md = renderSummaryMd("Test", [{ severity: "weird", file: "a.ts", summary: "sneaky bug" }], "");
  assert.match(md, /changes requested/); // blocks
  assert.match(md, /1 major/); // counted as major, not zero
  assert.match(md, /### Major \(1\)/); // rendered under Major, not dropped
  assert.match(md, /sneaky bug/); // the finding text appears
});

test("renderSummaryMd: advisory lens with a critical finding does NOT say 'changes requested'", () => {
  const findings = [{ severity: "critical", file: "a.ts", summary: "big issue" }];
  // Non-advisory: a critical finding blocks.
  const gating = renderSummaryMd("Design fit review", findings, "");
  assert.match(gating, /changes requested/);
  // Advisory: check reports success, so the body must not contradict it with ❌.
  const advisory = renderSummaryMd("Design fit review", findings, "", { advisory: true });
  assert.doesNotMatch(advisory, /changes requested/);
  assert.match(advisory, /advisory — not gating/);
  assert.match(advisory, /### Critical \(1\)/); // still lists the finding
  assert.match(advisory, /big issue/);
});

// --- demoted (novelty-gate) rendering ----------------------------------------

test("renderSummaryMd: demoted findings are reported but do not affect the header", () => {
  // The header must count what GATES. Counting demoted findings would print
  // "❌ 1 blocking" above a check the panel concluded green — the same
  // contradiction the `advisory` flag exists to prevent, one level down.
  const md = renderSummaryMd("Correctness review", [{ severity: "minor", file: "a.mjs", summary: "small" }], "", {
    demoted: [{ severity: "critical", file: "moved.mjs", summary: "old bug in moved code" }],
  });
  assert.match(md, /approved/);
  assert.doesNotMatch(md, /changes requested/);
  assert.match(md, /Relocated code — not written by this change \(1, not blocking\)/);
  assert.match(md, /old bug in moved code/); // still reported, not vanished
});

test("renderSummaryMd: a demotion prints the evidence that makes it auditable", () => {
  const md = renderSummaryMd("R", [], "", {
    demoted: [
      { summary: "a", novelty: { alsoAt: "abc123:old.mjs:42" } },
      { summary: "b", novelty: { contentSha: "fd374623f5aa", alsoAt: null } },
    ],
  });
  assert.match(md, /this line already exists at `abc123:old\.mjs:42`/);
  assert.match(md, /content dates to `fd374623f`/);
});

test("renderSummaryMd: out-of-scope findings get their own section and proof line", () => {
  const md = renderSummaryMd("R", [], "", {
    demoted: [
      {
        severity: "major",
        file: "added-by-fixer.mjs",
        summary: "unvalidated payload",
        surface: { scope: "out-of-scope", addedBy: "fd374623f5aa9911", contentSha: "fd374623f5aa9911" },
      },
    ],
  });
  assert.match(md, /Out of scope — added by a later fix round \(1, not blocking\)/);
  assert.match(md, /unvalidated payload/);
  assert.match(md, /added by `fd374623f`, after the review surface froze/);
  // It must NOT claim the code predates anything — that is the relocated gate's
  // claim and the opposite of this one.
  assert.doesNotMatch(md, /Relocated code/);
  assert.doesNotMatch(md, /already existed/);
});

test("renderSummaryMd: the two demotion reasons render under separate headings", () => {
  const md = renderSummaryMd("R", [], "", {
    demoted: [
      { severity: "major", summary: "old bug", novelty: { origin: "relocated", alsoAt: "abc123:old.mjs:42" } },
      { severity: "major", summary: "fixer bug", surface: { scope: "out-of-scope", addedBy: "f".repeat(40) } },
    ],
  });
  assert.match(md, /Relocated code — not written by this change \(1, not blocking\)/);
  assert.match(md, /Out of scope — added by a later fix round \(1, not blocking\)/);
  assert.match(md, /old bug/);
  assert.match(md, /fixer bug/);
});

test("renderSummaryMd: novelty is reported over scope when both gates would demote", () => {
  // `routeFinding` tests novelty first, so the section must name the gate that
  // actually demoted the finding rather than one that merely would have.
  const md = renderSummaryMd("R", [], "", {
    demoted: [
      {
        severity: "major",
        summary: "both",
        novelty: { origin: "relocated", alsoAt: "abc123:old.mjs:42" },
        surface: { scope: "out-of-scope", addedBy: "f".repeat(40) },
      },
    ],
  });
  assert.match(md, /Relocated code — not written by this change \(1, not blocking\)/);
  assert.doesNotMatch(md, /Out of scope/);
});

test("renderSummaryMd: an in-scope surface stamp does not demote or re-file anything", () => {
  // The gate stamps every finding it judged, including the ones it KEPT. A stamp
  // that read as a demotion would move gating findings out of the header count.
  const md = renderSummaryMd("R", [], "", {
    demoted: [{ severity: "major", summary: "kept-but-listed", surface: { scope: "in-scope", addedBy: "a".repeat(40) } }],
  });
  // It was handed to us as demoted, so it renders — but under the default heading,
  // never as an out-of-scope claim we cannot support.
  assert.doesNotMatch(md, /Out of scope/);
  assert.match(md, /kept-but-listed/);
});

test("renderSummaryMd: an out-of-scope demotion never vanishes from the body", () => {
  // The one property that matters most: a demoted finding that renders NOWHERE is
  // a silently lost finding.
  for (const surface of [
    { scope: "out-of-scope", addedBy: null },
    { scope: "out-of-scope" },
    { scope: "unknown" },
    {},
  ]) {
    const md = renderSummaryMd("R", [], "", { demoted: [{ severity: "major", summary: "must-appear", surface }] });
    assert.match(md, /must-appear/, JSON.stringify(surface));
  }
});

test("renderSummaryMd: a demotion with no established proof asserts nothing", () => {
  // An audit line that can be false is worse than none — its whole job is to let
  // a reader check the demotion.
  const md = renderSummaryMd("R", [], "", { demoted: [{ summary: "unproven", novelty: {} }] });
  // The bullet is rendered with NO proof clause appended after it.
  assert.match(md, /^- unproven$/m);
  assert.doesNotMatch(md, /content dates to/);
  assert.doesNotMatch(md, /already exists at/);
});

test("renderSummaryMd: no demoted findings renders no demoted section at all", () => {
  const plain = renderSummaryMd("R", [{ severity: "minor", summary: "x" }], "");
  assert.doesNotMatch(plain, /Relocated code/);
  assert.doesNotMatch(renderSummaryMd("R", [], "", { demoted: [] }), /Relocated code/);
  // Junk entries are ignored rather than rendered as empty bullets.
  assert.doesNotMatch(renderSummaryMd("R", [], "", { demoted: [null, 42] }), /Relocated code/);
});

test("normalizeFindings: preserves orchestrator-added fields, not just the four it names", () => {
  // Regression. This used to rebuild each finding as exactly
  // {severity,file,summary,evidence}, which silently dropped everything the
  // orchestrator annotates AFTER the lens produced it. renderSummaryMd renders
  // from classify()'s output, so the "verifier could not settle this" marker
  // shipped in the absence-claims change never appeared in a single check body —
  // the field was gone before the renderer looked for it.
  const [out] = normalizeFindings([
    { severity: "weird", file: "a.mjs", summary: "s", evidence: "e",
      unsettled: true, lane: "blocking", claimType: "absence",
      novelty: { origin: "introduced" }, mergedFrom: [{ severity: "major", summary: "other wording" }] },
  ]);
  assert.equal(out.severity, "major"); // still coerced
  assert.equal(out.unsettled, true);
  assert.equal(out.lane, "blocking");
  assert.equal(out.claimType, "absence");
  assert.deepEqual(out.novelty, { origin: "introduced" });
  assert.equal(out.mergedFrom.length, 1);
  // Junk entries still normalize to a blocking placeholder rather than throwing.
  assert.equal(normalizeFindings([null, 42])[0].severity, "major");
});

test("renderSummaryMd: an unsettled finding is marked, and merged wordings are shown", () => {
  // The end-to-end version of the above: both annotations must survive
  // classify() and reach the rendered body.
  const md = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "unsettled one", unsettled: true },
    { severity: "major", file: "b.mjs", summary: "kept wording",
      mergedFrom: [{ severity: "major", summary: "folded wording" }] },
  ], "");
  assert.match(md, /verifier could not settle this/);
  assert.match(md, /also reported as \(1\)/);
  assert.match(md, /folded wording/);
});

test("renderSummaryMd: a verifier outage is stated above the findings", () => {
  // On #592 a 429 session limit made every verifier session throw. Findings are
  // kept when verification fails, so the body read as a full 40-finding review
  // with nothing indicating the filter never ran. The note goes ABOVE the
  // findings because it changes how all of them should be read.
  const findings = [{ severity: "critical", file: "a.mjs", summary: "looks real" }];
  const md = renderSummaryMd("R", findings, "body text", { unverified: { errored: 40, sent: 40 } });
  assert.match(md, /verifier did not run on 40 of 40/);
  assert.match(md, /UNFILTERED rather than confirmed/);
  // Ordering: the warning precedes the summary text and the first finding.
  assert.ok(md.indexOf("verifier did not run") < md.indexOf("body text"));
  assert.ok(md.indexOf("verifier did not run") < md.indexOf("looks real"));
  // The conclusion is untouched — an outage keeps findings, so it still blocks.
  assert.match(md, /changes requested/);
});

test("renderSummaryMd: no note when verification ran, or when the count is zero", () => {
  const findings = [{ severity: "major", file: "a.mjs", summary: "s" }];
  for (const unverified of [null, undefined, { errored: 0, sent: 12 }]) {
    const md = renderSummaryMd("R", findings, "b", { unverified });
    assert.doesNotMatch(md, /verifier did not run/, JSON.stringify(unverified));
  }
  // Byte-identical to the no-option call, so the common path is unchanged.
  assert.equal(renderSummaryMd("R", findings, "b", { unverified: null }), renderSummaryMd("R", findings, "b"));
});

// --- the fixer-prompt cut contract -----------------------------------------
//
// agent-review-panel.yml's "Gather failing lens findings" step hands each lens
// body to the fixer as <panel-findings>, cut at the FIRST "\n### " so the fixer
// gets the verdict and the narration but NO findings — its work list is the
// separately-filtered checklist. That cut lives in YAML and cannot be unit
// tested, so the invariant it rests on is pinned here, in the module that
// renders the thing being cut: nothing above the first "\n### " may be a
// finding, and every finding section must begin with exactly that marker.

test("renderSummaryMd: the fixer cuts this body at the first '### ' — nothing above it may be a finding", () => {
  const md = renderSummaryMd(
    "R",
    [
      { severity: "critical", file: "a.mjs", summary: "CRIT_MARKER" },
      { severity: "major", file: "b.mjs", summary: "MAJOR_MARKER" },
      { severity: "minor", file: "c.mjs", summary: "MINOR_MARKER" },
      { severity: "nit", file: "d.mjs", summary: "NIT_MARKER" },
    ],
    "lens narration",
    { demoted: [{ summary: "DEMOTED_MARKER", novelty: {} }], unverified: { errored: 1, sent: 2 } },
  );
  const prose = md.split("\n### ")[0];
  // Kept: the verdict header, the verifier-outage warning, the lens's narration.
  assert.match(prose, /changes requested/);
  assert.match(prose, /verifier did not run on 1 of 2/);
  assert.match(prose, /lens narration/);
  // Dropped: every finding at every severity, and the demoted section. The
  // blocking ones go too — they reach the fixer through the checklist instead,
  // already filtered to critical/major and non-backlog.
  for (const marker of ["CRIT_MARKER", "MAJOR_MARKER", "MINOR_MARKER", "NIT_MARKER", "DEMOTED_MARKER"]) {
    assert.ok(!prose.includes(marker), `${marker} survived the cut`);
  }
});

test("renderSummaryMd: every '###' it emits is a section marker the cut can find", () => {
  const md = renderSummaryMd(
    "R",
    [
      { severity: "critical", summary: "c" },
      { severity: "major", summary: "m" },
      { severity: "minor", summary: "n" },
      { severity: "nit", summary: "t" },
    ],
    "prose",
    { demoted: [{ summary: "d", novelty: {} }] },
  );
  // Four severity sections + the demoted one, each introduced by "\n### ".
  assert.equal(md.split("\n### ").length, 6);
  // And NO other "###" anywhere — one not at a line start, or written as "####",
  // would sit above the split point and carry a finding across it.
  assert.equal((md.match(/###/g) || []).length, 5);
});

test("renderSummaryMd: a '### ' inside the lens's own prose cuts early but still leaks no finding", () => {
  // `summaryText` is the lens's own narration, so it can contain anything —
  // including a markdown heading. That cuts the body earlier than intended,
  // which costs context but never leaks a finding, because every finding is
  // rendered strictly after `summaryText`. Pinned so the failure direction
  // stays the safe one.
  const md = renderSummaryMd("R", [{ severity: "critical", summary: "CRIT_MARKER" }], "intro\n### Notes\nmore");
  const prose = md.split("\n### ")[0];
  assert.match(prose, /intro/);
  assert.ok(!prose.includes("CRIT_MARKER"));
});

// --- Phase 2 enrichment: locators, verifier outcomes, adjudication ----------

test("renderSummaryMd: a finding with a line renders `file:line`", () => {
  const md = renderSummaryMd("R", [{ severity: "major", file: "a.mjs", line: 42, summary: "s" }], "");
  assert.match(md, /^- `a\.mjs:42` — s$/m);
});

test("renderSummaryMd: the line falls back to a same-file evidence citation, never a foreign one", () => {
  // Same-file citation → the line is trusted and rendered.
  const same = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "s", evidence: "the guard at a.mjs:88 covers undo only" },
  ], "");
  assert.match(same, /^- `a\.mjs:88` — s$/m);
  // A citation for a DIFFERENT file must not be paired with this finding's file
  // (findingLocation's rule) — the row keeps the bare file, exactly as today.
  const foreign = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "s", evidence: "unlike other.mjs:7" },
  ], "");
  assert.match(foreign, /^- `a\.mjs` — s$/m);
});

test("renderSummaryMd: per-finding verifier outcomes are rendered, and their absence renders as today", () => {
  const md = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "confirmed one", verification: "confirmed-high" },
    { severity: "major", file: "b.mjs", summary: "shaky one", verification: "confirmed-low" },
    { severity: "major", file: "c.mjs", summary: "orphan one", verification: "errored" },
    { severity: "major", file: "d.mjs", summary: "legacy one" },
  ], "");
  assert.match(md, /confirmed one _\(verifier: confirmed, high confidence\)_/);
  assert.match(md, /shaky one _\(verifier: confirmed, low confidence\)_/);
  assert.match(md, /orphan one _\(UNVERIFIED — the verifier session errored\)_/);
  // A finding from before the field existed renders with no marker at all.
  assert.match(md, /^- `d\.mjs` — legacy one$/m);
  // `unsettled` keeps its exact existing wording and wins over `verification`.
  const unsettled = renderSummaryMd("R", [
    { severity: "major", file: "e.mjs", summary: "u", unsettled: true, verification: "confirmed-high" },
  ], "");
  assert.match(unsettled, /u _\(verifier could not settle this\)_/);
  assert.doesNotMatch(unsettled, /confirmed, high/);
});

test("renderSummaryMd: an upheld dispute renders the adjudicator's decision and reason", () => {
  const md = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", line: 214, summary: "redo misses the guard",
      adjudication: { upheld: 1, verdict: "upheld", reason: "the guard at store.ts:88 covers undo but not redo" } },
  ], "");
  assert.match(md, /^- `a\.mjs:214` — redo misses the guard$/m);
  assert.match(md, /^ {2}- dispute adjudicated: \*\*upheld\*\* — "the guard at store\.ts:88 covers undo but not redo"$/m);
});

test("renderSummaryMd: adjudication wordings cover unresolved, errored, ungrounded-overturn and fix-claims", () => {
  const md = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "a", adjudication: { upheld: 1, verdict: "unresolved", reason: "could not tell" } },
    { severity: "major", file: "b.mjs", summary: "b", adjudication: { upheld: 1, verdict: "errored", reason: "" } },
    { severity: "major", file: "c.mjs", summary: "c", adjudication: { upheld: 1, verdict: "overturned", reason: "no citation given" } },
    { severity: "major", file: "d.mjs", summary: "d", adjudication: { upheld: 1, verdict: "unadjudicated-fix-claim", reason: "moved the guard" } },
  ], "");
  assert.match(md, /\*\*upheld\*\* \(the adjudicator could not settle the dispute\) — "could not tell"/);
  // An errored session has no reason to quote — the label stands alone.
  assert.match(md, /^ {2}- dispute adjudicated: \*\*upheld\*\* \(the adjudicator session errored\)$/m);
  // A kept "overturned" verdict means the gate rejected it as ungrounded.
  assert.match(md, /\*\*upheld\*\* \(the overturn lacked grounded evidence\) — "no citation given"/);
  assert.match(md, /fix claimed by the author, but the panel re-found this — counts as an upheld dispute — "moved the guard"/);
});

test("renderSummaryMd: a verdict-less adjudication (carried-forward history) renders no decision line", () => {
  // The output.text carry strips adjudication to `{ upheld: N }` by design, so
  // EVERY carried-forward finding with an adjudication has exactly that
  // verdict-less shape. The fallback label used to declare "the overturn
  // lacked grounded evidence" for it — a decision that never happened this
  // round. No verdict → no line; unknown or empty verdict strings fail the
  // same silent direction.
  for (const adjudication of [{ upheld: 2 }, { upheld: 1, verdict: "" }, { upheld: 1, verdict: "future-value" }]) {
    const md = renderSummaryMd("R", [
      { severity: "major", file: "a.mjs", summary: "carried", adjudication },
    ], "");
    assert.doesNotMatch(md, /dispute adjudicated/, JSON.stringify(adjudication));
    assert.doesNotMatch(md, /lacked grounded evidence/, JSON.stringify(adjudication));
    assert.match(md, /^- `a\.mjs` — carried$/m); // the row itself is unchanged
  }
});

test("renderSummaryMd: author-reported skips get their own section and still gate", () => {
  const md = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "needs the Store interface change",
      adjudication: { upheld: 1, verdict: "skipped-by-author", reason: "tracked in #701" } },
  ], "");
  // Still counted and listed as a blocking finding…
  assert.match(md, /changes requested/);
  assert.match(md, /### Major \(1\)/);
  // …and reported as a skip with the author's note, in its own section.
  assert.match(md, /### Author-reported skips \(1 — still blocking\)/);
  assert.match(md, /author note: "tracked in #701"/);
  // The skip is NOT also rendered as a "dispute adjudicated" sub-bullet.
  assert.doesNotMatch(md, /dispute adjudicated/);
  // No skips → no section, so pre-#633 bodies render exactly as before.
  assert.doesNotMatch(renderSummaryMd("R", [{ severity: "major", summary: "x" }], ""), /Author-reported skips/);
});

test("renderSummaryMd: author-written prose cannot smuggle a live hidden marker into the body", () => {
  // This body is copied verbatim into a BOT-authored PR comment
  // (agent-review-on-demand.yml), and the paged latches trust the bot identity —
  // so the adjudication reason and the skip note, the two author-adjacent
  // strings on this surface, are neutralized. A live latch here would freeze
  // the loop (the #681 ledger-injection shape, one surface over).
  const md = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "s",
      adjudication: { upheld: 1, verdict: "upheld", reason: 'see <!-- agent-review-paged --> above' } },
    { severity: "major", file: "b.mjs", summary: "t",
      adjudication: { upheld: 1, verdict: "skipped-by-author", reason: 'note <!-- agent-paged --> here' } },
  ], "");
  assert.ok(!md.includes("<!-- agent-review-paged -->"), "review latch survived into the body");
  assert.ok(!md.includes("<!-- agent-paged -->"), "CI latch survived into the body");
  // The prose itself still reads through — only the comment-open is split.
  assert.match(md, /agent-review-paged/);
  assert.match(md, /agent-paged/);
});

test("renderSummaryMd: the skips section is introduced by the same '\\n### ' marker the fixer cut relies on", () => {
  // The cut contract above: nothing above the first "\n### " may be a finding.
  // A skipped finding is blocking, so it must sit strictly below the cut.
  const md = renderSummaryMd("R", [
    { severity: "major", file: "a.mjs", summary: "SKIP_MARKER",
      adjudication: { upheld: 1, verdict: "skipped-by-author", reason: "why" } },
  ], "lens narration");
  const prose = md.split("\n### ")[0];
  assert.ok(!prose.includes("SKIP_MARKER"), "a skipped finding leaked above the cut");
  assert.match(md, /\n### Author-reported skips/);
});
