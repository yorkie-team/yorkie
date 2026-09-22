// Collect the PREVIOUS round's blocking findings, tagged with the lens that
// raised them, for `review-panel.mjs --prior-findings`.
//
// This is the cross-round re-check's input: without it, a finding that round N's
// fresh pass happens to miss vanishes from the gate even though nobody fixed it
// (the #521 false negative). Each prior finding is re-verified against the
// current repository and survives unless the verifier can refute it on grounded
// evidence.
//
// WHY A MODULE. The logic lived as ~40 lines of inline `github-script` in
// agent-review-panel.yml, and PR #558 needed a second copy for the on-demand
// workflow. Inline YAML JavaScript is untestable and un-lintable, and two copies
// of a gate input is how one of them rots. A plain `gh`-CLI script mirrors
// `review-round-guard.mjs` and `mark-ready.mjs`, and the parsing is unit-tested.
//
// Usage:
//   node prior-findings.mjs <pr-number> --lenses <lenses.json> [--out <file>]
// Writes a JSON array to --out (default stdout), and it is ALWAYS a valid array —
// see `tagPriorFindings` for why an API or parse error must not become a hard
// failure. The two exceptions are bad ARGUMENTS (unusable PR number, unreadable
// or empty lens manifest): those exit 2, because a broken invocation must not be
// indistinguishable from a legitimately empty first round.
//
// Requires the `gh` CLI authenticated via GH_TOKEN / GITHUB_TOKEN.

import { readFileSync, writeFileSync } from "node:fs";
import { BLOCKING, normalizeSeverity } from "./severity.mjs";
import { latestLensRuns } from "./review-state.mjs";
import { gh, prCommitsWithCheckRuns, allCheckRuns, withFullOutput, parseArgs } from "./gh-checks.mjs";

// Stable PREFIX of the synthetic record review-panel.mjs writes when a lens hits
// an API/quota outage (its `summaryText`: "Review could not run — Claude API/quota
// error…"). Used ONLY for the legacy fallback below — see isInfraRecord.
export const INFRA_SENTINEL = "Review could not run — Claude API/quota error";

/**
 * A carried record that is really an infra/quota outage, not a code finding.
 *
 * `infra: true` is the AUTHORITATIVE signal: the producer (review-panel.mjs) sets
 * it on the synthesised record, so — unlike `summary`, which is model output — a
 * model or prompt-injected finding cannot forge it. That flag is the shared
 * producer/consumer discriminator; prefer it for everything written after it
 * existed.
 *
 * The message-prefix branch is a LEGACY fallback only, for records persisted
 * BEFORE the flag existed (e.g. a PR contaminated by a pre-fix 429 round, like
 * #632, whose App-owned check runs cannot be rewritten). Because `summary` is
 * model-controlled, the prefix alone must NOT mark a record infra — otherwise a
 * real finding that merely quotes this text could be silently dropped, or an
 * injected finding could suppress itself from re-check. So the fallback also
 * requires the synthetic record's shape: no `file`. Every genuine finding cites a
 * `file`; the synthetic record carries only `{ severity, summary }`. A real
 * blocker beginning with the legacy text therefore stays a blocker. Remove this
 * branch once no pre-fix-contaminated PRs remain open.
 */
function isInfraRecord(f) {
  if (!f || typeof f !== "object") return false;
  if (f.infra === true) return true;
  const noFile = f.file === undefined || f.file === null || f.file === "";
  return noFile && typeof f.summary === "string" && f.summary.startsWith(INFRA_SENTINEL);
}

/**
 * Turn `{ lensCheckName -> check run }` into a flat, lens-tagged findings array.
 *
 * FAIL DIRECTION, and it is the opposite of most things in this pipeline. Prior
 * findings are an ADDITIVE recall aid: they can only re-raise a blocker, never
 * clear one. So a parse failure must degrade to "carry fewer findings", never to
 * "fail the round" — a thrown error here would turn one lens's malformed JSON
 * into a panel-wide outage, which is strictly worse than losing that lens's
 * carry-forward for a round.
 *
 * Consequently each lens is parsed INDEPENDENTLY: one lens's garbage `output.text`
 * cannot zero out the other four. The inline version this replaces already had
 * that property (a `try` per lens inside the loop); it is restated here because
 * the granularity is a requirement, not an implementation detail, and every
 * `catch` in this module is per-lens or per-commit for the same reason.
 */
export function tagPriorFindings(runsByLens) {
  const out = [];
  const entries = runsByLens instanceof Map ? [...runsByLens] : Object.entries(runsByLens ?? {});
  for (const [name, run] of entries) {
    if (typeof name !== "string") continue;
    const lens = name.replace(/^agent-review-/, "");
    const text = run?.output?.text;
    if (typeof text !== "string" || text === "") continue; // absent ≠ "found nothing"
    let parsed;
    try {
      parsed = JSON.parse(text);
    } catch {
      continue; // this lens only
    }
    if (!Array.isArray(parsed)) continue;
    for (const f of parsed) {
      // Drop the synthesised INFRA record a lens writes when it hits an API/quota
      // outage (e.g. a 429 session limit): the reviewer never ran, so "Review
      // could not run …" is not a finding to re-check, and the verifier (biased to
      // keep) cannot refute it on grounded evidence. `isInfraRecord` keys on the
      // script-set `infra` flag (authoritative), with a shape-guarded legacy
      // prefix fallback for records persisted before the flag existed — never on
      // model text alone, so a genuine finding is never suppressed. The panel
      // workflow already persists none for an infra lens; this is the read-side
      // backstop so the guarantee holds regardless of which producer wrote it.
      if (isInfraRecord(f)) continue;
      // `lens` last: a finding cannot spoof its own origin by carrying a `lens`
      // key, since these come from a previous round's model output.
      if (f && typeof f === "object" && !Array.isArray(f)) out.push({ ...f, lens });
    }
  }
  return out;
}

/**
 * What ONE lens carries into the next round, read from its own `verdict.json`.
 *
 * The cloud never needs this: a round there reads the PREVIOUS round's findings
 * back out of a check run's `output.text`, which is exactly the projection
 * `agent-review-panel.yml` applies inline when it writes that field. A LOCAL
 * round has no check runs — `spec-to-pr.mjs review` writes its verdicts to a
 * directory — so it needs the same projection applied to the same source
 * (`verdict.json` is what the workflow's inline copy reads too).
 *
 * SELECTION ONLY, and that word is the contract. The workflow also trims every
 * field to fit a check run's 60k `output.text` budget; that is TRANSPORT, it has
 * no local equivalent, and copying it here would only lose evidence the local
 * verifier can use. What must not drift is WHICH findings carry, so only that
 * half is mirrored — and `prior-findings.test.mjs` asserts the workflow's inline
 * copy still applies both filters:
 *
 *   - blocking severity only (`normalizeSeverity` is the workflow's `norm`), and
 *   - not demoted to the `backlog` lane.
 *
 * A demoted finding is wrong input for a carry-forward for the reason the
 * workflow states at length: it is nobody's to fix, its recorded line has since
 * been rewritten, and it can never shrink round over round — so carrying it
 * would make the loop unable to converge.
 *
 * `isInfraRecord` is applied for the same reason `tagPriorFindings` applies it:
 * a lens that hit a quota outage never reviewed, and "the review could not run"
 * is not a finding the next round's verifier can refute.
 *
 * Junk in → `[]`. Like everything else on this path, carrying fewer findings is
 * the safe failure.
 */
/** Absent fields stay absent, so a projected finding is byte-comparable to a
 *  hand-written one and the JSON carries no empty keys. */
function dropUndefined(obj) {
  const out = {};
  for (const [k, v] of Object.entries(obj)) if (v !== undefined) out[k] = v;
  return out;
}

export function carryForwardFindings(verdict, lensId) {
  const findings = Array.isArray(verdict?.findings) ? verdict.findings : [];
  return findings
    .filter((f) => f && typeof f === "object" && !Array.isArray(f))
    // `lane` and `severity` are read BEFORE the projection drops them.
    .filter((f) => BLOCKING.has(normalizeSeverity(f.severity)))
    .filter((f) => f.lane !== "backlog")
    // PROJECT, never spread. `verdict.json`'s findings are MODEL OUTPUT with the
    // orchestrator's annotations added, and a spread carries every key a lens
    // chose to write — `infra` included.
    //
    // `line` is here and not in the cloud's list on purpose: the local reporter
    // prints `file:line`, and a line number changes no downstream decision.
    .map((f) => dropUndefined({
      severity: f.severity,
      file: f.file,
      line: f.line,
      summary: f.summary,
      evidence: f.evidence,
      claimType: f.claimType === "absence" ? "absence" : undefined,
      searchedFor: Array.isArray(f.searchedFor) ? f.searchedFor : undefined,
      mergedFrom: Array.isArray(f.mergedFrom) ? f.mergedFrom : undefined,
      adjudication: Number.isInteger(f.adjudication?.upheld) ? { upheld: f.adjudication.upheld } : undefined,
      // `lens` last, mirroring `tagPriorFindings`: the panel filters prior
      // findings by `p.lens === lens.id`, so an untagged one is carried by nobody.
      lens: typeof lensId === "string" && lensId !== "" ? lensId : f.lens,
    }))
    // AFTER the projection, and that ordering is the whole point. `isInfraRecord`
    // treats `infra: true` as authoritative because the PRODUCER sets it — which
    // is true of a check run's text, projected by the workflow, and NOT true of a
    // raw `verdict.json`, where the key sits on model output this function reads
    // directly. Filtering first let a finding write `infra: true` on itself and
    // vanish: dropped from its own lens's report AND from every later round,
    // after gating the one that raised it.
    //
    // The projection has already removed the key, so the flag branch cannot fire
    // on a forged one. The genuine synthesised record still goes, caught by the
    // shape rule underneath it — no file, and the stable sentinel prefix — which
    // is the branch that existed for records written before the flag did.
    .filter((f) => !isInfraRecord(f));
}

/** Lens check-run names from a lenses.json manifest. Junk → []. */
export function lensCheckNames(manifest) {
  return (Array.isArray(manifest) ? manifest : [])
    .filter((l) => l && typeof l.id === "string" && l.id !== "")
    .map((l) => `agent-review-${l.id}`);
}

/**
 * Resolve `--checks a,b` OR `--lenses <manifest>` into check-run names.
 *
 * Shared by the two `@claude fix` scripts so neither has to re-derive
 * `agent-review-<id>` — the naming convention lives in `lensCheckNames` and a
 * second copy of it would drift the day a lens is renamed. `--checks` wins when
 * both are given: the panel workflow already computes an APPLICABILITY-filtered
 * list, which is strictly better than the whole manifest.
 *
 * Returns [] on anything unreadable. Every caller treats an empty list as a usage
 * error and exits 2, so a broken manifest cannot silently become "no lenses to
 * look at" — which for the eligibility gate would read as "no panel ran".
 */
export function resolveCheckNames(args, { read = readFileSync, log = console.error } = {}) {
  const explicit = (typeof args?.checks === "string" ? args.checks : "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  if (explicit.length > 0) return explicit;
  if (!args?.lenses) return [];
  try {
    return lensCheckNames(JSON.parse(read(args.lenses, "utf8")));
  } catch (err) {
    log(`could not read --lenses '${args.lenses}': ${err.message}`);
    return [];
  }
}

// --- CLI --------------------------------------------------------------------

/**
 * Read every prior round's findings for a PR.
 *
 * `api` is injected — the reason this module exists is that inline YAML JavaScript
 * cannot be tested, and a version whose only API path was reachable exclusively
 * through a real `gh` binary would have kept exactly that problem.
 *
 * The two API contracts this depends on (`--slurp` on the object-wrapped
 * check-runs endpoint; the per-run re-fetch because the list response omits
 * `output.text`) live in gh-checks.mjs, stated once and shared with
 * review-scope.mjs. The first draft of this module got both wrong.
 *
 * NEVER throws: worst case it returns []. See `tagPriorFindings` for why the fail
 * direction here is the opposite of the rest of the pipeline.
 */
export function collectPrior({ pr, names, api = gh, log = console.error }) {
  try {
    const runs = allCheckRuns(prCommitsWithCheckRuns(pr, { api, log }));
    return tagPriorFindings(withFullOutput(latestLensRuns(runs, names), { api, log }));
  } catch (err) {
    log(`Could not assemble prior findings (${err.message}); carrying none.`);
    return [];
  }
}

function main() {
  const args = parseArgs(process.argv);
  const pr = args._[0];
  if (!pr || !/^\d+$/.test(pr)) {
    console.error("Usage: node prior-findings.mjs <pr-number> --lenses <lenses.json> [--out <file>]");
    process.exit(2); // usage error is a tooling error, not a review outcome
  }
  // An unreadable manifest is the SAME class of error as a bad PR number: this is
  // a trusted repo file the caller named, not untrusted API data. Exiting 2 keeps
  // a broken invocation distinguishable from a legitimately empty first round —
  // silently writing `[]` here would disable carry-forward on every round of
  // every PR with nothing to notice it.
  let names = [];
  try {
    names = lensCheckNames(JSON.parse(readFileSync(args.lenses, "utf8")));
  } catch (err) {
    console.error(`Could not read --lenses '${args.lenses}': ${err.message}`);
    process.exit(2);
  }
  if (names.length === 0) {
    console.error(`No lens ids in '${args.lenses}' — refusing to pretend there are no prior findings.`);
    process.exit(2);
  }

  const prior = collectPrior({ pr, names });
  const json = JSON.stringify(prior);
  if (args.out) writeFileSync(args.out, json);
  else console.log(json);
  console.error(`prior findings carried into re-check: ${prior.length}`);
}

// Only run as a CLI, so the pure helpers above are importable by tests.
if (process.argv[1] && process.argv[1].endsWith("prior-findings.mjs")) main();
