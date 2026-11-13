// What the shared half of the harness is, as bytes: a manifest of it, a check
// that a tree still matches, and a check against the repository it was copied
// from.
//
// THE CORE is every file in this package and every
// `.github/workflows/agent-*.yml`. It is meant to be the same bytes in every
// repository that runs the harness; what differs lives in
// `.github/agent-harness/` and `.github/actions/agent-toolchain/`. A copy that
// has been edited by hand is a fork nobody decided to make, and nothing about
// it looks wrong until the next sync overwrites the edit or, worse, does not.
//
// `MANIFEST.sha256`, beside this file, lists the core: one `<sha256>  <path>`
// line per file, sorted by path. Three checks rest on it, each catching what
// the one before cannot:
//
//   1. THE TREE MATCHES THE MANIFEST (`verify`, and a test in this package).
//      Editing a core file without regenerating the manifest fails `npm test`.
//      It does not stop someone regenerating it.
//   2. THE MANIFEST IS THE ONE THAT WAS ADOPTED. `.github/agent-harness/
//      upstream.json` records, in a repository that copied the core, where
//      from and the digest of the manifest it copied. A test compares. So a
//      copy can only drift through a visible edit to that file.
//   3. THE RECORDED DIGEST IS UPSTREAM'S (`upstream`, run by
//      `agent-scripts.yml`). It fetches the manifest from the recorded
//      repository at the recorded commit and compares, so `upstream.json`
//      cannot simply be edited to agree with a drifted copy. It also says, as
//      a warning, when upstream's default branch has moved on.
//
// A fourth thing is not a check of the copy at all: `upstream-notify.mjs`
// opens an issue in a copy whose upstream has a newer core, so that the
// warning above is not the only place it is said.
//
// WHICH FILES, WITHOUT ASKING GIT. `verify` runs wherever `npm test` does,
// which includes a copy of a tree with no `.git` at all, so "tracked" is
// decided by rule rather than by `git ls-files`: under this package, every
// file except `MANIFEST.sha256` itself, anything inside a `node_modules`
// directory, and any file or directory whose name starts with a dot. The
// package tracks no dotfile, and a test holds it to that. A stray untracked
// file therefore shows up as "added" — which is the right answer to a file in
// the core that the manifest does not know.
//
// Usage:
//   node manifest.mjs build      write MANIFEST.sha256 from the tree
//   node manifest.mjs verify     exit 1, naming each path added, removed or changed
//   node manifest.mjs digest     print the sha256 of MANIFEST.sha256
//   node manifest.mjs upstream   compare with the repository upstream.json names

import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, realpathSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { HARNESS_DIR, isRepoSlug } from "./config.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));

/** The package and the workflows, as paths from a repository's root. */
export const PACKAGE_DIR = "scripts/agent";
export const WORKFLOW_DIR = ".github/workflows";
export const MANIFEST_FILE = "MANIFEST.sha256";
export const MANIFEST_PATH = `${PACKAGE_DIR}/${MANIFEST_FILE}`;
// Beside the configuration, and located through `config.mjs` like it: that
// module is the one place the directory is named.
export const UPSTREAM_PATH = `${HARNESS_DIR}/upstream.json`;

const isAgentWorkflow = (name) => /^agent-[A-Za-z0-9._-]+\.ya?ml$/.test(name);

/** Is `rel` (from the repository root, `/`-separated) a path the core may hold? */
export function isCorePath(rel) {
  if (typeof rel !== "string" || rel === "" || rel.includes("\\") || rel.startsWith("/")) return false;
  const parts = rel.split("/");
  if (parts.some((p) => p === "" || p === "." || p === ".." || p === "node_modules")) return false;
  if (rel === MANIFEST_PATH) return false;
  if (parts.length === 3 && rel.startsWith(`${WORKFLOW_DIR}/`)) return isAgentWorkflow(parts[2]);
  if (!rel.startsWith(`${PACKAGE_DIR}/`)) return false;
  // Nothing dot-named under the package: not a file, not a directory.
  return parts.slice(2).every((p) => !p.startsWith("."));
}

/** The root of the checkout this copy of the package sits in. */
export function defaultRoot(here = HERE) {
  return path.resolve(here, "..", "..");
}

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

/** Every core path under `root`, sorted. */
export function corePaths(root) {
  const found = [];
  const walk = (dir, rel) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      if (e.name === "node_modules" || e.name.startsWith(".")) continue;
      const childRel = `${rel}/${e.name}`;
      // Regular files only. A link would be hashed as whatever it points at,
      // which is not a statement about this tree.
      if (e.isSymbolicLink()) throw new Error(`manifest: ${childRel} is a symbolic link; the core holds regular files only`);
      if (e.isDirectory()) walk(path.join(dir, e.name), childRel);
      else if (e.isFile()) found.push(childRel);
      else throw new Error(`manifest: ${childRel} is not a regular file; the core holds regular files only`);
    }
  };
  const pkg = path.join(root, PACKAGE_DIR);
  if (existsSync(pkg)) walk(pkg, PACKAGE_DIR);
  const wf = path.join(root, WORKFLOW_DIR);
  if (existsSync(wf)) {
    for (const e of readdirSync(wf, { withFileTypes: true })) {
      if (!isAgentWorkflow(e.name)) continue;
      if (!e.isFile()) throw new Error(`manifest: ${WORKFLOW_DIR}/${e.name} is not a regular file; the core holds regular files only`);
      found.push(`${WORKFLOW_DIR}/${e.name}`);
    }
  }
  // Byte order, not locale order: the same list on every machine.
  return found.filter(isCorePath).sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

/** The manifest text for the tree at `root`. */
export function buildManifest(root) {
  return corePaths(root)
    .map((rel) => `${sha256(readFileSync(path.join(root, rel)))}  ${rel}\n`)
    .join("");
}

/**
 * A manifest's entries, as a `Map` of path to hash. Throws on a line that is
 * not `<64 hex>  <core path>`, on a repeat, and on a list that is not sorted —
 * a manifest is compared by digest, so two orderings of one list must not both
 * be valid.
 */
export function parseManifest(text) {
  if (typeof text !== "string" || text === "") throw new Error("manifest: empty");
  // Said by name, because the alternative is a complaint about a line that
  // looks perfectly well formed: with `core.autocrlf` on and no attribute
  // telling git to leave these files alone, a checkout rewrites every line
  // ending, the manifest's included.
  if (text.includes("\r")) throw new Error(`manifest: ${LINE_ENDINGS}`);
  if (!text.endsWith("\n")) throw new Error("manifest: does not end with a newline");
  const entries = new Map();
  let previous = "";
  for (const line of text.slice(0, -1).split("\n")) {
    const m = /^([0-9a-f]{64}) {2}(\S.*)$/.exec(line);
    if (!m) throw new Error(`manifest: not a \`<sha256>  <path>\` line: ${JSON.stringify(line.slice(0, 120))}`);
    const [, hash, rel] = m;
    if (!isCorePath(rel)) throw new Error(`manifest: lists ${JSON.stringify(rel)}, which is not a file of the core`);
    if (entries.has(rel)) throw new Error(`manifest: lists ${rel} twice`);
    if (!(previous < rel)) throw new Error(`manifest: not sorted at ${rel}`);
    previous = rel;
    entries.set(rel, hash);
  }
  return entries;
}

export const readManifest = (root) => {
  const file = path.join(root, MANIFEST_PATH);
  try {
    return readFileSync(file, "utf8");
  } catch (err) {
    throw new Error(`manifest: cannot read ${file} (${err.code || err.message}); run \`node manifest.mjs build\``);
  }
};

/** The digest a copy is identified by: sha256 of the manifest file. */
export const manifestDigest = (text) => sha256(Buffer.from(text, "utf8"));

/**
 * How the tree at `root` differs from its manifest: `{ added, removed,
 * changed }`, each a sorted list of paths. All empty means it matches.
 */
export function compareTree(root, manifestText = readManifest(root)) {
  const recorded = parseManifest(manifestText);
  const built = buildManifest(root);
  if (built === "") throwEmpty(root);
  const actual = parseManifest(built);
  const added = [...actual.keys()].filter((p) => !recorded.has(p));
  const removed = [...recorded.keys()].filter((p) => !actual.has(p));
  const changed = [...actual.keys()].filter((p) => recorded.has(p) && recorded.get(p) !== actual.get(p));
  return { added, removed, changed };
}
function throwEmpty(root) {
  throw new Error(`manifest: no core files under ${root}`);
}

/**
 * The attributes a repository's root `.gitattributes` must set, so that no
 * checkout converts the core's line endings. The manifest is over bytes.
 */
export const GITATTRIBUTES_LINES = Object.freeze([`${PACKAGE_DIR}/** -text`, `${WORKFLOW_DIR}/agent-*.yml -text`]);

const LINE_ENDINGS =
  "the file has CR line endings. The shared files are compared byte for byte and must be checked out unconverted: " +
  `the repository's root .gitattributes needs \`${GITATTRIBUTES_LINES.join("` and `")}\`, ` +
  "and a checkout made before that was added needs `git add --renormalize .` or a fresh clone.";

/**
 * Why files that "changed" may not have been edited at all: a sentence when
 * any of them now has CRLF line endings, else "". None of the core does.
 */
export function lineEndingHint(root, changed) {
  const converted = changed.filter((rel) => {
    try {
      return readFileSync(path.join(root, rel)).includes("\r\n");
    } catch {
      return false;
    }
  });
  return converted.length === 0 ? "" : `${converted.length} of the changed files have CRLF line endings: ${LINE_ENDINGS}`;
}

/** `compareTree`'s result as lines for a person, or `[]` when it matches. */
export function describeDifference({ added, removed, changed }) {
  return [
    ...changed.map((p) => `changed: ${p}`),
    ...added.map((p) => `added (not in the manifest): ${p}`),
    ...removed.map((p) => `removed (in the manifest, not in the tree): ${p}`),
  ];
}

// --- upstream.json ---------------------------------------------------------------
//
// Per repository. In the repository the core is written in:
//
//   { "repository": null }
//
// and in one that copied it:
//
//   { "repository": "owner/name", "commit": "<40 hex>", "manifestDigest": "<64 hex>" }
//
// Every key is required and no other is allowed, for the reason the
// configuration is strict: a typo must not read as "that value is absent".

/** Validate a parsed `upstream.json`. Returns it, or throws naming every problem. */
export function validateUpstream(value) {
  const problems = [];
  const isObject = value !== null && typeof value === "object" && !Array.isArray(value);
  if (!isObject) throw new Error("upstream.json must be an object");
  if (!Object.hasOwn(value, "repository")) problems.push("repository is missing (null in the repository the core is written in)");
  const allowed = value.repository === null ? ["repository"] : ["repository", "commit", "manifestDigest"];
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) problems.push(`${key} is not a known key${value.repository === null ? " when repository is null" : ""}`);
  }
  if (Object.hasOwn(value, "repository") && value.repository !== null) {
    if (!isRepoSlug(value.repository)) problems.push("repository must be null or `owner/name`");
    if (typeof value.commit !== "string" || !/^[0-9a-f]{40}$/.test(value.commit)) problems.push("commit must be a full 40-character commit id, lower-case");
    if (typeof value.manifestDigest !== "string" || !/^[0-9a-f]{64}$/.test(value.manifestDigest)) problems.push("manifestDigest must be a sha256, 64 lower-case hex characters");
  }
  if (problems.length > 0) throw new Error(`upstream.json is invalid:\n${problems.map((p) => `  - ${p}`).join("\n")}`);
  return value;
}

export function readUpstream(root) {
  const file = path.join(root, UPSTREAM_PATH);
  let text;
  try {
    text = readFileSync(file, "utf8");
  } catch (err) {
    throw new Error(`cannot read ${file} (${err.code || err.message}); every repository that runs the harness has one`);
  }
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch (err) {
    throw new Error(`${file} is not valid JSON (${err.message})`);
  }
  return validateUpstream(parsed);
}

/**
 * Does the copy at `root` still carry the manifest it adopted? `null` when it
 * does, or when `root` is where the core is written; otherwise the sentence
 * that says what differs.
 */
export function adoptionProblem(root) {
  const upstream = readUpstream(root);
  if (upstream.repository === null) return null;
  const local = manifestDigest(readManifest(root));
  if (local === upstream.manifestDigest) return null;
  return (
    `the core's manifest digest is ${local}, but ${UPSTREAM_PATH} records ${upstream.manifestDigest} ` +
    `(${upstream.repository} at ${upstream.commit}). The shared files were changed without a sync: ` +
    "revert the change, or sync from upstream with `node scripts/agent/sync.mjs --from <checkout>`."
  );
}

// --- the network check -------------------------------------------------------------

/**
 * Compare this copy with the repository it names. Returns `{ ok, lines,
 * warnings }`; never throws for something the network did.
 *
 * FOUR QUESTIONS, and the first three FAIL CLOSED: if upstream cannot be
 * asked, the answer is a failure with the URL and the status in it, never a
 * pass. A transient refusal (429, 5xx, a network error) is retried a few
 * times first.
 *
 *   1. Does this copy still carry the recorded manifest? (No network.)
 *   2. Is the recorded commit ON UPSTREAM'S DEFAULT BRANCH? A commit id alone
 *      proves little: every head a pull request ever had stays fetchable from
 *      the repository it was opened against, merged or not, so "this commit
 *      exists upstream" is true of code upstream never accepted. The default
 *      branch is read from the repository, not assumed, and the comparison
 *      must say the commit is `behind` it or `identical` to it. There is no
 *      fallback: a commit that cannot be placed on that branch fails.
 *   3. Is the manifest at that commit the recorded one?
 *   4. Has the default branch moved on? This one only ever WARNS, including
 *      when it cannot be asked: being behind is information, and not knowing
 *      must not red a pull request.
 *
 * `fetchText(url)` resolves to `{ status, text }`. `self` is the repository
 * this is running in, when known; `sleep(ms)` is the pause between retries.
 * A copy that passes also gets `branch`, upstream's default branch.
 */
export async function checkUpstream({ upstream, localDigest, fetchText, self = "", sleep = (ms) => new Promise((r) => setTimeout(r, ms)) }) {
  if (upstream.repository === null) {
    // Nothing here can tell the origin from a copy that says it is the
    // origin — that is pinned by each repository's own tests. What this can do
    // is put the claim, and who is making it, in the log.
    const who = self ? `${self} is` : "This is";
    return {
      ok: true,
      lines: [`${who} recorded as the repository the core is written in (${UPSTREAM_PATH}: repository is null); nothing to compare. A copy must name its upstream there instead.`],
      warnings: [],
    };
  }
  const { repository, commit, manifestDigest: recorded } = upstream;
  const lines = [];
  const warnings = [];
  const api = `https://api.github.com/repos/${repository}`;
  const manifestUrl = (ref) => `${api}/contents/${MANIFEST_PATH}${ref ? `?ref=${ref}` : ""}`;
  const fail = (line) => {
    lines.push(line);
    return { ok: false, lines, warnings };
  };
  const CLOSED = "The copy cannot be compared, so this check fails rather than passes.";

  const get = (url) => getWithRetry(url, fetchText, sleep);
  const unreachable = (url, r) =>
    r.error !== undefined
      ? `Could not fetch ${url}: ${r.error} (after ${RETRY_DELAYS_MS.length + 1} attempts). ${CLOSED}`
      : `Fetching ${url} returned HTTP ${r.status}. ${CLOSED}`;

  // 1.
  if (localDigest !== recorded) return fail(`This copy's manifest digest is ${localDigest}; ${UPSTREAM_PATH} records ${recorded}.`);

  // 2.
  const repo = await get(api);
  if (repo.status !== 200) return fail(unreachable(api, repo));
  let branch;
  try {
    branch = JSON.parse(repo.text).default_branch;
  } catch {
    branch = undefined;
  }
  if (typeof branch !== "string" || !/^[\w./-]+$/.test(branch)) return fail(`${api} did not say what ${repository}'s default branch is. ${CLOSED}`);
  const compareUrl = `${api}/compare/${encodeURIComponent(branch)}...${commit}`;
  const compare = await get(compareUrl);
  if (compare.status === 404) {
    return fail(`${repository} has no commit ${commit} that can be compared with its default branch (${compareUrl} returned HTTP 404). ${UPSTREAM_PATH} must record a commit on ${branch}.`);
  }
  if (compare.status !== 200) return fail(unreachable(compareUrl, compare));
  let status;
  try {
    status = JSON.parse(compare.text).status;
  } catch {
    status = undefined;
  }
  if (status !== "behind" && status !== "identical") {
    return fail(
      `The recorded commit ${commit} is not on ${repository}'s default branch (${branch}): it is ${JSON.stringify(status ?? "unknown")} relative to it. ` +
        `A copy is synced from the default branch only — a pull request's head exists upstream whether or not it was merged. Sync again from ${branch}.`,
    );
  }

  // 3.
  const pinnedUrl = manifestUrl(commit);
  const pinned = await get(pinnedUrl);
  if (pinned.status === 404) {
    return fail(`${repository} has no ${MANIFEST_PATH} at ${commit} (${pinnedUrl} returned HTTP 404). The recorded commit predates the manifest, or the record is wrong. ${CLOSED}`);
  }
  if (pinned.status !== 200) return fail(unreachable(pinnedUrl, pinned));
  const upstreamDigest = manifestDigest(pinned.text);
  if (upstreamDigest !== recorded) {
    return fail(`${repository} at ${commit} has manifest digest ${upstreamDigest}; ${UPSTREAM_PATH} records ${recorded}. The record does not describe that commit.`);
  }
  lines.push(`This copy is ${repository} at ${commit}, on ${branch} (manifest digest ${recorded}).`);
  // `branch` is handed back for `upstream-notify.mjs`, which goes on to ask
  // where that branch is now.
  const passed = { ok: true, lines, warnings, branch };

  // 4.
  const behind = `this copy is behind. Sync with \`node scripts/agent/sync.mjs --from <checkout>\`.`;
  try {
    const head = await fetchText(manifestUrl(""));
    if (head.status !== 200) warnings.push(`Could not tell whether ${repository} has moved on: its default branch's manifest returned HTTP ${head.status}.`);
    else if (manifestDigest(head.text) !== recorded) warnings.push(`${repository}'s default branch has a different manifest (digest ${manifestDigest(head.text)}); ${behind}`);
  } catch (err) {
    warnings.push(`Could not tell whether ${repository} has moved on: ${err.message}.`);
  }
  return passed;
}

/** Pauses before the second, third and fourth attempt at a fetch that must not be skipped. */
export const RETRY_DELAYS_MS = Object.freeze([1000, 3000, 9000]);

/**
 * One GET through `fetchText`, retrying a transient refusal (429, 5xx, a
 * network error). Returns `{ status, text }`, or `{ error }` when every
 * attempt threw.
 */
export async function getWithRetry(url, fetchText, sleep) {
  let last = {};
  for (let attempt = 0; attempt < RETRY_DELAYS_MS.length + 1; attempt++) {
    if (attempt > 0) await sleep(RETRY_DELAYS_MS[attempt - 1]);
    try {
      last = await fetchText(url);
      if (last.status !== 429 && !(last.status >= 500 && last.status <= 599)) return last;
    } catch (err) {
      last = { error: err.message };
    }
  }
  return last;
}

/** `fetchText` over the GitHub API: raw content, the ambient token if there is one. */
export async function githubFetchText(url) {
  const headers = { Accept: "application/vnd.github.raw+json", "User-Agent": "harness-manifest-check", "X-GitHub-Api-Version": "2022-11-28" };
  // The job's own read token, when present: it reads any public repository and
  // lifts the unauthenticated rate limit, which shared runner addresses
  // exhaust. It is sent to api.github.com and nowhere else — the URL is
  // checked, and a redirect is not followed with it.
  if (!url.startsWith("https://api.github.com/")) throw new Error(`refusing to fetch ${url}: not api.github.com`);
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(url, { headers, redirect: "error", signal: AbortSignal.timeout(30_000) });
  return { status: res.status, text: res.status === 200 ? await res.text() : "" };
}

// --- CLI ---------------------------------------------------------------------------

function isDirectRun() {
  if (!process.argv[1]) return false;
  const entry = fileURLToPath(import.meta.url);
  try {
    return realpathSync(process.argv[1]) === realpathSync(entry);
  } catch {
    return path.resolve(process.argv[1]) === path.resolve(entry);
  }
}

async function main(argv) {
  const [verb, ...extra] = argv;
  const root = defaultRoot();
  if (extra.length > 0 || !["build", "verify", "digest", "upstream"].includes(verb)) {
    console.error("usage: node manifest.mjs build | verify | digest | upstream");
    return 1;
  }
  if (verb === "build") {
    const text = buildManifest(root);
    if (text === "") throwEmpty(root);
    writeFileSync(path.join(root, MANIFEST_PATH), text);
    console.log(`${MANIFEST_PATH}: ${text.split("\n").length - 1} files, digest ${manifestDigest(text)}`);
    return 0;
  }
  if (verb === "verify") {
    const lines = describeDifference(compareTree(root));
    if (lines.length === 0) {
      console.log(`The core matches ${MANIFEST_PATH}.`);
      return 0;
    }
    console.error(`The core does not match ${MANIFEST_PATH}:\n${lines.map((l) => `  ${l}`).join("\n")}`);
    const hint = lineEndingHint(root, compareTree(root).changed);
    if (hint) console.error(hint);
    console.error("If the change is meant, it belongs upstream; in the repository the core is written in, run `node scripts/agent/manifest.mjs build`.");
    return 1;
  }
  if (verb === "digest") {
    console.log(manifestDigest(readManifest(root)));
    return 0;
  }
  const result = await checkUpstream({
    upstream: readUpstream(root),
    localDigest: manifestDigest(readManifest(root)),
    fetchText: githubFetchText,
    self: process.env.GITHUB_REPOSITORY || "",
  });
  for (const line of result.lines) console.log(result.ok ? line : `::error::${line}`);
  for (const line of result.warnings) console.log(`::warning::${line}`);
  return result.ok ? 0 : 1;
}

if (isDirectRun()) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (err) => {
      console.error(err.message);
      process.exit(1);
    },
  );
}
