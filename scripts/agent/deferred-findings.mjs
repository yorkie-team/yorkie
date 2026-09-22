// The findings the gate DEFERS, as a channel a tool can read on the pull request.
//
// WHAT WAS ALREADY THERE, so this is not sold as a rescue. Nothing is being
// deleted today: `verdict.json` keeps every finding a lens raised, demoted ones
// included, and the stage-detail capture (`buildStageDetail`, default ON) keeps the
// raw per-sample findings before `unionSamples` and before `clusterFindings`. The
// non-blocking pile is durable. What it is not is ADDRESSED TO ANYONE:
//
//   - `output.text` is filtered to `critical|major` and `lane !== 'backlog'` when
//     the panel writes it, deliberately and for three named consumers (see the
//     comment above that filter in agent-review-panel.yml). That channel must not
//     widen: the non-convergence detector needs a quantity that SHRINKS as the
//     fixer works, and a pile nobody is fixing never shrinks.
//   - `output.summary` does carry Minor, Nit and demoted sections, but it is
//     markdown for a human, and `proseOnly` cuts every finding section out of it
//     before the fixer ever sees it.
//   - the stage-detail capture is a DIAGNOSTIC. It is per-sample and PRE-cluster,
//     so one defect two samples both raised appears twice and never went through
//     `annotateFindings`; and it is committed to a separate repository by a
//     collector on its own schedule. A tool asking "what did this round defer on
//     this PR" cannot answer from it without re-implementing clustering.
//
// So this module adds a SECOND check run, advisory, with no consumer that gates —
// the same finding set `verdict.json` already holds, projected once, on the PR, in
// the round. It reads the same file the gating channel reads, so the two cannot
// disagree about what the round found.
//
// WHY A MODULE AND NOT INLINE `github-script`, the same reason fix-brief.mjs and
// prior-findings.mjs are modules: inline YAML JavaScript can hold neither a unit
// test nor a linter, and every guard here is one a test has to be able to break.
// The three that must never regress are `DEFERRED_CHECK_NAME` (outside the
// `agent-review-` namespace), `DEFERRED_CONCLUSION` (never `failure`), and the
// omission tally. A guard living in YAML is a guard nothing mutates.
//
// Usage:
//   node deferred-findings.mjs --review-dir .agent-review
//                              --lenses <lenses.json> --panel-sha <sha>
//                              --out-json <file>
// Writes the check-run payload as JSON. It does NOT call the API: the workflow's
// existing `github-script` step owns check-run I/O, and keeping the write there
// keeps this file pure and testable.

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "./gh-checks.mjs";
import { BLOCKING, normalizeSeverity } from "./severity.mjs";
import { findingLocation } from "./novelty.mjs";

/**
 * The check-run name, and it is load-bearing that it is OUTSIDE
 * `agent-review-<lens>`.
 *
 * `lensCheckNames` builds exactly that namespace from the manifest, and six modules
 * strip `^agent-review-` to recover a lens id. Those are manifest-bound and so are
 * safe either way — but `set-state.mjs` is not: it enumerates every check run on the
 * commit, filters `startsWith("agent-review-")`, and sets `lensBlocked` from whatever
 * it finds. A name inside the namespace would put this advisory record into a signal
 * that decides PR state. The rule is therefore not defensive about a future consumer;
 * it protects one that reads check runs by prefix today.
 */
export const DEFERRED_CHECK_NAME = "agent-deferred-findings";

/**
 * Always `neutral`, never `success`.
 *
 * `success` on this check would read as "checked, nothing to report", which is false
 * on exactly the runs that matter — a round with forty deferred findings is not a
 * clean round, it is a round whose findings nobody is obliged to fix. `neutral` is
 * the conclusion that means "recorded, no opinion", which is the whole contract of
 * this channel.
 */
export const DEFERRED_CONCLUSION = "neutral";

/**
 * Conclusions an advisory check may carry at all. `success` is permitted here and
 * unused above on purpose: the guard states the RULE (an advisory channel never
 * fails a PR), while `DEFERRED_CONCLUSION` states this channel's narrower choice
 * within it. Collapsing the two would make the guard untestable against anything
 * except the literal it is derived from.
 */
export const ADVISORY_CONCLUSIONS = Object.freeze(["neutral", "success"]);

/** The lens namespace this channel must stay out of. See `DEFERRED_CHECK_NAME`. */
const LENS_CHECK_PREFIX = "agent-review-";

/**
 * `output.text`'s hard ceiling, the same 60000 the gating channel bounds itself to.
 * Its OWN budget, not a share of a lens's: a check run's output fields are per check
 * run, so this cannot crowd out a lens's findings even when the deferred pile is
 * larger than the blocking one — which is the normal case.
 */
export const MAX_TEXT_CHARS = 60000;

/** `output.summary`'s ceiling, matching what the panel already applies to a lens body. */
export const MAX_SUMMARY_CHARS = 60000;

/** What `text` is, so a consumer can refuse a shape it does not know. */
export const DEFERRED_SCHEMA = "agent-deferred-findings/v1";

const str = (v) => (typeof v === "string" ? v : "");
const clip = (s, n) => {
  const t = String(s ?? "");
  return t.length > n ? `${t.slice(0, n)}…` : t;
};

/**
 * Did the gate defer this finding?
 *
 * Two populations, and the test for each is the ABSENCE or PRESENCE of a lane
 * rather than anything this module invents:
 *
 *   - a native `minor`/`nit` never reached the gate, so `annotateFindings` returned
 *     it untouched and it has NO lane. Absence of a lane is the signal.
 *   - a `critical`/`major` reached the gate and was routed off it, so it carries
 *     `lane: "backlog"`.
 *
 * `lane: "discarded"` cannot appear here and is not tested for: `keepUnrefuted` runs
 * before `writeVerdict`, so a refuted finding is not in `verdict.json` at all. That
 * is the right population to be missing — a finding the verifier refuted is not
 * deferred work, and filing it as deferred would poison the pile with claims the
 * panel already decided were wrong.
 *
 * FAIL DIRECTION: `normalizeSeverity` maps an unrecognised severity to `major`
 * (fail-safe), so a junk severity is treated as a blocker and is deferred only if it
 * was explicitly demoted. This channel therefore cannot quietly re-file a finding of
 * unknown severity as non-blocking work — it reads the same fail-safe the gate does.
 */
export function isDeferred(finding) {
  if (!finding || typeof finding !== "object" || Array.isArray(finding)) return false;
  // `discarded` is tested FIRST, before severity, and the order is the whole point.
  // Testing severity first returned `true` for a refuted NON-blocker, because the
  // non-blocking branch never looked at the lane — so the one population this
  // docblock promises can never appear had an open door for half of its members.
  // `keepUnrefuted` means production does not produce these, which is exactly why
  // the asymmetry survived review of the code that documents it.
  if (finding.lane === "discarded") return false;
  if (!BLOCKING.has(normalizeSeverity(finding.severity))) return true;
  return finding.lane === "backlog";
}

/**
 * One finding, projected for the record.
 *
 * PRIMITIVES ONLY, ABSENCE INCLUDED. `lane`, `novelty.origin` and `surface.scope`
 * are copied as they are and omitted when the panel did not stamp them, because
 * every one of those absences is itself a fact: no lane means the finding never
 * reached the gate, and no `surface` means the surface gate had no opinion.
 *
 * 🔴 There is deliberately NO derived "why was this deferred" field, though one is
 * the obvious convenience. Two reasons, and the second is the one that would have
 * produced a wrong archive:
 *
 *   1. `annotateFindings`' own argument against giving non-blockers a lane applies
 *      verbatim to a discriminator computed from severity and lane — it would be "a
 *      second, redundant way to express the same thing, and any disagreement between
 *      the two would be a bug".
 *   2. `severity.mjs::demotedBy` already answers "which gate demoted this" and is
 *      LOSSY BY DESIGN: it defaults to `relocated` for callers that never stamped
 *      `origin`, which its own comment explains and defends. Storing its answer
 *      would bake a default into an archive as though it were a measurement. A
 *      reader wanting the heading can apply that precedence; the record keeps the
 *      two facts it is derived from.
 *
 * `line` comes from `findingLocation`, not from `finding.line`, so it cannot drift
 * from the location the novelty and surface gates actually resolved. That function
 * falls back to the first same-file `file:line` citation in the evidence, which its
 * docblock measured as the difference between 7 and 24 locatable findings out of 44.
 * A record nobody can navigate to is a record nobody reads.
 */
export function deferredRecord(finding, lens) {
  const loc = findingLocation(finding);
  const line = Number.isInteger(loc?.line) && loc.line >= 1 ? loc.line : undefined;
  // No `|| finding.file` fallback: `findingLocation` returns null only when the
  // finding has no usable file AND no citation naming one, so the fallback was
  // unreachable by construction. An unreachable branch in a projection is worse than
  // no branch — it reads as a handled case.
  const file = str(loc?.file).trim() || undefined;
  // Did the PANEL stamp this finding's provenance, or did the MODEL write it?
  // `annotateFindings` returns non-blockers untouched, so `lane`, `novelty` and
  // `surface` on a `minor` are whatever the lens chose to emit — and a model that
  // writes `lane: "backlog"` on a nit would otherwise have it recorded here as
  // though the gate had demoted a major. That forges exactly the provenance §4
  // exists to make trustworthy, so these three fields are carried ONLY for the
  // severities the gate actually routes.
  const stamped = BLOCKING.has(normalizeSeverity(finding.severity));
  return {
    lens: str(lens),
    // Clipped to the same widths the gating projection uses, so one oversized
    // model string cannot spend the whole budget and push real findings out.
    ...(file ? { file: clip(file, 300) } : {}),
    ...(line ? { line } : {}),
    severity: normalizeSeverity(finding.severity),
    // Gates nothing anywhere in the panel — `classify` reads severity alone. Carried
    // because it is the only field a lens has for expressing doubt without moving
    // severity, which is exactly the discrimination a later triage pass over this
    // pile needs and cannot recover from anything else here.
    // Clipped like every other model-controlled string. It was the one copied whole,
    // so a lens emitting a megabyte `confidence` could evict every real record.
    // 20 chars fits the widest legal value ("medium") many times over.
    ...(str(finding.confidence) ? { confidence: clip(finding.confidence, 20) } : {}),
    summary: clip(finding.summary, 2000),
    evidence: clip(finding.evidence, 2000),
    // ABSENT for a native minor, `"backlog"` for a demoted blocker. See the docblock.
    ...(stamped && str(finding.lane) ? { lane: str(finding.lane) } : {}),
    ...(stamped && str(finding.novelty?.origin) ? { noveltyOrigin: str(finding.novelty.origin) } : {}),
    ...(stamped && str(finding.surface?.scope) ? { surfaceScope: str(finding.surface.scope) } : {}),
  };
}

/**
 * Every deferred finding across the panel, in manifest order then per-lens order.
 *
 * ONE record set for the whole panel, not one per lens: six more per-lens checks
 * would treble the check list on every PR for a channel nobody has to act on, and a
 * reader looking for "what did this round defer" would have to visit six places and
 * union them.
 *
 * Deterministic ordering, and it is the trim that makes that matter — the tail is
 * what gets dropped, so an unstable order would drop a different finding on a re-run
 * of the same round.
 */
export function collectDeferred(lensFindings) {
  const out = [];
  for (const entry of Array.isArray(lensFindings) ? lensFindings : []) {
    const lens = str(entry?.lens);
    if (!lens) continue;
    for (const f of Array.isArray(entry?.findings) ? entry.findings : []) {
      if (isDeferred(f)) out.push(deferredRecord(f, lens));
    }
  }
  return out;
}

/**
 * The machine-readable payload, bounded, WITH THE TALLY IN IT.
 *
 * The convention is `buildChecklist`'s, and its docblock says why it exists: "A
 * silent truncation reads as 'this is everything', which is how a fixer concludes it
 * is done." The trim this channel is modelled on does not do that — the gating
 * channel drops trailing findings until the string fits and records nothing — so
 * this one carries `total`, `emitted` and `omitted` from the first version rather
 * than gaining them after somebody is misled.
 *
 * `text` is an OBJECT and not a bare array for exactly that reason: a JSON array has
 * nowhere to say it is partial. It is also where the generation stamp lives (once
 * per run, not per record).
 *
 * The whole payload is re-serialised on every iteration rather than the string being
 * sliced, for the reason the gating trim states: slicing a serialised value yields
 * JSON the next reader cannot parse. It terminates because dropping a record shrinks
 * `records` by far more than `omitted`'s digits grow, and at `records: []` the header
 * alone is a few hundred bytes — so the floor is a truthful `omitted: total`, never
 * an overflowing lie.
 */
export function buildDeferredText(records, { panelSha = null, maxChars = MAX_TEXT_CHARS } = {}) {
  const all = Array.isArray(records) ? records : [];
  const payload = (kept) => ({
    schema: DEFERRED_SCHEMA,
    // THE GENERATION STAMP. `severity` means whatever the rubric in force said it
    // meant, so a record written after a rubric change is indistinguishable from one
    // written before it unless something on the record says which rubric produced it.
    // An archive's value grows with time, so this cannot be added in version two —
    // it would describe none of the records that already matter.
    //
    // This is the sha of the `.trusted` checkout, which is where the rubrics the
    // panel actually read come from. NOT the PR head: `.trusted` is checked out at
    // `ref: main`, so the head sha names a tree that need not contain these rubrics
    // at all. NOT `rubric_sha256` either — that is computed by the eval harness
    // (`config-build.mjs`, and it is in `SNAPSHOT_ONLY_LENS_KEYS`), is absent from
    // `lenses.json`, and is unreachable at panel write time. `null` when the sha
    // could not be resolved, because "unknown" is a fact and a fabricated stamp is
    // worse than none.
    panel_sha: /^[0-9a-f]{40}$/.test(str(panelSha)) ? str(panelSha) : null,
    total: all.length,
    emitted: kept.length,
    omitted: all.length - kept.length,
    records: kept,
  });
  let kept = all;
  let text = JSON.stringify(payload(kept));
  while (text.length > maxChars && kept.length > 0) {
    kept = kept.slice(0, -1);
    text = JSON.stringify(payload(kept));
  }
  const final = payload(kept);
  return { text, total: final.total, emitted: final.emitted, omitted: final.omitted };
}

/**
 * The human-readable body. A count per lens and per severity, and the omission tally
 * again — a reader who never opens `text` still has to be told the list is partial.
 *
 * ⚠ `records` here is EVERY deferred finding, not the trimmed prefix that reached
 * `text`. The two channels are bounded by different things and the asymmetry is
 * deliberate: `text` is capped because it carries each record whole, while these
 * tables are keyed by lens and by severity, so their size is bounded by the manifest
 * (six lenses, four severities) no matter how many findings there are. Tallying only
 * the emitted prefix would report `total` in the prose and a smaller number in the
 * tables, and would count a demoted finding that fell off the tail as non-blocking —
 * which is the population confusion this whole record exists to prevent.
 */
export function renderDeferredSummary({ total, emitted, omitted, records, panelSha, maxChars = MAX_TEXT_CHARS }) {
  const rows = Array.isArray(records) ? records : [];
  if (total === 0) {
    return [
      "## Deferred findings",
      "",
      "None this round.",
      "",
      "_Advisory. This check never gates: it records what the gate deferred so it can",
      "be triaged later, and reports nothing you are obliged to fix._",
    ].join("\n");
  }
  const tally = (key) => {
    const counts = new Map();
    for (const r of rows) {
      const k = str(r?.[key]) || "(none)";
      counts.set(k, (counts.get(k) ?? 0) + 1);
    }
    return [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  };
  // A demoted blocker carries `lane: "backlog"`; a native minor has no lane at all.
  // Reported as two lines rather than one "deferred" total because they are different
  // populations with different precisions, and pooling them is what makes a later
  // rubric change unattributable.
  const demoted = rows.filter((r) => r?.lane === "backlog").length;
  const out = [
    "## Deferred findings",
    "",
    `${total} finding(s) the gate did not act on: ${total - demoted} non-blocking, ${demoted} demoted.`,
    "",
    "| lens | n |",
    "| --- | --- |",
    ...tally("lens").map(([k, n]) => `| ${k} | ${n} |`),
    "",
    "| severity | n |",
    "| --- | --- |",
    ...tally("severity").map(([k, n]) => `| ${k} | ${n} |`),
    "",
  ];
  if (omitted > 0) {
    out.push(
      // The budget ACTUALLY applied, not the module default. They differ whenever a
      // caller passes `maxChars`, and a notice that names a number the trim did not
      // use is a false explanation of a real omission.
      `⚠ **This record is PARTIAL** — ${emitted} of ${total} findings are in \`output.text\`; `
        + `${omitted} were dropped to fit the ${maxChars} character budget.`,
      "",
    );
  }
  out.push(
    `_Advisory. This check never gates. Rubric generation: \`${panelSha ?? "unknown"}\`._`,
  );
  // `MAX_SUMMARY_CHARS - 1`, because `clip` appends an ellipsis and so returns n+1
  // characters. Clipping at the ceiling itself produced 60001 and a constant the code
  // exceeds is worse than no constant — the marker is kept, the ceiling is exact.
  return clip(out.join("\n"), MAX_SUMMARY_CHARS - 1);
}

/**
 * Refuse to emit anything that could gate.
 *
 * A THROW, not a coercion, and this is the file's one hard failure. The house rule is
 * that read paths degrade to fewer records and the write path refuses on any doubt —
 * and the doubt this guards is a code change, not bad data, so it should redden CI
 * rather than ship a quietly gating channel. The step that calls this is
 * `continue-on-error`, so the blast radius of the throw is one advisory check run
 * going missing, which is the correct trade against writing a `failure` conclusion
 * into a namespace `set-state.mjs` reads.
 */
export function assertAdvisory({ name, conclusion }) {
  if (String(name ?? "").startsWith(LENS_CHECK_PREFIX)) {
    throw new Error(
      `deferred-findings: check name ${JSON.stringify(name)} is inside the ${LENS_CHECK_PREFIX} `
        + "namespace, which set-state.mjs reads by prefix to decide lensBlocked",
    );
  }
  if (!ADVISORY_CONCLUSIONS.includes(String(conclusion ?? ""))) {
    throw new Error(
      `deferred-findings: conclusion ${JSON.stringify(conclusion)} is not advisory `
        + `(expected one of ${ADVISORY_CONCLUSIONS.join(", ")})`,
    );
  }
}

/** The whole check-run payload, guarded. */
export function buildDeferredCheck({ lensFindings, panelSha = null, maxChars = MAX_TEXT_CHARS } = {}) {
  const records = collectDeferred(lensFindings);
  const { text, total, emitted, omitted } = buildDeferredText(records, { panelSha, maxChars });
  const sha = /^[0-9a-f]{40}$/.test(str(panelSha)) ? str(panelSha) : null;
  const payload = {
    name: DEFERRED_CHECK_NAME,
    conclusion: DEFERRED_CONCLUSION,
    output: {
      title: omitted > 0
        ? `${total} deferred (${omitted} not recorded)`
        : `${total} deferred`,
      // ALL records, not the emitted prefix — see `renderDeferredSummary`. The trim
      // bounds `text`; the tables are bounded by the manifest, so narrowing them to
      // the prefix would only make the counts disagree with the prose.
      summary: renderDeferredSummary({ total, emitted, omitted, records, panelSha: sha, maxChars }),
      text,
    },
  };
  assertAdvisory(payload);
  return { ...payload, total, emitted, omitted };
}

/**
 * Read each manifest lens's `verdict.json`.
 *
 * Read-path discipline: a lens whose file is missing or unparseable contributes
 * nothing and does not throw. A lens that crashed has no verdict to defer, and a
 * malformed one must not take the advisory channel down with it — the same
 * fail-quiet the gating writer applies to the same file.
 */
export function readLensFindings(reviewDir, manifest, { read = readFileSync, exists = existsSync } = {}) {
  const out = [];
  for (const lens of Array.isArray(manifest) ? manifest : []) {
    const id = str(lens?.id);
    if (!id) continue;
    const file = path.join(reviewDir, id, "verdict.json");
    if (!exists(file)) continue;
    try {
      const v = JSON.parse(read(file, "utf8"));
      out.push({ lens: id, findings: Array.isArray(v?.findings) ? v.findings : [] });
    } catch {
      continue; // this lens only
    }
  }
  return out;
}

// `parseArgs` starts at argv[2] — it is given the WHOLE `process.argv`, the way
// every other CLI in this directory calls it, not a pre-sliced tail.
function main(argv) {
  const args = parseArgs(argv);
  const reviewDir = str(args["review-dir"]) || ".agent-review";
  const lensesPath = str(args.lenses);
  const outJson = str(args["out-json"]);
  if (!lensesPath || !outJson) {
    console.error("usage: deferred-findings.mjs --lenses <lenses.json> --out-json <file> [--review-dir <dir>] [--panel-sha <sha>]");
    process.exit(2);
  }
  let manifest = [];
  try {
    manifest = JSON.parse(readFileSync(lensesPath, "utf8"));
  } catch (e) {
    // The manifest is the trusted lens set. Without it there is no population to
    // record and no honest way to guess one, so refuse rather than write an empty
    // record that reads as "this round deferred nothing".
    console.error(`deferred-findings: cannot read ${lensesPath}: ${e.message}`);
    process.exit(1);
  }
  const check = buildDeferredCheck({
    lensFindings: readLensFindings(reviewDir, manifest),
    panelSha: str(args["panel-sha"]),
  });
  writeFileSync(outJson, `${JSON.stringify(check, null, 2)}\n`);
  console.log(
    `deferred-findings: ${check.total} deferred, ${check.emitted} recorded, ${check.omitted} omitted → ${outJson}`,
  );
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url))) {
  main(process.argv);
}
