// The ONE exact-identity rule for a finding: its file, plus its case- and
// whitespace-insensitive summary.
//
// It lived as a private `const` inside `review-panel.mjs`, under a docblock
// warning about the thing that had already happened to it — "a second copy of
// this expression could drift looser than the merge it is supposed to agree
// with". There were three copies in the tree when this file was made: the
// definition beside `dedupeFindings`, a byte-identical inline `keyOf` inside
// `compareSampleAgreement`, and a third in the eval harness. Now there is one,
// and the warning is enforced by the import graph instead of by a comment.
//
// It imports nothing, on purpose. `review-panel.mjs` needs it and so does
// `eval/`, and the benchmark may never be imported by the panel (it only reads,
// which is the property that makes it safe to land here at all). So the shared
// rule has to sit somewhere both can reach without either reaching the other.
//
// THIS IS IDENTITY, NOT SIMILARITY, and the distinction is the whole reason the
// key is worth pinning. Two wordings of one defect get two keys here, and that
// is correct behaviour rather than a limitation: `clusterFindings` (restatement
// collapsing, inside the panel) and `finding-match.mjs` (#646, cross-stream
// matching) are the mechanisms for "the same defect, said differently". Swapping
// either of them in here would loosen the key that `dedupeFindings` and
// `compareSampleAgreement` share, and every number they produce —
// `review-lens-stats.json`'s `agreement` most of all — would quietly stop
// meaning what the panel says it means.

/**
 * `dedupeFindings`' collision key: file plus case- and whitespace-insensitive
 * summary. Extracted so `sameFinding` can be built ON it rather than beside it —
 * a second copy of this expression could drift looser than the merge it is
 * supposed to agree with, and that drift is what would let a verification be
 * skipped for a finding the merge then keeps separately.
 *
 * Deliberately NOT null-guarded. It is applied to findings that have already
 * been through `coerceFindings` (which replaces junk with a real object), and
 * adding a guard here would change the panel's behaviour on the one input shape
 * that is supposed to be impossible by then — a silent widening of what the
 * merge accepts, dressed as robustness.
 */
export const findingKey = (f) => `${f.file ?? ""}::${String(f.summary ?? "").toLowerCase().trim()}`;
