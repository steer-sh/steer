import { execFile, spawn } from "node:child_process";

const STEER_REVIEW = "__STEER_REVIEW__";
const STEER_BACKFILL = "__STEER_BACKFILL__";
const STEER_GUIDANCE = "__STEER_GUIDANCE__";
const STEER_REVIEW_TIMEOUT_MS = __STEER_REVIEW_TIMEOUT_MS__;
const STEER_BACKFILL_TIMEOUT_MS = __STEER_BACKFILL_TIMEOUT_MS__;

function review(payload: any): Promise<any> {
  return new Promise((resolve) => {
    const child = execFile(
      STEER_REVIEW,
      [],
      {
        cwd: payload.cwd,
        encoding: "utf8",
        shell: true,
        timeout: STEER_REVIEW_TIMEOUT_MS,
        windowsHide: true,
      },
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

function backfill(cwd: string): void {
  const child = spawn(STEER_BACKFILL, [], {
    cwd,
    detached: true,
    shell: true,
    stdio: "ignore",
    timeout: STEER_BACKFILL_TIMEOUT_MS,
    windowsHide: true,
  });
  child.unref();
}

// batchOf names the other tool calls in the assistant message that holds this
// call. Pi runs every tool_call handler of a message before it runs any call,
// and the session manager holds that message by then.
function batchOf(ctx: any, toolCallId: any): any {
  try {
    const branch = ctx.sessionManager.getBranch?.() ?? [];
    for (let i = branch.length - 1; i >= 0; i--) {
      const message = branch[i]?.type === "message" ? branch[i].message : undefined;
      if (message?.role !== "assistant" || !Array.isArray(message.content)) continue;
      const calls = message.content.filter((block: any) => block?.type === "toolCall");
      if (!calls.some((block: any) => block?.id === toolCallId)) break;
      return {
        found: true,
        siblings: calls
          .filter((block: any) => block?.id !== toolCallId)
          .map((block: any) => String(block?.name ?? "")),
      };
    }
  } catch {
    // An unreadable session leaves the batch unknown.
  }
  return { found: false };
}

export default function (pi: any) {
  pi.on("tool_call", async (event: any, ctx: any) => {
    try {
      if (event?.toolName !== "bash" && event?.toolName !== "powershell") {
        return undefined;
      }
      const payload: any = {
        hook_event_name: "PreToolUse",
        tool_name: "Bash",
        tool_input: { command: event?.input?.command ?? "" },
        cwd: ctx.cwd,
        session_id: ctx.sessionManager.getSessionId(),
        transcript_path: ctx.sessionManager.getSessionFile(),
        model: ctx.model?.id ?? "",
      };
      if (event?.parentToolCallId) {
        payload.parent_tool_call_id = String(event.parentToolCallId);
      } else {
        payload.steer_batch = batchOf(ctx, event?.toolCallId);
      }
      const reply = await review(payload);
      if (ctx.hasUI && typeof reply?.systemMessage === "string") {
        ctx.ui.notify(reply.systemMessage, "info");
      }
      if (typeof reply?.additionalContext === "string") {
        pi.sendMessage(
          {
            customType: "steer-commit-gate",
            content: reply.additionalContext,
            display: false,
          },
          { deliverAs: "steer" },
        );
      }
      if (reply?.block === true) {
        return { block: true, reason: reply.reason };
      }
      return undefined;
    } catch {
      return undefined;
    }
  });

  pi.on("before_agent_start", async (event: any) => {
    try {
      const sections = event?.systemPromptOptions?.sections;
      if (sections && typeof sections === "object") {
        sections["steer-commit-gate"] = STEER_GUIDANCE;
      }
    } catch {
      // A prompt without the guidance still reaches the gate.
    }
    return undefined;
  });

  pi.on("turn_end", async (_event: any, ctx: any) => {
    try {
      backfill(ctx.cwd);
    } catch {
      // A resolver failure cannot change the completed turn.
    }
  });
}
