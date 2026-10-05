**Created**: 2026-09-26

# Harden the advisory verbs — lessons

## A verb that never ran looked like a working verb

`agent-summarize.yml` passed every test and actionlint, and the design doc
listed it as landed. It failed on its first step that talked to the action,
every time. Structural tests pin what a workflow declares, not whether the
action it calls can start. A new workflow needs one real run before it counts
as shipped.

## "Read-only" does not mean "low trust"

The PR-author path was justified as low-risk because the verbs write no code.
The risk is not what the verb writes but what the model can read: its process
environment holds the Claude credential, and on a public repository "the PR
author" is any account. Trigger authority has to be judged by what an
unattended model run exposes, not by the verb's output.

## Deny rules beat allow rules for paths

`--allowedTools Read` permits any absolute path. Verified in js-sdk with the
CLI: `Read` from an unrelated cwd opened `/etc/hosts`, and
`--disallowedTools "Read(//etc/**)"` refused it while `./` stayed readable.
Deny rules take precedence, so `/proc` and `/sys` are denied for every
read-capable built-in tool rather than trying to narrow the allow list.

## Back-ports leave stale prose behind

The code diff applied cleanly, but three pieces of prose outside the diff
became false: the summarize header's list of posting jobs, the step name
("no repo credentials"), and the pick-credential exception ("has no repo
checkout at all"). After a back-port, grep for the old claim, not just the
old code.
