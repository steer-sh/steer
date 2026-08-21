"""Tier 1 of the port's differential replay: the layers that are a pure function of
diff text and a path list. Emits the Python side's answers for the Go test to compare."""
import importlib.util, pathlib, json, hashlib, sys
spec = importlib.util.spec_from_file_location("cr", pathlib.Path.home()/".claude/hooks/commit-review.py")
cr = importlib.util.module_from_spec(spec); spec.loader.exec_module(cr)

DIFFS = pathlib.Path.home()/".claude/state/commit-review/diffs"

def section_paths(diff):
    """One path per column-0 `diff --git ` header, taken from the b-side."""
    paths = []
    for line in diff.splitlines():
        if line.startswith("diff --git "):
            rest = line[len("diff --git "):]
            b = rest.split(" b/", 1)
            paths.append(b[1] if len(b) == 2 else rest)
    return paths

BUDGETS = [2000, 20000, 180000]

out = []
for path in sorted(DIFFS.glob("*.diff")):
    diff = path.read_text(errors="replace")
    paths = section_paths(diff)
    names = "".join(p + "\n" for p in paths)
    rec = {
        "name": path.name,
        "diff_sha": hashlib.sha256(diff.encode()).hexdigest()[:16],
        "paths": paths,
        "prose_only": cr.is_prose_only(paths),
        "noise": [p for p in paths if cr.is_noise(p)],
        "cruft": [p for p in paths if cr.is_os_cruft(p)],
        "neutralized_sha": hashlib.sha256(cr.neutralize_tags(diff).encode()).hexdigest(),
        "symbols": sorted(cr.changed_symbols(diff, paths)),
        "budgets": [],
    }
    for budget in BUDGETS:
        old = cr.MAX_DIFF_CHARS
        cr.MAX_DIFF_CHARS = budget
        try:
            kept, dropped = cr.budget_sections(diff, names)
        finally:
            cr.MAX_DIFF_CHARS = old
        rec["budgets"].append({
            "budget": budget,
            "ok": kept is not None,
            "kept_sha": hashlib.sha256(kept.encode()).hexdigest() if kept is not None else "",
            "kept_len": len(kept) if kept is not None else 0,
            "dropped": dropped if dropped is not None else [],
        })
    out.append(rec)

json.dump(out, open(sys.argv[1], "w"))
print(len(out), "diffs replayed")
