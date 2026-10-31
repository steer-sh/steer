import { execFile } from "node:child_process";

const STEER_MASTER = "__STEER_MASTER__";
const STEER_TIMEOUT_MS = 10_000;

function steerToolCall(event: any): any {
  const input = event?.input ?? {};
  switch (event?.toolName) {
    case "bash":
    case "powershell":
      return { tool_name: "Bash", tool_input: { command: input.command ?? "" } };
    case "write":
      return {
        tool_name: "Write",
        tool_input: { file_path: input.path ?? "", content: input.content ?? "" },
      };
    case "edit":
      const edits = Array.isArray(input.edits)
        ? input.edits
        : typeof input.oldText === "string" && typeof input.newText === "string"
          ? [{ oldText: input.oldText, newText: input.newText }]
          : [];
      return {
        tool_name: "MultiEdit",
        tool_input: {
          file_path: input.path ?? "",
          edits: edits.map((edit: any) => ({
            old_string: edit?.oldText ?? "",
            new_string: edit?.newText ?? "",
          })),
        },
      };
    default:
      return undefined;
  }
}

function runSteer(payload: any): Promise<any> {
  return new Promise((resolve) => {
    const child = execFile(
      "python3",
      [STEER_MASTER, "--harness", "pi"],
      { cwd: payload.cwd, encoding: "utf8", timeout: STEER_TIMEOUT_MS },
      (error, stdout) => {
        if (error || !stdout.trim()) {
          resolve(undefined);
          return;
        }
        try {
          resolve(JSON.parse(stdout));
        } catch {
          resolve(undefined);
        }
      },
    );
    child.stdin?.end(JSON.stringify(payload));
  });
}

export default function (pi: any) {
  pi.on("tool_call", async (event: any, ctx: any) => {
    try {
      const call = steerToolCall(event);
      if (!call) return undefined;
      const payload: any = {
        hook_event_name: "PreToolUse",
        tool_name: call.tool_name,
        tool_input: call.tool_input,
        cwd: ctx.cwd,
        session_id: ctx.sessionManager.getSessionId(),
        transcript_path: ctx.sessionManager.getSessionFile(),
        model: ctx.model?.id ?? "",
        // steerToolCall reduces the native call to the Claude shape every guard
        // is written against, which drops the fields Pi alone sends. The audit
        // keeps the call as it arrived beside the reduced form.
        steer_original_call: { tool_name: event?.toolName, tool_input: event?.input },
      };
      if (event?.toolCallId !== undefined) {
        payload.tool_call_id = event.toolCallId;
      }
      const reply = await runSteer(payload);
      if (ctx.hasUI && typeof reply?.systemMessage === "string") {
        ctx.ui.notify(reply.systemMessage, "info");
      }
      return reply?.block === true ? reply : undefined;
    } catch {
      return undefined;
    }
  });
}
