// D1/D2 take effect only through the workflows: the trusted report jobs must
// post the removal record, and both fixer prompts must carry the rule.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const WF = (n) => readFileSync(path.join(HERE, "..", "..", ".github", "workflows", n), "utf8");

for (const file of ["agent-review-panel.yml", "agent-fix.yml"]) {
  test(`${file}: the trusted report job records removed tests as github-actions[bot]`, () => {
    const src = WF(file);
    const at = src.indexOf("- name: Record tests the fix round removed\n");
    assert.ok(at > 0, "no removal step");
    const block = src.slice(at, src.indexOf("\n      - ", at + 1));
    assert.match(block, /GH_TOKEN: \$\{\{ secrets\.GITHUB_TOKEN \}\}/);
    assert.match(block, /node scripts\/agent\/test-removals\.mjs post "\$PR" --before "\$BEFORE" --after "\$AFTER" --head "\$HEAD_SHA"/);
    assert.match(block, /continue-on-error: true/, "evidence is best-effort; it must never red the report job");
  });

  // /code-review: AFTER is the live branch ref, read after the fixer finished, so
  // a commit pushed later by a human was blamed on the round. The script bounds
  // the round by the App's own pushes from `before`, using a start time recorded
  // BEFORE the agent ran and the App's identity from this job's own token.
  test(`${file}: the removal record is bounded by the App's own pushes`, () => {
    const src = WF(file);
    const at = src.indexOf("- name: Record tests the fix round removed\n");
    const block = src.slice(at, src.indexOf("\n      - ", at + 1));
    assert.match(block, /--pusher "\$PUSHER" --since "\$SINCE" --branch "\$BRANCH"/);
    assert.match(block, /PUSHER: \$\{\{ steps\.app-token\.outputs\.app-slug \}\}\[bot\]/);
    assert.match(block, /SINCE: \$\{\{ needs\.fix\.outputs\.before_at \}\}/);
    assert.match(src, /\n {6}before_at: \$\{\{ steps\.before-fix\.outputs\.at \}\}\n/);
    const rec = src.indexOf("- name: Record branch head before fix\n");
    const recBlock = src.slice(rec, src.indexOf("\n      - ", rec + 1));
    assert.match(recBlock, /echo "at=\$\(date -u \+%Y-%m-%dT%H:%M:%SZ\)"/);
    // Recorded before the agent: nothing may run after it in its own job.
    const agent = src.indexOf("- name: Address panel findings\n");
    assert.ok(rec > 0 && agent > 0 && rec < agent, "the start time must be written before the agent");
  });

  test(`${file}: the fixer is told not to delete a test that still reproduces`, () => {
    // Go has no `it.fails`: the reproducer is kept behind a `t.Skip` that
    // names the finding, the item is reported skipped, and test-removals.mjs
    // records the skip for the adjudicator.
    assert.match(WF(file), /NEVER DELETE A TEST THAT SHOWS A FINDING STILL REPRODUCES\./);
    assert.match(WF(file), /make its first\s+statement `t\.Skip\("still reproduces: <finding>"\)`/);
    assert.match(WF(file), /report that item `--skipped`/);
  });
}

for (const file of ["agent-review-panel.yml", "agent-review-on-demand.yml"]) {
  test(`${file}: an unreadable issue is recorded, and the panel is told`, () => {
    const src = WF(file);
    assert.match(src, /fs\.writeFileSync\('\/tmp\/issue\.state', 'unreadable'\);/);
    assert.match(src, /--issue-file \/tmp\/issue\.txt\n\s+--issue-state \/tmp\/issue\.state/);
  });
}

// ghLines JSON.parses every output line, so each --jq it gets must print JSON.
// `.[].sha` printed bare shas, the parse threw, and the record was never
// posted: main() logged "could not read the round's commits" every time.
test("every --jq the removal CLI parses as JSON lines prints JSON", () => {
  const src = readFileSync(path.join(HERE, "test-removals.mjs"), "utf8");
  const jqs = [...src.matchAll(/ghLines\(\[[^\]]*?"--jq",\s*"([^"]+)"/g)].map((m) => m[1]);
  assert.ok(jqs.length >= 3, `found the ghLines calls (${jqs.length})`);
  for (const jq of jqs) assert.match(jq.trim(), /(\}|\[\]|tojson)$/, `--jq ${jq} must print JSON`);
});
