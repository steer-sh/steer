# Steer master-hook runtime. Spliced into the generated master hook, so it
# declares no imports and no entrypoint: datetime, io, json, os and sys are
# guaranteed by the generated file's import block.

STEER_CLAUDE = "claude-code"
STEER_CODEX = "codex"


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
    """Walk up from cwd to the directory holding .git."""
    try:
        path = os.path.abspath(cwd or "")
    except Exception:
        return ""
    while True:
        if os.path.isdir(os.path.join(path, ".git")):
            return path
        parent = os.path.dirname(path)
        if parent == path:
            return ""
        path = parent


def steer_patch_sections(patch_text):
    """Split an apply_patch document into (op, path, added_content) triples.

    apply_patch carries its whole multi-file patch in one string, so a guard that
    reads it as a command would inspect diff syntax rather than the code landing.
    Only the added and replacement lines are content; context and removed lines
    are what the file already survived.
    """
    sections = []
    op = ""
    path = ""
    added = []

    def flush():
        if op:
            sections.append((op, path, "\n".join(added)))

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
            continue
        if line.startswith("*** Move to: "):
            # The patch renames as it edits; the target path is what lands.
            path = line[len("*** Move to: "):].strip()
            continue
        if line.startswith("*** "):
            continue
        if op and line.startswith("+"):
            added.append(line[1:])

    flush()
    return sections


def steer_edit(payload, tool_name, file_path, key, value):
    """Build one virtual edit: the payload's envelope with a rewritten tool call."""
    edit = dict(payload)
    edit["tool_name"] = tool_name
    tool_input = {}
    if file_path:
        tool_input["file_path"] = file_path
    tool_input[key] = value
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
        for op, path, added in steer_patch_sections(command):
            if op == "add":
                edits.append(steer_edit(payload, "Write", path, "content", added))
            else:
                edits.append(steer_edit(payload, "Edit", path, "new_string", added))
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
            edits.append(steer_edit(payload, "Edit", file_path, "new_string", new_string))
        # A guard that only ever handled Edit now sees every hunk of a MultiEdit.
        return edits

    return [payload]


def steer_audit_log(hook_func_name, payload, edit, harness):
    """Append one JSON line per block decision to steer-audit.jsonl.

    Auditing must never break or weaken the block decision, so every failure path
    is swallowed.
    """
    try:
        meta = STEER_HOOK_META.get(hook_func_name, {})
        if not isinstance(payload, dict):
            payload = {}
        if not isinstance(edit, dict):
            edit = {}
        tool_input = edit.get("tool_input")
        if not isinstance(tool_input, dict):
            tool_input = {}
        snippet = ""
        for key in ("command", "content", "new_string"):
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
            "hook_name": meta.get("hook_name", hook_func_name),
            "enforcement": "block",
            "harness": harness,
            "session_id": session_id,
            "thread_id": thread_id,
            "repository": steer_repo_root(cwd),
            "file_path": file_path,
            "tool_name": tool_name,
            "snippet": snippet,
        }
        audit_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "steer-audit.jsonl")
        with open(audit_path, "a", encoding="utf-8") as handle:
            handle.write(json.dumps(entry) + "\n")
    except Exception:
        pass


def steer_removal_footer(hook_func_name):
    """Name the hook that blocked and the command that removes it."""
    meta = STEER_HOOK_META.get(hook_func_name, {})
    hook_name = meta.get("hook_name", hook_func_name)
    source = meta.get("source", "")
    source_id = meta.get("source_id", "")
    if not source:
        return "\n\n— steer hook: " + hook_name
    if source == "factory":
        cmd = "steer preset disable " + (source_id or hook_name)
    else:
        cmd = "steer remove " + hook_name
    return "\n\n— steer hook: " + hook_name + " · remove: " + cmd


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


def steer_render_block(output, result, hook_func_name, harness):
    """Render a guard's block in the harness's native deny contract."""
    footer = steer_removal_footer(hook_func_name)
    if harness == STEER_CODEX:
        reason = output
        if result is not None:
            reason = ""
            for key in ("stopReason", "reason"):
                value = result.get(key)
                if isinstance(value, str) and value:
                    reason = value
                    break
        return json.dumps({
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": "deny",
                "permissionDecisionReason": (reason + footer) if reason else footer.lstrip(),
            }
        })
    if result is None:
        # An undecodable reply is passed through untouched, exactly as it was
        # before a footer could be attached to it.
        return output
    reason = result.get("stopReason")
    if isinstance(reason, str):
        result["stopReason"] = reason + footer
    else:
        result["stopReason"] = footer.lstrip()
    return json.dumps(result)


def steer_run(hooks_to_run):
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
    for hook_func in hooks_to_run:
        for edit, edit_json in encoded:
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
            steer_audit_log(hook_func.__name__, payload, edit, harness)
            print(steer_render_block(output, result, hook_func.__name__, harness))
            sys.exit(0)

    sys.exit(0)
