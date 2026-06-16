#!/usr/bin/env bun

import { mkdtempSync, copyFileSync, existsSync, mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";

const formatExtensions = new Set([
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".mts",
  ".cts",
  ".json",
  ".jsonc",
]);

function git(
  args: string[],
  options: { env?: NodeJS.ProcessEnv; input?: string | Uint8Array } = {},
) {
  const result = spawnSync("git", args, {
    cwd: repoRoot,
    env: { ...process.env, ...options.env },
    input: options.input,
    encoding: typeof options.input === "string" ? "utf8" : "buffer",
    stdio: options.input === undefined ? ["ignore", "pipe", "pipe"] : ["pipe", "pipe", "pipe"],
  });

  if (result.status !== 0) {
    const stderr = result.stderr?.toString() ?? "";
    throw new Error(`git ${args.join(" ")} failed${stderr ? `:\n${stderr}` : ""}`);
  }

  return result.stdout.toString();
}

function command(args: string[], options: { cwd: string; env?: NodeJS.ProcessEnv }) {
  const result = spawnSync(args[0]!, args.slice(1), {
    cwd: options.cwd,
    env: { ...process.env, ...options.env },
    stdio: "inherit",
  });

  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}

function parseNulList(output: string) {
  return output.split("\0").filter(Boolean);
}

function hasFormatExtension(path: string) {
  for (const extension of formatExtensions) {
    if (path.endsWith(extension)) return true;
  }
  return false;
}

function hashFile(path: string) {
  const result = spawnSync("git", ["hash-object", "--", path], {
    cwd: repoRoot,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });

  if (result.status !== 0) return null;
  return result.stdout.trim();
}

const rootResult = spawnSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" });
if (rootResult.status !== 0) {
  process.stderr.write(rootResult.stderr);
  process.exit(rootResult.status ?? 1);
}

const repoRoot = rootResult.stdout.trim();
process.chdir(repoRoot);

const stagedFiles = parseNulList(
  git(["diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR"]),
);
const targets = stagedFiles.filter(hasFormatExtension);

if (targets.length === 0) {
  process.exit(0);
}

const tempDir = mkdtempSync(join(tmpdir(), "format-staged-"));
const tempTree = join(tempDir, "tree");
const tempIndex = join(tempDir, "index");
mkdirSync(tempTree);

try {
  const indexPath = git(["rev-parse", "--git-path", "index"]).trim();
  copyFileSync(indexPath, tempIndex);

  const tempGitEnv = { GIT_INDEX_FILE: tempIndex, GIT_WORK_TREE: tempTree };
  const indexEntries = git(["ls-files", "-s", "--", ...targets]);
  const entries = new Map<string, { mode: string; sha: string }>();

  for (const line of indexEntries.trim().split("\n").filter(Boolean)) {
    const match = /^(\d+) ([0-9a-f]+) \d\t(.+)$/.exec(line);
    if (!match) throw new Error(`could not parse index entry: ${line}`);
    entries.set(match[3]!, { mode: match[1]!, sha: match[2]! });
  }

  git(["checkout-index", "-z", "--stdin", "-f"], {
    env: tempGitEnv,
    input: Buffer.from(`${targets.join("\0")}\0`),
  });

  for (const configFile of [".gitignore", ".oxfmtrc.json", ".oxfmtrc.jsonc"]) {
    const source = join(repoRoot, configFile);
    if (existsSync(source)) copyFileSync(source, join(tempTree, configFile));
  }

  command(["bunx", "oxfmt", "--write", ...targets], { cwd: tempTree, env: { FORCE_COLOR: "1" } });

  let changed = 0;
  let kept = 0;

  for (const path of targets) {
    const entry = entries.get(path);
    if (!entry) continue;

    const tempPath = join(tempTree, path);
    const newSha = git(["hash-object", "-w", "--path", path, tempPath]).trim();
    if (newSha === entry.sha) continue;

    git(["update-index", "--cacheinfo", `${entry.mode},${newSha},${path}`]);
    changed++;

    const workingTreeSha = hashFile(path);
    if (workingTreeSha === entry.sha) {
      git(["checkout", "--", path]);
    } else {
      kept++;
    }
  }

  if (changed > 0) {
    const suffix = kept > 0 ? ` (${kept} kept due to unstaged edits)` : "";
    console.log(`[format-staged] formatted ${changed} staged file(s)${suffix}`);
  }
} finally {
  rmSync(tempDir, { recursive: true, force: true });
}
