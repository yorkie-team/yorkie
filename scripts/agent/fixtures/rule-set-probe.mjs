// Runs calls against the modules that read the configuration at import, in a
// process of its own, so a test can ask "what do these say under ANOTHER
// configuration?" — the cargo rule set, which the fixture does not switch on.
//
//   AGENT_HARNESS_CONFIG_DIR=<dir> node rule-set-probe.mjs < calls.json
//
// stdin:  { "<key>": ["<module>", "<export>", ...args] }
// stdout: { "<key>": <result> }       (a thrown error is { "$error": message })
//
// An argument or a property written as one of these stands for what JSON
// cannot hold:
//   { "$set": [...] }       a Set
//   { "$read": {...} }      a function (a, b) => table[`${a}:${b}`] ?? null
//   { "$value": x }         a function () => x
// A non-function export is returned as it is.

import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));

function revive(v) {
  if (Array.isArray(v)) return v.map(revive);
  if (v === null || typeof v !== "object") return v;
  if (Object.hasOwn(v, "$set")) return new Set(v.$set);
  if (Object.hasOwn(v, "$read")) return (a, b) => v.$read[`${a}:${b}`] ?? null;
  if (Object.hasOwn(v, "$value")) return () => v.$value;
  return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, revive(x)]));
}

const calls = JSON.parse(readFileSync(0, "utf8"));
const out = {};
for (const [key, [module, name, ...args]] of Object.entries(calls)) {
  try {
    const mod = await import(pathToFileURL(path.join(HERE, "..", `${module}.mjs`)));
    const target = mod[name];
    out[key] = typeof target === "function" ? target(...revive(args)) : target;
    if (out[key] === undefined) out[key] = null;
  } catch (err) {
    out[key] = { $error: err.message };
  }
}
process.stdout.write(`${JSON.stringify(out)}\n`);
