import { execFile, spawn } from "node:child_process";

const STEER_REVIEW = "__STEER_REVIEW__";
const STEER_BACKFILL = "__STEER_BACKFILL__";
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

export default function (pi: any) {
  pi.on("tool_call", async (event: any, ctx: any) => {
    try {
      if (event?.toolName !== "bash" && event?.toolName !== "powershell") {
        return undefined;
      }
      const reply = await review({
        hook_event_name: "PreToolUse",
        tool_name: "Bash",
        tool_input: { command: event?.input?.command ?? "" },
        cwd: ctx.cwd,
        session_id: ctx.sessionManager.getSessionId(),
        transcript_path: ctx.sessionManager.getSessionFile(),
        model: ctx.model?.id ?? "",
      });
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

  pi.on("turn_end", async (_event: any, ctx: any) => {
    try {
      backfill(ctx.cwd);
    } catch {
      // A resolver failure cannot change the completed turn.
    }
  });
}
