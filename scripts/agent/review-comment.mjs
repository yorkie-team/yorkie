// Triage renderer for the on-demand "@claude review" comment.
//
// The panel concatenates every lens's full summary.md into ONE comment, so
// blocking findings end up buried among nits and long per-lens prose (a real
// review ran ~20k chars / ~120 lines). This module RE-ORGANISES the SAME
// findings — read from each lens's verdict.json — into a triage layout:
//
//   - a one-line verdict + counts headline;
//   - EVERY merge-blocking finding, one line each, expanded, at the top;
//   - minor/nit findings collapsed per lens (count in the <summary>);
//   - the long reviewer prose + relocated/pre-existing findings collapsed.
//
// PRESENTATION ONLY. It changes nothing about which findings exist, their
// severity, or the gate — it reads verdict.json and renders. The autonomous
// panel's per-lens check runs (and their machine-readable output.text) go
// through severity.mjs::renderSummaryMd and are deliberately untouched; this
// renderer is used only by agent-review-on-demand.yml, which posts no checks.
//
// GitHub renders <details>/<summary> and fenced blocks natively, so the whole
// layout is plain Markdown with no external rendering.

import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { BLOCKING, adjudicationNote, normalizeSeverity } from "./severity.mjs";

// Leave ~5k of the 65 536-char comment cap for the parts the workflow adds
// around this region (marker, verifier tally, AI-prompt fold, advisory note).
const DEFAULT_MAX_CHARS = 60000;

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

/** relocated | blocking | suggestion | nit | other — from severity + lane. */
export function bucketOf(f) {
  if (!f || typeof f !== "object") return "other";
  if (f.lane === "backlog") return "relocated"; // demoted: real but predates the PR
  const sev = normalizeSeverity(f.severity);
  if (BLOCKING.has(sev)) return "blocking";
  if (sev === "minor") return "suggestion";
  if (sev === "nit") return "nit";
  return "other";
}

// collapse whitespace for one-line rendering (the summary is a single sentence).
const oneLine = (s) => String(s ?? "").replace(/\s+/g, " ").trim();

/**
 * A linkable `file:line` reference. With a blobBase (…/blob/<sha>) it becomes a
 * real link to the reviewed commit; without one it degrades to a code span; with
 * no file it renders nothing. Line is appended only when the lens supplied it.
 */
export function loc(f, blobBase) {
  const file = f && typeof f.file === "string" ? f.file.trim() : "";
  if (!file) return "";
  const line = Number.isInteger(f.line) && f.line > 0 ? f.line : null;
  const label = line ? `${file}:${line}` : file;
  if (!blobBase) return `\`${label}\``;
  // Encode each path segment (preserving the `/` separators) so a filename with a
  // reserved char — `)` would end the Markdown link early, `#`/`?` would start a
  // fragment/query — produces a valid commit URL. The label keeps the raw path.
  // encodeURIComponent leaves `()` unescaped, but `)` is exactly what closes the
  // link, so encode parens too.
  const enc = (s) => encodeURIComponent(s).replace(/\(/g, "%28").replace(/\)/g, "%29");
  const encodedPath = file.split("/").map(enc).join("/");
  const url = `${blobBase.replace(/\/$/, "")}/${encodedPath}${line ? `#L${line}` : ""}`;
  return `[\`${label}\`](${url})`;
}

// The wordings the clustering pass folded into this finding — shown on expand so
// a reader can see what was merged (never silently dropped). Not separate
// findings, so they don't count toward preservation.
function mergedNote(f) {
  const m = Array.isArray(f.mergedFrom) ? f.mergedFrom : [];
  if (!m.length) return "";
  return (
    `\n\n  <details><summary>also reported as (${m.length})</summary>\n\n` +
    m.map((x) => `  - (${normalizeSeverity(x.severity)}) ${oneLine(x.summary) || "(no summary)"}`).join("\n") +
    `\n  </details>`
  );
}

// The one-liner for a blocking finding — summary + linkable ref + lens tag. This
// is the part that is NEVER dropped: every blocking finding is published at least
// this much (see the budget assembly in renderReviewComment).
function blockingHead(f, blobBase) {
  const where = loc(f, blobBase);
  return `- ${where ? `**${where}** — ` : ""}${oneLine(f.summary) || "(no summary)"}` +
    ` <sup>${f._lens}</sup>` +
    (f.unsettled ? " _(verifier could not settle this)_" : "") +
    // The adjudicator's decision on a finding that survived a dispute. It was
    // computed, rendered into the lens check body by severity.mjs, and then
    // dropped on the floor by every COMMENT surface — so the one place a
    // maintainer actually reads findings never said a dispute had happened, let
    // alone how it went. Same renderer as the check body, so the two cannot drift.
    adjudicationNote(f);
}

// The collapsible extra for a blocking finding — its longer `evidence` and any
// merged wordings. "" when there is none. Appended to the one-liner only while
// the comment is under budget, so oversized evidence can never push a blocking
// finding out — it just loses its (collapsible) detail.
function blockingEvidence(f) {
  const ev = typeof f.evidence === "string" && f.evidence.trim();
  const evBlock = ev ? `\n\n  <details><summary>evidence</summary>\n\n  ${f.evidence.trim().replace(/\n/g, "\n  ")}\n  </details>` : "";
  return evBlock + mergedNote(f);
}

// One row inside a collapsed minor/nit block. `showSev` prefixes the severity
// only when the block mixes minor and nit (a single-severity block says it once
// in its <summary> line instead).
function minorRow(f, blobBase, showSev) {
  const where = loc(f, blobBase);
  const tag = showSev ? `_(${normalizeSeverity(f.severity)})_ ` : "";
  return `- ${tag}${where ? `**${where}** — ` : ""}${oneLine(f.summary) || "(no summary)"}` +
    (f.unsettled ? " _(verifier could not settle this)_" : "") +
    adjudicationNote(f);
}

// One relocated/pre-existing finding, keeping the proof line that justifies the
// demotion (mirrors severity.mjs::demotedSection so the audit trail survives).
function relocatedRow(f, blobBase) {
  const where = loc(f, blobBase);
  const n = f.novelty || {};
  const proof = n.alsoAt
    ? `\n  - this line already exists at \`${n.alsoAt}\``
    : n.contentSha
      ? `\n  - content dates to \`${String(n.contentSha).slice(0, 9)}\`, which predates the base`
      : "";
  return `- ${where ? `**${where}** — ` : ""}${oneLine(f.summary) || "(no summary)"} <sup>${f._lens}</sup>${proof}`;
}

/**
 * Render the triage comment region (everything between the marker and the
 * verifier tally). `lenses` is an array of per-lens objects, in manifest order:
 *   { id, title, gating (bool), applicable (bool), conclusion, findings[], summary, unverified }
 * `findings[]` are the raw verdict.json findings for that lens.
 *
 * Returns "" when there is nothing to say (no lens produced findings AND none
 * errored) so the caller can fall back; otherwise a self-contained Markdown
 * region. Collapsed sections past `maxChars` are dropped with a stated count —
 * blocking findings and the headline are never dropped.
 */
export function renderReviewComment(lenses, { blobBase = "", maxChars = DEFAULT_MAX_CHARS } = {}) {
  const list = (Array.isArray(lenses) ? lenses : []).filter((l) => l && typeof l === "object");

  // Tag every finding with its lens title, and bucket it. Lenses keep manifest
  // order so per-lens sections and reviewer notes read in a stable sequence.
  const perLens = list.map((l) => {
    const findings = (Array.isArray(l.findings) ? l.findings : []).filter((f) => f && typeof f === "object");
    for (const f of findings) f._lens = l.title || l.id || "lens";
    const by = { blocking: [], suggestion: [], nit: [], relocated: [] };
    for (const f of findings) {
      const b = bucketOf(f);
      if (by[b]) by[b].push(f);
    }
    return { ...l, findings, by, total: findings.length };
  });

  const all = (k) => perLens.flatMap((l) => l.by[k]);
  const blocking = all("blocking");
  const suggestions = all("suggestion");
  const nits = all("nit");
  const relocated = all("relocated");
  const lensesWithFindings = perLens.filter((l) => l.total > 0);
  // A blocking finding the verifier ERRORED on (per-lens count) is unfiltered,
  // not confirmed — surfaced once under the headline instead of per lens.
  const erroredLenses = perLens.filter((l) => Number(l.unverified?.errored) > 0);
  const erroredCount = erroredLenses.reduce((n, l) => n + Number(l.unverified.errored || 0), 0);

  if (lensesWithFindings.length === 0 && erroredCount === 0) return "";

  // Header verdict from the gate rule the panel uses (blocking+applicable lens
  // whose conclusion isn't success). Same decision as the workflow's old inline
  // computation, moved here so it lives next to what it describes.
  const blocked = perLens.some(
    (l) => l.gating !== false && l.applicable !== false && l.conclusion && l.conclusion !== "success",
  );
  const header = blocked ? "### 🔴 Review panel: changes suggested" : "### 🟢 Review panel: looks good";

  // The bold headline stays the three primary triage buckets; relocated and the
  // unverified flag ride after "across N lenses" so they don't dilute it.
  const counts = [
    plural(blocking.length, "blocking", "blocking"),
    plural(suggestions.length, "suggestion", "suggestions"),
    plural(nits.length, "nit", "nits"),
  ];
  let headline = `**${counts.join(" · ")}** across ${plural(lensesWithFindings.length, "lens", "lenses")}`;
  if (relocated.length) headline += ` · ${plural(relocated.length, "relocated", "relocated")}`;
  if (erroredCount) headline += ` · ⚠️ ${erroredCount} unverified`;

  const unverifiedNote = erroredCount
    ? `\n\n> ⚠️ ${plural(erroredCount, "blocking finding", "blocking findings")} across ` +
      `${plural(erroredLenses.length, "lens", "lenses")} could not be verified (session errors) — treat those as unreviewed claims.`
    : "";

  // Blocking section: critical before major, then manifest lens order, then the
  // order they were found. Always emitted in full — this is the whole point.
  const rankSev = (f) => (normalizeSeverity(f.severity) === "critical" ? 0 : 1);
  const lensOrder = new Map(perLens.map((l, i) => [l.title || l.id, i]));
  const blockingSorted = blocking
    .map((f, i) => ({ f, i }))
    .sort((a, b) => rankSev(a.f) - rankSev(b.f) || (lensOrder.get(a.f._lens) - lensOrder.get(b.f._lens)) || a.i - b.i)
    .map((x) => x.f);
  // Optional collapsed blocks, appended in priority order against the budget.
  const optional = [];
  for (const l of perLens) {
    const mn = l.by.suggestion, nt = l.by.nit;
    if (!mn.length && !nt.length) continue;
    const parts = [];
    if (mn.length) parts.push(plural(mn.length, "suggestion", "suggestions"));
    if (nt.length) parts.push(plural(nt.length, "nit", "nits"));
    const mixed = mn.length > 0 && nt.length > 0;
    const rows = [...mn, ...nt].map((f) => minorRow(f, blobBase, mixed)).join("\n");
    optional.push({
      count: mn.length + nt.length,
      md: `<details>\n<summary>💡 ${l.title} — ${parts.join(" · ")}</summary>\n\n${rows}\n</details>`,
    });
  }
  if (relocated.length) {
    optional.push({
      count: relocated.length,
      md: `<details>\n<summary>📦 Relocated / pre-existing — not this PR's to fix (${relocated.length})</summary>\n\n` +
        relocated.map((f) => relocatedRow(f, blobBase)).join("\n") + `\n</details>`,
    });
  }
  const notes = lensesWithFindings
    .filter((l) => typeof l.summary === "string" && l.summary.trim())
    .map((l) => `**${l.title}** — ${l.summary.trim()}`);
  if (notes.length) {
    optional.push({
      count: 0, // prose, not findings — never counted as "hidden findings"
      md: `<details>\n<summary>🗒️ Reviewer notes (${plural(notes.length, "lens", "lenses")})</summary>\n\n${notes.join("\n\n")}\n</details>`,
    });
  }

  // ---- assemble under maxChars, enforcing it over EVERYTHING ---------------
  // Guaranteed minimum: header + headline + note + EVERY blocking finding's
  // one-liner. Evidence folds and the collapsed sections are added only while
  // under budget; whatever doesn't fit is reported as a count, never dropped
  // silently. This is what keeps oversized blocking evidence from either
  // exceeding GitHub's cap or slicing a blocking finding out downstream.
  const NOTE_MARGIN = 200; // reserve room for the "… N hidden" notice
  const budget = Math.max(0, maxChars - NOTE_MARGIN);
  const preamble = `${header}\n${headline}${unverifiedNote}`;

  let out = preamble;
  let hiddenBlocking = 0;
  const shown = []; // indices of blockingSorted whose one-liner was kept
  if (blocking.length) {
    out += `\n\n#### 🚫 Blocking (${blocking.length})`;
    for (let i = 0; i < blockingSorted.length; i++) {
      const line = `\n${blockingHead(blockingSorted[i], blobBase)}`;
      // One-liners are the guaranteed minimum, but a pathological count still
      // must not exceed the cap — keep as many as fit, count the rest.
      if (out.length + line.length <= budget) { out += line; shown.push(i); }
      else hiddenBlocking++;
    }
    // Add each kept finding's evidence fold, front-to-back, while under budget —
    // rebuilt so the fold sits under its own finding. Skipped entirely (no
    // rebuild) in the common case where no blocking finding carries evidence.
    if (shown.some((i) => blockingEvidence(blockingSorted[i]))) {
      let rebuilt = `\n\n#### 🚫 Blocking (${blocking.length})`;
      let used = preamble.length + rebuilt.length;
      for (const i of shown) {
        const head = `\n${blockingHead(blockingSorted[i], blobBase)}`;
        const evid = blockingEvidence(blockingSorted[i]);
        if (evid && used + head.length + evid.length <= budget) {
          rebuilt += head + evid; used += head.length + evid.length;
        } else {
          rebuilt += head; used += head.length;
        }
      }
      out = preamble + rebuilt;
    }
  }

  // Fit optional collapsed blocks under the budget; whole blocks only.
  let hiddenOptional = 0;
  let stopped = false;
  for (const block of optional) {
    if (!stopped && out.length + block.md.length + 2 <= budget) {
      out += `\n\n${block.md}`;
    } else {
      stopped = true;
      hiddenOptional += block.count;
    }
  }

  const hidden = hiddenBlocking + hiddenOptional;
  if (hidden > 0) {
    const parts = [];
    if (hiddenBlocking) parts.push(plural(hiddenBlocking, "blocking finding", "blocking findings"));
    if (hiddenOptional) parts.push(plural(hiddenOptional, "other finding", "other findings"));
    out += `\n\n… _(${parts.join(" + ")} hidden for length; full detail in the review artifacts.)_`;
  }
  return out;
}

/**
 * Assemble the per-lens inputs from a `.agent-review` tree. Fail-safe: a missing
 * dir / malformed file contributes nothing rather than throwing (this feeds an
 * advisory comment). `manifest` is lenses.json (order + titles + gating).
 */
export function collectLenses(reviewDir, manifest) {
  const lenses = Array.isArray(manifest) ? manifest : [];
  let panel = [];
  try { panel = JSON.parse(readFileSync(path.join(reviewDir, "panel.json"), "utf8")); } catch { panel = []; }
  const byId = Object.fromEntries((Array.isArray(panel) ? panel : []).map((p) => [p.id, p]));
  const out = [];
  for (const lens of lenses) {
    if (!lens || !lens.id) continue;
    let v = null;
    try { v = JSON.parse(readFileSync(path.join(reviewDir, lens.id, "verdict.json"), "utf8")); } catch { v = null; }
    const p = byId[lens.id] || {};
    out.push({
      id: lens.id,
      title: lens.title || lens.id,
      gating: String(lens.gating ?? "blocking") === "blocking",
      applicable: p.applicable !== false,
      conclusion: p.conclusion ?? (v ? v.conclusion : "failure"),
      findings: v && Array.isArray(v.findings) ? v.findings : [],
      summary: v && typeof v.summary === "string" ? v.summary : "",
      unverified: v && v.unverified ? v.unverified : null,
    });
  }
  return out;
}

const HERE = path.dirname(fileURLToPath(import.meta.url));

// --- CLI -------------------------------------------------------------------
// node review-comment.mjs --review-dir .agent-review --blob-base <url> --out <file>
// Writes the rendered region (or "" when there's nothing to say) to --out and
// echoes it. Always exits 0 — advisory; the workflow falls back to the old
// per-lens concatenation when the file is empty/missing.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const opt = (name, def) => {
    const i = args.indexOf(name);
    return i >= 0 && i + 1 < args.length ? args[i + 1] : def;
  };
  const reviewDir = opt("--review-dir", ".agent-review");
  const lensesPath = opt("--lenses", path.join(HERE, "lenses", "lenses.json"));
  const blobBase = opt("--blob-base", "");
  const out = opt("--out", null);
  let region = "";
  try {
    let manifest = [];
    try { manifest = JSON.parse(readFileSync(lensesPath, "utf8")); } catch { manifest = []; }
    region = renderReviewComment(collectLenses(reviewDir, manifest), { blobBase });
  } catch (err) {
    process.stderr.write(`review-comment: ${err?.message ?? err}\n`);
  }
  if (out) {
    try { writeFileSync(out, region); } catch (err) { process.stderr.write(`review-comment: ${err?.message ?? err}\n`); }
  }
  if (region) process.stdout.write(region + "\n");
}
