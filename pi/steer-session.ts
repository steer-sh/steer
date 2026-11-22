import { execFile } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// The launcher of the package copy that holds this file.
const STEER_LAUNCHER = join(dirname(dirname(fileURLToPath(import.meta.url))), "scripts", "steer");
const STEER_CADENCE_TIMEOUT_MS = 10000;

// cadence runs the package copy's session-start entry point, which installs a
// newer copy as the stable executable and names pending work. It resolves to the
// parsed reply, or undefined on any failure.
function cadence(cwd: string): Promise<any> {
  return new Promise((resolve) => {
    execFile(
      STEER_LAUNCHER,
      ["report", "cadence"],
      {
        cwd,
        encoding: "utf8",
        env: { ...process.env, STEER_HARNESS: "pi" },
        timeout: STEER_CADENCE_TIMEOUT_MS,
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
  });
}

export default function (pi: any) {
  // The session's cadence context, until the first run takes it.
  let pending: Promise<string | undefined> | undefined;

  // The handler does not wait for the cadence, so startup never waits on an install.
  pi.on("session_start", (_event: any, ctx: any) => {
    pending = cadence(ctx.cwd).then((reply) => {
      try {
        if (ctx.hasUI && typeof reply?.systemMessage === "string" && reply.systemMessage) {
          ctx.ui.notify(reply.systemMessage, "info");
        }
      } catch {
        // A session that ended before the cadence answered shows nothing.
      }
      const context = reply?.hookSpecificOutput?.additionalContext;
      return typeof context === "string" && context ? context : undefined;
    });
    return undefined;
  });

  // The first run waits for a cadence still running, so its context reaches the
  // first prompt, as a SessionStart hook's does.
  pi.on("before_agent_start", async () => {
    const waiting = pending;
    pending = undefined;
    const context = waiting && (await waiting);
    if (!context) {
      return undefined;
    }
    return { message: { customType: "steer-session", content: context, display: false } };
  });
}
