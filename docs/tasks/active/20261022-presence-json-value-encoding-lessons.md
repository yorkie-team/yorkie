**Created**: 2026-10-22

# Lessons: Go presence values are not JSON-encoded

- A `map<string, string>` on the wire says nothing about what the string
  means. Both SDKs compiled and ran for years with two different answers;
  only a mixed-SDK session surfaced it.
- The write boundary and the read boundary have to move together. Encoding
  `Set` alone would have broken every Go assertion that reads its own
  presence back; decoding in `Document` (not `InternalDocument`) keeps the
  server's snapshot and push-pull bytes in the wire form.
- The undo path re-applies values it recorded earlier, which are already
  encoded. Any encode-on-write change needs a raw setter for it, or undo
  quietly double-encodes.
- Unit tests all passed while two integration tests failed: the admin
  `GetDocument(s)` summaries are built from `InternalDocument`, so they carry
  the wire form, and the only assertions that cover that path live behind the
  `integration` build tag. A boundary audit has to follow the server's read
  paths too, not just the SDK's.
