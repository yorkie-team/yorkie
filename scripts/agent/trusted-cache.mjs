// Which jobs restore an Actions cache while they hold something worth taking.
//
// THE PROPERTY. A GitHub Actions cache entry is scoped to the ref of the run
// that saved it, and a run can restore entries from its own ref and from the
// default branch. Every agent job here runs on the default branch's ref, and
// whoever controls a job's runner can read its Actions runtime token and save
// any entry in that scope, under any key not already taken. So an entry in
// the default branch's scope is untrusted data. A job that restores one and
// then runs what it holds (a toolchain, a module cache, a binary, an image
// layer) runs untrusted code with that job's credentials.
//
// That matters where the job is trusted and holds something: it runs on a
// trigger that is not a pull request's own code (push, release, schedule,
// workflow_run, workflow_dispatch, issue_comment, pull_request_target, ...),
// and it has a secret or a write-capable token. A pull request's own jobs are
// out of scope: they already run code the branch wrote.
//
// `findTrustedCacheRestores` reads every workflow and every local action they
// reach and returns one finding per step that restores a cache in such a job.
// trusted-cache.test.mjs asserts the list is empty for this repository.
//
// `yaml` is loaded with `await import`, never statically: no module here may
// import a third-party package at the top level (ask.test.mjs).

import { existsSync, readdirSync, readFileSync } from "node:fs";
import path from "node:path";

/** Events whose jobs run the pull request's own code on the pull request's ref. */
export const PULL_REQUEST_EVENTS = new Set([
  "pull_request",
  "pull_request_review",
  "pull_request_review_comment",
  "merge_group",
]);

const WRITE_ALL = "write-all";

/** The event names a workflow's `on:` declares. */
export function eventsOf(on) {
  if (on == null) return [];
  if (typeof on === "string") return [on];
  if (Array.isArray(on)) return on.map(String);
  return Object.keys(on);
}

/**
 * An expression that is off whenever the event is not a pull request's own:
 * a pull-request-only condition (`${{ github.event_name == 'pull_request' }}`),
 * or such a condition choosing a value with an empty or false fallback
 * (`${{ github.event_name == 'pull_request' && 'pnpm' || '' }}`). This guard
 * judges only trusted events, where both evaluate to off.
 */
function offOutsidePullRequest(v) {
  if (typeof v !== "string") return false;
  const m = /^\s*\$\{\{([\s\S]*)\}\}\s*$/.exec(v);
  if (!m) return false;
  const body = m[1].trim();
  if (onlyOnPullRequest(body)) return true;
  const alt = /^([\s\S]+?)\|\|\s*(?:''|""|false)$/.exec(body);
  return alt !== null && onlyOnPullRequest(alt[1]);
}

/** `false`, the string "false", or off outside pull requests. Anything else, another expression included, may be on. */
function isFalse(v) {
  return v === false || (typeof v === "string" && v.trim().toLowerCase() === "false") || offOutsidePullRequest(v);
}

function isTrue(v) {
  return v === true || (typeof v === "string" && v.trim().toLowerCase() === "true");
}

/** Set to something other than empty or false. An expression counts as set. */
function isSet(v) {
  if (v === undefined || v === null || v === "") return false;
  return !isFalse(v);
}

/**
 * Whether a token with these permissions can write anything. `undefined` means
 * none were declared anywhere, so the repository default applies, and that
 * default may be write-all: it counts as write.
 */
export function canWrite(perms) {
  if (perms === undefined || perms === null) return true;
  if (typeof perms === "string") return perms.trim() === WRITE_ALL;
  if (typeof perms !== "object") return true;
  return Object.values(perms).some((v) => typeof v !== "string" || v.trim() === "write");
}

/** Secret names a job's text references; `GITHUB_TOKEN` is the job token and is judged by permissions. */
function secretsReferenced(value) {
  const text = JSON.stringify(value ?? null);
  const names = new Set();
  for (const m of text.matchAll(/\bsecrets\s*\.\s*([A-Za-z_][A-Za-z0-9_-]*)/g)) {
    if (m[1] !== "GITHUB_TOKEN") names.add(m[1]);
  }
  if (/\bsecrets\s*\[/.test(text)) names.add("secrets[...]");
  return [...names];
}

/** `expr` split on `op` (`&&` or `||`) outside parentheses and quotes. */
function splitTop(expr, op) {
  const parts = [];
  let depth = 0;
  let start = 0;
  let quote = null;
  for (let i = 0; i < expr.length; i++) {
    const c = expr[i];
    if (quote) {
      if (c === quote) quote = null;
      continue;
    }
    if (c === "'" || c === '"') quote = c;
    else if (c === "(") depth++;
    else if (c === ")") depth--;
    else if (depth === 0 && expr.startsWith(op, i)) {
      parts.push(expr.slice(start, i).trim());
      start = i + op.length;
      i += op.length - 1;
    }
  }
  parts.push(expr.slice(start).trim());
  return parts;
}

/** `(x)` without its parentheses, when they enclose the whole of it. */
function unwrap(expr) {
  let e = expr.trim();
  while (e.startsWith("(") && e.endsWith(")")) {
    let depth = 0;
    let closesEarly = false;
    for (let i = 0; i < e.length - 1; i++) {
      if (e[i] === "(") depth++;
      else if (e[i] === ")") depth--;
      if (depth === 0) {
        closesEarly = true;
        break;
      }
    }
    if (closesEarly) break;
    e = e.slice(1, -1).trim();
  }
  return e;
}

/**
 * Whether an expression can only be true on a pull request's own event. It
 * recognises `github.event_name == '<pull request event>'`, a disjunction of
 * such comparisons, and a conjunction with one of those among its terms.
 * Anything else is treated as able to be true on a trusted trigger.
 */
export function onlyOnPullRequest(cond) {
  if (typeof cond !== "string") return false;
  let expr = cond.trim();
  const wrapped = /^\$\{\{([\s\S]*)\}\}$/.exec(expr);
  if (wrapped) expr = wrapped[1];
  expr = unwrap(expr);
  const ors = splitTop(expr, "||");
  if (ors.length > 1) return ors.every((p) => onlyOnPullRequest(p));
  const ands = splitTop(expr, "&&");
  if (ands.length > 1) return ands.some((p) => onlyOnPullRequest(p));
  const m = /^github\.event_name\s*==\s*['"]([a-z_]+)['"]$/.exec(expr);
  return m !== null && PULL_REQUEST_EVENTS.has(m[1]);
}

function splitUses(uses) {
  const at = uses.lastIndexOf("@");
  const name = (at < 0 ? uses : uses.slice(0, at)).toLowerCase();
  const ref = at < 0 ? "" : uses.slice(at + 1);
  const major = /^v(\d+)(?:\.|$)/.exec(ref);
  return { name, ref, major: major ? Number(major[1]) : undefined };
}

/**
 * Whether text names a `cache-from` that is, or may be, the Actions cache:
 * `type=gha` on the same logical line (backslash continuations joined), or a
 * value taken from a variable or an expression, which may hold it.
 */
const GHA_CACHE_FROM = (text) =>
  typeof text === "string" &&
  text
    .replace(/\\\r?\n/g, " ")
    .split("\n")
    .some((line) => /cache-from/.test(line) && (/type=gha\b/.test(line) || /cache-from[=:\s"']+\$/.test(line)));

/**
 * Why a single step restores an Actions cache, or null. `sccacheGha` is true
 * when sccache's Actions-cache backend is switched on for the job.
 */
export function stepRestoresCache(step, { sccacheGha = false } = {}) {
  const w = step.with ?? {};
  if (typeof step.run === "string" && GHA_CACHE_FROM(step.run)) {
    return "runs a build with `--cache-from` reading, or able to read, `type=gha`";
  }
  if (typeof step.uses !== "string") return null;
  // Only a version tag counts. A commit pin's `# vN` comment is not checked
  // against the commit, so it proves nothing.
  const { name, major } = splitUses(step.uses);
  if (name.startsWith("./") || name.startsWith("docker://")) return null;
  for (const [k, v] of Object.entries(w)) {
    if ((k === "cache-from" && typeof v === "string" && /type=gha\b|\$\{\{/.test(v)) || (k !== "cache-from" && GHA_CACHE_FROM(v))) {
      return `\`${k}\` reads, or may read, the Actions cache (\`type=gha\`)`;
    }
  }
  if (name === "actions/cache" || name === "actions/cache/restore") {
    return isTrue(w["lookup-only"]) ? null : `${name} restores an entry`;
  }
  if (name === "actions/setup-go") {
    // Default on since v4. The switch is required at every version: it is
    // harmless before v4, and a ref can be bumped without anyone looking here.
    return isFalse(w.cache) ? null : "actions/setup-go restores the module and build cache unless `cache: false`";
  }
  if (name === "actions/setup-node") {
    if (isSet(w.cache)) return "actions/setup-node restores the package manager cache (`cache:`)";
    // v5 and later cache on their own when package.json names a packageManager.
    // Only a tag below v5 shows the step predates that.
    const auto = !(major !== undefined && major < 5);
    if (auto && !isFalse(w["package-manager-cache"])) {
      return "actions/setup-node v5+ caches automatically unless `package-manager-cache: false`";
    }
    return null;
  }
  if (name.startsWith("actions/setup-")) {
    return isSet(w.cache) ? `${name} restores a cache (\`cache:\`)` : null;
  }
  if (name === "ruby/setup-ruby") {
    return isSet(w["bundler-cache"]) ? "ruby/setup-ruby restores the bundle (`bundler-cache:`)" : null;
  }
  if (name === "pnpm/action-setup") {
    return isSet(w.cache) ? "pnpm/action-setup restores the store (`cache:`)" : null;
  }
  if (name === "swatinem/rust-cache") {
    return isTrue(w["lookup-only"]) ? null : "Swatinem/rust-cache restores the target and registry cache";
  }
  if (name === "mozilla-actions/sccache-action") {
    return sccacheGha ? "sccache reads the Actions cache (`SCCACHE_GHA_ENABLED`)" : null;
  }
  if (name === "oven-sh/setup-bun") {
    return isTrue(w["no-cache"]) ? null : "oven-sh/setup-bun restores the Bun executable unless `no-cache: true`";
  }
  if (name === "anthropics/claude-code-action") {
    // Its own Bun install is oven-sh/setup-bun with the cache on; it is skipped
    // only when the caller hands it a Bun.
    return isSet(w.path_to_bun_executable)
      ? null
      : "anthropics/claude-code-action installs Bun from the Actions cache unless `path_to_bun_executable` is set";
  }
  if (name === "docker/setup-buildx-action") {
    return isFalse(w["cache-binary"]) ? null : "docker/setup-buildx-action restores the buildx binary unless `cache-binary: false`";
  }
  if (name === "docker/setup-qemu-action") {
    return isFalse(w["cache-image"]) ? null : "docker/setup-qemu-action restores the binfmt image unless `cache-image: false`";
  }
  return null;
}

/** sccache's Actions-cache backend, switched on anywhere the job's steps can see. */
function sccacheGhaEnabled(...scopes) {
  for (const s of scopes) {
    if (s && typeof s === "object" && isSet(s.SCCACHE_GHA_ENABLED)) return true;
  }
  return false;
}

/**
 * The checker over a set of files. `workflowPaths` are the repository-relative
 * workflow files; `readText(rel)` returns a file's text or null. `parse` is a
 * YAML parser (the `yaml` package's `parse`).
 */
export function checkWorkflows({ workflowPaths, readText, parse }) {
  const findings = [];
  const docs = new Map();
  const load = (rel) => {
    if (!docs.has(rel)) {
      const text = readText(rel);
      docs.set(rel, text === null ? null : parse(text) ?? {});
    }
    return docs.get(rel);
  };

  // A local action: `./dir` names `dir/action.yml`. A checkout of this same
  // repository under a subdirectory (`./.trusted-toolchain/.github/actions/x`)
  // is read from this tree's `.github/...`, the copy this guard can see.
  const resolveAction = (uses) => {
    const rel = path.posix.normalize(uses.slice(2)).replace(/\/$/, "");
    const candidates = [rel];
    const gh = rel.indexOf("/.github/");
    if (gh >= 0) candidates.push(rel.slice(gh + 1));
    for (const c of candidates) {
      for (const f of ["action.yml", "action.yaml"]) {
        const p = `${c}/${f}`;
        if (load(p) !== null) return p;
      }
    }
    return null;
  };

  // The workflow file of this repository a job-level `uses:` calls, or null for
  // another repository's. `./.github/workflows/x.yml` is local. So is
  // `<owner>/<repo>/.github/workflows/x.yml@ref` whenever this tree has that
  // file: the configuration names the repository but not its owner, and the
  // checker reads nothing from the environment, so it cannot tell this
  // repository's full name from another's. Reading such a call as local can
  // only put the file under a caller's trigger and credentials; a same-named
  // workflow elsewhere is judged by this tree's copy.
  const known = new Set(workflowPaths);
  const localWorkflow = (uses) => {
    if (uses.startsWith("./")) return path.posix.normalize(uses.slice(2).replace(/@.*$/, ""));
    const m = /^[^/]+\/[^/]+\/(\.github\/workflows\/[^@]+)@/.exec(uses);
    return m && known.has(path.posix.normalize(m[1])) ? path.posix.normalize(m[1]) : null;
  };

  // Callers of each local reusable workflow, so a callee is judged in its callers' context.
  const callers = new Map();
  for (const rel of workflowPaths) {
    const wf = load(rel);
    for (const [jobId, job] of Object.entries(wf?.jobs ?? {})) {
      const callee = typeof job?.uses === "string" ? localWorkflow(job.uses) : null;
      if (callee !== null) {
        if (!callers.has(callee)) callers.set(callee, []);
        callers.get(callee).push(`${rel}#${jobId}`);
      }
    }
  }

  const scanSteps = (steps, where, scc, seen) => {
    for (const [i, step] of (steps ?? []).entries()) {
      if (!step || typeof step !== "object") continue;
      if (onlyOnPullRequest(step.if)) continue;
      const label = `${where} › step ${JSON.stringify(step.name ?? step.uses ?? step.id ?? `#${i + 1}`)}`;
      const sccache = scc || sccacheGhaEnabled(step.env);
      const why = stepRestoresCache(step, { sccacheGha: sccache });
      if (why) findings.push(`${label}: ${why}`);
      if (typeof step.uses === "string" && step.uses.startsWith("./") && !step.uses.endsWith(".yml") && !step.uses.endsWith(".yaml")) {
        const actionFile = resolveAction(step.uses);
        if (actionFile === null) {
          findings.push(`${label}: local action ${step.uses} cannot be read, so what it restores cannot be checked`);
          continue;
        }
        if (seen.has(actionFile)) continue;
        const action = load(actionFile);
        if (action?.runs?.using === "composite") {
          scanSteps(action.runs.steps, `${label} → ${actionFile}`, sccache, new Set([...seen, actionFile]));
        }
      }
    }
  };

  // ctx: { trusted, callerPerms (undefined = none declared), callerHasSecrets, chain }
  const scanWorkflow = (rel, ctx) => {
    const wf = load(rel);
    if (!wf) {
      findings.push(`${ctx.chain.join(" → ")}: reusable workflow ${rel} cannot be read`);
      return;
    }
    for (const [jobId, job] of Object.entries(wf.jobs ?? {})) {
      if (!job || typeof job !== "object") continue;
      const where = `${[...ctx.chain, rel].join(" → ")} › job ${jobId}`;
      if (!ctx.trusted || onlyOnPullRequest(job.if)) continue;
      const declared = job.permissions ?? wf.permissions;
      // A called workflow's token is capped by its caller's.
      const writes = canWrite(declared) && (ctx.callerPerms === undefined || canWrite(ctx.callerPerms));
      const effective = declared ?? ctx.callerPerms;

      if (typeof job.uses === "string") {
        const callee = localWorkflow(job.uses);
        if (callee === null) continue; // another repository's workflow: not visible here
        // GitHub refuses a workflow that calls itself, directly or not; so does this.
        if ([...ctx.chain.map((c) => c.split("#")[0]), rel].includes(callee)) {
          findings.push(`${where}: calls ${callee}, which is already in its call chain`);
          continue;
        }
        const passes = job.secrets === "inherit" || (job.secrets && typeof job.secrets === "object" && Object.keys(job.secrets).length > 0);
        scanWorkflow(callee, {
          trusted: true,
          callerPerms: effective,
          callerHasSecrets: ctx.callerHasSecrets && Boolean(passes),
          chain: [...ctx.chain, `${rel}#${jobId}`],
        });
        continue;
      }

      const secrets = ctx.callerHasSecrets ? secretsReferenced({ env: wf.env, job }) : [];
      if (!writes && secrets.length === 0) continue;
      const scc = sccacheGhaEnabled(wf.env, job.env) ||
        (job.steps ?? []).some((s) => typeof s?.run === "string" && /SCCACHE_GHA_ENABLED=(?!false\b)/.test(s.run));
      scanSteps(job.steps, where, scc, new Set());
    }
  };

  for (const rel of workflowPaths) {
    const wf = load(rel);
    if (!wf) continue;
    const events = eventsOf(wf.on);
    const own = events.filter((e) => e !== "workflow_call");
    if (own.length > 0) {
      scanWorkflow(rel, {
        trusted: own.some((e) => !PULL_REQUEST_EVENTS.has(e)),
        callerPerms: undefined,
        callerHasSecrets: true,
        chain: [],
      });
    }
    // Reusable with no caller in this repository: nothing here says on what
    // trigger, with what token or which secrets it runs, so it is judged as
    // trusted, with secrets, and with the default token. (A call from another
    // repository runs in that repository's cache scope, not this one's.)
    if (events.includes("workflow_call") && !callers.has(rel)) {
      scanWorkflow(rel, { trusted: true, callerPerms: undefined, callerHasSecrets: true, chain: [] });
    }
  }
  return [...new Set(findings)];
}

/** Every workflow file under `.github/workflows`, repository-relative. */
export function workflowFiles(root) {
  const dir = path.join(root, ".github", "workflows");
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .filter((f) => f.endsWith(".yml") || f.endsWith(".yaml"))
    .sort()
    .map((f) => `.github/workflows/${f}`);
}

/** The checker over the repository at `root`. */
export async function findTrustedCacheRestores(root) {
  const { parse } = await import("yaml");
  const base = path.resolve(root);
  const readText = (rel) => {
    const abs = path.resolve(base, rel);
    if (!abs.startsWith(base + path.sep) || !existsSync(abs)) return null;
    return readFileSync(abs, "utf8");
  };
  return checkWorkflows({ workflowPaths: workflowFiles(root), readText, parse });
}
