# Lessons: restore span with a text parent

## Red evidence

On unmodified `main` (180780d9), both new tests fail with the error the
issue describes, for an element span and for a text span alike:

```text
--- FAIL: TestRestoreSpanUnderTextParentStaysApplicable/element_span
    tree_restore_text_parent_test.go:141: Received unexpected error:
        text node cannot have children
--- FAIL: TestRestoreSpanUnderTextParentStaysApplicable/text_span
    tree_restore_text_parent_test.go:141: Received unexpected error:
        text node cannot have children
--- FAIL: TestTreeRestoreTextParentSkip
    tree_restore_test.go:263: Received unexpected error:
        text node cannot have children  (isText=false, then isText=true)
```

Line 141 is the pulling replica's `ApplyChangePack`. The crafted pack
decodes without error, which confirms decode cannot catch it.

## Notes

- Decode-time validation is the wrong layer here. The converter sees a
  span, not the document, so it cannot know the parent ID names a text
  node. A push-time check would also leave changes already stored in the
  database unloadable.
- The skip is convergent because node kind is fixed at creation. A parent
  ID resolves to the same text node on every replica that still holds it,
  and to nothing on a replica that purged it, which B1 already skips.
- The text-span variant reaches the same `InsertAt` fallback: a text
  parent has no children, so rungs (a) to (c) all miss.
