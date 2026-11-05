"""Runs memory-tool code through the real `om call` over the memory setup.sh built."""
import json
import os
import subprocess


def om_call(om, work, code, timeout=60):
    """Returns (is_error, text) of one memory-tool call with this Go code."""
    env = {"HOME": os.path.join(work, "home"), "OM_HOME": os.path.join(work, "om"),
           "PATH": "/usr/bin:/bin"}
    try:
        r = subprocess.run([om, "call", "--harness", "trial", "--dir", os.path.join(work, "repo")],
                           input=json.dumps({"code": code}), capture_output=True, text=True,
                           env=env, timeout=timeout)
    except subprocess.TimeoutExpired:
        return True, "timeout"
    try:
        j = json.loads(r.stdout)
    except ValueError:
        return True, "om call: " + r.stdout + r.stderr
    return bool(j.get("isError")), j.get("text", "")
