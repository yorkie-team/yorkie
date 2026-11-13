// Replace a repository's copy of the core with another checkout's.
//
//   node scripts/agent/sync.mjs --from <checkout of the upstream repository>
//                               [--to <repository to update>]
//                               [--repository owner/name]
//
// `--to` defaults to the repository this file sits in, which is the usual way
// to run it: from the copy being updated. A repository adopting the harness
// for the first time has no copy yet, so it runs the SOURCE's and names itself
// with `--to`.
//
// WHAT IT DOES, in this order, and it stops at the first thing that is wrong:
//
//   1. The source's tree must match the source's manifest, and its core must
//      have nothing uncommitted — otherwise the commit recorded below would
//      not describe the bytes copied.
//   2. The source must be the repository the core is written in (its
//      `upstream.json` says `repository: null`). Syncing from a copy would
//      record a commit of the wrong repository.
//   3. The destination's core must have nothing uncommitted, so that what
//      this overwrites can be got back with git.
//   4. Every file the source's manifest lists is copied over; every core file
//      the destination has and the source does not is deleted; the manifest
//      is copied.
//   5. The destination's `.github/agent-harness/upstream.json` is written:
//      the source's repository, its HEAD commit, the manifest's digest.
//   6. The NEW core's configuration loader is run against the destination's
//      configuration, and what it refuses — a key the new schema requires,
//      one it no longer knows — is printed. That is the list of edits the
//      sync leaves for a person.
//
// WHAT IT NEVER TOUCHES: anything outside the core. The paths it writes and
// deletes are checked against `isCorePath`, so `.github/agent-harness/`
// (bar `upstream.json`, step 5) and `.github/actions/` are out of reach by
// construction, not by care.
//
// A LOCAL CHECKOUT ONLY. Fetching one is `git clone`; doing it here would add
// a network path and a credential question to a tool that needs neither.

import { execFileSync, spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { CONFIG_DIR_ENV, HARNESS_DIR } from "./config.mjs";
import { repoScopedEnv } from "./git-env.mjs";
import {
  MANIFEST_PATH,
  PACKAGE_DIR,
  UPSTREAM_PATH,
  WORKFLOW_DIR,
  compareTree,
  corePaths,
  defaultRoot,
  describeDifference,
  isCorePath,
  manifestDigest,
  parseManifest,
  readManifest,
  readUpstream,
  validateUpstream,
} from "./manifest.mjs";

const REPO_SLUG = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;

/** git in `root`, unable to reach any other repository. Throws with git's own words. */
function git(root, ...args) {
  try {
    // Not trimmed: `status --porcelain` lines begin with a significant space.
    return execFileSync("git", args, { cwd: root, env: repoScopedEnv(root), encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  } catch (err) {
    throw new Error(`git ${args.join(" ")} failed in ${root}: ${String(err.stderr || err.message).trim()}`);
  }
}

/** Uncommitted changes under the core of the repository at `root`, as status lines. */
function uncommittedCore(root) {
  const out = git(root, "status", "--porcelain", "--untracked-files=all", "--", PACKAGE_DIR, WORKFLOW_DIR);
  return out
    .split("\n")
    .filter((l) => l !== "")
    // `XY path` or `XY old -> new`; a core path on either side counts.
    .filter((l) => l.slice(3).split(" -> ").some((p) => isCorePath(p.replace(/^"|"$/g, "")) || p.replace(/^"|"$/g, "") === MANIFEST_PATH));
}

/** The source's `origin` URL, or "" when it has none. */
function originUrl(root) {
  try {
    return git(root, "config", "--get", "remote.origin.url").trim();
  } catch {
    return "";
  }
}

/** `owner/name` from a GitHub remote URL, or null. */
export function slugFromRemote(url) {
  const m = /github\.com[:/]([A-Za-z0-9][A-Za-z0-9-]*)\/([A-Za-z0-9._-]+?)(?:\.git)?\/?$/.exec(String(url ?? "").trim());
  return m ? `${m[1]}/${m[2]}` : null;
}

/**
 * Sync the core from `from` into `to`. Returns `{ copied, deleted, upstream,
 * configProblems }`. Throws, having changed nothing, when a precondition fails.
 */
export function sync({ from, to, repository }) {
  const src = path.resolve(from);
  const dest = path.resolve(to);
  for (const [what, dir] of [["--from", src], ["--to", dest]]) {
    if (!existsSync(path.join(dir, ".git"))) throw new Error(`${what} ${dir} is not the root of a git checkout`);
  }
  if (realpathSync(src) === realpathSync(dest)) throw new Error("--from and --to are the same checkout");

  // 1. The source is what it says it is.
  const manifestText = readManifest(src);
  const difference = describeDifference(compareTree(src, manifestText));
  if (difference.length > 0) {
    throw new Error(`the source's tree does not match its ${MANIFEST_PATH}; refusing to copy it:\n${difference.map((l) => `  ${l}`).join("\n")}`);
  }
  const srcDirty = uncommittedCore(src);
  if (srcDirty.length > 0) {
    throw new Error(`the source has uncommitted changes under the core, so its HEAD does not describe it:\n${srcDirty.map((l) => `  ${l}`).join("\n")}`);
  }
  // 2. …and is where the core is written.
  const srcUpstream = readUpstream(src);
  if (srcUpstream.repository !== null) {
    throw new Error(`the source is itself a copy (of ${srcUpstream.repository}); sync from a checkout of that repository instead`);
  }
  const slug = repository ?? slugFromRemote(originUrl(src));
  if (!slug || !REPO_SLUG.test(slug)) {
    throw new Error("cannot tell which repository the source is: its `origin` is not a GitHub URL. Pass --repository owner/name");
  }
  const commit = git(src, "rev-parse", "HEAD").trim();

  // 3. Nothing in the destination is lost that git cannot give back.
  const destDirty = uncommittedCore(dest);
  if (destDirty.length > 0) {
    throw new Error(`the destination has uncommitted changes under the core; commit or discard them first:\n${destDirty.map((l) => `  ${l}`).join("\n")}`);
  }

  // 4. Copy and delete. Every path was checked by `parseManifest`/`corePaths`
  //    to be a core path; checked once more here, where the writes are.
  const wanted = [...parseManifest(manifestText).keys()];
  const copied = [];
  for (const rel of wanted) {
    if (!isCorePath(rel)) throw new Error(`refusing to write ${rel}: not a file of the core`);
    const target = path.join(dest, rel);
    mkdirSync(path.dirname(target), { recursive: true });
    copyFileSync(path.join(src, rel), target);
    copied.push(rel);
  }
  const deleted = [];
  for (const rel of corePaths(dest)) {
    if (wanted.includes(rel)) continue;
    if (!isCorePath(rel)) throw new Error(`refusing to delete ${rel}: not a file of the core`);
    rmSync(path.join(dest, rel));
    deleted.push(rel);
  }
  writeFileSync(path.join(dest, MANIFEST_PATH), manifestText);

  // 5. Record where it came from.
  const upstream = validateUpstream({ repository: slug, commit, manifestDigest: manifestDigest(manifestText) });
  mkdirSync(path.dirname(path.join(dest, UPSTREAM_PATH)), { recursive: true });
  writeFileSync(path.join(dest, UPSTREAM_PATH), `${JSON.stringify(upstream, null, 2)}\n`);

  // 6. What the new schema makes of the old configuration.
  return { copied, deleted, upstream, configProblems: configProblems(dest) };
}

/**
 * The destination's configuration, as the core now in it reads it: `[]` when
 * it loads, else the loader's problems, one per entry. Run as a child so that
 * the schema applied is the one just copied, not the one this process loaded.
 */
export function configProblems(dest) {
  const configDir = path.join(dest, ...HARNESS_DIR.split("/"));
  const run = spawnSync(process.execPath, [path.join(dest, PACKAGE_DIR, "config.mjs"), "get", "repo.name"], {
    encoding: "utf8",
    env: { ...process.env, [CONFIG_DIR_ENV]: configDir },
  });
  if (run.status === 0) return [];
  const lines = String(run.stderr).split("\n").map((l) => l.replace(/^\s+- /, "").trim()).filter((l) => l !== "");
  // The loader's first line names the file; the rest are the problems.
  return lines.length > 1 && /is invalid:$/.test(lines[0]) ? lines.slice(1) : lines;
}

// --- CLI ---------------------------------------------------------------------------

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    if (!["--from", "--to", "--repository"].includes(flag) || argv[i + 1] === undefined) return null;
    out[flag.slice(2)] = argv[++i];
  }
  return out.from ? out : null;
}

function isDirectRun() {
  if (!process.argv[1]) return false;
  const entry = fileURLToPath(import.meta.url);
  try {
    return realpathSync(process.argv[1]) === realpathSync(entry);
  } catch {
    return path.resolve(process.argv[1]) === path.resolve(entry);
  }
}

if (isDirectRun()) {
  const args = parseArgs(process.argv.slice(2));
  if (!args) {
    console.error("usage: node sync.mjs --from <upstream checkout> [--to <repository>] [--repository owner/name]");
    process.exit(1);
  }
  try {
    const result = sync({ from: args.from, to: args.to ?? defaultRoot(), repository: args.repository });
    console.log(`Copied ${result.copied.length} files, deleted ${result.deleted.length}.`);
    for (const rel of result.deleted) console.log(`  deleted: ${rel}`);
    console.log(`${UPSTREAM_PATH}: ${result.upstream.repository} at ${result.upstream.commit}, manifest digest ${result.upstream.manifestDigest}`);
    if (result.configProblems.length === 0) {
      console.log("The configuration loads under the new schema.");
    } else {
      console.log(`\nThe configuration does not load under the new schema. Edit ${HARNESS_DIR}/config.json:`);
      for (const p of result.configProblems) console.log(`  - ${p}`);
    }
    console.log("\nNext: `cd scripts/agent && npm ci && npm test && npm run test:repo`, then review and commit.");
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
}
