// Where the workflows run the harness scripts from, and so where each script
// reads the harness configuration from.
//
// `config.mjs` resolves `.github/agent-harness` beside the checkout the running
// script came from, or from `AGENT_HARNESS_CONFIG_DIR` for a staged copy. The
// configuration therefore has exactly the provenance of the script that reads
// it — and the property that matters is one about EXECUTION: every script a
// workflow runs must come from a tree that is the default branch's AT THE STEP
// THAT RUNS IT. Read from a pull request's tree, the script and its
// configuration are the author's, and the author is choosing the gates the
// pull request is judged by.
//
// An earlier version of this guard checked the checkout and copy STEPS: that
// each one fetched the configuration along with the scripts. That is necessary
// and nowhere near sufficient, because it never asked what was run, or from
// where, or what had happened to the tree in between. Six changes that each
// hand a pull request its own gates passed it; they are the first six mutation
// cases at the bottom of this file.
//
// So this guard is invocation-first. It walks every job of every
// `agent-*.yml` step by step, tracking which trees exist and whether each is
// still trusted, and at every place a harness script is run it requires:
//
//   - the tree it runs from is trusted at that step — a side-directory checkout
//     of the default branch, the staged copy made from one, or a workspace that
//     holds the default branch and has had nothing else checked out, fetched
//     or switched into it;
//   - if the script reaches `config.mjs`, that tree carries the configuration:
//     a checkout that includes `.github/agent-harness`, or the staged copy with
//     the variable naming the staged configuration.
//
// The workflows are read as text, like the other wiring guards in this
// package, and the checks run on text handed in — which is what lets the
// mutation cases run against altered workflows without touching a file.
//
// WHAT THIS GUARD DOES NOT SEE. It reads workflow text for the shapes the
// workflows use today, and a text guard can always be written around. It
// exists to catch a well-meaning edit that quietly moves a script or its
// configuration onto an untrusted tree — not to stop someone who is trying.
// Workflow files are CI-defining and CODEOWNER-ed, so every edit to one still
// needs a human reviewer, and these are the things that reviewer must look for
// because nothing here will:
//
//   - writes into a trusted tree or the staged copy by any means other than
//     the recognised `cp -R` staging lines: `cp -a`, `rsync`, `tar -x`,
//     `actions/download-artifact`, a redirect into a script's path;
//   - the dependency tarball a `deps` job builds and a later job unpacks
//     beside the trusted scripts (the cross-job hand-off is not followed);
//   - a script run through a shell variable, `xargs`, `make`, or any
//     interpreter other than a literal `node <path>`;
//   - YAML written in a form the line reader does not expect: quoted keys
//     (`"ref":`, `"uses":`), flow-style mappings, anchors;
//   - what the toolchain action does once it is running: the actions it
//     nests and the commands it runs are that file's, and only WHERE the file
//     came from is checked here.
//
// LOCAL ACTIONS ARE SEEN. `uses: ./…` loads an action from the workspace at
// the step that runs it, with the job's tokens — the same exposure as a script.
// Exactly one is allowed, the repository's `agent-toolchain`, and only from a
// tree that is trusted at that step: a side-directory checkout of the default
// branch, or a workspace that still is one.
//
// Directory changes ARE seen, coarsely: a script run by relative path in a
// step with `working-directory:`, a `cd`, or through `npm run` is refused
// outright rather than resolved, apart from the one job listed below.
//
// A JOB THAT WRITES ISSUES ON A CLOCK IS HELD TO MORE. `upstream-notify.mjs`
// runs with `issues: write`. Being run from the default branch's tree is
// necessary and not enough: the job that runs it must be one no pull request
// or push can start, must hold `contents: read` and `issues: write` and
// nothing else, and must name no secret — and, because a step added beside
// that token is as good as a new script, the workflow is held to its whole
// shape: one job, three steps, each line of each on a list. See
// `notifierProblems`.
//
// IT ASSERTS HOW MANY SITES IT INSPECTED. A guard that depends on where things
// are — a step shape, an indentation, a path spelling — passes vacuously the
// day the shape changes and it finds nothing. Adding or removing a site changes
// a count below; that is the prompt to look at the new site, not a nuisance.

import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(HERE, "..", "..");
const WF_DIR = path.join(ROOT, ".github", "workflows");

const SCRIPTS = "scripts/agent";
const ROUTER = "scripts/agent/command.mjs";
const CONFIG = ".github/agent-harness";
const ENV = "AGENT_HARNESS_CONFIG_DIR";
const TEMP = "$RUNNER_TEMP";
const STAGED_TOOLS = `${TEMP}/agent-tools`;
const STAGED_CONFIG = `${TEMP}/agent-harness`;
/** The step that refuses a checkout whose head is not the commit that was reviewed. */
const REVERIFY = /^\s*-? ?name: Re-verify the checked-out commit\b/;
/** Where the fix-scope hook is installed: outside the workspace, named by `core.hooksPath`. */
const HOOK_DIR = `${TEMP}/agent-hooks`;
/** The one local action a workflow may use, as a path from a tree's root. */
const TOOLCHAIN = ".github/actions/agent-toolchain";

// The counts. Each is the number of sites of one kind in the workflows today.
const EXPECTED = {
  // Sparse checkouts of the scripts directory from the default branch.
  trustedSparseCheckouts: 19,
  // Route jobs that check out `command.mjs` alone. They read no configuration;
  // the others bring the whole package and hand configured values on.
  routerCheckouts: 2,
  // `uses: ./…/agent-toolchain`, and the side-directory checkouts made for it
  // (the fifth use is from a workspace that is still the default branch).
  toolchainUses: 5,
  toolchainCheckouts: 4,
  // Side-directory checkouts made inside a workspace that holds a pull
  // request's tree, each of which must clear its path first.
  sideCheckoutsOverUntrustedWorkspace: 8,
  // Steps that install the fix-scope pre-push hook.
  scopeHooks: 3,
  // Jobs that re-verify the commit they checked out, before which no
  // toolchain may be set up.
  reverifiedCheckouts: 1,
  // `cp -R … scripts/agent …/agent-tools`, each with its configuration.
  stagingCopies: 4,
  // Places a harness script is run, and how many of them read the configuration.
  invocations: 114,
  configReadingInvocations: 90,
  // …by the kind of tree each runs from.
  fromWorkspace: 66,
  fromSideDirectory: 27,
  fromStagedCopy: 21,
  // Jobs that run the upstream notifier, each held to `notifierProblems`.
  notifierJobs: 1,
};

/**
 * Jobs allowed to run the package from a changed directory. `agent-scripts.yml`
 * is this package's own test lane: it runs the PULL REQUEST's copy on purpose,
 * with `contents: read` and no secrets, so there is no gate for it to subvert.
 */
const DIRECTORY_CHANGE_ALLOWED = new Set(["agent-scripts.yml › test"]);

/**
 * Events whose DEFAULT checkout ref is trusted, so a checkout with no `ref:`
 * may be believed. An allow-list, because the default ref is whatever the event
 * says it is and new events appear: anything not named here is not trusted.
 *
 *   issue_comment, workflow_run, schedule — GitHub runs these from the default
 *     branch, whatever pull request the event is about.
 *
 * Deliberately absent: every `pull_request*` event and `push`, where the
 * default ref is the author's — and `workflow_dispatch`, where it is whichever
 * ref the dispatcher picked. That ref is a maintainer's choice, but "the
 * branch I am testing" is exactly a tree that is not the default branch's, so
 * a workflow that can be dispatched names `main` on every checkout it trusts.
 */
const TRUSTED_DEFAULT_REF_EVENTS = new Set(["issue_comment", "workflow_run", "schedule"]);

/** The script that opens, rewrites and closes the sync issue. */
const NOTIFIER = "upstream-notify.mjs";
/** The only events that may start a job that runs it: a clock, and a maintainer. */
const NOTIFIER_EVENTS = new Set(["schedule", "workflow_dispatch"]);
/** Everything that job may hold. */
const NOTIFIER_PERMISSIONS = { contents: "read", issues: "write" };

/** `ref:` values that name the default branch outright. */
const TRUSTED_REFS = new Set(["main", "refs/heads/main", "${{ github.event.repository.default_branch }}"]);

// --- which scripts read the configuration ---------------------------------------

const MODULES = readdirSync(HERE).filter((f) => f.endsWith(".mjs") && !f.endsWith(".test.mjs"));
const localImports = (file) => {
  const src = readFileSync(path.join(HERE, file), "utf8");
  return [
    ...src.matchAll(/^\s*(?:import|export)\b[^;]*?["'](\.\/[^"']+)["']/gm),
    ...src.matchAll(/\bimport\s*\(\s*["'](\.\/[^"']+)["']/g),
  ]
    .map((m) => path.normalize(m[1]))
    .filter((f) => MODULES.includes(f));
};
function reachesConfig(file, seen = new Set()) {
  if (file === "config.mjs") return true;
  if (seen.has(file) || !MODULES.includes(file)) return false;
  seen.add(file);
  return localImports(file).some((f) => reachesConfig(f, seen));
}
const READS_CONFIG = new Set(MODULES.filter((f) => reachesConfig(f)));

// --- reading a workflow ------------------------------------------------------------

const isComment = (line) => /^\s*#/.test(line);
/** `${{ runner.temp }}` and `$RUNNER_TEMP` are one place. */
const normalise = (line) =>
  line
    .replaceAll("${{ runner.temp }}", TEMP)
    .replaceAll("${RUNNER_TEMP}", TEMP)
    .replaceAll("${GITHUB_WORKSPACE}", "$GITHUB_WORKSPACE")
    // A trailing comment says nothing about what runs. (`#` after whitespace
    // and before a space: `'#!/bin/sh'` and `#123` are not comments.)
    .replace(/\s+#\s.*$/, "");
const indentOf = (line) => /^(\s*)/.exec(line)[1].length;
const isBlank = (line) => line.trim() === "" || isComment(line);
/** A step's `uses:` or `run:` key — what makes a step a step. */
const isStepKey = (line) => !isComment(line) && /^\s*(?:- )?(?:uses|run):(?:\s|$)/.test(line);

function parseWorkflow(file, text) {
  const lines = text.split("\n");
  const jobsAt = lines.findIndex((l) => /^jobs:\s*$/.test(l));
  if (jobsAt < 0) return { file, events: [], jobs: [], unreadable: "no top-level `jobs:`" };
  const onAt = lines.findIndex((l) => /^on:\s*$/.test(l));
  const events = [];
  if (onAt >= 0) {
    for (const line of lines.slice(onAt + 1, jobsAt)) {
      if (/^\S/.test(line)) break;
      const m = /^ {2}([a-z_]+):/.exec(line);
      if (m) events.push(m[1]);
    }
  }
  // Indentation is read off the file, not assumed: a job or a step list
  // indented differently from its neighbours is still a job and still steps.
  const body = lines.slice(jobsAt + 1);
  const firstKey = body.find((l) => !isBlank(l));
  const jobIndent = firstKey ? indentOf(firstKey) : 2;
  const jobs = [];
  let job = null;
  let step = null;
  let stepIndent = -1;
  let inSteps = false;
  for (const line of body) {
    const header = !isBlank(line) && indentOf(line) === jobIndent && /^\s*([A-Za-z0-9_-]+):\s*(?:#.*)?$/.exec(line);
    if (header) {
      job = { file, name: header[1], events, head: [], steps: [] };
      jobs.push(job);
      step = null;
      stepIndent = -1;
      inSteps = false;
      continue;
    }
    if (!job) continue;
    if (!inSteps) {
      if (!isBlank(line) && /^\s*steps:\s*(?:#.*)?$/.test(line)) inSteps = true;
      else job.head.push(line);
      continue;
    }
    if (stepIndent < 0 && !isBlank(line) && /^\s*- /.test(line)) stepIndent = indentOf(line);
    if (stepIndent >= 0 && indentOf(line) === stepIndent && /^\s*- /.test(line)) {
      step = { lines: [] };
      job.steps.push(step);
    }
    if (step) step.lines.push(line);
  }
  // THE INDEPENDENT COUNT. Every `uses:`/`run:` key in the file must sit inside
  // a step the walk above produced. One that does not belongs to a job this
  // reader could not parse — which would otherwise be a job nothing inspects.
  const stepKeys = body.filter(isStepKey).length;
  const seen = jobs.reduce((n, j) => n + j.steps.reduce((m, st) => m + st.lines.filter(isStepKey).length, 0), 0);
  const unparsed = stepKeys === seen ? null : `${stepKeys - seen} of ${stepKeys} \`uses:\`/\`run:\` keys are outside every step this guard could parse`;
  return { file, events, jobs, preamble: lines.slice(0, jobsAt), unparsed };
}

/** A step's own scalar key, or undefined. Comment lines are not values. */
function scalar(step, key) {
  for (const line of step.lines) {
    if (isComment(line)) continue;
    const m = new RegExp(`^\\s+(?:- )?${key}:\\s*(.*?)\\s*(?:\\s#.*)?$`).exec(line);
    if (m) return m[1].replace(/^(["'])(.*)\1$/, "$2");
  }
  return undefined;
}

/**
 * A checkout step's sparse patterns, inline or block form, with a leading or
 * trailing `/` dropped: in non-cone mode `/scripts/agent`, `scripts/agent/`
 * and `scripts/agent` all select the same directory. `null` when the step
 * checks out everything.
 */
function sparsePatterns(step) {
  const at = step.lines.findIndex((l) => !isComment(l) && /^\s+sparse-checkout:/.test(l));
  if (at < 0) return null;
  const head = /^(\s+)sparse-checkout:\s*(.*?)\s*$/.exec(step.lines[at]);
  const raw = [];
  if (!/^[|>][+-]?$/.test(head[2])) raw.push(head[2]);
  else {
    for (const line of step.lines.slice(at + 1)) {
      if (isComment(line) || line.trim() === "") continue;
      if (/^(\s*)/.exec(line)[1].length <= head[1].length) break;
      raw.push(line.trim());
    }
  }
  return raw.map((p) => p.replace(/^(["'])(.*)\1$/, "$2").replace(/^\/+/, "").replace(/\/+$/, ""));
}

const uses = (step, action) => step.lines.some((l) => !isComment(l) && new RegExp(`\\buses:\\s*${action}@`).test(l));

// --- the walk ----------------------------------------------------------------------

/**
 * Variables no workflow, job or step `env:` may set: the configuration's
 * location (one writer, the staging step) and `NODE_OPTIONS`, which can
 * preload a module into every script.
 */
const FORBIDDEN_ENV = /\b(?:AGENT_HARNESS_CONFIG_DIR|NODE_OPTIONS)\b/;

const ACCEPTED_FORMS =
  'the staging copy `cp -R <trusted tree>/.github/agent-harness "$RUNNER_TEMP/agent-harness"`, ' +
  'the export `echo "AGENT_HARNESS_CONFIG_DIR=$RUNNER_TEMP/agent-harness" >> "$GITHUB_ENV"`, ' +
  "the pre-push hook's own export line and its argument, or the prefix " +
  "`AGENT_HARNESS_CONFIG_DIR=$RUNNER_TEMP/agent-harness node $RUNNER_TEMP/agent-tools/<script>`";

/**
 * A job's own `permissions:` block as `{ scope: level }`, or a string saying
 * why it could not be read as one (absent, or a scalar such as `write-all`).
 */
function jobPermissions(job) {
  const head = job.head.filter((l) => !isBlank(l)).map(normalise);
  const at = head.findIndex((l) => /^\s*permissions:/.test(l));
  if (at < 0) return "it has no `permissions:` of its own, so it holds whatever the workflow or the repository grants";
  const value = /^\s*permissions:\s*(.*?)\s*$/.exec(head[at])[1];
  if (value !== "") return `its \`permissions:\` is ${value}, not a list of scopes`;
  const found = {};
  for (const line of head.slice(at + 1)) {
    if (indentOf(line) <= indentOf(head[at])) break;
    const m = /^\s*([a-z-]+):\s*(\S+)\s*$/.exec(line);
    if (!m) return `its \`permissions:\` has a line this guard cannot read: ${line.trim()}`;
    found[m[1]] = m[2];
  }
  return found;
}

/** The notifier's invocation, as the one line its step may run. */
const NOTIFIER_RUN = `run: node ${SCRIPTS}/${NOTIFIER}`;
/**
 * THE THREE STEPS A NOTIFIER JOB HAS, and every line each may contain (after
 * comments and the leading `- ` are dropped). An allow-list, because the job
 * holds a token that can write and the thing to refuse is "anything else":
 * a fourth step, an install, a credential left in `.git/config`, a token
 * from somewhere other than the job itself.
 */
const NOTIFIER_STEPS = [
  {
    what: "the checkout",
    is: (lines) => lines.some((l) => /^uses: actions\/checkout@/.test(l)),
    allowed: [
      /^uses: actions\/checkout@v\d+$/,
      /^with:$/,
      /^ref: (.+)$/,
      /^sparse-checkout: \|$/,
      /^scripts\/agent$/,
      /^\.github\/agent-harness$/,
      /^sparse-checkout-cone-mode: false$/,
      /^persist-credentials: false$/,
    ],
    required: ["persist-credentials: false"],
  },
  {
    what: "the Node setup",
    is: (lines) => lines.some((l) => /^uses: actions\/setup-node@/.test(l)),
    allowed: [/^uses: actions\/setup-node@v\d+$/, /^with:$/, /^node-version: ["']?[\d.x]+["']?$/],
    required: [],
  },
  {
    what: "the script",
    is: (lines) => lines.includes(NOTIFIER_RUN),
    allowed: [/^name: .+$/, /^env:$/, /^GH_TOKEN: \$\{\{ github\.token \}\}$/, new RegExp(`^${NOTIFIER_RUN.replace(/[.\/]/g, "\\$&")}$`)],
    required: [NOTIFIER_RUN],
  },
];

/**
 * What is wrong with a job that runs the upstream notifier, as sentences.
 * The notifier writes issues, so the job is one that PULL-REQUEST-CONTROLLED
 * CODE CAN NEVER BE IN: started by a clock or a maintainer only, holding two
 * permissions, and naming no secret. (That its checkout is the default
 * branch's is the walk's ordinary rule, and `workflow_dispatch` is not an
 * event whose default ref it believes — so the checkout must say `main`.)
 *
 * AND THE WORKFLOW AROUND IT IS HELD TO ITS SHAPE. The three rules above are
 * about the job that runs the script; none of them sees a second job in the
 * same file, or a fourth step in this one, and either runs in a workflow a
 * clock starts with a token nobody is watching. So: the workflow grants
 * nothing by default, has this job and no other, the job has exactly the
 * three steps of `NOTIFIER_STEPS`, and nothing in the file reads a secret, a
 * variable, or an action that is not GitHub's own checkout or setup-node.
 */
function notifierProblems(job, wf) {
  const problems = [];
  const stray = job.events.filter((e) => !NOTIFIER_EVENTS.has(e));
  if (job.events.length === 0 || stray.length > 0) {
    problems.push(`runs ${NOTIFIER}, which writes issues, in a workflow that ${stray.join(", ") || "no readable event"} can start; only ${[...NOTIFIER_EVENTS].join(" and ")} may`);
  }
  const held = jobPermissions(job);
  if (typeof held === "string") problems.push(`runs ${NOTIFIER}, but ${held}`);
  else if (JSON.stringify(Object.entries(held).sort()) !== JSON.stringify(Object.entries(NOTIFIER_PERMISSIONS).sort())) {
    const show = (o) => Object.entries(o).map(([k, v]) => `${k}: ${v}`).join(", ") || "none";
    problems.push(`runs ${NOTIFIER} holding ${show(held)}; it may hold exactly ${show(NOTIFIER_PERMISSIONS)}`);
  }

  // --- the workflow: nothing granted by default, and no other job.
  const code = (lines) => lines.filter((l) => !isBlank(l)).map((l) => normalise(l).trimEnd());
  if (!code(wf.preamble).includes("permissions: {}")) {
    problems.push(`runs ${NOTIFIER} in a workflow without a top-level \`permissions: {}\`; a job added beside it would inherit whatever the repository grants`);
  }
  const others = wf.jobs.filter((j) => j !== job).map((j) => j.name);
  if (others.length > 0) {
    problems.push(`runs ${NOTIFIER} in a workflow that has other jobs (${others.join(", ")}); the notifier's workflow holds one job, so that nothing else runs on its clock`);
  }

  // --- nothing from outside the job: no secret, no variable, no other action.
  // (`secrets.GITHUB_TOKEN` is spelt `github.token` here, so that the rule is
  // one a reader can check by eye: the words do not appear.)
  const everything = code([...wf.preamble, ...wf.jobs.flatMap((j) => [...j.head, ...j.steps.flatMap((st) => st.lines)])]);
  for (const [word, what] of [["secrets", "a secret"], ["vars", "a repository variable"]]) {
    const line = everything.find((l) => new RegExp(`\\b${word}\\b`).test(l));
    if (line) problems.push(`runs ${NOTIFIER} in a workflow that names ${what} (\`${line.trim().slice(0, 80)}\`); the job's own \`github.token\` is all it may use`);
  }
  for (const line of everything) {
    const m = /^\s*(?:- )?uses:\s*["']?([^\s"'#]+)/.exec(line);
    if (m && !/^actions\/(?:checkout|setup-node)@/.test(m[1])) {
      problems.push(`runs ${NOTIFIER} in a workflow that uses ${m[1]}; only actions/checkout and actions/setup-node may run beside a token that can write`);
    }
  }

  // --- the steps: these three, in this order, and each line of each on its list.
  const steps = job.steps.map((st) => code(st.lines).map((l) => l.trim().replace(/^- /, "")));
  const kinds = steps.map((lines) => NOTIFIER_STEPS.find((k) => k.is(lines)));
  steps.forEach((lines, i) => {
    const kind = kinds[i];
    if (!kind) {
      problems.push(`runs ${NOTIFIER} in a job with a step that is none of the three it may have (\`${(lines[0] ?? "").slice(0, 80)}\`); the checkout, the Node setup and the script are all that run beside its token`);
      return;
    }
    for (const line of lines) {
      if (!kind.allowed.some((re) => re.test(line))) problems.push(`runs ${NOTIFIER} in a job whose step for ${kind.what} has a line it may not have: \`${line.slice(0, 80)}\``);
    }
    for (const line of kind.required) {
      if (!lines.includes(line)) problems.push(`runs ${NOTIFIER} in a job whose step for ${kind.what} lacks \`${line}\``);
    }
    const ref = /^ref: (.+)$/.exec(lines.find((l) => l.startsWith("ref: ")) ?? "");
    if (kind === NOTIFIER_STEPS[0] && !(ref && TRUSTED_REFS.has(ref[1]))) {
      problems.push(`runs ${NOTIFIER} in a job whose checkout does not name the default branch (\`ref: ${ref ? ref[1] : "<none>"}\`)`);
    }
  });
  if (kinds.length !== NOTIFIER_STEPS.length || kinds.some((k, i) => k !== NOTIFIER_STEPS[i])) {
    problems.push(`runs ${NOTIFIER} in a job with ${steps.length} steps (${kinds.map((k) => (k ? k.what : "something else")).join(", ")}); it has exactly three: ${NOTIFIER_STEPS.map((k) => k.what).join(", ")}`);
  }
  return problems;
}

const GIT_SWITCH = /\bgit((?:\s+-[Cc]\s+\S+)*)\s+(?:checkout|switch|fetch|pull|merge|reset|restore|worktree|clone|apply|am|cherry-pick|rebase|stash)(?![\w-])/;

/**
 * Walk every job and return what was found: `problems` (each a sentence naming
 * the workflow and job) and `counts`. `workflows` is `[{ file, text }]`.
 */
function inspect(workflows) {
  const problems = [];
  const counts = {
    trustedSparseCheckouts: 0,
    routerCheckouts: 0,
    toolchainUses: 0,
    toolchainCheckouts: 0,
    sideCheckoutsOverUntrustedWorkspace: 0,
    scopeHooks: 0,
    reverifiedCheckouts: 0,
    stagingCopies: 0,
    invocations: 0,
    configReadingInvocations: 0,
    fromWorkspace: 0,
    fromSideDirectory: 0,
    fromStagedCopy: 0,
    notifierJobs: 0,
    jobs: 0,
    files: 0,
  };

  for (const { file, text } of workflows) {
    const wf = parseWorkflow(file, text);
    counts.files++;
    if (wf.unreadable) {
      problems.push(`${file}: ${wf.unreadable}`);
      continue;
    }
    if (wf.unparsed) problems.push(`${file}: ${wf.unparsed}`);
    // Workflow-level `env:` reaches every step of every job.
    for (const line of wf.preamble) {
      if (!isComment(line) && FORBIDDEN_ENV.test(normalise(line))) {
        problems.push(`${file}: sets ${FORBIDDEN_ENV.exec(line)[0]} for the whole workflow: ${line.trim().slice(0, 100)}`);
      }
    }
    for (const job of wf.jobs) {
      counts.jobs++;
      const at = `${file} › ${job.name}`;
      const flag = (message) => problems.push(`${at}: ${message}`);

      // The trees this job has, and whether each is still the default branch's.
      // `{ trusted, why, scripts: "all" | "router" | "none", config: boolean }`.
      let workspace = null;
      const side = new Map();
      const staged = { tools: null, config: null, exportedAtStep: -1 };
      let afterAgent = false;
      let notifies = false;
      // Workspace paths an `rm -rf` has cleared since the workspace last changed.
      const cleared = new Set();
      // Has the step that re-verifies the checked-out commit run yet? Only asked
      // in a job that has one.
      const reverifyAt = job.steps.findIndex((s) => s.lines.some((l) => !isComment(l) && REVERIFY.test(l)));
      if (reverifyAt >= 0) counts.reverifiedCheckouts++;

      const distrust = (why) => {
        if (workspace) workspace = { ...workspace, trusted: false, why };
        // A side directory inside the workspace is only as safe as the
        // workspace: a branch can track files at that very path.
        for (const [dir, tree] of side) if (!dir.startsWith(TEMP)) side.set(dir, { ...tree, trusted: false, why });
      };

      // Job-level keys: `env:` and `defaults.run.working-directory` apply to
      // every step below.
      const jobHead = job.head.filter((l) => !isComment(l)).map(normalise);
      for (const line of jobHead) {
        if (FORBIDDEN_ENV.test(line)) flag(`sets ${FORBIDDEN_ENV.exec(line)[0]} for the whole job: ${line.trim().slice(0, 100)}`);
      }
      const jobChangesDirectory = jobHead.some((l) => /^\s*working-directory:/.test(l));
      const directoryChangeAllowed = DIRECTORY_CHANGE_ALLOWED.has(at);

      job.steps.forEach((step, stepIndex) => {
        // ---- a checkout -------------------------------------------------------
        if (uses(step, "actions/checkout")) {
          const patterns = sparsePatterns(step);
          const ref = scalar(step, "ref");
          const repository = scalar(step, "repository");
          // `path: .` and the workspace spelt out are the workspace.
          const rawTarget = scalar(step, "path");
          const target = rawTarget === undefined || /^(?:\.\/?|\$\{\{ github\.workspace \}\}\/?)$/.test(rawTarget) ? undefined : rawTarget;

          let trusted = true;
          let why = "";
          if (repository !== undefined) {
            trusted = false;
            why = `it names a repository (${repository}); only this repository's default branch is trusted`;
          } else if (ref !== undefined) {
            trusted = TRUSTED_REFS.has(ref);
            why = `its ref is ${ref}`;
          } else {
            const untrustedEvents = job.events.filter((e) => !TRUSTED_DEFAULT_REF_EVENTS.has(e));
            trusted = job.events.length > 0 && untrustedEvents.length === 0;
            why = `it has no ref, and the workflow runs on ${untrustedEvents.join(", ") || "no readable event"}`;
          }

          const hasDir = (dir) => patterns === null || patterns.includes(dir);
          const scripts = hasDir(SCRIPTS) ? "all" : patterns.length === 1 && patterns[0] === ROUTER ? "router" : "none";
          const tree = { trusted, why, scripts, config: hasDir(CONFIG), toolchain: hasDir(TOOLCHAIN) };
          if (patterns !== null && tree.toolchain && trusted) counts.toolchainCheckouts++;

          if (patterns !== null) {
            const partial = patterns.some((p) => p.startsWith(`${SCRIPTS}/`));
            if (scripts === "router") counts.routerCheckouts++;
            else if (partial && scripts !== "all") {
              flag(`a sparse checkout of part of ${SCRIPTS} (${patterns.join(", ")}) — only the whole directory, or ${ROUTER} alone, is a shape this guard can check`);
            }
            if (scripts === "all") {
              if (trusted) counts.trustedSparseCheckouts++;
              if (!tree.config) flag(`checks out ${SCRIPTS} without ${CONFIG}; every script that reads the configuration throws at import there`);
              if (scalar(step, "sparse-checkout-cone-mode") !== "false") {
                flag("a sparse checkout of the scripts without `sparse-checkout-cone-mode: false`, which widens it to every top-level file");
              }
            }
            if (scripts !== "all" && patterns.includes(CONFIG)) flag(`fetches ${CONFIG} without the scripts that read it`);
          }

          // A SIDE DIRECTORY INSIDE A PULL REQUEST'S TREE IS A PATH THE PULL
          // REQUEST CHOSE THE CONTENTS OF. A symlink committed there is
          // followed by the checkout, which empties whatever it points at —
          // the staged scripts, say. So the path is removed first, in this
          // job, after the tree that could have planted it was checked out.
          if (target !== undefined && !target.startsWith(TEMP) && workspace && !workspace.trusted) {
            counts.sideCheckoutsOverUntrustedWorkspace++;
            if (!cleared.has(target)) {
              flag(`checks out into ${target} inside an untrusted workspace without an \`rm -rf ${target}\` first; the branch may have planted a symlink there`);
            }
          }
          if (target === undefined) cleared.clear();
          else cleared.delete(target);

          if (target === undefined) {
            workspace = tree;
            // Anything already sitting in the workspace is now under a tree
            // that may track files at its path.
            if (!trusted) {
              for (const [dir, t] of side) {
                if (!dir.startsWith(TEMP)) side.set(dir, { ...t, trusted: false, why: "an untrusted checkout into the workspace followed it" });
              }
            }
          } else {
            side.set(target, tree);
          }
          return;
        }

        const isAgent = uses(step, "anthropics/claude-code-action");

        // ---- a local action ------------------------------------------------------
        // Read from the workspace when the step runs, and run with whatever the
        // job holds. One is allowed, from a tree that is trusted right now.
        for (const rawLine of step.lines) {
          if (isComment(rawLine)) continue;
          const local = /^\s*(?:- )?uses:\s*["']?(\.{1,2}\/[^\s"'#]*)/.exec(normalise(rawLine));
          if (!local) continue;
          const ref = local[1].replace(/\/+$/, "");
          const m = /^\.\/(?:(.+)\/)?\.github\/actions\/agent-toolchain$/.exec(ref);
          if (!m || ref.includes("..")) {
            flag(`uses the local action ${ref}; the only local action a workflow may use is ${TOOLCHAIN}, from a trusted tree`);
            continue;
          }
          counts.toolchainUses++;
          // A toolchain action may install what the checked-out commit asks
          // for. Where a job goes on to check that the commit is the one that
          // was reviewed, that check comes first — or the dependencies of an
          // unreviewed commit are installed before anything refuses it.
          if (reverifyAt >= 0 && stepIndex < reverifyAt) {
            flag("sets up the toolchain before re-verifying the checked-out commit; a commit pushed in between would have its dependencies installed before it is refused");
          }
          const root = m[1] ?? "";
          const tree = root === "" ? workspace : side.get(root);
          const name = root || "the workspace";
          if (!tree) flag(`uses the toolchain action from ${name}, which no checkout in this job produced`);
          else if (!tree.trusted) flag(`uses the toolchain action from ${name}, which is not trusted at that step: ${tree.why}`);
          else if (afterAgent) flag(`uses the toolchain action from ${name} after the agent ran in this job`);
          else if (!tree.toolchain) flag(`uses the toolchain action from ${name}, whose checkout does not include ${TOOLCHAIN}`);
        }
        // Where a relative path resolves is no longer the workspace root once
        // the directory changes. Not resolved — refused, below.
        let directoryChanged = jobChangesDirectory || scalar(step, "working-directory") !== undefined;
        const code = step.lines.filter((l) => !isComment(l)).map((l) => normalise(l).trim());
        const writesHook = code.some((l) => /(?:^|[\s>"'/])pre-push["']?\s*$/.test(l) && l.includes(">"));
        if (writesHook) {
          counts.scopeHooks++;
          // WHERE GIT WILL LOOK, not where hooks usually live. `.git/hooks` is
          // ignored once `core.hooksPath` is set, so a hook written there can
          // be installed and never run; and a hooks directory inside the
          // workspace is one the branch can have put files in.
          const dir = `HOOKS="${HOOK_DIR}"`;
          if (!code.includes(dir)) flag(`installs a pre-push hook somewhere other than ${HOOK_DIR}; git ignores .git/hooks when core.hooksPath is set`);
          if (!code.some((l) => l.endsWith('> "$HOOKS/pre-push"'))) flag("installs a pre-push hook that is not written to \"$HOOKS/pre-push\"");
          if (!code.includes('git config core.hooksPath "$HOOKS"')) flag("installs a pre-push hook without pointing core.hooksPath at it, so git may never run it");
          if (!code.includes('EFFECTIVE="$(git rev-parse --path-format=absolute --git-path hooks)"') || !code.includes('if [ "$EFFECTIVE" != "$HOOKS" ]; then')) {
            flag("installs a pre-push hook without checking where git will read hooks from");
          }
        }
        const hookNamesConfig =
          step.lines.some((l) => !isComment(l) && l.includes(`export ${ENV}="%s"`)) &&
          step.lines.some((l) => !isComment(l) && normalise(l).trim() === `"${STAGED_CONFIG}" \\`);

        for (const rawLine of step.lines) {
          if (isComment(rawLine)) continue;
          const line = normalise(rawLine);
          const trimmed = line.trim();
          if (trimmed === "") continue;

          if (FORBIDDEN_ENV.test(line) && /\bNODE_OPTIONS\b/.test(line)) flag(`sets or reads NODE_OPTIONS: ${trimmed.slice(0, 100)}`);

          // ---- a directory change, or a package script ---------------------------
          if (!isAgent && !directoryChangeAllowed) {
            if (/(?:^|[\s;&|(])(?:cd|pushd)\s/.test(line)) directoryChanged = true;
            if (!/^\/\//.test(trimmed) && /\bnpm\s+(?:(?:--prefix|-C)\s+\S+\s+)?(?:run|run-script|test|start|exec|x)\b|(?:^|[\s;&|(])npx\s/.test(line)) {
              flag(`runs a package script (\`${trimmed.slice(0, 80)}\`), which this guard cannot follow to the scripts it runs`);
            }
            if (directoryChanged) {
              // After a directory change a bare `node set-state.mjs` IS a harness script.
              for (const m of line.matchAll(/\bnode\s+(?:--[\w=-]+\s+)*["']?(?:\.\/)?([\w-]+\.mjs)\b/g)) {
                if (MODULES.includes(m[1])) flag(`runs ${m[1]} by a bare path after a directory change; run it by its path from the workspace root or the runner's temp directory`);
              }
            }
          }

          // ---- something that changes what the workspace holds -----------------
          // (Not inside the agent's own prompt: the agent step distrusts
          // everything when it ends, below.)
          const git = isAgent ? null : GIT_SWITCH.exec(line);
          if (git || (!isAgent && /\bgh pr checkout\b/.test(line))) {
            // `git -C <dir> …` acts on that directory: a side directory if it
            // names one, otherwise (`.`, the workspace path, anything this
            // guard cannot place) the workspace.
            const dashC = git && /-C\s+["']?([^\s"']+)/.exec(git[1]);
            const target = dashC ? dashC[1].replace(/^\.\//, "").replace(/\/$/, "") : "";
            const why = `\`${trimmed.slice(0, 60)}\` ran there`;
            if (target !== "" && side.has(target)) side.set(target, { ...side.get(target), trusted: false, why });
            else distrust(why);
          }

          // ---- side directories moved or removed --------------------------------
          const moved = /^\s*mv (\.[\w.-]+) "?(\$RUNNER_TEMP\/[\w.-]+)"?\s*$/.exec(line);
          if (moved && side.has(moved[1])) {
            side.set(moved[2], side.get(moved[1]));
            side.delete(moved[1]);
          }
          const removed = /^\s*(?:run:\s*)?rm -rf ((?:\.[\w.-]+\s*)+)$/.exec(line);
          if (removed) {
            for (const dir of removed[1].trim().split(/\s+/)) {
              side.delete(dir);
              cleared.add(dir);
            }
          }

          // ---- the staging copy --------------------------------------------------
          const stage = /\bcp -R (\S*?)(scripts\/agent|\.github\/agent-harness) "([^"]+)"\s*$/.exec(line);
          if (stage) {
            const [, prefix, what, dest] = stage;
            const root = prefix.replace(/^\.\//, "").replace(/\/$/, "");
            const source = root === "" ? workspace : side.get(root);
            const isTools = what === SCRIPTS;
            const want = isTools ? STAGED_TOOLS : STAGED_CONFIG;
            const has = source && (isTools ? source.scripts === "all" : source.config);
            const from = root || "the workspace";
            if (dest !== want) flag(`copies ${what} to ${dest}; the staged copy lives at ${want}`);
            else if (!source) flag(`stages ${what} from ${from}, which no checkout in this job produced`);
            else if (!source.trusted) flag(`stages ${what} from ${from}, which is not trusted there: ${source.why}`);
            else if (!has) flag(`stages ${what} from ${from}, whose checkout does not include it`);
            else if (afterAgent) flag(`stages ${what} after the agent ran`);
            else if (isTools) {
              staged.tools = { root };
              counts.stagingCopies++;
            } else if (!staged.tools || staged.tools.root !== root) {
              flag(`stages ${CONFIG} from ${from}, not from the tree the scripts were staged from`);
            } else {
              staged.config = { root };
            }
            continue;
          }

          // ---- naming the staged configuration -----------------------------------
          if (trimmed === `echo "${ENV}=${STAGED_CONFIG}" >> "$GITHUB_ENV"`) {
            if (!staged.config) flag(`exports ${ENV} before a trusted configuration was staged`);
            else staged.exportedAtStep = stepIndex;
            continue;
          }

          // Every OTHER mention of the configuration is accounted for, or it is
          // a problem: a second writer, a step `env:`, a copy from somewhere
          // else. One writer per job means one answer to "which configuration
          // did this job read".
          const inlineEnv = `${ENV}=${STAGED_CONFIG} node ${STAGED_TOOLS}/`;
          // A step's `name:` is a label, not a command.
          const isLabel = /^(?:- )?name:\s/.test(trimmed);
          if (!isLabel && (line.includes("agent-harness") || line.includes(ENV))) {
            const rest = trimmed.slice(inlineEnv.length);
            const accounted =
              (writesHook && (line.includes(`export ${ENV}="%s"`) || trimmed === `"${STAGED_CONFIG}" \\`)) ||
              (trimmed.startsWith(inlineEnv) && !rest.includes("agent-harness") && !rest.includes(ENV));
            if (!accounted) {
              flag(
                `touches the harness configuration outside the staging step: ${trimmed.slice(0, 120)} ` +
                  `(any mention of \`agent-harness\` or ${ENV} outside a comment or a step name is refused unless it is ${ACCEPTED_FORMS})`,
              );
            }
          }
          if (/\b(?:cp|mv|ln|rsync|rm)\b[^|;&]*\$RUNNER_TEMP\/agent-tools(?!\/)/.test(line)) {
            flag(`rewrites the staged scripts outside the staging step: ${trimmed.slice(0, 120)}`);
          }

          // ---- running a harness script -------------------------------------------
          if (/^\[ -f /.test(trimmed)) continue; // an existence test runs nothing
          for (const m of line.matchAll(/([^\s"'`(=]*?)(scripts\/agent|agent-tools)\/([\w-]+\.mjs)\b/g)) {
            const [whole, prefix, kind, script] = m;
            const before = line.slice(0, m.index);
            const run = /\bnode\s+(?:--[\w=-]+\s+)*["']?$/.test(before) || /\b(?:import|require)\s*\(\s*["'`]$/.test(before);
            // The hook body is written with printf: the script is an argument
            // there, run later by git.
            const hookArg = writesHook && kind === "agent-tools" && !run;
            if (!run && !hookArg) continue;

            counts.invocations++;
            if (script === NOTIFIER) notifies = true;
            const reads = READS_CONFIG.has(script);
            if (reads) counts.configReadingInvocations++;
            if (!MODULES.includes(script)) flag(`runs ${script}, which is not a module of this package`);

            if (kind === "agent-tools") {
              counts.fromStagedCopy++;
              if (prefix !== `${TEMP}/`) flag(`runs ${whole}; the staged copy is ${STAGED_TOOLS}`);
              else if (!staged.tools) flag(`runs ${script} from the staged copy, which this job did not stage from a trusted tree`);
              else if (afterAgent) {
                flag(`runs ${script} from the staged copy after the agent ran; the agent can rewrite everything under the runner's temp directory`);
              } else if (!reads) {
                // Trusted, and it needs no configuration.
              } else if (!staged.config) {
                flag(`runs ${script}, which reads the configuration, from a staged copy with no trusted configuration staged beside it`);
              } else if (hookArg) {
                if (!hookNamesConfig) flag(`installs a hook that runs ${script} without naming the staged configuration in the hook itself`);
              } else if (isAgent) {
                // The fixer's shell is not this job's shell. The command it is
                // given must carry the variable itself.
                if (!before.trimStart().startsWith(`${ENV}=${STAGED_CONFIG} `)) {
                  flag(`tells the agent to run ${script} without \`${ENV}=${STAGED_CONFIG}\` on the command itself`);
                }
              } else if (!(staged.exportedAtStep >= 0 && staged.exportedAtStep < stepIndex)) {
                // `$GITHUB_ENV` reaches LATER steps only.
                flag(`runs ${script}, which reads the configuration, before ${ENV} is exported by an EARLIER step`);
              }
              continue;
            }

            // A checkout: the workspace, or a side directory.
            const root = prefix.replace(/^\$GITHUB_WORKSPACE\//, "").replace(/^\.\//, "").replace(/\/$/, "");
            const tree = root === "" ? workspace : side.get(root);
            const name = root || "the workspace";
            if (root === "") counts.fromWorkspace++;
            else counts.fromSideDirectory++;
            if (directoryChanged && !root.startsWith(TEMP) && !prefix.startsWith("$GITHUB_WORKSPACE/") && !directoryChangeAllowed) {
              flag(`runs ${script} by a relative path in a step that changes directory (\`working-directory:\` or \`cd\`); the path no longer means ${name}`);
            } else if (!tree) flag(`runs ${script} from ${name}, which no checkout in this job produced`);
            else if (!tree.trusted) flag(`runs ${script} from ${name}, which is not trusted at that step: ${tree.why}`);
            else if (afterAgent && !root.startsWith(TEMP)) flag(`runs ${script} from ${name} after the agent ran in this job`);
            else if (tree.scripts === "none" || (tree.scripts === "router" && `${SCRIPTS}/${script}` !== ROUTER)) {
              flag(`runs ${script} from ${name}, whose checkout does not include it`);
            } else if (reads && !tree.config) {
              flag(`runs ${script}, which reads the configuration, from ${name}, whose checkout does not include ${CONFIG}`);
            }
          }
        }

        if (isAgent) {
          afterAgent = true;
          distrust("the agent ran in this job and owns the workspace");
        }
      });

      if (notifies) {
        counts.notifierJobs++;
        for (const problem of notifierProblems(job, wf)) flag(problem);
      }
    }
  }
  return { problems, counts };
}

// --- the workflows as they are ------------------------------------------------------

const WORKFLOWS = readdirSync(WF_DIR)
  .filter((f) => /^agent-.*\.ya?ml$/.test(f))
  .sort()
  .map((file) => ({ file, text: readFileSync(path.join(WF_DIR, file), "utf8") }));
const REAL = inspect(WORKFLOWS);

test("the workflows were read: every agent workflow, with jobs and steps", () => {
  assert.ok(REAL.counts.files >= 9, `expected the agent workflows, read ${REAL.counts.files}`);
  assert.ok(REAL.counts.jobs >= 25, `expected their jobs, read ${REAL.counts.jobs}`);
  // Jobs and steps, counted a second way: by the keys that open them, read
  // straight off each file with no parsing of structure at all.
  for (const { file, text } of WORKFLOWS) {
    const parsed = parseWorkflow(file, text);
    assert.equal(parsed.unparsed, null, `${file}: ${parsed.unparsed}`);
    const runsOn = text.split("\n").filter((l) => /^\s+(?:runs-on|uses):\s/.test(l) && indentOf(l) === 4 && !isComment(l)).length;
    assert.equal(parsed.jobs.length, runsOn, `${file}: read ${parsed.jobs.length} jobs, but the file has ${runsOn} \`runs-on:\` keys`);
    for (const job of parsed.jobs) assert.ok(job.steps.length > 0, `${file} › ${job.name}: no steps were read`);
  }
  assert.ok(READS_CONFIG.size >= 20 && READS_CONFIG.has("set-state.mjs"), "the import walk found too few configuration readers");
  assert.ok(!READS_CONFIG.has("command.mjs"), "the router must not read the configuration");
});

test("every harness script a workflow runs comes from a tree that is trusted at that step", () => {
  assert.deepEqual(REAL.problems, []);
});

test("the guard inspected the sites it is expected to", () => {
  const { files: _f, jobs: _j, ...sites } = REAL.counts;
  assert.deepEqual(
    sites,
    EXPECTED,
    "a checkout, a staging copy or a script invocation was added or removed — look at the new site, then update the count",
  );
  assert.equal(sites.fromWorkspace + sites.fromSideDirectory + sites.fromStagedCopy, sites.invocations);
});

test("the repository has the configuration the default resolution expects", () => {
  // What a full checkout of the default branch — the promote and stalled jobs,
  // the workspace before a branch checkout — relies on being there.
  for (const file of ["config.json", "coverage-note.md"]) {
    assert.ok(existsSync(path.join(ROOT, CONFIG, file)), `${CONFIG}/${file} is missing from this repository`);
  }
});

// --- mutations: each must FAIL the guard -----------------------------------------------
//
// Every case here is a change that hands a pull request the scripts or the
// configuration it is judged by. The first six passed the step-shaped guard
// this file replaced. They run against the real workflows with one edit
// applied in memory, so they keep testing the guard against the workflows as
// they are, not against a fixture that drifts.

const edit = (file, change) =>
  WORKFLOWS.map((w) => {
    if (w.file !== file) return w;
    try {
      return { file, text: change(w.text) };
    } catch (err) {
      throw new Error(`${file}: ${err.message}`);
    }
  });
const addJob = (file, job) => edit(file, (t) => `${t}\n${job}`);
const PR_HEAD = "${{ github.event.workflow_run.head_sha }}";
const replaceOnce = (text, anchor, replacement) => {
  assert.equal(text.split(anchor).length - 1, 1, `mutation anchor not found exactly once: ${anchor.slice(0, 60)}`);
  return text.replace(anchor, () => replacement);
};
const NOTIFY_FILE = "agent-upstream-notify.yml";
const NOTIFY_STEP = "      - name: Open, update or close the sync issue\n";
const EXPORT_LINE = '          echo "AGENT_HARNESS_CONFIG_DIR=${{ runner.temp }}/agent-harness" >> "$GITHUB_ENV"\n';

const MUTATIONS = [
  {
    name: "a new job runs a configuration reader from a full checkout of the pull request's branch",
    files: () =>
      edit("agent-sweep.yml", (t) => `${t}
  evil:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: \${{ github.event.workflow_run.head_branch }}
      - run: node ./scripts/agent/set-state.mjs "$PR" blocked
`),
    expect: /agent-sweep\.yml › evil: runs set-state\.mjs from the workspace, which is not trusted at that step/,
  },
  {
    name: "a `repository:` points a trusted-looking checkout at a fork",
    files: () =>
      edit("agent-review-panel.yml", (t) =>
        replaceOnce(
          t,
          "          ref: main\n          path: .trusted\n",
          "          repository: ${{ github.event.workflow_run.head_repository.full_name }}\n          ref: main\n          path: .trusted\n",
        )),
    expect: /agent-review-panel\.yml › review-panel: runs review-panel\.mjs from \.trusted, which is not trusted at that step: it names a repository/,
  },
  {
    name: "`gh pr checkout` switches the workspace before the staging copy",
    files: () =>
      edit("agent-fix.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Stage the trusted agent scripts\n",
          '      - name: switch\n        run: gh pr checkout "$PR"\n      - name: Stage the trusted agent scripts\n',
        )),
    expect: /agent-fix\.yml › fix: stages scripts\/agent from the workspace, which is not trusted there/,
  },
  {
    name: "a sparse pattern written `/scripts/agent` brings the scripts without the configuration",
    files: () =>
      edit("agent-sweep.yml", (t) => `${t}
  extra:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: main
          sparse-checkout: |
            /scripts/agent
          sparse-checkout-cone-mode: false
      - run: node scripts/agent/set-state.mjs "$PR" blocked
`),
    expect: /agent-sweep\.yml › extra: runs set-state\.mjs, which reads the configuration, from the workspace, whose checkout does not include \.github\/agent-harness/,
  },
  {
    name: "an invocation moves from the trusted side directory to the pull request's workspace",
    files: () =>
      edit("agent-review-on-demand.yml", (t) =>
        replaceOnce(t, "node .trusted/scripts/agent/metrics.mjs summarize", "node ./scripts/agent/metrics.mjs summarize")),
    expect: /agent-review-on-demand\.yml › review: runs metrics\.mjs from the workspace, which is not trusted at that step/,
  },
  {
    name: "the staged configuration is copied over from the workspace after it was exported",
    files: () =>
      edit("agent-fix.yml", (t) =>
        replaceOnce(
          t,
          EXPORT_LINE,
          `${EXPORT_LINE}          rm -rf "\${{ runner.temp }}/agent-harness" && cp -R "$GITHUB_WORKSPACE/pr/.github/agent-harness" "\${{ runner.temp }}/agent-harness"\n`,
        )),
    expect: /agent-fix\.yml › fix: touches the harness configuration outside the staging step/,
  },
  // …and the neighbours of those six.
  {
    name: "a trusted checkout loses `.github/agent-harness`",
    files: () => edit("agent-sweep.yml", (t) => replaceOnce(t, "            .github/agent-harness\n", "")),
    expect: /agent-sweep\.yml › sweep: checks out scripts\/agent without \.github\/agent-harness/,
  },
  {
    name: "a trusted checkout's ref becomes the pull request's head",
    files: () => edit("agent-sweep.yml", (t) => replaceOnce(t, "          ref: main\n", "          ref: ${{ github.event.pull_request.head.sha }}\n")),
    expect: /agent-sweep\.yml › sweep: runs loop-sweep\.mjs from the workspace, which is not trusted at that step: its ref is/,
  },
  {
    name: "a ref-less checkout in a workflow a pull request can start",
    files: () => edit("agent-rerun.yml", (t) => replaceOnce(t, "\non:\n  issue_comment:\n", "\non:\n  pull_request_target:\n  issue_comment:\n")),
    expect: /agent-rerun\.yml › rerun: runs loop-status\.mjs from the workspace, which is not trusted at that step: it has no ref, and the workflow runs on pull_request_target/,
  },
  {
    name: "the variable is pointed into the workspace",
    files: () =>
      edit("agent-fix.yml", (t) =>
        replaceOnce(t, EXPORT_LINE, '          echo "AGENT_HARNESS_CONFIG_DIR=$GITHUB_WORKSPACE/.github/agent-harness" >> "$GITHUB_ENV"\n')),
    expect: /agent-fix\.yml › fix: touches the harness configuration outside the staging step/,
  },
  {
    name: "a step `env:` names a different configuration",
    files: () =>
      edit("agent-iterate-ci.yml", (t) =>
        replaceOnce(
          t,
          '        run: node "$RUNNER_TEMP/agent-tools/set-state.mjs" "$PR" fixing\n',
          '        env:\n          AGENT_HARNESS_CONFIG_DIR: ${{ github.workspace }}/.github/agent-harness\n        run: node "$RUNNER_TEMP/agent-tools/set-state.mjs" "$PR" fixing\n',
        )),
    expect: /agent-iterate-ci\.yml › iterate: touches the harness configuration outside the staging step/,
  },
  {
    name: "the staging step stops exporting the variable",
    files: () => edit("agent-review-panel.yml", (t) => replaceOnce(t, EXPORT_LINE, "")),
    expect: /agent-review-panel\.yml › fix: runs [\w-]+\.mjs, which reads the configuration, before AGENT_HARNESS_CONFIG_DIR is exported/,
  },
  {
    name: "the fixer is told to run a configuration reader without the variable on the command",
    files: () =>
      edit("agent-fix.yml", (t) =>
        replaceOnce(
          t,
          "AGENT_HARNESS_CONFIG_DIR=${{ runner.temp }}/agent-harness node ${{ runner.temp }}/agent-tools/fix-report.mjs post",
          "node ${{ runner.temp }}/agent-tools/fix-report.mjs post",
        )),
    expect: /agent-fix\.yml › fix: tells the agent to run fix-report\.mjs without `AGENT_HARNESS_CONFIG_DIR=/,
  },
  {
    name: "the pre-push hook stops naming the staged configuration",
    files: () =>
      edit("agent-iterate-ci.yml", (t) =>
        replaceOnce(
          replaceOnce(t, 'export AGENT_HARNESS_CONFIG_DIR="%s"\\n', ""),
          '            "$RUNNER_TEMP/agent-harness" \\\n',
          "",
        )),
    expect: /agent-iterate-ci\.yml › iterate: installs a hook that runs fix-guard\.mjs without naming the staged configuration/,
  },
  {
    name: "a script is run from the staged copy after the agent",
    files: () =>
      edit("agent-fix.yml", (t) => {
        const anchor = "      # EVERYTHING AFTER THE AGENT RUNS IN THE `report` JOB BELOW";
        return replaceOnce(t, anchor, `      - name: late\n        run: node "$RUNNER_TEMP/agent-tools/set-state.mjs" "$PR" blocked\n\n${anchor}`);
      }),
    expect: /agent-fix\.yml › fix: runs set-state\.mjs from the staged copy after the agent ran/,
  },
  {
    name: "a `git fetch` and checkout replace a trusted workspace before a script runs",
    files: () =>
      edit("agent-sweep.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Sweep the loop PRs\n",
          '      - run: git fetch origin "$BRANCH" && git checkout FETCH_HEAD\n      - name: Sweep the loop PRs\n',
        )),
    expect: /agent-sweep\.yml › sweep: runs loop-sweep\.mjs from the workspace, which is not trusted at that step: `/,
  },
  // …directory changes: the path on the line is no longer the path that runs.
  {
    name: "the pull request is checked out at `pr` and a step runs from there",
    files: () =>
      addJob("agent-sweep.yml", `  evil:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: main
      - uses: actions/checkout@v4
        with:
          ref: ${PR_HEAD}
          path: pr
      - working-directory: pr
        run: node scripts/agent/set-state.mjs "$PR" blocked
`),
    expect: /agent-sweep\.yml › evil: runs set-state\.mjs by a relative path in a step that changes directory/,
  },
  {
    name: "`cd` into the package and a bare script name",
    files: () =>
      addJob("agent-sweep.yml", `  evil:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${PR_HEAD}
      - run: cd scripts/agent && node set-state.mjs "$PR" blocked
`),
    expect: /agent-sweep\.yml › evil: runs set-state\.mjs by a bare path after a directory change/,
  },
  {
    name: "`cd pr` before an existing trusted invocation",
    files: () => edit("agent-sweep.yml", (t) => replaceOnce(t, "node scripts/agent/loop-sweep.mjs ", "cd pr && node scripts/agent/loop-sweep.mjs ")),
    expect: /agent-sweep\.yml › sweep: runs loop-sweep\.mjs by a relative path in a step that changes directory/,
  },
  {
    name: "a job-level `defaults.run.working-directory`",
    files: () =>
      edit("agent-sweep.yml", (t) => replaceOnce(t, "  sweep:\n", "  sweep:\n    defaults:\n      run:\n        working-directory: pr\n")),
    expect: /agent-sweep\.yml › sweep: runs loop-sweep\.mjs by a relative path in a step that changes directory/,
  },
  {
    name: "a harness script reached through `npm --prefix … run`",
    files: () =>
      edit("agent-sweep.yml", (t) =>
        replaceOnce(t, "      - name: Sweep the loop PRs\n", "      - run: npm --prefix pr/scripts/agent run sweep\n      - name: Sweep the loop PRs\n")),
    expect: /agent-sweep\.yml › sweep: runs a package script/,
  },
  // …environment set above the steps.
  {
    name: "a job-level `env:` names a configuration",
    files: () =>
      edit("agent-sweep.yml", (t) =>
        replaceOnce(t, "  sweep:\n", "  sweep:\n    env:\n      AGENT_HARNESS_CONFIG_DIR: ${{ github.workspace }}/pr/.github/agent-harness\n")),
    expect: /agent-sweep\.yml › sweep: sets AGENT_HARNESS_CONFIG_DIR for the whole job/,
  },
  {
    name: "a workflow-level `env:` names a configuration",
    files: () =>
      edit("agent-sweep.yml", (t) => replaceOnce(t, "\njobs:\n", "\nenv:\n  AGENT_HARNESS_CONFIG_DIR: /tmp/pr-config\n\njobs:\n")),
    expect: /agent-sweep\.yml: sets AGENT_HARNESS_CONFIG_DIR for the whole workflow/,
  },
  {
    name: "a job-level `NODE_OPTIONS` preloads a module into every script",
    files: () =>
      edit("agent-sweep.yml", (t) => replaceOnce(t, "  sweep:\n", "  sweep:\n    env:\n      NODE_OPTIONS: --import ./pr/hook.mjs\n")),
    expect: /agent-sweep\.yml › sweep: sets NODE_OPTIONS for the whole job/,
  },
  // …git, spelt with options before the verb.
  {
    name: "`git -C . checkout` replaces a trusted workspace",
    files: () =>
      edit("agent-sweep.yml", (t) =>
        replaceOnce(t, "      - name: Sweep the loop PRs\n", '      - run: git -C . checkout "$SHA"\n      - name: Sweep the loop PRs\n')),
    expect: /agent-sweep\.yml › sweep: runs loop-sweep\.mjs from the workspace, which is not trusted at that step: `/,
  },
  {
    name: "`git -c key=value checkout` replaces a trusted workspace",
    files: () =>
      edit("agent-sweep.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Sweep the loop PRs\n",
          '      - run: git -c advice.detachedHead=false checkout "$SHA"\n      - name: Sweep the loop PRs\n',
        )),
    expect: /agent-sweep\.yml › sweep: runs loop-sweep\.mjs from the workspace, which is not trusted at that step: `/,
  },
  {
    name: "`git -C .trusted checkout` replaces a trusted side directory",
    files: () =>
      edit("agent-review-on-demand.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Describe the capture (attribution for the artifact below)\n",
          '      - run: git -C .trusted checkout "$HEAD_SHA"\n      - name: Describe the capture (attribution for the artifact below)\n',
        )),
    expect: /agent-review-on-demand\.yml › review: runs capture-meta\.mjs from \.trusted, which is not trusted at that step: `/,
  },
  {
    name: "a pull-request checkout into `path: .` over a trusted workspace",
    files: () =>
      edit("agent-sweep.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Sweep the loop PRs\n",
          `      - uses: actions/checkout@v4\n        with:\n          ref: ${PR_HEAD}\n          path: .\n      - name: Sweep the loop PRs\n`,
        )),
    expect: /agent-sweep\.yml › sweep: runs loop-sweep\.mjs from the workspace, which is not trusted at that step: its ref is/,
  },
  // …the toolchain action: a local action runs with the job's tokens.
  {
    name: "the toolchain action is used from the pull request's workspace",
    files: () =>
      edit("agent-review-reply.yml", (t) =>
        replaceOnce(t, `        uses: ./.trusted-toolchain/${TOOLCHAIN}\n`, `        uses: ./${TOOLCHAIN}\n`)),
    expect: /agent-review-reply\.yml › reply: uses the toolchain action from the workspace, which is not trusted at that step/,
  },
  {
    name: "the toolchain action's checkout takes the pull request's ref",
    files: () =>
      edit("agent-fix.yml", (t) =>
        replaceOnce(t, "          ref: main\n          path: .trusted-toolchain\n", `          ref: ${PR_HEAD}\n          path: .trusted-toolchain\n`)),
    expect: /agent-fix\.yml › fix: uses the toolchain action from \.trusted-toolchain, which is not trusted at that step: its ref is/,
  },
  {
    name: "the toolchain action's checkout names another repository",
    files: () =>
      edit("agent-iterate-ci.yml", (t) =>
        replaceOnce(
          t,
          "          ref: main\n          path: .trusted-toolchain\n",
          "          repository: ${{ github.event.workflow_run.head_repository.full_name }}\n          ref: main\n          path: .trusted-toolchain\n",
        )),
    expect: /agent-iterate-ci\.yml › iterate: uses the toolchain action from \.trusted-toolchain, which is not trusted at that step: it names a repository/,
  },
  {
    name: "the toolchain action's checkout no longer fetches the action",
    files: () =>
      edit("agent-review-panel.yml", (t) =>
        replaceOnce(t, `          sparse-checkout: ${TOOLCHAIN}\n`, "          sparse-checkout: .github/workflows\n")),
    expect: /agent-review-panel\.yml › fix: uses the toolchain action from \.trusted-toolchain, whose checkout does not include/,
  },
  {
    name: "a `git checkout` in the side directory before the toolchain action is used",
    files: () =>
      edit("agent-fix.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Set up the toolchain (main's definition, not the branch's)\n",
          '      - run: git -C .trusted-toolchain checkout "$SHA"\n      - name: Set up the toolchain (main\'s definition, not the branch\'s)\n',
        )),
    expect: /agent-fix\.yml › fix: uses the toolchain action from \.trusted-toolchain, which is not trusted at that step: `/,
  },
  {
    name: "the branch is checked out over a workspace the toolchain action is then used from",
    files: () =>
      edit("agent-implement.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Set up the toolchain (the workspace is still main)\n",
          `      - uses: actions/checkout@v4\n        with:\n          ref: ${PR_HEAD}\n      - name: Set up the toolchain (the workspace is still main)\n`,
        )),
    expect: /agent-implement\.yml › implement: uses the toolchain action from the workspace, which is not trusted at that step: its ref is/,
  },
  {
    name: "the dispatcher's ref, not `main`, under the workspace the toolchain action is used from",
    files: () =>
      edit("agent-implement.yml", (t) =>
        replaceOnce(t, "          ref: main # not the dispatcher's ref: the prompt says \"the checkout is on `main`\"\n", "")),
    expect: /agent-implement\.yml › implement: uses the toolchain action from the workspace, which is not trusted at that step: it has no ref, and the workflow runs on workflow_dispatch/,
  },
  {
    name: "the toolchain action is used after the agent",
    files: () =>
      edit("agent-fix.yml", (t) => {
        const anchor = "      # EVERYTHING AFTER THE AGENT RUNS IN THE `report` JOB BELOW";
        return replaceOnce(t, anchor, `      - name: late\n        uses: ./.trusted-toolchain/${TOOLCHAIN}\n\n${anchor}`);
      }),
    expect: /agent-fix\.yml › fix: uses the toolchain action from \.trusted-toolchain(?:, which is not trusted at that step| after the agent ran)/,
  },
  {
    name: "a second local action, from the pull request's tree",
    files: () =>
      edit("agent-review-reply.yml", (t) =>
        replaceOnce(
          t,
          "      - name: Keep the toolchain action out of the fixer's commits\n",
          "      - uses: ./.github/actions/setup\n      - name: Keep the toolchain action out of the fixer's commits\n",
        )),
    expect: /agent-review-reply\.yml › reply: uses the local action \.\/\.github\/actions\/setup; the only local action/,
  },
  {
    name: "a local action reached by climbing out of the trusted directory",
    files: () =>
      edit("agent-review-reply.yml", (t) =>
        replaceOnce(t, `        uses: ./.trusted-toolchain/${TOOLCHAIN}\n`, `        uses: ./.trusted-toolchain/../${TOOLCHAIN}\n`)),
    expect: /agent-review-reply\.yml › reply: uses the local action/,
  },
  {
    name: "a side-directory checkout over a pull request's tree without clearing the path first",
    files: () =>
      edit("agent-fix.yml", (t) =>
        replaceOnce(t, "      - name: Clear the path the toolchain action is checked out to\n", "      - name: Clear some other path\n").replace(
          "        run: rm -rf .trusted-toolchain\n",
          "        run: rm -rf .trusted-other\n",
        )),
    expect: /agent-fix\.yml › fix: checks out into \.trusted-toolchain inside an untrusted workspace without an `rm -rf \.trusted-toolchain` first/,
  },
  {
    name: "the path is cleared before the pull request's checkout, not after it",
    files: () =>
      edit("agent-iterate-ci.yml", (t) => {
        const clear = "      - name: Clear the path the trusted copy is checked out to\n        run: rm -rf .trusted-agent\n";
        const anchor = "      - name: Generate the agent's narrow token\n";
        return replaceOnce(replaceOnce(t, clear, ""), anchor, `${clear}${anchor}`);
      }),
    expect: /agent-iterate-ci\.yml › iterate: checks out into \.trusted-agent inside an untrusted workspace without an `rm -rf \.trusted-agent` first/,
  },
  // …the toolchain: not before the checked-out commit is known to be the reviewed one.
  {
    name: "the toolchain is set up before the checked-out commit is re-verified",
    files: () =>
      edit("agent-fix.yml", (t) => {
        const from = t.indexOf("      - name: Clear the path the toolchain action is checked out to\n");
        const end = "        run: echo '/.trusted-toolchain/' >> .git/info/exclude\n";
        const to = t.indexOf(end, from) + end.length;
        const anchor = "      - name: Re-verify the checked-out commit is the one the panel reviewed\n";
        assert.ok(from > 0 && to > from && t.indexOf(anchor) > 0 && t.indexOf(anchor) < from, "mutation anchors not found in the expected order");
        const steps = t.slice(from, to);
        return replaceOnce(t.slice(0, from) + t.slice(to), anchor, `${steps}${anchor}`);
      }),
    expect: /agent-fix\.yml › fix: sets up the toolchain before re-verifying the checked-out commit/,
  },
  // …the fix-scope hook: installed where git will actually look.
  {
    name: "the scope hook is written to `.git/hooks`, which `core.hooksPath` bypasses",
    files: () =>
      edit("agent-fix.yml", (t) => replaceOnce(t, ' > "$HOOKS/pre-push"\n', " > .git/hooks/pre-push\n")),
    expect: /agent-fix\.yml › fix: installs a pre-push hook that is not written to "\$HOOKS\/pre-push"/,
  },
  {
    name: "the scope hook's directory is inside the workspace",
    files: () =>
      edit("agent-review-panel.yml", (t) => replaceOnce(t, '          HOOKS="$RUNNER_TEMP/agent-hooks"\n', '          HOOKS="$GITHUB_WORKSPACE/.agent-hooks"\n')),
    expect: /agent-review-panel\.yml › fix: installs a pre-push hook somewhere other than \$RUNNER_TEMP\/agent-hooks/,
  },
  {
    name: "the scope hook is written but `core.hooksPath` is not pointed at it",
    files: () =>
      edit("agent-iterate-ci.yml", (t) => replaceOnce(t, '          git config core.hooksPath "$HOOKS"\n', "")),
    expect: /agent-iterate-ci\.yml › iterate: installs a pre-push hook without pointing core\.hooksPath at it/,
  },
  {
    name: "the scope hook is installed without asking git where it reads hooks from",
    files: () =>
      edit("agent-fix.yml", (t) => replaceOnce(t, '          EFFECTIVE="$(git rev-parse --path-format=absolute --git-path hooks)"\n', '          EFFECTIVE="$HOOKS"\n')),
    expect: /agent-fix\.yml › fix: installs a pre-push hook without checking where git will read hooks from/,
  },
  // …the upstream notifier: a job with `issues: write` that no pull request may be in.
  {
    name: "the notifier's workflow can also be started by a pull request",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "  workflow_dispatch:\n", "  workflow_dispatch:\n  pull_request:\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs, which writes issues, in a workflow that pull_request can start/,
  },
  {
    name: "the notifier's workflow can also be started by a push",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "  workflow_dispatch:\n", "  workflow_dispatch:\n  push:\n    branches: [main]\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs, which writes issues, in a workflow that push can start/,
  },
  {
    name: "the notifier's job gains `contents: write`",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "      contents: read #", "      contents: write #")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs holding contents: write, issues: write; it may hold exactly contents: read, issues: write/,
  },
  {
    name: "the notifier's job gains a third permission",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "      issues: write #", "      pull-requests: write\n      issues: write #")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs holding .*pull-requests: write/,
  },
  {
    name: "the notifier's job drops its own `permissions:` and inherits",
    files: () =>
      edit(NOTIFY_FILE, (t) => {
        const from = t.indexOf("    permissions:\n      contents: read #");
        const to = t.indexOf("    steps:\n", from);
        assert.ok(from > 0 && to > from, "mutation anchors not found in the expected order");
        return t.slice(0, from) + t.slice(to);
      }),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs, but it has no `permissions:` of its own/,
  },
  {
    name: "the notifier's job is given `write-all`",
    files: () =>
      edit(NOTIFY_FILE, (t) => {
        const from = t.indexOf("    permissions:\n      contents: read #");
        const to = t.indexOf("    steps:\n", from);
        assert.ok(from > 0 && to > from, "mutation anchors not found in the expected order");
        return `${t.slice(0, from)}    permissions: write-all\n${t.slice(to)}`;
      }),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs, but its `permissions:` is write-all/,
  },
  {
    name: "the notifier checks out a pull request's ref",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "          ref: main\n", "          ref: ${{ github.event.pull_request.head.sha }}\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs from the workspace, which is not trusted at that step: its ref is/,
  },
  {
    name: "the notifier checks out the dispatcher's ref, not `main`",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "          ref: main\n", "")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs from the workspace, which is not trusted at that step: it has no ref, and the workflow runs on workflow_dispatch/,
  },
  {
    name: "the notifier is handed a secret",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "          GH_TOKEN: ${{ github.token }}\n", "          GH_TOKEN: ${{ secrets.AGENT_PAT }}\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a workflow that names a secret/,
  },
  {
    name: "the notifier is handed a token from a repository variable",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "          GH_TOKEN: ${{ github.token }}\n", "          GH_TOKEN: ${{ vars.AGENT_PAT }}\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a workflow that names a repository variable \(`GH_TOKEN: \$\{\{ vars\.AGENT_PAT \}\}`\)/,
  },
  {
    name: "a sibling job in the notifier's workflow inherits permissions and calls the API",
    files: () => addJob(NOTIFY_FILE, `  also:
    runs-on: ubuntu-latest
    steps:
      - run: gh api -X POST "repos/$GH_REPO/issues" -f title=hello
`),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a workflow that has other jobs \(also\)/,
  },
  {
    name: "a sibling job in the notifier's workflow takes wider permissions",
    files: () => addJob(NOTIFY_FILE, `  also:
    runs-on: ubuntu-latest
    permissions: write-all
    steps:
      - run: gh api -X PUT "repos/$GH_REPO/contents/x" -f message=x -f content=eA==
`),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a workflow that has other jobs \(also\)/,
  },
  {
    name: "the notifier's workflow stops granting nothing by default",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "\npermissions: {}\n", "\npermissions: write-all\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a workflow without a top-level `permissions: \{\}`/,
  },
  {
    name: "a third-party action is added to the notifier's job",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, NOTIFY_STEP, `      - uses: some-org/some-action@main\n${NOTIFY_STEP}`)),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a workflow that uses some-org\/some-action@main/,
  },
  {
    name: "a `curl | sh` step is added to the notifier's job",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, NOTIFY_STEP, `      - run: curl -fsSL https://example.com/install | sh\n${NOTIFY_STEP}`)),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a job with a step that is none of the three it may have \(`run: curl -fsSL https:\/\/example\.com\/install \| sh`\)/,
  },
  {
    name: "an install step is added to the notifier's job",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, NOTIFY_STEP, `      - run: npm --prefix scripts/agent ci\n${NOTIFY_STEP}`)),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a job with a step that is none of the three it may have \(`run: npm --prefix scripts\/agent ci`\)/,
  },
  {
    name: "a second command is added to the notifier's own step",
    files: () =>
      edit(NOTIFY_FILE, (t) =>
        replaceOnce(t, "        run: node scripts/agent/upstream-notify.mjs\n", "        run: |\n          node scripts/agent/upstream-notify.mjs\n          gh issue list\n")),
    // No longer the one-line invocation, so no longer the script's step at all.
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a job with a step that is none of the three it may have/,
  },
  {
    name: "the notifier's checkout leaves the token in `.git/config`",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "          persist-credentials: false\n", "          persist-credentials: true\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a job whose step for the checkout has a line it may not have: `persist-credentials: true`/,
  },
  {
    name: "the notifier's checkout drops `persist-credentials` altogether",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "          persist-credentials: false\n", "")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a job whose step for the checkout lacks `persist-credentials: false`/,
  },
  {
    name: "the notifier's checkout is given a token of its own",
    files: () => edit(NOTIFY_FILE, (t) => replaceOnce(t, "          persist-credentials: false\n", "          persist-credentials: false\n          token: ${{ github.token }}\n")),
    expect: /agent-upstream-notify\.yml › notify: runs upstream-notify\.mjs in a job whose step for the checkout has a line it may not have: `token:/,
  },
  {
    name: "the notifier is moved into the test lane, which pull requests start",
    files: () =>
      addJob("agent-scripts.yml", `  notify:
    if: github.event_name == 'schedule'
    runs-on: ubuntu-latest
    permissions:
      contents: read
      issues: write
    steps:
      - uses: actions/checkout@v4
        with:
          ref: main
      - run: node scripts/agent/upstream-notify.mjs
`),
    expect: /agent-scripts\.yml › notify: runs upstream-notify\.mjs, which writes issues, in a workflow that push, pull_request can start/,
  },
  // …jobs the reader used to walk past.
  {
    name: "a job whose step list is indented differently from its neighbours",
    files: () =>
      addJob("agent-sweep.yml", `  evil:
    runs-on: ubuntu-latest
    steps:
        - uses: actions/checkout@v4
          with:
            ref: ${PR_HEAD}
        - run: node ./scripts/agent/set-state.mjs "$PR" blocked
`),
    expect: /agent-sweep\.yml › evil: runs set-state\.mjs from the workspace, which is not trusted at that step/,
  },
  {
    name: "a job whose steps this reader cannot parse at all",
    files: () =>
      addJob("agent-sweep.yml", `  evil:
    runs-on: ubuntu-latest
    "steps":
      - uses: actions/checkout@v4
        with:
          ref: ${PR_HEAD}
      - run: node ./scripts/agent/set-state.mjs "$PR" blocked
`),
    expect: /agent-sweep\.yml: 2 of \d+ `uses:`\/`run:` keys are outside every step this guard could parse/,
  },
];

test("each trust-weakening mutation of the real workflows fails the guard", () => {
  // Every case runs, whatever happens to the others: a mutation whose anchor no
  // longer exists (a renamed step, say) is reported by name, with its file, and
  // the rest are still checked.
  const failures = [];
  for (const { name, files, expect } of MUTATIONS) {
    let problems;
    try {
      ({ problems } = inspect(files()));
    } catch (err) {
      failures.push(`COULD NOT APPLY "${name}" — ${err.message}. Update the mutation to match the workflow.`);
      continue;
    }
    if (problems.length === 0) failures.push(`PASSED THE GUARD: ${name}`);
    else if (!problems.some((p) => expect.test(p))) {
      failures.push(`${name}: the guard failed, but not for the reason expected.\n  wanted ${expect}\n  got:\n  - ${problems.join("\n  - ")}`);
    }
  }
  assert.deepEqual(failures, []);
  // Guard on the guard: the six that got through first, and their neighbours.
  assert.ok(MUTATIONS.length >= 68, `expected the mutation cases, ran ${MUTATIONS.length}`);
});

test("what is not a command is not flagged", () => {
  // A trailing comment, a step name and a full-line comment mention the
  // directory without touching it. The guard must stay quiet, or people learn
  // to route around it.
  const quiet = [
    ["a trailing comment", (t) => replaceOnce(t, "          ref: main\n", "          ref: main # with .github/agent-harness, see AGENT_HARNESS_CONFIG_DIR\n")],
    ["a step name", (t) => replaceOnce(t, "      - name: Sweep the loop PRs\n", "      - name: Sweep the loop PRs (reads .github/agent-harness)\n")],
    ["a full-line comment", (t) => replaceOnce(t, "      - name: Sweep the loop PRs\n", "      # AGENT_HARNESS_CONFIG_DIR is not set here; agent-harness is beside the scripts.\n      - name: Sweep the loop PRs\n")],
    ["`ref: refs/heads/main`", (t) => replaceOnce(t, "          ref: main\n", "          ref: refs/heads/main\n")],
  ];
  for (const [what, change] of quiet) {
    const { problems, counts } = inspect(edit("agent-sweep.yml", change));
    assert.deepEqual(problems, [], what);
    assert.equal(counts.invocations, EXPECTED.invocations, what);
  }
});
