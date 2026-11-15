// No trusted job that holds a secret or a write token restores an Actions
// cache (trusted-cache.mjs says why). The last test runs the check over this
// repository; the rest pin what the check refuses and what it lets through.

import "./fixtures/use-fixture-config.mjs";

import { test } from "node:test";
import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { defaultRoot } from "./manifest.mjs";
import { checkWorkflows, findTrustedCacheRestores, onlyOnPullRequest, workflowFiles } from "./trusted-cache.mjs";

// Third-party packages are loaded dynamically in this directory (ask.test.mjs).
const { parse, stringify } = await import("yaml");

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SHA = "0123456789abcdef0123456789abcdef01234567";

/** The check over in-memory files: `{ "repo/relative/path": text }`. */
function check(files) {
  return checkWorkflows({
    workflowPaths: Object.keys(files).filter((f) => f.startsWith(".github/workflows/")).sort(),
    readText: (rel) => files[rel] ?? null,
    parse,
  });
}

/** One workflow, one job. `job` is merged into the job; omitted permissions stay undeclared. */
function workflow({ on = { push: { branches: ["main"] } }, permissions, env, job = {} } = {}) {
  return stringify({
    on,
    ...(permissions !== undefined && { permissions }),
    ...(env !== undefined && { env }),
    jobs: { build: { "runs-on": "ubuntu-latest", ...job } },
  });
}

/** The findings for a single step in an otherwise default job: push to main, no permissions declared. */
function oneStep(step, opts = {}) {
  return check({ ".github/workflows/w.yml": workflow({ ...opts, job: { ...opts.job, steps: [step] } }) });
}

const SETUP_GO = { uses: "actions/setup-go@v6", with: { "go-version": "1.26" } };

test("every way of restoring a cache is refused in a trusted job with a write token", () => {
  const cases = [
    [{ uses: "actions/cache@v4", with: { path: "~/x", key: "k" } }, /actions\/cache restores/],
    [{ uses: "actions/cache/restore@v4", with: { path: "~/x", key: "k" } }, /actions\/cache\/restore restores/],
    [SETUP_GO, /setup-go .*cache: false/],
    [{ uses: `actions/setup-go@${SHA}`, with: { "go-version": "1.26" } }, /setup-go/],
    [{ uses: "actions/setup-go@v3", with: { cache: true } }, /setup-go/],
    [{ uses: "actions/setup-node@v4", with: { cache: "pnpm" } }, /setup-node restores/],
    [{ uses: "actions/setup-node@v5", with: { "node-version": "22" } }, /package-manager-cache: false/],
    [{ uses: "actions/setup-python@v5", with: { cache: "pip" } }, /setup-python restores/],
    [{ uses: "actions/setup-java@v4", with: { cache: "gradle" } }, /setup-java restores/],
    [{ uses: "ruby/setup-ruby@v1", with: { "bundler-cache": true } }, /bundler-cache/],
    [{ uses: "pnpm/action-setup@v4", with: { cache: true } }, /pnpm\/action-setup restores/],
    [{ uses: "Swatinem/rust-cache@v2" }, /rust-cache/],
    [{ uses: "docker/build-push-action@v6", with: { "cache-from": "type=gha" } }, /type=gha/],
    [{ uses: "docker/build-push-action@v6", with: { "cache-from": "type=registry,ref=r\ntype=gha,scope=a\n" } }, /type=gha/],
    [{ uses: "docker/bake-action@v5", with: { set: "*.cache-from=type=gha\n*.cache-to=type=gha" } }, /type=gha/],
    [{ run: "docker buildx build --cache-from type=gha --push ." }, /--cache-from type=gha/],
    [{ uses: "docker/setup-buildx-action@v3" }, /cache-binary: false/],
    [{ uses: "docker/setup-qemu-action@v3" }, /cache-image: false/],
    [{ uses: "oven-sh/setup-bun@v2", with: { "bun-version": "1.3.14" } }, /no-cache: true/],
    [{ uses: "anthropics/claude-code-action@v1", with: { prompt: "p" } }, /path_to_bun_executable/],
    // An expression may evaluate to on, so it counts as on.
    [{ uses: "actions/setup-go@v6", with: { cache: "${{ inputs.cache }}" } }, /setup-go/],
  ];
  for (const [step, why] of cases) {
    const found = oneStep(step);
    assert.equal(found.length, 1, `${JSON.stringify(step)}: ${JSON.stringify(found)}`);
    assert.match(found[0], why, JSON.stringify(step));
    assert.match(found[0], /^\.github\/workflows\/w\.yml › job build › step /);
  }
  // sccache reads the Actions cache only when its backend is switched on, in any env the step sees.
  const sccache = { uses: "mozilla-actions/sccache-action@v0.0.9" };
  assert.equal(oneStep(sccache).length, 0);
  assert.equal(oneStep(sccache, { job: { env: { SCCACHE_GHA_ENABLED: "true" } } }).length, 1);
  assert.equal(oneStep(sccache, { env: { SCCACHE_GHA_ENABLED: "true" } }).length, 1);
  assert.equal(
    check({
      ".github/workflows/w.yml": workflow({
        job: { steps: [{ run: 'echo "SCCACHE_GHA_ENABLED=true" >> "$GITHUB_ENV"' }, sccache] },
      }),
    }).length,
    1,
  );
});

test("a restore switched off, or a step that only saves, is accepted", () => {
  const off = [
    { ...SETUP_GO, with: { ...SETUP_GO.with, cache: false } },
    { ...SETUP_GO, with: { ...SETUP_GO.with, cache: "false" } },
    { uses: "actions/setup-go@v3", with: { "go-version": "1.20" } }, // off by default before v4
    { uses: "actions/setup-node@v4", with: { "node-version": "22" } },
    { uses: "actions/setup-node@v5", with: { "package-manager-cache": false } },
    { uses: "actions/setup-python@v5", with: { "python-version": "3.12" } },
    { uses: "pnpm/action-setup@v4", with: { version: 9 } },
    { uses: "actions/cache@v4", with: { path: "x", key: "k", "lookup-only": true } },
    { uses: "actions/cache/save@v4", with: { path: "x", key: "k" } },
    { uses: "docker/build-push-action@v6", with: { "cache-to": "type=gha,mode=max" } },
    { uses: "docker/setup-buildx-action@v3", with: { "cache-binary": false } },
    { uses: "docker/setup-qemu-action@v3", with: { "cache-image": false } },
    { uses: "oven-sh/setup-bun@v2", with: { "no-cache": true } },
    { uses: "anthropics/claude-code-action@v1", with: { path_to_bun_executable: "${{ steps.bun.outputs.bun-path }}" } },
    // Pull-request-only values evaluate to off on every trusted event.
    { uses: "actions/setup-node@v4", with: { cache: "${{ github.event_name == 'pull_request' && 'pnpm' || '' }}" } },
    {
      uses: "actions/setup-node@v4",
      with: { cache: "${{ (github.event_name == 'pull_request' || github.event_name == 'merge_group') && 'pnpm' || '' }}" },
    },
    { ...SETUP_GO, with: { ...SETUP_GO.with, cache: "${{ github.event_name == 'pull_request' }}" } },
  ];
  for (const step of off) assert.deepEqual(oneStep(step), [], JSON.stringify(step));

  // A commit pin is read at the version its trailing comment names.
  const pinned = (comment) =>
    check({
      ".github/workflows/w.yml": [
        "on: push",
        "jobs:",
        "  build:",
        "    runs-on: ubuntu-latest",
        "    steps:",
        `      - uses: actions/setup-node@${SHA}${comment}`,
      ].join("\n"),
    });
  assert.deepEqual(pinned(" # v4.4.0"), []);
  assert.equal(pinned(" # v5.0.0").length, 1);
  assert.equal(pinned("").length, 1, "an unknown version is read as one that caches");
});

test("a job is privileged by a secret, by any write scope, or by declaring no permissions", () => {
  const read = { contents: "read" };
  const refused = [
    {}, // nothing declared: the repository default may be write-all
    { permissions: "write-all" },
    { permissions: { contents: "read", "id-token": "write" } },
    { permissions: read, job: { permissions: { contents: "write" } } },
    { permissions: read, job: { env: { T: "${{ secrets.NPM_TOKEN }}" } } },
    { permissions: read, env: { T: "${{ secrets.NPM_TOKEN }}" } },
    { permissions: read, job: { env: { T: "${{ secrets[format('TOKEN_{0}', 1)] }}" } } },
  ];
  for (const opts of refused) assert.equal(oneStep(SETUP_GO, opts).length, 1, JSON.stringify(opts));

  // A secret referenced by the restoring step itself counts as well.
  assert.equal(oneStep({ ...SETUP_GO, env: { T: "${{ secrets.X }}" } }, { permissions: read }).length, 1);

  const accepted = [
    { permissions: read },
    { permissions: {} },
    { permissions: "read-all" },
    { permissions: { contents: "write" }, job: { permissions: read } },
    // The job token is judged by its permissions, not as a secret.
    { permissions: read, job: { env: { GH_TOKEN: "${{ secrets.GITHUB_TOKEN }}" } } },
  ];
  for (const opts of accepted) assert.deepEqual(oneStep(SETUP_GO, opts), [], JSON.stringify(opts));
});

test("every trigger but a pull request's own is trusted", () => {
  for (const on of ["push", "release", "workflow_run", "schedule", "workflow_dispatch", "issue_comment", "pull_request_target", "workflow_call", "repository_dispatch"]) {
    assert.equal(oneStep(SETUP_GO, { on }).length, 1, on);
  }
  for (const on of ["pull_request", "pull_request_review", "pull_request_review_comment", "merge_group"]) {
    assert.deepEqual(oneStep(SETUP_GO, { on }), [], on);
  }
  assert.equal(oneStep(SETUP_GO, { on: ["pull_request", "push"] }).length, 1, "one trusted trigger is enough");
  assert.deepEqual(oneStep(SETUP_GO, { on: ["pull_request", "merge_group"] }), []);
});

test("a job or step that runs only on a pull request's own event is accepted", () => {
  const on = ["push", "pull_request"];
  const prOnly = "github.event_name == 'pull_request'";
  assert.deepEqual(oneStep(SETUP_GO, { on, job: { if: prOnly } }), []);
  assert.deepEqual(oneStep(SETUP_GO, { on, job: { if: `\${{ ${prOnly} && github.event.pull_request.draft == false }}` } }), []);
  assert.deepEqual(oneStep({ ...SETUP_GO, if: prOnly }, { on }), []);
  // Any other step condition may hold on the push.
  assert.equal(oneStep({ ...SETUP_GO, if: "github.ref == 'refs/heads/main'" }, { on }).length, 1);
  assert.equal(oneStep({ ...SETUP_GO, if: "always()" }, { on }).length, 1);
  // Anything else may run on the push.
  assert.equal(oneStep(SETUP_GO, { on, job: { if: `github.ref == 'refs/heads/main' || ${prOnly}` } }).length, 1);
  assert.equal(oneStep(SETUP_GO, { on, job: { if: "github.event_name != 'push'" } }).length, 1);
  assert.equal(oneStep(SETUP_GO, { on, job: { if: "always()" } }).length, 1);

  assert.equal(onlyOnPullRequest("(github.event_name == 'pull_request' || github.event_name == 'merge_group') && x"), true);
  assert.equal(onlyOnPullRequest("github.event_name == 'pull_request' || github.event_name == 'push'"), false);
  assert.equal(onlyOnPullRequest("(github.event_name == 'pull_request') || (x)"), false);
  assert.equal(onlyOnPullRequest(undefined), false);
});

test("a local reusable workflow is checked in its callers' trigger, secrets and token", () => {
  const callee = (job = {}) =>
    stringify({ on: { workflow_call: {} }, jobs: { publish: { "runs-on": "ubuntu-latest", steps: [SETUP_GO], ...job } } });
  const caller = (on, call) =>
    stringify({ on, jobs: { call: { uses: "./.github/workflows/base.yml", ...call } } });

  // The docker-publish shape: a trusted caller passing every secret.
  const found = check({
    ".github/workflows/base.yml": callee({ env: { U: "${{ secrets.DOCKER_USERNAME }}" } }),
    ".github/workflows/release.yml": caller({ release: { types: ["published"] } }, { secrets: "inherit" }),
  });
  assert.equal(found.length, 1);
  assert.match(found[0], /^\.github\/workflows\/release\.yml#call → \.github\/workflows\/base\.yml › job publish › step /);

  // Called only from a pull request's workflow: accepted.
  assert.deepEqual(
    check({
      ".github/workflows/base.yml": callee(),
      ".github/workflows/pr.yml": caller("pull_request", { secrets: "inherit" }),
    }),
    [],
  );
  // A read-only caller that passes no secrets: the callee holds nothing.
  assert.deepEqual(
    check({
      ".github/workflows/base.yml": callee({ env: { U: "${{ secrets.DOCKER_USERNAME }}" } }),
      ".github/workflows/push.yml": caller("push", { permissions: { contents: "read" } }),
    }),
    [],
  );
  // The callee cannot widen its caller's read-only token.
  assert.deepEqual(
    check({
      ".github/workflows/base.yml": callee({ permissions: { contents: "write" } }),
      ".github/workflows/push.yml": caller("push", { permissions: { contents: "read" } }),
    }),
    [],
  );
  // With no caller in the repository, it is judged as callable from anywhere.
  assert.equal(check({ ".github/workflows/base.yml": callee() }).length, 1);
  // A caller pointing at a file that is not there cannot be checked.
  assert.match(check({ ".github/workflows/push.yml": caller("push", {}) })[0], /cannot be read/);

  // Called by its full name: a file this tree has is read as this repository's,
  // even when a pull-request-only caller also reaches it by `./`.
  const full = (on, ref) => stringify({ on, jobs: { call: { uses: ref, secrets: "inherit" } } });
  const byName = check({
    ".github/workflows/base.yml": stringify({
      on: { workflow_call: {} },
      jobs: { publish: { "runs-on": "ubuntu-latest", steps: [{ uses: "actions/cache@v4", with: { path: "x", key: "k" } }] } },
    }),
    ".github/workflows/pr.yml": caller("pull_request", { secrets: "inherit" }),
    ".github/workflows/push.yml": full("push", "yorkie-team/yorkie/.github/workflows/base.yml@main"),
  });
  assert.equal(byName.length, 1, JSON.stringify(byName));
  assert.match(byName[0], /^\.github\/workflows\/push\.yml#call → \.github\/workflows\/base\.yml › job publish › /);
  // Another repository's workflow, with no file of that name here: not visible, not an error.
  assert.deepEqual(check({ ".github/workflows/push.yml": full("push", "other/repo/.github/workflows/nope.yml@v1") }), []);

  // A workflow in its own call chain is reported, not followed forever.
  const cycle = check({
    ".github/workflows/self.yml": stringify({
      on: { push: {}, workflow_call: {} },
      jobs: { again: { uses: "./.github/workflows/self.yml", secrets: "inherit" } },
    }),
  });
  assert.ok(cycle.length > 0 && cycle.every((f) => /already in its call chain/.test(f)), JSON.stringify(cycle));
});

test("a local composite action's steps are checked as the job's own", () => {
  const composite = (steps) => stringify({ name: "x", runs: { using: "composite", steps } });
  const job = (uses) => workflow({ job: { steps: [{ uses: "actions/checkout@v4" }, { name: "toolchain", uses }] } });

  const found = check({
    ".github/actions/tc/action.yml": composite([SETUP_GO]),
    ".github/workflows/w.yml": job("./.github/actions/tc"),
  });
  assert.equal(found.length, 1);
  assert.match(found[0], /step "toolchain" → \.github\/actions\/tc\/action\.yml › step "actions\/setup-go@v6": /);

  // Nested, and `action.yaml`.
  assert.equal(
    check({
      ".github/actions/outer/action.yml": composite([{ uses: "./.github/actions/inner" }]),
      ".github/actions/inner/action.yaml": composite([{ uses: "Swatinem/rust-cache@v2" }]),
      ".github/workflows/w.yml": job("./.github/actions/outer"),
    }).length,
    1,
  );
  // A checkout of this repository under a subdirectory reads this tree's copy.
  const sub = check({
    ".github/actions/tc/action.yml": composite([SETUP_GO]),
    ".github/workflows/w.yml": job("./.trusted-toolchain/.github/actions/tc"),
  });
  assert.equal(sub.length, 1);
  assert.match(sub[0], /→ \.github\/actions\/tc\/action\.yml › step "actions\/setup-go@v6": actions\/setup-go restores/);
  // Switched off inside the action: accepted.
  assert.deepEqual(
    check({
      ".github/actions/tc/action.yml": composite([{ ...SETUP_GO, with: { cache: false } }]),
      ".github/workflows/w.yml": job("./.github/actions/tc"),
    }),
    [],
  );
  // An action that cannot be read cannot be cleared.
  assert.match(check({ ".github/workflows/w.yml": job("./.github/actions/missing") })[0], /cannot be read/);
  // A composite that calls itself is read once.
  assert.deepEqual(
    check({
      ".github/actions/loop/action.yml": composite([{ uses: "./.github/actions/loop" }]),
      ".github/workflows/w.yml": job("./.github/actions/loop"),
    }),
    [],
  );
});

test("this repository's trusted, privileged jobs restore no Actions cache", async () => {
  const root = defaultRoot(HERE);
  assert.ok(workflowFiles(root).length > 0, "expected to find the workflows");
  assert.deepEqual(await findTrustedCacheRestores(root), []);
});
