// What THIS repository's harness configuration must say.
//
// `scripts/agent/*.test.mjs` grade the harness code against a fixture
// configuration and are the same bytes in every repository that runs the
// harness. This file is the other half: it grades the values in this directory
// — the ones that are different in every repository — and it is the only test
// that reads them.
//
// Run from `scripts/agent`: `npm run test:repo`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(HERE, "..", "..");
const AGENT = path.join(ROOT, "scripts", "agent");
const WORKFLOWS = path.join(ROOT, ".github", "workflows");

// This directory, whatever the caller's environment says. Set before the first
// harness module is imported — which is why the imports below are dynamic: a
// static import would be evaluated before this line.
process.env.AGENT_HARNESS_CONFIG_DIR = HERE;

const agent = (file) => import(path.join(AGENT, file));
const checks = await agent("checks.mjs");
const panel = await agent("review-panel.mjs");
const captureMeta = await agent("capture-meta.mjs");
const rounds = await agent("rounds.mjs");
const metrics = await agent("metrics.mjs");
const setState = await agent("set-state.mjs");
const rebuttal = await agent("rebuttal.mjs");
const fixGuard = await agent("fix-guard.mjs");
const testRemovals = await agent("test-removals.mjs");
const protection = await agent("branch-protection.mjs");
const reviewScope = await agent("review-scope.mjs");
const { compileGlob, loadConfig } = await agent("config.mjs");
const workflowValues = await agent("workflow-values.mjs");
const manifest = await agent("manifest.mjs");

const CONFIG = loadConfig();

test("this file grades the configuration in its own directory", () => {
  assert.equal(loadConfig(HERE), CONFIG);
  assert.equal(CONFIG.repo.name, "yorkie");
});

// --- the values the harness shipped with, before they were configuration ------
//
// `repo.snapshot.json` was written from the harness modules while every value
// below was still a literal in `scripts/agent/`. Moving the values into this
// directory must not change one character of what the harness exports, says to
// a reviewer, or decides — so each is compared against that recording.
//
// It stays after the move as a change detector: an edit to `config.json` or
// `coverage-note.md` that alters a gate, a prompt or a comment fails here until
// the recording is updated in the same change, which puts the before and after
// of every such edit in front of whoever reviews it.
//
// One entry is newer than the recording, and deliberate: `.github/agent-harness/**`
// joined the CI-defining paths when this directory began to hold the gates.

const SNAPSHOT = JSON.parse(readFileSync(path.join(HERE, "repo.snapshot.json"), "utf8"));

test("snapshot: the paths that define CI", () => {
  assert.deepEqual([...checks.CI_DEFINING_PATHS], SNAPSHOT.CI_DEFINING_PATHS);
});

test("snapshot: the file-class rules", () => {
  assert.deepEqual(JSON.parse(JSON.stringify(panel.CLASS_RULES)), SNAPSHOT.CLASS_RULES);
});

test("snapshot: the mechanical-coverage note", () => {
  assert.equal(panel.MECHANICAL_COVERAGE_NOTE, SNAPSHOT.MECHANICAL_COVERAGE_NOTE);
});

test("snapshot: every lens's appliesWhen and rubric, as the panel loads them", () => {
  const lenses = panel.loadLenses(path.join(AGENT, "lenses"));
  assert.deepEqual(
    Object.fromEntries(lenses.map((l) => [l.id, [...l.appliesWhen]])),
    SNAPSHOT.lensAppliesWhen,
  );
  assert.deepEqual(Object.fromEntries(lenses.map((l) => [l.id, l.rubric])), SNAPSHOT.lensRubrics);
  assert.ok(lenses.length >= 6, `expected the whole panel, loaded ${lenses.length} lenses`);
});

test("snapshot: the ready gate's CI gate name and the hand-off comment's CI bullet", () => {
  assert.equal(checks.CI_GATE_NAME, SNAPSHOT.ciGateName);
  assert.deepEqual([...checks.HANDOFF_CI_SUMMARY], SNAPSHOT.handoffCiBullet);
});

test("snapshot: the capture schema id and the lens prompt's opening line", () => {
  assert.equal(captureMeta.CAPTURE_META_SCHEMA, SNAPSHOT.CAPTURE_META_SCHEMA);
  assert.equal(
    panel.lensCacheKey({ diff: "x", scopeNote: "" }).split("\n")[0],
    SNAPSHOT.lensPromptFirstLine,
  );
});

test("snapshot: the bot identities each reader believes", () => {
  assert.deepEqual([...rounds.PAGE_AUTHOR_LOGINS], SNAPSHOT.PAGE_AUTHOR_LOGINS);
  assert.deepEqual([...metrics.LEDGER_AUTHOR_LOGINS], SNAPSHOT.LEDGER_AUTHOR_LOGINS);
  assert.deepEqual([...setState.LATCH_AUTHOR_LOGINS], SNAPSHOT.LATCH_AUTHOR_LOGINS);
  assert.deepEqual([...rebuttal.REBUTTAL_AUTHOR_LOGINS], SNAPSHOT.REBUTTAL_AUTHOR_LOGINS);
});

test("snapshot: the branch-protection decline message", () => {
  assert.equal(
    protection.decideProtection({ classicError: { status: 403 } }).reason,
    SNAPSHOT.protectionUnreadableReason,
  );
});

test("snapshot: which changes the fix guard calls a replicated rule, and what it says", () => {
  const paths = Object.entries(SNAPSHOT.fixGuard.replicatedRule);
  assert.ok(paths.length >= 9, `expected the recorded sample paths, found ${paths.length}`);
  for (const [file, expected] of paths) {
    assert.equal(fixGuard.crdtRuleChanges([{ filename: file }]).length === 1, expected, file);
  }
  const files = [{ filename: "pkg/document/crdt/tree.go" }];
  assert.deepEqual(
    fixGuard.lintRound({ files, prBody: "see Yorkie-JS-SDK#1" }).crdt,
    SNAPSHOT.fixGuard.lintWithBodyNamingCounterpart,
  );
  assert.deepEqual(fixGuard.lintRound({ files, prBody: "nothing" }).crdt, SNAPSHOT.fixGuard.lintWithoutIt);
  assert.equal(
    fixGuard.renderFixGuard(fixGuard.lintRound({ files, prBody: "" }), { head: "a".repeat(40), after: "b".repeat(40) }),
    SNAPSHOT.fixGuard.rendered,
  );
});

test("snapshot: what the test-removal record counts as a test, and as a case", () => {
  const { isTestFile, isRunnableTest, countCasesPatch, countCases } = SNAPSHOT.testRemovals;
  assert.ok(Object.keys(isTestFile).length >= 25, "expected the recorded sample paths");
  for (const [file, expected] of Object.entries(isTestFile)) {
    assert.equal(testRemovals.isTestFile(file), expected, `isTestFile(${file})`);
  }
  for (const [file, expected] of Object.entries(isRunnableTest)) {
    assert.equal(testRemovals.isRunnableTest(file), expected, `isRunnableTest(${file})`);
  }
  assert.ok(countCases.length >= 4, "expected the recorded patches");
  for (const { file, status, result } of countCases) {
    assert.deepEqual(testRemovals.countCases(countCasesPatch, { file, status }), result, `${file} (${status})`);
  }
});

// --- the paths that define CI ---------------------------------------------------
//
// `ci.definingPaths`, entry by entry, with the reason each is on the list. The
// harness adds the entries every repository shares (the workflows and actions,
// CODEOWNERS, this directory, the hook layer) around these; this is what THIS repository's CI
// resolves through. `config.json` cannot carry comments, so they live here, and
// the test below fails if the two lists drift apart.
//
// NO SECOND LIST TO MIRROR. Some repositories keep a CODEOWNER-ed declaration of
// "files that decide how much CI runs" for a test to compare against. This one
// has none: `ci.yml`'s own filter declares `build: '**'`, so the lint/build/
// test job is unconditional and no path can shrink it. That removes the drift
// risk and the cross-check with it, which is why the comments below are the
// only thing saying why each entry is listed.
const CI_DEFINING = [
  // The lanes themselves. `ci.yml` calls `make lint`, `make build` and a
  // `go test` line; what those resolve to lives in the Makefile and
  // `.golangci.yml`, so a branch editing either supplies part of the CI
  // definition exactly as if it had edited the workflow.
  "Makefile",
  ".golangci.yml",
  "codecov.yml",
  // The toolchain and the dependency graph CI resolves before it runs anything.
  "go.mod",
  "go.sum",
  // Codegen configuration: the `buf generate` freshness gate is only as strict
  // as the config it regenerates from.
  "buf.gen.yaml",
  "buf.work.yaml",
  "api/buf.yaml",
  "api/buf.gen.yaml",
  // The stack the integration lane is graded against.
  "build/docker/**",
  // Scripts a workflow invokes directly.
  "scripts/ci/**",
  // …and the shell scripts a Makefile target a workflow invokes resolves to.
  // `go-fix.sh` is the whole body of the `modernize` lane (`Makefile`'s
  // `verify-modernize` runs nothing else) and it also runs on every push
  // through `make verify`, so a branch editing it decides whether that lane
  // checks anything at all — the same fail-open as editing the Makefile line
  // that calls it, one file away.
  "scripts/go-fix.sh",
  // `go-build-tags.sh` prints the tag set both `make lint` and `go-fix.sh`
  // run under. A branch making it print nothing would lint and modernize the
  // default build only, with every lane still green.
  "scripts/go-build-tags.sh",
  // …and the modules those scripts import. `scripts/*.mjs` rather than
  // `scripts/verify-*.mjs`: `verify-doc-links.mjs` and `verify-license.mjs`
  // both import `direct-run.mjs` for the predicate that decides whether their
  // CLI body runs at all, so a branch editing that one file makes both of
  // docs.yml's gates exit 0 having checked nothing — the same fail-open as
  // editing the verify scripts themselves, one import away from the pattern
  // that only named them. The directory holds nothing but those scripts and
  // their shared helpers, so the wider glob costs no false refusals.
  "scripts/*.mjs",
  // …and the suite those scripts are graded by. `docs.yml` runs
  // `node --test 'scripts/test/**'`, so a branch editing a guard there changes
  // what the lane proves exactly as much as editing the script it guards.
  "scripts/test/**",
];

test("CI_DEFINING_PATHS covers the surface CI's behaviour is read from", () => {
  // Gate 1b refuses a PR that supplies any part of the CI definition.
  //
  // NO SECOND LIST TO MIRROR HERE, and that is a fact about this repository
  // rather than an omission. Upstream asserts this list is a superset of its
  // `harness.config.json` `ciConfig`, which exists because a changed path there
  // can SHRINK a CI run — so a PR editing it could grade its own homework. This
  // repository's `build` filter is `'**'` plus negations naming documentation
  // paths, and `definesCi` refuses a PR that edits `ci.yml` at all, so a branch
  // cannot widen those negations to exempt its own code; the tag-gated jobs it
  // can narrow (bench, complex-test, load-test) are not what a gate reads. The
  // property the upstream mirror protects is structurally absent, so what is
  // left to assert is that the matcher classifies the real surface.
  assert.deepEqual([...CONFIG.ci.definingPaths], CI_DEFINING, "config.json and the annotated list above have drifted");

  for (const p of [
    ".github/workflows/ci.yml",
    ".github/workflows/nested/whatever.yml",
    ".github/actions/setup/action.yml",
    ".github/CODEOWNERS",
    // The lanes: `ci.yml` runs `make lint` and `make build`, and what those do
    // lives here.
    "Makefile",
    ".golangci.yml",
    "codecov.yml",
    "go.mod",
    "go.sum",
    "buf.gen.yaml",
    "buf.work.yaml",
    "api/buf.yaml",
    "api/buf.gen.yaml",
    "build/docker/docker-compose.yml",
    "build/docker/sharding/docker-compose.yml",
    "scripts/ci/parse-bench.js",
    "scripts/verify-doc-links.mjs",
    // The module both verify scripts import their "am I the entry point?"
    // predicate from. Editing it alone makes docs.yml's two gates exit 0
    // without checking anything, so it defines CI's behaviour exactly as much
    // as the scripts that import it.
    "scripts/direct-run.mjs",
    // The entire body of ci.yml's `modernize` lane, reached through the
    // Makefile's `verify-modernize` target and through `make verify` on push.
    "scripts/go-fix.sh",
    // The tag set `make lint` and `go-fix.sh` both run under.
    "scripts/go-build-tags.sh",
  ]) {
    assert.equal(checks.definesCi(p), true, `${p} defines what CI does and must be refused by gate 1b`);
  }

  // ...and does not swallow the code under review. A gate that refuses every PR
  // is the same outage as one that refuses none, arrived at from the other side.
  for (const p of [
    "pkg/document/crdt/tree.go",
    "server/backend/database/mongo/client.go",
    "test/integration/document_test.go",
    "api/yorkie/v1/resources.proto",
    "docs/design/tree.md",
    "README.md",
  ]) {
    assert.equal(checks.definesCi(p), false, `${p} is code or prose under review, not the CI definition`);
  }
});

test("every configured path and glob matches a file this repository tracks", () => {
  // The loader can tell a glob from a non-glob, not a right glob from a wrong
  // one. `build/docker` is a valid glob that matches only a FILE of that name;
  // `*.go` is a valid glob that matches only at the root; a renamed directory
  // leaves a valid glob behind. Each matches nothing, and on these lists
  // "matches nothing" means an entry quietly off a refusal surface or out of a
  // review scope. So every entry must match at least one tracked file — or be
  // on RESERVED, with the reason it is listed ahead of the file existing.
  const RESERVED = {
    // Not present today. Listed so that CREATING it — which would change what
    // the codegen gate regenerates from — is refused and reviewed as policy.
    "api/buf.gen.yaml": ["ci.definingPaths", "review.classRules.policy"],
  };
  const tracked = execFileSync("git", ["ls-files"], { cwd: ROOT, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 })
    .split("\n")
    .filter(Boolean);
  assert.ok(tracked.length > 500, `expected the repository's files, git listed ${tracked.length}`);

  const lists = {
    "ci.definingPaths": CONFIG.ci.definingPaths,
    "review.classRules.code-adjacent": CONFIG.review.classRules["code-adjacent"],
    "review.classRules.policy": CONFIG.review.classRules.policy,
    "review.sourceGlobs": CONFIG.review.sourceGlobs,
    "fixGuard.replicatedRuleGlobs": CONFIG.fixGuard.replicatedRuleGlobs,
  };
  let checked = 0;
  const reservedSeen = [];
  for (const [key, entries] of Object.entries(lists)) {
    for (const entry of entries) {
      checked++;
      const re = compileGlob(entry);
      const matches = tracked.some((f) => re.test(f));
      const reserved = (RESERVED[entry] ?? []).includes(key);
      if (reserved) reservedSeen.push(`${key}: ${entry}`);
      assert.equal(
        matches,
        !reserved,
        reserved
          ? `${key}: ${entry} matches a tracked file now — take it off RESERVED`
          : `${key}: ${entry} matches no tracked file. A directory is \`dir/**\`, any depth is \`**/name\`; ` +
            "if the entry is deliberate, add it to RESERVED with the reason",
      );
    }
  }
  // `review.diffExcludes` are git pathspecs, so git is asked.
  for (const spec of CONFIG.review.diffExcludes) {
    checked++;
    const hit = execFileSync("git", ["ls-files", "--", spec], { cwd: ROOT, encoding: "utf8" }).trim();
    assert.ok(hit !== "", `review.diffExcludes: ${spec} matches no tracked file`);
  }
  // Guards on the guard: the lists were walked, and RESERVED holds nothing stale.
  assert.ok(checked >= 30, `expected every configured entry, checked ${checked}`);
  assert.deepEqual(reservedSeen.sort(), Object.entries(RESERVED).flatMap(([e, keys]) => keys.map((k) => `${k}: ${e}`)).sort());
  assert.equal(CONFIG.fixGuard.counterpartRepo, "yorkie-team/yorkie-js-sdk");
  // What a pull request here writes to point at the other side still counts.
  const named = fixGuard.counterpartReference(CONFIG.fixGuard.counterpartRepo);
  for (const body of ["JS side: yorkie-js-sdk#1442", "yorkie-team/yorkie-js-sdk#1442", "https://github.com/yorkie-team/yorkie-js-sdk/pull/1442"]) {
    assert.equal(named(body), true, body);
  }
  assert.equal(named("Fixes yorkie-team/yorkie#2100"), false, "this repository's own name is not the counterpart's");
});

test("this is the repository the shared files are written in", () => {
  // `upstream.json` says so with `repository: null`. A copy records the
  // repository and commit it was synced from instead, and the package's own
  // suite then holds it to that manifest; here, the manifest is whatever
  // `node scripts/agent/manifest.mjs build` last wrote, and the suite only
  // requires that it matches the tree.
  const upstream = JSON.parse(readFileSync(path.join(HERE, "upstream.json"), "utf8"));
  assert.deepEqual(upstream, { repository: null });
});

test("no checkout converts the shared files' line endings", () => {
  // The manifest is over bytes. With `core.autocrlf` on and no attribute, git
  // rewrites every line ending on checkout and each shared file then hashes
  // differently. The package cannot set this for a repository: `.gitattributes`
  // at the root is the repository's own file.
  const lines = readFileSync(path.join(ROOT, ".gitattributes"), "utf8").split("\n").map((l) => l.trim());
  for (const line of manifest.GITATTRIBUTES_LINES) assert.ok(lines.includes(line), `.gitattributes must contain \`${line}\``);
  // …and git reads it that way, where git is there to ask.
  if (!existsSync(path.join(ROOT, ".git"))) return;
  for (const rel of ["scripts/agent/config.mjs", "scripts/agent/lenses/lenses.json", ".github/workflows/agent-fix.yml"]) {
    const out = execFileSync("git", ["check-attr", "text", "--", rel], { cwd: ROOT, encoding: "utf8" });
    assert.match(out, /: text: unset\s*$/, `${rel}: ${out.trim()}`);
  }
});

// --- what the harness assumes of this repository's CI workflow --------------------------

test("CI_WORKFLOW_PATH names a workflow file that actually exists", () => {
  // The gate matches CI runs on this path instead of on the run's display name,
  // which is what makes gate 1 unforgeable — a second file cannot claim the
  // path. The cost is that a typo, or renaming ci.yml, silently makes the gate
  // unsatisfiable for every PR: the workflow-scoped query would find nothing and read as
  // "CI has not run". Assert the file is there, and that it is the one whose
  // runs are named "CI".
  const src = readFileSync(path.join(ROOT, checks.CI_WORKFLOW_PATH), "utf8"); // throws if the path is wrong
  assert.match(src, /^name:\s*CI\s*$/m, "ci.yml must still be the workflow whose runs are named CI");
});

test("no two ci.yml triggers can race for the same PR head SHA", () => {
  // THE INVARIANT `ciConclusion` RESTS ON — stated precisely, because an earlier
  // revision of this test overclaimed it as "exactly one run per head SHA".
  // That is false: closing and reopening a PR files a second `pull_request` run
  // for the same commit. Newest-wins is CORRECT there, because the later run is
  // a fresh execution of the same file at the same tree and supersedes the
  // earlier one.
  //
  // What must not happen is two runs from DIFFERENT triggers for one PR head,
  // where "newest" is arbitrary rather than superseding. `push` restricted to
  // `main` never fires for a PR branch, and a `merge_group` run carries the
  // speculative merge commit. Widen `push`, or add a trigger that fires on a PR
  // head, and this fails — making the choice conscious instead of silent.
  const yml = readFileSync(path.join(ROOT, checks.CI_WORKFLOW_PATH), "utf8");
  // To the next TOP-LEVEL key, whatever it is. Slicing to a named one (`permissions:`)
  // silently ran to end-of-file in a workflow that declares none, and the "events"
  // it then found were job names.
  const from = yml.indexOf("\non:");
  const rest = yml.slice(from + 1);
  const nextKey = rest.slice(3).search(/^[a-z_]+:/m);
  const on = rest.slice(0, nextKey < 0 ? undefined : nextKey + 3);
  const code = on.split("\n").filter((l) => !/^\s*#/.test(l)).join("\n");

  const events = [...code.matchAll(/^ {2}([a-z_]+):/gm)].map((m) => m[1]);
  assert.deepEqual(
    events.sort(),
    ["pull_request", "push"],
    "a new CI trigger may fire for a PR head SHA — see ciConclusion before adding one",
  );
  assert.match(
    code.slice(code.indexOf("push:"), code.indexOf("pull_request:")),
    /branches: \[["']?main["']?\]/,
    "push must stay restricted to main, or every PR branch push races pull_request",
  );
});

// --- this repository's own script tests --------------------------------------------------

test("every script test outside the harness that creates a git fixture uses git-env.mjs", () => {
  // The harness package checks its own tests for this (`git-env.test.mjs`).
  // THIS repository's script tests are held to the same helper by choice:
  // under a git hook, git exports GIT_DIR and GIT_INDEX_FILE, a fixture
  // repository that inherits them writes into the real one, and one test here
  // once set `core.bare` in the shared configuration that way. Another
  // repository may scrub the environment differently; that is why this
  // assertion lives here and not in the package.
  const scriptsDir = path.join(ROOT, "scripts");
  const files = [];
  const walk = (dir) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      if (e.name === "node_modules") continue;
      const p = path.join(dir, e.name);
      if (p === AGENT) continue; // the package grades itself
      if (e.isDirectory()) walk(p);
      else if (e.name.endsWith(".test.mjs")) files.push(p);
    }
  };
  walk(scriptsDir);
  assert.ok(files.length > 0, "found no script tests outside the harness — this check would be vacuous");
  const spawnsGit = /\b(?:execFileSync|spawnSync|spawn|execFile)\(\s*["']git["']/;
  const initsRepo = /["']init["']/;
  const offenders = files
    .filter((f) => {
      const src = readFileSync(f, "utf8");
      return spawnsGit.test(src) && initsRepo.test(src) && !src.includes("git-env.mjs");
    })
    .map((f) => path.relative(ROOT, f));
  assert.deepEqual(offenders, [], "use fixtureGitEnv(dir) from scripts/agent/git-env.mjs for every git call on a fixture");
});

test("ci.yml files a run for every PR, so a docs-only PR reaches the pipeline", () => {
  // THE PROPERTY THE WHOLE AUTONOMOUS ARM RESTS ON, and it is about the run
  // EXISTING, not about what runs inside it.
  //
  // `agent-review-panel.yml` and `agent-iterate-ci.yml` both trigger on
  // `workflow_run` of `ci.yml`. A workflow skipped by a trigger-level
  // `paths-ignore` files NO RUN, so there is no event, so neither fires — and a
  // PR confined to markdown sat outside the loop entirely: `@claude loop`
  // labelled it and nothing happened, `mark-ready.mjs`'s gate could never go
  // green, and `@claude fix`'s docs-only carve-out above was written for a state
  // `fix-eligible.mjs` had already refused. Moving the filter onto the `build`
  // JOB keeps the cost saving (no Go lane, no MongoDB, no `-race` suite) and
  // gives the pipeline its event back.
  //
  // Asserted here rather than left to review because it is a ONE-LINE
  // regression: re-adding `paths-ignore:` under `pull_request` restores every
  // symptom above and changes nothing a reviewer of that diff would see run.
  const yml = readFileSync(path.join(ROOT, checks.CI_WORKFLOW_PATH), "utf8");

  const on = yml.slice(yml.indexOf("\non:"), yml.indexOf("\nenv:"));
  const pr = on.slice(on.indexOf("  pull_request:"));
  assert.ok(pr.startsWith("  pull_request:"), "ci.yml must still trigger on pull_request");
  assert.ok(
    !/^ {4}paths(-ignore)?:/m.test(pr.split("\n").filter((l) => !/^\s*#/.test(l)).join("\n")),
    "ci.yml must not filter `pull_request` at the workflow level — a filtered-out PR files no run, "
    + "and the review panel and the CI-fix arm both trigger on one",
  );

  // ...and the filter that replaced it must keep failing toward RUNNING. The
  // only positive pattern is `**`, so a path nothing mentions still builds; the
  // rest are negations. A positive list here would silently skip CI on any new
  // directory, which is the failure this shape exists to make impossible.
  const step = yml.slice(yml.indexOf("id: code-changed"));
  const filters = step.slice(step.indexOf("build:"), step.indexOf("\n\n"));
  const patterns = [...filters.matchAll(/^ {14}- '(.+)'$/gm)].map((m) => m[1]);
  assert.ok(patterns.length >= 2, "could not read the build filter's patterns from ci.yml");
  assert.equal(patterns[0], "**", "the build filter's only positive pattern must be `**`");
  for (const p of patterns.slice(1)) {
    assert.ok(p.startsWith("!"), `the build filter must be \`**\` plus negations only; found '${p}'`);
  }
  // Without `every`, dorny/paths-filter ORs the patterns per file: a lone
  // `README.md` matches `**` and the filter is true, so the negations do
  // nothing and the step is a no-op that looks like a filter.
  assert.match(step.slice(0, 400), /predicate-quantifier: every/,
    "the build filter needs `predicate-quantifier: every`, or its negations are inert");
});

test("the Go test rules match where this repository keeps its tests and runs them", () => {
  // `tests.go.sharedSuiteDirs`: yorkie keeps the bodies of its database and RPC
  // suites in shared `testcases/testcases.go` files (not `_test.go`):
  // `func RunXxxTest(` with the signature on the following lines, called from
  // thin `_test.go` wrappers. A skip or a deleted subtest there is a test
  // switched off, so the record has to count those files as tests.
  assert.deepEqual([...CONFIG.tests.rules], ["go", "vitest"]);
  assert.deepEqual([...CONFIG.tests.go.sharedSuiteDirs], ["testcases"]);
  for (const f of ["server/backend/database/testcases/testcases.go", "server/rpc/testcases/testcases.go"]) {
    assert.ok(existsSync(path.join(ROOT, f)), `${f} no longer exists — is the shared-suite directory still right?`);
    assert.equal(testRemovals.isTestFile(f), true, f);
  }
  assert.equal(testRemovals.isTestFile("server/backend/database/testcase.go"), false);

  // `tests.go.ciPlatform`: what `go test ./...` skips by file name depends on
  // the platform CI runs on. The `build` job — the one that runs the suite —
  // is on a GitHub-hosted Ubuntu runner, which is linux/amd64.
  assert.deepEqual({ ...CONFIG.tests.go.ciPlatform }, { goos: "linux", goarch: "amd64" });
  const ci = readFileSync(path.join(ROOT, checks.CI_WORKFLOW_PATH), "utf8");
  const build = ci.slice(ci.indexOf("\n    name: build\n"));
  assert.match(build.slice(0, 600), /^ {4}runs-on: ubuntu-[\d.]+\s*$/m, "ci.yml's build job no longer runs on an Ubuntu runner");
});

test("ci.laneReports says what ci.yml uploads", () => {
  const ci = readFileSync(path.join(ROOT, checks.CI_WORKFLOW_PATH), "utf8");
  assert.equal(CONFIG.ci.laneReports, /^\s+name: ci-lane-reports\s*$/m.test(ci));
});

// --- file classes and lens scoping ------------------------------------------------

test("classifyFile: this repository's lane configuration is policy, its fixtures are code-adjacent", () => {
  // The lanes themselves: what golangci-lint checks, and what `make` runs, is
  // the difference between a mechanical guarantee and a claim.
  assert.equal(panel.classifyFile(".golangci.yml"), "policy");
  assert.equal(panel.classifyFile("Makefile"), "policy");
  assert.equal(panel.classifyFile("api/buf.gen.yaml"), "policy");
  // The configuration this file grades governs the agents too: it is policy
  // to the reviewers, and part of the CI definition to the ready gate, so a
  // pull request that edits it is reviewed as policy and never promotes itself.
  for (const file of ["config.json", "coverage-note.md", "repo.snapshot.json", "repo.test.mjs"]) {
    assert.equal(panel.classifyFile(`.github/agent-harness/${file}`), "policy", file);
    assert.equal(checks.definesCi(`.github/agent-harness/${file}`), true, file);
  }

  // Data that behavior depends on is read as code.
  assert.equal(panel.classifyFile("server/backend/testdata/config.json"), "code-adjacent");
  assert.equal(panel.classifyFile("test/fixtures/sample.json"), "code-adjacent");
  assert.equal(panel.classifyFile("build/docker/docker-compose.yml"), "code-adjacent");

  // Go source is code, TEST source included — a `_test.go` file is where a
  // convergence bug hides, not narration about one.
  assert.equal(panel.classifyFile("pkg/document/crdt/tree.go"), "code");
  assert.equal(panel.classifyFile("server/backend/database/mongo/client_test.go"), "code");
  assert.equal(panel.classifyFile("test/integration/document_test.go"), "code");
  assert.equal(panel.classifyFile("api/yorkie/v1/resources.proto"), "code");
  // Generated protobuf is never demoted to a cheap class.
  assert.equal(panel.classifyFile("api/yorkie/v1/resources.pb.go"), "code");
});

test("review.sourceGlobs covers this repository's source, and the path-scoped lenses run on it", () => {
  const lenses = panel.loadLenses(path.join(AGENT, "lenses"));
  const manifest = JSON.parse(readFileSync(path.join(AGENT, "lenses", "lenses.json"), "utf8"));
  const lensOf = (id) => lenses.find((l) => l.id === id);

  // Every glob names a top-level directory that exists.
  for (const glob of CONFIG.review.sourceGlobs) {
    const dir = glob.replace(/\/\*\*$/, "");
    assert.ok(statSync(path.join(ROOT, dir)).isDirectory(), `${glob}: ${dir}/ is not a directory`);
  }
  // …and every top-level directory that holds Go is named, or a new package
  // tree would be reviewed by the wildcard lenses only.
  const covered = new Set(CONFIG.review.sourceGlobs.map((g) => g.split("/")[0]));
  const hasGo = (dir, depth = 0) =>
    readdirSync(dir, { withFileTypes: true }).some((e) =>
      e.isDirectory() ? depth < 6 && e.name !== "node_modules" && hasGo(path.join(dir, e.name), depth + 1) : e.name.endsWith(".go"));
  const goDirs = readdirSync(ROOT, { withFileTypes: true })
    .filter((e) => e.isDirectory() && !e.name.startsWith(".") && hasGo(path.join(ROOT, e.name)))
    .map((e) => e.name);
  assert.ok(goDirs.length >= 5, `expected to find the Go trees, found ${goDirs.join(",")}`);
  // `build/` holds no reviewed product code, only packaging.
  for (const dir of goDirs.filter((d) => d !== "build")) {
    assert.ok(covered.has(dir), `${dir}/ holds Go source but review.sourceGlobs does not name it`);
  }

  // A code PR runs every path-scoped lens; a docs-only PR runs none of them.
  const code = ["pkg/document/crdt/tree.go"];
  for (const id of ["correctness", "security", "design-fit", "test-adequacy", "blast-radius"]) {
    assert.equal(panel.lensApplies(lensOf(id), code), true, id);
  }
  assert.equal(panel.lensApplies(lensOf("docs"), code), false);
  const reviewing = reviewScope.reviewingLensIds(manifest, code);
  for (const id of ["test-adequacy", "blast-radius"]) assert.ok(reviewing.includes(id), `${id} must review a code change`);
  assert.ok(!reviewing.includes("docs"), "the docs lens must not be required for a .go-only change");
  const docsOnly = reviewScope.reviewingLensIds(manifest, ["docs/tasks/active/20260912-x-todo.md"]);
  for (const id of ["test-adequacy", "blast-radius"]) assert.ok(!docsOnly.includes(id), id);
});

// --- the mechanical-coverage note ---------------------------------------------------
//
// EVERY CLAIM IN `coverage-note.md` WAS READ OFF THIS REPOSITORY, not assumed,
// because the failure mode is silent: tell a lens something is covered when it
// is not and that whole finding class stops being reported, with nothing in the
// output to show for it. The audit that produced its two halves:
//   - `.golangci.yml` runs gofmt AND goimports as formatters, so formatting and
//     import grouping are ENFORCED here. That is the opposite of the repository
//     the note was first written for, where Prettier is write-only — copying
//     its "no lane checks formatting" bullet would have sent every lens hunting
//     a class golangci-lint already reds.
//   - The same file once DISABLED `staticcheck` and `unused`, and the note said
//     so in the NOT-enforced half. Both are enabled now, with every build tag,
//     so the bullet moved up — the same discipline as the licence header below.
//   - The Apache 2.0 licence header CLAUDE.md requires on every Go file HAS a
//     lane as of `scripts/verify-license.mjs`: `ci.yml`'s `build` job runs it
//     and `make verify` runs it locally. It did not when the note was first
//     written — 17 files had drifted — and the bullet lived in the NOT-enforced
//     half until the lane landed. Moving it is the same discipline as the
//     formatting bullet above, in the other direction: a lens told to hunt a
//     class CI reds in seconds spends turns for nothing.
//   - `ci.yml`'s `build` job is filtered on documentation paths, so on a
//     documentation-only PR none of the lanes run at all — only `docs.yml`'s
//     link check. (The filter used to sit on ci.yml's trigger, which skipped
//     the whole run; it moved onto the job so the run still exists for the
//     pipeline to trigger on. Nothing changed about which lanes execute.) The
//     licence check loses nothing by it: it reads `.go` files, and no filtered
//     path can hold one.
//   - `complex-test`, `bench` and `load-test` are path-gated by the
//     `ci-target-check` job, so most pull requests run none of them. `build` is
//     gated only on the documentation filter above, so it runs on every pull
//     request that touches anything else.
//   - `go vet -tags rgafuzz ./...` COMPILES the tag-gated reproductions and
//     deliberately never runs them; they are expected to fail when run.

test("the coverage note claims only mechanisms this repo actually runs", () => {
  // The failure mode is SILENT: tell a lens something is covered when it is not
  // and that finding class stops being reported, with nothing in the output to
  // show for it. Each assertion below pins a fact read off THIS repository, so a
  // change to the real lane breaks this test instead of quietly making the note
  // a lie. Every one of them was re-derived when the note was ported — the
  // upstream assertions pinned a pnpm monorepo's lanes and would have passed
  // here while describing a repository that does not exist.
  const N = panel.MECHANICAL_COVERAGE_NOTE;
  assert.equal(N, readFileSync(path.join(HERE, "coverage-note.md"), "utf8").replace(/\n$/, ""));
  assert.equal(N.split("\n")[0], "## What the mechanical lanes cover on this branch");
  assert.ok(N.indexOf("ENFORCED") >= 0 && N.indexOf("NOT ENFORCED") > N.indexOf("ENFORCED"), "the note needs both halves, in order");
  // The same anti-clamp rules `review-panel.test.mjs` applies to the rubrics: a
  // note that says "don't report X" is exactly the shape a clamp hides in.
  for (const clamp of [/when unsure,?\s+downgrade/i, /mark it minor/i, /only with (?:a )?concrete/i, /ONLY for a\s+concrete/i, /severity ONLY/i]) {
    assert.ok(!clamp.test(N), `coverage-note.md re-introduces a certainty clamp: ${clamp}`);
  }
  const enforced = N.slice(N.indexOf("ENFORCED"), N.indexOf("NOT ENFORCED"));
  const notEnforced = N.slice(N.indexOf("NOT ENFORCED"));

  // FORMATTING IS CHECKED HERE, and this is the assertion most likely to be
  // copied wrong: .golangci.yml enables gofmt and goimports as formatters, so
  // unlike the repository this note came from, a lens may rely on them.
  assert.match(enforced, /gofmt and goimports as formatters/);
  assert.ok(!/write-only|no lane checks formatting/i.test(N),
    "golangci-lint formats here — do not carry over the upstream formatting gap");

  // staticcheck and unused are ENABLED in .golangci.yml, with every build tag.
  // This was the other way round until they were turned on, and like the
  // licence header it is pinned in both directions: a note that still listed
  // them as unenforced would send every lens hunting a class lint reds.
  const lintStart = enforced.indexOf("golangci-lint run");
  // FOUND FIRST, then read. A reworded bullet would make indexOf return -1 and
  // the slice below check some other text, passing having checked nothing.
  assert.ok(lintStart >= 0, "the note no longer has a `golangci-lint run` bullet to check");
  const lintBullet = enforced.slice(lintStart, enforced.indexOf("\n- ", lintStart));
  assert.match(lintBullet, /staticcheck/);
  assert.match(lintBullet, /unused/);
  assert.match(lintBullet, /every build tag/);
  assert.doesNotMatch(notEnforced, /`staticcheck` and `unused`/);

  // The licence header HAS a lane — `scripts/verify-license.mjs`, run by
  // ci.yml's `build` job. This assertion was the other way round until that
  // lane landed, and it is pinned in both directions on purpose: the note's
  // whole failure mode is a half that has stopped being true, and a guard
  // that only checks the NOT-enforced half cannot catch a claim that moved.
  assert.match(enforced, /verify-license\.mjs/);
  assert.doesNotMatch(notEnforced, /Apache 2\.0 licence header/);

  // The tag-gated suites are path-gated, so most PRs run none of them. Only the
  // `build` job is unconditional.
  assert.match(notEnforced, /`go test -tags complex`/);
  assert.match(notEnforced, /path-gated/);
  const suiteLine = N.split("\n").find((l) => l.includes("-tags integration -race"));
  assert.ok(suiteLine, "the note no longer claims the `-tags integration -race` lane");
  // MongoDB must be named ON THAT BULLET, not merely somewhere in the note —
  // the earlier `/MongoDB/.test(N)` was satisfied by any other mention.
  assert.match(
    `${suiteLine}\n${N.split("\n")[N.split("\n").indexOf(suiteLine) + 1] ?? ""}`,
    /MongoDB/,
    `the suite claim must name what it runs against: ${suiteLine}`,
  );

  // A docs-only PR runs NONE of it. This is the gap most likely to be missed,
  // because every other bullet reads as unconditional.
  assert.match(notEnforced, /documentation-only PR/);

  // `-race` is on, and the note must not over-claim it: it observes executions.
  assert.match(enforced, /race detector is on/);
  assert.match(notEnforced, /observes executions, not/);

  // MECHANISMS, NEVER CATEGORIES. "type problems" and friends also cover the
  // things the named tool accepts; named tools and named lanes only.
  for (const category of [/\btype problems\b/i, /\btype issues\b/i, /\blint issues are\b/i, /\bstyle problems\b/i]) {
    assert.ok(!category.test(N), `the note must name a mechanism, not a category: ${category}`);
  }

  // The tense claim. The panel runs CONCURRENTLY with CI, so nothing here is
  // proven at the moment a lens reads it — only that the lens is not the last
  // line of defence. "already caught" would be false for the current round.
  assert.ok(!/already (?:caught|checked|passed|proven)/i.test(N),
    "the panel runs alongside CI — the note may not claim these have already passed");
  assert.match(N, /must pass before it can be promoted/);
});

// --- the bots ------------------------------------------------------------------------

test("the bots the workflows believe are the ones the scripts believe", () => {
  // The gate, throttle and page steps used to carry a literal copy of the
  // allow-list each, pinned here to `repo.botLogins`. They read it from the
  // configuration now (`workflow-values.mjs`, `bot_logins`), so what is left to
  // pin is that the workflows' list and the scripts' list are one list, and
  // that the App the fixer jobs allow is the App the readers believe.
  const value = (name) => workflowValues.values([name])[0][1];
  assert.deepEqual(JSON.parse(value("bot_logins")), [...rounds.PAGE_AUTHOR_LOGINS]);
  assert.deepEqual(JSON.parse(value("bot_logins")), ["github-actions[bot]", "yorkie-team-agent[bot]"]);
  assert.equal(value("app_slug"), "yorkie-team-agent");
  assert.ok(CONFIG.repo.appLogins.includes(value("app_bot_login")));
  assert.equal(value("allowed_bots"), "yorkie-team-agent[bot],yorkie-team-agent");
});

// --- what the workflows say, in this repository's words ---------------------------------
//
// The `agent-*.yml` files carry no command, path or name of this repository's:
// they read each from the configuration through `workflow-values.mjs`. These
// tests are where the result is pinned for THIS repository.

/** Collapse runs of whitespace: a prompt's line wrapping is not its text. */
const flat = (text) => text.replace(/\s+/g, " ").trim();

/** `text` with every configured-value expression replaced by this repository's value. */
function rendered(text) {
  const all = Object.fromEntries(workflowValues.values(Object.keys(workflowValues.VALUES)));
  return text.replace(/\$\{\{\s*(?:steps\.cfg|needs\.[\w-]+)\.outputs\.([a-z_]+)\s*\}\}/g, (whole, name) =>
    Object.hasOwn(all, name) ? all[name] : whole);
}

/** Every `prompt: |` block of a workflow, in order. */
function promptBlocks(text) {
  const lines = text.split("\n");
  const blocks = [];
  lines.forEach((line, i) => {
    const m = /^(\s+)prompt: \|\s*$/.exec(line);
    if (!m) return;
    const body = [];
    for (const next of lines.slice(i + 1)) {
      if (next.trim() !== "" && /^(\s*)/.exec(next)[1].length <= m[1].length) break;
      body.push(next);
    }
    blocks.push(body.join("\n"));
  });
  return blocks;
}

/**
 * The issue-to-PR prompt, which is a heredoc in a `run:` step. Configured
 * values reach it as environment variables substituted for `__PLACEHOLDER__`s,
 * so the same substitutions are made here: each quoted `TEMPLATE=` line, with
 * its variable resolved through the step's `env:`.
 */
function heredocPrompt(text) {
  const start = text.indexOf("<<'PROMPT'\n");
  const end = text.indexOf("\n          PROMPT\n", start);
  assert.ok(start > 0 && end > start, "agent-implement.yml no longer builds its prompt in a `PROMPT` heredoc");
  let prompt = text.slice(start + "<<'PROMPT'\n".length, end);
  // No configured value may be spliced into the script itself.
  assert.ok(!prompt.includes("${{"), "the issue-to-PR heredoc interpolates an expression; configured values must arrive through env");
  // The step that builds the prompt, and nothing after it.
  const stepAt = text.lastIndexOf("\n      - name: ", start);
  const stepEnd = text.indexOf("\n      - name: ", start);
  const step = text.slice(stepAt, stepEnd < 0 ? undefined : stepEnd);
  const env = new Map([...step.matchAll(ENV_FROM_CONFIG)].map((m) => [m[1], m[2]]));
  let substituted = 0;
  for (const m of step.matchAll(QUOTED_SUBSTITUTION)) {
    assert.ok(env.has(m[2]), `agent-implement.yml substitutes ${m[1]} with a variable its env does not set from the configuration`);
    prompt = prompt.replaceAll(m[1], env.get(m[2]));
    substituted++;
  }
  assert.equal(substituted, env.size, "every configured value in the step's env must be substituted into the prompt, quoted");
  assert.ok(substituted >= 5, `expected the configured placeholders, found ${substituted}`);
  return [prompt];
}
/** `NAME: ${{ steps.cfg.outputs.value }}` in a step's `env:`. */
const ENV_FROM_CONFIG = /^\s+([A-Z_]+): (\$\{\{ steps\.cfg\.outputs\.[a-z_]+ \}\})\s*$/gm;
/** A placeholder replaced by a QUOTED variable: unquoted, bash 5.2 expands `&` in it. */
const QUOTED_SUBSTITUTION = /^\s+TEMPLATE=\$\{TEMPLATE\/\/(__[A-Z_]+__)\/"\$([A-Z_]+)"\}\s*$/gm;

test("snapshot: every value the workflows read from the configuration", () => {
  const names = Object.keys(workflowValues.VALUES);
  assert.deepEqual(Object.fromEntries(workflowValues.values(names)), SNAPSHOT.WORKFLOW_VALUES);
});

test("snapshot: the prompts the agents are given, as rendered for this repository", () => {
  // Recorded from the prompts as they stood while every command and path in
  // them was a literal, with two deliberate differences (the fix, panel-fix
  // and CI-fix prompts gained the generated-code rule, and the first two name
  // the `modernize` lane as the CI-fix prompt already did). Line wrapping is
  // not compared: a configured value arrives as one line.
  //
  // After that it is a change detector, like the rest of the recording: a
  // prompt edit, here or in a workflow, shows its before and after in the diff
  // of `repo.snapshot.json`.
  const sites = [
    ["agent-fix.yml", promptBlocks],
    ["agent-review-panel.yml", promptBlocks],
    ["agent-iterate-ci.yml", promptBlocks],
    ["agent-review-reply.yml", promptBlocks],
    ["agent-summarize.yml", promptBlocks],
    ["agent-implement.yml", heredocPrompt],
  ];
  const got = {};
  for (const [file, extract] of sites) {
    const blocks = extract(readFileSync(path.join(WORKFLOWS, file), "utf8"));
    assert.ok(blocks.length > 0, `${file}: no prompt found`);
    blocks.forEach((block, i) => {
      const text = flat(rendered(block));
      // Nothing configured may be left unrendered, and nothing may render to a hole.
      assert.ok(!/(?:steps\.cfg|needs\.route)\.outputs\./.test(text), `${file}: a configured value was not rendered`);
      got[`${file}#${i}`] = text;
    });
  }
  assert.deepEqual(got, SNAPSHOT.WORKFLOW_PROMPTS);
});

test("the toolchain action pins what the Makefile's `tools` target installs", () => {
  // The action exists so that a token-bearing job never runs `make tools` from
  // a branch. The price is a second copy of the linter version, and a copy
  // that goes stale shows up as the fixer and CI disagreeing about lint.
  const action = readFileSync(path.join(ROOT, ".github", "actions", "agent-toolchain", "action.yml"), "utf8");
  const code = action.split("\n").filter((l) => !/^\s*#/.test(l)).join("\n");
  const PIN = /go install (github\.com\/golangci\/golangci-lint\/v2\/cmd\/golangci-lint@v[0-9.]+)/;
  const pinned = PIN.exec(code);
  assert.ok(pinned, "the toolchain action must install the linter at a pinned version");
  const makefile = readFileSync(path.join(ROOT, "Makefile"), "utf8");
  assert.ok(makefile.includes(`go install ${pinned[1]}\n`), `the Makefile's tools target does not install ${pinned[1]}`);
  // It must not be the thing it replaces, and it must save no cache: a post
  // step would store what the agent left behind under a key the agent controls.
  assert.ok(!/\bmake\s+tools\b/.test(code), "the toolchain action runs the working tree's `make tools`");
  assert.match(code, /uses: actions\/setup-go@v\d+\n\s+with:\n\s+go-version: "(\d+\.\d+)"\n\s+cache: false\n/);
  const goVersion = /go-version: "(\d+\.\d+)"/.exec(code)[1];
  assert.match(readFileSync(path.join(ROOT, "go.mod"), "utf8"), new RegExp(`^go ${goVersion.replace(".", "\\.")}(?:\\.\\d+)?$`, "m"),
    "the toolchain action's Go version is not go.mod's");
  // And it is governed like the workflows that call it.
  assert.equal(checks.definesCi(".github/actions/agent-toolchain/action.yml"), true);
  assert.equal(panel.classifyFile(".github/actions/agent-toolchain/action.yml"), "policy");
});

test("the commands the fixer prompts name are targets this repository has", () => {
  const makefile = readFileSync(path.join(ROOT, "Makefile"), "utf8");
  const target = (name) => new RegExp(`^${name}:`, "m").test(makefile);
  // `fixer.verifyCommand` is the whole of what a fixer verifies with: lint, the
  // licence headers, `go fix`, the unit tests. A hand-written subset would let
  // a fixer push something CI then refuses.
  assert.equal(CONFIG.fixer.verifyCommand, "make verify");
  assert.ok(target("verify"));
  assert.match(makefile, /^verify: lint verify-license verify-modernize\b/m);
  // Every `make <target>` any configured sentence names exists.
  const named = new Set();
  for (const value of Object.values(CONFIG.fixer)) {
    for (const m of String(value).matchAll(/`make ([a-z-]+)`/g)) named.add(m[1]);
  }
  assert.ok(named.size >= 8, `expected the make targets the prompts name, found ${[...named].join(", ")}`);
  for (const name of named) assert.ok(target(name), `a fixer prompt names \`make ${name}\`, which the Makefile does not have`);
  // The reproducer convention is the one `test-removals.mjs` records for Go.
  assert.match(CONFIG.fixer.keepReproducerAs, /`t\.Skip\("still reproduces: <finding>"\)`/);
  assert.match(CONFIG.fixer.disabledTestForms, /`t\.Skip`\/`t\.Skipf`\/`t\.SkipNow`/);
  // Not configured, on purpose: a review reply names no verify command here,
  // the todo carries no tracking line, and the CI-fix prompt says nothing
  // more about a failing integration lane than that the push re-runs it.
  assert.equal(CONFIG.fixer.replyVerifyInstruction, "");
  assert.equal(CONFIG.fixer.todoTrackingLine, "");
  assert.equal(CONFIG.fixer.ciFailureNote, "");
  // `fixer.commitNote` states the convention and claims no hook: a fresh
  // checkout on a runner has `core.hooksPath` unset, so `.githooks/commit-msg`
  // is not active there, and the harness then points the path at its own hook.
  assert.match(CONFIG.fixer.commitNote, /subject <= 70 characters, a blank line, body wrapped at 80/);
  assert.ok(!/hook/i.test(CONFIG.fixer.commitNote), "the commit note must not claim a hook checks the message on a runner");
  assert.ok(existsSync(path.join(ROOT, ".githooks", "commit-msg")), "the convention the note states is the one .githooks/commit-msg enforces locally");
});

test("a release the agent App created publishes nothing", () => {
  // `contents: write` IS ONE PERMISSION COVERING COMMITS AND RELEASES. The token
  // a fix agent holds to push a branch can therefore publish a release, and
  // `docker-publish.yml` answers `release: published` with `secrets: inherit` —
  // Docker Hub credentials. GitHub offers no narrower grant, so the escape
  // closes at the consumer or not at all.
  const dir = WORKFLOWS;
  // The App's `[bot]` login, from this repository's configuration.
  const appBot = CONFIG.repo.appLogins.find((l) => l.endsWith("[bot]"));
  assert.ok(appBot, "repo.appLogins names no [bot] login");
  const onRelease = readdirSync(dir)
    .filter((f) => f.endsWith(".yml"))
    .filter((f) => {
      const code = readFileSync(path.join(dir, f), "utf8").split("\n").filter((l) => !/^\s*#/.test(l));
      const on = code.slice(0, code.findIndex((l) => /^jobs:/.test(l)));
      return on.some((l) => /^\s+release:\s*$/.test(l));
    });
  assert.ok(onRelease.length > 0, "no release-triggered workflow found — this guard would be vacuous");
  for (const file of onRelease) {
    const wf = readFileSync(path.join(dir, file), "utf8");
    assert.ok(
      wf.includes(`github.event.release.author.login != '${appBot}'`),
      `${file} runs on a published release and would run for one the agent App created`,
    );
  }
  // THE SAME CREDENTIALS, REACHED THROUGH A MERGE. `contents: write` also merges
  // a PR a human already approved, which is a push to `main` authored by the
  // App — and a push-triggered workflow that inherits secrets answers it the
  // same way. Every such workflow must refuse the App as the actor.
  const onPushInherit = readdirSync(dir)
    .filter((f) => f.endsWith(".yml"))
    .filter((f) => {
      const code = readFileSync(path.join(dir, f), "utf8").split("\n").filter((l) => !/^\s*#/.test(l));
      const on = code.slice(0, code.findIndex((l) => /^jobs:/.test(l)));
      return on.some((l) => /^\s+push:\s*$/.test(l)) && code.some((l) => /^\s+secrets: inherit\s*$/.test(l));
    });
  assert.ok(onPushInherit.includes("docker-publish-latest.yml"), "the push-triggered publisher must be found");
  for (const file of onPushInherit) {
    assert.ok(
      readFileSync(path.join(dir, file), "utf8").includes(`github.actor != '${appBot}'`),
      `${file} inherits secrets on a push and would run for one the agent App made by merging`,
    );
  }
});
