/**
 * Environments for shelling out to git safely.
 *
 * `cwd` and `-C` do NOT decide which repository git operates on — the
 * environment does, and it wins. That matters because **git exports its
 * location variables into every hook it runs**, and this repo's `pre-push`
 * hook runs `pnpm verify:self`, which reaches these scripts. Under a hook,
 * a command that looks repo-scoped silently operates on whatever `GIT_DIR`
 * names instead.
 *
 * That is not hypothetical. A test fixture inheriting these variables
 * replaced a working branch's tip three times, and one of those states
 * reached a public PR as a diff appearing to delete every file in the repo.
 * Read paths answer about the wrong repository; write paths corrupt it.
 *
 * The quiet variant is the dangerous one. An inherited `GIT_INDEX_FILE` alone
 * leaves the suite EXITING 0 while the real index is replaced by the fixture's,
 * so `git status` then reports every tracked file as deleted and nothing in the
 * run said a word. The loud variants (`GIT_DIR`) at least fail the tests.
 *
 * `--dry-run` IS NOT A DRY RUN for this. `git push --dry-run` runs the
 * `pre-push` hook — and therefore `pnpm verify:self` — before git decides the
 * push is a rehearsal. One of the three incidents above was exactly that: an
 * operator being careful with `--no-verify` on every real push, then letting a
 * `--dry-run` through as obviously harmless. If you are guarding against this
 * class of bug with `--no-verify`, `--dry-run` needs it too.
 *
 * Two environments, because reads and writes need different strictness:
 *
 * - `repoScopedEnv(root)` — for READING an existing repository. Strips every
 *   location variable so `cwd` selects the repo, and caps discovery so it
 *   cannot ascend past `root`.
 * - `fixtureGitEnv(dir)` — for WRITING to a throwaway repository. Everything
 *   above, plus explicit `GIT_DIR`/`GIT_WORK_TREE` so no discovery happens.
 */

import { execFileSync } from "node:child_process";
import path from "node:path";

/**
 * Every variable that can redirect which repository, index, or object store
 * git touches. Removing all of them is the point: a denylist that misses one
 * is a denylist that fails silently, which is how this class of bug survives.
 *
 * The four that redirect WRITES are the dangerous ones. An inherited
 * `GIT_INDEX_FILE` makes `git add` rewrite another repository's index — and
 * because the objects it references live elsewhere, it leaves that index
 * CORRUPT, not merely modified. Pinning `GIT_DIR` alone does not stop it.
 */
export const GIT_LOCATION_VARS = Object.freeze([
  "GIT_DIR",
  "GIT_WORK_TREE",
  "GIT_COMMON_DIR",
  "GIT_INDEX_FILE",
  "GIT_OBJECT_DIRECTORY",
  "GIT_ALTERNATE_OBJECT_DIRECTORIES",
  "GIT_CEILING_DIRECTORIES",
  "GIT_NAMESPACE",
  "GIT_DISCOVERY_ACROSS_FILESYSTEM",
  "GIT_INDEX_VERSION",
]);

/**
 * Variables that steer git's *behaviour* rather than its location.
 *
 * git exports `GIT_CONFIG_PARAMETERS` for every `-c` it was given, so config
 * set on an outer invocation reaches an inner one. Pathspec-magic variables
 * silently change what a pathspec matches. Neither redirects the repository,
 * but both can change an answer this harness then gates on.
 */
export const GIT_BEHAVIOUR_VARS = Object.freeze([
  "GIT_CONFIG_PARAMETERS",
  "GIT_CONFIG_COUNT",
  "GIT_CONFIG_GLOBAL",
  "GIT_CONFIG_SYSTEM",
  "GIT_CONFIG_NOSYSTEM",
  "GIT_GLOB_PATHSPECS",
  "GIT_NOGLOB_PATHSPECS",
  "GIT_LITERAL_PATHSPECS",
  "GIT_ICASE_PATHSPECS",
]);

/** `GIT_CONFIG_KEY_0`, `GIT_CONFIG_VALUE_0`, … — numbered, so matched by shape. */
const NUMBERED_CONFIG = /^GIT_CONFIG_(?:KEY|VALUE)_\d+$/;

/** A copy of `base` with every git-steering variable removed. */
function stripped(base) {
  const env = { ...base };
  for (const key of [...GIT_LOCATION_VARS, ...GIT_BEHAVIOUR_VARS]) delete env[key];
  for (const key of Object.keys(env)) {
    if (NUMBERED_CONFIG.test(key)) delete env[key];
  }
  return env;
}

/**
 * Environment for reading the repository at `root`, with `cwd: root`.
 *
 * Deleting `GIT_DIR` rather than pinning it is deliberate: `root` may be a
 * linked worktree, where `.git` is a FILE holding a `gitdir:` pointer, so a
 * computed `GIT_DIR: root/.git` would be wrong exactly where it matters most.
 *
 * Deletion alone would leave discovery free to ascend, so `root` not being a
 * repository would silently yield answers about an ancestor — a gate quietly
 * measuring the wrong tree. `GIT_CEILING_DIRECTORIES` is set to `root`'s
 * PARENT to cap that: git still finds `root`'s own `.git`, but refuses to
 * climb past it and fails loudly instead. The parent, not `root` itself —
 * git only honours ceiling entries that are strict ancestors of the directory
 * it is searching from, so a ceiling equal to `cwd` is inert.
 *
 * Verified against a linked worktree: the `gitdir:` pointer is still followed,
 * because a ceiling constrains directory discovery, not pointer resolution.
 */
export function repoScopedEnv(root, base = process.env) {
  const env = stripped(base);
  const abs = path.resolve(root);
  const parent = path.dirname(abs);
  // At a filesystem root `dirname` is a fixed point; a ceiling there is inert
  // and would only be misleading, so leave it unset.
  if (parent !== abs) env.GIT_CEILING_DIRECTORIES = parent;
  return env;
}

/**
 * Environment for writing to a throwaway repository at `dir`.
 *
 * Strictly stronger than `repoScopedEnv`: every steering variable is removed
 * AND `GIT_DIR`/`GIT_WORK_TREE` are pinned, so git performs no discovery at
 * all. Pinning is right here (unlike the read path) because `dir` is always a
 * fresh ordinary repository — never a worktree or a bare repo.
 */
export function fixtureGitEnv(dir, base = process.env) {
  const abs = path.resolve(dir);
  return {
    ...repoScopedEnv(abs, base),
    GIT_DIR: path.join(abs, ".git"),
    GIT_WORK_TREE: abs,
  };
}

/**
 * The commit `HEAD` names in the repository at `root`, or `null` if that
 * cannot be determined.
 *
 * Scoped like every other read here. A guard that reads `HEAD` with an
 * inherited environment can watch a different repository than the one being
 * damaged, which makes it silently inert precisely under a hook — the one
 * place it is needed.
 */
export function readHeadSha(root) {
  try {
    return execFileSync("git", ["rev-parse", "HEAD"], {
      cwd: root,
      env: repoScopedEnv(root),
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return null;
  }
}
