# Lessons: HLL register bound and cross-SDK hash vectors

## Notes

- The sentinel bit in `HLL.Add` (`w |= 1 << (hllPrecision - 1)`) caps rho
  at `64 - hllPrecision + 1` = 51, which is exactly the JS
  `maxRegisterValue`. The bound in `Restore` is therefore the inverse of
  what `Add` can produce, not an independent constant.
- Deriving each vector's register index and rho by observing
  `NewHLL()` + `Add` + `Bytes()` keeps the fixture a check on the
  implementation rather than a second copy of the index/rho formula.
- The fixture pins the UTF-8 bytes of every string next to the string
  itself, so an editor or a transfer that normalizes the combining-mark
  and ZWJ vectors fails the test instead of silently changing what is
  being hashed.

## Self review

- Not run: this branch was produced by an autonomous one-shot run, which
  is not granted the reviewer subagent `/self-review` dispatches. The PR
  is left as a draft for CI, `@claude review` and a human.
