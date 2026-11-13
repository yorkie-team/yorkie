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

import { readFileSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));

export const CONFIG_DIR_ENV = "AGENT_HARNESS_CONFIG_DIR";
export const CONFIG_FILE = "config.json";
export const COVERAGE_NOTE_FILE = "coverage-note.md";

/** The language rule sets `tests.rules` may name. */
export const TEST_RULE_SETS = Object.freeze(["go", "vitest"]);

// --- schema ------------------------------------------------------------------
//
// One entry per leaf: a predicate and the words for what it wants. A nested
// object is a nested schema. Every key listed is REQUIRED and every key not
// listed is REFUSED — a typo in a key name must not read as "that value is
// simply absent", which is the silent default this module exists to prevent.

const isStr = (v) => typeof v === "string";
const isList = (v) => Array.isArray(v) && v.every((x) => isStr(x) && x !== "");

const text = [(v) => isStr(v) && v.trim() !== "", "a non-empty string"];
/** A string that may be empty: the repository has nothing to say here, on purpose. */
const optionalText = [isStr, "a string (may be empty)"];
const list = [isList, "an array of non-empty strings (may be empty)"];
const nonEmptyList = [(v) => isList(v) && v.length > 0, "a non-empty array of non-empty strings"];
const flag = [(v) => typeof v === "boolean", "a boolean"];
const ruleSets = [
  (v) => isList(v) && v.length > 0 && v.every((x) => TEST_RULE_SETS.includes(x)) && new Set(v).size === v.length,
  `a non-empty array drawn from ${JSON.stringify(TEST_RULE_SETS)}, without repeats`,
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
    definingPaths: nonEmptyList,
    // What the green-CI gate covers, in the ready-gate report.
    gateLabel: text,
    // The hand-off comment's CI bullet, one entry per line.
    handoffSummary: nonEmptyList,
    // Does CI upload per-lane reports for the CI-fix arm to read?
    laneReports: flag,
  },
  review: {
    classRules: {
      "code-adjacent": list,
      policy: list,
    },
    // What counts as source for the path-scoped lenses.
    sourceGlobs: nonEmptyList,
    // The design-fit rubric's sentence about where shared code lives.
    sharedPrimitivesNote: text,
    // Pathspecs left out of the diff a reviewer reads.
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
      sharedSuiteDirs: list,
      ciPlatform: { goos: text, goarch: text },
    }),
  },
  fixGuard: {
    replicatedRuleGlobs: list,
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
      const [ok, wants] = rule;
      if (!ok(value[key])) problems.push(`${at} must be ${wants}`);
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
  if (config.tests.rules.includes("go") && !config.tests.go) {
    problems.push('tests.go is missing, and tests.rules names "go"');
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
export function configDir(env = process.env) {
  const override = env[CONFIG_DIR_ENV];
  if (override === undefined) return path.resolve(HERE, "..", "..", ".github", "agent-harness");
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

// --- globs -------------------------------------------------------------------

/**
 * Compile one configured glob: `**` spans separators, `*` does not, everything
 * else is literal. Deliberately tiny and deliberately anchored at both ends:
 * the lists these come from are REFUSAL surfaces, so a pattern that
 * accidentally matched less is a fail-open.
 */
export function compileGlob(glob) {
  return new RegExp(
    `^${glob
      .split("**")
      .map((span) =>
        span
          .split("*")
          .map((lit) => lit.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
          .join("[^/]*"),
      )
      .join(".*")}$`,
  );
}

// --- CLI ---------------------------------------------------------------------

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
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
