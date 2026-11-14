// What the workflows say and run that differs between repositories, as step
// outputs.
//
// The `agent-*.yml` files are meant to be the same bytes in every repository
// that runs this harness. What a prompt tells a fixer to run, which paths a
// reviewer's diff leaves out, which bot a gate believes — those live in
// `.github/agent-harness/config.json`, and this is the one way a workflow
// reads them:
//
//   - name: Read the harness configuration
//     id: cfg
//     run: node <trusted>/scripts/agent/workflow-values.mjs verify_command … >> "$GITHUB_OUTPUT"
//
// after which a later step writes `${{ steps.cfg.outputs.verify_command }}`.
// A job with no checkout takes them from its `route` job's outputs instead
// (`${{ needs.route.outputs.app_slug }}`); the route job reads them this way.
//
// ONE VALUE, ONE NAME, ASKED FOR BY NAME. The step lists the values it wants,
// so the workflow says what it depends on, and a name this module does not
// know exits non-zero rather than writing nothing — an output that was never
// written reads as an empty string in an expression, and an empty string in a
// prompt is a sentence with a hole in it that nothing reports.
// `workflow-values.test.mjs` checks the other half statically: every
// `outputs.<name>` a workflow reads was asked for by the step it reads it from.
//
// EMPTY IS DECIDED HERE, NOT IN THE WORKFLOW. A required value that is blank
// throws. An optional one (`fixer.generatedCodeNote`, say) is rendered WITH the
// punctuation that joins it to its sentence — a leading space, a bullet, a
// trailing comma — so that when the repository has nothing to say the output
// is empty and the sentence around it still reads whole. `OPTIONAL` below is
// the list; the workflow writes those against the preceding word with no space
// of its own.
//
// Usage:
//   node workflow-values.mjs <name>...   GitHub's `name<<delimiter` output
//                                        format on stdout; exit 1 on any failure

import { randomUUID } from "node:crypto";
import { realpathSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { loadConfig } from "./config.mjs";

/** `prefix + text + suffix` when the repository said something, else nothing. */
const joined = (text, prefix, suffix = "") => (text.trim() === "" ? "" : `${prefix}${text.trim()}${suffix}`);

/** " <warning>, and <the push> re-runs <what> anyway." — or nothing. */
const integrationSentence = (c, thePush) =>
  c.fixer.integrationWarning === "" ? "" : ` ${c.fixer.integrationWarning}, and ${thePush} re-runs ${c.fixer.integrationRerunBy} anyway.`;

/**
 * Every value a workflow may ask for: where it comes from and how it is
 * rendered. Names are the output names, so they are lower_snake_case — a
 * dotted name cannot be read back with `steps.cfg.outputs.<name>`.
 */
export const VALUES = Object.freeze({
  // --- identity --------------------------------------------------------------
  repo_name: (c) => c.repo.name,
  /** What a message calls the App. */
  app_slug: (c) => c.repo.appSlug,
  /** The login the App's comments and pushes carry. */
  app_bot_login: (c) => `${c.repo.appSlug}[bot]`,
  /** `claude-code-action`'s `allowed_bots`: the App, in both spellings. */
  allowed_bots: (c) => `${c.repo.appSlug}[bot],${c.repo.appSlug}`,
  /** A JSON array, for `JSON.parse(process.env.…)` in a `github-script` step. */
  bot_logins: (c) => JSON.stringify(c.repo.botLogins),
  /**
   * The command-surface document, as Markdown for a comment: a URL bare, so it
   * stays a link, and a path in a code span. Either way the message writes it
   * as it is given, with nothing around it.
   */
  docs_ref: (c) => (/^https?:\/\/\S+$/.test(c.docs.commandVerbsRef) ? c.docs.commandVerbsRef : `\`${c.docs.commandVerbsRef}\``),

  // --- what a fixer is told ----------------------------------------------------
  verify_command: (c) => c.fixer.verifyCommand,
  verify_description: (c) => c.fixer.verifyDescription,
  verify_failure_hint: (c) => joined(c.fixer.verifyFailureHint, " "),
  /**
   * A sentence of its own, or nothing: which suites a fixer must not run, and
   * that the push runs them anyway. Two renderings, because the fix prompts
   * go on to describe the push ("the push below") and the CI-fix prompt has
   * already described it.
   */
  integration_sentence_before_push: (c) => integrationSentence(c, "the push below"),
  integration_sentence: (c) => integrationSentence(c, "the push"),
  ci_failure_note: (c) => joined(c.fixer.ciFailureNote, " "),
  /** A list item of its own, after the last one the workflow wrote. */
  generated_code_bullet: (c) => joined(c.fixer.generatedCodeNote, "\n- "),
  /**
   * The issue-to-PR prompt's last paragraph, with the blank line that
   * separates it from the one before: the generated-code rule, then what to
   * do about an issue that needs generated code changed.
   */
  generated_code_paragraph: (c) => joined([c.fixer.generatedCodeNote, c.fixer.apiChangeNote].filter((s) => s !== "").join(" "), "\n\n"),
  keep_reproducer_as: (c) => c.fixer.keepReproducerAs,
  disabled_test_forms: (c) => c.fixer.disabledTestForms,
  commit_note: (c) => joined(c.fixer.commitNote, " "),
  licence_scope: (c) => joined(c.fixer.licenceScope, " "),
  todo_tracking: (c) => joined(c.fixer.todoTrackingLine, " "),
  implement_verify_step: (c) => c.fixer.implementVerifyStep,
  /** A clause between two commas: "edit the branch,<this> commit …". */
  reply_verify_clause: (c) => joined(c.fixer.replyVerifyInstruction, " ", ","),
  toolchain_steps: (c) => c.fixer.toolchainSteps,
  last_resort_diagnosis: (c) => c.fixer.lastResortDiagnosis,

  // --- what the workflows do ---------------------------------------------------
  /** `true` or `false`: does CI upload per-lane reports for the CI-fix arm? */
  ci_lane_reports: (c) => String(c.ci.laneReports),
  /**
   * The pathspecs of the diff a reviewer reads, one per line: `.`, then an
   * `:(exclude)` for each configured path. Never empty, even for a repository
   * that excludes nothing — so a step that finds it empty knows the value did
   * not arrive, rather than diffing unfiltered and saying nothing.
   */
  diff_pathspecs: (c) => [".", ...c.review.diffExcludes.map((spec) => `:(exclude)${spec}`)].join("\n"),
});

/**
 * The values that may be empty. Each is rendered with its own joining
 * punctuation (see `joined`), so the workflow writes it against the word
 * before it and the sentence reads whole either way.
 */
export const OPTIONAL = Object.freeze(
  new Set([
    "verify_failure_hint",
    "integration_sentence_before_push",
    "integration_sentence",
    "ci_failure_note",
    "generated_code_bullet",
    "generated_code_paragraph",
    "commit_note",
    "licence_scope",
    "todo_tracking",
    "reply_verify_clause",
  ]),
);

/** The named values, in the order asked. Throws on anything it cannot answer. */
export function values(names, config = loadConfig()) {
  if (!Array.isArray(names) || names.length === 0) throw new Error("workflow-values: name at least one value");
  const seen = new Set();
  return names.map((name) => {
    if (!Object.hasOwn(VALUES, name)) {
      throw new Error(`workflow-values: no value named ${JSON.stringify(name)}. Known: ${Object.keys(VALUES).join(", ")}`);
    }
    if (seen.has(name)) throw new Error(`workflow-values: ${name} is asked for twice`);
    seen.add(name);
    const value = VALUES[name](config);
    if (typeof value !== "string") throw new Error(`workflow-values: ${name} did not render to a string`);
    if (value === "" && !OPTIONAL.has(name)) {
      throw new Error(`workflow-values: ${name} is empty, and it is not one of the values that may be`);
    }
    return [name, value];
  });
}

/**
 * `pairs` in the format `$GITHUB_OUTPUT` takes for a value that may span
 * lines: `name<<delimiter`, the value, the delimiter. The delimiter is random
 * and checked against every value, so no value can close its own block.
 */
export function githubOutput(pairs, delimiter = `harness_${randomUUID()}`) {
  let out = "";
  for (const [name, value] of pairs) {
    if (value.split("\n").includes(delimiter)) throw new Error(`workflow-values: ${name} contains the output delimiter`);
    out += `${name}<<${delimiter}\n${value}\n${delimiter}\n`;
  }
  return out;
}

// Realpath both sides, as `config.mjs` does and for the same reason: compared
// as typed, a run through a symlinked path looks like an import, prints
// nothing and exits 0 — which the workflow would read as every value empty.
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
  try {
    process.stdout.write(githubOutput(values(process.argv.slice(2))));
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
}
