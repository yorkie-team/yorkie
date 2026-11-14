import { FIXTURE_CONFIG_DIR } from "./fixtures/use-fixture-config.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
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
  globProblem,
  loadConfig,
  getOptionalList,
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

/** The keys that only add to a list the package already has, and so may be absent. */
const OPTIONAL_ADDITIONS = ["review.classRules.design-spec", "review.classRules.prose"];

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
  // `tests.go.*` is the one conditional block, and the two class-rule
  // additions are the only optional leaves; each has its own test below.
  const required = leafPaths(FIXTURE).filter((p) => !p.startsWith("tests.go.") && !OPTIONAL_ADDITIONS.includes(p));
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
    ["repo.appSlug", ""],
    ["repo.appSlug", "Fixture Agent"],
    ["repo.appSlug", "fixture-agent[bot]"],
    ["fixer.verifyCommand", "   "],
    ["fixer.verifyCommand", "make verify\n"],
    ["fixer.verifyDescription", "lint and tests."],
    ["fixer.verifyDescription", "lint,\ntests"],
    ["fixer.keepReproducerAs", ", keep it"],
    ["fixer.integrationWarning", "Do NOT run it."],
    ["fixer.todoTrackingLine", ", and a second line"],
    ["fixer.todoTrackingLine", "its second line is this."],
    ["fixer.todoTrackingLine", "Its second line is this"],
    ["fixer.licenceScope", "A header on every file.\nAnd a comment."],
    ["fixer.lastResortDiagnosis", "re-run it"],
    ["fixer.ciFailureNote", "let CI confirm"],
    ["fixer.implementVerifyStep", 5],
    ["fixer.todoTrackingLine", null],
    ["fixer.verifyFailureHint", null],
    ["fixer.replyVerifyInstruction", ["run it"]],
    ["tests.rules", []],
    ["tests.rules", ["cobol"]],
    ["tests.rules", ["go", "go"]],
    ["tests.go.ciPlatform", "linux/amd64"],
    ["tests.go.ciPlatform.goos", ""],
    ["fixGuard.replicatedRuleGlobs", "lib/**"],
    ["fixGuard.counterpartRepos", "fixture-org/fixture-counterpart"],
    ["fixGuard.counterpartRepos", ["fixture-counterpart"]],
    ["fixGuard.counterpartRepos", ["fixture-org/fixture-counterpart#"]],
    ["fixGuard.counterpartRepos", ["https://github.com/fixture-org/fixture-counterpart"]],
    ["fixGuard.counterpartRepos", ["a/b/c"]],
    ["fixGuard.counterpartRepos", ["fixture-org/.."]],
    ["fixGuard.counterpartRepos", ["fixture-org/."]],
    ["fixGuard.counterpartRepos", ["fixture--org/x"]],
    ["fixGuard.counterpartRepos", ["fixture-org-/x"]],
    ["fixGuard.counterpartRepos", ["a/b", "a/b"]],
    ["fixGuard.counterpartRepos", ["a/b", ""]],
    ["fixer.commitNote", "subject under 70"],
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

  // The other direction: a Go block with the Go rules off is configuration
  // nothing reads, written by someone who believes it is on.
  const blockWithoutGo = scratch(t, (c) => withValue(c, "tests.rules", ["vitest"]));
  assert.throws(() => loadConfig(blockWithoutGo), /tests\.go is present, but tests\.rules does not name "go"/);
});

test("loadConfig: tests.cargo is required exactly when tests.rules names cargo", (t) => {
  const block = { integrationTestDirs: ["crates/*/tests", "tests"], testOnlyFileGlobs: ["crates/core/src/sync_scenarios.rs"] };
  const cargo = (c, value = block) => {
    c.tests = { rules: ["cargo", "vitest"], ...(value === null ? {} : { cargo: value }) };
    return c;
  };
  const fine = scratch(t, (c) => cargo(c));
  assert.deepEqual([...loadConfig(fine).tests.rules], ["cargo", "vitest"]);
  assert.deepEqual([...loadConfig(fine).tests.cargo.integrationTestDirs], ["crates/*/tests", "tests"]);
  // Beside Go, each with its own block.
  const both = scratch(t, (c) => {
    c.tests = { ...c.tests, rules: ["go", "cargo"], cargo: block };
    return c;
  });
  assert.deepEqual([...loadConfig(both).tests.rules], ["go", "cargo"]);
  // A crate with no integration tests, and no file to single out, says so.
  const empty = scratch(t, (c) => cargo(c, { integrationTestDirs: [], testOnlyFileGlobs: [] }));
  assert.deepEqual([...loadConfig(empty).tests.cargo.integrationTestDirs], []);

  const missing = scratch(t, (c) => cargo(c, null));
  assert.throws(() => loadConfig(missing), /tests\.cargo is missing, and tests\.rules names "cargo"/);
  const unread = scratch(t, (c) => {
    c.tests.cargo = block;
    return c;
  });
  assert.throws(() => loadConfig(unread), /tests\.cargo is present, but tests\.rules does not name "cargo"/);
  // A Go block does not stand in for it, in either direction.
  const goBlockOnly = scratch(t, (c) => withValue(c, "tests.rules", ["cargo"]));
  assert.throws(() => loadConfig(goBlockOnly), (err) =>
    err.message.includes('tests.cargo is missing') && err.message.includes('tests.go is present, but tests.rules does not name "go"'));

  for (const key of ["integrationTestDirs", "testOnlyFileGlobs"]) {
    const without_ = scratch(t, (c) => cargo(c, without(structuredClone(block), key)));
    assert.throws(() => loadConfig(without_), (err) => err.message.includes(`tests.cargo.${key} is missing`), key);
    for (const bad of ["crates/*/tests", ["crates/*/tests/"], ["./tests"], ["{a,b}/tests"], ["a", "a"], [""]]) {
      const dir = scratch(t, (c) => cargo(c, { ...block, [key]: bad }));
      assert.throws(() => loadConfig(dir), (err) => err.message.includes(`tests.cargo.${key} must be`), `${key} = ${JSON.stringify(bad)}`);
    }
  }
  // A target directory is named itself. `dir/**` names its contents at every
  // depth, and would make every helper file under it a target.
  for (const bad of [["crates/*/tests/**"], ["**"]]) {
    const dir = scratch(t, (c) => cargo(c, { ...block, integrationTestDirs: bad }));
    assert.throws(() => loadConfig(dir), /tests\.cargo\.integrationTestDirs must be an array of directories.*name the directory itself/, JSON.stringify(bad));
  }
  const deep = scratch(t, (c) => cargo(c, { ...block, integrationTestDirs: ["**/tests"] }));
  assert.deepEqual([...loadConfig(deep).tests.cargo.integrationTestDirs], ["**/tests"]);
  const unknown = scratch(t, (c) => cargo(c, { ...block, features: ["default"] }));
  assert.throws(() => loadConfig(unknown), /tests\.cargo\.features is not a known key/);
});

test("loadConfig: the App's slug and its logins name one App", (t) => {
  // The workflows allow-list `<slug>[bot]` and the scripts believe the logins.
  // A slug that named another App would have the two disagree in silence.
  const other = scratch(t, (c) => withValue(c, "repo.appSlug", "another-agent"));
  assert.throws(() => loadConfig(other), (err) =>
    err.message.includes('repo.appLogins must include "another-agent[bot]"') &&
    err.message.includes('repo.botLogins must include "another-agent[bot]"'));
  const missing = scratch(t, (c) => without(c, "repo.appSlug"));
  assert.throws(() => loadConfig(missing), /repo\.appSlug is missing/);
});

test("loadConfig: the optional fixer sentences may be empty, the others may not", (t) => {
  for (const key of ["verifyFailureHint", "ciFailureNote", "generatedCodeNote", "apiChangeNote", "commitNote", "licenceScope", "todoTrackingLine", "replyVerifyInstruction"]) {
    const dir = scratch(t, (c) => withValue(c, `fixer.${key}`, ""));
    assert.equal(loadConfig(dir).fixer[key], "", key);
  }
  // The two halves of the "do not run" sentence: both, or neither.
  const neither = scratch(t, (c) => withValue(withValue(c, "fixer.integrationWarning", ""), "fixer.integrationRerunBy", ""));
  assert.equal(loadConfig(neither).fixer.integrationWarning, "");
  for (const key of ["integrationWarning", "integrationRerunBy"]) {
    const half = scratch(t, (c) => withValue(c, `fixer.${key}`, ""));
    assert.throws(() => loadConfig(half), /fixer\.integrationWarning and fixer\.integrationRerunBy must both be set or both be empty/, key);
  }
  for (const key of ["verifyCommand", "verifyDescription", "keepReproducerAs", "disabledTestForms", "implementVerifyStep", "toolchainSteps", "lastResortDiagnosis"]) {
    const dir = scratch(t, (c) => withValue(c, `fixer.${key}`, ""));
    assert.throws(() => loadConfig(dir), (err) => err.message.includes(`fixer.${key} must be`), key);
  }
});

test("loadConfig: a replicated-rule glob needs its notice and its counterpart", (t) => {
  for (const [key, nothing] of [["fixGuard.replicatedRuleNotice", ""], ["fixGuard.counterpartRepos", []]]) {
    const dir = scratch(t, (c) => withValue(c, key, nothing));
    assert.throws(() => loadConfig(dir), (err) => err.message.includes(`${key} must be non-empty`), key);
  }
  // A repository with no replicated rule says so with an empty list, and then
  // has nothing to say in the other two.
  const none = scratch(t, (c) => {
    c.fixGuard = { replicatedRuleGlobs: [], replicatedRuleNotice: "", counterpartRepos: [] };
    return c;
  });
  assert.deepEqual([...loadConfig(none).fixGuard.replicatedRuleGlobs], []);
  // More than one counterpart is the point of the list.
  const two = scratch(t, (c) => withValue(c, "fixGuard.counterpartRepos", ["a/b", "a/c"]));
  assert.deepEqual([...loadConfig(two).fixGuard.counterpartRepos], ["a/b", "a/c"]);
});

test("loadConfig: the class-rule additions may be absent, and are validated when present", (t) => {
  for (const key of OPTIONAL_ADDITIONS) {
    assert.ok(Array.isArray(get(key)), `${key}: the fixture is expected to set it`);
    const dir = scratch(t, (c) => without(c, key));
    const config = loadConfig(dir);
    assert.equal(Object.hasOwn(config.review.classRules, key.split(".").at(-1)), false, key);
    // Absent reads as "no additions", through the one reader that allows it.
    assert.deepEqual(getOptionalList(key, config), [], key);
    assert.throws(() => get(key, config), /no key/, `${key}: get() still refuses an absent key`);
    assert.deepEqual([...getOptionalList(key)], FIXTURE.review.classRules[key.split(".").at(-1)], key);
    const empty = scratch(t, (c) => withValue(c, key, []));
    assert.deepEqual(getOptionalList(key, loadConfig(empty)), [], key);
    for (const bad of ["docs/plans", ["docs/plans/"], ["{a,b}/**"], [7], null]) {
      const wrong = scratch(t, (c) => withValue(c, key, bad));
      assert.throws(() => loadConfig(wrong), (err) => err.message.includes(`${key} must be`), `${key} = ${JSON.stringify(bad)}`);
    }
  }
  // The reader is for those keys alone: it must not become a way to read any
  // absent key as an empty list.
  for (const key of ["review.classRules.policy", "review.sourceGlobs", "tests.go", "review.classRules.nope", "nope"]) {
    assert.throws(() => getOptionalList(key), /is not an optional list/, key);
  }
  // `prose` is matched last and routed off the code lenses: one entry that
  // matches everything would leave no file in `code`.
  const all = scratch(t, (c) => withValue(c, "review.classRules.prose", ["notes/**", "**"]));
  assert.throws(() => loadConfig(all), /review\.classRules\.prose must not contain "\*\*"/);
  const allSpec = scratch(t, (c) => withValue(c, "review.classRules.design-spec", ["**"]));
  assert.deepEqual([...loadConfig(allSpec).review.classRules["design-spec"]], ["**"], "design-spec is read by MORE lenses than code, so this is only expensive");
});

test("loadConfig: a key that was renamed says what to write instead", (t) => {
  // What an adopter sees after syncing past the rename: `sync.mjs` prints
  // these lines as the edits the new core leaves for a person.
  const old = scratch(t, (c) => {
    delete c.fixGuard.counterpartRepos;
    c.fixGuard.counterpartRepo = "fixture-org/fixture-counterpart";
    return c;
  });
  assert.throws(() => loadConfig(old), (err) =>
    err.message.includes("fixGuard.counterpartRepos is missing") &&
    err.message.includes('fixGuard.counterpartRepo is not a known key — it was renamed to fixGuard.counterpartRepos and is now a list: write ["owner/name"]'));
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

test("configDir: the default applies only to a package sitting at <root>/scripts/agent", () => {
  // In place, under any root — a side-directory checkout included.
  assert.equal(configDir({}, "/work/repo/.trusted/scripts/agent"), path.normalize("/work/repo/.trusted/.github/agent-harness"));
  // A staged copy has no repository beside it. Two directories up is wherever
  // the copy landed, and a `.github/agent-harness` that happened to be there
  // must not be read as if it were the trusted one.
  for (const staged of ["/home/runner/work/_temp/agent-tools", "/tmp/agent", "/tmp/scripts/agent-tools", "/tmp/x/scripts/agent/sub"]) {
    assert.throws(() => configDir({}, staged), /not at <root>\/scripts\/agent/, staged);
    // …and with the variable, the copy reads exactly what it names.
    assert.equal(configDir({ [CONFIG_DIR_ENV]: "/trusted/agent-harness" }, staged), path.normalize("/trusted/agent-harness"));
  }
});

test("a staged copy without the variable fails even when a config sits two directories up", (t) => {
  // The real thing, not the function: copy the package to <tmp>/x/agent-tools
  // and plant a valid configuration at <tmp>/.github/agent-harness, which is
  // exactly where the old default would have looked.
  const root = mkdtempSync(path.join(tmpdir(), "harness-staged-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const tools = path.join(root, "x", "agent-tools");
  mkdirSync(tools, { recursive: true });
  cpSync(path.join(HERE, "config.mjs"), path.join(tools, "config.mjs"));
  cpSync(FIXTURE_CONFIG_DIR, path.join(root, ".github", "agent-harness"), { recursive: true });
  const env = { ...process.env };
  delete env[CONFIG_DIR_ENV];
  const run = (extra) => spawnSync(process.execPath, [path.join(tools, "config.mjs"), "get", "repo.name"], { encoding: "utf8", env: { ...env, ...extra } });
  const bare = run({});
  assert.equal(bare.status, 1);
  assert.equal(bare.stdout, "");
  assert.match(bare.stderr, /not at <root>\/scripts\/agent/);
  const named = run({ [CONFIG_DIR_ENV]: FIXTURE_CONFIG_DIR });
  assert.equal(named.status, 0, named.stderr);
  assert.equal(named.stdout, "fixture-repo\n");
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

test("compileGlob: `**` is whole directories (none included), `*` stays in a segment, the rest is literal", () => {
  const m = (glob, p) => compileGlob(glob).test(p);
  assert.ok(m("tools/ci/**", "tools/ci/a/b.sh"));
  assert.ok(m("tools/ci/**", "tools/ci/a"));
  assert.ok(!m("tools/ci/**", "xtools/ci/a"));
  assert.ok(!m("tools/ci/**", "tools/ci"), "`dir/**` is what is UNDER the directory");
  assert.ok(m("tools/*.sh", "tools/a.sh"));
  assert.ok(!m("tools/*.sh", "tools/a/b.sh"));
  assert.ok(!m("build.config", "buildXconfig"), "a dot is a dot");
  assert.ok(!m("build.config", "build.config.bak"), "anchored at the end");
  assert.ok(!m("build.config", "sub/build.config"), "anchored at the start");
  // ZERO OR MORE directories. The earlier compilers needed at least one, so
  // `**/x` silently missed a root-level `x` and `docs/**/*.md` missed
  // `docs/a.md`.
  assert.ok(m("**/README.md", "README.md"));
  assert.ok(m("**/README.md", "a/b/README.md"));
  assert.ok(!m("**/README.md", "a/XREADME.md"));
  assert.ok(m("docs/**/*.md", "docs/a.md"));
  assert.ok(m("docs/**/*.md", "docs/a/b/c.md"));
  assert.ok(!m("docs/**/*.md", "xdocs/a.md"));
  assert.ok(m("**/fixtures/**", "fixtures/a.json"));
  assert.ok(m("**/fixtures/**", "a/fixtures/b/c.json"));
  assert.ok(!m("**/fixtures/**", "a/xfixtures/b"));
  assert.ok(m("**", "anything/at/all") && m("**", "x"));
  assert.ok(m("a/**/b/**/c", "a/b/c") && m("a/**/b/**/c", "a/x/b/y/z/c") && !m("a/**/b/**/c", "a/c"));
  // Regex metacharacters in a path are literal.
  assert.ok(m("a+b/(c)/$d.^e", "a+b/(c)/$d.^e") && !m("a+b", "aab"));
});

test("globProblem / compileGlob: syntax this dialect does not have is REFUSED, not read literally", () => {
  // Each of these compiles in some other tool and would match NOTHING here —
  // on a refusal surface that is an entry quietly taken off the list.
  const refused = [
    "", " ", "\t", " a", "a ", "./Makefile", "../x", "a/./b", "a/../b", "/Makefile", "tools/", "a//b",
    "{a,b}/**", "*.{js,ts}", "!secret/**", "a?b", "[ab].txt", "a\\b", "**.md", "a**", "a/**b/c", "***",
  ];
  for (const glob of refused) {
    assert.equal(typeof globProblem(glob), "string", `${JSON.stringify(glob)} must be refused`);
    assert.throws(() => compileGlob(glob), /harness glob/, JSON.stringify(glob));
  }
  for (const notString of [null, undefined, 7, {}, ["a"]]) assert.equal(globProblem(notString), "is not a string");
  // A run of wildcards in one segment is a typo that compiles into a pattern
  // which backtracks for minutes on a single long path. Two is the cap.
  for (const glob of ["a*b*c*d", "*a*a*a*a*a*a*a*a*b", "dir/*x*y*.go", "*/*/a*b*c*"]) {
    assert.match(globProblem(glob) ?? "", /more than 2 `\*` in one path segment/, glob);
    assert.throws(() => compileGlob(glob), /harness glob/, glob);
  }
  for (const glob of ["*.pb.go", "*_test.*", "tsconfig*.json", "*/*/*", "a*/b*/c*c*"]) assert.equal(globProblem(glob), null, glob);
  const started = Date.now();
  assert.equal(compileGlob("*a*b").test(`${"a".repeat(5000)}c`), false);
  assert.ok(Date.now() - started < 1000, "a capped glob must not backtrack for long on a long path");
  for (const fine of ["Makefile", ".golangci.yml", "a/b.c", "**", "**/x", "a/**", "a/**/b", "*.md", "a/*/b", "a-b_c/d e.txt", "scripts/*.mjs"]) {
    assert.equal(globProblem(fine), null, fine);
  }
});

test("loadConfig: every glob list refuses a non-glob entry and names it; no list takes a blank or repeated entry", (t) => {
  const globKeys = ["ci.definingPaths", "review.classRules.code-adjacent", "review.classRules.policy", "review.classRules.design-spec", "review.classRules.prose", "review.sourceGlobs", "fixGuard.replicatedRuleGlobs"];
  let checked = 0;
  for (const key of globKeys) {
    for (const bad of ["./x", "x/", "{a,b}", "!x", "a?b", "/x", "**.md"]) {
      const dir = scratch(t, (c) => withValue(c, key, ["ok/**", bad]));
      assert.throws(
        () => loadConfig(dir),
        (err) => err.message.includes(`${key} must be`) && err.message.includes(JSON.stringify(bad)),
        `${key} must refuse ${bad} and say which entry`,
      );
      checked++;
    }
  }
  assert.equal(checked, 49);
  // Blank, padded and repeated entries: in every list, glob or not.
  const lists = [...globKeys, "repo.botLogins", "repo.appLogins", "review.diffExcludes", "tests.rules", "tests.go.sharedSuiteDirs", "fixGuard.counterpartRepos", "redact.extraPatterns", "ask.permittedMcpTools"];
  for (const key of lists) {
    for (const value of [["a", " "], ["a", ""], ["a", " b"], ["a", "b "], ["a", "a"]]) {
      const dir = scratch(t, (c) => withValue(c, key, value));
      assert.throws(() => loadConfig(dir), (err) => err.message.includes(`${key} must be`), `${key} = ${JSON.stringify(value)}`);
    }
  }
  // The hand-off bullet is prose: indentation is kept, and a line may recur.
  const prose = scratch(t, (c) => withValue(c, "ci.handoffSummary", ["- a", "  indented", "  indented"]));
  assert.deepEqual([...loadConfig(prose).ci.handoffSummary], ["- a", "  indented", "  indented"]);
  const blankLine = scratch(t, (c) => withValue(c, "ci.handoffSummary", ["- a", "  "]));
  assert.throws(() => loadConfig(blankLine), /ci\.handoffSummary must be/);
});

test("loadConfig: the Go block takes identifiers and plain directory names, nothing else", (t) => {
  const wrong = [
    ["tests.go.ciPlatform.goos", "Linux"],
    ["tests.go.ciPlatform.goos", "linux/amd64"],
    ["tests.go.ciPlatform.goos", "linux "],
    ["tests.go.ciPlatform.goarch", "amd-64"],
    ["tests.go.ciPlatform.goarch", "64"],
    ["tests.go.sharedSuiteDirs", [".."]],
    ["tests.go.sharedSuiteDirs", ["."]],
    ["tests.go.sharedSuiteDirs", ["a/b"]],
    ["tests.go.sharedSuiteDirs", ["server/rpc/testcases"]],
    ["tests.go.sharedSuiteDirs", ["test*"]],
    ["tests.go.sharedSuiteDirs", [".hidden"]],
  ];
  for (const [key, value] of wrong) {
    const dir = scratch(t, (c) => withValue(c, key, value));
    assert.throws(() => loadConfig(dir), (err) => err.message.includes(`${key} must be`), `${key} = ${JSON.stringify(value)}`);
  }
  const fine = scratch(t, (c) => withValue(withValue(c, "tests.go.sharedSuiteDirs", ["testcases", "shared_suites", "v1.suites"]), "tests.go.ciPlatform", { goos: "linux", goarch: "riscv64" }));
  assert.deepEqual([...loadConfig(fine).tests.go.sharedSuiteDirs], ["testcases", "shared_suites", "v1.suites"]);
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

test("CLI: run through a symlinked path, it still runs", (t) => {
  // The loader resolves `import.meta.url` through symlinks; `process.argv[1]`
  // is what the caller typed. Compared as strings the two differ, the CLI body
  // is skipped, and the process prints nothing and exits 0 — an empty value to
  // whoever captured stdout.
  const dir = mkdtempSync(path.join(tmpdir(), "harness-link-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const link = path.join(dir, "scripts-agent-link");
  symlinkSync(HERE, link, "dir");
  const viaLink = spawnSync(process.execPath, [path.join(link, "config.mjs"), "get", "repo.name"], { encoding: "utf8" });
  assert.equal(viaLink.status, 0, viaLink.stderr);
  assert.equal(viaLink.stdout, "fixture-repo\n");
  const failing = spawnSync(process.execPath, [path.join(link, "config.mjs"), "get", "ci.nope"], { encoding: "utf8" });
  assert.equal(failing.status, 1);
  assert.match(failing.stderr, /no key "ci\.nope"/);
});

// --- the package's own discipline ---------------------------------------------

const MODULES = readdirSync(HERE).filter((f) => f.endsWith(".mjs"));
const source = (file) => readFileSync(path.join(HERE, file), "utf8");
// Static `import … from "./x.mjs"` / `export … from`, and dynamic `import("./x.mjs")`.
const localImports = (file) =>
  [
    ...source(file).matchAll(/^\s*(?:import|export)\b[^;]*?["'](\.\/[^"']+)["']/gm),
    ...source(file).matchAll(/\bimport\s*\(\s*["'](\.\/[^"']+)["']/g),
  ]
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
      // Comments aside, and the one place the directory is NAMED rather than
      // read: `checks.mjs` lists it among the paths that define CI.
      source(file).replace(/^\s*(\/\/|\*).*$/gm, "").replace('".github/agent-harness/**"', ""),
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

test("every test that reaches the configuration runs against the fixture", () => {
  const tests = MODULES.filter((f) => f.endsWith(".test.mjs"));
  const reaching = tests.filter((f) => reachesConfig(f));
  // Guard on the guard: an import walk that resolved nothing would pass.
  assert.ok(reaching.length >= 10, `expected many tests to reach config.mjs, found ${reaching.length}`);
  for (const file of reaching) {
    const first = /^\s*import\b[^;]*?["']([^"']+)["']/m.exec(source(file));
    assert.equal(
      first?.[1],
      "./fixtures/use-fixture-config.mjs",
      `${file} reaches config.mjs, so its FIRST import must be ./fixtures/use-fixture-config.mjs — ` +
        "otherwise it grades the code against this repository's values instead of the fixture's",
    );
  }
});
