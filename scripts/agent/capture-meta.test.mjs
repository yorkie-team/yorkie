import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildCaptureMeta, capturedLenses, CAPTURE_META_SCHEMA } from "./capture-meta.mjs";

const CLI = fileURLToPath(new URL("./capture-meta.mjs", import.meta.url));

const HEAD = "c18b6abbd7df4247c865fe12fc0f0d3530c294d6";
const BASE = "268fb507b1e4a9b0c3d2e5f60718293a4b5c6d7e";
const PANEL = "5ee400b4210b10ffe66765f66de7df51a6e7dbf0";

/** A complete, valid gating capture. Every test below varies exactly one field of it. */
const GATING = {
  pr: 669,
  headSha: HEAD,
  baseSha: BASE,
  channel: "gating",
  workflow: "agent-review-panel.yml",
  runId: 30889625585,
  runAttempt: 1,
  event: "workflow_run",
  panelSha: PANEL,
  lenses: ["correctness", "security"],
  capturedAt: "2026-08-04T07:53:22Z",
};

const tmp = () => mkdtempSync(path.join(tmpdir(), "capture-meta-"));

/** A `.agent-review`-shaped directory: one subdirectory per lens that captured. */
function captureDir(lenses, { extraFiles = {} } = {}) {
  const dir = path.join(tmp(), ".agent-review");
  mkdirSync(dir, { recursive: true });
  for (const lens of lenses) {
    mkdirSync(path.join(dir, lens), { recursive: true });
    writeFileSync(path.join(dir, lens, "stage-detail.json"), '{"samples":[]}\n');
    // The panel writes these beside it; they must not be mistaken for a capture.
    writeFileSync(path.join(dir, lens, "verdict.json"), '{"ok":true}\n');
    writeFileSync(path.join(dir, lens, "summary.md"), "# summary\n");
  }
  for (const [name, body] of Object.entries(extraFiles)) writeFileSync(path.join(dir, name), body);
  return dir;
}

test("buildCaptureMeta: the exact payload a collector will read, field for field", () => {
  // deepEqual, not a field-by-field spot check: the whole point of this file is
  // that a consumer can be written against a fixed shape, so an ADDED field is a
  // schema change and has to be a deliberate one.
  assert.deepEqual(buildCaptureMeta(GATING), {
    schema: "wafflebase/stage-capture-meta@1",
    pr: 669,
    headSha: HEAD,
    baseSha: BASE,
    channel: "gating",
    workflow: "agent-review-panel.yml",
    runId: 30889625585,
    runAttempt: 1,
    event: "workflow_run",
    panelSha: PANEL,
    lenses: ["correctness", "security"],
    capturedAt: "2026-08-04T07:53:22Z",
  });
  assert.equal(CAPTURE_META_SCHEMA, "wafflebase/stage-capture-meta@1");
});

test("buildCaptureMeta: NO config_hash, and no field a consumer must guess at", () => {
  // Deliberately excluded — see the module header. An audit of the existing
  // config-hash logic found it omits fields that change behaviour, so two judges
  // that decide differently hash identically; `panelSha` is the honest identity.
  // Pinned as a key-set assertion because the failure mode is someone ADDING it
  // back as an obvious improvement.
  const keys = Object.keys(buildCaptureMeta(GATING));
  assert.ok(!keys.includes("config_hash"), `config_hash is deliberately absent, found: ${keys.join(", ")}`);
  assert.ok(!keys.includes("configHash"), `configHash is deliberately absent, found: ${keys.join(", ")}`);
  // `schema` first, so a reader can dispatch on the version before parsing.
  assert.equal(keys[0], "schema");
});

test("buildCaptureMeta: channel is the ONLY thing separating a gating round from an advisory one", () => {
  // Both producers upload the same capture, from the same writer, for the same
  // commit. Without this field one head sha reviewed twice reads as two gating
  // rounds — and nothing else in the payload differs.
  const advisory = buildCaptureMeta({
    ...GATING,
    channel: "advisory",
    workflow: "agent-review-on-demand.yml",
    event: "issue_comment",
  });
  assert.equal(advisory.channel, "advisory");
  assert.equal(advisory.workflow, "agent-review-on-demand.yml");
  assert.equal(advisory.event, "issue_comment");
  // Same PR, same commit, same panel version — the two payloads are otherwise
  // identical, which is exactly why `channel` has to carry the distinction.
  const gating = buildCaptureMeta(GATING);
  for (const k of ["pr", "headSha", "baseSha", "panelSha", "runId", "capturedAt"]) {
    assert.deepEqual(advisory[k], gating[k], `${k} must not differ between channels`);
  }
});

test("buildCaptureMeta: an absent diff base is null — present, and not the empty string", () => {
  // `null`, not omitted: an absent KEY is indistinguishable from a file written
  // before the field existed. `""` would be a value a consumer might take
  // literally. The diff base is genuinely unknown when the diff step never ran.
  for (const absent of [undefined, null, ""]) {
    const meta = buildCaptureMeta({ ...GATING, baseSha: absent });
    assert.ok("baseSha" in meta, `baseSha must be PRESENT for input ${JSON.stringify(absent)}`);
    assert.equal(meta.baseSha, null);
  }
  // A base sha that is present but malformed is a refusal, not a null: the
  // difference between "we did not record it" and "we recorded nonsense".
  assert.throws(() => buildCaptureMeta({ ...GATING, baseSha: "268fb50" }), /baseSha must be 40 lowercase hex/);
});

test("buildCaptureMeta: every field refuses rather than degrading, and the message names it", () => {
  // The failure this whole module exists to prevent is a meta.json that reads
  // `"pr": null` and uploads anyway. There is no partial payload: the builder
  // either returns a complete object or throws.
  const cases = [
    ["pr", 0], ["pr", -1], ["pr", "12a"], ["pr", "1e3"], ["pr", " 12"], ["pr", "01"],
    ["pr", "../../etc"], ["pr", 1.5], ["pr", null], ["pr", undefined],
    ["headSha", "C18B6ABBD7DF4247C865FE12FC0F0D3530C294D6"], // upper case is not the sha git prints
    ["headSha", HEAD.slice(0, 39)], ["headSha", `${HEAD}0`], ["headSha", ""], ["headSha", null],
    ["channel", "GATING"], ["channel", "gate"], ["channel", ""], ["channel", undefined],
    ["workflow", "agent-review-panel"], ["workflow", "Agent Review Panel"], ["workflow", "../x.yml"], ["workflow", ""],
    ["runId", "0"], ["runId", "abc"], ["runId", ""],
    // Digits all the way down, and `Number` rounds them off: 12345678901234567890
    // parses to …567000 and a 30-digit run to 1e+30. Either would be a JSON
    // number naming a DIFFERENT run, well-formed and wrong.
    ["runId", "12345678901234567890"], ["runId", "9".repeat(30)],
    ["pr", "12345678901234567890"],
    ["runAttempt", "0"], ["runAttempt", "-1"], ["runAttempt", undefined],
    ["event", "workflow-run"], ["event", "Workflow_Run"], ["event", ""],
    ["panelSha", "main"], ["panelSha", ""],
    ["capturedAt", "2026-08-04T07:53:22+09:00"], // an offset compares wrong lexicographically
    ["capturedAt", "2026-08-04 07:53:22Z"], ["capturedAt", "2026-08-04"], ["capturedAt", 1754294002000],
  ];
  for (const [field, value] of cases) {
    assert.throws(
      () => buildCaptureMeta({ ...GATING, [field]: value }),
      (e) => e.message.includes(`capture meta: ${field}`),
      `${field}=${JSON.stringify(value)} must be refused with a message naming ${field}`,
    );
  }
});

test("buildCaptureMeta: lenses must be a non-empty list of slugs, sorted and deduped", () => {
  // Non-empty is half of the rule that makes `meta.json` present ⟺ a real
  // capture; the CLI supplies the other half by not writing one when no lens
  // captured. Slugs because these become file names in the artifact and path
  // segments in whatever stores it.
  for (const bad of [[], undefined, null, "correctness", [""], ["Correctness"], ["../evil"], ["a b"], [null]]) {
    assert.throws(() => buildCaptureMeta({ ...GATING, lenses: bad }), /capture meta: lenses/, `lenses=${JSON.stringify(bad)} must be refused`);
  }
  const meta = buildCaptureMeta({ ...GATING, lenses: ["security", "correctness", "security", "test-adequacy"] });
  assert.deepEqual(meta.lenses, ["correctness", "security", "test-adequacy"]);
});

test("buildCaptureMeta: derives only — it never mutates its inputs", () => {
  // Same property #641 pinned on `buildStageDetail`, for the same reason: this
  // runs beside a review, and instrumentation that edits its caller's arrays is
  // how instrumentation changes a verdict.
  const input = { ...GATING, lenses: ["security", "correctness"] };
  const before = JSON.stringify(input);
  buildCaptureMeta(input);
  assert.equal(JSON.stringify(input), before);
});

test("buildCaptureMeta: accepts a Date and env-shaped strings, emits numbers and a Z timestamp", () => {
  // Everything the workflow passes arrives as a STRING. The payload must still
  // hold `pr`/`runId`/`runAttempt` as JSON numbers, or a consumer comparing
  // against run metadata compares 669 with "669".
  const meta = buildCaptureMeta({
    ...GATING,
    pr: "669",
    runId: "30889625585",
    runAttempt: "2",
    capturedAt: new Date(Date.UTC(2026, 7, 4, 7, 53, 22)),
  });
  assert.equal(meta.pr, 669);
  assert.equal(meta.runId, 30889625585);
  assert.equal(meta.runAttempt, 2);
  assert.equal(meta.capturedAt, "2026-08-04T07:53:22.000Z");
  assert.equal(typeof meta.pr, "number");
  // Round-trips: the file on disk is exactly the object this returned.
  assert.deepEqual(JSON.parse(JSON.stringify(meta)), meta);
});

test("capturedLenses: reads what will be UPLOADED, not what was planned", () => {
  const dir = captureDir(["security", "correctness"], { extraFiles: { "review-timing.json": "{}\n" } });
  // A lens directory with no stage-detail.json — skipped, crashed, or not
  // applicable. Listing it would tell a collector to expect a file that never
  // existed.
  mkdirSync(path.join(dir, "design-fit"), { recursive: true });
  writeFileSync(path.join(dir, "design-fit", "verdict.json"), '{"ok":true}\n');
  // A `stage-detail.json` that is not a FILE. upload-artifact drops directories
  // from its search results, so listing this lens would claim a capture the
  // artifact does not carry — the one direction in which this function could
  // over-report.
  mkdirSync(path.join(dir, "blast-radius", "stage-detail.json"), { recursive: true });
  assert.deepEqual(capturedLenses(dir), ["correctness", "security"]);
});

test("capturedLenses: a missing capture directory is empty, not an error", () => {
  // Capture disabled, or every lens skipped — the same normal case the upload
  // step's `if-no-files-found: ignore` exists for.
  assert.deepEqual(capturedLenses(path.join(tmp(), "never-created")), []);
});

test("CLI: writes meta.json beside the lens directories, and says what it wrote", () => {
  const dir = captureDir(["correctness", "security"]);
  const out = path.join(dir, "meta.json");
  const stdout = execFileSync("node", [CLI,
    "--out", out,
    "--pr", "669", "--head-sha", HEAD, "--base-sha", BASE,
    "--channel", "gating", "--workflow", "agent-review-panel.yml",
    "--run-id", "30889625585", "--run-attempt", "1",
    "--event", "workflow_run", "--panel-sha", PANEL,
  ], { encoding: "utf8" });

  const meta = JSON.parse(readFileSync(out, "utf8"));
  assert.equal(meta.schema, CAPTURE_META_SCHEMA);
  assert.equal(meta.pr, 669);
  assert.equal(meta.channel, "gating");
  assert.deepEqual(meta.lenses, ["correctness", "security"]);
  // Discovered from disk, never passed in — the one field the caller cannot get wrong.
  assert.ok(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{3})?Z$/.test(meta.capturedAt), meta.capturedAt);
  // A silent success is the failure mode this subsystem keeps hitting.
  assert.match(stdout, /pr 669, gating/);
});

test("CLI: no lens capture → no meta.json, exit 0, and it SAYS so", () => {
  // The artifact is then empty and never uploaded, which is normal. Writing
  // attribution for a capture that does not exist would make an empty round look
  // like "artifact present but nothing valid inside" — loud, and wrong.
  const dir = path.join(tmp(), ".agent-review");
  mkdirSync(dir, { recursive: true });
  const out = path.join(dir, "meta.json");
  const stdout = execFileSync("node", [CLI, "--out", out, "--pr", "669", "--head-sha", HEAD,
    "--channel", "gating", "--workflow", "agent-review-panel.yml", "--run-id", "1",
    "--run-attempt", "1", "--event", "workflow_run", "--panel-sha", PANEL], { encoding: "utf8" });
  assert.equal(existsSync(out), false, "no capture must mean no meta.json");
  assert.match(stdout, /no per-lens capture/);
});

test("CLI: an unattributable capture writes NOTHING and exits 1 with an annotation", () => {
  // The whole failure this PR exists to prevent, exercised end to end: an unknown
  // PR number must not become `"pr": null` on disk. `continue-on-error: true` at
  // the call site keeps this off the review's critical path; `::error::` makes it
  // an annotation on the run summary rather than a log line nobody opens.
  const dir = captureDir(["correctness"]);
  const out = path.join(dir, "meta.json");
  let status = 0, stderr = "";
  try {
    execFileSync("node", [CLI, "--out", out, "--pr", "", "--head-sha", HEAD,
      "--channel", "gating", "--workflow", "agent-review-panel.yml", "--run-id", "1",
      "--run-attempt", "1", "--event", "workflow_run", "--panel-sha", PANEL],
      { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  } catch (e) {
    status = e.status;
    stderr = String(e.stderr);
  }
  assert.equal(status, 1, "a refused capture must exit non-zero");
  assert.equal(existsSync(out), false, "a refused capture must leave NO meta.json behind");
  assert.match(stderr, /^::error::capture meta: pr must be a positive integer/m);
  assert.match(stderr, /unattributable/);
});

test("CLI: an unreadable capture directory is an ANNOTATION, never a stack trace", () => {
  // `capturedLenses` rethrows anything that is not a missing directory, so the
  // CLI has to catch it: an uncaught readdir error exits non-zero with a stack
  // trace and NO `::error::`, which leaves the run summary clean while the
  // artifact goes out unattributable. A real failure shape — `--out` under a
  // path that is a regular file — not a mock.
  const dir = tmp();
  const notADir = path.join(dir, "occupied");
  writeFileSync(notADir, "i am a file\n");
  let status = 0, stderr = "";
  try {
    execFileSync("node", [CLI, "--out", path.join(notADir, "meta.json"), "--pr", "669", "--head-sha", HEAD,
      "--channel", "gating", "--workflow", "agent-review-panel.yml", "--run-id", "1",
      "--run-attempt", "1", "--event", "workflow_run", "--panel-sha", PANEL],
      { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  } catch (e) {
    status = e.status;
    stderr = String(e.stderr);
  }
  assert.equal(status, 1);
  assert.match(stderr, /^::error::capture meta: could not read the capture directory/m);
  assert.doesNotMatch(stderr, /at capturedLenses|node:fs/, "a stack trace is not an annotation");
});

test("CLI: a write it cannot perform is refused loudly, not swallowed", () => {
  // Distinct from the validation path: the payload was fine and the file still
  // did not land. A collector must be able to tell "no capture" from "capture we
  // failed to describe", and only the exit code carries that here.
  //
  // A real failure shape rather than a mock or a chmod — `meta.json` already
  // exists as a DIRECTORY, so mkdirSync succeeds and writeFileSync raises EISDIR.
  // chmod would be a no-op for a test process running as root.
  const dir = captureDir(["correctness"]);
  const out = path.join(dir, "meta.json");
  mkdirSync(out);
  let status = 0, stderr = "";
  try {
    execFileSync("node", [CLI, "--out", out, "--pr", "669", "--head-sha", HEAD,
      "--channel", "gating", "--workflow", "agent-review-panel.yml", "--run-id", "1",
      "--run-attempt", "1", "--event", "workflow_run", "--panel-sha", PANEL],
      { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  } catch (e) {
    status = e.status;
    stderr = String(e.stderr);
  }
  assert.equal(status, 1);
  assert.match(stderr, /^::error::capture meta: could not write/m);
});
