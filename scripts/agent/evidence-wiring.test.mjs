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
