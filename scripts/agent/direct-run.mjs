// Was this module run, or imported? One answer for every tool in this package.
//
// WHY IT IS SHARED. The workflows do not start these files from the checkout
// they were written in: they copy the package to `$RUNNER_TEMP/agent-tools`, or
// sparse-check it out into a side path, and run `node "<there>/<tool>.mjs"`.
// Any of those directories can pass through a symbolic link, and Node resolves
// a module's own `import.meta.url` through links while leaving
// `process.argv[1]` exactly as typed. Compared as written —
// `path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)` — the two
// differ on precisely the runner the tool runs on, the CLI block is skipped,
// and the process exits 0 having done nothing: no state written, no refusal,
// and a step the workflow credits as a success. So both sides go through
// `realpath`, here, once.
//
// A repository may have a `scripts/direct-run.mjs` of its own (the one this
// core is written in does, with the same name and call). This package cannot
// import it: every workflow stages this directory without its parent, so a
// `../` import is ERR_MODULE_NOT_FOUND (`checks.test.mjs` pins it). And a path
// that exists but cannot be resolved THROWS here rather than falling back to
// the plain comparison, because that fallback answers "imported" in exactly
// the linked case and so restores the silent no-op.
//
// ONE TOOL MAY NOT IMPORT THIS: `command.mjs`, which route jobs check out
// alone (`router-checkout.test.mjs` says which, and runs it that way). It
// carries an inline copy of the function below; keep the two in step.

import { realpathSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

/**
 * True when the module whose `import.meta.url` is passed is the file Node was
 * asked to run, false when it was imported. Call as
 * `isDirectRun(import.meta.url)`.
 *
 * @param {string} moduleUrl the caller's own `import.meta.url`
 * @returns {boolean}
 * @throws {Error} when a path that exists cannot be resolved through symbolic
 *   links, so neither answer is known to be right.
 */
export function isDirectRun(moduleUrl) {
  const typed = process.argv[1];
  // `node --eval`, `node --test` with no file, the REPL: no script was run.
  if (!typed) return false;
  const self = fileURLToPath(moduleUrl);
  // The common case: nothing on the path is a link.
  if (path.resolve(typed) === self) return true;
  let entry;
  try {
    entry = realpathSync(typed);
  } catch (error) {
    // A path that is not there cannot be this module, which is — nor can a
    // name too long to exist, which is what an `argv[1]` that is not a path
    // at all (an argument after `node -e`) can be.
    if (NOT_THERE.has(error?.code)) return false;
    throw unresolvable(typed, error);
  }
  try {
    return entry === realpathSync(self);
  } catch (error) {
    throw unresolvable(self, error);
  }
}

const NOT_THERE = new Set(["ENOENT", "ENOTDIR", "ENAMETOOLONG"]);

function unresolvable(target, cause) {
  return new Error(
    `cannot tell whether this script was run or imported: ${target} does not resolve through symbolic links (${cause?.message ?? cause})`,
    { cause },
  );
}
