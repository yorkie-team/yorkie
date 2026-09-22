// Read a PR's check runs from the GitHub API, correctly.
//
// WHY THIS IS A MODULE AND NOT THREE COPIES. Two contracts of the check-runs API
// are non-obvious, and this repo has now got each of them wrong at least once:
//
//   1. `commits/{sha}/check-runs` returns an OBJECT (`{ total_count, check_runs }`),
//      so plain `gh api --paginate` concatenates raw per-page objects into text
//      that is NOT valid JSON. `--slurp` wraps each page as an array element
//      instead, and the caller flattens `check_runs` itself.
//   2. That list response OMITS or TRUNCATES `output.text`. Reading findings from
//      it yields nothing for a lens that had many — a truncated payload does not
//      even fail loudly, it just fails `JSON.parse` and gets skipped.
//
// Both were verified the hard way in review-round-guard.mjs, then re-broken in the
// first draft of prior-findings.mjs. Stating them once is the point of this file:
// a hand-written third copy is how one of them rots.
//
// FAIL DIRECTION. Every read here degrades to less data, never to a throw, and
// every `catch` is per-commit or per-run so one bad response cannot zero the rest.
// Both consumers can survive missing data (carry fewer prior findings; fall back
// to a full review) and neither can survive an outage of the whole panel.

import { execFileSync } from "node:child_process";

/** One `gh api` call, parsed. Injected as `api` in tests. */
export function gh(args) {
  return JSON.parse(execFileSync("gh", args, { encoding: "utf8", maxBuffer: 32 * 1024 * 1024 }));
}

/**
 * `--flag value` pairs plus positionals in `_`, shared by the CLIs in this family
 * rather than copied into each.
 *
 * Two properties worth keeping in one place: the accumulator has a NULL prototype,
 * so `--__proto__ x` writes an ordinary key instead of setting the object's
 * prototype; and positionals are collected wherever they appear, so argument order
 * is not load-bearing (reading the PR number from `argv[2]` made a flag-first
 * invocation fail with a usage error).
 *
 * `booleans` names the value-less flags, and it is DECLARED rather than inferred.
 * Inferring — "treat `--x` as boolean when the next token also starts with `--`"
 * — is what most hand-rolled parsers do and it is wrong in both directions: a
 * genuinely value-less flag at the end of argv silently becomes `undefined`
 * (`--append` parsed as falsy and harvest.mjs printed instead of writing, with an
 * exit code of 0 and no complaint), and a flag whose legitimate value begins with
 * `--` is swallowed. Callers that pass no `booleans` are unaffected, including the
 * ones that deliberately pass possibly-EMPTY strings: `""` is a value, not a flag.
 */
export function parseArgs(argv, { booleans = [] } = {}) {
  const flags = new Set(Array.isArray(booleans) ? booleans : []);
  const a = Object.create(null);
  const positional = [];
  for (let i = 2; i < (Array.isArray(argv) ? argv.length : 0); i++) {
    const v = argv[i];
    if (typeof v !== "string") continue;
    if (!v.startsWith("--")) { positional.push(v); continue; }
    const key = v.slice(2);
    if (flags.has(key)) { a[key] = true; continue; }
    a[key] = argv[i + 1];
    i++;
  }
  a._ = positional;
  return a;
}

/** Permissions that mean "may drive the loop on this repo". Mirrors the
 *  `['admin','maintain','write']` list every `@claude` workflow gates on. */
export const WRITE_PERMISSIONS = Object.freeze(["admin", "maintain", "write"]);

/**
 * A memoized "does this login have write access?" lookup.
 *
 * WHY IT EXISTS: `author_association` is not a permission. It describes the
 * commenter's relationship to the PR thread, and GitHub reports a repo
 * maintainer as `CONTRIBUTOR` whenever their org membership is private or they
 * have commits on the repo — which is what happened on #648, where four
 * `@claude rerun` commands from a Maintain-level maintainer were all ignored by
 * the round-budget floor while the command itself ran fine. The commands were
 * gated by `getCollaboratorPermissionLevel` (authoritative) and their EFFECT by
 * `author_association` (a hint), so the verb worked and its consequence was
 * silently dropped.
 *
 * Both `permission` and `role_name` are checked: the legacy field collapses
 * `maintain` to `write` on some responses, and the granular one is absent on
 * others. Accepting either matches what the workflows already do.
 *
 * MEMOIZED, including failures. A PR has many comments from few people, and a
 * login that 404s once (a deleted account, a lookup that is not permitted) must
 * not be re-asked per comment.
 *
 * Returns `true` / `false` / `null`, and `null` — could not determine — is
 * deliberately distinct from `false`. Callers decide which way an unknown falls;
 * a shared default here would hide that choice from the place that has to make it.
 */
export function permissionResolver({ api = gh, log = console.error } = {}) {
  const cache = new Map();
  return function hasWriteAccess(login) {
    const name = typeof login === "string" ? login.trim() : "";
    if (name === "") return null;
    if (cache.has(name)) return cache.get(name);
    let verdict;
    try {
      const d = api(["api", `repos/{owner}/{repo}/collaborators/${encodeURIComponent(name)}/permission`]);
      const level = String(d?.permission ?? "");
      const role = String(d?.role_name ?? "");
      verdict = WRITE_PERMISSIONS.includes(level) || WRITE_PERMISSIONS.includes(role);
    } catch (err) {
      // Unknown, not "no": the caller decides. A 404 here is genuinely "not a
      // collaborator", but a 403/5xx is "we could not ask", and this layer
      // cannot tell them apart from the CLI's exit status alone.
      log(`permission: could not resolve ${name} (${err.message}).`);
      verdict = null;
    }
    cache.set(name, verdict);
    return verdict;
  };
}

/**
 * Check runs for ONE commit, applying contract 1 from the header.
 *
 * Split out of `prCommitsWithCheckRuns` because a caller that needs the verdict on
 * a single known commit should not pay one API call per commit on the PR to get
 * it — and because a hand-rolled second copy of the `--slurp` incantation is
 * exactly what this module exists to prevent.
 */
export function commitCheckRuns(sha, opts) {
  const { api = gh } = opts && typeof opts === "object" ? opts : {};
  const pages = api([
    "api",
    `repos/{owner}/{repo}/commits/${sha}/check-runs?per_page=100`,
    "--paginate",
    "--slurp",
  ]);
  // `check_runs` is guarded with Array.isArray rather than `?? []`: a scalar or
  // object under that key would be spread into the result by flatMap and travel
  // downstream as if it were a run.
  return (Array.isArray(pages) ? pages : []).flatMap((p) =>
    Array.isArray(p?.check_runs) ? p.check_runs : [],
  );
}

/**
 * Every commit on the PR with its check runs attached: `[{ sha, checkRuns }]`.
 *
 * All commits, not just the head: the last completed run for a lens may sit on an
 * earlier commit if that lens produced no verdict in the most recent round. The
 * shape matches what `rounds.mjs::groupReviewRounds` consumes — and ONLY that.
 *
 * It is NOT enough for `countFailedReviewRounds`, which also reads `parents` and
 * `commit.committer.date`; both are dropped here. Its caller fetches
 * `pulls/{pr}/commits` itself for exactly that reason. A round count built on this
 * shape would not error — it would silently see no merges and no timestamps, and
 * count every failing commit.
 *
 * `pulls/{pr}/commits` is a bare-array endpoint, where plain `--paginate` is
 * correct (same call shape as review-round-guard.mjs's `listAll`). Only the
 * check-runs endpoint needs `--slurp`.
 */
export function prCommitsWithCheckRuns(pr, opts) {
  const { api = gh, log = console.error } = opts && typeof opts === "object" ? opts : {};
  let commits;
  try {
    commits = api(["api", "--paginate", `repos/{owner}/{repo}/pulls/${pr}/commits?per_page=100`]);
  } catch (err) {
    log(`could not list commits for #${pr} (${err.message}); treating the PR as having none.`);
    return [];
  }
  const out = [];
  for (const c of Array.isArray(commits) ? commits : []) {
    if (!c?.sha) continue;
    // PER COMMIT: one unreadable commit costs that commit's runs, not the PR's.
    try {
      out.push({ sha: c.sha, checkRuns: commitCheckRuns(c.sha, { api }) });
    } catch (err) {
      log(`could not read check runs for ${c.sha} (${err.message}); skipping that commit.`);
      // Still record the commit: `groupReviewRounds` treats a commit with no lens
      // runs as "not a round", which is the same conclusion it would reach for a
      // commit that genuinely has none. Dropping it entirely would be identical
      // here, but keeping it means the caller's commit count stays truthful.
      out.push({ sha: c.sha, checkRuns: [] });
    }
  }
  return out;
}

/** Flatten `prCommitsWithCheckRuns` output to a single run list. */
export function allCheckRuns(commits) {
  return (Array.isArray(commits) ? commits : []).flatMap((c) => c?.checkRuns ?? []);
}

/**
 * Re-fetch each selected run by id so `output.text` is the FULL payload.
 *
 * See contract 2 in the header. Bounded at one call per entry (five lenses today).
 * Per-entry `try`, falling back to the list copy rather than dropping the entry: if
 * that copy happens to be complete it parses, and if it is truncated the outcome is
 * the same skip the caller would have reached anyway.
 */
export function withFullOutput(byName, opts) {
  const { api = gh, log = console.error } = opts && typeof opts === "object" ? opts : {};
  const out = new Map();
  for (const [name, run] of byName instanceof Map ? byName : Object.entries(byName ?? {})) {
    out.set(name, run);
    if (!run?.id) continue;
    try {
      out.set(name, api(["api", `repos/{owner}/{repo}/check-runs/${run.id}`]));
    } catch (err) {
      log(`could not fetch ${name} in full (${err.message}); using the list copy.`);
    }
  }
  return out;
}
