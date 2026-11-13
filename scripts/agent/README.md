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

`.github/agent-harness/` holds four things:

| File | What it is |
|---|---|
| `config.json` | Every repository-specific value. Validated on load: a missing key, an unknown key or a value of the wrong shape throws. |
| `coverage-note.md` | What the mechanical lanes prove, told to every lens. Loaded as `review.mechanicalCoverageNote`. |
| `repo.test.mjs` | Grades the two files above, and the toolchain action, against the repository (`npm run test:repo`). |
| `upstream.json` | Where the shared files came from: `{ "repository": null }` in the repository they are written in, and the repository, commit and manifest digest in one that copied them. See "Adopting and syncing". |

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
| `docs.commandVerbsRef` | `branch-protection.mjs`, `workflow-presence.mjs`; workflows: decline messages, where a path is shown in a code span and an `http(s)` URL is left bare so it stays a link | wired |
| `ci.definingPaths` | `checks.mjs` | wired |
| `ci.gateLabel`, `ci.handoffSummary` | `checks.mjs`, for `mark-ready.mjs` | wired |
| `ci.laneReports` | `agent-iterate-ci.yml`: whether the CI-fix arm downloads and renders lane reports before the log tail | wired |
| `review.classRules.code-adjacent`, `review.classRules.policy` | `review-panel.mjs` | wired |
| `review.sourceGlobs` | `lenses/lenses.json`, as `$config:review.sourceGlobs` | wired |
| `review.sharedPrimitivesNote` | `lenses/design-fit.md`, as `{{config:review.sharedPrimitivesNote}}` | wired |
| `review.diffExcludes` | the three reviewed-diff steps (panel, panel delta, on-demand) | wired |
| `fixer.*` | the agents' prompts and two messages — see "What the prompts say" below | wired |
| `tests.rules`, `tests.go.*` | `test-removals.mjs`, `fix-guard.mjs` | wired |
| `fixGuard.replicatedRuleGlobs`, `fixGuard.replicatedRuleNotice`, `fixGuard.counterpartRepo` | `fix-guard.mjs`. `counterpartRepo` is `owner/name`; a PR body silences the warning by referring to it as `owner/name`, `owner/name#12`, `name#12` or a github.com link, never by merely containing the name | wired |
| `redact.extraPatterns` | — | not wired yet |
| `ask.permittedMcpTools` | — | not wired yet |

The two keys marked "not wired yet" are required by the schema and read by
nothing; editing one changes nothing. `repo.appSlug` must agree with the
logins: `<slug>[bot]` has to be in both `repo.appLogins` and `repo.botLogins`.

`tests.rules` chooses from the rule sets that exist, `go` and `vitest`.
Another test framework needs a rule set written in `test-removals.mjs` first;
naming one here does not create it.

### What the prompts say

Each `fixer.*` value is dropped into a sentence a workflow owns. The table
shows the sentence, with the value in angle brackets, so a configuration can
be written from it without reading the workflows. Two shapes, checked on load:

- a **fragment** continues the workflow's sentence: one line, no leading
  punctuation, no trailing `.`, `,`, `;`, `:`, `!` or `?`;
- **sentences** stand on their own: one line, starting with a capital, a
  digit, a backtick or a quote, and ending with `.`, `!` or `?`.

A key marked optional may be `""`, and what the table shows in square
brackets is then left out whole.

| Key | Shape | Where it lands | This repository's value |
|---|---|---|---|
| `verifyCommand` | fragment | fix, panel-fix, CI-fix: "Run `<verifyCommand>` ONCE at the end to confirm green — not after every edit." | `make verify` |
| `verifyDescription` | fragment | the next sentence: "It is `<verifyDescription>`, which is the whole of what this job can verify." | `` `make lint`, the Apache licence-header check, the `go fix` check and `go test ./...` `` |
| `verifyFailureHint` | sentences, optional | straight after: "…can verify.[ `<verifyFailureHint>`]" | If the `go fix` check or the `modernize` lane fails, run `make modernize` rather than editing the idioms by hand. |
| `integrationWarning`, `integrationRerunBy` | fragments, optional as a pair | straight after: "[ `<integrationWarning>`, and the push below re-runs `<integrationRerunBy>` anyway.]" ("the push" in the CI-fix prompt) | Do NOT run `make test`: it needs a MongoDB this job does not stand up / the full integration lane |
| `ciFailureNote` | sentences, optional | CI-fix only, straight after that: "[ `<ciFailureNote>`]" | empty |
| `generatedCodeNote` | sentences, optional | fix, panel-fix, CI-fix: a list item of its own, "[- `<generatedCodeNote>`]". Issue-to-PR: opens the last paragraph | Generated protobuf under api/yorkie/v1/ cannot be changed from this run: hand-editing it is forbidden, and `make proto` needs buf and two protoc plugins that are not installed here. |
| `apiChangeNote` | sentences, optional | issue-to-PR, the last paragraph: "[`<generatedCodeNote>` `<apiChangeNote>`]" — either alone is still a paragraph | If the issue requires an API change, say so on the issue and stop rather than working around it. |
| `keepReproducerAs` | fragment | fix, panel-fix: "If a test you wrote (or found) still fails, the finding is not fixed. `<keepReproducerAs>`, and report that item `--skipped` with the reason." | Go has no `it.fails`, so keep the test and make its first statement `t.Skip("still reproduces: <finding>")`, with a comment naming the finding |
| `disabledTestForms` | fragment | the same item: "The pipeline records every test a fix round removes or `<disabledTestForms>`, and shows it to the next round's adjudicator…" | switches off with `t.Skip`/`t.Skipf`/`t.SkipNow` |
| `commitNote` | sentences, optional | fix, panel-fix, CI-fix, after the push rules: "…revert the listed files and skip or dispute that finding.[ `<commitNote>`]". Say what this repository asks of a commit message. Do not say a hook checks it unless one is active in a fresh checkout: the harness points `core.hooksPath` at its own scope hook | Commit messages follow this repository's convention: subject <= 70 characters, a blank line, body wrapped at 80. |
| `licenceScope` | sentences, optional | issue-to-PR: "3. IMPLEMENT: make the change.[ `<licenceScope>`] Commit subject <= 70 chars…" | Apache 2.0 licence header on every new Go file; every package needs a package comment. |
| `todoTrackingLine` | sentences, optional | issue-to-PR: "The todo starts with a `**Created**: YYYY-MM-DD` line.[ `<todoTrackingLine>`]" | empty |
| `implementVerifyStep` | sentences | issue-to-PR, the whole of step 5: "5. `<implementVerifyStep>`". Say what to run, what not to run, and that CI runs the rest | LET CI VERIFY — do NOT run `make test`, `make test-complex`, `make bench` or `make coverage`: … |
| `replyVerifyInstruction` | fragment, optional | review-reply: "Act on it if it is right: edit the branch,[ `<replyVerifyInstruction>`,] commit with the trailer…" | empty |
| `toolchainSteps` | fragment | the issue-to-PR job's report: "This run stopped after setup and before the agent started — `<toolchainSteps>`, or fetching the issue." | the Go toolchain, the linter install |
| `lastResortDiagnosis` | sentences | CI-fix, when nothing could be read: "No failed-step log could be read for run N. `<lastResortDiagnosis>`" | Re-run the failing lane locally: `make verify` (…), then `make build`. |

The three issue-to-PR values may use that prompt's own placeholders, which
are filled in after them: `__ISSUE_NUMBER__`, `__TODAY__` (`YYYYMMDD`),
`__ISSUE_FILE__` and `__PR_FILE__`. A tracking line that names the issue is
written `` `Tracked as #__ISSUE_NUMBER__` ``.

A repository on another toolchain fills the same frames. With cargo, say:
`verifyCommand` is `cargo fmt --check && cargo clippy -- -D warnings && cargo
test` (or the one script that runs them), `verifyDescription` is "the format
check, clippy with warnings denied and the test suite", and the integration
pair is empty if no suite needs a service.

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

- it executes nothing the branch wrote: no build file's install target, no
  package manager's lifecycle scripts, hooks or branch-supplied settings;
- the tools it fetches itself are pinned in the action. Where the toolchain
  has to install the branch's dependencies, it installs exactly what the
  lockfile names, with scripts off;
- it saves no cache;
- those four jobs clear `.trusted-toolchain`, check `main`'s copy out into it
  and use it from there, never from the workspace. The directory stays until
  the job ends (the runner re-reads a local action before its post steps) and
  is listed in `.git/info/exclude`. The issue-to-PR job uses it from the
  workspace, which is still `main` at that step.

The workflows cannot check the first three: the action is a different file in
every repository. Each repository's `repo.test.mjs` pins them for its own
action. In yorkie, for example, it pins the linter version to the Makefile's
`tools` target, the Go version to `go.mod`, `cache: false`, and that the
action never runs `make tools`. `agent-scripts.yml` runs weekly as well as on
change, because the files such a test reads (there, the Makefile and
`go.mod`) are not among the paths that start it.

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
- The repository is public. The issue-to-PR job holds no `contents`
  permission and checks `main` out with the ambient token, which can read a
  public repository and nothing else. A private repository would have to
  grant that job `contents: read`.
- `agent-scripts.yml` triggers on the harness's own paths and weekly. It
  cannot trigger on a repository's lane files (a `paths:` filter is not
  configurable), so drift between the toolchain action — and anything it
  ships, such as a copied `.npmrc` — and the files only `repo.test.mjs` reads
  is found within a week, not on the pull request that causes it.
- The fix-scope pre-push hook is advisory. It is installed in
  `$RUNNER_TEMP/agent-hooks` with `core.hooksPath` pointed at it, which
  replaces whatever hooks the checkout would otherwise run, and the agent can
  point the path elsewhere. The report job's scope check is the gate.
- CI's lane reports, where `ci.laneReports` is true, are an artifact named
  `ci-lane-reports` rendered by `scripts/ci/summarize-ci.mjs`.
- The issue-to-PR prompt names `CLAUDE.md`, `CONTRIBUTING.md`, `docs/tasks/`,
  `docs/design/TEMPLATE.md` and `.github/PULL_REQUEST_TEMPLATE.md`.
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

## Adopting and syncing

The shared half — the core — is every file in this directory and every
`.github/workflows/agent-*.yml`. It is written in one repository and copied,
unchanged, into the others. `MANIFEST.sha256` lists it: one `<sha256>  <path>`
line per file.

A repository that runs the harness owns exactly these, and a sync never
touches them (bar the last, which it writes):

| File | |
|---|---|
| `.github/agent-harness/config.json` | the values |
| `.github/agent-harness/coverage-note.md` | what CI proves |
| `.github/agent-harness/repo.test.mjs`, and whatever it records | the tests of the two above and of the toolchain action |
| `.github/actions/agent-toolchain/` | the toolchain action and anything it ships |
| `.github/agent-harness/upstream.json` | which repository and commit the core was copied from |

To adopt the harness, or to take a newer core, from a local checkout of the
repository the core is written in:

```sh
node scripts/agent/sync.mjs --from ../path/to/upstream-checkout
# first adoption, when there is no copy to run it from:
node ../path/to/upstream-checkout/scripts/agent/sync.mjs --from ../path/to/upstream-checkout --to .
```

A copy is synced from the upstream's default branch, and only from it. The
commit a sync records is looked up there by the copy's CI, which refuses one
that is not on that branch — a pull request's head exists in the upstream
repository whether or not it was ever merged. So `sync.mjs` refuses a source
whose `HEAD` is not contained in its `origin/<default branch>` as last
fetched (`git fetch origin` first). `--allow-unmerged` lets a local trial
through and warns that the copy's CI will fail until it is synced again.

It checks everything before it writes anything, and refuses when:

- the source's tree does not match the source's manifest, has uncommitted
  changes under the core, is itself a copy, or is off its default branch;
- the destination has, under the core, a modified or staged file, a file git
  does not track (ignored ones count: git could not give it back), or a
  symbolic link or other non-regular file at or on the way to any path it
  would write.

Then it writes every file the manifest lists, deletes the tracked core files
the source no longer has, writes `upstream.json` (the source's repository,
its `HEAD`, the manifest's digest), and runs the new core's configuration
loader against the existing `config.json`, printing each key the new schema
requires and each it no longer knows. Only a filesystem error can stop it
part-way; every file touched was tracked and unmodified, so
`git restore --staged --worktree -- scripts/agent .github/workflows` undoes
it. What it leaves to a person, and prints:

1. the configuration edits, and the toolchain action on a first adoption;
2. the upstream pin below;
3. `git add scripts/agent .github/workflows .github/agent-harness`, and only
   then `npm ci && npm test && npm run test:repo` in `scripts/agent` — the
   suite compares the manifest with what git tracks, so it fails on files
   that were copied and not yet added.

**Required in every copy's `repo.test.mjs`: pin the upstream.** The core
cannot know which repository is the origin. A copy whose `upstream.json` was
edited to `{ "repository": null }` would pass all three checks below, because
that is what the origin's says. Only the repository's own test can refuse it:

```js
test("the shared files are a copy of owner/name", () => {
  const upstream = JSON.parse(readFileSync(path.join(HERE, "upstream.json"), "utf8"));
  assert.equal(upstream.repository, "owner/name");
});
```

The origin pins `{ repository: null }` the same way.

**Required in every repository's root `.gitattributes`:**

```
scripts/agent/** -text
.github/workflows/agent-*.yml -text
```

The manifest is over bytes, and a checkout with `core.autocrlf` on would
otherwise rewrite every line ending. `sync.mjs` warns when they are missing;
`manifest.mjs verify` names line endings when that is what changed. File
modes are not in the manifest: no shared file is executable, and each is run
through `node`.

Three checks keep a copy honest. Each catches what the one before cannot:

| Check | Where it runs | What it catches |
|---|---|---|
| The tree matches `MANIFEST.sha256` | `npm test` (`manifest.test.mjs`); `node manifest.mjs verify` | A core file edited, added or removed by hand. It names the path. Regenerating the manifest gets past it |
| The manifest's digest is the one `upstream.json` records | `npm test`, in a copy only | A regenerated manifest. Getting past it takes an edit to `upstream.json`, which is visible in review and is a CI-defining path |
| The recorded commit is on the recorded repository's default branch, and the manifest there is the recorded one | `agent-scripts.yml`, on change and weekly (`npm run check:upstream`) | An `upstream.json` edited to agree with a drifted copy, or pointed at a commit upstream never merged. It fails when upstream cannot be asked, after retrying a transient refusal. It also warns, without failing, when upstream's default branch has a newer core |

In the repository the core is written in, `upstream.json` is
`{ "repository": null }`, only the first check applies, and a change to the
core is finished with `node scripts/agent/manifest.mjs build`. The third
check then prints which repository is claiming to be the origin, so a copy
that makes the claim is at least visible in its log.

"Upstream has moved on" is a warning annotation on a run that is otherwise
green. Nobody is notified of it: it is seen by whoever opens that run. A
repository that wants to be told when it falls behind needs a reminder of its
own; the harness does not send one.

Which files are the core is decided by rule, not by asking git, because the
first check has to work in a tree with no `.git`: everything under
`scripts/agent` except the manifest, anything inside a `node_modules`, and
anything dot-named. So an untracked file left in this directory fails the
first check as "added".

The third check uses the job's read-only token against `api.github.com`. That
reads a public upstream. A private one would need a credential the workflow
does not take.

## Tests

```sh
cd scripts/agent
npm ci
npm test              # the package, against fixtures/harness-config
npm run test:repo     # the repository's values in .github/agent-harness
npm run check:upstream  # the copy against the repository it names (network)
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
started by `workflow_dispatch` or a pull request event. A checkout into a side
directory of a workspace that holds a pull request's tree must be preceded by
an `rm -rf` of that path: the branch could have committed a symlink there.

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
- What happens to `.trusted-toolchain` after the agent starts. The agent can
  rewrite the action's file before the job's post phase. The post steps that
  run were recorded when the action first ran, and an agent with a shell can
  already rewrite the runner's own action cache, so this is accepted rather
  than closed.

Workflow files are CI-defining and CODEOWNER-ed, so every edit to one still
needs a human reviewer. The list above is what that reviewer has to look for,
because the guard will not.
