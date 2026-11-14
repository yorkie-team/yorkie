// The re-run arms of `@claude rerun` and `@claude loop`, EXTRACTED FROM THE
// WORKFLOW AND RUN.
//
// checks.test.mjs pins the two run SELECTIONS against checks.mjs. That is not
// enough on its own: #1047 and #1052 stalled because the step around a correct
// selection stopped when the selection was empty, reported "the panel will
// engage on the next CI run", and there was no next CI run. What follows drives
// the whole step against a stubbed github-script context, one scenario per arm,
// so a future edit that re-introduces a silent no-op fails here rather than on a
// PR nobody notices has stopped.
//
// The step is read out of the YAML rather than re-typed, for the reason the
// mirror test already gives: a hand copy can only prove this file agrees with
// itself.
import "./fixtures/use-fixture-config.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { PAGE_AUTHOR_LOGINS } from "./rounds.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));

/**
 * The `script: |` body of the step with `id: <stepId>`, dedented.
 *
 * Hand-rolled rather than a YAML parse: this repo has no YAML dependency at the
 * root, and a block scalar is exactly "every following line indented deeper than
 * the key". Terminates on the first non-blank line that is not, which is what a
 * naive `indexOf('- name:')` got wrong — a comment line above the next step ends
 * the block too.
 */
function stepScript(file, stepId) {
  const yml = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", file), "utf8");
  const lines = yml.split("\n");
  const idAt = lines.findIndex((l) => l.trim() === `id: ${stepId}`);
  assert.notEqual(idAt, -1, `${file}: no step with id: ${stepId}`);
  const scriptAt = lines.findIndex((l, i) => i > idAt && l.trim() === "script: |");
  assert.notEqual(scriptAt, -1, `${file}: step ${stepId} has no script block`);
  const body = [];
  let indent = null;
  for (const line of lines.slice(scriptAt + 1)) {
    if (line.trim() === "") { body.push(""); continue; }
    const width = line.length - line.trimStart().length;
    if (indent === null) indent = width;
    if (width < indent) break;
    body.push(line.slice(indent));
  }
  assert.ok(body.length > 10, `${file}: extracted an implausibly short script for ${stepId}`);
  return body.join("\n");
}

/** What the steps' `env:` blocks hand in: the placeholder and `repo.botLogins`. */
const STEP_ENV = { PLACEHOLDER_ID: "42", BOT_LOGINS: JSON.stringify(PAGE_AUTHOR_LOGINS) };

const CI = (id, status, conclusion) => ({ id, status, conclusion, html_url: `https://example/${id}` });
const HEAD = "sha-head";

/** Run an extracted step against stubs, with a deterministic clock. */
async function drive(script, { runs, poll = [], headSha = HEAD, freshSha, pollErrors = 0 }) {
  const calls = { reRun: [], edits: [], polls: 0 };
  let pollIdx = 0;
  let errorsLeft = pollErrors;
  let prGets = 0;
  const github = {
    paginate: async (fn, args) => fn(args),
    rest: {
      pulls: {
        // First call is the step's own `pr`; any later call is the post-wait
        // freshness check.
        get: async () => ({
          data: {
            head: { sha: prGets++ === 0 ? headSha : (freshSha ?? headSha), ref: "agent/x", repo: { full_name: "o/r" } },
            labels: [],
          },
        }),
      },
      issues: {
        listComments: async () => [],
        deleteComment: async () => {},
        removeLabel: async () => {},
        getLabel: async () => ({}),
        addLabels: async () => {},
        updateComment: async ({ body }) => calls.edits.push(body),
      },
      actions: {
        listWorkflowRuns: async () => runs,
        getWorkflowRun: async () => {
          calls.polls++;
          if (errorsLeft-- > 0) throw new Error("502 upstream");
          return { data: poll[Math.min(pollIdx++, poll.length - 1)] };
        },
        reRunWorkflow: async ({ run_id }) => calls.reRun.push(run_id),
      },
    },
  };
  const outputs = {};
  const core = { setOutput: (k, v) => { outputs[k] = v; }, warning: () => {}, notice: () => {} };
  const context = { repo: { owner: "o", repo: "r" }, payload: { issue: { number: 1 } } };

  // Every Date.now() advances 20s and setTimeout is instant, so the 30-minute
  // wait resolves in ~90 synchronous iterations instead of half an hour.
  const realNow = Date.now;
  const realTimeout = globalThis.setTimeout;
  let clock = 1_000_000;
  Date.now = () => (clock += 20_000);
  globalThis.setTimeout = (cb) => { cb(); return 0; };
  // The step's `env:`, as the workflow hands it in. A variable the script
  // reads and this table does not supply fails here by name, not as a
  // JSON.parse of `undefined` deep in the step.
  for (const [, name] of script.matchAll(/process\.env\.([A-Z_][A-Z0-9_]*)/g)) {
    assert.ok(Object.hasOwn(STEP_ENV, name), `the step reads ${name}, which this test does not supply`);
  }
  const saved = Object.fromEntries(Object.keys(STEP_ENV).map((k) => [k, process.env[k]]));
  Object.assign(process.env, STEP_ENV);
  try {
    await new Function("github", "context", "core", "process", `return (async () => {\n${script}\n})()`)(
      github, context, core, process,
    );
  } finally {
    Date.now = realNow;
    globalThis.setTimeout = realTimeout;
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
  return { outputs, calls };
}

for (const [file, stepId, verb] of [
  ["agent-rerun.yml", "rerun", "@claude rerun"],
  ["agent-loop.yml", "enable", "@claude loop"],
]) {
  test(`${verb}: a completed CI run is re-run (unchanged)`, async () => {
    const script = stepScript(file, stepId);
    const { outputs, calls } = await drive(script, { runs: [CI(5, "completed", "success")] });
    assert.deepEqual(calls.reRun, [5]);
    assert.match(outputs.outcome, /Re-running CI now/);
    assert.equal(calls.polls, 0, "a finished run is not polled");
  });

  test(`${verb}: waits for an in-flight run, then re-runs it`, async () => {
    // The #1047 / #1052 case. Nothing completed, so the old step stopped here
    // and said the panel would engage on the next CI run — which never came,
    // because this round's `requested` event was already spent and the panel
    // refuses a `completed` event on attempt 1.
    const script = stepScript(file, stepId);
    const { outputs, calls } = await drive(script, {
      runs: [CI(9, "in_progress", null)],
      poll: [CI(9, "in_progress", null), CI(9, "completed", "success")],
    });
    assert.deepEqual(calls.reRun, [9], "the awaited run is the one re-run");
    assert.match(outputs.outcome, /Waited for the in-flight CI run/);
    assert.ok(
      calls.edits.some((b) => b.includes("still in flight")),
      "the operator is told what the silence is, not left on 'Working on it…'",
    );
  });

  test(`${verb}: a transient read error does not end the wait`, async () => {
    // ~90 API calls over half an hour will meet a 5xx eventually. Letting one
    // throw out of the poll would abandon the round in exactly the state this
    // change exists to rescue it from.
    const script = stepScript(file, stepId);
    const { outputs, calls } = await drive(script, {
      runs: [CI(9, "in_progress", null)],
      poll: [CI(9, "completed", "success")],
      pollErrors: 3,
    });
    assert.deepEqual(calls.reRun, [9]);
    assert.match(outputs.outcome, /Waited for the in-flight CI run/);
  });

  test(`${verb}: leaves a red run to the CI-fix arm`, async () => {
    // agent-iterate-ci.yml has no attempt gate, so it fired on this completion
    // and its fixer may be pushing. Its concurrency group is cancel-in-progress
    // keyed on the branch, so a second completion here cancels it mid-push.
    const script = stepScript(file, stepId);
    const { outputs, calls } = await drive(script, {
      runs: [CI(9, "queued", null)],
      poll: [CI(9, "completed", "failure")],
    });
    assert.deepEqual(calls.reRun, [], "re-running would cancel the fixer mid-push");
    assert.match(outputs.outcome, /CI-fix arm engaged/);
  });

  test(`${verb}: does not re-run CI for a sha that is no longer the head`, async () => {
    const script = stepScript(file, stepId);
    const { outputs, calls } = await drive(script, {
      runs: [CI(9, "in_progress", null)],
      poll: [CI(9, "completed", "success")],
      freshSha: "sha-new",
    });
    assert.deepEqual(calls.reRun, []);
    assert.match(outputs.outcome, /A new commit landed/);
  });

  test(`${verb}: says so when the wait budget runs out`, async () => {
    const script = stepScript(file, stepId);
    const { outputs, calls } = await drive(script, {
      runs: [CI(9, "in_progress", null)],
      poll: [CI(9, "in_progress", null)],
    });
    assert.deepEqual(calls.reRun, []);
    assert.match(outputs.outcome, /still running after 30 minutes/);
    assert.match(outputs.outcome, /@claude rerun/, "and names the way to retry");
    assert.ok(calls.polls > 1, "it actually polled");
  });

  test(`${verb}: no CI run for this head re-runs nothing`, async () => {
    const script = stepScript(file, stepId);
    const { outputs, calls } = await drive(script, { runs: [] });
    assert.deepEqual(calls.reRun, []);
    assert.match(outputs.outcome, /next CI run/);
  });
}

test("@claude rerun: the latch summary and `reran` survive every arm", async () => {
  const script = stepScript("agent-rerun.yml", "rerun");
  const green = await drive(script, { runs: [CI(5, "completed", "success")] });
  assert.equal(green.outputs.engaged, "true");
  assert.equal(green.outputs.reran, "true");
  assert.match(green.outputs.outcome, /^🔁 Rerun engaged — cleared \d+ paged marker\(s\)\./);

  // `reran` is what the loop-status note reads; before it existed the note said
  // "CI re-running" on an arm that re-ran nothing.
  const none = await drive(script, { runs: [] });
  assert.equal(none.outputs.reran, "false");
  assert.equal(none.outputs.engaged, "true");
});
