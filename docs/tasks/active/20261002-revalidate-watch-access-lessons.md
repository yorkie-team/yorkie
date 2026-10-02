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
