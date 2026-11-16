#!/usr/bin/env node
// The issue-to-PR verb's handoff: the agent's commits, checked and pushed by a
// job the agent never ran in.
//
// WHY THE AGENT DOES NOT PUSH. A job's runner receives every secret the job
// references when it starts, and the agent has the runner to itself — a shell,
// root through passwordless `sudo`, the docker group. So a credential in the
// agent's job is a credential the agent holds, whichever step names it. The
// agent's job (`implement` in agent-implement.yml) therefore holds no
// credential that writes to the repository: it commits on a local branch, a
// step after it packs the
// commits since the base into a `git bundle`, and the bundle crosses to the
// `finish` job as an artifact. `finish` runs this file, on a fresh runner,
// from `main`'s checkout, and only then mints the token that pushes.
//
// THE BUNDLE IS ATTACKER-CONTROLLED DATA. Everything in it — its header, its
// refs, its objects, the commits' trees — was written by a process that may
// have been steered by the issue text. Nothing here checks out a tree from it,
// runs a hook, reads a configuration file from it or executes any of it. Git
// reads it into a fresh bare repository with no configuration but its own and
// checks every object, as it arrives (`transfer.fsckObjects`) and again once
// it is in (`git fsck --strict`), and then these must hold, or nothing is
// pushed:
//
//   - it is a regular file, not empty, and no larger than `MAX_BUNDLE_BYTES`;
//   - it carries exactly ONE ref, `refs/heads/agent/<issue>-<slug>`, for the
//     issue this run was asked about, and that ref names a commit;
//   - its prerequisites are in the recorded base (`git bundle verify`), which
//     `finish` takes from `prepare` — a job the agent never had either — and
//     fetches into the fresh repository itself;
//   - every commit between the base and the tip descends from the base: each
//     has a parent, and each parent is the base or another commit of the
//     branch, so no older or unrelated history comes along;
//   - there are at least one and at most `MAX_COMMITS` of them;
//   - no commit changes `.github/workflows`: its tree there must be identical
//     to every parent's. The App holds no `workflows` permission, so GitHub
//     would refuse a push that creates or edits a workflow anyway; refusing it
//     here says why, and it refuses a DELETION too, which GitHub would let
//     through.
//
// `push` then refuses when any `agent/<issue>-*` branch already exists on the
// remote — another run for the same issue got there first — and pushes the
// verified commit as a creation, refused if the branch exists by then. It
// never forces.
//
// Usage:
//   node bundle-handoff.mjs verify --bundle <file> --base <sha> --issue <n> --git-dir <dir> [--remote <url>]
//   node bundle-handoff.mjs push   --git-dir <dir> --branch <name> --head <sha> --issue <n> [--remote <url>]
//
// `verify` creates <dir>, which must not exist. Both write their results to
// $GITHUB_OUTPUT when it is set (`verdict` — ok, none, refused, or error when
// the check itself could not run — and on success `branch`, `head`, `commits`;
// `pushed`) and exit 0 only on success. The remote defaults to
// this repository on $GITHUB_SERVER_URL; a token in $GH_TOKEN authenticates
// to it, through the environment rather than the command line.

import { execFileSync } from "node:child_process";
import { appendFileSync, existsSync, lstatSync } from "node:fs";

import { repoScopedEnv } from "./git-env.mjs";
import { isDirectRun } from "./direct-run.mjs";

/** Generous for a change an agent writes in under an hour; a bundle past it is refused unread. */
export const MAX_BUNDLE_BYTES = 32 * 1024 * 1024;
/** Commits between the base and the tip. */
export const MAX_COMMITS = 100;
/** The tree no commit of the agent's may change. */
export const PROTECTED_TREE = ".github/workflows";
/** Where the verified tip is kept between `verify` and `push`. */
export const HANDOFF_REF = "refs/handoff/head";

const SHA = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const ISSUE = /^[1-9][0-9]{0,9}$/;

/** The one branch name a run for `issue` may hand over: `agent/<issue>-<slug>`, no slashes in the slug. */
export function branchPattern(issue) {
  if (!ISSUE.test(String(issue))) throw new Error(`not an issue number: ${JSON.stringify(issue)}`);
  return new RegExp(`^agent/${issue}-[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`);
}

/** Text that came from the bundle, safe to print: quoted, control characters escaped, capped. */
const shown = (s) => JSON.stringify(String(s).slice(0, 200));

/**
 * The environment every git call runs with: none of the variables that point
 * git at another repository or object store (git-env.mjs), no global or
 * system configuration, no prompt, no hooks, and — when there is a token —
 * one HTTP header for the remote's origin, passed as configuration in the
 * environment so it never appears on a command line.
 */
export function gitEnv({ remote, token } = {}) {
  const env = {
    ...repoScopedEnv(process.cwd()),
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: "/dev/null",
    GIT_TERMINAL_PROMPT: "0",
    // No background maintenance: from git 2.47 a fetch starts `maintenance
    // run --auto` DETACHED, which keeps writing into the object store after
    // the call returns and races whatever reads or removes it next.
    GIT_CONFIG_COUNT: "3",
    GIT_CONFIG_KEY_0: "core.hooksPath",
    GIT_CONFIG_VALUE_0: "/dev/null",
    GIT_CONFIG_KEY_1: "maintenance.auto",
    GIT_CONFIG_VALUE_1: "false",
    GIT_CONFIG_KEY_2: "gc.auto",
    GIT_CONFIG_VALUE_2: "0",
  };
  if (token && remote && /^https:\/\//.test(remote)) {
    const origin = `${new URL(remote).origin}/`;
    const basic = Buffer.from(`x-access-token:${token}`).toString("base64");
    env.GIT_CONFIG_COUNT = "4";
    env.GIT_CONFIG_KEY_3 = `http.${origin}.extraheader`;
    env.GIT_CONFIG_VALUE_3 = `AUTHORIZATION: basic ${basic}`;
  }
  return env;
}

/** The longest any one git call may take. A small bundle can still be slow to unpack. */
export const GIT_TIMEOUT_MS = 5 * 60 * 1000;

/** A git call that ran out of time: not a verdict on the commits, so it is thrown, not returned. */
class GitTimeout extends Error {}

function git(gitDir, args, ctx) {
  try {
    return execFileSync("git", ["--git-dir", gitDir, ...args], {
      encoding: "utf8",
      env: ctx.env,
      timeout: ctx.timeoutMs,
      killSignal: "SIGKILL",
      maxBuffer: 64 * 1024 * 1024,
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  } catch (e) {
    if (e.code === "ETIMEDOUT" || e.signal === "SIGKILL") throw new GitTimeout(`git ${args[0]} did not finish within ${ctx.timeoutMs} ms`);
    throw e;
  }
}

/** Like `git`, but a non-zero exit is an answer, not an error. A timeout still throws. */
function tryGit(gitDir, args, ctx) {
  try {
    return { ok: true, out: git(gitDir, args, ctx) };
  } catch (e) {
    if (e instanceof GitTimeout) throw e;
    return { ok: false, out: String(e.stdout ?? "").trim(), err: String(e.stderr ?? e.message ?? "").trim() };
  }
}

/**
 * Create a fresh bare repository at `gitDir` holding the base commit and
 * nothing else of the remote's. `gitDir` must not exist: a directory that
 * does is one this run did not make.
 */
export function prepareRepository({ gitDir, remote, base, token, timeoutMs = GIT_TIMEOUT_MS }) {
  if (!SHA.test(String(base))) throw new Error(`not a commit id: ${JSON.stringify(base)}`);
  if (existsSync(gitDir)) throw new Error(`${gitDir} already exists`);
  const ctx = { env: gitEnv({ remote, token }), timeoutMs };
  execFileSync("git", ["init", "--quiet", "--bare", gitDir], { env: ctx.env, timeout: timeoutMs, killSignal: "SIGKILL", stdio: ["ignore", "pipe", "pipe"] });
  git(gitDir, ["fetch", "--quiet", "--no-tags", "--depth=1", remote, base], ctx);
  git(gitDir, ["cat-file", "-e", `${base}^{commit}`], ctx);
}

/**
 * Check the bundle against a repository that holds `base`, and fetch its one
 * branch into `HANDOFF_REF`. Returns `{ verdict: "ok", branch, head, commits }`,
 * `{ verdict: "none", reason }` when there is no bundle at all (the agent
 * committed nothing on an agent branch), or `{ verdict: "refused", reason }`.
 */
export function verifyBundle({ gitDir, bundle, base, issue, maxBytes = MAX_BUNDLE_BYTES, maxCommits = MAX_COMMITS, timeoutMs = GIT_TIMEOUT_MS }) {
  const refuse = (reason) => ({ verdict: "refused", reason });
  if (!SHA.test(String(base))) return refuse(`the recorded base is not a commit id: ${shown(base)}`);
  if (!ISSUE.test(String(issue))) return refuse(`the issue number is not a number: ${shown(issue)}`);
  const ctx = { env: gitEnv(), timeoutMs };

  let st;
  try {
    st = lstatSync(bundle);
  } catch (e) {
    if (e.code === "ENOENT") return { verdict: "none", reason: "there is no bundle: the agent committed nothing on an agent branch" };
    return refuse(`the bundle could not be read (${e.code ?? e.message})`);
  }
  if (!st.isFile()) return refuse("the bundle is not a regular file");
  if (st.size === 0) return refuse("the bundle is empty");
  if (st.size > maxBytes) return refuse(`the bundle is ${st.size} bytes, over the ${maxBytes}-byte limit`);

  // The header, read by git. Exactly one ref, and the right one.
  const heads = tryGit(gitDir, ["bundle", "list-heads", bundle], ctx);
  if (!heads.ok) return refuse("git could not read it as a bundle");
  const lines = heads.out.split("\n").filter((l) => l !== "");
  if (lines.length !== 1) return refuse(`it carries ${lines.length} refs; the handoff is exactly one branch`);
  const m = /^([0-9a-f]{40}|[0-9a-f]{64}) (refs\/heads\/(\S+))$/.exec(lines[0]);
  if (!m) return refuse(`its one ref is not a branch: ${shown(lines[0])}`);
  const [, tip, ref, branch] = m;
  if (!branchPattern(issue).test(branch)) return refuse(`its branch ${shown(branch)} is not agent/${issue}-<slug>`);
  if (!tryGit(gitDir, ["check-ref-format", ref], ctx).ok) return refuse(`its branch ${shown(branch)} is not a valid ref name`);

  // Its prerequisites must already be here — that is, in the base — and the
  // objects must pass git's own checks on the way in.
  if (!tryGit(gitDir, ["bundle", "verify", "--quiet", bundle], ctx).ok) {
    return refuse("its prerequisites are not in the base, or it is not a valid bundle");
  }
  // `+` because HANDOFF_REF is this repository's own scratch ref, rewritten
  // by every verification run against it; it says nothing about the remote.
  const fetched = tryGit(
    gitDir,
    ["-c", "transfer.fsckObjects=true", "-c", "fetch.fsckObjects=true", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", bundle, `+${ref}:${HANDOFF_REF}`],
    ctx,
  );
  if (!fetched.ok) return refuse("git refused its objects");
  // AND AGAIN, OVER EVERYTHING NOW HERE, not left to whether a bundle fetch
  // honours the setting above — which has varied between git versions. The
  // workflows rule below reads a tree by path, and a tree carrying two entries
  // of one name answers with the first of them; git's object checks refuse
  // such a tree, so this one is load-bearing for that rule.
  if (!tryGit(gitDir, ["fsck", "--strict", "--no-dangling", "--no-progress"], ctx).ok) return refuse("git's object check failed on its objects");
  const head = tip;
  if (tryGit(gitDir, ["cat-file", "-t", head], ctx).out !== "commit") return refuse("its branch does not name a commit");

  // Descent: every commit's parents are the base or each other.
  if (!tryGit(gitDir, ["merge-base", "--is-ancestor", base, head], ctx).ok) return refuse("its branch is not built on the base");
  const listed = tryGit(gitDir, ["rev-list", "--parents", `${base}..${head}`], ctx);
  if (!listed.ok) return refuse("its history could not be walked from the base");
  const rows = listed.out.split("\n").filter((l) => l !== "").map((l) => l.split(" "));
  if (rows.length === 0) return refuse("it holds no commit beyond the base");
  if (rows.length > maxCommits) return refuse(`it holds ${rows.length} commits, over the limit of ${maxCommits}`);
  const mine = new Set(rows.map(([c]) => c));
  for (const [commit, ...parents] of rows) {
    if (parents.length === 0) return refuse(`commit ${commit.slice(0, 12)} has no parent, so it does not descend from the base`);
    const stray = parents.find((p) => p !== base && !mine.has(p));
    if (stray) return refuse(`commit ${commit.slice(0, 12)} has a parent (${stray.slice(0, 12)}) that is neither the base nor on the branch`);
  }

  // The workflows: identical in every commit to every parent.
  const treeOf = new Map();
  const tree = (c) => {
    if (!treeOf.has(c)) {
      const r = tryGit(gitDir, ["rev-parse", "--verify", "--quiet", `${c}:${PROTECTED_TREE}`], ctx);
      treeOf.set(c, r.ok ? r.out : "");
    }
    return treeOf.get(c);
  };
  for (const [commit, ...parents] of rows) {
    if (parents.some((p) => tree(p) !== tree(commit))) {
      return refuse(`commit ${commit.slice(0, 12)} changes ${PROTECTED_TREE}, which this verb may not touch`);
    }
  }
  return { verdict: "ok", branch, head, commits: rows.length };
}

/**
 * Push the verified commit to `branch` on the remote: no force, and not when
 * any `agent/<issue>-*` branch already exists there. Returns
 * `{ pushed: true }` or `{ pushed: false, reason }`.
 */
export function pushBranch({ gitDir, remote, branch, head, issue, token, timeoutMs = GIT_TIMEOUT_MS }) {
  if (!ISSUE.test(String(issue))) return { pushed: false, reason: `the issue number is not a number: ${shown(issue)}` };
  if (!branchPattern(issue).test(String(branch))) return { pushed: false, reason: `${shown(branch)} is not agent/${issue}-<slug>` };
  const ctx = { env: gitEnv({ remote, token }), timeoutMs };
  // Only what `verify` fetched: the commit it checked, under the ref it wrote.
  // That equality is also what makes `head` a plain commit id below.
  const verified = tryGit(gitDir, ["rev-parse", "--verify", "--quiet", HANDOFF_REF], ctx);
  if (!verified.ok || verified.out !== head) return { pushed: false, reason: "that commit is not the one the verification fetched" };

  const listed = tryGit(gitDir, ["ls-remote", "--heads", remote], ctx);
  if (!listed.ok) return { pushed: false, reason: "the remote's branches could not be listed" };
  const prefix = `refs/heads/agent/${issue}-`;
  const taken = listed.out.split("\n").map((l) => l.split("\t")[1] ?? "").filter((r) => r.startsWith(prefix));
  if (taken.length > 0) {
    return { pushed: false, reason: `${taken[0].replace("refs/heads/", "")} already exists for this issue; another run got there first` };
  }
  // CREATE-ONLY. The listing above and this push are two round trips, and a
  // plain push onto a branch that appeared between them would fast-forward it
  // when its tip is a commit this repository has. A lease on the empty value
  // closes that in two halves: git refuses the push if the branch is in the
  // push's own listing of the remote, and the update it sends names no old
  // commit, which the server applies only if the branch still does not
  // exist. The test exercises the first half.
  const pushed = tryGit(gitDir, ["push", "--quiet", `--force-with-lease=refs/heads/${branch}:`, remote, `${head}:refs/heads/${branch}`], ctx);
  if (!pushed.ok) {
    if (/stale info/.test(`${pushed.out}\n${pushed.err}`)) return { pushed: false, reason: `${branch} appeared on the remote after the check; another run got there first` };
    return { pushed: false, reason: `the push was refused: ${pushed.err.split("\n").slice(-3).join(" / ")}` };
  }
  return { pushed: true };
}

// --- CLI ---------------------------------------------------------------------------

function args(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const m = /^--([a-z-]+)$/.exec(argv[i]);
    if (!m || i + 1 >= argv.length) throw new Error(`unexpected argument: ${argv[i]}`);
    out[m[1]] = argv[++i];
  }
  return out;
}

function output(pairs) {
  const text = Object.entries(pairs).map(([k, v]) => `${k}=${v}\n`).join("");
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, text);
}

function defaultRemote() {
  const server = process.env.GITHUB_SERVER_URL || "https://github.com";
  const repo = process.env.GITHUB_REPOSITORY || "";
  if (!/^[\w.-]+\/[\w.-]+$/.test(repo)) throw new Error("GITHUB_REPOSITORY is not owner/name and no --remote was given");
  return `${server}/${repo}.git`;
}

function main(argv) {
  const [command, ...rest] = argv;
  const a = args(rest);
  const remote = a.remote ?? defaultRemote();
  const token = process.env.GH_TOKEN || "";
  if (token && process.env.GITHUB_ACTIONS === "true") {
    console.log(`::add-mask::${Buffer.from(`x-access-token:${token}`).toString("base64")}`);
  }
  if (command === "verify") {
    prepareRepository({ gitDir: a["git-dir"], remote, base: a.base, token });
    const r = verifyBundle({ gitDir: a["git-dir"], bundle: a.bundle, base: a.base, issue: a.issue });
    if (r.verdict === "ok") {
      console.log(`bundle-handoff: verified ${r.commits} commit(s) on ${r.branch} at ${r.head}`);
      output({ verdict: "ok", branch: r.branch, head: r.head, commits: r.commits });
      return 0;
    }
    console.log(`bundle-handoff: ${r.verdict === "none" ? "nothing to hand over" : "refused"}: ${r.reason}`);
    if (r.verdict === "refused") console.log("::warning::The agent's commits were not pushed: they failed the handoff checks. This step's log says which.");
    output({ verdict: r.verdict });
    return 1;
  }
  if (command === "push") {
    const r = pushBranch({ gitDir: a["git-dir"], remote, branch: a.branch, head: a.head, issue: a.issue, token });
    if (r.pushed) {
      console.log(`bundle-handoff: pushed ${a.branch}`);
      output({ pushed: "true" });
      return 0;
    }
    console.log(`bundle-handoff: not pushed: ${r.reason}`);
    console.log("::warning::The agent's verified commits were not pushed. This step's log says why.");
    return 1;
  }
  throw new Error(`unknown command: ${command ?? "(none)"}; expected verify or push`);
}

// Through `direct-run.mjs`, like every tool here: workflows run this from a
// sparse checkout beside the workspace, whose path may pass through a link.
if (isDirectRun(import.meta.url)) {
  try {
    process.exitCode = main(process.argv.slice(2));
  } catch (e) {
    // The check could not run — a usage error, or the base could not be
    // fetched. Not a verdict on the commits, and said so.
    console.log(`bundle-handoff: ${e.message}`);
    output({ verdict: "error" });
    process.exitCode = 2;
  }
}
