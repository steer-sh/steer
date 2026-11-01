"""
A variable quoted once and rebound before use is no longer quoted
This should be blocked by the hook
"""

import shlex
import subprocess


def clone(repo_url):
    target = shlex.quote(repo_url)
    target = input()
    subprocess.run(f"git clone {target}", shell=True)
