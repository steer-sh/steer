#!/usr/bin/env python3
"""Write correction_list_golden.json: the reference scoring of synthetic turns.

Run from this directory with python3. TestCorrectionScoreMatchesReference reads the
output, so a change to the list regenerates it here.
"""
import hashlib
import json
import re

CUT_ABOVE, CUT_HEAD, CUT_TAIL = 1500, 1000, 400
CUT_MARK = " …[cut]… "

with open("../correction_list.json", encoding="utf-8") as f:
    LIST = json.load(f)


def truncate(text):
    if len(text) <= CUT_ABOVE:
        return text
    return text[:CUT_HEAD] + CUT_MARK + text[-CUT_TAIL:]


def features(text, tools):
    tokens = re.findall(r"[a-z0-9']+", text.lower())
    found = set()
    for n in range(1, 6):
        for j in range(len(tokens) - n + 1):
            found.add(" ".join(tokens[j : j + n]))
    for n in range(1, 5):
        if len(tokens) >= n:
            found.add("^" + " ".join(tokens[:n]))
    c = len(tokens)
    found.add("len:1-5" if c <= 5 else "len:6-15" if c <= 15 else "len:16-40" if c <= 40
              else "len:41-100" if c <= 100 else "len:101-300" if c <= 300 else "len:301+")
    n = text.count("\n") + 1
    found.add("lines:1" if n == 1 else "lines:2-5" if n <= 5 else "lines:6-20" if n <= 20 else "lines:21+")
    if text.strip().endswith("?"):
        found.add("end:?")
    found.add("tools:any" if tools else "tools:0")
    for tool in tools:
        found.add("tool:" + tool.split(":")[0])
    return found


def words(n):
    return " ".join(f"w{i}" for i in range(n))


def lines(n):
    return "\n".join(["line"] * n)


CASES = [
    # Token count buckets, each edge from both sides.
    ("no tokens", "?!", []),
    ("len 5", words(5), []),
    ("len 6", words(6), []),
    ("len 15", words(15), []),
    ("len 16", words(16), []),
    ("len 40", words(40), []),
    ("len 41", words(41), []),
    ("len 100", words(100), []),
    ("len 101", words(101), []),
    ("len 300", words(300), []),
    ("len 301", words(301), []),
    # Line count buckets.
    ("lines 1", lines(1), []),
    ("lines 2", lines(2), []),
    ("lines 5", lines(5), []),
    ("lines 6", lines(6), []),
    ("lines 20", lines(20), []),
    ("lines 21", lines(21), []),
    # Opening n-grams and n-gram lengths.
    ("opening one", "no", ["Bash"]),
    ("opening four", "why did you not rename the helper", []),
    ("opening past four", "what are you doing with the build", []),
    ("five gram", "that is not what i asked for", []),
    ("apostrophe", "that's not what i wanted, you didn't read it", ["Read"]),
    # The question mark reads the trimmed end.
    ("question", "why is this file here?", []),
    ("question then space", "why is this file here?  \n\t", []),
    ("question then separator", "why is this file here?\x1c", []),
    ("question inside", "why? this file is here", []),
    # Tool features: duplicates, a cut name, and any name the list does not hold.
    ("one tool", "rename the helper", ["Edit"]),
    ("three tools", "rename the helper", ["Bash", "Read", "Edit"]),
    ("repeated tool", "rename the helper", ["Bash", "Bash", "Bash"]),
    ("tool with suffix", "rename the helper", ["exec_command:ls -la", "Bash:x"]),
    ("unknown tool", "rename the helper", ["SomeOtherTool"]),
    # Truncation by code point, on each side of the limit, with text in the dropped middle.
    ("at the limit", "wrong " + "x" * 1494, []),
    ("over the limit", "wrong " + "x" * 1495, []),
    ("cut drops the middle", "y" * 1100 + " you forgot " + "y" * 1100, []),
    ("cut keeps the tail", "y" * 1200 + " you forgot it", []),
    ("cut keeps the lines", "y" * 1200 + "\n" * 300 + "z" * 200, []),
    ("cut counts code points", "é" * 1100 + " you forgot " + "é" * 500, []),
    # Lowercasing where Python and Go could differ in ASCII.
    ("upper case", "STOP DOING THAT, IT IS WRONG", []),
    ("dotted capital I", "İnstead of the build", []),
    ("dotted capital I inside", "wİll stop", []),
    ("kelvin sign", "Keep doing it", []),
    # The flag threshold: below, exactly on, and above it.
    ("just below the threshold", "that is wrong", ["Read"]),
    ("on the threshold", "that was wrong", ["Read"]),
    ("just above the threshold", "instead stop", ["Bash"]),
]


def row(name, text, tools):
    found = features(truncate(text), tools)
    score = LIST["bias"] + sum(LIST["weights"].get(f, 0) for f in found)
    return {
        "name": name,
        "text": text,
        "tools": tools,
        "features": len(found),
        "features_sha256": hashlib.sha256("\n".join(sorted(found)).encode()).hexdigest(),
        "listed": sorted(f for f in found if f in LIST["weights"]),
        "score": score,
        "flagged": score > 1e-9,
    }


with open("correction_list_golden.json", "w", encoding="utf-8") as f:
    json.dump([row(*case) for case in CASES], f, ensure_ascii=True, indent=1)
    f.write("\n")
