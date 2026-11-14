// What a repository must HAVE before the harness is switched on, beyond its
// configuration.
//
// Most of what differs between repositories is a value in
// `.github/agent-harness/config.json`. A few things are not values but files
// the workflows name in so many words, and the issue-to-PR prompt names the
// most: it tells the agent to follow two documents, to write its plan into a
// directory, to start a design document from a template and list it in an
// index, and to shape the pull request after a template. The prompt is the
// same bytes in every repository, so the files have to exist in each — and a
// repository that lacks one finds out from an agent that was told to follow
// a file it cannot open, an hour into its first run.
//
// This module is the list, so that the repository's own test can check it
// (`missingImplementPaths`, called from `repo.test.mjs`) and the package's
// test can check that the list and the prompt still agree
// (`conventions.test.mjs`). It reads no configuration: the paths are the
// harness's, not the repository's.

import { statSync } from "node:fs";
import path from "node:path";

/**
 * Every repository path the issue-to-PR prompt (`agent-implement.yml`) names.
 * `named` is the text the prompt uses for it, and `role` is what the agent is
 * told to do with it.
 */
export const IMPLEMENT_PROMPT_PATHS = Object.freeze([
  { path: "CLAUDE.md", kind: "file", named: "CLAUDE.md", role: "followed as the authoritative process" },
  { path: "CONTRIBUTING.md", kind: "file", named: "CONTRIBUTING.md", role: "followed as the authoritative process" },
  { path: "docs/tasks/active", kind: "directory", named: "docs/tasks/active/", role: "where the todo and lessons pair is written" },
  { path: "docs/design/TEMPLATE.md", kind: "file", named: "docs/design/TEMPLATE.md", role: "what a new design document starts from" },
  { path: "docs/design/README.md", kind: "file", named: "docs/design/README.md", role: "the index a new design document is added to" },
  { path: ".github/PULL_REQUEST_TEMPLATE.md", kind: "file", named: ".github/PULL_REQUEST_TEMPLATE.md", role: "the shape of the pull request body" },
].map(Object.freeze));

/**
 * The entries of `IMPLEMENT_PROMPT_PATHS` that `root` does not have, each
 * with the reason: nothing there, or a file where a directory is named (or
 * the reverse). `[]` means the issue-to-PR prompt names nothing the
 * repository lacks. A directory has to exist in a fresh checkout, so it needs
 * a tracked file in it; an empty one is not there.
 */
export function missingImplementPaths(root) {
  const out = [];
  for (const entry of IMPLEMENT_PROMPT_PATHS) {
    let stat = null;
    try {
      stat = statSync(path.join(root, ...entry.path.split("/")));
    } catch {
      // reported below
    }
    const ok = stat !== null && (entry.kind === "directory" ? stat.isDirectory() : stat.isFile());
    if (!ok) out.push({ path: entry.path, kind: entry.kind, role: entry.role, problem: stat === null ? "does not exist" : `is not a ${entry.kind}` });
  }
  return out;
}
