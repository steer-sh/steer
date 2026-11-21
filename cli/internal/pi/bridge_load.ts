
import * as steerCrypto from "node:crypto";
import * as steerFs from "node:fs";
import * as steerPath from "node:path";
import * as steerUrl from "node:url";

const STEER_LOADS = "__STEER_LOADS__";

// steerRecordLoad writes, at each session start, which bytes of this extension
// Pi loaded: the file's resolved path and its digest. Steer reads the record to
// tell a loaded extension from one that is only on disk.
function steerRecordLoad(pi: any): void {
  pi.on("session_start", () => {
    try {
      const path = steerFs.realpathSync(steerUrl.fileURLToPath(import.meta.url));
      const digest = steerCrypto.createHash("sha256").update(steerFs.readFileSync(path)).digest("hex");
      const name = steerCrypto.createHash("sha256").update(path).digest("hex").slice(0, 16) + ".json";
      steerFs.mkdirSync(STEER_LOADS, { recursive: true });
      const tmp = steerPath.join(STEER_LOADS, "." + name + "." + process.pid);
      steerFs.writeFileSync(tmp, JSON.stringify({ path, digest, loaded_at: new Date().toISOString() }) + "\n");
      steerFs.renameSync(tmp, steerPath.join(STEER_LOADS, name));
    } catch {
      // A load Steer cannot record reads as not loaded, never as a failure.
    }
    return undefined;
  });
}
