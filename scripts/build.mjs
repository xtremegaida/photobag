#!/usr/bin/env node
// Builds PhotoBag: regenerates the TypeScript API types from the Go structs,
// builds the web UI into internal/webui/dist (embedded in the binary), then
// cross-compiles CGO-free binaries into dist/.
//
//   node scripts/build.mjs                      # all default targets
//   node scripts/build.mjs --targets linux/amd64
//   node scripts/build.mjs --skip-web           # reuse the last UI build
//   node scripts/build.mjs --version v1.0.0
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const args = process.argv.slice(2);
const flag = (name) => args.includes(name);
const option = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};

const defaultTargets = "windows/amd64,linux/amd64,linux/arm64";
const targets = option("--targets", defaultTargets).split(",").map((t) => t.trim()).filter(Boolean);

function run(cmd, cmdArgs, opts = {}) {
  console.log(`\n$ ${cmd} ${cmdArgs.join(" ")}`);
  execFileSync(cmd, cmdArgs, {
    stdio: "inherit",
    cwd: root,
    // npm is npm.cmd on Windows, which needs a shell; keep go unshelled so
    // arguments with spaces (ldflags) survive.
    shell: process.platform === "win32" && cmd === "npm",
    ...opts,
  });
}

function version() {
  const v = option("--version", "");
  if (v) return v;
  try {
    return execFileSync("git", ["describe", "--tags", "--always", "--dirty"], { cwd: root, stdio: ["ignore", "pipe", "ignore"] })
      .toString()
      .trim();
  } catch {
    return "dev";
  }
}

// 1. API types (Go -> TypeScript).
run("go", ["run", "github.com/gzuidhof/tygo@v0.2.21", "generate"]);

// 2. Web UI.
if (!flag("--skip-web")) {
  const web = path.join(root, "web");
  if (!existsSync(path.join(web, "node_modules"))) run("npm", ["ci"], { cwd: web });
  run("npm", ["run", "typecheck"], { cwd: web });
  run("npm", ["run", "build"], { cwd: web });
}

// 3. Binaries.
const v = version();
const out = path.join(root, "dist");
mkdirSync(out, { recursive: true });
for (const target of targets) {
  const [goos, goarch] = target.split("/");
  if (!goos || !goarch) throw new Error(`bad target ${target} (want os/arch)`);
  const name = `photobag-${goos}-${goarch}${goos === "windows" ? ".exe" : ""}`;
  run(
    "go",
    ["build", "-trimpath", "-ldflags", `-s -w -X photobag/internal/cli.Version=${v}`, "-o", path.join("dist", name), "./cmd/photobag"],
    { env: { ...process.env, CGO_ENABLED: "0", GOOS: goos, GOARCH: goarch } },
  );
}
console.log(`\nBuilt ${targets.length} binaries (${v}) in ${out}`);
