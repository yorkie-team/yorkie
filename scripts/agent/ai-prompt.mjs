// "Prompt for AI Agents" section for the on-demand review comment.
//
// When "@claude review" posts its findings, we also append a COLLAPSED
// <details> block whose body is a single fenced code block: a ready-to-paste
// instruction listing every blocking (critical/major) finding, so a maintainer
// can hand the whole review to their own coding agent in one click. GitHub
// renders every fenced code block with a native copy-to-clipboard button — that
// IS the "copy" affordance (a comment cannot run custom JS), so the whole
// section is deliberately just Markdown: a <details> fold wrapping a ``` fence.
//
// Scope: only findings that actually GATE this PR. Critical/major define
// "blocking" (see severity.mjs); demoted findings (lane === "backlog") are
// EXCLUDED because the panel already ruled they predate this change and are not
// this PR's to fix — putting them in a "fix these" prompt would be wrong.
// Minor/nit are excluded too: this block is the "make the PR mergeable" prompt,
// not a wishlist.

import { readFileSync, writeFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { BLOCKING, normalizeSeverity } from "./severity.mjs";

/** A finding gates iff it is blocking (critical/major) and NOT demoted. */
function isFixable(f) {
  if (!f || typeof f !== "object") return false;
  if (f.lane === "backlog") return false; // pre-existing / relocated — not ours to fix
  return BLOCKING.has(normalizeSeverity(f.severity));
}

// Collapse whitespace so "same summary, different wrapping" dedupes as one.
const norm = (s) => String(s ?? "").replace(/\s+/g, " ").trim();

/**
 * Blocking findings across all lenses, deduped by (file, summary). Sorted
 * critical-before-major, then by file, so the prompt is stable and the worst
 * items lead. Order among equal keys is preserved from the input.
 */
export function fixableFindings(findings) {
  const arr = Array.isArray(findings) ? findings.filter(isFixable) : [];
  const seen = new Set();
  const unique = [];
  for (const f of arr) {
    // "\n" separator: norm() strips newlines, so it can't appear inside either
    // part — the (file, summary) key is unambiguous.
    const key = `${norm(f.file)}\n${norm(f.summary)}`;
    if (seen.has(key)) continue;
    seen.add(key);
    unique.push(f);
  }
  const rank = (f) => (normalizeSeverity(f.severity) === "critical" ? 0 : 1);
  // Stable sort: decorate with the original index so equal keys keep input order.
  return unique
    .map((f, i) => ({ f, i }))
    .sort((a, b) => rank(a.f) - rank(b.f) || norm(a.f.file).localeCompare(norm(b.f.file)) || a.i - b.i)
    .map((x) => x.f);
}

// A fence at least one backtick longer than the longest backtick run in the
// body, so a summary that itself contains ``` cannot close the block early.
function fenceFor(body) {
  let longest = 0;
  for (const m of String(body).matchAll(/`+/g)) longest = Math.max(longest, m[0].length);
  return "`".repeat(Math.max(3, longest + 1));
}

/** One prompt line per finding: "N. [severity] `file` — summary". */
function promptLine(f, n) {
  const sev = normalizeSeverity(f.severity);
  const where = f.file ? `\`${norm(f.file)}\` — ` : "";
  return `${n}. [${sev}] ${where}${norm(f.summary) || "(no summary)"}`;
}

/**
 * Render the collapsed "Prompt for AI Agents" section, or "" when there are no
 * blocking findings (nothing to fix → no section). `findings` is the flattened
 * findings array from every lens's verdict.json.
 */
export function renderAiPromptSection(findings) {
  const fixable = fixableFindings(findings);
  if (fixable.length === 0) return "";
  const intro =
    "Fix the following blocking issues found by the review panel on this pull request. " +
    "For each, make the minimal change that resolves it, match the surrounding code style, " +
    "and do not introduce unrelated changes:";
  const lines = fixable.map((f, i) => promptLine(f, i + 1)).join("\n");
  const body = `${intro}\n\n${lines}`;
  const fence = fenceFor(body);
  // Blank lines around the fence are REQUIRED for GitHub to render a code block
  // inside the <details> HTML block (Markdown inside raw HTML needs the break).
  return (
    `<details>\n<summary>🤖 Prompt for AI Agents</summary>\n\n` +
    `${fence}\n${body}\n${fence}\n\n` +
    `</details>`
  );
}

/**
 * Collect the flattened findings from every lens's `<review-dir>/<lens>/verdict.json`.
 * Fail-safe: a missing dir, a non-lens entry, or a malformed verdict.json is
 * skipped, never thrown — this feeds an advisory comment and must not break it.
 */
export function collectFindings(reviewDir) {
  let entries = [];
  try {
    entries = readdirSync(reviewDir, { withFileTypes: true });
  } catch {
    return [];
  }
  const findings = [];
  for (const e of entries) {
    if (!e.isDirectory()) continue; // skip panel.json et al.
    try {
      const v = JSON.parse(readFileSync(path.join(reviewDir, e.name, "verdict.json"), "utf8"));
      if (Array.isArray(v?.findings)) findings.push(...v.findings);
    } catch {
      /* no verdict.json in this dir, or malformed — skip */
    }
  }
  return findings;
}

// --- CLI -------------------------------------------------------------------
// node ai-prompt.mjs --review-dir .agent-review --out /tmp/ai-prompt.md
// Writes the rendered section (or "" when nothing blocks) to --out, and echoes
// it to stdout. Always exits 0: this is an advisory add-on, never a gate.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const opt = (name, def) => {
    const i = args.indexOf(name);
    return i >= 0 && i + 1 < args.length ? args[i + 1] : def;
  };
  const reviewDir = opt("--review-dir", ".agent-review");
  const out = opt("--out", null);
  let section = "";
  try {
    section = renderAiPromptSection(collectFindings(reviewDir));
  } catch (err) {
    process.stderr.write(`ai-prompt: ${err?.message ?? err}\n`);
  }
  if (out) {
    try { writeFileSync(out, section); } catch (err) { process.stderr.write(`ai-prompt: ${err?.message ?? err}\n`); }
  }
  if (section) process.stdout.write(section + "\n");
}
