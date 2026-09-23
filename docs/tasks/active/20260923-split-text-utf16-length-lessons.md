# Lessons: SplitText records the left piece's length in runes

- **One unit everywhere.** The tree's lengths and offsets are UTF-16 code
  units because they have to agree with the JS SDK's string indices. A single
  site computing in runes is invisible for BMP text, which is most test data,
  and fatal for the first emoji or flag.
- **Test text outside the BMP.** A string with a surrogate pair in the middle
  exercises every place where runes and code units could be confused; plain
  ASCII and Hangul do not.
