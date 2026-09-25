**Created**: 2026-09-25

# The index's silences: a coverage gate, a design-doc home, and the direct-run boundary

Three changes that share one theme — a rule this repository states but nothing
checks.

## Motivation

`scripts/verify-doc-links.mjs` walks the documentation graph and fails on a
link that resolves to nothing. Its own header names what it deliberately does
not answer:

> It does not demand that every .md be reachable. Coverage — "was this file
> ever introduced to anyone?" — is the opposite question […] This file is
> about the index's claims; coverage is about its silences.

Nothing answers it. A design document can land, be linked from nowhere, and
every check in the repository stays green.

Two further gaps in the same area:

- The local enforcement layer merged in #2040 — git hooks, Claude Code hooks,
  the per-clone snapshot installer, `make verify`, the licence gate — has no
  design-doc home. Its trust model lives in source comments and in a task
  record that #2053 archived. `docs/design/README.md`'s Development section
  has one entry.
- `scripts/direct-run.mjs` exists because a plain string comparison makes a
  script invoked through a symlinked path exit 0 having checked nothing. Three
  files import it; twenty-two files under `scripts/agent/` carry the raw
  idiom, four of them in a spelling that is wrong for a second reason.

## Approach

### 1. `scripts/verify-doc-index.mjs`

Two checks, kept separate in the code and in the header because they catch
different failures and carry different weight.

**Coverage (gating).** Every `docs/design/*.md` is a link target of
`docs/design/README.md`; every top-level entry in `scripts/` is named in
`scripts/README.md`. Two areas, not "every .md in the repository" — an
unrooted sweep would need an exception list for the hundreds of archived task
records, which is the rot `verify-doc-links.mjs` refuses for the same reason.

**Staleness (reported, non-gating).** A todo in `docs/tasks/active/` with no
unchecked box left is a finished task nobody archived. This is the check
that would have caught the four records #2053 cleaned up. Coverage would not
have: the index and the filesystem agreed, and both said "active".

It does not gate, and the reason is that it would fire on the last commit of
every task that ever runs it. `CLAUDE.md` step 6 archives *before merge*,
after CI is green — so a todo with every box checked sitting in `active/` is
the normal state of a PR that just finished its work. A gate that refuses
that is a gate that gets bypassed by reflex.

### 2. `docs/design/local-enforcement-layer.md`

Written from the archived task record, `scripts/setup.sh`,
`scripts/hooks/install.mjs` and `.githooks/*`. The load-bearing claim: both
hook systems install from a `$GIT_DIR` snapshot rather than from the worktree,
because a tracked hook is code the branch supplies. Cost: staleness.

This doc is check 1's first customer.

### 3. The `direct-run` boundary

Investigate before converting. If `scripts/agent/*.mjs` can import
`../direct-run.mjs`, convert; if it cannot, say why and pin the rule.

## Checklist

- [x] `scripts/verify-doc-index.mjs` — coverage (`collectFindings`) and
      staleness (`collectStaleTasks`), separated.
- [x] `scripts/test/verify-doc-index.test.mjs` — planted trees under the OS
      temp dir, per the sibling suites.
- [x] Fix the one pre-existing coverage finding: `scripts/agent/` is not named
      in `scripts/README.md`.
- [x] Wire it into `.github/workflows/docs.yml` and pin that step in
      `harness-hooks.test.mjs`.
- [x] Row in `scripts/README.md` for the new script.
- [x] `docs/design/local-enforcement-layer.md` + its row in
      `docs/design/README.md`.
- [x] Settle the `scripts/agent/` → `scripts/direct-run.mjs` import question
      with evidence, not assertion.
- [x] Prove the gate by breaking what it guards, both halves, and restore.

## Review

**Check 1 found one thing on its first run**, which is the right number for a
gate written against a tree somebody already tidied: `scripts/agent/` — the
largest directory under `scripts/`, 40-odd modules and the whole `@claude`
pipeline — had no row in `scripts/README.md`. The three sibling directories
(`test/`, `hooks/`, `ci/`) all had one. Fixed rather than excepted.

The matcher is stricter than a substring for exactly that reason. `scripts/
README.md` mentions `$GIT_DIR/agent-hooks/` and `agent-review-panel.yml`, so a
naive search for `agent` passes on a README that never mentions the directory.
The rule is: the entry name, spelled as `name/` for a directory, not preceded
by a word character or a hyphen. `agent-hooks/` does not satisfy `agent/`;
`.githooks/` does not satisfy `hooks/`; `scripts/agent/` does.

The two areas use different evidence, deliberately. `docs/design/README.md` is
a navigation surface — a reader clicks through — so a design doc counts as
indexed only if the README *links* it, computed with `verify-doc-links.mjs`'s
own `linkTargets` so a filename inside a fence or inline code does not count.
`scripts/README.md` is a reference table whose entries are written as inline
code (`` `setup.sh` ``); requiring links there would mean linking eleven
scripts for no reader benefit, and running `linkTargets` over it would delete
exactly the form every entry is written in.

**Check 2 is green on this tree** and was expected to be: #2053 archived the
four records the day before. It was proved by planting a completed todo, not
by waiting for one.

**Intent 3 reversed the brief's direction, on evidence.** `scripts/agent/`
cannot import `../direct-run.mjs`. Every agent workflow stages the package in
one of two ways, and both of them drop it:

- `sparse-checkout: scripts/agent` with `sparse-checkout-cone-mode: false` —
  set explicitly in all 19 checkout steps across 10 workflows. Verified
  against real git: with cone mode off, the pattern matches `scripts/agent/**`
  and nothing else. `scripts/direct-run.mjs` is not written to disk. (With
  cone mode *on* it would be, which is how the claim looks true from memory.)
- `cp -R scripts/agent "$RUNNER_TEMP/agent-tools"` — four workflows copy the
  directory out from under its parent entirely.

Six workflows go further and sparse-check-out `scripts/agent/command.mjs`
alone. `command.mjs` today has zero relative imports; adding one would turn
every `@claude` comment into `ERR_MODULE_NOT_FOUND`, and the router's failure
is silent at the workflow level — an empty `command=` output reads as "no verb
here".

So the boundary is not an npm-packaging nicety, it is a deployment fact. The
precedent the brief cites points the other way and is safe for the reason it
is safe: `scripts/test/harness-hooks.test.mjs` imports `../agent/git-env.mjs`,
and a test only ever runs from a full checkout.

What was done instead:

- The four deviant guards (`command.mjs`, `pick-credential.mjs`,
  `pick-fix-credential.mjs`, `review-surface.mjs`) were normalised to the
  spelling the other eighteen use. Three of them compared
  `` import.meta.url === `file://${process.argv[1]}` ``, which is a real
  defect independent of any of this: a clone under `~/My Projects/` gives a
  `file://` URL with `%20` where `argv[1]` has a space, the comparison fails,
  and the CLI silently does not run. `install.mjs`'s `shellQuote` header
  records the same class of bug one layer over.
- The invariant is now pinned in `scripts/agent/checks.test.mjs`: no
  non-test module under `scripts/agent/` may import outside its own directory,
  and `command.mjs` may have no relative imports at all. Both derive their
  reason from the workflows rather than restating it.
- `scripts/README.md`'s new `agent/` row states the rule where someone about
  to add the import will read it.

Known limitations, deliberate:

- The staleness check does not gate. A `main` that drifts is caught by reading
  the `Docs` job log, not by a red lane. Gating it would need to know whether
  the work has merged, which needs git history, which would cost the
  pure-over-a-planted-tree property both sibling suites are built on.
- Coverage asks whether an entry is *mentioned*, not whether it is described.
  Deleting `scripts/agent/`'s row keeps the gate green as long as another row
  still writes `` `agent/` `` in passing — which is how it behaved when the
  break test deleted the row. Pinning "has a table row" would pin the README's
  current shape, and the first reformat would be answered by loosening the
  gate. The failure being closed is a script landing that the README never
  learns about at all.
- Coverage checks two areas. `docs/tasks/**` is not one of them: it already
  has a generator (`scripts/tasks-index.sh`) whose output is the index, so a
  coverage check there would assert that a script ran.
- `scripts/agent/*.mjs` keeps a raw entry-point idiom, so a symlinked
  invocation path still defeats it. It does not bite today — CI paths are
  real and the package's own suite derives CLI paths from an already-resolved
  `import.meta.url` — and closing it properly would mean a second copy of the
  predicate inside the package boundary, which is speculative until something
  actually runs these through a symlink.
