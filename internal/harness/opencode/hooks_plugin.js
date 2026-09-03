import { readFileSync } from "node:fs";

const config = JSON.parse(readFileSync(new URL("./config.json", import.meta.url), "utf8"));

function normalizeToolName(name) {
  return String(name ?? "").toLowerCase();
}

function matchesMatcher(matcher, toolName) {
  if (!matcher || matcher === "*") return true;
  const actual = normalizeToolName(toolName);
  return String(matcher)
    .split("|")
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean)
    .some((candidate) => candidate === actual || candidate === "bash" && actual === "shell");
}

async function runHook(kind, specPath, payload) {
  const proc = Bun.spawn(
    ["agentpack", "hook-exec", kind, "--target", "opencode", "--spec", specPath],
    {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  const writer = proc.stdin.getWriter();
  await writer.write(new TextEncoder().encode(JSON.stringify(payload)));
  await writer.close();
  const stdout = await new Response(proc.stdout).text();
  const stderr = await new Response(proc.stderr).text();
  const status = await proc.exited;
  if (status !== 0) {
    throw new Error(stderr || stdout || `hook-exec exited ${status}`);
  }
  return stdout.trim() ? JSON.parse(stdout) : { decision: "allow" };
}

function entriesFor(event, toolName) {
  return (config.hooks || []).filter((entry) => entry.event === event && matchesMatcher(entry.matcher, toolName));
}

export default {
  id: "agentpack-hooks",
  server: async () => ({
    "tool.execute.before": async (input, output) => {
      for (const entry of entriesFor("tool.execute.before", input.tool)) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.updated_input !== undefined) {
          output.args = result.updated_input;
        }
        if (result.decision === "deny" || result.decision === "ask") {
          throw new Error(result.message || "OpenCode hook blocked tool execution");
        }
      }
    },
    "tool.execute.after": async (input, output) => {
      for (const entry of entriesFor("tool.execute.after", input.tool)) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.updated_tool_output !== undefined) {
          output.output = typeof result.updated_tool_output === "string"
            ? result.updated_tool_output
            : JSON.stringify(result.updated_tool_output);
        }
        if (result.additional_context) {
          output.output = `${output.output}\n\n${result.additional_context}`;
        }
      }
    },
    "permission.ask": async (input, output) => {
      for (const entry of entriesFor("permission.ask", input.tool || input.command || input.kind)) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.decision === "deny") output.status = "deny";
        else if (result.decision === "ask" && output.status !== "deny") output.status = "ask";
        else if (result.decision === "allow" && output.status !== "deny") output.status = "allow";
      }
    },
    "chat.message": async (input, output) => {
      for (const entry of entriesFor("chat.message", "message")) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.additional_context) {
          output.parts.push({ type: "text", text: result.additional_context });
        }
      }
    },
    "experimental.session.compacting": async (input, output) => {
      for (const entry of entriesFor("experimental.session.compacting", "compact")) {
        const result = await runHook(entry.kind, entry.specPath, { input, output });
        if (result.additional_context) {
          output.context.push(result.additional_context);
        }
      }
    },
  }),
};
