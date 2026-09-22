import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { collectFindings, fixableFindings, renderAiPromptSection } from "./ai-prompt.mjs";

const CLI = fileURLToPath(new URL("./ai-prompt.mjs", import.meta.url));

const F = (severity, file, summary, extra = {}) => ({ severity, file, summary, ...extra });

test("fixableFindings keeps only blocking, non-demoted findings", () => {
  const got = fixableFindings([
    F("critical", "a.ts", "boom"),
    F("major", "b.ts", "leak"),
    F("minor", "c.ts", "style"),
    F("nit", "d.ts", "typo"),
  ]);
  assert.deepEqual(got.map((f) => f.file), ["a.ts", "b.ts"]);
});

test("demoted (lane=backlog) blocking findings are excluded — not this PR's to fix", () => {
  const got = fixableFindings([
    F("critical", "a.ts", "boom"),
    F("critical", "old.ts", "pre-existing", { lane: "backlog" }),
  ]);
  assert.deepEqual(got.map((f) => f.file), ["a.ts"]);
});

test("unknown severity is treated as major (fail-safe) and kept", () => {
  const got = fixableFindings([F("weird", "a.ts", "?")]);
  assert.equal(got.length, 1);
});

test("dedupes by file + summary across lenses, ignoring whitespace", () => {
  const got = fixableFindings([
    F("major", "a.ts", "same  bug"),
    F("major", "a.ts", "same bug"),
    F("major", "b.ts", "same bug"),
  ]);
  assert.deepEqual(
    got.map((f) => f.file),
    ["a.ts", "b.ts"],
  );
});

test("critical sorts before major; stable within a severity", () => {
  const got = fixableFindings([
    F("major", "z.ts", "m1"),
    F("critical", "b.ts", "c1"),
    F("major", "a.ts", "m2"),
  ]);
  assert.deepEqual(got.map((f) => f.file), ["b.ts", "a.ts", "z.ts"]);
});

test("no blocking findings → empty string (no section)", () => {
  assert.equal(renderAiPromptSection([F("minor", "a.ts", "x")]), "");
  assert.equal(renderAiPromptSection([]), "");
  assert.equal(renderAiPromptSection(null), "");
  assert.equal(renderAiPromptSection(undefined), "");
});

test("rendered section is a collapsed <details> with a fenced code block", () => {
  const out = renderAiPromptSection([F("critical", "a.ts", "boom"), F("major", "b.ts", "leak")]);
  assert.match(out, /^<details>\n<summary>🤖 Prompt for AI Agents<\/summary>/);
  assert.match(out, /<\/details>$/);
  // Numbered, severity-tagged lines with the file in backticks.
  assert.match(out, /1\. \[critical\] `a\.ts` — boom/);
  assert.match(out, /2\. \[major\] `b\.ts` — leak/);
  // A blank line after <summary> (Markdown-in-HTML requires it to render).
  assert.match(out, /<\/summary>\n\n```/);
});

test("a summary containing a triple-backtick fence cannot break out of the block", () => {
  const out = renderAiPromptSection([F("critical", "a.ts", "see ``` here")]);
  // The fence must grow past any backtick run in the body (```→````), so the
  // summary's ``` can't close the block early.
  assert.ok(out.includes("````"), "fence should grow to at least 4 backticks");
  // And the whole thing still closes as one details block.
  assert.match(out, /<\/details>$/);
});

test("a finding with no file renders without an empty backtick pair", () => {
  const out = renderAiPromptSection([F("critical", "", "global issue")]);
  assert.match(out, /1\. \[critical\] global issue/);
  assert.doesNotMatch(out, /`` —/);
});

// Build a fake `.agent-review` tree: one dir per lens with a verdict.json,
// plus a stray panel.json and a dir with no verdict.json (both must be ignored).
function fakeReviewDir() {
  const dir = mkdtempSync(path.join(tmpdir(), "ai-prompt-"));
  const lens = (id, findings) => {
    mkdirSync(path.join(dir, id), { recursive: true });
    writeFileSync(path.join(dir, id, "verdict.json"), JSON.stringify({ findings }));
  };
  lens("correctness", [F("critical", "a.ts", "boom"), F("minor", "a.ts", "ZZnitpick")]);
  lens("security", [F("major", "b.ts", "leak")]);
  mkdirSync(path.join(dir, "empty-lens"), { recursive: true }); // no verdict.json
  writeFileSync(path.join(dir, "panel.json"), "[]"); // stray file, not a dir
  return dir;
}

test("collectFindings flattens every lens's verdict.json, skips non-lens entries", () => {
  const dir = fakeReviewDir();
  const all = collectFindings(dir);
  assert.equal(all.length, 3); // 2 from correctness + 1 from security
  // A missing dir is fail-safe, not a throw.
  assert.deepEqual(collectFindings(path.join(dir, "does-not-exist")), []);
});

test("CLI writes the section to --out and echoes it (blocking findings present)", () => {
  const dir = fakeReviewDir();
  const outFile = path.join(dir, "ai-prompt.md");
  const stdout = execFileSync("node", [CLI, "--review-dir", dir, "--out", outFile], { encoding: "utf8" });
  const written = readFileSync(outFile, "utf8");
  assert.match(written, /🤖 Prompt for AI Agents/);
  assert.match(written, /\[critical\] `a\.ts` — boom/);
  assert.match(written, /\[major\] `b\.ts` — leak/);
  assert.doesNotMatch(written, /ZZnitpick/); // minor excluded
  assert.equal(stdout.trim(), written.trim());
});

test("CLI writes an empty file when nothing blocks", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "ai-prompt-empty-"));
  mkdirSync(path.join(dir, "docs"));
  writeFileSync(path.join(dir, "docs", "verdict.json"), JSON.stringify({ findings: [F("minor", "a.ts", "x")] }));
  const outFile = path.join(dir, "ai-prompt.md");
  execFileSync("node", [CLI, "--review-dir", dir, "--out", outFile], { encoding: "utf8" });
  assert.equal(readFileSync(outFile, "utf8"), "");
});
