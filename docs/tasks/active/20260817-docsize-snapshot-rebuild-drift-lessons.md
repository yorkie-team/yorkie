# Lessons: DocSize drift between the rebuilt and incremental root

**Created**: 2026-08-17

## Measure the shape of a drift, not one instance of it

This was first recorded as "a constant `TicketSize`, reduced from one per
removed element" — measured on a document whose two removals were *nested*,
where the inner element's size had already been charged by the outer removal,
so the second removal refunded nothing. That looked like the fix had improved
the drift. Re-measuring with four *independent siblings* showed `+24/+48/
+72/+96`: linear in tombstone count, and byte-identical before and after the
fix.

Two mistakes in one: reading a trend from a single data point, and picking as
that point the case whose structure happened to suppress the effect. For a
quantity claimed to scale with something, vary that something and read the
slope. Three or four points cost nothing here.

## "Reported" and "enforced" can be different numbers

`Document.DocSize()` returns the incrementally maintained root's figure;
`MaxSizeLimit` is checked against `cloneRoot.DocSize()`, which is a
`DeepCopy` → `NewRoot` rebuild. The two are assumed equal and are not. The
drift was easy to dismiss as "a server-side reporting difference" until the
`document.go:257` call site showed the rebuilt figure is the one with
authority.

Worth checking, for any invariant maintained in two places: which copy does
the enforcement actually read? An accounting bug on the reporting path is a
cosmetic issue; the same bug on the enforcement path changes behaviour.

## An existing fix for the same shape is a good place to look for siblings

PR #1294 ("Separate correction logic in RegisterGCPair for editing and build")
fixed precisely this build-vs-edit conflation, one level down, for the
node-level GC pairs. The element-level pairs were never given the same
treatment, and that is exactly where this drift lives. When a bug turns out to
have a named predecessor in the same subsystem, the useful question is not
"was this fixed?" but "what else has the same shape and was missed?"
