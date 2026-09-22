import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chooseSlot, capacityLine } from "./pick-credential.mjs";
import { createTokenPool, MAX_SLOTS, TOKEN_ENV } from "./token-pool.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const WORKFLOWS = path.join(HERE, "../../.github/workflows");
const pool = (env) => createTokenPool({ env });

// --- chooseSlot ---------------------------------------------------------------

test("chooseSlot: names the run-id-derived slot, and different runs differ", () => {
  // The whole point: distribution ACROSS jobs. Two jobs never coordinate, they just
  // land on different accounts because their run ids differ.
  const env = (runId) => ({
    GITHUB_RUN_ID: runId,
    [TOKEN_ENV]: "t0",
    [`${TOKEN_ENV}_1`]: "t1",
    [`${TOKEN_ENV}_2`]: "t2",
  });
  const picked = new Set();
  for (const runId of ["100", "101", "102", "103", "104", "105"]) {
    const got = chooseSlot(pool(env(runId)));
    assert.equal(got.reason, "selected");
    assert.ok(["", "1", "2"].includes(got.slot), `unexpected slot ${got.slot}`);
    picked.add(got.slot);
  }
  assert.ok(picked.size > 1, "every run id landed on the same slot — distribution is broken");
});

test("chooseSlot: an unconfigured pool falls back to the ambient credential", () => {
  // `size: 0` is a supported configuration — a repo with no pool. The empty slot
  // resolves to the unsuffixed secret in the workflow, which is what these steps
  // always did.
  const got = chooseSlot(pool({ GITHUB_RUN_ID: "1" }));
  assert.deepEqual(got, { slot: "", reason: "pool-unconfigured" });
});

test("chooseSlot: a pool of one names that one slot", () => {
  const got = chooseSlot(pool({ GITHUB_RUN_ID: "1", [TOKEN_ENV]: "only" }));
  assert.deepEqual(got, { slot: "", reason: "selected" });
  const suffixed = chooseSlot(pool({ GITHUB_RUN_ID: "1", [`${TOKEN_ENV}_5`]: "only" }));
  assert.deepEqual(suffixed, { slot: "5", reason: "selected" });
});

test("chooseSlot: junk in place of a pool falls back rather than throwing", () => {
  for (const bad of [null, undefined, {}, 42, "pool", { size: 3 }]) {
    const got = chooseSlot(bad);
    assert.equal(got.slot, "", JSON.stringify(bad));
    assert.ok(["no-pool", "pool-unconfigured"].includes(got.reason), got.reason);
  }
});

test("chooseSlot: a slot name outside the pool's own naming is refused", () => {
  // The result is interpolated into a `secrets[...]` lookup, so a name this module
  // cannot place must degrade to the ambient credential rather than steer that lookup.
  const rogue = { size: 2, currentSlotName: () => "GITHUB_TOKEN" };
  assert.deepEqual(chooseSlot(rogue), { slot: "", reason: "unrecognised-slot" });
  const past = { size: 2, currentSlotName: () => `${TOKEN_ENV}_${MAX_SLOTS + 1}` };
  assert.deepEqual(chooseSlot(past), { slot: "", reason: "unrecognised-slot" });
  const nul = { size: 2, currentSlotName: () => null };
  assert.deepEqual(chooseSlot(nul), { slot: "", reason: "unrecognised-slot" });
});

test("chooseSlot: a duplicated secret is one slot, so selection cannot 'spread' onto itself", () => {
  // The migration shape — unsuffixed secret also copied into `_1` — dedupes to a single
  // slot. Selection must not believe it has two accounts and report a second name that
  // is the same credential.
  const got = chooseSlot(pool({ GITHUB_RUN_ID: "3", [TOKEN_ENV]: "same", [`${TOKEN_ENV}_1`]: "same" }));
  assert.equal(got.slot, "", "the deduped pool has exactly one slot: slot zero");
});

// --- capacityLine ------------------------------------------------------------

test("capacityLine: a small pool tells the operator to register more", () => {
  const line = capacityLine(pool({ GITHUB_RUN_ID: "1", [TOKEN_ENV]: "a", [`${TOKEN_ENV}_1`]: "b" }));
  assert.match(line, /2 of 9 credential slot\(s\) configured/);
  assert.match(line, /register more/);
});

test("capacityLine: an unconfigured pool says the pool is off", () => {
  const line = capacityLine(pool({ GITHUB_RUN_ID: "1" }));
  assert.match(line, /pool is OFF/);
  assert.doesNotMatch(line, /register more/);
});

test("capacityLine: a healthy pool reports without nagging", () => {
  const env = { GITHUB_RUN_ID: "1" };
  for (let i = 1; i <= 5; i++) env[`${TOKEN_ENV}_${i}`] = `t${i}`;
  const line = capacityLine(pool(env));
  assert.match(line, /5 of 9/);
  assert.doesNotMatch(line, /register more/);
});

test("capacityLine: junk yields an empty line rather than a wrong number", () => {
  for (const bad of [null, undefined, {}, { size: "two" }]) {
    assert.equal(capacityLine(bad), "", JSON.stringify(bad));
  }
});

// --- the fleet invariant -----------------------------------------------------

/**
 * Workflows whose `claude-code-action` step is knowingly still on the ambient
 * credential, each with the reason it was left alone. Anything NOT listed here must go
 * through the picker — that is what stops the next call site being added on slot zero
 * by copy-paste.
 */
const AMBIENT_ALLOWED = new Map([
  [
    "agent-summarize.yml",
    "Has no repo checkout at all and is deliberately built without repo credentials " +
      "('Summarize (read-only; no repo credentials)'). Running the picker needs a trusted " +
      "copy of it on disk, so wiring it would mean adding a checkout to the one job that " +
      "was designed not to have one — for the least consequential step in the pipeline.",
  ],
]);

test("every claude-code-action step selects a slot, or is a documented exception", () => {
  // The regression guard. Five workflows shared the slot-zero pin, and the fix is
  // per-workflow, so nothing structural stops a sixth being added the old way.
  const files = readdirSync(WORKFLOWS).filter((f) => f.endsWith(".yml") || f.endsWith(".yaml"));
  const ambient = [];
  let wired = 0;

  for (const f of files) {
    const text = readFileSync(path.join(WORKFLOWS, f), "utf8");
    if (!text.includes("claude-code-action")) continue;
    const bare = text.includes("claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}");
    const picked = text.includes("steps.cred.outputs.slot != ''");
    if (bare) ambient.push(f);
    if (picked) wired++;
  }

  const unexplained = ambient.filter((f) => !AMBIENT_ALLOWED.has(f));
  assert.deepEqual(
    unexplained,
    [],
    "these workflows hand claude-code-action the bare ambient credential, pinning them to " +
      `pool slot zero: ${unexplained.join(", ")}. Run the picker, or add an entry to ` +
      "AMBIENT_ALLOWED saying why not.",
  );
  // DERIVED, not a literal floor. Upstream pins 4 because it installs 5 such
  // workflows; a repository that installs fewer would fail this for having
  // ported less rather than for pinning a slot. The invariant that carries over
  // is the partition: every workflow calling claude-code-action is either wired
  // or documented, and at least one is wired so the loop is not vacuous.
  assert.ok(wired > 0, "no workflow selects a slot — this guard would be vacuous");
  assert.equal(
    wired + ambient.length,
    files.filter((f) => readFileSync(path.join(WORKFLOWS, f), "utf8").includes("claude-code-action")).length,
    "a claude-code-action workflow is neither wired to the picker nor a documented exception",
  );
});

test("each wired workflow guards the picker and fails open", () => {
  // Two properties per call site: the script is guarded with `[ -f ]` so a branch older
  // than it skips instead of redding, and the token expression falls back to the ambient
  // secret on an empty slot. Without the fallback an unset output would pass an EMPTY
  // credential and every one of these jobs would break at once.
  const files = readdirSync(WORKFLOWS).filter((f) => f.endsWith(".yml"));
  let checked = 0;
  for (const f of files) {
    const text = readFileSync(path.join(WORKFLOWS, f), "utf8");
    // Scoped to the ENV-BASED picker. `agent-review-panel.yml` shares the
    // `steps.cred.outputs.slot` convention but runs `pick-fix-credential.mjs`, which
    // reads the panel's pool-state artifact instead of the environment — so it needs
    // neither the pool env nor this file's guard shape. Its own tests cover it.
    if (!text.includes("pick-credential.mjs")) continue;
    checked++;
    assert.match(text, /\[ -f "?\.?[^"]*pick-credential\.mjs"? \]/, `${f}: picker must be guarded`);
    assert.match(
      text,
      /\|\| secrets\.CLAUDE_CODE_OAUTH_TOKEN \}\}/,
      `${f}: token expression must fall back to the ambient secret`,
    );
    assert.match(text, /continue-on-error: true/, `${f}: the picker must not be able to red the job`);
    // The pool must actually be passed, or the picker sees an empty pool every time and
    // silently reports `pool-unconfigured` forever.
    assert.match(text, /CLAUDE_CODE_OAUTH_TOKEN_8: \$\{\{ secrets\.CLAUDE_CODE_OAUTH_TOKEN_8 \}\}/, `${f}: pool env missing`);
  }
  // Same reasoning as the derived count above: the per-file assertions are the
  // guard, and this only keeps the loop from silently covering nothing.
  assert.ok(checked > 0, "no workflow runs the env-based picker — this guard would be vacuous");
});

test("no workflow runs the picker from an untrusted checkout", () => {
  // The picker receives all nine pool secrets, so a branch-controlled copy of it could
  // read every credential the pool holds. Each call site must run either the repo's own
  // main checkout (issue-triggered jobs, whose default ref IS main), the staged
  // `$RUNNER_TEMP/agent-tools` snapshot, or an explicit trusted sparse checkout.
  const TRUSTED = [
    "$RUNNER_TEMP/agent-tools/pick-credential.mjs", // staged from main before the branch checkout
    "$RUNNER_TEMP/trusted-cred/scripts/agent/pick-credential.mjs", // trusted sparse checkout, moved clear of the workspace
    "scripts/agent/pick-credential.mjs", // workspace IS main (issue_comment default ref)
  ];
  // `.trusted-cred/...` is NOT on this list any more, deliberately. `path:` is
  // relative to $GITHUB_WORKSPACE, so that spelling left main's copy inside the
  // tree an agent is told to commit and push — untracked and unignored. The
  // checkout still lands there (actions/checkout will not write outside the
  // workspace) and is moved out before the agent runs; the test below pins the
  // move.
  const files = readdirSync(WORKFLOWS).filter((f) => f.endsWith(".yml"));
  for (const f of files) {
    const text = readFileSync(path.join(WORKFLOWS, f), "utf8");
    for (const m of text.matchAll(/node "?([^"\s]*pick-credential\.mjs)"?/g)) {
      assert.ok(TRUSTED.includes(m[1]), `${f}: picker run from an unrecognised path: ${m[1]}`);
    }
  }
});

test("agent-review-reply stages its trusted copy after the branch, and out of it", () => {
  // Two properties, both ordering.
  //
  // AFTER the branch checkout: `actions/checkout` cleans its target path, so a
  // root checkout running later would delete the subdirectory and the `[ -f ]`
  // guard would silently skip to the ambient credential.
  //
  // OUT OF the workspace before the agent: the agent step below is told to
  // commit and push, and `path:` is workspace-relative — so main's copy of
  // scripts/agent would otherwise sit untracked and unignored in a
  // contributor's branch, one `git add -A` from being committed.
  const text = readFileSync(path.join(WORKFLOWS, "agent-review-reply.yml"), "utf8");
  const branch = text.indexOf("ref: ${{ steps.pr.outputs.ref }}");
  const trusted = text.indexOf("path: .trusted-cred");
  const moved = text.indexOf('mv .trusted-cred "$RUNNER_TEMP/trusted-cred"');
  const picker = text.indexOf('node "$RUNNER_TEMP/trusted-cred/scripts/agent/pick-credential.mjs"');
  const agent = text.indexOf("uses: anthropics/claude-code-action@");
  for (const [name, at] of [["branch", branch], ["trusted", trusted], ["moved", moved], ["picker", picker], ["agent", agent]]) {
    assert.ok(at > 0, `${name} step not found`);
  }
  assert.ok(branch < trusted, "the trusted checkout must come after the branch checkout");
  assert.ok(trusted < moved, "the move must come after the checkout it moves");
  assert.ok(moved < picker, "the picker must read the moved copy");
  assert.ok(moved < agent, "the copy must leave the workspace before the agent can commit it");
});
