import { execFile } from "node:child_process";

const STEER_CHECK = "__STEER_CHECK__";
const STEER_CHECK_TIMEOUT_MS = __STEER_CHECK_TIMEOUT_MS__;

function runCheck(payload: any, signal: any): Promise<any> {
  return new Promise((resolve) => {
    const child = execFile(
      STEER_CHECK,
      [],
      {
        cwd: payload.cwd,
        encoding: "utf8",
        shell: true,
        signal,
        timeout: STEER_CHECK_TIMEOUT_MS,
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

function tellOperator(ctx: any, message: string): void {
  if (ctx.hasUI) {
    ctx.ui.notify(message, "warning");
  } else {
    process.stderr.write(message + "\n");
  }
}

export default function (pi: any) {
  // agent_before_settle is Pi's last boundary before it goes idle: retries,
  // compaction and queued messages have run, and a returned entry with
  // continue: true starts one more run with that entry in context.
  pi.on("agent_before_settle", async (event: any, ctx: any) => {
    try {
      if (event?.outcome !== "completed") {
        return undefined;
      }
      const reply = await runCheck(
        {
          hook_event_name: "Stop",
          cwd: ctx.cwd,
          session_id: ctx.sessionManager.getSessionId(),
          transcript_path: ctx.sessionManager.getSessionFile(),
          leaf_id: ctx.sessionManager.getLeafId?.() ?? "",
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
          { type: "custom_message", customType: "steer-check", content: reply.reason, display: true },
        ],
        continue: true,
      };
    } catch {
      return undefined;
    }
  });
}
