// The single read path for a repository's harness configuration.
//
// Everything under `scripts/agent/` is meant to be the same bytes in every
// repository that runs this harness. What differs between repositories — which
// paths define CI, which globs are source, what the mechanical lanes prove, the
// names of the bots — lives in ONE directory outside this package,
// `.github/agent-harness/`, and this module is the only thing that reads it:
//
//   config.json        every value, validated against SCHEMA below
//   coverage-note.md   the mechanical-coverage note, as prose
//
// WHERE THE DIRECTORY IS. By default it is resolved from this file's own
// location: `<root>/scripts/agent/config.mjs` reads
// `<root>/.github/agent-harness/`. That is what makes the trust model work
// without a flag: a workflow that checks the default branch out into `.trusted/`
// gets `.trusted/scripts/agent` and `.trusted/.github/agent-harness` from the
// same commit, and a script run from there can only reach the configuration
// beside it — never the pull request's copy in the workspace.
//
// Some jobs stage this package on its own (`cp -R scripts/agent
// "$RUNNER_TEMP/agent-tools"`), which drops the parent directories the default
// is resolved through. Those jobs copy the configuration from the same trusted
// checkout and name it with `AGENT_HARNESS_CONFIG_DIR`. The override must be an
// ABSOLUTE path: a relative one would be resolved against the working
// directory, which in those jobs is the pull request's checkout — the one
// place the configuration must never be read from.
//
// THE DEFAULT APPLIES ONLY IN PLACE. Two directories up from a staged copy is
// not a repository root, it is wherever the copy happened to land — the
// parent of the runner's temp directory, say — and a `.github/agent-harness`
// that happened to exist there would be read as if it were trusted. So the
// default is used only when this file really sits at `<root>/scripts/agent/`;
// anywhere else, the variable is required and its absence throws.
//
// NO DEFAULTS, ANYWHERE. A missing directory, a missing file, a missing key or
// a value of the wrong type throws. Falling back to a built-in value would let
// a gate keep running on a list that is quietly shorter than the repository's
// own — the failure would look exactly like a pass. Callers do not catch what
// this throws; an uncaught error exits non-zero, which is the visible outcome.
//
// `command.mjs` DOES NOT IMPORT THIS, and must not: route jobs check that one
// file out alone, where neither this module nor the configuration exists.
//
// Usage:
//   node config.mjs get <dotted.key>     a string is printed raw, anything
//                                        else as JSON; exit 1 on any failure

import { readFileSync, realpathSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));

export const CONFIG_DIR_ENV = "AGENT_HARNESS_CONFIG_DIR";
export const CONFIG_FILE = "config.json";
export const COVERAGE_NOTE_FILE = "coverage-note.md";

/** The language rule sets `tests.rules` may name. */
export const TEST_RULE_SETS = Object.freeze(["go", "vitest"]);

// --- globs -------------------------------------------------------------------
//
// ONE DIALECT, for every glob the harness matches a path against — the
// configured lists and the ones written in this package alike:
//
//   **    as a whole path segment: any number of directories, including none.
//         `**/x` matches `x` and `a/b/x`; `a/**` matches everything under `a/`;
//         alone, it matches every path.
//   *     inside a segment: any run of characters that is not a separator.
//   else  literal.
//
// Paths are repository-relative with `/` separators, and a glob is anchored at
// both ends. NOTHING ELSE IS SUPPORTED, and what is not supported is REFUSED
// rather than read literally: these lists are refusal surfaces and review
// scopes, so a pattern in another tool's syntax — `{a,b}`, `!negated`, `./x`,
// `dir/`, `a?b` — would compile, match no path, and quietly take that entry
// off the list. `globProblem` says why; `compileGlob` throws it.

export const GLOB_DIALECT = "`**` as a whole segment, `*` within a segment, everything else literal";

/** Why `glob` is not a glob in the harness dialect, or `null` when it is one. */
export function globProblem(glob) {
  if (typeof glob !== "string") return "is not a string";
  if (glob === "" || glob.trim() === "") return "is blank";
  if (glob !== glob.trim()) return "has leading or trailing whitespace";
  if (glob.startsWith("!")) return "is a negation; list only what should match";
  if (/[{}[\]?\\]/.test(glob)) return "uses `{}`, `[]`, `?` or `\\`, which this dialect does not have";
  if (glob.startsWith("/")) return "starts with `/`; paths are repository-relative, written without it";
  if (glob.endsWith("/")) return "ends with `/`; write `dir/**` for everything under a directory";
  const segments = glob.split("/");
  if (segments.includes("")) return "has an empty path segment (`//`)";
  if (segments.includes(".") || segments.includes("..")) return "has a `.` or `..` segment; write the path from the repository root";
  if (segments.some((s) => s.includes("**") && s !== "**")) return "uses `**` inside a segment; it must be a whole segment (`a/**/b`)";
  return null;
}

/** Compile one glob of the harness dialect. Throws on anything outside it. */
export function compileGlob(glob) {
  const problem = globProblem(glob);
  if (problem) throw new Error(`harness glob ${JSON.stringify(glob)} ${problem}`);
  const segments = glob.split("/");
  let re = "";
  segments.forEach((segment, i) => {
    const last = i === segments.length - 1;
    if (segment === "**") {
      // Zero or more directories; as the last segment, whatever is left.
      re += last ? ".*" : "(?:.*/)?";
      return;
    }
    re += segment
      .split("*")
      .map((lit) => lit.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
      .join("[^/]*");
    if (!last) re += "/";
  });
  return new RegExp(`^${re}$`);
}

// --- schema ------------------------------------------------------------------
//
// One entry per leaf: a predicate and the words for what it wants. A nested
// object is a nested schema. Every key listed is REQUIRED and every key not
// listed is REFUSED — a typo in a key name must not read as "that value is
// simply absent", which is the silent default this module exists to prevent.

const isStr = (v) => typeof v === "string";
// An entry is a non-blank string with no padding, and a list holds each entry
// once. A blank or padded entry matches nothing and a repeated one is a typo
// for something else, so neither is allowed to pass as a value.
const isEntry = (x) => isStr(x) && x !== "" && x === x.trim();
const isList = (v) => Array.isArray(v) && v.every(isEntry) && new Set(v).size === v.length;
const isGlobList = (v) => isList(v) && v.every((x) => globProblem(x) === null);
const ENTRIES = "strings that are not blank, not padded and not repeated";
const GLOBS = `globs in the harness dialect (${GLOB_DIALECT}), not blank, not padded and not repeated`;

const text = [(v) => isStr(v) && v.trim() !== "", "a non-empty string"];
/** A string that may be empty: the repository has nothing to say here, on purpose. */
const optionalText = [isStr, "a string (may be empty)"];
const list = [isList, `an array of ${ENTRIES} (may be empty)`];
const nonEmptyList = [(v) => isList(v) && v.length > 0, `a non-empty array of ${ENTRIES}`];
/** Names the first entry that is not a glob, and why. */
const globDetail = (v) => {
  const bad = Array.isArray(v) ? v.find((x) => globProblem(x) !== null) : undefined;
  return bad === undefined ? "" : ` — ${JSON.stringify(bad)} ${globProblem(bad)}`;
};
const globList = [isGlobList, `an array of ${GLOBS} (may be empty)`, globDetail];
const nonEmptyGlobList = [(v) => isGlobList(v) && v.length > 0, `a non-empty array of ${GLOBS}`, globDetail];
/** Lines of prose, in order: indentation is theirs to keep and a line may recur. */
const lines = [
  (v) => Array.isArray(v) && v.length > 0 && v.every((x) => isStr(x) && x.trim() !== ""),
  "a non-empty array of non-blank lines",
];
const flag = [(v) => typeof v === "boolean", "a boolean"];
const ruleSets = [
  (v) => isList(v) && v.length > 0 && v.every((x) => TEST_RULE_SETS.includes(x)),
  `a non-empty array drawn from ${JSON.stringify(TEST_RULE_SETS)}, without repeats`,
];
/** `linux`, `amd64`: what a Go file-name suffix can spell. */
const identifier = [(v) => isStr(v) && /^[a-z][a-z0-9]*$/.test(v), "a lower-case identifier such as \"linux\" or \"amd64\""];
/** Directory NAMES, matched as one path segment — not paths, not patterns. */
const dirNames = [
  (v) => isList(v) && v.every((x) => /^[A-Za-z0-9_][A-Za-z0-9._-]*$/.test(x)),
  "an array of plain directory names (one path segment each: no slash, no dot-prefix, no pattern)",
];

/** Marks a nested schema whose key may be absent as a whole. */
const OPTIONAL = Symbol("optional");
const optional = (schema) => ({ [OPTIONAL]: true, ...schema });

const SCHEMA = {
  repo: {
    // The repository's name as a reviewer is told it, and the prefix of the
    // capture schema id.
    name: text,
    // Bots whose latch, page and ledger comments are believed.
    botLogins: nonEmptyList,
    // The agent App's own identities, in both spellings the API reports.
    appLogins: nonEmptyList,
  },
  docs: {
    // How a decline message names the command-surface design document.
    commandVerbsRef: text,
  },
  ci: {
    // Paths that define what CI does, beyond the ones every repository shares.
    definingPaths: nonEmptyGlobList,
    // What the green-CI gate covers, in the ready-gate report.
    gateLabel: text,
    // The hand-off comment's CI bullet, one entry per line.
    handoffSummary: lines,
    // Does CI upload per-lane reports for the CI-fix arm to read?
    laneReports: flag,
  },
  review: {
    classRules: {
      "code-adjacent": globList,
      policy: globList,
    },
    // What counts as source for the path-scoped lenses.
    sourceGlobs: nonEmptyGlobList,
    // The design-fit rubric's sentence about where shared code lives.
    sharedPrimitivesNote: text,
    // Git pathspecs left out of the diff a reviewer reads. Git's dialect, not
    // the harness's: they are handed to `git diff`, never compiled here.
    diffExcludes: list,
  },
  fixer: {
    verifyCommand: text,
    verifyDescription: text,
    forbiddenCommands: text,
    targetedTestHint: text,
    generatedCodeNote: optionalText,
    keepReproducerAs: text,
    licenceScope: text,
    todoTrackingLine: optionalText,
  },
  tests: {
    rules: ruleSets,
    go: optional({
      sharedSuiteDirs: dirNames,
      ciPlatform: { goos: identifier, goarch: identifier },
    }),
  },
  fixGuard: {
    replicatedRuleGlobs: globList,
    replicatedRuleNotice: optionalText,
    counterpartRepo: optionalText,
  },
  redact: {
    extraPatterns: list,
  },
  ask: {
    permittedMcpTools: list,
  },
};

const isObject = (v) => v !== null && typeof v === "object" && !Array.isArray(v);

function validate(value, schema, where, problems) {
  if (!isObject(value)) {
    problems.push(`${where || "the document"} must be an object`);
    return;
  }
  for (const key of Object.keys(schema)) {
    const at = where ? `${where}.${key}` : key;
    const rule = schema[key];
    if (!Object.hasOwn(value, key)) {
      if (!Array.isArray(rule) && rule[OPTIONAL]) continue;
      problems.push(`${at} is missing`);
      continue;
    }
    if (Array.isArray(rule)) {
      const [ok, wants, detail] = rule;
      if (!ok(value[key])) problems.push(`${at} must be ${wants}${detail ? detail(value[key]) : ""}`);
    } else {
      validate(value[key], rule, at, problems);
    }
  }
  for (const key of Object.keys(value)) {
    if (!Object.hasOwn(schema, key)) problems.push(`${where ? `${where}.` : ""}${key} is not a known key`);
  }
}

/** Rules that span keys, checked only once each key has the right shape. */
function crossChecks(config, problems) {
  const wantsGo = config.tests.rules.includes("go");
  if (wantsGo && !config.tests.go) {
    problems.push('tests.go is missing, and tests.rules names "go"');
  }
  // The other direction is refused too: a block nothing reads looks like
  // configuration, and whoever wrote it believes the Go rules are on.
  if (!wantsGo && config.tests.go) {
    problems.push('tests.go is present, but tests.rules does not name "go" — nothing would read it');
  }
  const guard = config.fixGuard;
  if (guard.replicatedRuleGlobs.length > 0) {
    if (guard.replicatedRuleNotice.trim() === "") {
      problems.push("fixGuard.replicatedRuleNotice must be non-empty when fixGuard.replicatedRuleGlobs is");
    }
    if (guard.counterpartRepo.trim() === "") {
      problems.push("fixGuard.counterpartRepo must be non-empty when fixGuard.replicatedRuleGlobs is");
    }
  }
}

function deepFreeze(v) {
  if (v !== null && typeof v === "object" && !Object.isFrozen(v)) {
    Object.freeze(v);
    for (const child of Object.values(v)) deepFreeze(child);
  }
  return v;
}

// --- loading -----------------------------------------------------------------

/**
 * The directory the configuration is read from: the `AGENT_HARNESS_CONFIG_DIR`
 * override when it is set, otherwise `.github/agent-harness` under the root
 * this package sits in. Throws on an override that is empty or relative.
 */
export function configDir(env = process.env, here = HERE) {
  const override = env[CONFIG_DIR_ENV];
  if (override === undefined) {
    const inPlace = path.basename(here) === "agent" && path.basename(path.dirname(here)) === "scripts";
    if (!inPlace) {
      throw new Error(
        `harness config: this copy of the package is at ${here}, not at <root>/scripts/agent, so there is ` +
          `no repository beside it to read .github/agent-harness from. Set ${CONFIG_DIR_ENV} to the ` +
          "absolute path of a trusted copy of that directory.",
      );
    }
    return path.resolve(here, "..", "..", ".github", "agent-harness");
  }
  if (override.trim() === "") {
    throw new Error(
      `${CONFIG_DIR_ENV} is set but empty. Unset it to read the configuration beside this ` +
        "package, or set it to the absolute path of a trusted copy.",
    );
  }
  if (!path.isAbsolute(override)) {
    throw new Error(
      `${CONFIG_DIR_ENV} must be an absolute path, got ${JSON.stringify(override)}. A relative ` +
        "path would be resolved against the working directory, which may be a pull request's checkout.",
    );
  }
  return path.normalize(override);
}

function readText(dir, name) {
  const file = path.join(dir, name);
  try {
    return readFileSync(file, "utf8");
  } catch (err) {
    throw new Error(`harness config: cannot read ${file} (${err.code || err.message})`);
  }
}

const cache = new Map();

/**
 * Read, validate and freeze the configuration in `dir`. The result is the
 * parsed `config.json` with `review.mechanicalCoverageNote` added from
 * `coverage-note.md`. Cached per directory for the life of the process.
 *
 * Throws — with every problem found, not just the first — when the directory
 * or either file is missing, the JSON does not parse, a key is missing or
 * unknown, or a value has the wrong type.
 */
export function loadConfig(dir = configDir()) {
  const resolved = path.resolve(dir);
  const hit = cache.get(resolved);
  if (hit) return hit;

  let isDir = false;
  try {
    isDir = statSync(resolved).isDirectory();
  } catch {
    // reported below
  }
  if (!isDir) {
    throw new Error(
      `harness config: no directory at ${resolved}. Every checkout or copy of scripts/agent must ` +
        `bring .github/agent-harness from the same trusted ref, or name it with ${CONFIG_DIR_ENV}.`,
    );
  }

  const file = path.join(resolved, CONFIG_FILE);
  let parsed;
  try {
    parsed = JSON.parse(readText(resolved, CONFIG_FILE));
  } catch (err) {
    if (err.message.startsWith("harness config:")) throw err;
    throw new Error(`harness config: ${file} is not valid JSON (${err.message})`);
  }

  const problems = [];
  validate(parsed, SCHEMA, "", problems);
  if (problems.length === 0) crossChecks(parsed, problems);

  // Exactly one trailing newline is the file's, not the note's.
  const note = readText(resolved, COVERAGE_NOTE_FILE).replace(/\r?\n$/, "");
  if (note.trim() === "") problems.push(`${COVERAGE_NOTE_FILE} is empty`);

  if (problems.length > 0) {
    throw new Error(`harness config: ${file} is invalid:\n${problems.map((p) => `  - ${p}`).join("\n")}`);
  }

  const config = deepFreeze({ ...parsed, review: { ...parsed.review, mechanicalCoverageNote: note } });
  cache.set(resolved, config);
  return config;
}

/**
 * The value at a dotted key (`"ci.definingPaths"`). Throws on a key that does
 * not exist: an absent value is never `undefined` to a caller, because a caller
 * that spreads `undefined` into a list gets a shorter list and no error.
 */
export function get(key, config = loadConfig()) {
  if (typeof key !== "string" || key.trim() === "") throw new Error("harness config: get() needs a dotted key");
  let node = config;
  const walked = [];
  for (const part of key.split(".")) {
    walked.push(part);
    if (!isObject(node) || !Object.hasOwn(node, part)) {
      throw new Error(`harness config: no key ${JSON.stringify(walked.join("."))} (asked for ${JSON.stringify(key)})`);
    }
    node = node[part];
  }
  return node;
}

// --- CLI ---------------------------------------------------------------------

/**
 * Is this module the process's entry point? REALPATH BOTH SIDES: the loader
 * resolves `import.meta.url` through symlinks and `process.argv[1]` is whatever
 * the caller typed, so compared as plain strings a run through a symlinked
 * path looks like an import — and the CLI would print nothing and exit 0,
 * which a caller reads as an empty value. (The same predicate as the
 * repository-level `scripts/direct-run.mjs`, copied because this package is
 * checked out without its parent directory.)
 */
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
  const [verb, key, ...extra] = process.argv.slice(2);
  if (verb !== "get" || !key || extra.length > 0) {
    console.error("usage: node config.mjs get <dotted.key>");
    process.exit(1);
  }
  let value;
  try {
    value = get(key);
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
  process.stdout.write(`${typeof value === "string" ? value : JSON.stringify(value)}\n`);
}
