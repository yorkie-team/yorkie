**Created**: 2026-10-02

# Lessons: Revalidate open Watch streams on demand

- Polling the webhook from every stream (#2075) cannot satisfy fast cutoff,
  bounded webhook load and tolerance of a slow webhook at once; five review
  rounds oscillated between those three. Only the customer knows when a
  permission changed, so the signal has to be pushed.
- What a revoked Watch leaks is narrower than "the document": PushPull
  re-authorizes per call. The exposure is broadcast payloads and activity
  metadata. Scope the fix to the stream.
- Treat "the check could not run" separately from "the check failed". The
  first draft mapped the revalidation's own context error to "webhook failed"
  and would have disconnected healthy streams on a slow project-wide call,
  then sent them all to an already slow webhook. The self review caught it;
  a unit test with a verifier that outlives the caller now guards it.
- Before reading a stream loop's ctx error as a cause, keep the old result
  for the old cases: returning context.Cause would have turned a client's
  own deadline into DeadlineExceeded, which logs at Error.
- Cross-check a replacement against the review history of what it
  replaces. #2075's findings were about polling, yet two of them (events
  delivered after a cut, a slow webhook against a fixed budget) survived in a
  new shape here.
- A `select` over a done channel and a data channel is not a priority
  order. Measure it: a 1000-run probe showed about one leaked event per
  closed stream.
- Only a definite answer should change state. "Could not verify" and "denied"
  are different outcomes even when both are errors.
