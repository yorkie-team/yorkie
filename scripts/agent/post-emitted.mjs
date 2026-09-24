#!/usr/bin/env node
// Post a report or a dispute that a fix agent EMITTED, by re-rendering it here.
//
// WHY NOT JUST POST THE FILE. The agent writes it, and this step posts under the
// App identity — and this pipeline trusts latches, ledgers and dedupe markers BY
// AUTHOR. So a file posted verbatim is an arbitrary App-authored comment written
// by an agent that reads untrusted input, which is the same channel the narrow
// token was minted to close. Passing it through `sed` was worse: it mangled the
// hidden record the next panel round parses, losing every fix claim and dispute.
//
// The agent cannot be trusted to have used the CLI either — it holds a `Write`
// tool and can produce that file directly.
//
// So the emitted file is treated as DATA. Its hidden record is parsed here, in
// trusted code re-staged after the agent stopped, and the body that gets posted
// is this module's own render of that record — which neutralises visible markers
// on the way through. A file whose record does not parse is not posted at all:
// an unreadable report is one the next round would ignore anyway, and posting it
// would put unrendered agent text under the App's name.
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { parseFixReportComment, renderFixReportBody } from "./fix-report.mjs";
import { parseRebuttalComment, renderRebuttalComment } from "./rebuttal.mjs";

const GH_MAX_BUFFER = 64 * 1024 * 1024;

const USAGE = "usage: post-emitted.mjs <report|dispute> <pr> --file <path> [--disputed <n>]";

function main() {
  const [kind, pr] = process.argv.slice(2);
  const args = {};
  for (let i = 4; i < process.argv.length; i += 2) {
    args[String(process.argv[i]).replace(/^--/, "")] = process.argv[i + 1];
  }
  if (!["report", "dispute"].includes(kind) || !/^\d+$/.test(String(pr)) || !args.file) {
    process.stderr.write(`${USAGE}\n`);
    process.exit(2);
  }

  let raw;
  try {
    raw = readFileSync(String(args.file), "utf8");
  } catch (e) {
    process.stderr.write(`post-emitted: cannot read ${args.file} (${e.message}); nothing to post\n`);
    return;
  }

  const rec = kind === "report" ? parseFixReportComment(raw) : parseRebuttalComment(raw);
  if (!rec) {
    // REFUSED, LOUDLY. This is the case where the agent wrote the file itself
    // instead of letting the CLI render it, and it is the one this module
    // exists to catch.
    process.stderr.write(
      `post-emitted: ${args.file} carries no readable ${kind} record; refusing to post ` +
        "unrendered agent text under the App identity\n",
    );
    process.exit(1);
  }

  const body = kind === "report"
    ? renderFixReportBody(rec, { disputed: Number(args.disputed || 0) })
    : renderRebuttalComment(rec);
  if (!body) {
    process.stderr.write(`post-emitted: the ${kind} did not round-trip; refusing to post it\n`);
    process.exit(1);
  }

  try {
    execFileSync("gh", ["pr", "comment", String(pr), "--body", body], {
      encoding: "utf8",
      maxBuffer: GH_MAX_BUFFER,
    });
  } catch (e) {
    process.stderr.write(`post-emitted: could not comment on #${pr} (${e.message})\n`);
    process.exit(1);
  }
  process.stderr.write(`post-emitted: posted a ${kind} on #${pr}\n`);
}

main();
