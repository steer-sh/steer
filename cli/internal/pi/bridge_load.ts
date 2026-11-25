import * as steerCrypto from "node:crypto";
import * as steerFs from "node:fs";
import * as steerOs from "node:os";
import * as steerPath from "node:path";
import * as steerUrl from "node:url";

// steerLoadedBytes is this extension's resolved path and its bytes, read when Pi
// imports the module. Pi reuses an imported module across /new, /resume and /fork, so
// a later read could name bytes this runtime does not run.
const steerLoadedBytes: { path: string; content: Buffer } | undefined = (() => {
  try {
    const path = steerFs.realpathSync(steerUrl.fileURLToPath(import.meta.url));
    return { path, content: steerFs.readFileSync(path) };
  } catch {
    return undefined;
  }
})();

// steerRecordLoad writes, at each session start, which bytes of this extension
// Pi loaded: the file's resolved path and the digest of the bytes it imported. Steer
// reads the record to tell a loaded extension from one that is only on disk. The
// record goes to the running user's ~/.steer/state/pi-loads, so a committed bridge
// records on every clone.
function steerRecordLoad(pi: any): void {
  pi.on("session_start", () => {
    try {
      if (!steerLoadedBytes) return undefined;
      const { path, content } = steerLoadedBytes;
      const loads = steerPath.join(steerOs.homedir(), ".steer", "state", "pi-loads");
      const digest = steerCrypto.createHash("sha256").update(content).digest("hex");
      const name = steerCrypto.createHash("sha256").update(path).digest("hex").slice(0, 16) + ".json";
      steerFs.mkdirSync(loads, { recursive: true });
      const tmp = steerPath.join(loads, "." + name + "." + process.pid);
      steerFs.writeFileSync(tmp, JSON.stringify({ path, digest, loaded_at: new Date().toISOString() }) + "\n");
      steerFs.renameSync(tmp, steerPath.join(loads, name));
    } catch {
      // A load Steer cannot record reads as not loaded, never as a failure.
    }
    return undefined;
  });
}
