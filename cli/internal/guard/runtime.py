# Steer master-hook runtime. Spliced into the generated master hook, so it
# declares no imports and no entrypoint: ast, datetime, hashlib, io, json, os,
# stat and sys are guaranteed by the generated file's import block.

STEER_CLAUDE = "claude-code"
STEER_CODEX = "codex"
STEER_PI = "pi"


def steer_harness():
    """Read the harness from the command line. Claude Code is the default."""
    argv = sys.argv[1:]
    for index, arg in enumerate(argv):
        if arg == "--harness" and index + 1 < len(argv):
            return argv[index + 1]
        if arg.startswith("--harness="):
            return arg.split("=", 1)[1]
    return STEER_CLAUDE


def steer_repo_root(cwd):
    """Walk up from cwd to the directory holding .git.

    A linked worktree or a submodule carries .git as a file naming its git
    directory, so a file marks a root as a directory does.
    """
    try:
        path = os.path.abspath(cwd or "")
    except Exception:
        return ""
    while True:
        marker = os.path.join(path, ".git")
        if os.path.isdir(marker) or os.path.isfile(marker):
            return path
        parent = os.path.dirname(path)
        if parent == path:
            return ""
        path = parent


def steer_patch_sections(patch_text):
    """Split an apply_patch document into (op, path, added, removed) tuples.

    apply_patch carries its whole multi-file patch in one string, so a guard that
    reads it as a command would inspect diff syntax rather than the code landing.
    added joins a section's + lines and removed its - lines, each in patch order
    across every hunk; context lines are in neither.
    """
    sections = []
    op = ""
    path = ""
    added = []
    removed = []

    def flush():
        if op:
            sections.append((op, path, "\n".join(added), "\n".join(removed)))

    for line in (patch_text or "").split("\n"):
        header = None
        for marker, kind in (
            ("*** Add File: ", "add"),
            ("*** Update File: ", "update"),
            ("*** Delete File: ", "delete"),
        ):
            if line.startswith(marker):
                header = (kind, line[len(marker):].strip())
                break
        if header:
            flush()
            op, path = header
            added = []
            removed = []
            continue
        if line.startswith("*** Move to: "):
            # The patch renames as it edits; the target path is what lands.
            path = line[len("*** Move to: "):].strip()
            continue
        if line.startswith("*** "):
            continue
        if op and line.startswith("+"):
            added.append(line[1:])
        elif op and line.startswith("-"):
            removed.append(line[1:])

    flush()
    return sections


def steer_edit(payload, tool_name, file_path, fields):
    """Build one virtual edit: the payload's envelope with a rewritten tool call.

    fields holds the tool_input keys after file_path, which is omitted when empty.
    """
    edit = dict(payload)
    edit["tool_name"] = tool_name
    tool_input = {}
    if file_path:
        tool_input["file_path"] = file_path
    tool_input.update(fields)
    edit["tool_input"] = tool_input
    return edit


def steer_virtual_edits(payload):
    """Normalize one hook payload into per-file virtual edits.

    A guard is written against Claude's Edit / Write / Bash shapes. Every input a
    harness can send is expressed in those shapes here, so one guard covers both
    harnesses and every file a single call touches.
    """
    tool_name = payload.get("tool_name")
    if not isinstance(tool_name, str):
        tool_name = ""
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        tool_input = {}

    if tool_name == "apply_patch":
        command = tool_input.get("command")
        if not isinstance(command, str):
            command = ""
        edits = []
        for op, path, added, removed in steer_patch_sections(command):
            if op == "add":
                fields = {"content": added}
                edits.append(steer_edit(payload, "Write", path, fields))
            elif op == "delete":
                # The deleted content is never read from disk; a guard that
                # needs it opens the file itself.
                fields = {"steer_operation": "delete", "new_string": ""}
                edits.append(steer_edit(payload, "Edit", path, fields))
            else:
                fields = {"old_string": removed, "new_string": added}
                edits.append(steer_edit(payload, "Edit", path, fields))
        return edits

    if tool_name == "MultiEdit":
        file_path = tool_input.get("file_path")
        if not isinstance(file_path, str):
            file_path = ""
        entries = tool_input.get("edits")
        if not isinstance(entries, list):
            return [payload]
        edits = []
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            new_string = entry.get("new_string")
            if not isinstance(new_string, str):
                new_string = ""
            fields = {}
            for key in ("old_string", "replace_all"):
                if key in entry:
                    fields[key] = entry[key]
            fields["new_string"] = new_string
            edits.append(steer_edit(payload, "Edit", file_path, fields))
        # A guard that only ever handled Edit now sees every hunk of a MultiEdit.
        return edits

    return [payload]


STEER_EVIDENCE_INLINE_LIMIT = 16384
# The version of the contest instruction a block's footer carries. A row
# without it was written by a master whose footer offered no contest.
STEER_CONTEST_PROTOCOL = 1
STEER_PAYLOADS_DIR = "steer-audit-payloads"

# Every identifier a harness can supply for the call being blocked. A key that
# did not arrive is left out of the row, so a reader tells "not supplied" from
# "supplied empty".
STEER_LOCATOR_KEYS = (
    "tool_use_id",
    "tool_call_id",
    "call_id",
    "turn_id",
    "transcript_path",
    "session_id",
    "agent_id",
    "thread_id",
    "cwd",
)


def steer_master_build_digest():
    """Read the digest of the master build that is running.

    The constant is written by the generator, so a runtime loaded any other way
    reports no build rather than failing.
    """
    return globals().get("STEER_MASTER_BUILD_DIGEST", "")


def steer_locators(payload):
    """Collect the call identifiers the payload supplied, by key presence."""
    locators = {}
    for key in STEER_LOCATOR_KEYS:
        if key in payload:
            locators[key] = payload[key]
    return locators


def steer_descriptor_writes():
    """Use directory-relative no-follow writes where the platform supports them."""
    return (hasattr(os, "O_NOFOLLOW") and hasattr(os, "O_DIRECTORY")
            and os.open in os.supports_dir_fd)


def steer_unlinked_path(directory, name):
    path = os.path.join(directory, name)
    if os.path.islink(path):
        raise OSError("audit path is a symlink")
    return path


def steer_open_store(store_dir, scope):
    """Anchor writes in the master's store, refusing repository-carried links."""
    parent = os.path.dirname(store_dir)
    if not steer_descriptor_writes():
        if scope != "global" and os.path.islink(parent):
            raise OSError("store parent is a symlink")
        steer_unlinked_path(parent, os.path.basename(store_dir))
        return store_dir
    flags = os.O_RDONLY | os.O_DIRECTORY
    if scope == "global":
        parent_fd = os.open(parent, flags)
    else:
        root_fd = os.open(os.path.dirname(parent), flags)
        try:
            parent_fd = os.open(os.path.basename(parent), flags | os.O_NOFOLLOW,
                                dir_fd=root_fd)
        finally:
            os.close(root_fd)
    try:
        return os.open(os.path.basename(store_dir), flags | os.O_NOFOLLOW,
                       dir_fd=parent_fd)
    finally:
        os.close(parent_fd)


def steer_write_sidecar(data, name, store):
    """Retain a whole sidecar without opening an existing temporary file."""
    if isinstance(store, str):
        directory = steer_unlinked_path(store, STEER_PAYLOADS_DIR)
        os.makedirs(directory, exist_ok=True)
        target = steer_unlinked_path(directory, name)
        temp = steer_unlinked_path(directory, name + ".part")
        handle = open(temp, "xb")
        try:
            with handle:
                handle.write(data)
            os.replace(temp, target)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)
        return
    try:
        os.mkdir(STEER_PAYLOADS_DIR, dir_fd=store)
    except FileExistsError:
        pass
    directory_fd = os.open(STEER_PAYLOADS_DIR,
                           os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                           dir_fd=store)
    try:
        try:
            info = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
            if stat.S_ISLNK(info.st_mode):
                raise OSError("evidence target is a symlink")
        except FileNotFoundError:
            pass
        temp = name + ".part"
        fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                     0o600, dir_fd=directory_fd)
        try:
            with os.fdopen(fd, "wb") as handle:
                handle.write(data)
            os.replace(temp, name, src_dir_fd=directory_fd, dst_dir_fd=directory_fd)
        finally:
            try:
                os.unlink(temp, dir_fd=directory_fd)
            except FileNotFoundError:
                pass
    finally:
        os.close(directory_fd)


def steer_store_evidence(entry, evidence, store):
    """Put the evidence bundle inline, or in a sidecar when it is too large.

    The bundle is the payload as it arrived and the virtual edit the guard
    objected to. A sidecar is written whole and renamed into place before the row
    naming it is appended, so a row never references a partial file. A failure to
    retain the bundle marks the row incomplete rather than dropping the firing.
    """
    try:
        data = json.dumps(evidence, ensure_ascii=False).encode("utf-8")
    except Exception:
        entry["evidence_complete"] = False
        entry["evidence_error"] = "encode"
        return
    entry["evidence_bytes"] = len(data)
    entry["evidence_sha256"] = hashlib.sha256(data).hexdigest()
    if len(data) <= STEER_EVIDENCE_INLINE_LIMIT:
        entry["evidence"] = evidence
        entry["evidence_complete"] = True
        return
    name = (entry.get("firing_id") or entry["evidence_sha256"]) + ".json"
    try:
        steer_write_sidecar(data, name, store)
    except Exception:
        entry["evidence_complete"] = False
        entry["evidence_error"] = "write"
        return
    entry["evidence_ref"] = STEER_PAYLOADS_DIR + "/" + name
    entry["evidence_complete"] = True


def steer_new_firing_id():
    """A fresh firing identifier, or "" when none can be allocated."""
    try:
        return os.urandom(16).hex()
    except Exception:
        return ""


def steer_open_audit(store):
    if isinstance(store, str):
        path = steer_unlinked_path(store, "steer-audit.jsonl")
        if os.path.exists(path) and not stat.S_ISREG(os.stat(path).st_mode):
            raise OSError("audit log is not a regular file")
        return open(path, "a", encoding="utf-8")
    fd = os.open("steer-audit.jsonl", os.O_WRONLY | os.O_APPEND | os.O_CREAT
                 | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600, dir_fd=store)
    return os.fdopen(fd, "a", encoding="utf-8")


def steer_audit_torn(store):
    """True when the audit log ends mid-line, as a crash during an earlier append
    leaves it. Any failure to read it answers False."""
    try:
        flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0)
        if isinstance(store, str):
            fd = os.open(steer_unlinked_path(store, "steer-audit.jsonl"), flags)
        else:
            fd = os.open("steer-audit.jsonl", flags, dir_fd=store)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or info.st_size == 0:
                return False
            os.lseek(fd, info.st_size - 1, os.SEEK_SET)
            return os.read(fd, 1) != b"\n"
        finally:
            os.close(fd)
    except Exception:
        return False


def steer_audit_log(hook_name, payload, edit, harness, edit_index=None, firing_id=""):
    """Append one JSON line per block decision to steer-audit.jsonl.

    firing_id is the identifier the block's footer shows. When the caller has
    none, the row gets its own when one can be allocated, and no
    contest_protocol, because its footer offered no contest. Auditing must never
    break or weaken the block decision, so every failure path is swallowed.
    """
    try:
        meta = STEER_HOOK_META.get(hook_name, {})
        if not isinstance(payload, dict):
            payload = {}
        if not isinstance(edit, dict):
            edit = {}
        tool_input = edit.get("tool_input")
        if not isinstance(tool_input, dict):
            tool_input = {}
        snippet = ""
        for key in ("command", "content", "new_string", "old_string"):
            value = tool_input.get(key)
            if isinstance(value, str) and value.strip():
                snippet = value[:200]
                break
        file_path = tool_input.get("file_path")
        if not isinstance(file_path, str):
            file_path = ""
        tool_name = payload.get("tool_name")
        if not isinstance(tool_name, str):
            tool_name = ""
        cwd = payload.get("cwd")
        if not isinstance(cwd, str):
            cwd = ""
        session_id = payload.get("session_id")
        if not isinstance(session_id, str):
            session_id = ""
        thread_id = ""
        for key in ("agent_id", "thread_id"):
            value = payload.get(key)
            if isinstance(value, str) and value:
                thread_id = value
                break
        entry = {
            "timestamp": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "rule": meta.get("rule", ""),
            "hook_name": meta.get("hook_name", hook_name),
            "enforcement": "block",
            "harness": harness,
            "session_id": session_id,
            "thread_id": thread_id,
            "repository": steer_repo_root(cwd),
            "file_path": file_path,
            "tool_name": tool_name,
            "snippet": snippet,
        }
        if firing_id:
            entry["firing_id"] = firing_id
            entry["contest_protocol"] = STEER_CONTEST_PROTOCOL
        else:
            # A row without an identifier is read by its digest, as a row
            # written before identifiers existed is.
            own = steer_new_firing_id()
            if own:
                entry["firing_id"] = own
        # The guard source and the rule it enforces identify what ran; the build
        # digest identifies the master it ran inside. A stamp an older master
        # never wrote is left out.
        for key in ("hook_digest", "rule_hash"):
            value = meta.get(key, "")
            if value:
                entry[key] = value
        build = steer_master_build_digest()
        if build:
            entry["master_build_digest"] = build
        locators = steer_locators(payload)
        if locators:
            entry["locators"] = locators
        evidence = {"payload": payload, "edit": edit}
        if edit_index is not None:
            evidence["edit_index"] = edit_index
        store_dir = os.path.dirname(os.path.abspath(__file__))
        store_fd = steer_open_store(store_dir, meta.get("scope"))
        try:
            steer_store_evidence(entry, evidence, store_fd)
            lead = "\n" if steer_audit_torn(store_fd) else ""
            with steer_open_audit(store_fd) as handle:
                if not stat.S_ISREG(os.fstat(handle.fileno()).st_mode):
                    raise OSError("audit log is not a regular file")
                handle.write(lead + json.dumps(entry) + "\n")
        finally:
            if not isinstance(store_fd, str):
                os.close(store_fd)
    except Exception:
        pass


def steer_cli():
    """The steer command a blocked agent can run: the stable executable when it
    is installed, otherwise the one on PATH."""
    try:
        stable = os.path.join(os.path.expanduser("~"), ".steer", "bin", "steer")
        if os.path.isfile(stable) and os.access(stable, os.X_OK):
            return stable
    except Exception:
        pass
    return "steer"


def steer_block_footer(hook_name, firing_id):
    """Name the hook and the firing, tell the agent how to contest the block,
    and name the removal command as the user's decision."""
    meta = STEER_HOOK_META.get(hook_name, {})
    hook_name = meta.get("hook_name", hook_name)
    source = meta.get("source", "")
    source_id = meta.get("source_id", "")
    cli = steer_cli()
    footer = "\n\n— steer hook: " + hook_name
    if firing_id:
        footer += " · firing: " + firing_id
        footer += ("\nIf this block is wrong for this task, record why: " + cli
                   + " contest " + firing_id + " '<task-specific reason>'. The block stays."
                   + " Continue with a permitted approach; do not repeat or bypass"
                   + " the blocked action.")
    if not source:
        return footer
    if source == "factory":
        cmd = cli + " preset disable " + (source_id or hook_name)
    else:
        cmd = cli + " remove " + hook_name
    if meta.get("scope") == "global":
        cmd += " --scope global"
    return footer + "\nRemoving this hook needs the user's approval: " + cmd


def steer_parse_block(output):
    """Report whether a guard blocked, and its decoded reply when there is one.

    The compact-text fallback exists because a guard may print a block reply this
    runtime cannot decode; refusing to block on a reply that says "block" would
    turn a parse failure into a silent allow.
    """
    try:
        result = json.loads(output)
    except Exception:
        return '"decision":"block"' in "".join(output.split()), None
    if isinstance(result, dict) and result.get("decision") == "block":
        return True, result
    return False, None


def steer_render_block(output, result, hook_name, harness, firing_id=""):
    """Render a guard's block in the harness's native deny contract.

    The reason carries the guard's own explanation and the footer. An
    undecodable reply that still says block becomes the reason verbatim.
    """
    footer = steer_block_footer(hook_name, firing_id)
    reason = output
    if result is not None:
        reason = ""
        for key in ("stopReason", "reason"):
            value = result.get(key)
            if isinstance(value, str) and value:
                reason = value
                break
    reason = (reason + footer) if reason else footer.lstrip()
    system_message = None
    if result is not None and isinstance(result.get("systemMessage"), str):
        system_message = result["systemMessage"]
    if harness == STEER_PI:
        reply = {"block": True, "reason": reason}
        if system_message is not None:
            reply["systemMessage"] = system_message
        return json.dumps(reply)
    reply = {
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "deny",
            "permissionDecisionReason": reason,
        }
    }
    # Claude Code shows the model permissionDecisionReason on a deny and the
    # user systemMessage; Codex rejects fields its event does not support.
    if harness != STEER_CODEX and system_message is not None:
        reply["systemMessage"] = system_message
    return json.dumps(reply)


def steer_load_note(hook_name, reason):
    """Name a guard the master could not load, on stderr: stdout is the reply."""
    try:
        meta = STEER_HOOK_META.get(hook_name, {})
        name = meta.get("hook_name", hook_name)
        sys.stderr.write("steer: guard " + name + " not loaded: " + reason + "\n")
    except Exception:
        pass


def steer_without_entry_call(tree):
    """Drop a module's final bare main() statement, the script entrypoint."""
    if tree.body:
        last = tree.body[-1]
        if (
            isinstance(last, ast.Expr)
            and isinstance(last.value, ast.Call)
            and isinstance(last.value.func, ast.Name)
            and last.value.func.id == "main"
            and not last.value.args
            and not last.value.keywords
        ):
            tree.body = tree.body[:-1]
    return tree


def steer_load(hook_name):
    """Compile one guard into its own module namespace and return its main().

    A guard is a standalone script, so it is loaded rather than rewritten into
    the master's namespace: its constants, helpers and imports keep the names its
    author gave them, nothing inside a string or a comment is renamed, and two
    guards cannot collide.

    A guard may end in a bare top-level main() call rather than an
    "if __name__" guard. That one statement is dropped before the module body
    runs, so loading never calls the guard. Any other entrypoint shape still
    runs here, on an empty stdin with its output discarded, so that it cannot
    answer outside the virtual-edit loop.
    """
    source = STEER_HOOK_SOURCES.get(hook_name)
    if not isinstance(source, str):
        return None
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), hook_name + ".py")
    namespace = {"__name__": "steer_guard_" + hook_name, "__file__": path}
    old_stdout = sys.stdout
    old_stdin = sys.stdin
    sys.stdout = io.StringIO()
    sys.stdin = io.StringIO("")
    try:
        tree = steer_without_entry_call(ast.parse(source, path))
        exec(compile(tree, path, "exec"), namespace)
    except SystemExit:
        pass
    except Exception as exc:
        steer_load_note(hook_name, type(exc).__name__)
        return None
    finally:
        sys.stdout = old_stdout
        sys.stdin = old_stdin
    entry = namespace.get("main")
    if not callable(entry):
        steer_load_note(hook_name, "no callable main")
        return None
    return entry


def steer_run(hook_names):
    harness = steer_harness()
    stdin_data = sys.stdin.read()
    try:
        payload = json.loads(stdin_data)
    except Exception:
        payload = {}
    if not isinstance(payload, dict):
        payload = {}

    edits = steer_virtual_edits(payload)
    encoded = [(edit, json.dumps(edit)) for edit in edits]

    # Guard-major: guards run in a fixed order and the first one to object wins,
    # whichever file of the call it objected to.
    for hook_name in hook_names:
        hook_func = steer_load(hook_name)
        if hook_func is None:
            continue
        for edit_index, (edit, edit_json) in enumerate(encoded):
            captured = io.StringIO()
            old_stdout = sys.stdout
            old_stdin = sys.stdin
            sys.stdout = captured
            sys.stdin = io.StringIO(edit_json)
            try:
                hook_func()
            except SystemExit:
                pass
            except Exception:
                pass
            finally:
                sys.stdout = old_stdout
                sys.stdin = old_stdin

            output = captured.getvalue().strip()
            if not output:
                continue
            blocked, result = steer_parse_block(output)
            if not blocked:
                continue
            firing_id = steer_new_firing_id()
            steer_audit_log(hook_name, payload, edit, harness, edit_index, firing_id)
            print(steer_render_block(output, result, hook_name, harness, firing_id))
            sys.exit(0)

    sys.exit(0)
