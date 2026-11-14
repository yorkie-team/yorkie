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
/** The repository's own half of the harness, as a path from the repository's root. */
export const HARNESS_DIR = ".github/agent-harness";
export const CONFIG_FILE = "config.json";
export const COVERAGE_NOTE_FILE = "coverage-note.md";

/** The language rule sets `tests.rules` may name. */
export const TEST_RULE_SETS = Object.freeze(["go", "vitest", "cargo"]);

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
// both ends — so a bare `dir` matches only a path that is exactly `dir` (write
// `dir/**` for its contents), and `*.ext` matches only at the root (write
// `**/*.ext` for any depth). Those two are valid globs and cannot be refused
// here; a repository's `repo.test.mjs` catches them by requiring every
// configured entry to match a tracked file. NOTHING ELSE IS SUPPORTED, and what is not supported is REFUSED
// rather than read literally: these lists are refusal surfaces and review
// scopes, so a pattern in another tool's syntax — `{a,b}`, `!negated`, `./x`,
// `dir/`, `a?b` — would compile, match no path, and quietly take that entry
// off the list. `globProblem` says why; `compileGlob` throws it.

const MAX_STARS_PER_SEGMENT = 2;

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
  // Each `*` is a place the matcher may backtrack. Two in a segment covers
  // every real name (`*.pb.go`, `tsconfig*.json`, `*_test.*`); a mistyped run
  // of them compiles to a pattern that takes minutes on one long path.
  if (segments.some((s) => s.split("*").length - 1 > MAX_STARS_PER_SEGMENT)) {
    return `has more than ${MAX_STARS_PER_SEGMENT} \`*\` in one path segment`;
  }
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

// WHAT A WORKFLOW DROPS INTO A SENTENCE IT OWNS comes in two shapes, and a
// value of the wrong shape renders as a sentence that is visibly broken
// ("…line. , and a second line…") or quietly run on. Both are ONE LINE: the
// value is spliced into a wrapped paragraph, and a line break in it would
// start a list item or end a block the workflow did not mean to end.
//
//   fragment   part of a sentence the workflow finishes. Starts with a word,
//              not punctuation; ends without `.`, `,`, `;`, `:`, `!` or `?`,
//              because the workflow writes what follows.
//   sentences  one or more whole sentences. Starts with a capital, a digit, a
//              backtick or a quote; ends with `.`, `!` or `?` (a closing
//              bracket, quote or backtick may follow).
const oneLine = (v) => isStr(v) && v !== "" && v === v.trim() && !/[\r\n]/.test(v);
const isFragment = (v) => oneLine(v) && !/^[,.;:!?)\]]/.test(v) && !/[.,;:!?]$/.test(v);
const isSentences = (v) => oneLine(v) && /^[\p{Lu}\p{N}`"'(]/u.test(v) && /[.!?][)"'`\]]*$/.test(v);
const FRAGMENT = "one line that continues a sentence: no leading punctuation, no trailing `.`, `,`, `;`, `:`, `!` or `?`";
const SENTENCES = "one line of whole sentences: starts with a capital, a digit, a backtick or a quote, and ends with `.`, `!` or `?`";
/**
 * `owner/name`, as GitHub spells a repository, and nothing looser: the value is
 * put into URLs and paths. An owner is letters, digits and single inner
 * hyphens, at most 39 characters; a name is letters, digits, `.`, `_` and `-`,
 * at most 100, and is not `.` or `..`.
 */
export function isRepoSlug(value) {
  if (typeof value !== "string") return false;
  const parts = value.split("/");
  if (parts.length !== 2) return false;
  const [owner, name] = parts;
  return (
    /^[A-Za-z0-9](?:[A-Za-z0-9]|-(?=[A-Za-z0-9])){0,38}$/.test(owner) &&
    /^[A-Za-z0-9._-]{1,100}$/.test(name) &&
    name !== "." &&
    name !== ".."
  );
}
/**
 * An MCP tool name as the SDK spells one, `mcp__<server>__<tool>`, and only an
 * exact one: each part is letters and digits joined by single `-` or `_`, so a
 * part holds no `__` and the name splits one way. A wildcard, a bare server
 * (`mcp__server`, which grants all of its tools) and a permission rule
 * (`mcp__server__run(...)`) are refused: the allow-list in `ask.mjs` matches
 * names exactly, and none of those is a name.
 */
export function isMcpToolName(value) {
  const part = "[A-Za-z0-9]+(?:[-_][A-Za-z0-9]+)*";
  return typeof value === "string" && new RegExp(`^mcp__${part}__${part}$`).test(value);
}
/**
 * The structure of a `redact.extraPatterns` source that makes its matching
 * cost unbounded, or null. A pattern runs over every published text, and a
 * backtracking pattern can take minutes on one long run of characters
 * (`(a+)+$` on 100 KB). `redact.mjs` bounds the TEXT it runs on (tokens, in
 * windows); this bounds the PATTERN, so that each window costs at most a
 * square of its length:
 *
 *   - no repeating quantifier on a group that holds a quantifier or an
 *     alternation (`(a+)+`, `(a|ab)*`, `(a|b){8}`): the classic exponential
 *     shapes. `?` repeats nothing and may follow any group;
 *   - at most ONE quantifier whose count varies, other than `?` (`+`, `*`,
 *     `{n,}`, `{n,m}`; an exact `{n}` is free): two of them in a row
 *     (`\w*\w*!`) are already cubic;
 *   - at most four `?`;
 *   - no back-reference.
 *
 * A credential shape — a fixed prefix and one run of key characters, perhaps
 * an optional infix — fits inside all four.
 */
function redactPatternCost(source) {
  const groups = [{ quant: false, alt: false }];
  let varying = 0;
  let optional = 0;
  let lastAtom = null; // { group } for a just-closed group, {} for anything else
  for (let i = 0; i < source.length; i++) {
    const ch = source[i];
    let quant = null;
    let count = 0;
    if (ch === "\\") {
      const next = source[i + 1] ?? "";
      if (/[1-9]/.test(next) || next === "k") return "uses a back-reference";
      i++;
      lastAtom = {};
      continue;
    }
    if (ch === "[") {
      i++;
      if (source[i] === "^") i++;
      if (source[i] === "]") i++;
      while (i < source.length && source[i] !== "]") {
        if (source[i] === "\\") i++;
        i++;
      }
      lastAtom = {};
      continue;
    }
    if (ch === "(") {
      groups.push({ quant: false, alt: false });
      // Skip the group's kind: `?:`, `?=`, `?!`, `?<=`, `?<!`, `?<name>`.
      if (source[i + 1] === "?") {
        i += 2;
        if (source[i] === "<" && source[i + 1] !== "=" && source[i + 1] !== "!") {
          while (i < source.length && source[i] !== ">") i++;
        } else if (source[i] === "<") {
          i++;
        }
      }
      lastAtom = null;
      continue;
    }
    if (ch === ")") {
      const g = groups.length > 1 ? groups.pop() : { quant: false, alt: false };
      // What the group held, its parent holds too.
      const parent = groups[groups.length - 1];
      parent.quant ||= g.quant;
      parent.alt ||= g.alt;
      lastAtom = { group: g };
      continue;
    }
    if (ch === "|") {
      groups[groups.length - 1].alt = true;
      lastAtom = null;
      continue;
    }
    if (ch === "*" || ch === "+") quant = "varying";
    else if (ch === "?") quant = lastAtom ? "optional" : null;
    else if (ch === "{") {
      const m = /^\{(\d+)(,(\d*))?\}/.exec(source.slice(i));
      if (m) {
        quant = m[2] === undefined || m[3] === m[1] ? "exact" : "varying";
        count = Number(m[1]);
        i += m[0].length - 1;
      }
    }
    if (quant && lastAtom) {
      // `?` repeats nothing, so it may follow any group: `(?:live_|test_)?`.
      const repeats = quant === "varying" || (quant === "exact" && count > 1);
      if (repeats && lastAtom.group && (lastAtom.group.quant || lastAtom.group.alt)) {
        return lastAtom.group.alt
          ? "puts a quantifier on an alternation, which can take exponential time"
          : "nests one quantifier inside another, which can take exponential time";
      }
      if (quant === "varying") varying++;
      if (quant === "optional") optional++;
      if (quant !== "exact") groups[groups.length - 1].quant = true;
      if (source[i + 1] === "?") i++; // lazy
      lastAtom = null;
      continue;
    }
    lastAtom = {};
  }
  if (varying > 1) return `has ${varying} quantifiers whose count varies; at most one (\`+\`, \`*\`, \`{n,}\`, \`{n,m}\`) is allowed`;
  if (optional > 4) return `has ${optional} \`?\`; at most four are allowed`;
  return null;
}

/** Texts a source must not match at zero width anywhere in. */
const ZERO_WIDTH_PROBES = Object.freeze([
  "",
  "a",
  "the quick brown fox",
  "key: wfb_AbC123-x_y.z, \"token\"='v'; 0 9 Z",
  "<REDACTED_API_KEY> sk-ant-x ghp_x eyJx.y.z https://example.com/a?b=c#d",
  "\t\n\r",
]);

/**
 * Why `source` cannot be a `redact.extraPatterns` entry, or null. It is the
 * source of a JavaScript regular expression, without slashes or flags:
 * `redact.mjs` compiles it with `g` and nothing else, and runs it over one
 * whitespace-separated token of a text at a time. So it:
 *
 *   - must compile;
 *   - must contain no whitespace and no `\s`: it is never shown any;
 *   - must not match at zero width ANYWHERE — `\b`, a lone lookaround, `x*` —
 *     because what a match covers is masked, and a match of nothing at a word
 *     boundary is a mask on the word beside it. Checked against probe texts,
 *     which catches every assertion-only pattern a person would write;
 *   - must have a bounded matching cost (`redactPatternCost`).
 */
export function redactPatternProblem(source) {
  if (typeof source !== "string" || source === "") return "is not a pattern";
  if (/^\/.*\/[a-z]*$/.test(source)) return "is written as a literal: give the source alone, without the slashes or flags";
  let re;
  try {
    re = new RegExp(source, "g");
  } catch (err) {
    return `does not compile (${err.message})`;
  }
  if (/\s/.test(source) || /\\[snrtvf]/.test(source.replace(/\\\\/g, ""))) {
    return "matches whitespace, but it is run one whitespace-separated token at a time";
  }
  const cost = redactPatternCost(source);
  if (cost) return cost;
  for (const probe of ZERO_WIDTH_PROBES) {
    re.lastIndex = 0;
    let m;
    while ((m = re.exec(probe)) !== null) {
      if (m[0] === "") return "can match at zero width, which would mask the text beside the match";
    }
  }
  return null;
}
const fragment = [isFragment, FRAGMENT];
const optionalFragment = [(v) => v === "" || isFragment(v), `empty, or ${FRAGMENT}`];
const sentences = [isSentences, SENTENCES];
const optionalSentences = [(v) => v === "" || isSentences(v), `empty, or ${SENTENCES}`];
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

// Directories, as globs: `crates/*/tests`. A last segment of `**` would name
// a directory's contents at every depth, and the rule these feed is about
// what sits DIRECTLY in the directory.
const isDirGlobList = (v) => isGlobList(v) && v.every((x) => x.split("/").at(-1) !== "**");
const dirGlobList = [
  isDirGlobList,
  `an array of directories as ${GLOBS} (may be empty); name the directory itself, not \`dir/**\``,
  globDetail,
];

/** Marks a nested schema whose key may be absent as a whole. */
const OPTIONAL = Symbol("optional");
const optional = (schema) => ({ [OPTIONAL]: true, ...schema });
/**
 * Marks a LIST that may be absent, for a key that only ever ADDS to a list
 * this package already has. Absent and `[]` then mean the same thing — "the
 * built-in entries and no more" — so absence cannot make any list shorter,
 * which is the failure "no defaults" exists to prevent. Read with
 * `getOptionalList`, never with `get`.
 */
const optionalAddition = ([ok, wants, detail]) => Object.assign([ok, wants, detail], { [OPTIONAL]: true });

const SCHEMA = {
  repo: {
    // The repository's name as a reviewer is told it, and the prefix of the
    // capture schema id.
    name: text,
    // The agent GitHub App's slug: what a message calls the App, and the stem
    // of its two logins.
    appSlug: [(v) => isStr(v) && /^[a-z0-9][a-z0-9-]*$/.test(v), "a GitHub App slug (lower-case letters, digits and hyphens)"],
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
      // Additions to the two classes whose paths are otherwise this
      // package's own: where the design contract lives beyond
      // `docs/design/**`, and what else is narration. Both may be absent.
      "design-spec": optionalAddition(globList),
      prose: optionalAddition(globList),
    },
    // What counts as source for the path-scoped lenses.
    sourceGlobs: nonEmptyGlobList,
    // The design-fit rubric's sentence about where shared code lives.
    sharedPrimitivesNote: text,
    // Git pathspecs left out of the diff a reviewer reads. Git's dialect, not
    // the harness's: they are handed to `git diff`, never compiled here.
    diffExcludes: list,
  },
  // What the workflows tell a fixer, in this repository's words. Each value is
  // dropped into a sentence the workflow owns; `workflow-values.mjs` renders
  // them and the README shows every frame with its surrounding words. A
  // FRAGMENT continues the workflow's sentence, SENTENCES stand on their own,
  // and an optional one may be empty: its sentence is then left out whole.
  fixer: {
    // "Run `<verifyCommand>` ONCE at the end … It is <verifyDescription>,
    // which is the whole of what this job can verify."
    verifyCommand: fragment,
    verifyDescription: fragment,
    // Said after that: what to do when a particular part of it fails.
    verifyFailureHint: optionalSentences,
    // "<integrationWarning>, and the push re-runs <integrationRerunBy>
    // anyway." Both or neither: a repository with no suite a fixer must keep
    // away from leaves both empty and the sentence is not said.
    integrationWarning: optionalFragment,
    integrationRerunBy: optionalFragment,
    // Said after that in the CI-fix prompt only: what to do when the failing
    // lane is one the fixer may not run.
    ciFailureNote: optionalSentences,
    // Which generated code may not be edited by hand: a list item in the three
    // fixer prompts, and the opening of the issue-to-PR prompt's last
    // paragraph, where <apiChangeNote> follows it.
    generatedCodeNote: optionalSentences,
    apiChangeNote: optionalSentences,
    // "If a test … still fails, the finding is not fixed. <keepReproducerAs>,
    // and report that item `--skipped` with the reason."
    keepReproducerAs: fragment,
    // "…every test a fix round removes or <disabledTestForms>, and shows it…"
    disabledTestForms: fragment,
    // Said after the push rules in the three fixer prompts: what this
    // repository asks of a commit message, or of anything else its own hooks
    // would check. The harness installs one hook, the scope check, and says so
    // itself; whether the repository's hooks are active in a fresh checkout is
    // the repository's to know, so this sentence is the repository's to write.
    commitNote: optionalSentences,
    // In the issue-to-PR prompt: "3. IMPLEMENT: make the change.
    // <licenceScope> Commit subject …"; "The todo starts with a `**Created**`
    // line. <todoTrackingLine>"; and "5. <implementVerifyStep>". These three
    // may use the prompt's own placeholders: `__ISSUE_NUMBER__`, `__TODAY__`,
    // `__ISSUE_FILE__`, `__PR_FILE__`.
    licenceScope: optionalSentences,
    todoTrackingLine: optionalSentences,
    implementVerifyStep: sentences,
    // "…edit the branch, <replyVerifyInstruction>, commit with the trailer…"
    replyVerifyInstruction: optionalFragment,
    // "…stopped after setup and before the agent started — <toolchainSteps>,
    // or fetching the issue."
    toolchainSteps: fragment,
    // "No failed-step log could be read for run N. <lastResortDiagnosis>"
    lastResortDiagnosis: sentences,
  },
  tests: {
    rules: ruleSets,
    go: optional({
      sharedSuiteDirs: dirNames,
      ciPlatform: { goos: identifier, goarch: identifier },
    }),
    cargo: optional({
      // Where cargo finds integration-test TARGETS: a `.rs` file directly in
      // one of these, or `<dir>/<name>/main.rs`, is built and run by name.
      // Usually `tests` beside each `Cargo.toml` (`crates/*/tests`).
      integrationTestDirs: dirGlobList,
      // Source files that are test code as a whole although neither their
      // directory nor their name says so: a module declared
      // `#[cfg(test)] mod sync_scenarios;` in its parent.
      testOnlyFileGlobs: globList,
    }),
  },
  fixGuard: {
    replicatedRuleGlobs: globList,
    replicatedRuleNotice: optionalText,
    // `owner/name`, each: the repositories that must apply the same rule. A
    // pull request body that refers to ANY of them — `owner/name`,
    // `owner/name#12`, `name#12`, or a github.com link to it — has made the
    // question visible. The full name, because a bare one is a substring of
    // its neighbours (`yorkie` of `yorkie-js-sdk`) and would match nearly
    // every body. A list, because a rule can have more than one other home: a
    // client SDK ports it from another SDK and must converge with a server.
    counterpartRepos: [(v) => isList(v) && v.every(isRepoSlug), "an array of repositories as `owner/name`, not repeated (may be empty)"],
  },
  // Credential shapes of this repository's own product, masked by
  // `redact.mjs` beside the shapes it knows itself. Empty for a product whose
  // keys have no shape a pattern could tell apart from an identifier.
  redact: {
    extraPatterns: [
      (v) => isList(v) && v.every((x) => redactPatternProblem(x) === null),
      `an array of JavaScript regular-expression sources, ${ENTRIES}, each compiling, matching neither whitespace nor zero width, and of bounded cost (may be empty)`,
      (v) => {
        const bad = Array.isArray(v) ? v.find((x) => isEntry(x) && redactPatternProblem(x) !== null) : undefined;
        return bad === undefined ? "" : ` — ${JSON.stringify(bad)} ${redactPatternProblem(bad)}`;
      },
    ],
  },
  // In-process MCP tools this repository implements itself, which `ask.mjs`
  // may grant a session by exact name. Empty refuses every MCP name.
  ask: {
    permittedMcpTools: [
      (v) => isList(v) && v.every(isMcpToolName),
      `an array of exact MCP tool names, \`mcp__<server>__<tool>\`, not repeated (may be empty)`,
      (v) => {
        const bad = Array.isArray(v) ? v.find((x) => isEntry(x) && !isMcpToolName(x)) : undefined;
        return bad === undefined ? "" : ` — ${JSON.stringify(bad)} is not one`;
      },
    ],
  },
};

const isObject = (v) => v !== null && typeof v === "object" && !Array.isArray(v);

// A key this schema used to have under another name, and what to write now.
// `sync.mjs` prints the loader's problems as the list of edits a newer core
// leaves for a person; "is not a known key" alone would leave them to find
// the new spelling by reading a diff.
const RENAMED_KEYS = Object.freeze({
  "fixGuard.counterpartRepo":
    'it was renamed to fixGuard.counterpartRepos and is now a list: write ["owner/name"], or [] where it was ""',
});

function validate(value, schema, where, problems) {
  if (!isObject(value)) {
    problems.push(`${where || "the document"} must be an object`);
    return;
  }
  for (const key of Object.keys(schema)) {
    const at = where ? `${where}.${key}` : key;
    const rule = schema[key];
    if (!Object.hasOwn(value, key)) {
      if (rule[OPTIONAL]) continue;
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
    if (Object.hasOwn(schema, key)) continue;
    const at = `${where ? `${where}.` : ""}${key}`;
    problems.push(`${at} is not a known key${Object.hasOwn(RENAMED_KEYS, at) ? ` — ${RENAMED_KEYS[at]}` : ""}`);
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
  const wantsCargo = config.tests.rules.includes("cargo");
  if (wantsCargo && !config.tests.cargo) {
    problems.push('tests.cargo is missing, and tests.rules names "cargo"');
  }
  if (!wantsCargo && config.tests.cargo) {
    problems.push('tests.cargo is present, but tests.rules does not name "cargo" — nothing would read it');
  }
  // One App, named three ways. A slug that disagreed with the logins would
  // have the workflows allow-list one bot while the scripts believe another.
  const appBot = `${config.repo.appSlug}[bot]`;
  if (!config.repo.appLogins.includes(appBot)) problems.push(`repo.appLogins must include ${JSON.stringify(appBot)}, the login of repo.appSlug`);
  if (!config.repo.botLogins.includes(appBot)) problems.push(`repo.botLogins must include ${JSON.stringify(appBot)}, the login of repo.appSlug`);
  // Two halves of one sentence: either without the other renders as
  // "…, and the push re-runs  anyway."
  if ((config.fixer.integrationWarning === "") !== (config.fixer.integrationRerunBy === "")) {
    problems.push("fixer.integrationWarning and fixer.integrationRerunBy must both be set or both be empty");
  }
  // `prose` is the one class routed AWAY from the code lenses, and it is
  // matched last. An addition that matches every path would turn every file
  // no other class claims — all the code — into prose.
  if ((config.review.classRules.prose ?? []).includes("**")) {
    problems.push('review.classRules.prose must not contain "**": every file no other class claims would be read as prose, by no code lens');
  }
  const guard = config.fixGuard;
  if (guard.replicatedRuleGlobs.length > 0) {
    if (guard.replicatedRuleNotice.trim() === "") {
      problems.push("fixGuard.replicatedRuleNotice must be non-empty when fixGuard.replicatedRuleGlobs is");
    }
    if (guard.counterpartRepos.length === 0) {
      problems.push("fixGuard.counterpartRepos must be non-empty when fixGuard.replicatedRuleGlobs is");
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
    return path.resolve(here, "..", "..", ...HARNESS_DIR.split("/"));
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

/**
 * The list at a key the schema marks as an optional addition: its entries, or
 * `[]` when the repository did not write the key. Throws on any other key, so
 * this cannot become the general "absent is fine" read that `get` refuses.
 */
export function getOptionalList(key, config = loadConfig()) {
  let rule = SCHEMA;
  for (const part of String(key).split(".")) rule = isObject(rule) && Object.hasOwn(rule, part) ? rule[part] : undefined;
  if (!Array.isArray(rule) || !rule[OPTIONAL]) {
    throw new Error(`harness config: ${JSON.stringify(key)} is not an optional list; read it with get()`);
  }
  let node = config;
  for (const part of key.split(".")) {
    if (!isObject(node) || !Object.hasOwn(node, part)) return [];
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
