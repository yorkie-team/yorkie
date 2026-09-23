#!/usr/bin/env node
// Fetch `main`'s protection and ask branch-protection.mjs whether it holds.
//
// A THIN SHELL ON PURPOSE. The decision lives in a pure function next door so a
// test can execute it; everything here is I/O. The version of this that lived
// inside a `github-script` block shipped with an identifier referenced and never
// declared, so three of its four refusal paths threw instead of printing the
// diagnosis — through five review rounds, because a YAML regex is the only thing
// that had ever read it.
//
// Exits 0 when protection holds, 1 when it does not, 2 on a usage error. The
// caller prints the reason and fails the job; a refusal here is the intended
// outcome, not a crash.
import { execFileSync } from "node:child_process";
import { decideProtection } from "./branch-protection.mjs";

const repo = process.env.GITHUB_REPOSITORY || "";
const branch = process.argv[2] || "main";

function api(path) {
  const out = execFileSync("gh", ["api", path, "-H", "Accept: application/vnd.github+json"], {
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
  return JSON.parse(out);
}

function tryApi(path) {
  try {
    return { data: api(path), error: null };
  } catch (e) {
    // `gh api` puts the status in its stderr; recover it so the decision can
    // tell "no classic protection" (404) from "this token may not ask" (403).
    const m = /HTTP (\d{3})/.exec(String(e.stderr ?? e.message ?? ""));
    return { data: null, error: { status: m ? Number(m[1]) : 0 } };
  }
}

function main() {
  if (!repo) {
    process.stderr.write("check-protection: GITHUB_REPOSITORY is unset\n");
    process.exit(2);
  }
  const classic = tryApi(`repos/${repo}/branches/${branch}/protection`);
  const listed = tryApi(`repos/${repo}/rules/branches/${branch}`);
  const rules = listed.error ? null : listed.data;

  const rulesets = [];
  for (const r of rules ?? []) {
    if (r.type !== "pull_request" || !r.ruleset_id) continue;
    const got = tryApi(`repos/${repo}/rulesets/${r.ruleset_id}`);
    rulesets.push({ id: r.ruleset_id, ruleset: got.error ? null : got.data });
  }

  const verdict = decideProtection({
    classic: classic.data,
    classicError: classic.error,
    rules,
    rulesets,
  });
  if (verdict.ok) {
    process.stdout.write(`${branch} requires a human approving review this App cannot bypass\n`);
    process.exit(0);
  }
  process.stderr.write(`${verdict.reason}. This verb opens PRs a bot wrote; refusing to run.\n`);
  process.exit(1);
}

main();
