# Tell a copy of the shared agent harness when it is behind — Lessons

**Created**: 2026-11-14

## Notes

- "The record is wrong" and "upstream moved" are different news. Only the
  second belongs in an issue that says "run a sync"; the first is a red run.
  So the notifier runs the pinned check first and stops there if it fails.
- An issue a bot will later edit or close has to be found by something a
  visitor cannot set. Title and label are both settable by other people;
  author is not, and a marker on the body's first line survives the bot
  quoting somebody's text further down.
- Read the head's manifest by the commit id just read, not by the branch. Two
  requests by branch can describe two different trees.
- Where a job with a write token must never be started by a pull request, a
  workflow of its own makes the trigger list the boundary. An `if:` beside a
  job that runs untrusted code is one line away from not being there.
- A guard keyed on a file name passes the day the job moves. Keying the rule
  on the script the job runs follows it into whichever workflow it lands in.
- Other suites read every `agent-*.yml`: a new workflow that runs a harness
  script needs a workflow-level `GH_REPO`, and no module but `config.mjs` may
  spell the configuration directory's name — the marker was renamed for that.
- Comparing the body's first line, not the whole body, is what makes "has
  anything changed" survive a web edit that turned every line ending to CRLF.
- "No open issue" is not "no issue". A person closing the issue is an answer,
  and a job that only lists open ones replies to it with a new issue every
  week. The closed one has to be read too, and the same state respected.
- Comment after the close, not before. Whichever write comes first is the one
  a retry repeats, and a repeated close is harmless where a repeated comment
  is not.
- Rules about the job that runs a script do not see the job next to it, or
  the step after it. Where a workflow holds a write token on a clock, the
  guard has to pin the file's shape — one job, these steps, these lines —
  and refuse everything else, rather than list what is known to be bad.
- "Starts with the marker" is true of other workflows' issues by the same
  bot. Match the whole line the script writes, and a second property (the
  title) that another workflow would not have by accident.
- A name that passed `[\w./-]+` is safe in a query string after encoding and
  not in a path: `..` survives. Check it where it is used, not where it was
  read.
