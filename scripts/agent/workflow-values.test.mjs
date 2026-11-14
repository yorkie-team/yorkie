// `workflow-values.mjs`, and how the workflows read it.
//
// Two halves. The first is the module: what each name renders, and that it
// refuses rather than writes nothing. The second is the wiring, read off the
// `agent-*.yml` files as text: an expression that names an output nobody wrote
// evaluates to an empty string, GitHub says nothing, and the prompt goes out
// with a hole in it — so every `outputs.<name>` a workflow reads must be one a
// step asked this module for, in a place that step's value can reach.

import "./fixtures/use-fixture-config.mjs";

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { CONFIG_DIR_ENV, loadConfig } from "./config.mjs";
import { OPTIONAL, VALUES, githubOutput, values } from "./workflow-values.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const CLI = path.join(HERE, "workflow-values.mjs");
const WF_DIR = path.join(HERE, "..", "..", ".github", "workflows");
const FIXTURE = loadConfig();

/** The fixture configuration with `change` applied, as a loaded config. */
function configWith(t, change) {
  const dir = mkdtempSync(path.join(os.tmpdir(), "wfv-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const raw = JSON.parse(readFileSync(path.join(process.env[CONFIG_DIR_ENV], "config.json"), "utf8"));
  change(raw);
  writeFileSync(path.join(dir, "config.json"), JSON.stringify(raw));
  writeFileSync(path.join(dir, "coverage-note.md"), "a note\n");
  return { dir, config: loadConfig(dir) };
}

// --- the module ----------------------------------------------------------------------

test("every name renders a string from the configuration", () => {
  for (const [name, value] of values(Object.keys(VALUES), FIXTURE)) {
    assert.equal(typeof value, "string", name);
    if (!OPTIONAL.has(name)) assert.notEqual(value, "", name);
  }
  const get = (name) => values([name], FIXTURE)[0][1];
  assert.equal(get("verify_command"), "fixture verify");
  assert.equal(get("app_slug"), "fixture-agent");
  assert.equal(get("app_bot_login"), "fixture-agent[bot]");
  assert.equal(get("allowed_bots"), "fixture-agent[bot],fixture-agent");
  assert.deepEqual(JSON.parse(get("bot_logins")), ["github-actions[bot]", "fixture-agent[bot]"]);
  assert.equal(get("ci_lane_reports"), "false");
});

test("the document reference is Markdown: a path in a code span, a URL left a link", (t) => {
  assert.equal(values(["docs_ref"], FIXTURE)[0][1], "`fixture-docs/command-verbs.md`");
  const { config } = configWith(t, (c) => {
    c.docs.commandVerbsRef = "https://example.test/org/repo/blob/main/docs/verbs.md";
  });
  // Bare, so the comment renders a link; in a code span it would be dead text.
  assert.equal(values(["docs_ref"], config)[0][1], "https://example.test/org/repo/blob/main/docs/verbs.md");
});

test("names are output names: lower_snake_case, readable back as `outputs.<name>`", () => {
  for (const name of Object.keys(VALUES)) assert.match(name, /^[a-z][a-z0-9_]*$/);
  for (const name of OPTIONAL) assert.ok(Object.hasOwn(VALUES, name), `${name} is optional but not a value`);
});

test("an optional value carries its own joining punctuation, and vanishes whole", (t) => {
  // A repository with nothing to say in any of them: every one is empty.
  const { config: bare } = configWith(t, (c) => {
    c.fixer.integrationWarning = "";
    c.fixer.integrationRerunBy = "";
    c.fixer.licenceScope = "";
  });
  for (const name of OPTIONAL) assert.equal(values([name], bare)[0][1], "", name);
  const { config } = configWith(t, (c) => {
    c.fixer.verifyFailureHint = "If the lint fails, run the fixer.";
    c.fixer.ciFailureNote = "If the failure is in one of them, let CI confirm.";
    c.fixer.generatedCodeNote = "Never edit gen/ by hand.";
    c.fixer.apiChangeNote = "If the issue needs it changed, say so and stop.";
    c.fixer.todoTrackingLine = "Its second line is `Tracked as #__ISSUE_NUMBER__`.";
    c.fixer.replyVerifyInstruction = "run `fixture verify`";
    c.fixer.commitNote = "Commit subjects are 70 characters or fewer.";
  });
  const get = (name) => values([name], config)[0][1];
  assert.equal(get("commit_note"), " Commit subjects are 70 characters or fewer.");
  assert.equal(get("verify_failure_hint"), " If the lint fails, run the fixer.");
  assert.equal(get("integration_sentence_before_push"), " Do NOT run `fixture test-all`: it needs a stack this job does not stand up, and the push below re-runs it anyway.");
  assert.equal(get("integration_sentence"), " Do NOT run `fixture test-all`: it needs a stack this job does not stand up, and the push re-runs it anyway.");
  assert.equal(get("ci_failure_note"), " If the failure is in one of them, let CI confirm.");
  assert.equal(get("generated_code_bullet"), "\n- Never edit gen/ by hand.");
  assert.equal(get("generated_code_paragraph"), "\n\nNever edit gen/ by hand. If the issue needs it changed, say so and stop.");
  assert.equal(get("licence_scope"), " A licence header on every new source file.");
  assert.equal(get("todo_tracking"), " Its second line is `Tracked as #__ISSUE_NUMBER__`.");
  assert.equal(get("reply_verify_clause"), " run `fixture verify`,");
  // Either half of the last paragraph alone is still a paragraph.
  const { config: noteOnly } = configWith(t, (c) => {
    c.fixer.apiChangeNote = "If the issue needs an API change, say so and stop.";
  });
  assert.equal(values(["generated_code_paragraph"], noteOnly)[0][1], "\n\nIf the issue needs an API change, say so and stop.");
});

test("the diff pathspecs are never empty, even with nothing to exclude", (t) => {
  assert.equal(values(["diff_pathspecs"], FIXTURE)[0][1], ".\n:(exclude)gen/**");
  const { config } = configWith(t, (c) => {
    c.review.diffExcludes = [];
  });
  assert.equal(values(["diff_pathspecs"], config)[0][1], ".");
  assert.ok(!OPTIONAL.has("diff_pathspecs"), "an empty value must mean it did not arrive");
});

test("asking for nothing, an unknown name or the same name twice throws", () => {
  assert.throws(() => values([], FIXTURE), /name at least one value/);
  assert.throws(() => values(["verify_comand"], FIXTURE), /no value named "verify_comand"/);
  assert.throws(() => values(["repo_name", "repo_name"], FIXTURE), /asked for twice/);
  // A dotted configuration key is not a name: the two namespaces stay apart.
  assert.throws(() => values(["fixer.verifyCommand"], FIXTURE), /no value named/);
});

test("a required value that renders empty throws instead of being written", () => {
  // The schema refuses a blank required key, so this takes a config object that
  // never went through the loader — the check here is the second lock.
  const hollow = { ...FIXTURE, fixer: { ...FIXTURE.fixer, verifyCommand: "" } };
  assert.throws(() => values(["verify_command"], hollow), /verify_command is empty/);
});

test("githubOutput: one delimited block per value, and no value can close its own", () => {
  const out = githubOutput([["a", "one"], ["b", "two\nlines"], ["c", ""]], "D");
  assert.equal(out, "a<<D\none\nD\nb<<D\ntwo\nlines\nD\nc<<D\n\nD\n");
  assert.throws(() => githubOutput([["a", "x\nD\ny"]], "D"), /contains the output delimiter/);
  // The default delimiter differs per call.
  const d = (s) => /^a<<(\S+)\n/.exec(s)[1];
  assert.notEqual(d(githubOutput([["a", "x"]])), d(githubOutput([["a", "x"]])));
});

test("CLI: prints the blocks and exits 0; exits 1 and prints nothing on any failure", () => {
  const run = (args, env = process.env) => spawnSync(process.execPath, [CLI, ...args], { encoding: "utf8", env });
  const ok = run(["verify_command", "todo_tracking"]);
  assert.equal(ok.status, 0, ok.stderr);
  assert.match(ok.stdout, /^verify_command<<(harness_[0-9a-f-]+)\nfixture verify\n\1\ntodo_tracking<<\1\n\n\1\n$/);
  for (const args of [[], ["nope"], ["verify_command", "nope"]]) {
    const bad = run(args);
    assert.equal(bad.status, 1, `args ${JSON.stringify(args)}`);
    assert.equal(bad.stdout, "", "a failed read must write no outputs at all");
    assert.notEqual(bad.stderr, "");
  }
  // An unreadable configuration is the same visible failure.
  const lost = run(["verify_command"], { ...process.env, [CONFIG_DIR_ENV]: path.join(os.tmpdir(), "no-such-harness-config") });
  assert.equal(lost.status, 1);
  assert.equal(lost.stdout, "");
});

test("CLI: run through a symlinked path, it still prints", (t) => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "wfv-link-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  execFileSync("ln", ["-s", HERE, path.join(dir, "agent")]);
  const out = spawnSync(process.execPath, [path.join(dir, "agent", "workflow-values.mjs"), "repo_name"], { encoding: "utf8" });
  assert.equal(out.status, 0, out.stderr);
  assert.match(out.stdout, /\nfixture-repo\n/);
});

// --- the wiring ----------------------------------------------------------------------

const isComment = (l) => /^\s*#/.test(l);
const NAME = /[a-z][a-z0-9_]*/;

/** `[{ file, name, head, steps: [{ text, code }] }]` for every job of every agent workflow. */
function readJobs(dir = WF_DIR) {
  const jobs = [];
  for (const file of readdirSync(dir).filter((f) => /^agent-.*\.ya?ml$/.test(f)).sort()) {
    const lines = readFileSync(path.join(dir, file), "utf8").split("\n");
    const start = lines.findIndex((l) => /^jobs:\s*$/.test(l));
    let job = null;
    let step = null;
    for (const line of lines.slice(start + 1)) {
      const header = /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(line);
      if (header) {
        job = { file, name: header[1], head: [], steps: [] };
        jobs.push(job);
        step = null;
        continue;
      }
      if (!job) continue;
      if (/^ {6}- /.test(line)) {
        step = { lines: [] };
        job.steps.push(step);
      }
      (step ? step.lines : job.head).push(line);
    }
  }
  for (const job of jobs) {
    job.headCode = job.head.filter((l) => !isComment(l)).join("\n");
    for (const step of job.steps) step.code = step.lines.filter((l) => !isComment(l)).join("\n");
  }
  return jobs;
}

const scalar = (code, key) => new RegExp(`^\\s+(?:- )?${key}:\\s*(.+?)\\s*$`, "m").exec(code)?.[1];

/** The names a step asks `workflow-values.mjs` for, or null when it does not run it. */
function asked(step) {
  const at = step.code.indexOf("workflow-values.mjs");
  if (at < 0) return null;
  const end = step.code.indexOf('>> "$GITHUB_OUTPUT"', at);
  if (end < 0) return { names: [], unredirected: true };
  const names = step.code
    .slice(at + "workflow-values.mjs".length, end)
    .split(/[\s"\\]+/)
    .filter(Boolean);
  return { names };
}

/** The `id:` of every step that runs `workflow-values.mjs`. */
const READER_ID = "cfg";

const JOBS = readJobs();

/** `sites`: every read of a configured value. `problems`: each as a sentence. */
function inspectWiring(jobs) {
  const problems = [];
  let readers = 0;
  let reads = 0;
  // job → { outputName: valueName } for outputs that carry a configured value.
  const exported = new Map();

  // Pass 1: the steps that read the configuration, and what each job exports.
  for (const job of jobs) {
    const at = `${job.file} › ${job.name}`;
    job.readers = new Map();
    job.steps.forEach((step, index) => {
      const ask = asked(step);
      if (!ask) return;
      readers++;
      const id = scalar(step.code, "id");
      if (!id) return problems.push(`${at}: a step runs workflow-values.mjs without an \`id:\`, so nothing can read what it wrote`);
      if (id !== READER_ID) problems.push(`${at}: the step that runs workflow-values.mjs has id ${id}; it must be \`${READER_ID}\`, the one name a read of a configured value is recognised by`);
      if (ask.unredirected) return problems.push(`${at}: step ${id} runs workflow-values.mjs without \`>> "$GITHUB_OUTPUT"\``);
      for (const name of ask.names) {
        if (!Object.hasOwn(VALUES, name)) problems.push(`${at}: step ${id} asks for ${name}, which workflow-values.mjs does not know`);
      }
      if (/continue-on-error:\s*true/.test(step.code)) problems.push(`${at}: step ${id} reads the configuration with continue-on-error, so a failed read would go on with empty values`);
      job.readers.set(id, { index, names: new Set(ask.names), cond: scalar(step.code, "if"), used: new Set() });
    });
  }
  const outputsOf = (job) => {
    const out = new Map();
    const block = /^ {4}outputs:\n((?: {6}.*\n?)*)/m.exec(job.headCode);
    for (const m of (block?.[1] ?? "").matchAll(/^ {6}([A-Za-z0-9_-]+):\s*\$\{\{\s*(.+?)\s*\}\}\s*$/gm)) out.set(m[1], m[2]);
    return out;
  };
  // Resolve exports to a fixpoint: an output may pass another job's on.
  for (let pass = 0; pass < 4; pass++) {
    for (const job of jobs) {
      const mine = exported.get(job) ?? new Map();
      for (const [out, expr] of outputsOf(job)) {
        const own = /^steps\.([\w-]+)\.outputs\.(\w+)$/.exec(expr);
        if (own && job.readers.has(own[1])) {
          const reader = job.readers.get(own[1]);
          if (pass === 0) {
            reads++;
            if (!reader.names.has(own[2])) problems.push(`${job.file} › ${job.name}: output ${out} reads ${own[2]}, which step ${own[1]} never asked for`);
            if (reader.cond) problems.push(`${job.file} › ${job.name}: output ${out} hands on a value from step ${own[1]}, which is conditional (\`if: ${reader.cond}\`) — the output is empty whenever it is skipped`);
            if (out !== own[2]) problems.push(`${job.file} › ${job.name}: output ${out} carries the value ${own[2]} under another name`);
          }
          reader.used.add(own[2]);
          mine.set(out, own[2]);
        }
        const passed = /^needs\.([\w-]+)\.outputs\.(\w+)$/.exec(expr);
        if (passed && Object.hasOwn(VALUES, passed[2])) {
          const from = jobs.find((j) => j.file === job.file && j.name === passed[1]);
          if (from && exported.get(from)?.has(passed[2])) mine.set(out, passed[2]);
        }
      }
      exported.set(job, mine);
    }
  }

  // Pass 2: every read, in the steps.
  for (const job of jobs) {
    const at = `${job.file} › ${job.name}`;
    const needs = (/^ {4}needs:\s*\[?([^\]\n]*)\]?/m.exec(job.headCode)?.[1] ?? "").split(/[,\s]+/).filter(Boolean);
    job.steps.forEach((step, index) => {
      for (const m of step.code.matchAll(new RegExp(`steps\\.([\\w-]+)\\.outputs\\.(${NAME.source})`, "g"))) {
        const reader = job.readers.get(m[1]);
        if (!reader) {
          // Some other step's output — unless it is spelt like a configured
          // read. `cfg` is the id every reader carries, so `steps.cfg.outputs.*`
          // in a job with no such step (a line moved between jobs, a
          // `needs.route` rewritten by hand) is always empty.
          if (m[1] === READER_ID) {
            reads++;
            problems.push(`${at}: reads steps.${READER_ID}.outputs.${m[2]}, but this job has no step that runs workflow-values.mjs — it is always empty`);
          }
          continue;
        }
        reads++;
        reader.used.add(m[2]);
        if (!reader.names.has(m[2])) problems.push(`${at}: reads ${m[1]}.outputs.${m[2]}, which that step never asked for — it is always empty`);
        if (index <= reader.index) problems.push(`${at}: reads ${m[1]}.outputs.${m[2]} in or before the step that writes it`);
        // A conditional reader must cover everything that reads it.
        if (reader.cond) {
          const mine = scalar(step.code, "if") ?? "";
          if (!mine.includes(reader.cond)) problems.push(`${at}: reads ${m[1]}.outputs.${m[2]} under \`if: ${mine || "(none)"}\`, but the step that writes it runs only under \`if: ${reader.cond}\``);
        }
      }
      for (const m of step.code.matchAll(new RegExp(`needs\\.([\\w-]+)\\.outputs\\.(${NAME.source})`, "g"))) {
        if (!Object.hasOwn(VALUES, m[2])) continue; // not a configured value
        reads++;
        const from = jobs.find((j) => j.file === job.file && j.name === m[1]);
        if (!from || !exported.get(from)?.has(m[2])) problems.push(`${at}: reads needs.${m[1]}.outputs.${m[2]}, which job ${m[1]} does not export from the configuration`);
        if (!needs.includes(m[1])) problems.push(`${at}: reads needs.${m[1]}.outputs.${m[2]} without \`needs: ${m[1]}\``);
      }
    });
    for (const [id, reader] of job.readers) {
      for (const name of reader.names) {
        if (!reader.used.has(name)) problems.push(`${at}: step ${id} asks for ${name} and nothing reads it`);
      }
    }
  }
  return { problems, readers, reads };
}

const WIRING = inspectWiring(JOBS);

test("every configured value a workflow reads was asked for, by a step that can reach it", () => {
  assert.deepEqual(WIRING.problems, []);
});

test("the wiring check inspected the sites it is expected to", () => {
  // A guard that finds nothing passes. Adding or removing a reader or a read
  // changes a count; that is the prompt to look at the new site.
  assert.deepEqual({ readers: WIRING.readers, reads: WIRING.reads }, { readers: 11, reads: 63 });
});

test("no workflow names the repository's values itself", () => {
  // The other direction: a value that is configured must not ALSO be written
  // into a workflow, where it would be one repository's answer in a file that
  // is meant to be every repository's. Checked against the fixture's values
  // being absent would prove nothing, so this looks for the SHAPES: a bot
  // login literal in an allow-list, an `:(exclude)` pathspec, `allowed_bots`
  // set to anything but the configured value.
  for (const file of readdirSync(WF_DIR).filter((f) => /^agent-.*\.ya?ml$/.test(f))) {
    const code = readFileSync(path.join(WF_DIR, file), "utf8").split("\n").filter((l) => !isComment(l) && !/^\s*\/\//.test(l));
    for (const line of code) {
      assert.ok(!/:\(exclude\)/.test(line), `${file}: a literal exclude pathspec — ${line.trim()}`);
      assert.ok(!/\[\s*['"][\w-]+\[bot\]['"]\s*,\s*['"][\w-]+\[bot\]['"]/.test(line), `${file}: a literal bot allow-list — ${line.trim()}`);
      if (/^\s*allowed_bots:/.test(line)) {
        assert.match(line, /allowed_bots: "\$\{\{ steps\.cfg\.outputs\.allowed_bots \}\}"/, `${file}: allowed_bots is not the configured value`);
      }
    }
  }
});

// --- mutations: each must fail the wiring check ---------------------------------------

test("each way of reading a value that is not there fails the wiring check", () => {
  const withText = (file, change) => {
    const jobs = readJobsFrom(file, change);
    return inspectWiring(jobs).problems;
  };
  const cases = [
    ["a misspelt output name", "agent-fix.yml", (t) => t.replace("steps.cfg.outputs.verify_command }}` ONCE", "steps.cfg.outputs.verify_comand }}` ONCE"), /reads cfg\.outputs\.verify_comand, which that step never asked for/],
    ["a name dropped from the request", "agent-fix.yml", (t) => t.replace("          app_slug docs_ref\n", "          app_slug\n"), /reads cfg\.outputs\.docs_ref, which that step never asked for/],
    ["a name the module does not know", "agent-loop.yml", (t) => t.replace("workflow-values.mjs app_slug docs_ref", "workflow-values.mjs app_slug docs_ref app_name"), /asks for app_name, which workflow-values\.mjs does not know/],
    ["the redirect removed", "agent-loop.yml", (t) => t.replace('workflow-values.mjs app_slug docs_ref >> "$GITHUB_OUTPUT"', "workflow-values.mjs app_slug docs_ref"), /without `>> "\$GITHUB_OUTPUT"`/],
    ["a route output that is no longer exported", "agent-summarize.yml", (t) => t.replace("      bot_logins: ${{ steps.cfg.outputs.bot_logins }}\n", ""), /reads needs\.route\.outputs\.bot_logins, which job route does not export/],
    ["the reader made conditional under an unconditional read", "agent-fix.yml", (t) => t.replace("      - name: Read the harness configuration\n        id: cfg\n", "      - name: Read the harness configuration\n        id: cfg\n        if: steps.app.outputs.configured == 'true'\n"), /but the step that writes it runs only under/],
    ["the reader allowed to fail", "agent-iterate-ci.yml", (t) => t.replace("      - name: Read the harness configuration\n        id: cfg\n", "      - name: Read the harness configuration\n        id: cfg\n        continue-on-error: true\n"), /with continue-on-error/],
    ["a read moved to a job that has no reader", "agent-fix.yml", (t) => t.replace("      - name: Collect the agent's output (untrusted data)\n", "      - name: stray\n        run: echo \"${{ steps.cfg.outputs.verify_command }}\"\n      - name: Collect the agent's output (untrusted data)\n"), /agent-fix\.yml › report: reads steps\.cfg\.outputs\.verify_command, but this job has no step that runs workflow-values\.mjs/],
    ["`needs.route` rewritten as `steps.cfg` in a job with no reader", "agent-summarize.yml", (t) => t.replace("BOT_LOGINS: ${{ needs.route.outputs.bot_logins }}", "BOT_LOGINS: ${{ steps.cfg.outputs.bot_logins }}"), /agent-summarize\.yml › announce: reads steps\.cfg\.outputs\.bot_logins, but this job has no step that runs workflow-values\.mjs/],
    ["a reader under another id", "agent-loop.yml", (t) => t.replace("        id: cfg\n", "        id: config\n"), /it must be `cfg`/],
    ["a value asked for and never read", "agent-summarize.yml", (t) => t.replace("workflow-values.mjs bot_logins >>", "workflow-values.mjs bot_logins repo_name >>"), /asks for repo_name and nothing reads it/],
  ];
  const failures = [];
  for (const [name, file, change, expect] of cases) {
    const before = readFileSync(path.join(WF_DIR, file), "utf8");
    if (change(before) === before) {
      failures.push(`COULD NOT APPLY "${name}" to ${file} — update the mutation to match the workflow`);
      continue;
    }
    const problems = withText(file, change);
    if (!problems.some((p) => expect.test(p))) failures.push(`${name}: wanted ${expect}, got ${JSON.stringify(problems)}`);
  }
  assert.deepEqual(failures, []);
});

/** `readJobs`, with one file's text changed in memory. */
function readJobsFrom(file, change) {
  const real = readFileSync;
  const target = path.join(WF_DIR, file);
  const text = change(real(target, "utf8"));
  const dir = mkdtempSync(path.join(os.tmpdir(), "wfv-wf-"));
  try {
    for (const f of readdirSync(WF_DIR).filter((n) => /^agent-.*\.ya?ml$/.test(n))) {
      writeFileSync(path.join(dir, f), f === file ? text : real(path.join(WF_DIR, f), "utf8"));
    }
    return readJobs(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}
