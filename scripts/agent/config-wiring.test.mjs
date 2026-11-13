// Where the workflows get the harness configuration from.
//
// `config.mjs` resolves `.github/agent-harness` beside the checkout the running
// script came from, so the configuration has exactly the provenance of the
// script that reads it — PROVIDED every place a workflow fetches `scripts/agent`
// from the default branch fetches the configuration with it. Two failures are
// possible, and neither shows up as a failing test anywhere else:
//
//   - a sparse checkout that brings the scripts WITHOUT the configuration. The
//     script throws at import. That is loud, but only at run time, on the
//     default branch, after the change that dropped the path has merged.
//   - a staging copy (`cp -R scripts/agent "$RUNNER_TEMP/agent-tools"`) that
//     drops the parent directories. The copy must be given a trusted
//     configuration by name, and the name must never point into the workspace,
//     which by then holds the pull request's branch — the one tree whose
//     configuration must not decide the pull request's own gates.
//
// So this enumerates every such site in `.github/workflows/agent-*.yml` and
// checks each one. The workflows are read as text, like the other wiring
// guards in this package.
//
// IT ASSERTS HOW MANY SITES IT INSPECTED. A guard that depends on where things
// are — a step shape, an indentation — passes vacuously the day the shape
// changes and it finds nothing. Adding or removing a site changes a count
// below; that is the prompt to look at the new site, not a nuisance.

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

// The counts. Each is the number of sites of one kind in the workflows today.
const EXPECTED = {
  // Sparse checkouts of the scripts directory from the default branch.
  trustedCheckouts: 12,
  // …of which this many land in a side directory (`path:`), not the workspace.
  trustedCheckoutsWithPath: 4,
  // Route jobs: `command.mjs` alone. They read no configuration.
  routerCheckouts: 7,
  // `cp -R … scripts/agent …/agent-tools`.
  stagingCopies: 4,
};

const isComment = (line) => /^\s*#/.test(line);
const code = (text) => text.split("\n").filter((l) => !isComment(l)).join("\n");

/** Every job of every agent workflow, split into its steps. */
function readJobs() {
  const jobs = [];
  const files = readdirSync(WF_DIR).filter((f) => /^agent-.*\.ya?ml$/.test(f)).sort();
  for (const file of files) {
    const text = readFileSync(path.join(WF_DIR, file), "utf8");
    const lines = text.split("\n");
    const jobsAt = lines.findIndex((l) => /^jobs:\s*$/.test(l));
    assert.ok(jobsAt >= 0, `${file}: no top-level \`jobs:\``);
    const triggers = code(lines.slice(0, jobsAt).join("\n"));
    let job = null;
    let step = null;
    for (const line of lines.slice(jobsAt + 1)) {
      const header = /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(line);
      if (header) {
        job = { file, name: header[1], triggers, steps: [] };
        jobs.push(job);
        step = null;
        continue;
      }
      if (!job) continue;
      if (/^ {6}- /.test(line)) {
        step = { lines: [] };
        job.steps.push(step);
      }
      if (step) step.lines.push(line);
    }
  }
  return jobs;
}

/** A step's `with:` scalar, or undefined. Comment lines are not values. */
function scalar(step, key) {
  for (const line of step.lines) {
    if (isComment(line)) continue;
    const m = new RegExp(`^\\s+${key}:\\s*(.*?)\\s*(?:#.*)?$`).exec(line);
    if (m) return m[1];
  }
  return undefined;
}

/** A checkout step's `sparse-checkout` patterns: inline or block form. `null` when the step has none. */
function sparsePatterns(step) {
  const at = step.lines.findIndex((l) => !isComment(l) && /^\s+sparse-checkout:/.test(l));
  if (at < 0) return null;
  const head = /^(\s+)sparse-checkout:\s*(.*?)\s*$/.exec(step.lines[at]);
  if (!/^[|>][+-]?$/.test(head[2])) return [head[2]];
  const out = [];
  for (const line of step.lines.slice(at + 1)) {
    if (isComment(line)) continue;
    if (line.trim() === "") continue;
    const indent = /^(\s*)/.exec(line)[1].length;
    if (indent <= head[1].length) break;
    out.push(line.trim());
  }
  return out;
}

const isCheckout = (step) => step.lines.some((l) => !isComment(l) && /\buses:\s*actions\/checkout@/.test(l));

/**
 * Is this checkout step's ref the default branch? `main` by name, or no `ref`
 * in a workflow no pull-request or push event can start — where the default
 * ref IS the default branch.
 */
function refIsTrusted(step, job) {
  const ref = scalar(step, "ref");
  if (ref === "main") return true;
  if (ref !== undefined) return false;
  return !/^\s+(pull_request|pull_request_target|pull_request_review|pull_request_review_comment|push):/m.test(job.triggers);
}

const JOBS = readJobs();
const where = (job) => `${job.file} › ${job.name}`;

test("the workflows were read: every agent workflow, with jobs and steps", () => {
  const files = new Set(JOBS.map((j) => j.file));
  assert.ok(files.size >= 9, `expected the agent workflows, read ${[...files].join(", ")}`);
  assert.ok(JOBS.length >= 25, `expected their jobs, read ${JOBS.length}`);
  for (const job of JOBS) assert.ok(job.steps.length > 0, `${where(job)}: no steps were read`);
});

test("every trusted checkout of scripts/agent brings .github/agent-harness from the same ref", () => {
  let trusted = 0;
  let withPath = 0;
  let router = 0;
  for (const job of JOBS) {
    for (const step of job.steps) {
      if (!isCheckout(step)) continue;
      const patterns = sparsePatterns(step);
      if (!patterns) continue;
      const touchesAgent = patterns.some((p) => p === SCRIPTS || p.startsWith(`${SCRIPTS}/`));
      if (!touchesAgent) {
        assert.ok(!patterns.includes(CONFIG), `${where(job)}: fetches the configuration without the scripts that read it`);
        continue;
      }

      // The router, alone. It must stay alone: it reads no configuration, and
      // `config.test.mjs` pins that `command.mjs` cannot reach `config.mjs`.
      if (patterns.length === 1 && patterns[0] === ROUTER) {
        router++;
        continue;
      }

      assert.ok(
        patterns.includes(SCRIPTS),
        `${where(job)}: a sparse checkout of part of ${SCRIPTS} (${patterns.join(", ")}) — only the whole ` +
          `directory or ${ROUTER} alone is a shape this guard knows how to check`,
      );
      trusted++;
      if (scalar(step, "path") !== undefined) withPath++;

      assert.ok(
        patterns.includes(CONFIG),
        `${where(job)}: checks out ${SCRIPTS} without ${CONFIG}. Every script that reads the configuration ` +
          "throws at import there.",
      );
      // Same step, so the same ref and the same commit: the configuration a
      // script reads is the one that shipped with it.
      assert.ok(
        refIsTrusted(step, job),
        `${where(job)}: ${SCRIPTS} and ${CONFIG} are checked out from ref ${JSON.stringify(scalar(step, "ref"))}, ` +
          "which is not provably the default branch — a pull request could supply the gates it is judged by",
      );
      assert.equal(
        scalar(step, "sparse-checkout-cone-mode"),
        "false",
        `${where(job)}: cone mode would widen the checkout to every top-level file`,
      );
    }
  }
  assert.equal(trusted, EXPECTED.trustedCheckouts, "a trusted checkout of scripts/agent was added or removed");
  assert.equal(withPath, EXPECTED.trustedCheckoutsWithPath, "a side-directory checkout was added or removed");
  assert.equal(router, EXPECTED.routerCheckouts, "a router-only checkout was added or removed");
});

/** Every `cp -R <prefix>scripts/agent "<dest>/agent-tools"` in the workflows. */
function stagingSites() {
  const sites = [];
  for (const job of JOBS) {
    job.steps.forEach((step, index) => {
      const lines = step.lines.filter((l) => !isComment(l));
      const at = lines.findIndex((l) => /\bcp -R \S*scripts\/agent "/.test(l));
      if (at < 0) return;
      const m = /\bcp -R (\S*)scripts\/agent "([^"]+)\/agent-tools"\s*$/.exec(lines[at]);
      sites.push({ job, step, index, lines, at, prefix: m?.[1], dest: m?.[2], line: lines[at].trim() });
    });
  }
  return sites;
}

test("every staged copy of scripts/agent is given a trusted configuration by name", () => {
  const sites = stagingSites();
  for (const site of sites) {
    const { job, lines, at, prefix, dest } = site;
    assert.ok(dest, `${where(job)}: could not read the staging copy: ${site.line}`);

    // 1. The configuration is copied from the SAME tree as the scripts…
    const copy = `cp -R ${prefix}${CONFIG} "${dest}/agent-harness"`;
    const copyAt = lines.findIndex((l) => l.trim() === copy);
    assert.ok(copyAt > at, `${where(job)}: the staging step must also run \`${copy}\``);

    // 2. …and named for every later step of the job, as an absolute path
    //    outside the workspace.
    const exported = `echo "${ENV}=${dest}/agent-harness" >> "$GITHUB_ENV"`;
    assert.ok(
      lines.findIndex((l) => l.trim() === exported) > copyAt,
      `${where(job)}: the staging step must then run \`${exported}\``,
    );
    assert.match(dest, /^(\$RUNNER_TEMP|\$\{\{ runner\.temp \}\})$/, `${where(job)}: stages into ${dest}, not the runner's temp directory`);

    // 3. The tree it copies from is the default branch's. A side directory is
    //    covered by the checkout guard above (it is a sparse checkout with a
    //    `path:`); the workspace is trusted only until the first checkout of
    //    anything else, so the newest checkout before this step decides.
    const root = prefix.replace(/^\.\//, "").replace(/\/$/, "");
    const before = job.steps.slice(0, site.index).filter(isCheckout);
    const source = before.filter((s) => (scalar(s, "path") ?? "") === root).at(-1);
    assert.ok(source, `${where(job)}: no checkout into ${JSON.stringify(root || "the workspace")} precedes the staging step`);
    assert.ok(
      refIsTrusted(source, job),
      `${where(job)}: the staging step copies from a checkout of ref ${JSON.stringify(scalar(source, "ref"))}`,
    );
  }
  assert.equal(sites.length, EXPECTED.stagingCopies, "a staging copy of scripts/agent was added or removed");
});

test("a job runs staged scripts only if it staged them, and nothing else names the configuration", () => {
  // Every job that invokes `…/agent-tools/<script>` is one of the staging jobs,
  // so the name is set before any staged script runs.
  const staging = new Set(stagingSites().map((s) => where(s.job)));
  let users = 0;
  for (const job of JOBS) {
    const text = code(job.steps.flatMap((s) => s.lines).join("\n"));
    if (!/agent-tools\/[\w-]+\.mjs/.test(text)) continue;
    users++;
    assert.ok(staging.has(where(job)), `${where(job)}: runs a script from agent-tools without staging it (and its configuration)`);
  }
  assert.equal(users, EXPECTED.stagingCopies, "a job started or stopped running staged scripts");

  // The variable is written at the staging sites and nowhere else: no step
  // `env:`, no other export. One writer per job means one answer to "which
  // configuration did this job read".
  let mentions = 0;
  for (const file of new Set(JOBS.map((j) => j.file))) {
    for (const line of code(readFileSync(path.join(WF_DIR, file), "utf8")).split("\n")) {
      if (!line.includes(ENV)) continue;
      mentions++;
      assert.match(
        line.trim(),
        new RegExp(`^echo "${ENV}=(\\$RUNNER_TEMP|\\$\\{\\{ runner\\.temp \\}\\})/agent-harness" >> "\\$GITHUB_ENV"$`),
        `${file}: sets ${ENV} somewhere other than a staging step: ${line.trim()}`,
      );
    }
  }
  assert.equal(mentions, EXPECTED.stagingCopies, `${ENV} is written at a new site, or a staging site lost it`);
});

test("the repository has the configuration the default resolution expects", () => {
  // What a full checkout of the default branch — the promote and stalled jobs,
  // the workspace before a branch checkout — relies on being there.
  for (const file of ["config.json", "coverage-note.md"]) {
    assert.ok(existsSync(path.join(ROOT, CONFIG, file)), `${CONFIG}/${file} is missing from this repository`);
  }
});
