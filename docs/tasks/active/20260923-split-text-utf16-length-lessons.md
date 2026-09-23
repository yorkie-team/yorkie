# Lessons: SplitText records the left piece's length in runes

- **One unit everywhere.** The tree's lengths and offsets are UTF-16 code
  units because they have to agree with the JS SDK's string indices. A single
  site computing in runes is invisible for BMP text, which is most test data,
  and fatal for the first emoji or flag.
- **Test text outside the BMP.** A string with a surrogate pair in the middle
  exercises every place where runes and code units could be confused; plain
  ASCII and Hangul do not.

## Review round 1 (panel)

- **Correctness (major), accepted.** Splitting at an offset inside a surrogate
  pair let `utf16.Decode` rewrite the character as U+FFFD in both halves, so the
  replica kept text no other replica has. A test that skips those offsets pins
  nothing. `SplitText` now reports `ErrSplitInSurrogatePair` before touching the
  node, the same way an out-of-range offset is reported, and the case is tested.
  Restoring the JS SDK's lone-surrogate halves is not possible in a Go string,
  so failing the operation is the only non-corrupting answer.
