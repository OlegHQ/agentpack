import { existsSync, readFileSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { execFileSync } from "node:child_process";
import vm from "node:vm";
import { createRequire } from "node:module";
import { cursorAgentBundle } from "../../config.ts";

export interface CursorProto {
  AgentClientMessage: ProtoClass;
  AgentServerMessage: ProtoClass;
}

export interface ProtoClass {
  typeName?: string;
  fromBinary(bytes: Uint8Array): ProtoMessage;
  fromJson(json: unknown): ProtoMessage;
}

export interface ProtoMessage {
  toBinary(): Uint8Array;
  toJson(options?: unknown): unknown;
}

let cached: CursorProto | undefined;

export function loadCursorProto(): CursorProto {
  if (cached) return cached;
  const bundle = resolveCursorBundle();
  const code = patchBundleMain(readFileSync(bundle, "utf8"));
  const req = createRequire(bundle);
  const ctx: Record<string, unknown> = {
    require: req,
    console,
    process,
    Buffer,
    TextDecoder,
    TextEncoder,
    crypto,
    performance,
    URL,
    Headers,
    setTimeout,
    clearTimeout,
    __filename: bundle,
    __dirname: dirname(bundle),
  };
  ctx.globalThis = ctx;
  ctx.global = ctx;
  vm.runInNewContext(code, ctx, { filename: bundle });
  const webpackRequire = ctx.__wr as ((id: string) => Record<string, unknown>) | undefined;
  if (!webpackRequire) {
    throw new Error(`Cursor Agent protobuf loader failed: webpack require was not exposed from ${bundle}`);
  }

  const agent = webpackRequire("../proto/dist/generated/agent/v1/agent_service_pb.js");
  cached = {
    AgentClientMessage: requireProtoClass(agent.KS, "agent.v1.AgentClientMessage"),
    AgentServerMessage: requireProtoClass(agent.Oy, "agent.v1.AgentServerMessage"),
  };
  return cached;
}

export function resolveCursorBundle(): string {
  const configured = cursorAgentBundle();
  if (configured) return normalizeBundlePath(configured);

  const found = findOnPath("cursor-agent");
  if (found) return normalizeBundlePath(found);

  throw new Error(
    "Cursor Agent bundle not found. Install cursor-agent or set CCP_CURSOR_AGENT_BUNDLE to its index.js path.",
  );
}

function normalizeBundlePath(path: string): string {
  const real = realpathSync(path);
  if (real.endsWith("index.js")) return real;
  const siblingIndex = join(dirname(real), "index.js");
  if (existsSync(siblingIndex)) return siblingIndex;
  throw new Error(`Cursor Agent bundle path does not point to index.js or a launcher beside index.js: ${path}`);
}

function findOnPath(bin: string): string | undefined {
  try {
    const out = execFileSync("which", [bin], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
    return out.trim() || undefined;
  } catch {
    return undefined;
  }
}

function patchBundleMain(code: string): string {
  const exact = 'var __webpack_exports__=__webpack_require__("./src/main.tsx")})();';
  if (code.includes(exact)) {
    return code.replace(
      exact,
      "globalThis.__wr=__webpack_require__; globalThis.__wm=__webpack_modules__;})();",
    );
  }
  const fallback = new RegExp(
    String.raw`var __webpack_exports__=__webpack_require__\(["']\./src/main\.tsx["']\)\}\)\(\);?\s*$`,
  );
  const patched = code.replace(
    fallback,
    "globalThis.__wr=__webpack_require__; globalThis.__wm=__webpack_modules__;})();",
  );
  if (patched !== code) return patched;
  throw new Error("Cursor Agent bundle main entry pattern was not recognized");
}

function requireProtoClass(value: unknown, typeName: string): ProtoClass {
  const candidate = value as Record<string, unknown> | undefined;
  if (
    candidate &&
    typeof value === "function" &&
    typeof candidate.fromBinary === "function" &&
    typeof candidate.fromJson === "function"
  ) {
    return value as unknown as ProtoClass;
  }
  throw new Error(`Cursor protobuf class missing: ${typeName}`);
}
