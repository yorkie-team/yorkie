# Lessons: server-side gate for MaxSizePerDocument

- **Pick the size source by its concurrency contract, not by how cheap it is
  to read.** The first attempt at this gate (rebuild-drift task, reverted)
  put the size on `DocInfo` because the push path already holds one. That
  `DocInfo` is the `docCache` entry, so writing the field raced every cache
  hit and invalidating it caused stale overwrites. The snapshot row costs one
  indexed read per growing push, but it is written once and never mutated,
  and compaction already purges it, which made the compaction reset free.
- **Gate on what a deletion shrinks.** `Total()` is `Live + GC`, and a
  deletion only moves bytes between the two. Gating on `Live` lets a removal
  re-open growth as soon as the next snapshot measures it, without waiting for
  garbage collection.
- **"Always admit detach" needs a qualifier.** Admitting a detach pack's
  changes as they are would make attach, push-in-detach, attach a way around
  the gate. The detach goes through; its growth changes are discarded, as
  stale-epoch changes already are.
- **Red check by disabling the gate.** With `checkDocumentSize` forced to
  admit, `TestDocumentSizeGate` fails on the refusal assertions, so the test
  exercises the gate rather than the SDK's own check.
