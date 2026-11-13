// Points `config.mjs` at the fixture configuration beside this file.
//
// A test that reaches `config.mjs` — directly or through any module that reads
// it — imports this FIRST, before anything else:
//
//   import "./fixtures/use-fixture-config.mjs";
//
// ES modules evaluate their imports in source order, so the variable is set
// before the first module that loads the configuration runs. It is set on
// `process.env`, so a CLI the test spawns inherits it too.
//
// That is what keeps this package's tests the same bytes in every repository:
// they grade the code against `fixtures/harness-config/`, never against the
// values of whichever repository they happen to be running in. The repository's
// own values are graded by `.github/agent-harness/repo.test.mjs`.
// `config.test.mjs` fails if a test file reaches the configuration without this
// import.

import path from "node:path";
import { fileURLToPath } from "node:url";

export const FIXTURE_CONFIG_DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), "harness-config");

process.env.AGENT_HARNESS_CONFIG_DIR = FIXTURE_CONFIG_DIR;
