// Re-stamp the verdicts already recorded on one head, instead of running the
// panel again.
//
// WHY THIS EXISTS. `review-scope.mjs` decides two cases where a fresh panel run
// would be a fresh SAMPLE of a question already answered:
//
//   carry  the PR's own diff is unchanged since the head every lens approved —
//          a merge of main that touched none of its hunks or their context, or
//          a clean rebase. On #1426 a maintainer's `update-branch` made exactly
//          that head, the re-sample turned two demoted findings blocking, and
//          with the fix budget spent the PR went from agent:ready to
//          agent:blocked on code nobody had touched.
//   reuse  a rerun on the very commit the verdicts are on. On #1426 that
//          re-reviewed the identical head for ~$10 to re-derive findings
//          already on record.
//
// This writes `.agent-review/` in the shape the orchestrator writes it —
// `panel.json`, and per lens `summary.md` plus `carried-text.json` (the
// findings payload, verbatim) — so the workflow's existing "Post per-lens check
// runs" step posts the result, and every gate downstream of it (promote, the
// round guard, the fix brief) reads it exactly as it reads a reviewed round.
//
// FAIL DIRECTION: toward REVIEWING. Every doubt returns `ok: false` and writes
// nothing, and the workflow then runs the panel as it would have without this.
// A wrongly carried verdict ships code nobody judged; a needless review costs
// tokens.
//
// Usage:
//   node carry-verdicts.mjs <pr> --mode carry|reuse --source <sha> --head <sha>
//     --base <sha> --fingerprint <fp> --carry <n>
//     --lenses <lenses.json> --changed-files <f> --out <dir>
// Writes `ok=true|false` and `reason=` to $GITHUB_OUTPUT. Always exits 0.

import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gh, commitCheckRuns, withFullOutput, parseArgs } from "./gh-checks.mjs";
import { latestLensRuns, parseReviewState, serializeReviewState } from "./review-state.mjs";
import { lensApplies } from "./review-panel.mjs";

const SHA = /^[0-9a-f]{40}$/i;
// The GitHub conclusions a lens check is posted with (see the post step), mapped
// back to what the orchestrator writes into panel.json.
const FROM_CHECK = { success: "success", failure: "failure", neutral: "skipped" };

/**
 * Decide and build, with the API injected. Pure apart from `api`.
 *
 * Returns `{ ok: true, panel, files }` or `{ ok: false, reason }`. `files` is
 * keyed by lens id: `{ summary, text }`, `text` absent for a skipped lens.
 */
export function carryVerdicts(opts) {
  const {
    mode, source, head, base = "", fingerprint = "", carry = 0,
    manifest, changedFiles, api = gh, log = console.error,
  } = opts && typeof opts === "object" ? opts : {};
  const refuse = (reason) => ({ ok: false, reason });

  if (mode !== "carry" && mode !== "reuse") return refuse(`unknown mode ${JSON.stringify(mode)}`);
  if (!SHA.test(String(source ?? "")) || !SHA.test(String(head ?? ""))) return refuse("source and head must be 40-hex shas");
  // A reuse is by definition the same commit; anything else is a carry that
  // skipped the fingerprint check.
  if (mode === "reuse" && source.toLowerCase() !== head.toLowerCase()) return refuse("reuse needs source === head");
  const lenses = (Array.isArray(manifest) ? manifest : []).filter((l) => l && typeof l.id === "string" && l.id !== "");
  if (lenses.length === 0) return refuse("no lenses in the manifest");
  const files = Array.isArray(changedFiles) ? changedFiles : [];

  let runs;
  try {
    const names = lenses.map((l) => `agent-review-${l.id}`);
    // Full payloads: the list endpoint omits or truncates `output.text`, and that
    // field IS the fixer's work list on a reuse.
    runs = withFullOutput(latestLensRuns(commitCheckRuns(source, { api }), names), { api, log });
  } catch (err) {
    return refuse(`could not read the check runs on ${source.slice(0, 12)} (${err.message})`);
  }

  const panel = [];
  const out = {};
  const from = source.slice(0, 12);
  for (const lens of lenses) {
    const blocking = String(lens.gating ?? "blocking") === "blocking";
    // The fingerprint (carry) or the commit itself (reuse) is unchanged, so the
    // changed-file list — and with it which lenses apply — is too.
    if (!lensApplies(lens, files)) {
      panel.push({ id: lens.id, title: lens.title, blocking, applicable: false, conclusion: "skipped", valid: true });
      out[lens.id] = { summary: `> Not applicable to this pull request's files.` };
      continue;
    }
    const run = runs.get(`agent-review-${lens.id}`);
    if (!run) return refuse(`${lens.id} has no completed verdict on ${from}`);
    const conclusion = FROM_CHECK[run.conclusion];
    if (!conclusion) return refuse(`${lens.id}'s run on ${from} concluded ${JSON.stringify(run.conclusion)}, which is not a verdict`);
    // Only an approval carries to another head. review-scope already enforces
    // this; checked again here because this is the step that writes it.
    if (mode === "carry" && conclusion === "failure") return refuse(`${lens.id} did not approve ${from}; a blocking verdict is not carried`);
    const prior = parseReviewState(run.external_id);
    if (!prior && conclusion !== "skipped") return refuse(`${lens.id}'s run on ${from} carries no review state`);
    const hasText = typeof run.output?.text === "string" && run.output.text !== "";
    // A BLOCKING verdict's findings are the fixer's work list. Missing text is a
    // failed full-output fetch (the list copy omits it), not "no findings" —
    // reusing it as `[]` would spend a fix round on an empty list.
    if (conclusion === "failure" && !hasText) return refuse(`${lens.id}'s findings on ${from} could not be read`);
    const text = hasText ? run.output.text : "[]";
    try {
      if (!Array.isArray(JSON.parse(text))) return refuse(`${lens.id}'s findings payload is not a list`);
    } catch {
      return refuse(`${lens.id}'s findings payload does not parse`);
    }

    let reviewState;
    if (prior && mode === "carry") {
      // Moves to the NEW head and says it was carried. Keeps the fingerprint the
      // next round compares against, and counts consecutive carries so the cap in
      // `resolveReviewMode` can force a rebaseline.
      reviewState = serializeReviewState({ reviewed: head, base, since: "", mode: "carry", fp: fingerprint || prior.fp, carry });
    } else if (prior) {
      reviewState = run.external_id;
    }
    panel.push({
      id: lens.id, title: lens.title, blocking, applicable: true, conclusion, valid: true,
      ...(reviewState ? { reviewState } : {}),
    });
    const why = mode === "carry"
      ? `> Carried from \`${from}\`: this pull request's own diff is unchanged since that head was reviewed, so its verdict stands. No lens ran on this commit.`
      : `> Reused the verdict already recorded on \`${from}\` — this is the same commit. No lens ran again. Comment \`@claude rerun review\` to ask for a fresh review.`;
    out[lens.id] = { summary: `${why}\n\n${String(run.output?.summary ?? "")}`, text };
  }
  return { ok: true, panel, files: out };
}

/** Lay the result out where the post step reads it. */
export function writeCarried(dir, result) {
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, "panel.json"), JSON.stringify(result.panel, null, 2));
  for (const [id, f] of Object.entries(result.files)) {
    mkdirSync(path.join(dir, id), { recursive: true });
    writeFileSync(path.join(dir, id, "summary.md"), f.summary);
    if (typeof f.text === "string") writeFileSync(path.join(dir, id, "carried-text.json"), f.text);
  }
}

function main() {
  const a = parseArgs(process.argv);
  const setOutput = (k, v) => {
    console.error(`  ${k}=${v}`);
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${k}=${v}\n`);
  };
  let manifest = null;
  let changedFiles = [];
  try {
    manifest = JSON.parse(readFileSync(a.lenses, "utf8"));
    changedFiles = readFileSync(a["changed-files"], "utf8").split("\n").map((s) => s.trim()).filter(Boolean);
  } catch (err) {
    console.error(`carry-verdicts: could not read inputs (${err.message}).`);
  }
  const result = carryVerdicts({
    mode: a.mode, source: a.source, head: a.head, base: a.base || "",
    fingerprint: a.fingerprint || "", carry: Number(a.carry) || 0,
    manifest, changedFiles,
  });
  if (result.ok) {
    writeCarried(a.out || ".agent-review", result);
    console.error(`carry-verdicts: ${a.mode} from ${String(a.source).slice(0, 12)} — ${result.panel.length} lens(es) written.`);
  } else {
    console.error(`carry-verdicts: not carrying (${result.reason}); the panel will review instead.`);
  }
  setOutput("ok", String(result.ok));
  setOutput("reason", result.ok ? a.mode : result.reason.replace(/\n/g, " "));
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (err) {
    // A crash is a refusal: the panel reviews, which is always correct.
    console.error(`carry-verdicts: crashed (${err.message}); the panel will review instead.`);
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, "ok=false\nreason=crashed\n");
  }
}
