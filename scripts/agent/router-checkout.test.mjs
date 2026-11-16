// The `@claude` verb router is delivered BY NAME, not as a package. Route jobs
// check out `scripts/agent/command.mjs` with a non-cone sparse-checkout naming
// that one file, so nothing else of `scripts/agent` is on disk when it runs.
//
// So a local import added to command.mjs is not a refactor, it is an outage:
// the imported file is absent, node exits with ERR_MODULE_NOT_FOUND before
// `parseCommand` is reached, and the route job's empty `command=` reads as "no
// verb here" for every comment. The unit tests cannot see that, because they
// import command.mjs from a full checkout. These tests reproduce the delivery
// instead — the file is copied out alone and run — and hold its private copy
// of `isDirectRun` to the shared one in `direct-run.mjs`.

import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const WORKFLOWS = path.join(HERE, "..", "..", ".github", "workflows");
const ROUTER = "scripts/agent/command.mjs";
const TMP = realpathSync(os.tmpdir());

function area(t) {
  const base = mkdtempSync(path.join(TMP, "router-checkout-"));
  t.after(() => rmSync(base, { recursive: true, force: true }));
  return base;
}

// `<base>/scripts/agent/command.mjs` and nothing else: the route job's disk.
function deliverAlone(base) {
  mkdirSync(path.join(base, "scripts", "agent"), { recursive: true });
  const solo = path.join(base, ROUTER);
  copyFileSync(path.join(HERE, "command.mjs"), solo);
  return solo;
}

/**
 * Every package file a workflow checks out WITHOUT the package around it: an
 * entry under `scripts/agent/` in a `sparse-checkout:` list that does not also
 * name `scripts/agent` itself.
 */
function deliveredAlone() {
  const alone = new Map();
  for (const file of readdirSync(WORKFLOWS).filter((f) => f.endsWith(".yml"))) {
    const lines = readFileSync(path.join(WORKFLOWS, file), "utf8").split("\n");
    lines.forEach((line, i) => {
      const m = /^(\s*)sparse-checkout:\s*(.*)$/.exec(line);
      if (!m) return;
      const entries = [];
      if (m[2] && m[2] !== "|") {
        entries.push(m[2].trim());
      } else {
        for (const next of lines.slice(i + 1)) {
          if (!next.trim() || next.search(/\S/) <= m[1].length) break;
          entries.push(next.trim());
        }
      }
      if (entries.some((e) => e.replace(/\/$/, "") === "scripts/agent")) return;
      for (const e of entries.filter((e) => e.startsWith("scripts/agent/"))) {
        alone.set(e, [...(alone.get(e) ?? []), file]);
      }
    });
  }
  return alone;
}

test("the router is the one package file a workflow checks out alone", () => {
  // Read from the workflows, so a new single-file checkout of another tool
  // fails here and gets the same treatment — an inline `isDirectRun`, no local
  // imports — instead of finding out in a route job.
  const alone = deliveredAlone();
  assert.deepEqual([...alone.keys()], [ROUTER], `checked out alone: ${JSON.stringify(Object.fromEntries(alone))}`);
  assert.ok(alone.get(ROUTER).length > 0);
});

test("every file checked out alone imports node builtins only", () => {
  for (const [rel, workflows] of deliveredAlone()) {
    const src = readFileSync(path.join(HERE, "..", "..", rel), "utf8");
    const specs = [
      ...src.matchAll(/^\s*(?:import|export)\b[^;]*?\sfrom\s+["']([^"']+)["']/gm),
      ...src.matchAll(/^\s*import\s+["']([^"']+)["']/gm),
      ...src.matchAll(/\bimport\s*\(\s*["']([^"']+)["']/g),
    ].map((m) => m[1]);
    for (const spec of specs) {
      assert.ok(spec.startsWith("node:"), `${rel} imports ${spec}, but ${workflows.join(", ")} check it out alone, so only node: builtins are there`);
    }
  }
});

test("delivered alone, the router routes a verb to stdout and GITHUB_OUTPUT", (t) => {
  const base = area(t);
  deliverAlone(base);
  const out = path.join(base, "gh-output");
  const r = spawnSync(process.execPath, [ROUTER, "please @claude review this", "pr"], {
    cwd: base,
    encoding: "utf8",
    env: { ...process.env, GITHUB_OUTPUT: out },
  });
  assert.equal(r.status, 0, `router failed when delivered alone:\n${r.stdout}${r.stderr}`);
  assert.doesNotMatch(r.stderr, /ERR_MODULE_NOT_FOUND/, "the router imported a file its own checkout does not carry");
  assert.equal(r.stdout, "command=review\n");
  assert.equal(readFileSync(out, "utf8"), "command=review\n");
});

test("delivered alone and started through a symbolic link, the router still routes", (t) => {
  // The reason `direct-run.mjs` exists, against the router's own copy: `argv[1]`
  // as typed passes through the link, the module URL node resolved does not,
  // and a gate that compared them as written would print nothing and exit 0.
  const base = area(t);
  deliverAlone(base);
  symlinkSync(path.join(base, "scripts"), path.join(base, "linked-scripts"));
  const r = spawnSync(process.execPath, [path.join(base, "linked-scripts", "agent", "command.mjs"), "@claude loop", "pr"], { encoding: "utf8" });
  assert.equal(r.status, 0, r.stdout + r.stderr);
  assert.equal(r.stdout, "command=loop\n", "the router's entry check failed through a linked path");
});

test("the router's copy of isDirectRun gives the shared one's answer in every entry state", (t) => {
  // Held to the BEHAVIOUR of `direct-run.mjs` rather than to its text. Each
  // state sets `argv[1]`, asks the shared function about the router's URL,
  // then loads the router delivered alone and sees what it did: routed (run),
  // stayed quiet (imported) or threw.
  const base = area(t);
  const solo = deliverAlone(base);
  symlinkSync(path.join(base, "scripts"), path.join(base, "linked"));
  writeFileSync(path.join(base, "other.mjs"), "");
  writeFileSync(path.join(base, "file"), "");
  symlinkSync(path.join(base, "loop-b"), path.join(base, "loop-a"));
  symlinkSync(path.join(base, "loop-a"), path.join(base, "loop-b"));
  const STATES = {
    "no entry": [undefined, "imported"],
    "the router itself": [solo, "run"],
    "the router through a linked directory": [path.join(base, "linked", "agent", "command.mjs"), "run"],
    "another file": [path.join(base, "other.mjs"), "imported"],
    "a missing file": [path.join(base, "missing.mjs"), "imported"],
    "a path through a file": [path.join(base, "file", "command.mjs"), "imported"],
    "a link loop": [path.join(base, "loop-a"), "throw"],
  };
  const routerUrl = pathToFileURL(solo).href;
  const sharedUrl = pathToFileURL(path.join(HERE, "direct-run.mjs")).href;
  for (const [name, [entry, expected]] of Object.entries(STATES)) {
    const script = `
      process.argv[1] = ${entry === undefined ? "undefined" : JSON.stringify(entry)};
      process.argv[2] = "@claude review";
      const { isDirectRun } = await import(${JSON.stringify(sharedUrl)});
      let shared;
      try { shared = isDirectRun(${JSON.stringify(routerUrl)}) ? "run" : "imported"; } catch { shared = "throw"; }
      let wrote = "";
      const write = process.stdout.write.bind(process.stdout);
      process.stdout.write = (s) => { wrote += s; return true; };
      let router;
      try { await import(${JSON.stringify(routerUrl)}); router = wrote.includes("command=") ? "run" : "imported"; } catch { router = "throw"; }
      process.stdout.write = write;
      console.log(JSON.stringify({ shared, router }));
    `;
    const r = spawnSync(process.execPath, ["--input-type=module", "-e", script], { cwd: base, encoding: "utf8", env: { ...process.env, GITHUB_OUTPUT: "" } });
    assert.equal(r.status, 0, `${name}: ${r.stderr}`);
    assert.deepEqual(JSON.parse(r.stdout), { shared: expected, router: expected }, name);
  }
});
