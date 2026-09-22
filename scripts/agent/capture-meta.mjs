// Make the stage-detail capture SELF-DESCRIBING: one `meta.json` per artifact,
// naming the PR, the commit, the channel and the panel version that produced it.
//
// THE PROBLEM. #641/#664 route a per-lens capture out of both review panels as
// the artifact `review-panel-stage-detail`, and **nothing inside it says which
// pull request it came from.** That is not an oversight in the payload, it is a
// property of how these workflows are triggered: `workflow_run` and
// `issue_comment` both make GitHub execute the DEFAULT BRANCH's copy of the
// workflow, so the run's own metadata reads `head_branch: main` and
// `pull_requests: []`. Measured on all three real captures to date (#665, #666,
// #669) — every one of them. A consumer therefore cannot attribute a capture
// from run metadata, and both producers upload under the SAME artifact name, so
// it cannot tell a gating round from an advisory one either and would record one
// head sha as two reviews. The #664 task doc filed exactly this as the open item
// a collector must settle. It is settled here instead, in the producer, because
// the producing job is the only place that still knows the answer.
//
// WHY A MODULE AND NOT YAML. This subsystem has now paid for that lesson twice.
// #641: "a `&& 'x' || 'y'` default in the workflow would put the inverted logic
// in the one place no test can reach it." #664 existed *because* a YAML-level
// mistake — one missing `include-hidden-files: true` — was untestable and stayed
// silent for five consecutive rounds. So the payload is built by a pure function
// with unit tests, and the workflow contributes only values it already holds.
//
// WHY `panelSha` AND NOT A `config_hash`. Two reviews are comparable only if the
// reviewer was the same, and a config hash is the obvious way to say so — but a
// separate audit of the existing config-hash logic found it omits fields that
// change behaviour, so two judges that decide differently hash identically. A
// raw commit sha of the trusted `main` checkout that RAN the panel is always
// available, always correct, and a config identity can be derived from it later.
// A wrong fingerprint is worse than none: it merges two populations silently.
//
// FAIL DIRECTION — refuse, never approximate. `buildCaptureMeta` throws on any
// field it cannot validate rather than emitting the field as `null`. A
// `meta.json` reading `"pr": null` that still uploaded would be the exact shape
// of failure this pipeline keeps re-learning: a capture that looks collected and
// is not attributable. Because the builder is all-or-nothing, a `meta.json` on
// disk is ALWAYS complete, and "is this capture attributable?" is a question a
// consumer answers by the file's existence rather than by inspecting it.

import { readdirSync, writeFileSync, mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "./gh-checks.mjs";

// Bumped only on a BREAKING change to the field set. A consumer that does not
// recognise the major must skip the capture loudly rather than guess at it —
// mis-parsing writes plausible garbage into the corpus, which no later check
// would notice.
export const CAPTURE_META_SCHEMA = "wafflebase/stage-capture-meta@1";

// The file name is part of the contract: it sits at the ROOT of the artifact,
// beside the per-lens directories, so a consumer reads one well-known entry.
export const CAPTURE_META_FILE = "meta.json";

const SHA40 = /^[0-9a-f]{40}$/;
// A positive integer with no leading zeros, sign, whitespace or decimal point.
// Deliberately stricter than Number(): `pr` and `runId` become S3 KEY SEGMENTS
// downstream, and a value that survives coercion but not this regex ("1e3",
// " 12", "../..") is how a path escapes its prefix.
const POSITIVE_INT = /^[1-9][0-9]*$/;
// Lens directory names, which also become file names inside the artifact.
const LENS_SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
// A workflow FILE name, not a display name — `agent-review-panel.yml`. The
// display name ("Agent Review Panel") is editable prose; the file name is what a
// consumer can join against `.github/workflows/`.
const WORKFLOW_FILE = /^[A-Za-z0-9._-]+\.ya?ml$/;
// GitHub event names are lowercase with underscores: `workflow_run`,
// `issue_comment`, `workflow_dispatch`.
const EVENT_NAME = /^[a-z][a-z_]*$/;
// UTC only, with the `Z`. An offset-bearing timestamp compares wrong
// lexicographically, and this codebase has already been bitten by that once.
const ISO_UTC = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{3})?Z$/;

export const CAPTURE_CHANNELS = ["gating", "advisory"];

/** Every refusal carries the field name and the offending value, so the log line names the fix. */
function refuse(field, value, expected) {
  const shown = typeof value === "string" ? JSON.stringify(value.slice(0, 80)) : JSON.stringify(value);
  throw new Error(`capture meta: ${field} must be ${expected}, got ${shown}`);
}

function requireMatch(field, value, re, expected) {
  if (typeof value !== "string" || !re.test(value)) refuse(field, value, expected);
  return value;
}

/** A count that arrives as a string from the environment, kept as a NUMBER in the payload. */
function requireCount(field, value) {
  const s = typeof value === "number" && Number.isSafeInteger(value) ? String(value) : value;
  requireMatch(field, s, POSITIVE_INT, "a positive integer");
  const n = Number(s);
  // The regex admits ANY run of digits, and `Number` then quietly rounds one
  // that will not fit: a 20-digit run id becomes …567000 and a 30-digit one
  // becomes 1e+30. Either would be emitted as a JSON number that a consumer
  // renders into a key segment naming a DIFFERENT run — a wrong attribution
  // that looks well-formed, which is the one outcome this module exists to
  // make impossible. Refuse instead; GitHub's real ids are far inside this range.
  if (!Number.isSafeInteger(n)) refuse(field, s, "a positive integer below 2^53");
  return n;
}

/**
 * The producer facts that identify one capture. PURE — no clock, no filesystem,
 * no environment; `capturedAt` and `lenses` are injected by the caller so the
 * whole payload is a function of its arguments and a test needs neither.
 *
 * Throws on ANY field it cannot validate. See the header: a partial meta.json is
 * worse than none, because absence is visible and a null field is not.
 */
export function buildCaptureMeta({
  pr,
  headSha,
  baseSha,
  channel,
  workflow,
  runId,
  runAttempt,
  event,
  panelSha,
  lenses,
  capturedAt,
}) {
  if (!CAPTURE_CHANNELS.includes(channel)) refuse("channel", channel, `one of ${CAPTURE_CHANNELS.join(" | ")}`);

  // Non-empty on purpose, and it is the load-bearing half of a two-part rule:
  // the CLI below does not write a meta.json when no lens captured anything.
  // Together they make `meta.json` present ⟺ a real capture, which keeps the
  // "every lens skipped" case — normal, and the reason the upload step carries
  // `if-no-files-found: ignore` — producing NO artifact rather than an artifact
  // holding attribution for nothing. A collector would have to treat the latter
  // as "present but nothing valid inside", which its own spec makes loud.
  if (!Array.isArray(lenses) || lenses.length === 0) refuse("lenses", lenses, "a non-empty array of lens slugs");
  for (const lens of lenses) requireMatch("lenses[]", lens, LENS_SLUG, "a lowercase kebab-case slug");
  // Copy before sorting: the caller's array must come back untouched, and the
  // panel's own purity discipline (#641) is not worth breaking for a sort.
  const sortedLenses = [...new Set(lenses)].sort();

  const at = capturedAt instanceof Date ? capturedAt.toISOString() : capturedAt;
  requireMatch("capturedAt", at, ISO_UTC, "an ISO 8601 UTC timestamp ending in Z");

  // `null`, not omitted, and not "". The diff base is legitimately unknown when
  // the diff step never ran, and a consumer must be able to tell "we did not
  // record it" from "we recorded the empty string" without a second rule. An
  // absent KEY would additionally be indistinguishable from a pre-schema file.
  const base = baseSha === undefined || baseSha === null || baseSha === "" ? null : requireMatch("baseSha", baseSha, SHA40, "40 lowercase hex characters or null");

  // Insertion order IS the on-disk field order — schema first so a reader can
  // dispatch on it before parsing anything else.
  return {
    schema: CAPTURE_META_SCHEMA,
    pr: requireCount("pr", pr),
    headSha: requireMatch("headSha", headSha, SHA40, "40 lowercase hex characters"),
    baseSha: base,
    channel,
    workflow: requireMatch("workflow", workflow, WORKFLOW_FILE, "a workflow file name ending in .yml"),
    runId: requireCount("runId", runId),
    runAttempt: requireCount("runAttempt", runAttempt),
    event: requireMatch("event", event, EVENT_NAME, "a GitHub event name"),
    panelSha: requireMatch("panelSha", panelSha, SHA40, "40 lowercase hex characters"),
    lenses: sortedLenses,
    capturedAt: at,
  };
}

/**
 * Which lenses actually left a capture in `outDir` — the injected side effect.
 *
 * Read off the DIRECTORY rather than from the panel's lens manifest, because the
 * question this answers is "what is about to be uploaded", not "what was
 * planned". A lens that skipped, crashed or was not applicable writes no
 * `stage-detail.json`, and listing it here would tell a collector to expect a
 * file that never existed.
 *
 * A missing `outDir` is NOT an error: capture disabled, or every lens skipped,
 * is the same normal case `if-no-files-found: ignore` exists for. Any other
 * readdir failure propagates — an unreadable capture directory is a real fault
 * and must not be laundered into "nothing was captured".
 */
export function capturedLenses(outDir) {
  let entries;
  try {
    entries = readdirSync(outDir, { withFileTypes: true });
  } catch (e) {
    if (e && e.code === "ENOENT") return [];
    throw e;
  }
  return entries
    .filter((e) => e.isDirectory())
    .map((e) => e.name)
    .filter((name) => {
      try {
        // `isFile()`, not merely "an entry with that name": upload-artifact
        // drops directories from its search results, so a `stage-detail.json`
        // that is not a file would be listed here and uploaded nowhere — the
        // one way this function could claim more than the artifact carries.
        return readdirSync(path.join(outDir, name), { withFileTypes: true })
          .some((f) => f.name === "stage-detail.json" && f.isFile());
      } catch {
        // One unreadable lens directory must not hide its four healthy
        // siblings; it simply is not listed.
        return false;
      }
    })
    .sort();
}

/**
 * Usage:
 *   node capture-meta.mjs --out .agent-review/meta.json
 *     --pr <n> --head-sha <sha> [--base-sha <sha>] --channel gating|advisory
 *     --workflow <file.yml> --run-id <n> --run-attempt <n> --event <name>
 *     --panel-sha <sha>
 *
 * The directory scanned for lens captures is `dirname(--out)`, not a second
 * flag. The meta file describes the capture it SITS IN, and coupling the two
 * removes the way they could be pointed at different directories — which is
 * also what makes the artifact layout come out as `meta.json` beside
 * `<lens>/stage-detail.json`.
 *
 * Exit codes:
 *   0  meta.json written, or nothing to describe (no lens capture — normal)
 *   1  refused: a field could not be validated, or the write failed
 *
 * Exit 1 is deliberately paired with `continue-on-error: true` at the call site:
 * a capture problem must never fail a code review, but it must not pass for
 * healthy either. The `::error::` below is a run-level ANNOTATION rather than a
 * log line, so it is visible on the run summary without opening the job — the
 * distinction that let "No files were found" hide for five rounds. Enforcement
 * proper belongs to the collector, which exits non-zero on a capture that has
 * no meta.json.
 */
function main(argv, now = new Date()) {
  const a = parseArgs(argv);
  const out = a.out;
  if (!out) {
    console.error("::error::capture meta: --out is required");
    return 1;
  }

  // `capturedLenses` rethrows anything that is not a missing directory, on
  // purpose — an unreadable capture directory is a real fault. It still has to
  // arrive as this step's own named annotation: an uncaught throw prints a
  // readdir stack trace with no `::error::`, so the run summary stays clean
  // while the artifact goes out unattributable. Same reason the workflow runs
  // `git … || true` rather than letting `set -e` kill the step.
  let lenses;
  try {
    lenses = capturedLenses(path.dirname(out));
  } catch (e) {
    console.error(`::error::capture meta: could not read the capture directory ${path.dirname(out)}: ${e.message} — this capture will have no meta.json and a collector will discard it as unattributable`);
    return 1;
  }
  if (lenses.length === 0) {
    // Not an error, and said out loud anyway: this is indistinguishable from a
    // broken capture if nobody prints it, and "the artifact was legitimately
    // empty" is a claim a reader should be able to check.
    console.log(`capture meta: no per-lens capture under ${path.dirname(out)}; not writing ${path.basename(out)}`);
    return 0;
  }

  let meta;
  try {
    meta = buildCaptureMeta({
      pr: a.pr,
      headSha: a["head-sha"],
      baseSha: a["base-sha"],
      channel: a.channel,
      workflow: a.workflow,
      runId: a["run-id"],
      runAttempt: a["run-attempt"],
      event: a.event,
      panelSha: a["panel-sha"],
      lenses,
      capturedAt: now,
    });
  } catch (e) {
    console.error(`::error::${e.message} — this capture will have no meta.json and a collector will discard it as unattributable`);
    return 1;
  }

  try {
    mkdirSync(path.dirname(out), { recursive: true });
    writeFileSync(out, `${JSON.stringify(meta, null, 2)}\n`);
  } catch (e) {
    console.error(`::error::capture meta: could not write ${out}: ${e.message}`);
    return 1;
  }
  console.log(`capture meta: ${out} — pr ${meta.pr}, ${meta.channel}, ${meta.headSha.slice(0, 8)}, panel ${meta.panelSha.slice(0, 8)}, ${meta.lenses.length} lens(es): ${meta.lenses.join(", ")}`);
  return 0;
}

// Only when executed directly — same guard as review-panel.mjs, so importing
// this module for tests never writes a file.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv));
}
