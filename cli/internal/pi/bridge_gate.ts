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

// backstop runs the resolver with a Stop payload, so it first checks the ledgers
// this turn's released review sequences owe. It resolves the reply, or undefined.
function backstop(payload: any, signal: any): Promise<any> {
  return new Promise((resolve) => {
    const child = execFile(
      STEER_BACKFILL,
      [],
      {
        cwd: payload.cwd,
        encoding: "utf8",
        shell: true,
        signal,
        timeout: STEER_BACKFILL_TIMEOUT_MS,
        windowsHide: true,
      },
      (error, stdout) => {
        const line = String(stdout ?? "").trim().split("\n")[0];
        if (error || !line) {
          resolve(undefined);
          return;
        }
        try {
          resolve(JSON.parse(line));
        } catch {
          resolve(undefined);
        }
      },
    );
    child.stdin?.end(JSON.stringify(payload));
  });
}

const BACKSTOP_MESSAGE = "steer-ledger-backstop";

// userTurn names the agent turn: the newest user message on the active branch.
// The backstop's own entries are custom messages, so a refusal keeps the turn.
function userTurn(ctx: any): { id: string; refused: boolean } {
  let refused = false;
  try {
    const branch = ctx.sessionManager.getBranch?.() ?? [];
    for (let i = branch.length - 1; i >= 0; i--) {
      const entry = branch[i];
      if (entry?.type === "custom_message" && entry.customType === BACKSTOP_MESSAGE) {
        refused = true;
      }
      if (entry?.type === "message" && entry.message?.role === "user") {
        return { id: String(entry.id ?? ""), refused };
      }
    }
  } catch {
    // An unreadable session names no turn, and the backstop then refuses nothing.
  }
  return { id: "", refused };
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

// shippedTools names the tools whose registered definition is Pi's own, not an
// extension's override of that name.
function shippedTools(pi: any): string[] {
  try {
    return (pi.getAllTools?.() ?? [])
      .filter((tool: any) => tool?.sourceInfo?.path === `builtin:${tool?.name}`)
      .map((tool: any) => String(tool.name));
  } catch {
    return [];
  }
}

// batchOf names the other tool calls in the assistant message that holds this
// call. Pi runs every tool_call handler of a message before it runs any call,
// and the session manager holds that message by then.
function batchOf(pi: any, ctx: any, toolCallId: any): any {
  try {
    const branch = ctx.sessionManager.getBranch?.() ?? [];
    for (let i = branch.length - 1; i >= 0; i--) {
      const message = branch[i]?.type === "message" ? branch[i].message : undefined;
      if (message?.role !== "assistant" || !Array.isArray(message.content)) continue;
      const calls = message.content.filter((block: any) => block?.type === "toolCall");
      if (!calls.some((block: any) => block?.id === toolCallId)) break;
      const siblings = calls
        .filter((block: any) => block?.id !== toolCallId)
        .map((block: any) => String(block?.name ?? ""));
      const shipped = shippedTools(pi);
      return { found: true, siblings, shipped: siblings.filter((name: string) => shipped.includes(name)) };
    }
  } catch {
    // An unreadable session leaves the batch unknown.
  }
  return { found: false };
}

// tellOperator shows an operator line: a notice in a UI, stderr in print mode.
function tellOperator(ctx: any, message: string): void {
  if (ctx.hasUI) {
    ctx.ui.notify(message, "info");
  } else {
    process.stderr.write(message + "\n");
  }
}

export default function (pi: any) {
  steerRecordLoad(pi);
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
        turn_id: userTurn(ctx).id,
      };
      if (event?.parentToolCallId) {
        payload.parent_tool_call_id = String(event.parentToolCallId);
      } else {
        payload.steer_batch = batchOf(pi, ctx, event?.toolCallId);
      }
      const reply = await review(payload);
      if (typeof reply?.systemMessage === "string") {
        tellOperator(ctx, reply.systemMessage);
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

  // agent_before_settle is Pi's root Stop: a refusal returns an entry with
  // continue: true, and the session's own record of it keeps that to one per turn.
  pi.on("agent_before_settle", async (event: any, ctx: any) => {
    try {
      if (event?.outcome !== "completed") {
        backfill(ctx.cwd);
        return undefined;
      }
      const turn = userTurn(ctx);
      const reply = await backstop(
        {
          hook_event_name: "Stop",
          cwd: ctx.cwd,
          session_id: ctx.sessionManager.getSessionId(),
          transcript_path: ctx.sessionManager.getSessionFile(),
          turn_id: turn.id,
          stop_hook_active: turn.refused,
        },
        ctx.signal,
      );
      if (typeof reply?.systemMessage === "string" && reply.systemMessage) {
        tellOperator(ctx, reply.systemMessage);
      }
      if (reply?.decision !== "block" || typeof reply?.reason !== "string") {
        return undefined;
      }
      return {
        entries: [
          ...(event.entries ?? []),
          { type: "custom_message", customType: BACKSTOP_MESSAGE, content: reply.reason, display: true },
        ],
        continue: true,
      };
    } catch {
      return undefined;
    }
  });

  // An aborted run skips agent_before_settle, so its rows are resolved here.
  pi.on("agent_settled", async (event: any, ctx: any) => {
    try {
      if (event?.aborted === true) {
        backfill(ctx.cwd);
      }
    } catch {
      // A resolver failure cannot change the settled run.
    }
  });
}
