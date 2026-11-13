# scripts/agent

The `@claude` command surface: the verb router, the review-panel orchestrator
and its lenses, and the credential, state, metrics and comment helpers that the
`agent-*.yml` workflows call.

Everything in this directory, and every `.github/workflows/agent-*.yml`, is
meant to be the same bytes in every repository that runs the harness. What
differs per repository lives in two places: `.github/agent-harness/`, read
only through `config.mjs`, and one composite action,
`.github/actions/agent-toolchain`.

## Configuration

`.github/agent-harness/` holds three things:

| File | What it is |
|---|---|
| `config.json` | Every repository-specific value. Validated on load: a missing key, an unknown key or a value of the wrong shape throws. |
| `coverage-note.md` | What the mechanical lanes prove, told to every lens. Loaded as `review.mechanicalCoverageNote`. |
| `repo.test.mjs` | Grades the two files above against this repository (`npm run test:repo`). |

`config.mjs` finds the directory beside the checkout it is running from
(`<root>/scripts/agent` reads `<root>/.github/agent-harness`). A copy of the
package anywhere else has no repository beside it and must be told where the
configuration is with `AGENT_HARNESS_CONFIG_DIR`, as an absolute path. There
are no defaults: a script that cannot load the configuration exits non-zero.

`node config.mjs get <dotted.key>` prints one value (a string raw, anything
else as JSON).

### Keys

| Key | Read by | Status |
|---|---|---|
| `repo.name` | `review-panel.mjs` (lens prompt), `capture-meta.mjs` (schema id), the issue-to-PR prompt | wired |
| `repo.appSlug` | workflows: decline messages, `allowed_bots`, the App's own marker | wired |
| `repo.botLogins` | `rounds.mjs`, `metrics.mjs`, `set-state.mjs`; workflows: the gate, throttle and page allow-lists | wired |
| `repo.appLogins` | `rebuttal.mjs` | wired |
| `docs.commandVerbsRef` | `branch-protection.mjs`, `workflow-presence.mjs`; workflows: decline messages | wired |
| `ci.definingPaths` | `checks.mjs` | wired |
| `ci.gateLabel`, `ci.handoffSummary` | `checks.mjs`, for `mark-ready.mjs` | wired |
| `ci.laneReports` | `agent-iterate-ci.yml`: whether the CI-fix arm downloads and renders lane reports before the log tail | wired |
| `review.classRules.code-adjacent`, `review.classRules.policy` | `review-panel.mjs` | wired |
| `review.sourceGlobs` | `lenses/lenses.json`, as `$config:review.sourceGlobs` | wired |
| `review.sharedPrimitivesNote` | `lenses/design-fit.md`, as `{{config:review.sharedPrimitivesNote}}` | wired |
| `review.diffExcludes` | the three reviewed-diff steps (panel, panel delta, on-demand) | wired |
| `fixer.verifyCommand`, `fixer.verifyDescription`, `fixer.verifyFailureHint`, `fixer.integrationWarning`, `fixer.integrationRerunBy`, `fixer.keepReproducerAs`, `fixer.disabledTestForms` | the fix, panel-fix and CI-fix prompts | wired |
| `fixer.generatedCodeNote` | those three prompts and the issue-to-PR prompt | wired |
| `fixer.licenceScope`, `fixer.todoTrackingLine`, `fixer.implementVerifyStep` | the issue-to-PR prompt | wired |
| `fixer.replyVerifyInstruction` | the review-reply prompt | wired |
| `fixer.toolchainSteps` | the issue-to-PR job's "stopped before the agent" report | wired |
| `fixer.lastResortDiagnosis` | the CI-fix arm, when no diagnosis can be read | wired |
| `tests.rules`, `tests.go.*` | `test-removals.mjs`, `fix-guard.mjs` | wired |
| `fixGuard.replicatedRuleGlobs`, `fixGuard.replicatedRuleNotice`, `fixGuard.counterpartRepo` | `fix-guard.mjs` | wired |
| `redact.extraPatterns` | — | not wired yet |
| `ask.permittedMcpTools` | — | not wired yet |

The two keys marked "not wired yet" are required by the schema and read by
nothing; editing one changes nothing. `repo.appSlug` must agree with the
logins: `<slug>[bot]` has to be in both `repo.appLogins` and `repo.botLogins`.

### How a workflow reads a value

Through `workflow-values.mjs`, and no other way. A trusted step asks for values
by name and they become its outputs:

```yaml
- name: Read the harness configuration
  id: cfg
  run: node ./scripts/agent/workflow-values.mjs verify_command docs_ref >> "$GITHUB_OUTPUT"
```

A later step writes `${{ steps.cfg.outputs.verify_command }}`. A job with no
checkout of its own takes the value from its `route` job (the panel: its
`gate` job), which reads it this way and declares it as a job output.

The names and what each renders are the `VALUES` table in that file. Three
rules hold it together:

- A name the module does not know, or a required value that is blank, exits
  non-zero. The step is never `continue-on-error`.
- A value that may be empty (`OPTIONAL` in that file) is rendered with the
  punctuation that joins it to its sentence, so the workflow writes it against
  the previous word and the sentence reads whole whether or not the repository
  said anything. `diff_pathspecs` is never empty — it always starts with `.` —
  so a diff step that finds it empty fails instead of diffing unfiltered.
- `workflow-values.test.mjs` reads the workflows and fails on an
  `outputs.<name>` that the step it names never asked for, on a read that a
  skipped reader would leave empty, and on a value asked for and never read.

`.github/agent-harness/repo.snapshot.json` records every value and every
rendered prompt for this repository, so a change to either shows its before
and after in review.

### The toolchain action

`.github/actions/agent-toolchain/action.yml` installs what an agent needs to
verify a change in this repository. It is the only per-repository step the
five agent jobs run, and the only local action a workflow may use. It runs
with a token in the environment and, in four of the five jobs, a pull
request's branch in the workspace, so:

- it pins every version it installs and runs nothing from the working tree;
- it saves no cache;
- those four jobs check `main`'s copy out into `.trusted-toolchain` and use it
  from there, never from the workspace. The directory stays until the job ends
  (the runner re-reads a local action before its post steps) and is listed in
  `.git/info/exclude`. The issue-to-PR job uses it from the workspace, which is
  still `main` at that step.

### Globs

One dialect, for every glob the harness matches a path against:

- `**` as a whole path segment is any number of directories, including none:
  `**/x` matches `x` and `a/b/x`, and `a/**` matches everything under `a/`.
- `*` inside a segment is any run of characters that is not a `/`.
- Everything else is literal. Paths are repository-relative.

A glob is anchored at both ends, which has two consequences worth knowing:

- `dir` matches only a path that is exactly `dir`. For what is under a
  directory, write `dir/**`.
- `*.ext` matches only at the repository root. For any depth, write
  `**/*.ext`.

Both are valid globs, so the loader cannot refuse them. A repository's
`repo.test.mjs` should require every configured entry to match a tracked file,
which is what catches them.

A path segment may hold at most two `*`.

Nothing else is supported, and the loader refuses it rather than reading it
literally: `{a,b}`, `!negation`, `?`, `[...]`, a leading `./` or `/`, and a
trailing `/` would all match no path. `review.diffExcludes` is the exception —
those are git pathspecs, handed to `git diff`.

In `lenses/lenses.json`, an `appliesWhen` entry `$config:<key>` stands for that
list. In a rubric, `{{config:<key>}}` stands for that string. Both throw when
the key is missing, empty or of the wrong type.

## Conventions the core assumes

These are written into the scripts and workflows rather than configured. A
repository that adopts the harness follows them:

- The default branch is `main`. Workflows check trusted code out with
  `ref: main`, and the scripts' base-branch arguments default to it.
- The repository is public. Two jobs that hold no `contents` permission (the
  issue-to-PR job, and the panel's `gate`) check `main` out with the ambient
  token, which can read a public repository and nothing else.
- The CI workflow is `.github/workflows/ci.yml`, named `CI`. The ready gate
  reads that file's runs, and the panel and the CI-fix arm trigger on it.
- `.github/workflows/**`, `.github/actions/**`, `.github/CODEOWNERS`,
  `.github/agent-harness/**`, `scripts/setup.sh`, `scripts/hooks/**` and
  `.githooks/**` define CI. A pull request touching any of them is never
  promoted automatically.
- `CLAUDE.md`, `AGENTS.md`, `CONTRIBUTING.md`, `MAINTAINING.md`,
  `scripts/agent/lenses/*.md`, `.github/**` and `.claude/**` are policy: they
  are reviewed as instructions to people and agents, not as prose.
- `docs/design/**` is the design contract (with `TEMPLATE.md` and `README.md`
  in it); the rest of `docs/**`, top-level `*.md` and `*.txt`, and any
  `README.md` are prose.
- `scripts/hooks/**` and `.githooks/**` are the local enforcement layer that
  `scripts/setup.sh` installs.
- This package lives at `scripts/agent`, and nothing in it imports from
  outside it: workflows check it out or copy it without its parent directory.
  `command.mjs` imports nothing at all, because route jobs check it out alone.

## Tests

```sh
cd scripts/agent
npm ci
npm test            # the package, against fixtures/harness-config
npm run test:repo   # this repository's values in .github/agent-harness
```

`npm test` sets `AGENT_HARNESS_CONFIG_DIR` to the fixture configuration for
every test process, and each test that reaches `config.mjs` also imports
`./fixtures/use-fixture-config.mjs` first so that running one file on its own
grades the same thing.

## The workflow trust guard

`config-wiring.test.mjs` walks every job of every `agent-*.yml` step by step.
It tracks which trees exist (the workspace, side-directory checkouts, the
staged copy) and whether each is still the default branch's, and it fails if a
harness script would run from a tree that is not, or would read the
configuration from somewhere other than beside that tree. It counts the sites
it inspects, so a change in workflow shape that hides them fails too.

It refuses rather than follows: a script run by a relative path in a step with
`working-directory:` or after a `cd`, a script reached through `npm run`, and
`AGENT_HARNESS_CONFIG_DIR` or `NODE_OPTIONS` set at workflow, job or step
level.

It treats a local action the same way as a script: `uses: ./…` is allowed for
`.github/actions/agent-toolchain` alone, from a tree that is trusted at that
step. A checkout with no `ref:` is trusted only in a workflow that cannot be
started by `workflow_dispatch` or a pull request event.

### What this guard does not see

It reads workflow text for the shapes the workflows use today. It exists to
catch a well-meaning edit that quietly moves a script or its configuration
onto an untrusted tree. It is not a defence against someone trying to get
around it, and these pass it:

- Writes into a trusted tree or the staged copy by anything other than the
  recognised `cp -R` staging lines: `cp -a`, `rsync`, `tar -x`,
  `actions/download-artifact`, a redirect into a script's path.
- The dependency tarball a `deps` job builds and a later job unpacks beside
  the trusted scripts. The hand-off between jobs is not followed.
- A script run through a shell variable, `xargs`, `make`, or any interpreter
  other than a literal `node <path>`.
- YAML in a form the line reader does not expect: quoted keys (`"ref":`,
  `"uses":`), flow-style mappings, anchors.
- What the toolchain action does once it runs. Where its file came from is
  checked; the actions it nests and the commands it runs are not.

Workflow files are CI-defining and CODEOWNER-ed, so every edit to one still
needs a human reviewer. The list above is what that reviewer has to look for,
because the guard will not.
