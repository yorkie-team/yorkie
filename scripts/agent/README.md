# scripts/agent

The `@claude` command surface: the verb router, the review-panel orchestrator
and its lenses, and the credential, state, metrics and comment helpers that the
`agent-*.yml` workflows call.

Everything in this directory is meant to be the same bytes in every repository
that runs the harness. What differs per repository lives in
`.github/agent-harness/` and is read only through `config.mjs`.

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
| `repo.name` | `review-panel.mjs` (lens prompt), `capture-meta.mjs` (schema id) | wired |
| `repo.botLogins` | `rounds.mjs`, `metrics.mjs`, `set-state.mjs` | wired |
| `repo.appLogins` | `rebuttal.mjs` | wired |
| `docs.commandVerbsRef` | `branch-protection.mjs`, `workflow-presence.mjs` | wired |
| `ci.definingPaths` | `checks.mjs` | wired |
| `ci.gateLabel`, `ci.handoffSummary` | `checks.mjs`, for `mark-ready.mjs` | wired |
| `ci.laneReports` | — | not wired yet |
| `review.classRules.code-adjacent`, `review.classRules.policy` | `review-panel.mjs` | wired |
| `review.sourceGlobs` | `lenses/lenses.json`, as `$config:review.sourceGlobs` | wired |
| `review.sharedPrimitivesNote` | `lenses/design-fit.md`, as `{{config:review.sharedPrimitivesNote}}` | wired |
| `review.diffExcludes` | — | not wired yet |
| `fixer.verifyCommand`, `fixer.verifyDescription`, `fixer.forbiddenCommands`, `fixer.targetedTestHint`, `fixer.generatedCodeNote`, `fixer.keepReproducerAs`, `fixer.licenceScope`, `fixer.todoTrackingLine` | — | not wired yet |
| `tests.rules`, `tests.go.*` | `test-removals.mjs`, `fix-guard.mjs` | wired |
| `fixGuard.replicatedRuleGlobs`, `fixGuard.replicatedRuleNotice`, `fixGuard.counterpartRepo` | `fix-guard.mjs` | wired |
| `redact.extraPatterns` | — | not wired yet |
| `ask.permittedMcpTools` | — | not wired yet |

The twelve keys marked "not wired yet" are required by the schema and read by
nothing. They record the values the workflow prompts and diff commands still
carry as literals; the workflows are to read them from here in a later change.
Until then, editing one changes nothing.

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
- A composite action loaded from a pull request's tree (`uses: ./pr/...`).

Workflow files are CI-defining and CODEOWNER-ed, so every edit to one still
needs a human reviewer. The list above is what that reviewer has to look for,
because the guard will not.
