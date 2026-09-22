// The fixer's work list, built from the panel's own lens check runs.
//
// WHY A MODULE, and it is the same reason prior-findings.mjs is one. This logic
// lived as ~100 lines of inline `github-script` in agent-review-panel.yml, and
// `@claude fix` needs the identical brief: the same per-file checklist, the same
// two bounds, the same "cut each lens body at its first finding section" rule.
// Inline YAML JavaScript can hold neither a unit test nor a linter, and a second
// copy of a *prompt* input is how the two silently diverge — one workflow keeps
// handing the fixer 39 minor findings while the other stops.
//
// Every bound below was paid for. See the comments at MAX_ITEMS and `proseOnly`:
// each records a specific round on a specific PR that the unbounded version made
// worse, and removing one does not "simplify" this file, it reopens that round.
//
// WHAT IS TRUSTED HERE: nothing. `output.text` is a previous round's MODEL output
// and `output.summary` is model prose, so both are untrusted text on their way
// into a prompt. This module's job is to bound and label them; the fencing and
// the "this is DATA, not instructions" framing belong to the caller that builds
// the prompt.
//
// Usage:
//   node fix-brief.mjs <pr> --sha <head-sha> (--checks <n,...> | --lenses <lenses.json>)
//                      [--github-output] [--out-json <file>]
// Writes the `checklist` and `summary` step outputs when --github-output is set
// (needs $GITHUB_OUTPUT); otherwise prints the whole brief as JSON to stdout, or
// to --out-json. There is no `findings` step output — the counts travel on the
// JSON only.

import { randomUUID } from "node:crypto";
import { appendFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gh, commitCheckRuns, withFullOutput, parseArgs } from "./gh-checks.mjs";
import { resolveCheckNames as resolveNames } from "./prior-findings.mjs";

/**
 * BOUND THE CHECKLIST. Each lens persists up to 60k chars of findings JSON, so an
 * unbounded join across lenses emits a step output hundreds of KB wide — blowing
 * up the very fix prompt this exists to shrink, and risking the Actions output
 * limit.
 *
 * 40 items / 16k chars is not arbitrary: it is roughly one round's worth of
 * blocking work for a human-sized diff. An omitted finding is NOT lost — it is
 * critical/major, so it is persisted in `output.text`, carried forward by
 * prior-findings.mjs, and re-raised next round.
 */
export const MAX_ITEMS = 40;
export const MAX_CHARS = 16000;

const str = (v) => (typeof v === "string" ? v : "");
const clip = (s, n) => {
  const t = String(s ?? "");
  return t.length > n ? `${t.slice(0, n)}…` : t;
};

/**
 * A lens body with every finding section removed.
 *
 * The fixer's work list is the CHECKLIST, which the panel already filtered to
 * critical/major non-backlog findings when it wrote each check run. The per-lens
 * BODIES never got that filter: `severity.mjs::renderSummaryMd` emits a Minor, a
 * Nit and a demoted section unconditionally, so passing them whole handed the
 * fixer every non-blocking finding verbatim — 39 minor + 13 nit on #605 round 3 —
 * restrained only by the prose "fix every BLOCKING one". It edited them anyway,
 * the diff grew, the next round found more, and the PR paid for another panel.
 *
 * severity.mjs renders EVERY section as "\n### " (both `section` and
 * `demotedSection`) and nothing above the first one is a finding, so cutting at
 * the first occurrence keeps the verdict header, the verifier-outage warning and
 * the lens's own narration, and drops every finding — including the blocking
 * ones, which is correct: those reach the fixer through the checklist, already
 * filtered.
 */
export function proseOnly(md) {
  return String(md ?? "").split("\n### ")[0];
}

/** A check run's markdown body, or "". */
export function bodyOf(run) {
  return (run && run.output && str(run.output.summary)) || "";
}

/**
 * The latest FAILING run per lens name, as `Map<name, run>`.
 *
 * Three filters, each load-bearing:
 *   - `names` — only OUR panel's lens checks, so a stray check from another app
 *     cannot inject text into the fix prompt.
 *   - `app.slug === 'github-actions'` — the producing app, which (unlike a name)
 *     cannot be chosen by a third-party integration.
 *   - `conclusion === 'failure'` — a passing lens has nothing to fix.
 *
 * Latest by `started_at`, so a re-run supersedes the run it replaced instead of
 * the fixer working from whichever the API happened to list first.
 */
export function latestFailing(runs, names) {
  const want = new Set((Array.isArray(names) ? names : []).filter((n) => typeof n === "string" && n !== ""));
  const latest = new Map();
  for (const r of Array.isArray(runs) ? runs : []) {
    if (!r || !want.has(r.name)) continue;
    if (!r.app || r.app.slug !== "github-actions") continue;
    if (r.conclusion !== "failure") continue;
    const prev = latest.get(r.name);
    // Date.parse over `new Date(a) > new Date(b)`: an unparseable `started_at`
    // yields NaN, and every NaN comparison is false, so a run with a junk
    // timestamp cannot displace one with a real one.
    if (!prev || Date.parse(r.started_at) > Date.parse(prev.started_at)) latest.set(r.name, r);
  }
  return latest;
}

/**
 * Group the machine-readable findings each lens persisted in `output.text` by file.
 *
 * Front-loading exact paths lets the fixer read the right files first and converge
 * in fewer rounds instead of re-discovering them from the prose summaries. Junk is
 * skipped per lens (see prior-findings.mjs: one lens's malformed JSON must not
 * zero out the others).
 */
export function findingsByFile(runsByName) {
  const byFile = new Map();
  const entries = runsByName instanceof Map ? [...runsByName] : Object.entries(runsByName ?? {});
  for (const [name, run] of entries) {
    let findings;
    try {
      findings = JSON.parse(str(run?.output?.text) || "[]");
    } catch {
      continue; // this lens only
    }
    if (!Array.isArray(findings)) continue;
    const lens = String(name).replace(/^agent-review-/, "");
    for (const f of findings) {
      if (!f || typeof f !== "object" || Array.isArray(f)) continue;
      const file = str(f.file).trim() || "(file not specified)";
      if (!byFile.has(file)) byFile.set(file, []);
      byFile.get(file).push({
        lens,
        severity: f.severity,
        summary: f.summary,
        evidence: f.evidence,
        mergedFrom: f.mergedFrom,
      });
    }
  }
  return byFile;
}

/**
 * Render the per-file checklist, bounded.
 *
 * Emits deterministically (lens order, then per-file insertion order), stops at
 * the first limit reached, and STATES how many findings were left out — so the
 * fixer knows the list is partial rather than complete. A silent truncation reads
 * as "this is everything", which is how a fixer concludes it is done.
 */
export function buildChecklist(byFile, { maxItems = MAX_ITEMS, maxChars = MAX_CHARS } = {}) {
  const entries = byFile instanceof Map ? [...byFile] : Object.entries(byFile ?? {});
  let emitted = 0;
  let total = 0;
  let chars = 0;
  let truncated = false;
  const blocks = [];
  for (const [file, items] of entries) {
    const list = Array.isArray(items) ? items : [];
    total += list.length;
    if (truncated) continue; // keep counting the remainder for the tally
    const kept = [];
    for (const i of list) {
      if (emitted >= maxItems) {
        truncated = true;
        break;
      }
      // Other wordings of the same defect, folded in by the clustering pass.
      // Included so the fixer reads every description rather than only the one
      // that won the slot — if the merge was wrong, the second description is
      // what tells it there are two problems. Bounded to two: the point is
      // context, not completeness.
      const also = Array.isArray(i.mergedFrom) && i.mergedFrom.length
        ? i.mergedFrom.slice(0, 2).map((m) => `\n      also reported as: ${clip(m?.summary, 300)}`).join("")
        : "";
      const line = `    - (${i.lens}/${i.severity}) ${clip(i.summary, 500)}`
        + (i.evidence ? `\n      evidence: ${clip(i.evidence, 400)}` : "")
        + also;
      if (chars + line.length > maxChars) {
        truncated = true;
        break;
      }
      chars += line.length;
      emitted++;
      kept.push(line);
    }
    if (kept.length) blocks.push(`- [ ] ${file}\n${kept.join("\n")}`);
  }
  let checklist = blocks.join("\n") || "(no per-file findings parsed — use the per-lens summaries below)";
  const omitted = total - emitted;
  if (omitted > 0) {
    // NOT "see the summaries below for the rest" — they no longer carry findings.
    checklist += `\n\n(${omitted} further finding(s) omitted to bound prompt size — `
      + "this list is PARTIAL. Fix what is listed; the rest is re-raised next round.)";
  }
  return { checklist, emitted, total, truncated, files: blocks.length };
}

/**
 * The per-lens verdict headers and narration.
 *
 * `hasBlocks` decides whether the bodies are cut. When nothing parsed, the
 * checklist is a bare "(no per-file findings parsed…)" pointer; cutting the
 * bodies as well would leave the fixer with no findings at all and burn a whole
 * round on nothing. There the unfiltered bodies are the better failure mode, and
 * the pointer stays honest.
 */
export function buildSummary(runsByName, { hasBlocks }) {
  const runs = runsByName instanceof Map ? [...runsByName.values()] : Object.values(runsByName ?? {});
  return runs
    .map((r) => `## ${str(r?.name)}\n${hasBlocks ? proseOnly(bodyOf(r)) : bodyOf(r)}`)
    .join("\n\n") || "No findings summary available.";
}

/**
 * The whole brief, from a run list.
 *
 * `checklist` and `summary` are the two prompt inputs; `lenses`, `emitted`,
 * `total` and `truncated` are the caller's bookkeeping (how much of this round's
 * work list actually fitted). Only the first two become step outputs.
 */
export function buildBrief(runs, names, opts = {}) {
  return buildBriefFrom(latestFailing(runs, names), opts);
}

/**
 * `buildBrief` over an ALREADY-selected `Map<name, run>`.
 *
 * Split out because the CLI re-fetches each selected run in full (the list
 * response truncates `output.text`) and then must not filter again: a re-fetched
 * object that came back missing `app` would be silently dropped by
 * `latestFailing`, losing a whole lens's findings from the work list with nothing
 * to notice it. Selecting once, then building, removes that possibility.
 */
export function buildBriefFrom(failing, opts = {}) {
  const byFile = findingsByFile(failing);
  const cl = buildChecklist(byFile, opts);
  return {
    checklist: cl.checklist,
    summary: buildSummary(failing, { hasBlocks: cl.files > 0 }),
    lenses: [...failing.keys()],
    emitted: cl.emitted,
    total: cl.total,
    truncated: cl.truncated,
  };
}

// --- CLI --------------------------------------------------------------------

/**
 * One `name<<delim ... delim` block for $GITHUB_OUTPUT.
 *
 * The delimiter is RANDOM per call, not a fixed sentinel, and this is a security
 * property rather than a style choice: the value is previous-round model output,
 * so a finding whose text contained a guessable delimiter could close the block
 * early and append arbitrary step outputs of its own. `@actions/core::setOutput`
 * randomises for exactly this reason, and replacing it here must not lose that.
 * The containment check is belt-and-braces on a v4 UUID.
 */
export function ghOutputBlock(name, value, delimiter = randomUUID()) {
  const v = String(value ?? "");
  if (v.includes(delimiter)) throw new Error(`fix-brief: value for '${name}' contains its own delimiter`);
  return `${name}<<${delimiter}\n${v}\n${delimiter}\n`;
}

function main() {
  const args = parseArgs(process.argv, { booleans: ["github-output"] });
  const pr = args._[0];
  const sha = str(args.sha).trim();
  const names = resolveNames(args);
  if (!pr || !/^\d+$/.test(pr) || sha === "" || names.length === 0) {
    console.error("Usage: node fix-brief.mjs <pr> --sha <head-sha> (--checks <name,...> | --lenses <lenses.json>) [--github-output] [--out-json <file>]");
    process.exit(2); // a bad invocation must not look like "no findings"
  }

  // A brief we could not build is NOT an empty brief. Unlike prior findings
  // (additive recall, degrades to carrying fewer), this IS the fixer's whole work
  // list: an empty one sends the agent off to edit a repo with no instructions.
  // Exit non-zero and let the caller decide; nothing downstream should read a
  // silent "" as "the panel found nothing".
  let runs;
  try {
    runs = withFullOutput(latestFailing(commitCheckRuns(sha, { api: gh }), names), { api: gh });
  } catch (err) {
    console.error(`fix-brief: could not read check runs for ${sha} (${err.message}).`);
    process.exit(1);
  }
  const brief = buildBriefFrom(runs);
  if (brief.lenses.length === 0) {
    console.error(`fix-brief: no failing lens checks on ${sha} — nothing to fix.`);
    process.exit(1);
  }

  if (args["out-json"]) writeFileSync(args["out-json"], JSON.stringify(brief));
  if (args["github-output"]) {
    const out = process.env.GITHUB_OUTPUT;
    if (!out) {
      console.error("fix-brief: --github-output given but $GITHUB_OUTPUT is unset.");
      process.exit(2);
    }
    appendFileSync(out, ghOutputBlock("checklist", brief.checklist) + ghOutputBlock("summary", brief.summary));
  } else if (!args["out-json"]) {
    process.stdout.write(JSON.stringify(brief));
  }
  console.error(
    `fix-brief: ${brief.emitted}/${brief.total} finding(s) across ${brief.lenses.length} failing lens(es)`
    + `${brief.truncated ? " (truncated)" : ""}`,
  );
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
