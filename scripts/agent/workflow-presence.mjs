// Test-only. Reads a workflow file that this repository may not install yet.
//
// Several guards in the suites beside this file assert that a module and a
// GitHub Actions workflow carry the same literal, or that a workflow still
// invokes the script whose behaviour the module defines. Those guards exist
// because the failure they catch is SILENT: a drifted copy does not error, it
// quietly stops doing its job. None of that changes here.
//
// What changes is which workflows exist. This repository installs the advisory
// half of the command surface — `@claude review` and `@claude summarize` — and
// not the gating review panel or the fix agent
// (docs/design/agent-command-verbs.md, Phase 1 vs Phases 2 and 3). A guard
// whose workflow is absent has nothing to drift from, so it SKIPS.
//
// Skipping rather than deleting is the whole point: the guard re-arms by itself
// the day the workflow lands, with no one having to remember it was removed.
// Deleting them would make Phase 2 a silent regression of checks that were
// written precisely because their failure mode is invisible.

import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

/** `.github/workflows`, resolved from this file rather than from `cwd`. */
export const WORKFLOW_DIR = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
  "..",
  ".github",
  "workflows",
);

/** Is this workflow installed in this repository? */
export function hasWorkflow(name) {
  return existsSync(path.join(WORKFLOW_DIR, name));
}

/**
 * The workflow's text, or `null` when it is not installed.
 *
 * Returning null instead of throwing is what lets a guard live at module scope:
 * an eager `readFileSync` of a missing workflow fails the whole FILE, taking
 * every unrelated test in it down with the one guard that needed the workflow.
 */
export function readWorkflow(name) {
  const p = path.join(WORKFLOW_DIR, name);
  return existsSync(p) ? readFileSync(p, "utf8") : null;
}

/**
 * Options for `test()` that skip when `name` is not installed.
 *
 * Usage: `test("...", skipWithout("agent-review-panel.yml"), () => { ... })`.
 */
export function skipWithout(name) {
  return hasWorkflow(name)
    ? {}
    : { skip: `${name} is not installed in this repository (docs/design/agent-command-verbs.md)` };
}

/** The same question about a sibling MODULE, for the phases that ship one. */
export function hasModule(name) {
  return existsSync(path.join(path.dirname(fileURLToPath(import.meta.url)), name));
}

/** Options for `test()` that skip when the sibling module is not ported yet. */
export function skipWithoutModule(name) {
  return hasModule(name)
    ? {}
    : { skip: `${name} is not ported to this repository (docs/design/agent-command-verbs.md)` };
}
