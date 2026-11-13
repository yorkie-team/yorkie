import { FIXTURE_CONFIG_DIR } from "./fixtures/use-fixture-config.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  CONFIG_DIR_ENV,
  CONFIG_FILE,
  COVERAGE_NOTE_FILE,
  compileGlob,
  configDir,
  get,
  loadConfig,
} from "./config.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FIXTURE = JSON.parse(readFileSync(path.join(FIXTURE_CONFIG_DIR, CONFIG_FILE), "utf8"));

/** A scratch copy of the fixture directory, with `edit` applied to its JSON. */
function scratch(t, edit = (c) => c) {
  const dir = mkdtempSync(path.join(tmpdir(), "harness-config-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  cpSync(FIXTURE_CONFIG_DIR, dir, { recursive: true });
  const next = edit(structuredClone(FIXTURE));
  if (next !== undefined) writeFileSync(path.join(dir, CONFIG_FILE), JSON.stringify(next));
  return dir;
}

/** Every dotted path to a leaf (a non-object value) of `obj`. */
function leafPaths(obj, prefix = "") {
  const out = [];
  for (const [k, v] of Object.entries(obj)) {
    const at = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === "object" && !Array.isArray(v)) out.push(...leafPaths(v, at));
    else out.push(at);
  }
  return out;
}

function without(obj, dotted) {
  const parts = dotted.split(".");
  let node = obj;
  for (const p of parts.slice(0, -1)) node = node[p];
  delete node[parts.at(-1)];
  return obj;
}

function withValue(obj, dotted, value) {
  const parts = dotted.split(".");
  let node = obj;
  for (const p of parts.slice(0, -1)) node = node[p];
  node[parts.at(-1)] = value;
  return obj;
}

test("loadConfig: reads the directory, adds the coverage note, and freezes the result", () => {
  const config = loadConfig(FIXTURE_CONFIG_DIR);
  assert.equal(config.repo.name, "fixture-repo");
  assert.deepEqual([...config.ci.definingPaths], FIXTURE.ci.definingPaths);
  // The note is the file minus exactly one trailing newline — the file's own.
  const raw = readFileSync(path.join(FIXTURE_CONFIG_DIR, COVERAGE_NOTE_FILE), "utf8");
  assert.ok(raw.endsWith("\n"), "the fixture note is expected to end with a newline");
  assert.equal(config.review.mechanicalCoverageNote, raw.slice(0, -1));
  assert.ok(Object.isFrozen(config) && Object.isFrozen(config.ci) && Object.isFrozen(config.ci.definingPaths));
  assert.throws(() => config.ci.definingPaths.push("x"), TypeError);
  assert.equal(loadConfig(FIXTURE_CONFIG_DIR), config, "one read per directory per process");
});

test("loadConfig: with no argument it reads the directory the environment names", () => {
  assert.equal(process.env[CONFIG_DIR_ENV], FIXTURE_CONFIG_DIR);
  assert.equal(loadConfig(), loadConfig(FIXTURE_CONFIG_DIR));
});

test("loadConfig: a missing directory, or either missing file, throws", (t) => {
  const gone = path.join(tmpdir(), "harness-config-does-not-exist");
  assert.throws(() => loadConfig(gone), /no directory at .*harness-config-does-not-exist/);

  const noJson = scratch(t);
  rmSync(path.join(noJson, CONFIG_FILE));
  assert.throws(() => loadConfig(noJson), /cannot read .*config\.json/);

  const noNote = scratch(t);
  rmSync(path.join(noNote, COVERAGE_NOTE_FILE));
  assert.throws(() => loadConfig(noNote), /cannot read .*coverage-note\.md/);

  const emptyNote = scratch(t);
  writeFileSync(path.join(emptyNote, COVERAGE_NOTE_FILE), "\n");
  assert.throws(() => loadConfig(emptyNote), /coverage-note\.md is empty/);

  // A FILE where the directory should be is not a directory.
  assert.throws(() => loadConfig(path.join(FIXTURE_CONFIG_DIR, CONFIG_FILE)), /no directory at/);
});

test("loadConfig: JSON that does not parse, or is not an object, throws", (t) => {
  const broken = scratch(t);
  writeFileSync(path.join(broken, CONFIG_FILE), "{ not json");
  assert.throws(() => loadConfig(broken), /is not valid JSON/);

  for (const doc of ["[]", "null", '"text"']) {
    const dir = scratch(t);
    writeFileSync(path.join(dir, CONFIG_FILE), doc);
    assert.throws(() => loadConfig(dir), /must be an object/, doc);
  }
});

test("loadConfig: EVERY key is required — removing any one of them throws, naming it", (t) => {
  // `tests.go.*` is the one conditional block; it has its own test below.
  const required = leafPaths(FIXTURE).filter((p) => !p.startsWith("tests.go."));
  // The count is the guard on the guard: a walk that found nothing would pass.
  assert.ok(required.length >= 25, `expected the whole schema, walked ${required.length} keys`);
  for (const key of required) {
    const dir = scratch(t, (c) => without(c, key));
    assert.throws(
      () => loadConfig(dir),
      (err) => err.message.includes(`${key} is missing`),
      `removing ${key} must throw and name it`,
    );
  }
  // …and a whole section, not just its leaves.
  for (const section of Object.keys(FIXTURE)) {
    const dir = scratch(t, (c) => without(c, section));
    assert.throws(() => loadConfig(dir), (err) => err.message.includes(`${section} is missing`), section);
  }
});

test("loadConfig: a value of the wrong type throws, and no value is coerced", (t) => {
  const wrong = [
    ["repo.name", ""],
    ["repo.name", 7],
    ["repo.botLogins", []],
    ["repo.botLogins", "github-actions[bot]"],
    ["repo.botLogins", ["ok", ""]],
    ["ci.definingPaths", []],
    ["ci.definingPaths", [1]],
    ["ci.handoffSummary", "one line"],
    ["ci.laneReports", "false"],
    ["review.classRules", []],
    ["review.classRules.policy", null],
    ["review.sourceGlobs", []],
    ["review.diffExcludes", {}],
    ["fixer.verifyCommand", "   "],
    ["fixer.todoTrackingLine", null],
    ["tests.rules", []],
    ["tests.rules", ["cobol"]],
    ["tests.rules", ["go", "go"]],
    ["tests.go.ciPlatform", "linux/amd64"],
    ["tests.go.ciPlatform.goos", ""],
    ["fixGuard.replicatedRuleGlobs", "lib/**"],
    ["redact.extraPatterns", null],
    ["ask.permittedMcpTools", [null]],
  ];
  for (const [key, value] of wrong) {
    const dir = scratch(t, (c) => withValue(c, key, value));
    assert.throws(
      () => loadConfig(dir),
      (err) => err.message.includes(`${key} must be`),
      `${key} = ${JSON.stringify(value)} must be refused`,
    );
  }
});

test("loadConfig: an unknown key throws, at any depth", (t) => {
  // A typo must not read as "that value is absent": `definingPath` beside a
  // complete config is caught here, and as a missing key when it replaces one.
  for (const [key, shown] of [["extra", "extra"], ["ci.definingPath", "ci.definingPath"], ["tests.go.ciPlatform.os", "tests.go.ciPlatform.os"]]) {
    const dir = scratch(t, (c) => withValue(c, key, "x"));
    assert.throws(() => loadConfig(dir), (err) => err.message.includes(`${shown} is not a known key`), key);
  }
});

test("loadConfig: reports every problem at once", (t) => {
  const dir = scratch(t, (c) => withValue(without(without(c, "repo.name"), "ci.gateLabel"), "ci.laneReports", 1));
  assert.throws(() => loadConfig(dir), (err) =>
    ["repo.name is missing", "ci.gateLabel is missing", "ci.laneReports must be"].every((p) => err.message.includes(p)));
});

test("loadConfig: tests.go is required exactly when tests.rules names go", (t) => {
  const goWithoutBlock = scratch(t, (c) => without(c, "tests.go"));
  assert.throws(() => loadConfig(goWithoutBlock), /tests\.go is missing, and tests\.rules names "go"/);

  const noGo = scratch(t, (c) => without(withValue(c, "tests.rules", ["vitest"]), "tests.go"));
  assert.deepEqual([...loadConfig(noGo).tests.rules], ["vitest"]);
  assert.equal(loadConfig(noGo).tests.go, undefined);
});

test("loadConfig: a replicated-rule glob needs its notice and its counterpart", (t) => {
  for (const key of ["fixGuard.replicatedRuleNotice", "fixGuard.counterpartRepo"]) {
    const dir = scratch(t, (c) => withValue(c, key, ""));
    assert.throws(() => loadConfig(dir), (err) => err.message.includes(`${key} must be non-empty`), key);
  }
  // A repository with no replicated rule says so with an empty list, and then
  // has nothing to say in the other two.
  const none = scratch(t, (c) => {
    c.fixGuard = { replicatedRuleGlobs: [], replicatedRuleNotice: "", counterpartRepo: "" };
    return c;
  });
  assert.deepEqual([...loadConfig(none).fixGuard.replicatedRuleGlobs], []);
});

test("configDir: beside the package by default, the override when set, never a relative path", () => {
  assert.equal(configDir({}), path.resolve(HERE, "..", "..", ".github", "agent-harness"));
  assert.equal(configDir({ [CONFIG_DIR_ENV]: "/somewhere/trusted" }), path.normalize("/somewhere/trusted"));
  // Set-but-empty is what an unset variable expands to in a workflow `env:`
  // block. Reading it as "use the default" would be a silent fallback.
  assert.throws(() => configDir({ [CONFIG_DIR_ENV]: "" }), /set but empty/);
  assert.throws(() => configDir({ [CONFIG_DIR_ENV]: "  " }), /set but empty/);
  // Relative resolves against the working directory — a PR checkout, in the
  // jobs that set this.
  assert.throws(() => configDir({ [CONFIG_DIR_ENV]: ".github/agent-harness" }), /must be an absolute path/);
});

test("get: a dotted key, a section, and a throw for anything that is not there", () => {
  assert.equal(get("repo.name"), "fixture-repo");
  assert.deepEqual([...get("review.classRules.policy")], FIXTURE.review.classRules.policy);
  assert.equal(get("ci").gateLabel, FIXTURE.ci.gateLabel);
  assert.equal(get("review.mechanicalCoverageNote").split("\n")[0], "## What the mechanical lanes cover on this branch");
  for (const key of ["nope", "ci.nope", "ci.gateLabel.length", "repo.name.x", "", "ci..gateLabel"]) {
    assert.throws(() => get(key), /harness config:/, `get(${JSON.stringify(key)})`);
  }
  assert.throws(() => get(undefined), /needs a dotted key/);
  // Inherited properties are not keys.
  assert.throws(() => get("ci.constructor"), /no key/);
  assert.throws(() => get("ci.definingPaths.length"), /no key/);
});

test("compileGlob: `**` spans separators, `*` does not, the rest is literal and anchored", () => {
  const m = (glob, p) => compileGlob(glob).test(p);
  assert.ok(m("tools/ci/**", "tools/ci/a/b.sh"));
  assert.ok(!m("tools/ci/**", "xtools/ci/a"));
  assert.ok(m("tools/*.sh", "tools/a.sh"));
  assert.ok(!m("tools/*.sh", "tools/a/b.sh"));
  assert.ok(!m("build.config", "buildXconfig"), "a dot is a dot");
  assert.ok(!m("build.config", "build.config.bak"), "anchored at the end");
  assert.ok(!m("build.config", "sub/build.config"), "anchored at the start");
  assert.ok(m("a?b", "a?b") && !m("a?b", "axb"), "`?` is literal");
});

// --- CLI ---------------------------------------------------------------------

function cli(args, env = {}) {
  return spawnSync(process.execPath, [path.join(HERE, "config.mjs"), ...args], {
    encoding: "utf8",
    env: { ...process.env, ...env },
  });
}

test("CLI: a string is printed raw, anything else as JSON", () => {
  const name = cli(["get", "repo.name"]);
  assert.equal(name.status, 0, name.stderr);
  assert.equal(name.stdout, "fixture-repo\n");

  const paths = cli(["get", "ci.definingPaths"]);
  assert.equal(paths.status, 0, paths.stderr);
  assert.deepEqual(JSON.parse(paths.stdout), FIXTURE.ci.definingPaths);

  assert.equal(cli(["get", "ci.laneReports"]).stdout, "false\n");
  assert.equal(cli(["get", "fixer.todoTrackingLine"]).stdout, "\n", "an empty string is a value, and exits 0");
});

test("CLI: every failure exits 1 with a message and prints NOTHING to stdout", (t) => {
  // Empty stdout matters as much as the status: a caller writing
  // `x=$(node config.mjs get …)` without `set -e` must not be handed text.
  const broken = scratch(t, (c) => without(c, "ci.gateLabel"));
  const cases = [
    [["get", "ci.nope"], {}, /no key "ci\.nope"/],
    [["get"], {}, /usage:/],
    [["get", "repo.name", "extra"], {}, /usage:/],
    [["list"], {}, /usage:/],
    [[], {}, /usage:/],
    [["get", "repo.name"], { [CONFIG_DIR_ENV]: path.join(tmpdir(), "harness-config-does-not-exist") }, /no directory at/],
    [["get", "repo.name"], { [CONFIG_DIR_ENV]: "" }, /set but empty/],
    [["get", "repo.name"], { [CONFIG_DIR_ENV]: "relative/dir" }, /must be an absolute path/],
    [["get", "repo.name"], { [CONFIG_DIR_ENV]: broken }, /ci\.gateLabel is missing/],
  ];
  for (const [args, env, message] of cases) {
    const r = cli(args, env);
    assert.equal(r.status, 1, `${JSON.stringify(args)} ${JSON.stringify(env)}: ${r.stderr}`);
    assert.equal(r.stdout, "", `${JSON.stringify(args)} printed to stdout on failure`);
    assert.match(r.stderr, message);
  }
});

// --- the package's own discipline ---------------------------------------------

const MODULES = readdirSync(HERE).filter((f) => f.endsWith(".mjs"));
const source = (file) => readFileSync(path.join(HERE, file), "utf8");
const localImports = (file) =>
  [...source(file).matchAll(/^\s*(?:import|export)\b[^;]*?["'](\.\/[^"']+)["']/gm)]
    .map((m) => path.normalize(m[1]))
    .filter((f) => MODULES.includes(f) || f.startsWith("fixtures"));

/** Does `file` reach `config.mjs` through static relative imports? */
function reachesConfig(file, seen = new Set()) {
  if (file === "config.mjs") return true;
  if (seen.has(file) || !MODULES.includes(file)) return false;
  seen.add(file);
  return localImports(file).some((f) => reachesConfig(f, seen));
}

test("config.mjs is the only module that knows where the configuration lives", () => {
  const sources = MODULES.filter((f) => !f.endsWith(".test.mjs") && f !== "config.mjs");
  assert.ok(sources.length > 20, `expected the agent package, found ${sources.length} modules`);
  for (const file of sources) {
    assert.doesNotMatch(
      source(file).replace(/^\s*(\/\/|\*).*$/gm, ""),
      /agent-harness|AGENT_HARNESS_CONFIG_DIR/,
      `${file} names the configuration directory; read values through config.mjs instead`,
    );
  }
});

test("command.mjs does not reach the configuration", () => {
  // Route jobs check `command.mjs` out alone. A failed router prints no
  // `command=`, which every consumer reads as "no verb in this comment".
  assert.equal(reachesConfig("command.mjs"), false);
  assert.deepEqual(localImports("command.mjs"), []);
});
