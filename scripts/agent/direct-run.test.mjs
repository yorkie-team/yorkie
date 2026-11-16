// `direct-run.mjs` answers one question for every tool in this package: was
// this file run, or imported? Workflows start the tools from a staged copy
// whose path can pass through a symbolic link, and the answer the tools used
// to compute came out "imported" there — so the step did nothing and exited 0.
//
// These tests RUN things through a linked directory rather than read the
// comparison: the function itself, then every tool in the package. The last
// test holds the directory to the shared module so the next tool does not
// hand-roll the broken form again.

import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { isDirectRun } from "./direct-run.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
// Realpath'd so the only link on a path below is the one a test makes.
const TMP = realpathSync(os.tmpdir());

function area(t) {
  const base = mkdtempSync(path.join(TMP, "direct-run-"));
  t.after(() => rmSync(base, { recursive: true, force: true }));
  return base;
}

// Set `process.argv[1]` for one test, and put it back.
function withEntry(t, value) {
  const saved = process.argv[1];
  t.after(() => {
    process.argv[1] = saved;
  });
  process.argv[1] = value;
}

test("imported, a module is not the entry point; the file node ran is", () => {
  assert.equal(isDirectRun(new URL("./direct-run.mjs", import.meta.url).href), false);
  assert.equal(isDirectRun(new URL("./set-state.mjs", import.meta.url).href), false);
  assert.equal(isDirectRun(import.meta.url), true);
});

test("run through a linked directory, a script is the entry point", (t) => {
  const base = area(t);
  mkdirSync(path.join(base, "real"));
  const probe = path.join(base, "real", "probe.mjs");
  writeFileSync(
    probe,
    `import { isDirectRun } from ${JSON.stringify(pathToFileURL(path.join(HERE, "direct-run.mjs")).href)};\n` +
      `console.log(isDirectRun(import.meta.url) ? "run" : "imported");\n`,
  );
  symlinkSync(path.join(base, "real"), path.join(base, "link"));

  const direct = spawnSync(process.execPath, [probe], { encoding: "utf8" });
  assert.equal(direct.stdout.trim(), "run", direct.stderr);
  // `argv[1]` as typed passes through the link; the module URL node resolved
  // does not. The case this module exists for.
  const viaLink = spawnSync(process.execPath, [path.join(base, "link", "probe.mjs")], { encoding: "utf8" });
  assert.equal(viaLink.stdout.trim(), "run", viaLink.stderr);
  // And a linked FILE, not only a linked directory.
  symlinkSync(probe, path.join(base, "probe-link.mjs"));
  const fileLink = spawnSync(process.execPath, [path.join(base, "probe-link.mjs")], { encoding: "utf8" });
  assert.equal(fileLink.stdout.trim(), "run", fileLink.stderr);
});

test("no entry, or an entry that is not there, is not this module", (t) => {
  const base = area(t);
  writeFileSync(path.join(base, "file"), "");
  withEntry(t, undefined);
  assert.equal(isDirectRun(import.meta.url), false);
  process.argv[1] = path.join(base, "no-such-script.mjs");
  assert.equal(isDirectRun(import.meta.url), false);
  // A file standing where a directory would be: ENOTDIR, also "not there".
  process.argv[1] = path.join(base, "file", "script.mjs");
  assert.equal(isDirectRun(import.meta.url), false);
  // A name too long to exist — an argument after `node -e`, not a path.
  process.argv[1] = "x".repeat(300);
  assert.equal(isDirectRun(import.meta.url), false);
});

test("an entry that exists but cannot be resolved throws instead of answering 'imported'", (t) => {
  const base = area(t);
  // A symbolic-link loop: the path is there, and resolving it fails (ELOOP).
  symlinkSync(path.join(base, "b"), path.join(base, "a"));
  symlinkSync(path.join(base, "a"), path.join(base, "b"));
  withEntry(t, path.join(base, "a"));
  assert.throws(() => isDirectRun(import.meta.url), /cannot tell whether this script was run or imported: .*\ba\b/);
});

// What each tool is run with below: an argument no tool accepts, which makes
// almost every one print its usage or refuse. The two that print nothing for
// it are given an `--out` they cannot write, which they report.
const PROBE_ARGS = {
  "ai-prompt.mjs": (base) => ["--out", path.join(base, "missing", "out")],
  "review-comment.mjs": (base) => ["--out", path.join(base, "missing", "out")],
};

test("every tool here does the same thing started through a linked directory as started directly", (t) => {
  // Behaviour, not text: a tool whose gate compares `argv[1]` as typed prints
  // nothing and exits 0 through the link, while the direct run prints its usage
  // — so the two differ, whatever the gate looks like.
  const base = area(t);
  const link = path.join(base, "agent-tools");
  symlinkSync(HERE, link);
  const home = path.join(base, "home");
  const bin = path.join(base, "bin");
  mkdirSync(home);
  mkdirSync(bin);
  // No credential and nothing on PATH — not `gh`, not `git` — so a tool that
  // did act would fail at once instead of reaching anything.
  const env = {
    PATH: bin,
    HOME: home,
    GH_CONFIG_DIR: home,
    AGENT_HARNESS_CONFIG_DIR: process.env.AGENT_HARNESS_CONFIG_DIR ?? path.join(HERE, "fixtures", "harness-config"),
  };
  const run = (dir, file) => {
    const args = PROBE_ARGS[file]?.(base) ?? ["--no-such-option"];
    const r = spawnSync(process.execPath, [path.join(dir, file), ...args], { cwd: base, env, encoding: "utf8", input: "", timeout: 30_000 });
    // A path printed from `argv[1]` names the link; one from the module, the
    // real directory. Neither is a difference in behaviour.
    return { status: r.status, output: `${r.stdout}${r.stderr}`.replaceAll(link, HERE) };
  };

  const tools = readdirSync(HERE).filter(
    (f) => f.endsWith(".mjs") && !f.endsWith(".test.mjs") && f !== "direct-run.mjs" && /\bprocess\.argv\b/.test(readFileSync(path.join(HERE, f), "utf8")),
  );
  assert.ok(tools.length > 25, `expected the agent tools, found ${tools.length}`);
  for (const file of tools) {
    const direct = run(HERE, file);
    assert.ok(
      direct.status !== 0 || direct.output !== "",
      `${file} run directly printed nothing and exited 0, so this probe cannot tell its gate fired; give it arguments in PROBE_ARGS that make it say something`,
    );
    assert.deepEqual(run(link, file), direct, `${file} behaves differently started through a symbolic link`);
  }
});

test("every tool decides run-or-imported through this module, bar the router", () => {
  // The forms that were wrong on a runner — `argv[1]` compared as typed with a
  // URL the loader had already resolved, or suffix matches — and private
  // copies of the right one, which drift.
  const HAND_ROLLED = [
    /path\.resolve\(process\.argv\[1\]\)/,
    /fileURLToPath\(import\.meta\.url\)\s*===\s*process\.argv\[1\]/,
    /import\.meta\.url\s*===\s*`file:/,
    /realpathSync\(process\.argv\[1\]\)/,
    /process\.argv\[1\]\.endsWith\(/,
    /function isDirectRun\(/,
  ];
  // `command.mjs` is the ONE exception, because of how it is delivered: route
  // jobs check that file out alone, so importing this module there is
  // ERR_MODULE_NOT_FOUND and no `@claude` verb is routed. It carries its own
  // copy, held to this module's answers by router-checkout.test.mjs.
  const ALONE = "command.mjs";
  const IMPORT = 'import { isDirectRun } from "./direct-run.mjs";';
  const modules = readdirSync(HERE).filter((f) => f.endsWith(".mjs") && !f.endsWith(".test.mjs") && f !== "direct-run.mjs");
  for (const file of modules) {
    const src = readFileSync(path.join(HERE, file), "utf8");
    if (file === ALONE) {
      assert.ok(!src.includes(IMPORT), `${ALONE} is checked out alone; it cannot import ./direct-run.mjs (see router-checkout.test.mjs)`);
      assert.match(src, /^if \(isDirectRun\(import\.meta\.url\)\) \{$/m, `${ALONE} no longer gates through its copy of isDirectRun`);
      continue;
    }
    for (const re of HAND_ROLLED) {
      assert.doesNotMatch(src, re, `${file} hand-rolls its entry check; use isDirectRun from ./direct-run.mjs`);
    }
    // A module that never asks — a CLI with no exports, run whenever it is
    // loaded — has no gate to get wrong.
    if (src.includes("isDirectRun(")) {
      assert.ok(src.includes(IMPORT), `${file} calls isDirectRun without importing it from ./direct-run.mjs`);
      assert.match(src, /^if \(isDirectRun\(import\.meta\.url\)\)/m, `${file} does not gate its CLI on isDirectRun(import.meta.url)`);
    }
  }
});
