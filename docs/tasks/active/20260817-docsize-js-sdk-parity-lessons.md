# Lessons: porting the DocSize container-removal fix to the JS SDK

**Created**: 2026-08-17

Filed alongside the todo, before the port. Carrying forward the one lesson
that is already established, so the port does not have to relearn it.

## Confirm cross-SDK parity by running both, not by reading both

The Go fix's paired lessons file records this at length
(`20260816-root-docsize-nested-container-gc-lessons.md`, "The same bug's
numbers in two SDKs is the strongest parity evidence"). The short version:
reading `registerRemovedElement` next to `RegisterRemovedElementPair` invites
"but the surrounding paths differ"; running the same four scenarios through
both SDKs and getting byte-identical figures does not.

The same standard applies in reverse when this port lands — the port is done
when the JS numbers match the *fixed* Go numbers on the same scenarios, not
when the JS code shape matches.

It also earns its keep on the differences: the one genuine Go/JS divergence
in this area (Go skipping the registration when a remove loses LWW, JS
registering unconditionally) was invisible in the source shape and only
appeared because the concurrent scenario was executed on both.

## See Also

- `docs/tasks/active/20260817-docsize-js-sdk-parity-todo.md` — the port plan
- `docs/tasks/archive/2026/08/20260816-root-docsize-nested-container-gc-lessons.md` —
  the Go fix's lessons, including the parity-evidence one above
