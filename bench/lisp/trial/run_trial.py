"""The Go-vs-Lisp memory-tool trial (docs/lisp-measurements.md, part 2).

  run_trial.py check OM TEMPLATE
      Runs every task's reference Go through the real tool and its reference Lisp through the mock,
      and writes expected.json from the Go output; fails when the two disagree (the mock is wrong).
  run_trial.py ask OM TEMPLATE PIG MODEL OUT [--max-calls N] [--retry]
      Asks the model for each task's Go and Lisp code (one call each, cached in OUT so a rerun makes
      no call it already made), runs and scores them; with --retry, every failed first try gets one
      more call carrying the task, its code and the error or output, while the calls last.
  run_trial.py score OM TEMPLATE OUT
      Rescores the cached replies in OUT (no model call) and prints the tables.

Each Go run builds the memory afresh with setup.sh in a temporary dir, so a definition one reply
keeps never reaches the next (TEMPLATE is unused by the runs and kept for the call shape). The model name is an argument only: it is never written to OUT.
"""
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

from omcall import om_call

HERE = os.path.dirname(os.path.abspath(__file__))
TASKS = json.load(open(os.path.join(HERE, "tasks.json")))
MANUAL = {"go": open(os.path.join(HERE, "manual-go.txt")).read(),
          "lisp": open(os.path.join(HERE, "manual-lisp.txt")).read()}
LANG = {"go": "Go", "lisp": "Common Lisp"}

PROMPT = """You are writing the code argument of one call to the `memory` tool. Its manual:

{manual}

Task: {task}

Reply with only the {lang} code for the tool's code argument: no explanation and no Markdown fence. The code's output must be exactly what the task asks to print, and nothing else."""

RETRY = """Your code for this task did not give the right result.

Task: {task}

Your code:
{code}

What it printed:
{out}

Reply with only the corrected {lang} code: no explanation and no Markdown fence."""


def norm(text):
    lines = [l.rstrip() for l in text.replace("\r", "").split("\n")]
    while lines and not lines[0]:
        lines.pop(0)
    while lines and not lines[-1]:
        lines.pop()
    return "\n".join(lines)


def run_go(om, template, code):
    """Returns (output, class, output): class is ok, syntax, compile or runtime; the Go tool shows
    a last value only for an expression statement, so the alternative rule changes nothing."""
    work = tempfile.mkdtemp(prefix="trial-go-")
    try:
        subprocess.run([os.path.join(HERE, "setup.sh"), om, work], check=True, capture_output=True)
        err, text = om_call(om, work, code)
    finally:
        shutil.rmtree(work, ignore_errors=True)
    out = "\n".join(l for l in text.split("\n")
                    if not l.startswith("kept: ") and not l.startswith("not kept: "))
    if out.strip() == "(no output)":
        out = ""
    if not err:
        return out, "ok", out
    if "syntax error" in text:
        return out, "syntax", out
    if "panic" in text:
        return out, "runtime", out
    return out, "compile", out


def run_lisp(code):
    """Returns (output, class, alternative output): class is ok, paren (an unbalanced read),
    reader (any other read error, an om symbol the package does not export included) or runtime;
    the alternative output is what a tool would show that prints the last value only when the
    code printed nothing (every Lisp form has a value, so the manual's rule echoes e.g. the string
    a final (when ... (princ x)) returns)."""
    with tempfile.NamedTemporaryFile("w", suffix=".lisp", delete=False) as f:
        f.write(code)
    try:
        r = subprocess.run(["sbcl", "--script", os.path.join(HERE, "mock", "run.lisp"), f.name],
                           capture_output=True, text=True, timeout=60)
        status = ([l for l in r.stderr.splitlines() if l.startswith("STATUS ")] or ["STATUS error: no status"])[-1]
        out = r.stdout
    except subprocess.TimeoutExpired:
        status, out = "STATUS error: timeout", "timeout"
    finally:
        os.unlink(f.name)
    m = re.search(r"^PRINTED-CHARS (\d+)$", r.stderr if status != "STATUS error: timeout" else "", re.M)
    printed = out[:int(m.group(1))] if m else out
    # The alternative rule: the value is shown only when the code printed nothing.
    alt = printed if printed.strip() else out
    if status == "STATUS ok":
        return out, "ok", alt
    if status.startswith("STATUS reader-error"):
        return out, "paren" if "end of file" in status.lower() else "reader", alt
    return out, "runtime", alt


def strip_fence(reply):
    m = re.search(r"```[a-zA-Z]*\n(.*?)```", reply, re.S)
    return (m.group(1) if m else reply).strip("\n"), bool(m)


def run(lang, om, template, code):
    return run_go(om, template, code) if lang == "go" else run_lisp(code)


def check(om, template):
    expected, bad = {}, 0
    for t in TASKS:
        go, gc, _ = run_go(om, template, t["go"])
        li, lc, _ = run_lisp(t["lisp"])
        ok = gc == "ok" and lc == "ok" and norm(go) == norm(li)
        bad += not ok
        expected[t["id"]] = norm(go)
        print(t["id"], "ok" if ok else "MISMATCH", repr(norm(go)) if ok else (gc, go, lc, li))
    json.dump(expected, open(os.path.join(HERE, "expected.json"), "w"), indent=1)
    sys.exit(1 if bad else 0)


def ask_model(pig, model, prompt):
    r = subprocess.run([pig, "-p", "--no-session", "--no-tools", "--no-extensions", "--no-mcp",
                        "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-approve",
                        "--model", model, "--thinking", "low", prompt],
                       capture_output=True, text=True, timeout=600, stdin=subprocess.DEVNULL)
    return r.stdout if r.returncode == 0 else "MODEL CALL FAILED: " + r.stderr[-500:]


def score(om, template, out_dir, pig=None, model=None, max_calls=0, retry=False):
    """Two passes, so retries never take a first try's call: every first try, then the retries."""
    expected = json.load(open(os.path.join(HERE, "expected.json")))
    calls = 0
    results = [{"task": t["id"], "lang": lang, "t": t} for t in TASKS for lang in ("go", "lisp")]
    for attempt in ("first", "retry"):
        for res in results:
            t, lang = res["t"], res["lang"]
            if attempt == "retry" and ("first" not in res or res["first"]["correct"]):
                continue
            path = os.path.join(out_dir, "%s.%s.%s.txt" % (t["id"], lang, attempt))
            if not os.path.exists(path):
                if not pig or calls >= max_calls or (attempt == "retry" and not retry):
                    continue
                prompt = PROMPT.format(manual=MANUAL[lang], task=t["task"], lang=LANG[lang])
                if attempt == "retry":
                    prompt += "\n\n" + RETRY.format(task=t["task"], code=res["first"]["code"],
                                                     out=res["first"]["out"][:2000], lang=LANG[lang])
                reply = ask_model(pig, model, prompt)
                calls += 1
                open(path, "w").write(reply)
            code, fenced = strip_fence(open(path).read())
            out, cls, alt = run(lang, om, template, code)
            correct = cls == "ok" and norm(out) == expected[t["id"]]
            res[attempt] = {"code": code, "fenced": fenced, "class": cls if cls != "ok" or correct else "wrong",
                            "correct": correct, "correct_alt": cls == "ok" and norm(alt) == expected[t["id"]],
                            "out": norm(out)[:2000], "chars": len(code)}
    results = [{k: v for k, v in r.items() if k != "t"} for r in results if "first" in r]
    json.dump(results, open(os.path.join(out_dir, "results.json"), "w"), indent=1)
    report(results)
    print("model calls this run:", calls)


def report(results):
    for lang in ("go", "lisp"):
        rs = [r for r in results if r["lang"] == lang]
        if not rs:
            continue
        first = sum(r["first"]["correct"] for r in rs)
        first_alt = sum(r["first"]["correct_alt"] for r in rs)
        after = sum(r["first"]["correct"] or r.get("retry", {}).get("correct", False) for r in rs)
        fenced = sum(r["first"]["fenced"] for r in rs)
        classes = {}
        for r in rs:
            classes[r["first"]["class"]] = classes.get(r["first"]["class"], 0) + 1
        print("%-4s tasks %2d  first-try correct %2d (alternative value rule %2d)  after one retry %2d  "
              "first-try classes %s  fenced %d  mean chars %.0f" % (
                  lang, len(rs), first, first_alt, after, json.dumps(classes, sort_keys=True), fenced,
                  sum(r["first"]["chars"] for r in rs) / len(rs)))
    for r in results:
        print(r["task"], r["lang"], r["first"]["class"], "correct" if r["first"]["correct"] else "",
              ("retry: " + r["retry"]["class"] + (" correct" if r["retry"]["correct"] else "")) if "retry" in r else "")


def main():
    cmd = sys.argv[1]
    if cmd == "check":
        check(os.path.abspath(sys.argv[2]), os.path.abspath(sys.argv[3]))
    elif cmd == "ask":
        args = sys.argv[2:]
        max_calls = 60
        if "--max-calls" in args:
            i = args.index("--max-calls")
            max_calls = int(args[i + 1])
            del args[i:i + 2]
        retry = "--retry" in args
        args = [a for a in args if a != "--retry"]
        om, template, pig, model, out = args
        os.makedirs(out, exist_ok=True)
        score(os.path.abspath(om), os.path.abspath(template), out, pig, model, max_calls, retry)
    elif cmd == "score":
        score(os.path.abspath(sys.argv[2]), os.path.abspath(sys.argv[3]), sys.argv[4])
    else:
        sys.exit(__doc__)


main()
