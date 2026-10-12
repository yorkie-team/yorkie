**Created**: 2026-09-25

# Lessons — the doc-index gate, the enforcement design doc, and direct-run

## The brief's premise was wrong, and checking it took ten minutes

The task named "roughly four files under `scripts/agent/` still carry the raw
idiom" and asked for them to import `../direct-run.mjs`. Both halves were
wrong in a way that only reading the workflows could show.

Twenty-two files carry it, not four. And none of them can import across the
directory boundary, because every workflow stages `scripts/agent/` detached
from its parent:

```
grep -n "sparse-checkout" .github/workflows/*.yml
  → 19 checkout steps, every one followed by
    sparse-checkout-cone-mode: false
grep -n "cp -R" .github/workflows/*.yml
  → cp -R scripts/agent "$RUNNER_TEMP/agent-tools"   (4 workflows)
```

The cone-mode flag is the whole argument, and it is exactly the kind of fact
that reads as true from memory in the wrong direction. Git's **cone** mode
includes every file in each ancestor directory of a cone, so
`sparse-checkout: scripts/agent` in cone mode *would* write
`scripts/direct-run.mjs`. With cone mode off the pattern is literal and it
does not. I proved it rather than recalling it, against real git, in a
throwaway clone:

```
git sparse-checkout set --no-cone scripts/agent
→ ./scripts/agent/command.mjs, ./scripts/agent/other.mjs
  (scripts/direct-run.mjs absent; even README.md absent)

git sparse-checkout init --cone && git sparse-checkout set scripts/agent/command.mjs
→ ./README.md, ./scripts/direct-run.mjs, ./scripts/agent/*
```

**Rule for next time:** a claim of the form "module A can import module B" in a
repository whose CI stages subdirectories is a claim about the *deployment*,
not about the filesystem in front of me. Find the staging step before writing
the import. Had I taken the brief at its word, `command.mjs` — the `@claude`
verb router six workflows check out on its own — would have thrown
`ERR_MODULE_NOT_FOUND` on every comment, and the failure is silent at the
workflow level: a job that cannot start the router reports an empty
`command=`, which every consumer reads as "no verb in this comment".

The brief also offered a precedent in the other direction
(`scripts/test/harness-hooks.test.mjs` imports `../agent/git-env.mjs`). It is
real and it is safe, for the reason that makes it useless as a precedent here:
a test only ever runs from a full checkout.

## A rejected task is not a finished one — leave evidence, not a verdict

Rather than doing nothing with intent 3, the useful residue of the
investigation was:

- normalising the four *deviant* guards, three of which were independently
  buggy (`` import.meta.url === `file://${process.argv[1]}` `` compares a
  percent-encoded URL against a raw path — a clone under `~/My Projects/`
  silently skips the CLI, which is the same class of bug `install.mjs`'s
  `shellQuote` header records one layer over);
- pinning the boundary in `checks.test.mjs`, with the premise read *from the
  workflows* so that changing the staging fails the premise assertion first,
  rather than leaving the constraint obeyed out of habit;
- writing the rule into `scripts/README.md`'s new `agent/` row, where someone
  about to add the import will read it.

A comment saying "do not do this" is worth less than a test that fails when
you do.

## Breaking the gate found a limit that running it green never would

Three Red/Green cycles, all of them cheap:

| Break | Result |
|---|---|
| Delete `Local Enforcement Layer` from `docs/design/README.md` | `docs/design/local-enforcement-layer.md is not linked from docs/design/README.md`, exit 1 |
| Delete the `agent/` row from `scripts/README.md` | **still green** — another row said "its reach stops at `agent/`" |
| Delete every mention of `agent/` | `scripts/agent/ is not mentioned in scripts/README.md`, exit 1 |
| Add one unchecked box to the live todo | staleness notice disappears; remove it, notice returns |

The second row is the one worth having found. The gate's contract is
"mentioned", not "has a row", and I only learned the difference by deleting
the row and watching nothing happen. It is now written in the script's header
as a stated limit rather than an unexamined assumption — pinning "has a table
row" would pin the README's current shape, and the first reformat would be
answered by loosening the gate.

**Rule:** run the new check against the thing it is supposed to catch, in the
exact shape a careless person would produce it — deleting one row, not
deleting the file.

## The staleness check flagged its own task document, twice, for two reasons

First it did **not** flag it, and that was a bug in my prose: the todo
explained the check by quoting the unchecked-box syntax in inline code, and
both this check and `scripts/tasks-archive.sh` match it unanchored. The
archiver would have refused to move the file for the same reason. I reworded
the todo rather than teaching the checker to strip inline code, because
diverging from the archiver would produce notices naming files the archiver
then refuses to touch — a gate pointing at a button that does not work.

Then it flagged it, correctly, and it still does: this task's todo has every
box ticked and is sitting in `active/`, because `CLAUDE.md` archives at merge.
That is precisely why the check does not gate. The first design I sketched had
it failing the build, and it would have refused the last commit of every task
that ever ran it — including this one. A gate that is wrong on the common case
teaches people the bypass flag, and after that it enforces nothing.

**Rule:** before making a check gating, ask what it does on the commit that
introduces it. If the answer is "refuses it", the check is measuring the wrong
thing or belongs in a different lane.

## Reusing the sibling's parser was the right call; reusing its evidence was not

`collectFindings` imports `linkTargets` from `verify-doc-links.mjs`, so a
design doc named inside a fence or inline code does not count as indexed — the
fence and comment stripping is already written, already tested, and already
argued for.

It would have been tempting to use it for `scripts/README.md` too. That would
have been silently wrong: every entry in that README is written as inline
code, which `prose()` deletes, so the check would have reported all eleven
entries as unindexed and I would have "fixed" it by weakening something. The
two areas index differently because they are read differently — one is
navigation, one is a reference table — and the evidence has to match.

## Verification

- `make verify` → exit 0; `[verify:license] Every Go file (497) carries the
  Apache 2.0 header`.
- `node --test 'scripts/test/**/*.test.mjs'` → 89 pass, 0 fail (24 of them
  new).
- `cd scripts/agent && npm ci && npm test` → 922 pass, 0 fail.
- `node scripts/verify-doc-links.mjs` → green.
- `node scripts/verify-doc-index.mjs` → coverage green over 44 design
  documents and 11 `scripts/` entries; one staleness notice, this task's own
  todo, exit 0.
