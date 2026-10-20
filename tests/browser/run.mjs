import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { spawnSync } from "node:child_process";
const require = createRequire(import.meta.url);
const [major, minor] = process.versions.node.split(".").map(Number);
if (major < 24 || (major === 24 && minor < 8)) {
  console.error("Browser E2E requires Node >=24.8.0.");
  process.exit(1);
}
const cli = join(dirname(require.resolve("e2e")), "cli", "bin.js");
const result = spawnSync(process.execPath, [cli, "run", ...process.argv.slice(2)], {
  stdio: "inherit",
  env: { ...process.env, E2E_TELEMETRY_DISABLED: "1" },
});
if (result.error) throw result.error;
process.exit(result.status ?? 1);
