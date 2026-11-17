# Add an external login provider verifying signed assertions — Lessons

**Created**: 2026-11-16

- Local users are inserted without an `auth_provider` field at all, not with
  an empty string. A unique index over `auth_provider` sees them as null, and
  a filter on `auth_provider: ""` never matches them. Check what the stored
  documents really look like before writing a filter or an index over them.
- A unique index added to a collection that older servers still write to
  breaks their inserts during a rolling update. A partial index over the new
  field leaves their documents out until the backfill reaches them.
- A backfill at start covers what older servers wrote before it, not what
  they write while both versions run. Backfill again lazily where the
  missing field is first needed.
- go-memdb's `CompoundIndex.AllowMissing` stops at the first missing field
  and still returns a key, so every local user got the same empty key in a
  "unique" index. `IndexSchema.AllowMissing` with a strict compound leaves
  them out instead.
- `types.UserFields.Validate` also requires a password, so it cannot check a
  username alone; `validation.ValidateValue` with the same tag can. A check
  that always fails looks like a strict check in tests that only expect
  rejections, so the happy path needs its own test.
- Prefixing external auth providers (`external:<name>`) removed a whole
  class of checks: no reserved-name list is needed to keep a provider named
  `github` away from GitHub users.
- A mutation that deletes the only use of a variable fails to compile, and a
  grep for `FAIL`/`ok` prints nothing, which reads like a pass. Mutate to a
  value that keeps the code compiling, and check the output is non-empty.

## Self review

- Round 1, A (DB and backfill; one reviewer, 8 tool calls): nothing
  blocking. Fixed: users written by an older server after start were locked
  out of provider login (lazy backfill); the memory compound index gave all
  local users one key; `DeepCopy` no longer copies `AccessedAt`, which was an
  unrelated change. Added tests for the rolling-update case, racing
  subjects, and the default user's subject.
- Round 1, B (security; one reviewer, 12 tool calls): one blocking finding.
  The callback redirected with 307, which keeps the method, so the browser
  re-posted the assertion to the dashboard host, which answers 405 on a
  static site. httptest asserted the 307 and never followed it. Fixed with
  303. Also fixed: the state cookie gets the `__Host-` prefix and Path=/ (a
  sibling subdomain could plant a more specific cookie; Path=/auth/ broke
  path-prefixed deployments), the deletion mirrors its attributes, the
  callback body is capped and urlencoded only, pending logins are capped,
  `LoginURL` must be https or loopback, and the design doc tells brokers to
  keep the state out of Referer and logs. Documented: a second login in
  another tab replaces the first.
- A test that asserts a status code without following it cannot tell a
  redirect that works from one that breaks the next hop. When a redirect
  follows a POST, check the code is 303.
