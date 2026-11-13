// Replace a repository's copy of the core with another checkout's.
//
//   node scripts/agent/sync.mjs --from <checkout of the upstream repository>
//                               [--to <repository to update>]
//                               [--repository owner/name]
//                               [--allow-unmerged]
//
// `--to` defaults to the repository this file sits in, which is the usual way
// to run it: from the copy being updated. A repository adopting the harness
// for the first time has no copy yet, so it runs the SOURCE's and names itself
// with `--to`.
//
// A COPY IS SYNCED FROM UPSTREAM'S DEFAULT BRANCH, AND ONLY FROM IT. The
// commit recorded below is later looked up in the upstream repository by the
// copy's CI (`manifest.mjs upstream`), which requires it to be on the default
// branch there: a commit that was never pushed is not found, and one that is
// only a pull request's head is refused. So the source's `HEAD` must be
// contained in its `origin/<default branch>` as last fetched. `--allow-unmerged`
// is for trying a sync out locally; it says, loudly, that the copy's CI will
// fail until it is synced again from the default branch.
//
// EVERYTHING IS CHECKED BEFORE ANYTHING IS WRITTEN. In order:
//
//   1. The source's tree matches the source's manifest, its core has nothing
//      uncommitted, it is the repository the core is written in (its
//      `upstream.json` says `repository: null`), and its `HEAD` is on its
//      default branch.
//   2. In the destination, under the core: nothing modified or staged; no
//      file git does not track, ignored ones included (the sync would
//      otherwise have to overwrite, delete or leave behind a file git cannot
//      give back); no symbolic link and nothing that is not a regular file,
//      at any path this will write or on the way to one — a link there would
//      take a write outside the repository.
//   3. Every file to be copied is read from the source.
//
// Then, and only then: the files are written, the core files the source no
// longer has are deleted (tracked ones only, by construction of step 2), the
// manifest is written, and `upstream.json` records the source's repository,
// commit and manifest digest. Nothing in that phase can fail except the
// filesystem. If it does — a full disk, a permission — the destination is
// part old and part new, and every file touched was tracked and unmodified,
// so `git restore --staged --worktree -- scripts/agent .github/workflows`
// puts back what was overwritten or deleted and `git clean -n -- scripts/agent
// .github/workflows` lists what was added.
//
// Last, the NEW core's configuration loader is run against the destination's
// configuration, and what it refuses — a key the new schema requires, one it
// no longer knows — is printed. That is the list of edits a sync leaves for a
// person.
//
// WHAT IT NEVER TOUCHES: anything outside the core, bar `upstream.json`. The
// paths it writes and deletes are checked against `isCorePath`, so the
// repository's own configuration and its toolchain action are out of reach
// by construction, not by care.
//
// A LOCAL CHECKOUT ONLY. Fetching one is `git clone`; doing it here would add
// a network path and a credential question to a tool that needs neither.

import { execFileSync, spawnSync } from "node:child_process";
import { lstatSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { CONFIG_DIR_ENV, HARNESS_DIR, isRepoSlug } from "./config.mjs";
import { repoScopedEnv } from "./git-env.mjs";
import {
  GITATTRIBUTES_LINES,
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

/** git in `root`, unable to reach any other repository. Throws with git's own words. */
function git(root, ...args) {
  try {
    // Not trimmed: `status --porcelain` lines begin with a significant space.
    return execFileSync("git", args, { cwd: root, env: repoScopedEnv(root), encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], maxBuffer: 64 * 1024 * 1024 });
  } catch (err) {
    throw new Error(`git ${args.join(" ")} failed in ${root}: ${String(err.stderr || err.message).trim()}`);
  }
}
const gitOk = (root, ...args) => {
  try {
    git(root, ...args);
    return true;
  } catch {
    return false;
  }
};

/** A core path, or the manifest: what this tool may read, write or delete. */
const isShared = (rel) => isCorePath(rel) || rel === MANIFEST_PATH;

/**
 * Modified, staged, renamed or conflicted paths under the core, as `XY path`
 * lines. `-z`, so a path is the bytes git has for it: the default output
 * quotes and escapes any name that is not plain ASCII, and a quoted name is
 * not a core path to anything that then tests it.
 */
function modifiedShared(root) {
  const fields = git(root, "status", "--porcelain=v1", "-z", "--untracked-files=no", "--", PACKAGE_DIR, WORKFLOW_DIR).split("\0");
  const out = [];
  for (let i = 0; i < fields.length; i++) {
    const entry = fields[i];
    if (entry === "") continue;
    const code = entry.slice(0, 2);
    const paths = [entry.slice(3)];
    // A rename or copy carries its other path in the next field.
    if (code.includes("R") || code.includes("C")) paths.push(fields[++i]);
    if (paths.some(isShared)) out.push(`${code} ${paths.join(" <- ")}`);
  }
  return out;
}

/** What git tracks under the core, as a set of paths. */
function trackedShared(root) {
  return new Set(git(root, "ls-files", "-z", "--", PACKAGE_DIR, WORKFLOW_DIR).split("\0").filter((p) => p !== "" && isShared(p)));
}

/** `lstat`, or null when nothing is there. */
function stat(file) {
  try {
    return lstatSync(file);
  } catch (err) {
    if (err.code === "ENOENT" || err.code === "ENOTDIR") return null;
    throw err;
  }
}

/**
 * Why `rel` cannot be safely written under `root`, or null. Every directory on
 * the way must be a real directory and the target, if it exists, a regular
 * file: a symbolic link at any of them would carry the write somewhere else.
 */
function unsafeTarget(root, rel, { directory = false } = {}) {
  const parts = rel.split("/");
  let at = root;
  for (let i = 0; i < parts.length; i++) {
    at = path.join(at, parts[i]);
    const s = stat(at);
    if (s === null) return null; // nothing there, and nothing below it either
    const shown = parts.slice(0, i + 1).join("/");
    const last = i === parts.length - 1;
    if (s.isSymbolicLink()) return `${shown} is a symbolic link`;
    if ((!last || directory) && !s.isDirectory()) return `${shown} is not a directory`;
    if (last && !directory && !s.isFile()) return `${shown} is not a regular file`;
  }
  return null;
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
  const m = /github\.com[:/]([^/\s]+)\/([^/\s]+?)(?:\.git)?\/?$/.exec(String(url ?? "").trim());
  const slug = m ? `${m[1]}/${m[2]}` : null;
  return slug && isRepoSlug(slug) ? slug : null;
}

/**
 * Is the source's `HEAD` on its remote's default branch, as last fetched?
 * `{ ok, branch, why }`. The default branch is read from `origin/HEAD`, which
 * a clone sets; it is not assumed to be `main`.
 */
function onDefaultBranch(src, commit) {
  let ref;
  try {
    ref = git(src, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD").trim();
  } catch {
    return { ok: false, why: "the source does not know its remote's default branch (`refs/remotes/origin/HEAD` is not set; `git remote set-head origin --auto` sets it)" };
  }
  const branch = ref.replace(/^refs\/remotes\//, "");
  if (gitOk(src, "merge-base", "--is-ancestor", commit, ref)) return { ok: true, branch };
  return { ok: false, why: `the source's HEAD (${commit}) is not contained in ${branch} as last fetched` };
}

/** Does git leave `rel`'s line endings alone in the repository at `root`? */
function unconverted(root, rel) {
  try {
    return /: text: unset\s*$/.test(git(root, "check-attr", "text", "--", rel));
  } catch {
    return false;
  }
}

/**
 * Sync the core from `from` into `to`. Returns `{ copied, deleted, upstream,
 * configProblems, warnings, todo }`. Throws, having changed nothing, when a
 * precondition fails.
 */
export function sync({ from, to, repository, allowUnmerged = false }) {
  const src = path.resolve(from);
  const dest = path.resolve(to);
  for (const [what, dir] of [["--from", src], ["--to", dest]]) {
    if (stat(path.join(dir, ".git")) === null) throw new Error(`${what} ${dir} is not the root of a git checkout`);
  }
  if (realpathSync(src) === realpathSync(dest)) throw new Error("--from and --to are the same checkout");
  const warnings = [];

  // ---- 1. The source is what it says it is. -----------------------------------
  const manifestText = readManifest(src);
  const difference = describeDifference(compareTree(src, manifestText));
  if (difference.length > 0) {
    throw new Error(`the source's tree does not match its ${MANIFEST_PATH}; refusing to copy it:\n${difference.map((l) => `  ${l}`).join("\n")}`);
  }
  const srcTracked = trackedShared(src);
  const srcDirty = [...modifiedShared(src), ...[...corePaths(src), MANIFEST_PATH].filter((p) => !srcTracked.has(p)).map((p) => `?? ${p}`)];
  if (srcDirty.length > 0) {
    throw new Error(`the source has uncommitted changes under the core, so its HEAD does not describe it:\n${srcDirty.map((l) => `  ${l}`).join("\n")}`);
  }
  const srcUpstream = readUpstream(src);
  if (srcUpstream.repository !== null) {
    throw new Error(`the source is itself a copy (of ${srcUpstream.repository}); sync from a checkout of that repository instead`);
  }
  const slug = repository ?? slugFromRemote(originUrl(src));
  if (!isRepoSlug(slug)) {
    throw new Error("cannot tell which repository the source is: its `origin` is not a GitHub URL. Pass --repository owner/name");
  }
  const commit = git(src, "rev-parse", "HEAD").trim();
  const placed = onDefaultBranch(src, commit);
  if (!placed.ok) {
    if (!allowUnmerged) {
      throw new Error(
        `${placed.why}. A copy is synced from the upstream's default branch only: the copy's CI looks the recorded commit up there and fails if it is ` +
          "not on that branch. In the source, `git fetch origin` and check out the default branch's tip; or, to try a sync out locally, pass --allow-unmerged.",
      );
    }
    warnings.push(
      `NOT ON THE DEFAULT BRANCH: ${placed.why}. --allow-unmerged let this through. The destination's CI (\`npm run check:upstream\`) WILL FAIL ` +
        "until it is synced again from the upstream's default branch. Do not merge this sync.",
    );
  }
  const wanted = [...parseManifest(manifestText).keys()];
  const upstream = validateUpstream({ repository: slug, commit, manifestDigest: manifestDigest(manifestText) });

  // ---- 2. Nothing in the destination is lost, and nothing is written elsewhere. --
  for (const rel of [...wanted, MANIFEST_PATH]) {
    if (!isShared(rel)) throw new Error(`refusing to write ${rel}: not a file of the core`);
  }
  // Links and odd files first: the walk below would follow or trip on them.
  const unsafe = [
    ...[PACKAGE_DIR, WORKFLOW_DIR, HARNESS_DIR].map((rel) => unsafeTarget(dest, rel, { directory: true })),
    ...[...wanted, MANIFEST_PATH, UPSTREAM_PATH].map((rel) => unsafeTarget(dest, rel)),
  ].filter((why) => why !== null);
  if (unsafe.length > 0) {
    throw new Error(
      "the destination has something other than plain files and directories where the core goes; refusing to write through it:\n" +
        [...new Set(unsafe)].map((l) => `  ${l}`).join("\n"),
    );
  }
  let present;
  try {
    present = corePaths(dest); // throws on a link or a non-regular file anywhere under the core
  } catch (err) {
    throw new Error(`the destination's core cannot be replaced: ${err.message.replace(/^manifest: /, "")}`);
  }
  if (stat(path.join(dest, MANIFEST_PATH)) !== null) present.push(MANIFEST_PATH);
  const destModified = modifiedShared(dest);
  if (destModified.length > 0) {
    throw new Error(`the destination has uncommitted changes under the core; commit or discard them first:\n${destModified.map((l) => `  ${l}`).join("\n")}`);
  }
  // Asked of git's own list rather than of `git status`, which does not show
  // ignored files: a `debug.log` matched by a `.gitignore` is just as much a
  // file this would delete or leave behind, and git could not give it back.
  const tracked = trackedShared(dest);
  const untracked = present.filter((p) => !tracked.has(p));
  if (untracked.length > 0) {
    throw new Error(
      "the destination has files under the core that git does not track (ignored ones count). A sync would have to overwrite, delete or strand them, " +
        `and git could not give them back; move them out first:\n${untracked.map((l) => `  ${l}`).join("\n")}`,
    );
  }
  const deletions = present.filter((p) => !wanted.includes(p) && p !== MANIFEST_PATH);
  for (const rel of deletions) {
    if (!isCorePath(rel) || !tracked.has(rel)) throw new Error(`refusing to delete ${rel}: not a tracked file of the core`);
  }
  for (const line of GITATTRIBUTES_LINES) {
    const probe = line.startsWith(PACKAGE_DIR) ? `${PACKAGE_DIR}/config.mjs` : `${WORKFLOW_DIR}/agent-scripts.yml`;
    if (!unconverted(dest, probe)) {
      warnings.push(
        `the destination's .gitattributes does not set \`${line}\`. A checkout with core.autocrlf on would rewrite the shared files' line endings ` +
          "and fail the manifest check; add it at the repository's root.",
      );
    }
  }

  // ---- 3. Read everything that will be written. ----------------------------------
  const bytes = new Map(wanted.map((rel) => [rel, readFileSync(path.join(src, rel))]));

  // ---- Write. From here only the filesystem can fail; see the header. ------------
  for (const [rel, data] of bytes) {
    mkdirSync(path.dirname(path.join(dest, rel)), { recursive: true });
    writeFileSync(path.join(dest, rel), data);
  }
  for (const rel of deletions) rmSync(path.join(dest, rel));
  writeFileSync(path.join(dest, MANIFEST_PATH), manifestText);
  mkdirSync(path.dirname(path.join(dest, UPSTREAM_PATH)), { recursive: true });
  writeFileSync(path.join(dest, UPSTREAM_PATH), `${JSON.stringify(upstream, null, 2)}\n`);

  return {
    copied: wanted,
    deleted: deletions,
    upstream,
    configProblems: configProblems(dest),
    warnings,
    todo: [
      // The core cannot know which repository is the origin: a copy whose
      // `upstream.json` said `repository: null` would pass every check here.
      // Only the repository's own tests can say what it is a copy of.
      `Pin the upstream in ${HARNESS_DIR}/repo.test.mjs, if it is not already: assert that ${UPSTREAM_PATH}'s \`repository\` is ${JSON.stringify(slug)}. ` +
        "Without that, a later edit to `null` would switch every drift check off.",
      `Stage the result before testing — \`git add ${PACKAGE_DIR} ${WORKFLOW_DIR} ${HARNESS_DIR}\` — then, in ${PACKAGE_DIR}: \`npm ci && npm test && npm run test:repo\`. ` +
        "The suite compares the manifest with what git tracks, so it fails on files that were copied and not yet added.",
    ],
  };
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
  const lines = String(run.stderr ?? "").split("\n").map((l) => l.replace(/^\s+- /, "").trim()).filter((l) => l !== "");
  // The loader's first line names the file; the rest are the problems.
  if (lines.length > 1 && /is invalid:$/.test(lines[0])) return lines.slice(1);
  return lines.length > 0 ? lines : ["the configuration loader could not be run"];
}

// --- CLI ---------------------------------------------------------------------------

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    if (flag === "--allow-unmerged") {
      out.allowUnmerged = true;
      continue;
    }
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
    console.error("usage: node sync.mjs --from <upstream checkout> [--to <repository>] [--repository owner/name] [--allow-unmerged]");
    process.exit(1);
  }
  try {
    const result = sync({ from: args.from, to: args.to ?? defaultRoot(), repository: args.repository, allowUnmerged: args.allowUnmerged === true });
    console.log(`Copied ${result.copied.length} files, deleted ${result.deleted.length}.`);
    for (const rel of result.deleted) console.log(`  deleted: ${rel}`);
    console.log(`${UPSTREAM_PATH}: ${result.upstream.repository} at ${result.upstream.commit}, manifest digest ${result.upstream.manifestDigest}`);
    for (const w of result.warnings) console.log(`\nWARNING: ${w}`);
    if (result.configProblems.length === 0) {
      console.log("\nThe configuration loads under the new schema.");
    } else {
      console.log(`\nThe configuration does not load under the new schema. Edit ${HARNESS_DIR}/config.json:`);
      for (const p of result.configProblems) console.log(`  - ${p}`);
    }
    console.log("\nTo do:");
    result.todo.forEach((line, i) => console.log(`  ${i + 1}. ${line}`));
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
}
