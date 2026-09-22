// Did THIS change PUT this line here, carrying code that already existed?
// Answered with git, not with a model.
//
// WHY THIS EXISTS. #573 deliberately stopped handing the verifier the diff: the
// lens that raised a finding reasoned FROM the diff, so a verifier reading the
// same diff inherits its misreadings. That independence is right and stays. But
// it leaves the verifier standing in the branch checkout with no view of the
// base — and from there a line a refactor MOVED and a line the change WROTE are
// byte-identical. The `pre-existing` refutation ground is file-scoped ("lives in
// a file this change did not touch"), so a function relocated into a new file
// legitimately fails it, and every finding on moved code is reported as new.
//
// That happened on #578: a PR that lifted `classifyResult` verbatim out of
// review-panel.mjs into ask.mjs had every pre-existing bug in that function
// re-reported as introduced-here, and the verifier confirmed them all because,
// from where it stands, they ARE present.
//
// WHAT THIS DELIBERATELY DOES NOT DO. "The code at this location is old" does
// NOT mean "this change did not cause the defect". A new guard bypassed by an
// untouched call site, a new caller reaching an old unguarded path, a test that
// stopped covering a branch — those defects live entirely in pre-base lines, and
// the blast-radius lens exists to find them. Its rubric orders it to cite the
// bypassing site, "not the diff line that introduced the guard"; correctness and
// security carry the same out-of-diff mandate. Demoting on line age alone would
// route that entire class off the gate — the opposite of the goal.
//
// So the question is narrower than "is this code old". It is: DID THIS CHANGE
// PUT THIS LINE HERE, and was the code it put here already in the tree? Two
// blames answer it, and both are needed:
//
//   plain  `git blame`            → which commit put this line at this offset
//   moved  `git blame -w -C -C -C` → which commit originally WROTE that content
//
// A line the change did not add is `pre-existing` and is NEVER demoted: whether
// it is a bug is exactly what the lens was asked, and it is not this module's
// business. Only a line the change ADDED, whose content predates the base, is
// `relocated` — and that alone demotes. That is the #578 shape and nothing else.
//
// THE ONE RULE: every uncertain path returns `unknown`, and `unknown` keeps the
// finding blocking. Demotion requires BOTH blames to have answered.

import { execFile } from "node:child_process";
import { repoScopedEnv } from "./git-env.mjs";
import { promisify } from "node:util";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseCitations } from "./citation.mjs";

const execFileAsync = promisify(execFile);

/**
 * Where the code a finding points at came from.
 *   introduced   — this change added the line, and the content is new
 *   relocated    — this change added the line, but the content predates the base
 *   pre-existing — this change did not add this line
 *   unknown      — could not tell (no location, no base, git unavailable/failed)
 * ONLY `relocated` demotes. `introduced`, `pre-existing` and `unknown` all keep
 * the finding on the gate, so mislabelling among those three is never a gate
 * error — it only affects how the finding reads.
 */
export const ORIGINS = ["introduced", "relocated", "pre-existing", "unknown"];

/** Origins that take a finding off the merge gate. Deliberately one element. */
export const DEMOTING_ORIGINS = new Set(["relocated"]);

// Accepts an abbreviated sha as well as a full one. The workflows always pass a
// full 40-hex `git merge-base`, but the dry-run entry point is driven by hand and
// rejecting `08c7885f9` there would make the gate silently INERT — every finding
// `unknown` — which looks identical to "nothing was relocated". Ambiguity is not
// a risk this regex should be guarding: git itself refuses an ambiguous prefix,
// and the value only ever reaches git as a rev to resolve.
const SHA = /^[0-9a-fA-F]{7,40}$/;

// `blame -C -C -C` searches for copies across every file in the commit, which on
// a large file with deep history is the one genuinely slow call here. A finding
// that costs more than this to place degrades to `unknown`, i.e. the status quo.
const GIT_TIMEOUT_MS = 10_000;
const GIT_MAX_BUFFER = 8 * 1024 * 1024;

// The content probe asks whether a line's exact text already existed in the base
// tree. On a SHORT or structural line that question is meaningless — `}`, `});`
// and `return null;` occur in almost any tree, so a finding touching one would
// probe as "already in base" and be demoted off the gate. That is the one way
// this module could lose a real finding, so the probe is consulted only for
// lines distinctive enough for a match to mean something.
const MIN_PROBE_LEN = 16;
const IDENTIFIER_RUN = /[A-Za-z_][A-Za-z0-9_]{2,}/g;
const MIN_PROBE_IDENTIFIERS = 2;

/** Is this line worth asking git about? See MIN_PROBE_LEN. */
export function isProbeableLine(text) {
  if (typeof text !== "string") return false;
  const t = text.trim();
  if (t.length < MIN_PROBE_LEN) return false;
  return (t.match(IDENTIFIER_RUN) ?? []).length >= MIN_PROBE_IDENTIFIERS;
}

/**
 * The whole decision table, as a pure function — this is what the tests pin.
 *
 * `changeAddedLine` is the load-bearing input and comes from PLAIN blame: did
 * this change put this line at this offset? When it is false the answer is
 * `pre-existing` and the finding stays on the gate no matter how old the content
 * is — that is what keeps out-of-diff findings (blast-radius, and the
 * correctness/security call-site mandate) gating.
 *
 * `contentPredatesBase` comes from MOVE-AWARE blame and `contentFoundInBase` from
 * a WHOLE-LINE search of the base tree. They answer the same question two ways,
 * and either affirmative is enough: `blame -C` reliably misses some moves (it
 * did on #578's `structured_output` line), and the text search still answers on
 * a shallow clone where blame cannot. Only `true` demotes; `false` and `null`
 * both leave the finding on the gate.
 *
 * What keeps the text search honest is the STRICTNESS of the match, not a rule
 * about when it may speak: it must be the exact same line, after trimming, on a
 * line distinctive enough to mean something (`isProbeableLine`). A substring
 * match would demote any new line whose text merely occurs inside an existing
 * one — which is both a false-demotion source and a bypass an author could aim
 * at deliberately.
 */
export function originFrom({
  hasLocation = false,
  changeAddedLine = null,
  contentPredatesBase = null,
  contentFoundInBase = null,
} = {}) {
  // Nothing to place, or plain blame could not answer: we cannot tell.
  if (!hasLocation || changeAddedLine === null) return "unknown";
  // The change did not put this line here. Whether the code is buggy is exactly
  // what the lens was asked; its age is not this module's business.
  if (changeAddedLine === false) return "pre-existing";
  // The change added this line. Did it bring content that already existed?
  if (contentPredatesBase === true || contentFoundInBase === true) return "relocated";
  return "introduced";
}

/**
 * Where does a finding point? `line` if the lens supplied one, else the first
 * `file:line` citation in its evidence that NAMES THE SAME FILE as the finding.
 *
 * The file check is not pedantry. Evidence routinely cites a second location for
 * contrast ("unlike other.mjs:7"), and pairing the finding's file with a foreign
 * file's line number produces a location that exists but means nothing — git is
 * then asked about whatever happens to sit at that offset, and an arbitrary old
 * line there would demote a genuinely new finding. A location that cannot be
 * trusted is worse than none, because none yields `unknown` and stays blocking.
 *
 * That rule is unchanged; what changed is WHERE it looks. This used to read only
 * the FIRST citation and discard it when the files disagreed, which threw away the
 * matching citation sitting two clauses later — and lenses habitually open their
 * evidence with the call site or the contract being violated before citing the file
 * the finding is filed under. Measured on the 44 blocking findings banked across the
 * open agent PRs: 24 carried a citation naming their own file and only 7 had it
 * first, so 17 findings were unlocatable for no better reason than citation order,
 * leaving both provenance gates unable to judge them.
 */
export function findingLocation(finding) {
  if (!finding || typeof finding !== "object") return null;
  const file = typeof finding.file === "string" && finding.file.trim() ? finding.file.trim() : null;
  if (Number.isInteger(finding.line) && finding.line >= 1 && file) {
    return { file, line: finding.line };
  }
  const cited = parseCitations(finding.evidence);
  if (cited.length === 0) return file ? { file, line: null } : null;
  // No file on the finding → the citation is all we have, so take the first whole.
  if (!file) return { file: cited[0].file, line: cited[0].line };
  // Both present → take the FIRST citation that AGREES on the file, not merely the
  // first citation. Comparison is on trailing path segments so `a/b.mjs` and
  // `./a/b.mjs` still match.
  //
  // This scans rather than checking only `cited[0]`, which is what this function's
  // documented contract always claimed ("the first same-file citation in its
  // evidence") and what the code did not do. Lenses habitually open their evidence
  // with the call site or the contract being violated and cite the finding's own
  // file second — a `correctness` finding on `auth.controller.ts` whose evidence
  // begins `cli-auth.store.ts:39` got no line at all, so BOTH provenance gates
  // answered `unknown` and could not judge it. Across the 44 blocking findings
  // banked on the open agent PRs that cost 17 locations, 39% of the total.
  //
  // The tradeoff is deliberate: a later same-file citation may point at context
  // rather than at the defect, so the line can be less precise than a first-citation
  // hit. That is strictly more information than the `null` it replaces, and it is
  // the same imprecision already accepted whenever the first citation matches. The
  // FIRST match is taken, not the closest or the last, because the earliest mention
  // of the filed file is the likeliest to be the primary claim.
  const match = cited.find((c) => samePath(file, c.file));
  return match ? { file, line: match.line } : { file, line: null };
}

/** Do two path strings denote the same file, allowing `./` and prefix drift? */
function samePath(a, b) {
  const norm = (p) => p.replace(/^\.\//, "").replace(/^\/+/, "");
  const x = norm(a);
  const y = norm(b);
  return x === y || x.endsWith(`/${y}`) || y.endsWith(`/${x}`);
}

/**
 * Run git. Never throws: resolves `{ok:false}` on any failure, including timeout.
 *
 * `status` is carried out because some git commands use the exit code as an
 * ANSWER rather than as an error — `grep` exits 1 for "no match", `merge-base
 * --is-ancestor` exits 1 for "no". Callers must be able to tell that from a
 * command that could not run at all, or a broken lookup gets recorded as a
 * confident negative. `null` means no exit status (spawn failure or timeout),
 * which is never an answer.
 *
 * Async on purpose. These run inside the orchestrator's per-lens `Promise.all`,
 * where every other lens is streaming an SDK session; a synchronous spawn with a
 * 10s ceiling would block the event loop and stall all of them.
 */
async function git(args, repo) {
  try {
    const { stdout } = await execFileAsync("git", args, {
      cwd: repo,
      env: repoScopedEnv(repo),
      timeout: GIT_TIMEOUT_MS,
      maxBuffer: GIT_MAX_BUFFER,
      encoding: "utf8",
    });
    return { ok: true, status: 0, stdout };
  } catch (e) {
    return { ok: false, status: typeof e?.code === "number" ? e.code : null, stdout: "" };
  }
}


/**
 * Does `baseSha` name a commit in this repo? Called ONCE by the caller so a
 * misconfigured base is reported loudly instead of silently turning every
 * finding into `unknown` — which is safe (everything keeps gating) but reads
 * exactly like "nothing was relocated", and a gate that is quietly off is worse
 * than one that is loudly off.
 */
export async function baseResolves(repo, baseSha) {
  if (!repo || typeof baseSha !== "string" || !SHA.test(baseSha)) return false;
  return (await git(["cat-file", "-e", `${baseSha}^{commit}`], repo)).ok;
}

/** First-field sha from `blame --porcelain`, or null. */
function porcelainSha(stdout) {
  const first = String(stdout).split("\n", 1)[0] ?? "";
  const sha = first.split(" ", 1)[0] ?? "";
  return SHA.test(sha) ? sha : null;
}

/**
 * Is `sha` reachable from `baseSha`? Exit 1 is git's ANSWER ("no"); any other
 * status, or none, means the lookup itself broke (unknown rev on a shallow
 * clone, timeout) and must not be recorded as a confident negative.
 */
async function isAncestor(repo, sha, baseSha) {
  if (!sha) return null;
  const r = await git(["merge-base", "--is-ancestor", sha, baseSha], repo);
  if (r.ok) return true;
  return r.status === 1 ? false : null;
}

/**
 * Answer "did this change put this line here, and was its content already in the
 * tree" for one finding location.
 *
 * `baseSha` is passed in, never guessed: this script has no idea what the base
 * branch is called, and a wrong guess would silently mis-date every finding.
 * Absent or malformed → `unknown` for everything, i.e. today's behaviour.
 *
 * `cache` is a caller-supplied Map — the same location is judged more than once
 * per round and `blame -C -C -C` is the expensive call.
 */
export async function noveltyOf({ repo, file, line, baseSha, cache }) {
  const unknown = { origin: "unknown", addedBy: null, contentSha: null, alsoAt: null };
  if (!repo || typeof file !== "string" || !file.trim()) return unknown;
  if (typeof baseSha !== "string" || !SHA.test(baseSha)) return unknown;
  // No line → no probe → `unknown`. There is deliberately no file-level
  // fallback: demoting a finding because its `file` string is absent from a
  // changed-file list is not a git answer, it is a string comparison, and a
  // `./` prefix or a path the model spelled differently would silently drop a
  // real blocker off the gate. The verifier's own `pre-existing` ground already
  // covers the file-level case, and it requires a grounded refutation to act.
  if (!Number.isInteger(line) || line < 1) return unknown;

  const key = `${file}:${line}`;
  if (cache instanceof Map && cache.has(key)) return cache.get(key);

  const result = await probe(repo, file, line, baseSha);
  if (cache instanceof Map) cache.set(key, result);
  return result;
}

async function probe(repo, file, line, baseSha) {
  const at = ["-L", `${line},${line}`, "--porcelain", "HEAD", "--", file];
  // Both blames in parallel — they are independent questions about the same line.
  // `--` before the path so a file named like a rev cannot be read as one.
  const [plain, moved] = await Promise.all([
    git(["blame", "-w", ...at], repo),
    git(["blame", "-w", "-C", "-C", "-C", ...at], repo),
  ]);

  // PLAIN blame: which commit put this line at this offset? If that commit is
  // reachable from the base, the change did not add this line.
  const plainSha = plain.ok ? porcelainSha(plain.stdout) : null;
  const plainIsOld = await isAncestor(repo, plainSha, baseSha);
  const changeAddedLine = plainIsOld === null ? null : !plainIsOld;

  // Everything below only refines HOW a line the change added got there, so
  // there is nothing to learn once we know it did not add it.
  if (changeAddedLine !== true) {
    return {
      origin: originFrom({ hasLocation: true, changeAddedLine }),
      addedBy: plainSha,
      contentSha: null,
      alsoAt: null,
    };
  }

  // MOVE-AWARE blame: the change added this line — where did its content come
  // from? An ancestor commit means the content predates the base: a move.
  const movedSha = moved.ok ? porcelainSha(moved.stdout) : null;
  const contentPredatesBase = await isAncestor(repo, movedSha, baseSha);

  // Content probe: is this exact line already in the base tree? A second,
  // independent answer to the same question, because `blame -C` demonstrably
  // misses moves (#578's `structured_output` line is one), and because it still
  // works on a shallow clone where blame cannot. It also supplies `alsoAt` — the
  // base location that makes a demotion checkable by eye, which a bare sha is
  // not. Skipped once blame has already concluded the content is older, since
  // there is nothing left to establish beyond that location.
  let contentFoundInBase = null;
  let alsoAt = null;
  const shown = await git(["show", `HEAD:${file}`], repo);
  const text = shown.ok ? (shown.stdout.split("\n")[line - 1] ?? "") : "";
  if (shown.ok && isProbeableLine(text)) {
    const found = await grepWholeLine(repo, baseSha, text.trim());
    alsoAt = found.at;
    if (contentPredatesBase !== true) contentFoundInBase = found.answered ? found.hit : null;
  }

  return {
    origin: originFrom({ hasLocation: true, changeAddedLine: true, contentPredatesBase, contentFoundInBase }),
    addedBy: plainSha,
    contentSha: movedSha,
    alsoAt,
  };
}

/**
 * Does a line whose trimmed text is EXACTLY `text` exist in the base tree?
 *
 * `git grep -F` is a substring match, so grepping `foo()` also hits
 * `not_foo()` and any comment mentioning it — on a whole-tree search that is a
 * broad net, and here a spurious hit demotes a finding off the merge gate. So
 * the grep is only a candidate filter: every hit is re-checked for whole-line
 * equality after trimming, and only an exact match counts.
 */
async function grepWholeLine(repo, baseSha, text) {
  const r = await git(["grep", "-F", "-n", "-e", text, baseSha], repo);
  if (!r.ok) return { answered: r.status === 1, hit: false, at: null };
  for (const row of r.stdout.split("\n")) {
    if (!row.trim()) continue;
    // `<rev>:<path>:<lineno>:<content>` — content may itself contain colons, so
    // split off exactly three leading fields and keep the rest intact.
    const m = /^([^:]+):(.+?):(\d+):(.*)$/.exec(row);
    if (!m) continue;
    if (m[4].trim() === text) return { answered: true, hit: true, at: `${m[1]}:${m[2]}:${m[3]}` };
  }
  return { answered: true, hit: false, at: null };
}

// --- dry run -----------------------------------------------------------------
// `node novelty.mjs --findings <json> --base-sha <sha> [--repo <dir>]`
//
// Prints the origin this module would assign to each finding, without running a
// panel or spending a token. This exists so a change to the probes can be
// checked against a REAL past review rather than only against fixtures: replay
// the findings from a PR whose disposition is already known and confirm the
// moved-code ones come out `relocated` and the rest do not. Reads only.
async function dryRun(argv) {
  const args = {};
  for (let i = 2; i < argv.length; i++) {
    if (argv[i].startsWith("--")) { args[argv[i].slice(2)] = argv[i + 1]; i++; }
  }
  const { readFileSync } = await import("node:fs");
  const repo = args.repo ?? process.cwd();
  const baseSha = args["base-sha"];
  if (!args.findings || !baseSha) {
    console.error("usage: node novelty.mjs --findings <json> --base-sha <sha> [--repo <dir>]");
    process.exit(2);
  }
  if (!(await baseResolves(repo, baseSha))) {
    console.error(`base-sha ${baseSha} does not resolve to a commit in ${repo} — every finding would read 'unknown'`);
    process.exit(2);
  }
  const raw = JSON.parse(readFileSync(args.findings, "utf8"));
  const findings = Array.isArray(raw) ? raw : (raw.findings ?? []);
  const cache = new Map();
  const tally = {};
  for (const f of findings) {
    const loc = findingLocation(f);
    const r = loc
      ? await noveltyOf({ repo, file: loc.file, line: loc.line, baseSha, cache })
      : { origin: "unknown", alsoAt: null };
    tally[r.origin] = (tally[r.origin] ?? 0) + 1;
    const where = loc ? `${loc.file}:${loc.line ?? "?"}` : "(unlocatable)";
    const gates = DEMOTING_ORIGINS.has(r.origin) ? "demoted" : "GATES";
    console.log(`${r.origin.padEnd(13)} ${gates.padEnd(8)} ${where}`);
    console.log(`              ${String(f.summary ?? "").slice(0, 96)}`);
    if (r.alsoAt) console.log(`              already at ${r.alsoAt}`);
  }
  console.log(`\n${findings.length} finding(s): ${JSON.stringify(tally)}`);
}

// Only when executed directly — same guard as review-panel.mjs, so importing
// this module for tests (or from the panel) never starts a dry run.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  dryRun(process.argv).catch((e) => { console.error(e.message); process.exit(1); });
}
