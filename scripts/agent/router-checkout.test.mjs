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

// A YAML parser rather than a line scan, so every spelling of the list — `|`,
// `|-`, `>`, a quoted scalar, blank lines inside it — reads the same as it
// does to the runner. Loaded with `await import` like everywhere in this
// package; it is a devDependency, and only tests need it here.
const { parse } = await import("yaml");
const ROOT = path.join(HERE, "..", "..");
const PACKAGE = "scripts/agent";

/** A sparse-checkout pattern as a repository path: no `./` or `/` in front, no `/` behind. */
const normalize = (pattern) => pattern.replace(/^\.?\/+/, "").replace(/\/+$/, "");

/** Does this pattern bring the whole package, not just files in it? */
const coversPackage = (pattern) => {
  const p = normalize(pattern).replace(/\/\*\*?$/, "");
  return p === "" || p === "*" || p === "**" || p === "scripts" || p === PACKAGE;
};

/** Every `.yml`/`.yaml` under `dir`, as `[path relative to ROOT, text]`. */
function* yamlFiles(dir) {
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch (e) {
    if (e.code === "ENOENT") return;
    throw e;
  }
  for (const e of entries) {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) yield* yamlFiles(full);
    else if (/\.ya?ml$/.test(e.name)) yield [path.relative(ROOT, full), readFileSync(full, "utf8")];
  }
}

/** Every `sparse-checkout` value anywhere in a parsed document. */
function* sparseCheckouts(node) {
  if (Array.isArray(node)) {
    for (const v of node) yield* sparseCheckouts(v);
  } else if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node)) {
      if (k === "sparse-checkout" && v != null) yield String(v);
      else yield* sparseCheckouts(v);
    }
  }
}

/**
 * Every package file a workflow or action checks out WITHOUT the package
 * around it: an entry under `scripts/agent/` in a `sparse-checkout` list that
 * does not also bring the package itself.
 */
function deliveredAlone(files = [...yamlFiles(path.join(ROOT, ".github", "workflows")), ...yamlFiles(path.join(ROOT, ".github", "actions"))]) {
  const alone = new Map();
  for (const [file, text] of files) {
    for (const value of sparseCheckouts(parse(text))) {
      const entries = value.split("\n").map((l) => l.trim()).filter((l) => l && !l.startsWith("#") && !l.startsWith("!"));
      if (entries.some(coversPackage)) continue;
      for (const e of entries.map(normalize).filter((e) => e.startsWith(`${PACKAGE}/`))) {
        alone.set(e, [...(alone.get(e) ?? []), file]);
      }
    }
  }
  return alone;
}

test("every spelling of a sparse-checkout list is read, in workflows and actions", () => {
  // The shapes a line scan misses. Each names one package file alone, or the
  // package beside it.
  const step = (value) => `jobs:\n  route:\n    steps:\n      - uses: actions/checkout@v4\n        with:\n          sparse-checkout: ${value}\n`;
  const files = [
    ["a.yml", step("|-\n            scripts/agent/a.mjs\n\n            .github/agent-harness")],
    ["b.yml", step("|+\n            scripts/agent/b.mjs")],
    ["c.yml", step(">-\n            scripts/agent/c.mjs")],
    ["d.yml", step('"/scripts/agent/d.mjs"')],
    ["e.yml", step("'./scripts/agent/e.mjs'")],
    ["actions/f/action.yml", "runs:\n  using: composite\n  steps:\n    - uses: actions/checkout@v4\n      with:\n        sparse-checkout: scripts/agent/f.mjs\n"],
    // The package itself is in the list, after a blank line: not alone.
    ["g.yml", step("|\n            scripts/agent/g.mjs\n\n            ./scripts/agent/")],
    ["h.yml", step("|\n            scripts/agent/h.mjs\n            scripts/agent/**")],
  ];
  assert.deepEqual(
    Object.fromEntries(deliveredAlone(files)),
    {
      "scripts/agent/a.mjs": ["a.yml"],
      "scripts/agent/b.mjs": ["b.yml"],
      "scripts/agent/c.mjs": ["c.yml"],
      "scripts/agent/d.mjs": ["d.yml"],
      "scripts/agent/e.mjs": ["e.yml"],
      "scripts/agent/f.mjs": ["actions/f/action.yml"],
    },
  );
});

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
    const src = readFileSync(path.join(ROOT, rel), "utf8");
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
    // An argument after `node -e`, not a path: a comment body, say.
    "a name too long to exist": [`@claude review ${"x".repeat(300)}`, "imported"],
    "a link loop": [path.join(base, "loop-a"), "throw"],
    // The one state where the module's own path is the linked one: under
    // `--preserve-symlinks` the loader keeps an import's URL as written, so
    // only a realpath of BOTH sides matches it to the real path that was run.
    "the router run, but loaded through a link with --preserve-symlinks": [
      solo,
      "run",
      { via: path.join(base, "linked", "agent", "command.mjs"), flags: ["--preserve-symlinks"] },
    ],
  };
  const sharedUrl = pathToFileURL(path.join(HERE, "direct-run.mjs")).href;
  for (const [name, [entry, expected, { via = solo, flags = [] } = {}]] of Object.entries(STATES)) {
    const routerUrl = pathToFileURL(via).href;
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
    const r = spawnSync(process.execPath, [...flags, "--input-type=module", "-e", script], { cwd: base, encoding: "utf8", env: { ...process.env, GITHUB_OUTPUT: "" } });
    assert.equal(r.status, 0, `${name}: ${r.stderr}`);
    assert.deepEqual(JSON.parse(r.stdout), { shared: expected, router: expected }, name);
  }
});
