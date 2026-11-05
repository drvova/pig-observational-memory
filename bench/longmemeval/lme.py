#!/usr/bin/env python3
"""LongMemEval_s through real PiG sessions with memory v2 (see README.md).

Python 3 standard library only. Subcommands:

  lme.py run    --data longmemeval_s.json --out DIR [selection] [--workers N]
  lme.py report --out DIR
"""

import argparse
import concurrent.futures
import datetime
import hashlib
import json
import os
import queue
import random
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
DEFAULT_EXTENSION = os.path.join(REPO, "extensions", "observational-memory")
DEFAULT_MODEL = "openai-codex/gpt-6-luna"
DEFAULT_AUTH = os.path.expanduser("~/.pig/agent/auth.json")

# Bench conditions (README "Bench conditions"). They are written into the run's shared PiG agent
# directory; the extension's settings stay at their defaults except debugLog.
DEFAULT_WINDOW = 64000      # contextWindow override for the model
DEFAULT_MAX_TOKENS = 16000  # maxTokens override (the shipped 128000 never fits a 64K window)
PIG_RESERVE_TOKENS = 16384  # PiG's default compaction.reserveTokens
PIG_KEEP_RECENT = 20000     # PiG's default compaction.keepRecentTokens

DATE_LAYOUTS = ("%Y/%m/%d (%a) %H:%M", "%Y/%m/%d %H:%M")
TURN_SPACING = datetime.timedelta(seconds=5)
QUESTION_TYPES = ["knowledge-update", "multi-session", "single-session-assistant",
                  "single-session-preference", "single-session-user", "temporal-reasoning"]

# LongMemEval / Mastra actor instructions, verbatim. PiG's own prompt (tools, rules, docs and the
# extension's sections) is kept underneath; nothing else is added.
ACTOR_INSTRUCTIONS = (
    "You are a helpful assistant with access to extensive conversation history. \n"
    "When answering questions, carefully review the conversation history to identify and use any "
    "relevant user preferences, interests, or specific details they have mentioned."
)
JUDGE_SYSTEM = "You are a helpful assistant."
PROBE_PROMPT = "Reply with the single word OK."

# Official LongMemEval judge prompts (evaluate_qa.py get_anscheck_prompt), verbatim.
JUDGE_DEFAULT = ("I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response is equivalent to the correct answer or contains all the intermediate steps to get the correct answer, you should also answer yes. If the response only contains a subset of the information required by the answer, answer no. \n\nQuestion: {}\n\nCorrect Answer: {}\n\nModel Response: {}\n\nIs the model response correct? Answer yes or no only.")
JUDGE_TEMPORAL = ("I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response is equivalent to the correct answer or contains all the intermediate steps to get the correct answer, you should also answer yes. If the response only contains a subset of the information required by the answer, answer no. In addition, do not penalize off-by-one errors for the number of days. If the question asks for the number of days/weeks/months, etc., and the model makes off-by-one errors (e.g., predicting 19 days when the answer is 18), the model's response is still correct. \n\nQuestion: {}\n\nCorrect Answer: {}\n\nModel Response: {}\n\nIs the model response correct? Answer yes or no only.")
JUDGE_KNOWLEDGE_UPDATE = ("I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response contains some previous information along with an updated answer, the response should be considered as correct as long as the updated answer is the required answer.\n\nQuestion: {}\n\nCorrect Answer: {}\n\nModel Response: {}\n\nIs the model response correct? Answer yes or no only.")
JUDGE_PREFERENCE = ("I will give you a question, a rubric for desired personalized response, and a response from a model. Please answer yes if the response satisfies the desired response. Otherwise, answer no. The model does not need to reflect all the points in the rubric. The response is correct as long as it recalls and utilizes the user's personal information correctly.\n\nQuestion: {}\n\nRubric: {}\n\nModel Response: {}\n\nIs the model response correct? Answer yes or no only.")
JUDGE_ABSTENTION = ("I will give you an unanswerable question, an explanation, and a response from a model. Please answer yes if the model correctly identifies the question as unanswerable. The model could say that the information is incomplete, or some other information is given but the asked information is not.\n\nQuestion: {}\n\nExplanation: {}\n\nModel Response: {}\n\nDoes the model correctly identify the question as unanswerable? Answer yes or no only.")

# Settings that change results: a run directory refuses a different value without --force-settings,
# every result.json carries them, and the report flags a result whose values differ from run.json.
RESULT_SETTINGS = ("model", "window", "max_tokens", "reserve_tokens", "keep_recent_tokens", "auto_compaction",
                   "thinking", "judge_thinking", "chunk", "extension", "extension_hash", "pig_version",
                   "actor_instructions", "judge_system")

AUTH_ERROR = re.compile(r"\b(401|403)\b|unauthori[sz]ed|invalid[_ ]grant|refresh[_ ]token|not signed in|sign in|login|expired", re.I)
RATE_ERROR = re.compile(r"\b429\b|rate[_ -]?limit|too many requests|quota|usage limit", re.I)

PRINT_LOCK = threading.Lock()
QUOTA_ERROR = re.compile(r"usage limit|quota", re.I)


class QuotaExhausted(Exception):
    """The plan's usage limit was reached: nothing from this point is a measurement."""


STOP = threading.Event()  # set when auth is lost or the usage limit is reached: no further question starts


def log(msg):
    with PRINT_LOCK:
        print(time.strftime("%H:%M:%S"), msg, flush=True)


# ─── files ────────────────────────────────────────────────────────────────────

def write_json(path, value):
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(value, f, indent=1, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp, path)


def read_json(path, default=None):
    try:
        with open(path) as f:
            return json.load(f)
    except FileNotFoundError:
        return default


def read_jsonl(path):
    out = []
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line:
                    try:
                        out.append(json.loads(line))
                    except ValueError:
                        pass
    except FileNotFoundError:
        pass
    return out


def tree_hash(root):
    """sha256 over the extension's files (paths and bytes), so a code change shows in the settings."""
    h = hashlib.sha256()
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in (".git", "node_modules"))
        for name in sorted(filenames):
            p = os.path.join(dirpath, name)
            if os.path.islink(p) or not os.path.isfile(p):
                continue
            h.update(os.path.relpath(p, root).encode() + b"\0")
            with open(p, "rb") as f:
                h.update(f.read())
            h.update(b"\0")
    return h.hexdigest()[:16]


def git_revision(path):
    try:
        p = subprocess.run(["git", "-C", path, "rev-parse", "HEAD"], capture_output=True, text=True, timeout=30,
                           stdin=subprocess.DEVNULL)
        return p.stdout.strip() if p.returncode == 0 else None
    except (OSError, subprocess.TimeoutExpired):
        return None


def pig_version(pig):
    p = subprocess.run([pig, "--version"], capture_output=True, text=True, timeout=60, stdin=subprocess.DEVNULL)
    return (p.stdout or p.stderr).strip().splitlines()[-1] if (p.stdout or p.stderr).strip() else "unknown"


# ─── dataset ──────────────────────────────────────────────────────────────────

def parse_date(text):
    for layout in DATE_LAYOUTS:
        try:
            return datetime.datetime.strptime(text.strip(), layout).replace(tzinfo=datetime.timezone.utc)
        except ValueError:
            continue
    raise ValueError("unparseable date %r" % text)


def answer_text(q):
    a = q.get("answer")
    return a if isinstance(a, str) else json.dumps(a)


def is_abstention(q):
    return q["question_id"].endswith("_abs")


def chronological_sessions(q):
    dates, sessions = q["haystack_dates"], q["haystack_sessions"]
    if len(dates) != len(sessions):
        raise ValueError("haystack_dates and haystack_sessions differ in length")
    ids = q.get("haystack_session_ids") or [None] * len(sessions)
    order = sorted(range(len(sessions)), key=lambda i: parse_date(dates[i]))  # stable
    return [(parse_date(dates[i]), sessions[i], ids[i]) for i in order]


def chunks_of(q, unit):
    """The haystack as replay chunks, oldest first, and the number of turns whose time was moved.

    A chunk is a list of (role, text, time, evidence). unit "turn": a user turn and the assistant
    turns after it (one PiG exchange, where PiG's auto-compaction checks); "session": one haystack
    session. A turn's time is its session date plus 5 s per turn index, raised to 5 s after the
    previous turn when sessions share a date or overlap, so time never goes backwards. evidence
    marks LongMemEval's has_answer turns, or every turn of an answer session when none is marked."""
    answer_sessions = set(q.get("answer_session_ids") or [])
    marked = any(t.get("has_answer") for s in q["haystack_sessions"] for t in s)
    out, prev, adjusted = [], None, 0
    for date, session, sid in chronological_sessions(q):
        current = []
        for t, turn in enumerate(session):
            text = turn.get("content") or ""
            if not text.strip():
                continue
            role = "assistant" if turn.get("role") == "assistant" else "user"
            if unit == "turn" and role == "user" and current:
                out.append(current)
                current = []
            at = date + t * TURN_SPACING
            if prev is not None and at <= prev:
                at = prev + TURN_SPACING
                adjusted += 1
            prev = at
            evidence = bool(turn.get("has_answer")) if marked else (sid in answer_sessions)
            current.append((role, text, at, evidence))
        if current:
            out.append(current)
    return out, adjusted


def select(questions, args):
    qs = questions
    if args.ids:
        want = [i for i in args.ids.split(",") if i]
        by_id = {q["question_id"]: q for q in qs}
        missing = [i for i in want if i not in by_id]
        if missing:
            raise SystemExit("unknown question ids: " + ",".join(missing))
        qs = [by_id[i] for i in want]
    if args.types:
        types = set(args.types.split(","))
        qs = [q for q in qs if q["question_type"] in types]
    if args.sample:
        rng = random.Random(args.seed)
        qs = rng.sample(qs, min(args.sample, len(qs)))
    if args.stratified:
        groups = {}
        for q in qs:
            groups.setdefault(q["question_type"], []).append(q)
        rr, i = [], 0
        while any(i < len(g) for g in groups.values()):
            for t in sorted(groups):
                if i < len(groups[t]):
                    rr.append(groups[t][i])
            i += 1
        qs = rr
    if args.limit:
        qs = qs[: args.limit]
    return qs


def opaque_dir(salt, qid):
    """A question's directory name. It never contains the question id, because PiG prints the cwd
    and home paths into the actor's system prompt, and an id ending in _abs gives the answer away."""
    return "q" + hashlib.sha256((salt + "\0" + qid).encode()).hexdigest()[:12]


# ─── session file ─────────────────────────────────────────────────────────────

def iso(t):
    return t.strftime("%Y-%m-%dT%H:%M:%S.") + "%03dZ" % (t.microsecond // 1000)


def ms(t):
    return int(t.timestamp() * 1000)


def js_len(s):
    return len(s.encode("utf-16-le")) // 2


def ceil_quarter(chars):
    return (chars + 3) // 4


ZERO_USAGE = {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
              "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}


class SessionFile:
    """A PiG session file the harness appends haystack entries to while no PiG process runs."""

    def __init__(self, path, provider, model_id):
        self.path, self.provider, self.model_id = path, provider, model_id

    def create(self, cwd, start):
        header = {"type": "session", "version": 3, "id": str(uuid.uuid4()), "timestamp": iso(start), "cwd": cwd}
        change = {"type": "model_change", "id": uuid.uuid4().hex[:8], "parentId": None, "timestamp": iso(start),
                  "provider": self.provider, "modelId": self.model_id}
        with open(self.path, "w") as f:
            f.write(json.dumps(header) + "\n")
            f.write(json.dumps(change) + "\n")

    def entries(self):
        return read_jsonl(self.path)

    def session_id(self):
        for e in self.entries():
            if e.get("type") == "session":
                return e.get("id")
        return None

    def append_chunk(self, chunk):
        """Append one chunk; returns the new size and the ids of its evidence entries."""
        entries = self.entries()
        ids = {e.get("id") for e in entries}
        leaf = next((e["id"] for e in reversed(entries) if e.get("type") != "session" and e.get("id")), None)
        lines, evidence = [], []
        for role, text, t, ev in chunk:
            eid = uuid.uuid4().hex[:8]
            while eid in ids:
                eid = uuid.uuid4().hex[:8]
            ids.add(eid)
            if role == "user":
                message = {"role": "user", "content": [{"type": "text", "text": text}], "timestamp": ms(t)}
            else:
                message = {"role": "assistant", "content": [{"type": "text", "text": text}],
                           "api": "openai-codex-responses", "provider": self.provider, "model": self.model_id,
                           "usage": ZERO_USAGE, "stopReason": "stop", "timestamp": ms(t)}
            lines.append({"type": "message", "id": eid, "parentId": leaf, "timestamp": iso(t), "message": message})
            if ev:
                evidence.append(eid)
            leaf = eid
        with open(self.path, "a") as f:
            for e in lines:
                f.write(json.dumps(e, ensure_ascii=False) + "\n")
        return os.path.getsize(self.path), evidence


def message_tokens(m):
    """PiG's chars/4 estimate of one context message (internal/codingagent/compaction EstimateTokens)."""
    role = m.get("role")
    chars = 0
    content = m.get("content")
    if role in ("user", "toolResult"):
        if isinstance(content, str):
            chars = js_len(content)
        else:
            for b in content or []:
                if b.get("type") == "text":
                    chars += js_len(b.get("text", ""))
                elif b.get("type") == "image":
                    chars += 4800
    elif role == "assistant":
        for b in content or []:
            if b.get("type") == "text":
                chars += js_len(b.get("text", ""))
            elif b.get("type") == "thinking":
                chars += js_len(b.get("thinking", ""))
            elif b.get("type") == "toolCall":
                chars += js_len(b.get("name", "")) + js_len(json.dumps(b.get("arguments", {}), separators=(",", ":"), ensure_ascii=False))
    else:
        return 0
    return ceil_quarter(chars)


def latest_compaction(entries):
    body = [e for e in entries if e.get("type") != "session"]
    last = max((i for i, e in enumerate(body) if e.get("type") == "compaction"), default=-1)
    return body, last


def estimate_context(entries):
    """PiG's projected-context estimate with no usage-bearing response after the latest
    compaction (Session.projectedContextUsage / checkCompactionDecision's zero-usage branch):
    the latest compaction's summary plus every context message from its firstKeptEntryId on."""
    body, last = latest_compaction(entries)
    start = 0
    tokens = 0
    if last >= 0:
        comp = body[last]
        tokens += ceil_quarter(js_len(comp.get("summary") or ""))
        kept = comp.get("firstKeptEntryId")
        start = next((i for i, e in enumerate(body) if e.get("id") == kept), last + 1)
    for i, e in enumerate(body[start:], start):
        if i == last:
            continue
        t = e.get("type")
        if t == "message":
            tokens += message_tokens(e.get("message") or {})
        elif t == "custom_message":
            c = e.get("content")
            tokens += ceil_quarter(js_len(c) if isinstance(c, str) else sum(js_len(b.get("text", "")) for b in c or [] if isinstance(b, dict)))
        elif t == "branch_summary":
            tokens += ceil_quarter(js_len(e.get("summary") or ""))
    return tokens


def evidence_location(entries, evidence_ids):
    """Where the evidence turns sit when the actor answers: "tail" (all in the raw turns kept after
    the latest compaction's firstKeptEntryId), "summary" (all removed, so only in memory), "mixed",
    or "none" (no evidence turn, as for most abstention questions)."""
    if not evidence_ids:
        return "none"
    body, last = latest_compaction(entries)
    pos = {e.get("id"): i for i, e in enumerate(body)}
    start = 0
    if last >= 0:
        kept = body[last].get("firstKeptEntryId")
        start = pos.get(kept, last + 1)
    kept_flags = [pos.get(i, -1) >= start for i in evidence_ids]
    if all(kept_flags):
        return "tail"
    if not any(kept_flags):
        return "summary"
    return "mixed"


def system_message_tokens(m):
    """chars/4 of a system message's text sections plus its tool definitions as JSON."""
    sections = m.get("sections") or {}
    chars = sum(js_len(v) for v in sections.values() if isinstance(v, str))
    if not sections and isinstance(m.get("content"), str):
        chars = js_len(m["content"])
    tools = m.get("toolsAdded") or []
    return ceil_quarter(chars) + ceil_quarter(js_len(json.dumps(tools, separators=(",", ":"), ensure_ascii=False)))


def system_messages(entries):
    return [e["message"] for e in entries if e.get("type") == "message" and (e.get("message") or {}).get("role") == "system"]


# ─── PiG processes ────────────────────────────────────────────────────────────

class PigError(Exception):
    pass


class AuthLost(Exception):
    """Auth is no longer ready: the run stops and the question is not recorded as failed."""


def pig_env(home, agent_dir):
    """PIG_HOME is per home (extension store, build cache, docs); the agent directory, which holds
    auth.json, is the run's one shared directory, so every bench process takes the same auth lock."""
    env = dict(os.environ)
    env["PIG_HOME"] = home
    env["PIG_CODING_AGENT_DIR"] = agent_dir
    env["TZ"] = "UTC"
    for k in ("PIG_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_DIR", "PIG_USE_PI_DIRS", "GOFLAGS"):
        env.pop(k, None)
    return env


class RPC:
    """One `pig --mode rpc` process. Every record it writes goes to transcript."""

    def __init__(self, pig, home, agent_dir, cwd, args, transcript):
        self.transcript = open(transcript, "a")
        self.err = open(transcript + ".err", "a")
        self.proc = subprocess.Popen([pig, "--mode", "rpc"] + args, cwd=cwd, env=pig_env(home, agent_dir),
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.err,
                                     text=True, bufsize=1, start_new_session=True)
        self.q = queue.Queue()
        self.events = []
        self.n = 0
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        for line in self.proc.stdout:
            self.q.put(line)
        self.q.put(None)

    def pump(self, until, timeout):
        end = time.time() + timeout
        while time.time() < end:
            try:
                line = self.q.get(timeout=1)
            except queue.Empty:
                continue
            if line is None:
                raise PigError("pig exited (code %s)" % self.proc.poll())
            self.transcript.write(line)
            self.transcript.flush()
            try:
                e = json.loads(line)
            except ValueError:
                continue
            self.events.append(e)
            if until(e):
                return e
        raise PigError("timed out after %ds" % timeout)

    def call(self, cmd, timeout=120):
        self.n += 1
        cid = "c%d" % self.n
        cmd = dict(cmd, id=cid)
        self.proc.stdin.write(json.dumps(cmd) + "\n")
        self.proc.stdin.flush()
        r = self.pump(lambda e: e.get("type") == "response" and e.get("id") == cid, timeout)
        return r

    def close(self, timeout=60):
        try:
            self.proc.stdin.close()
        except OSError:
            pass
        try:
            self.proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            self.kill()
        self._drain()

    def kill(self):
        try:
            os.killpg(self.proc.pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            pass
        try:
            self.proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            pass

    def _drain(self):
        if self.transcript.closed:
            return
        while True:
            try:
                line = self.q.get_nowait()
            except queue.Empty:
                break
            if line is None:
                break
            self.transcript.write(line)
        self.transcript.close()
        self.err.close()


COMMON_FLAGS = ["--no-mcp", "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-themes", "--offline"]


# ─── homes ────────────────────────────────────────────────────────────────────

def write_agent_dir(out, cfg):
    """The run's one PiG agent directory: auth.json (a symlink to --auth, never read), settings.json
    and models.json. Every bench process uses it through PIG_CODING_AGENT_DIR."""
    agent = os.path.join(out, "agent")
    os.makedirs(agent, exist_ok=True)
    link = os.path.join(agent, "auth.json")
    if not os.path.lexists(link):
        os.symlink(cfg["auth"], link)
    settings = {
        "packages": [cfg["extension"]],
        # PiG's defaults, written out so the bench condition is explicit.
        "compaction": {"enabled": cfg["auto_compaction"], "reserveTokens": cfg["reserve_tokens"],
                       "keepRecentTokens": cfg["keep_recent_tokens"]},
        "observational-memory": {"debugLog": True},
    }
    provider, model_id = cfg["model"].split("/", 1)
    models = {"providers": {provider: {"modelOverrides": {model_id: {"contextWindow": cfg["window"], "maxTokens": cfg["max_tokens"]}}}}}
    write_json(os.path.join(agent, "models.json"), models)
    write_json(os.path.join(agent, "settings.json"), settings)
    return agent


def collect_debug(agent_dir, session_id, dest, run_name):
    """Move the extension's debug log of one session (agent/observational-memory/debug/<id>.ndjson)
    into dest, tagged with run_name. Returns its request counts."""
    ddir = os.path.join(agent_dir, "observational-memory", "debug")
    lines = []
    if session_id:
        for name in (session_id + ".ndjson.1", session_id + ".ndjson"):
            p = os.path.join(ddir, name)
            if os.path.exists(p):
                lines += read_jsonl(p)
                os.remove(p)
    with open(dest, "a") as f:
        for e in lines:
            e["benchRun"] = run_name
            f.write(json.dumps(e, ensure_ascii=False) + "\n")
    requests = [e for e in lines if isinstance(e.get("data"), dict) and e["data"].get("request")]
    stages = {}
    for e in requests:
        s = e["data"].get("stage", "?")
        stages[s] = stages.get(s, 0) + 1
    tokens = {k: sum(int(e["data"].get(k) or 0) for e in requests)
              for k in ("inputTokens", "outputTokens", "cacheReadTokens")}
    return {"requests": len(requests), "stages": stages, "tokens": tokens, "lines": len(lines)}


def copy_build(tpl, home):
    for sub in ("cache", "state", "docs"):
        if not os.path.exists(os.path.join(home, sub)) and os.path.exists(os.path.join(tpl, sub)):
            shutil.copytree(os.path.join(tpl, sub), os.path.join(home, sub), symlinks=True)


def prepare_template(out, cfg, agent_dir):
    """A warmed home: its extension build (cache/, state/, docs/) is copied into every question home."""
    tpl = os.path.join(out, "template-home")
    marker = os.path.join(tpl, ".warm")
    if os.path.exists(marker):
        return tpl
    shutil.rmtree(tpl, ignore_errors=True)
    os.makedirs(tpl)
    cwd = os.path.join(out, "template-cwd")
    os.makedirs(cwd, exist_ok=True)
    rpc = RPC(cfg["pig"], tpl, agent_dir, cwd, ["--no-session", "--model", cfg["model"]] + COMMON_FLAGS, os.path.join(out, "template-warm.jsonl"))
    try:
        r = rpc.call({"type": "get_commands"}, timeout=600)
        names = [c.get("name") for c in (r.get("data") or {}).get("commands", [])]
        if "om:status" not in names:
            raise PigError("the extension did not load in the template home: commands %s" % names)
    finally:
        rpc.close()
    open(marker, "w").close()
    log("template home warmed: %s" % tpl)
    return tpl


def prepare_probe(out, cfg, agent_dir, tpl):
    """Measure the system message a real session of this setup sends (PiG's prompt, its default tools
    and the extension's sections), which the replay never creates. One model request per run."""
    path = os.path.join(out, "probe.json")
    done = read_json(path)
    if done:
        return done
    pdir = os.path.join(out, "probe")
    shutil.rmtree(pdir, ignore_errors=True)
    home, cwd = os.path.join(pdir, "home"), os.path.join(pdir, "cwd")
    os.makedirs(home)
    os.makedirs(cwd)
    copy_build(tpl, home)
    provider, model_id = cfg["model"].split("/", 1)
    sf = SessionFile(os.path.join(pdir, "session.jsonl"), provider, model_id)
    sf.create(cwd, datetime.datetime.now(datetime.timezone.utc))
    rpc = RPC(cfg["pig"], home, agent_dir, cwd,
              ["--session", sf.path, "--model", cfg["model"], "--thinking", cfg["thinking"]] + COMMON_FLAGS,
              os.path.join(pdir, "probe.jsonl"))
    try:
        rpc.call({"type": "get_state"}, timeout=300)
        r = rpc.call({"type": "prompt", "message": PROBE_PROMPT}, timeout=300)
        if not r.get("success"):
            raise PigError("probe prompt refused: %s" % r.get("error"))
        rpc.pump(lambda e: e.get("type") == "agent_settled", 600)
    except Exception:
        rpc.kill()
        rpc.close()
        raise
    rpc.close()
    entries = sf.entries()
    sys_msgs = system_messages(entries)
    if not sys_msgs:
        raise PigError("the probe session has no system message")
    m = sys_msgs[-1]
    assistants = [e["message"] for e in entries if e.get("type") == "message" and (e.get("message") or {}).get("role") == "assistant"]
    usage = (assistants[-1].get("usage") or {}) if assistants else {}
    res = {"system_tokens": system_message_tokens(m),
           "sections": {k: js_len(v) for k, v in (m.get("sections") or {}).items() if isinstance(v, str)},
           "tools": sorted(t.get("name") for t in m.get("toolsAdded") or []),
           "prompt_tokens": ceil_quarter(js_len(PROBE_PROMPT)),
           "provider_input": usage.get("input"), "provider_cache_read": usage.get("cacheRead")}
    write_json(path, res)
    shutil.rmtree(os.path.join(home, "cache"), ignore_errors=True)
    log("probe: system message ~%d tokens (chars/4), provider input %s for the probe request" % (res["system_tokens"], res["provider_input"]))
    return res


# ─── one question ─────────────────────────────────────────────────────────────

class Question:
    def __init__(self, out, q, cfg, dirname, agent_dir, system_tokens):
        self.q, self.cfg, self.agent_dir = q, cfg, agent_dir
        self.dirname = dirname
        self.dir = os.path.join(out, "questions", dirname)
        self.home = os.path.join(self.dir, "home")
        self.cwd = os.path.join(self.dir, "cwd")
        self.session = os.path.join(self.dir, "session.jsonl")
        self.state_path = os.path.join(self.dir, "state.json")
        self.logs = os.path.join(self.dir, "logs")
        self.debug_path = os.path.join(self.dir, "debug.ndjson")
        provider, model_id = cfg["model"].split("/", 1)
        self.sf = SessionFile(self.session, provider, model_id)
        self.threshold = cfg["window"] - cfg["reserve_tokens"]
        self.system_tokens = system_tokens

    # state ----------------------------------------------------------------
    def load_state(self):
        st = read_json(self.state_path)
        if st is None:
            st = {"chunks_done": 0, "size": 0, "compactions": [], "compaction_failures": [], "failed_runs": [], "runs": [],
                  "evidence_ids": [], "replay_seconds": 0.0, "answer_seconds": 0.0, "judge_seconds": 0.0, "attempts": 0}
        st.setdefault("failed_runs", [])
        st.setdefault("evidence_ids", [])
        return st

    def save_state(self, st):
        write_json(self.state_path, st)

    def setup(self, tpl):
        os.makedirs(self.logs, exist_ok=True)
        os.makedirs(self.cwd, exist_ok=True)
        os.makedirs(self.home, exist_ok=True)
        copy_build(tpl, self.home)
        if not os.path.exists(os.path.join(self.dir, "question.json")):
            write_json(os.path.join(self.dir, "question.json"), self.q)

    def store_dirs(self):
        return [os.path.join(self.home, "observational-memory")]

    def snapshot(self):
        """Before a PiG run: the extension store, so a failed run is undone with the session file."""
        for d in self.store_dirs():
            snap = d + ".snap"
            shutil.rmtree(snap, ignore_errors=True)
            if os.path.exists(d):
                shutil.copytree(d, snap, symlinks=True)

    def collect(self, run_name):
        return collect_debug(self.agent_dir, self.sf.session_id(), self.debug_path, run_name)

    def record_failed_run(self, st, run, error, seconds=None):
        """A run that did not finish: its extension requests are kept out of the success counts."""
        debug = self.collect(run + "-failed")
        st["failed_runs"].append({"run": run, "error": str(error)[:500], "seconds": seconds, "debug": debug})
        self.save_state(st)

    def restore(self, st):
        """Undo anything after the last consistent point: session bytes past st['size'] and the
        extension store's changes from an unfinished run."""
        if st.get("pending"):
            # A run the harness never finished (killed or crashed): keep its debug lines apart.
            debug = self.collect(st["pending"] + "-failed")
            if debug["lines"]:
                st["failed_runs"].append({"run": st["pending"], "error": "interrupted", "seconds": None, "debug": debug})
        if os.path.exists(self.session) and st["size"] and os.path.getsize(self.session) != st["size"]:
            with open(self.session, "r+b") as f:
                f.truncate(st["size"])
        if st.get("pending"):
            for d in self.store_dirs():
                snap = d + ".snap"
                if os.path.exists(snap):
                    shutil.rmtree(d, ignore_errors=True)
                    shutil.copytree(snap, d, symlinks=True)
            st["pending"] = None

    # replay ---------------------------------------------------------------
    def compact(self, st, chunk_index, estimate):
        run = "compact-%03d" % (len(st["compactions"]) + len(st["compaction_failures"]) + len(st["failed_runs"]) + 1)
        self.snapshot()
        st["pending"] = run
        self.save_state(st)
        t0 = time.time()
        rpc = RPC(self.cfg["pig"], self.home, self.agent_dir, self.cwd,
                  ["--session", self.session, "--model", self.cfg["model"], "--thinking", self.cfg["thinking"]] + COMMON_FLAGS,
                  os.path.join(self.logs, run + ".jsonl"))
        try:
            state = rpc.call({"type": "get_state"}, timeout=300)["data"]
            model = state.get("model") or {}
            if model.get("contextWindow") != self.cfg["window"] or bool(state.get("autoCompactionEnabled")) != self.cfg["auto_compaction"]:
                raise PigError("bench conditions not in effect: contextWindow %s autoCompaction %s" % (model.get("contextWindow"), state.get("autoCompactionEnabled")))
            stats = rpc.call({"type": "get_session_stats"})["data"]
            reported = (stats.get("contextUsage") or {}).get("tokens")
            r = rpc.call({"type": "compact"}, timeout=self.cfg["compact_timeout"])
        except Exception as e:
            rpc.kill()
            rpc.close()
            self.record_failed_run(st, run, e, round(time.time() - t0, 1))
            raise
        rpc.close()
        if not r.get("success"):
            err = r.get("error") or ""
            if not (err.startswith("Nothing to compact") or err.startswith("Already compacted")):
                self.record_failed_run(st, run, "compaction failed: " + err, round(time.time() - t0, 1))
                raise PigError("compaction failed: " + err)
        debug = self.collect(run)
        rec = {"run": run, "chunk": chunk_index, "estimate": estimate["total"], "estimate_messages": estimate["messages"],
               "system_tokens": estimate["system"], "reported": reported,
               "seconds": round(time.time() - t0, 1), "debug": debug}
        if r.get("success"):
            d = r.get("data") or {}
            details = d.get("details") or {}
            rec.update({"tokensBefore": d.get("tokensBefore"), "estimatedTokensAfter": d.get("estimatedTokensAfter"),
                        "summaryTokens": ceil_quarter(js_len(d.get("summary") or "")),
                        "observations": len(details.get("observations") or []),
                        "consolidation": details.get("consolidation")})
            rec["estimateAfter"] = estimate_context(self.sf.entries())
            st["compactions"].append(rec)
        else:
            rec["error"] = r.get("error") or ""
            st["compaction_failures"].append(rec)
        st["size"] = os.path.getsize(self.session)
        st["pending"] = None
        st["runs"].append({"run": run, "seconds": rec["seconds"], "requests": debug["requests"]})
        self.save_state(st)
        return rec

    def replay(self, st):
        chunks, adjusted = chunks_of(self.q, self.cfg["chunk"])
        st["timestamps_adjusted"] = adjusted
        if st["chunks_done"] == 0 and not st["size"]:
            self.sf.create(self.cwd, chunks[0][0][2] if chunks else datetime.datetime.now(datetime.timezone.utc))
            st["size"] = os.path.getsize(self.session)
            st["evidence_ids"] = []
            self.save_state(st)
        suspended_at = None
        while True:
            # PiG's check after a settled exchange: context > window - reserveTokens, with the
            # context counting the system message a real session sends. After "Nothing to
            # compact" it is asked again only once the branch has grown.
            if st.get("checked", 0) < st["chunks_done"]:
                i = st["chunks_done"] - 1
                messages = estimate_context(self.sf.entries())
                estimate = {"messages": messages, "system": self.system_tokens, "total": messages + self.system_tokens}
                if estimate["total"] > self.threshold and suspended_at != st["size"]:
                    t1 = time.time()
                    rec = self.compact(st, i, estimate)
                    st["replay_seconds"] += time.time() - t1
                    if "error" in rec:
                        suspended_at = st["size"]
                    log("%s %s at chunk %d/%d: %s" % (self.dirname, rec["run"], i + 1, len(chunks), compact_line(rec)))
                    reason = str((rec.get("consolidation") or {}).get("reason") or "")
                    if QUOTA_ERROR.search(reason):
                        raise QuotaExhausted("level 1 hit the usage limit at %s: %s" % (rec["run"], reason))
                st["checked"] = st["chunks_done"]
                self.save_state(st)
            if st["chunks_done"] >= len(chunks):
                break
            t0 = time.time()
            size, evidence = self.sf.append_chunk(chunks[st["chunks_done"]])
            st["size"] = size
            st["evidence_ids"] += evidence
            st["chunks_done"] += 1
            st["replay_seconds"] += time.time() - t0
            self.save_state(st)

    # answer ---------------------------------------------------------------
    def check_no_leak(self, system_msgs):
        """The question id must not reach the actor: not in any system section, the cwd or the home."""
        qid = self.q["question_id"]
        where = [k for m in system_msgs for k, v in (m.get("sections") or {}).items() if isinstance(v, str) and qid in v]
        if any(qid in (m.get("content") or "") for m in system_msgs if isinstance(m.get("content"), str)):
            where.append("content")
        if qid in self.cwd or qid in self.home or qid in self.session:
            where.append("paths")
        if where:
            raise PigError("the question id reaches the actor's system prompt (%s)" % ",".join(sorted(set(where))))

    def answer(self, st):
        path = os.path.join(self.dir, "answer.json")
        done = read_json(path)
        if done:
            return done
        pre = self.session + ".pre-answer"
        if not os.path.exists(pre):
            shutil.copyfile(self.session, pre)
        else:
            shutil.copyfile(pre, self.session)
        self.snapshot()
        st["pending"] = "answer"
        self.save_state(st)
        size_before = os.path.getsize(self.session)
        entries_before = self.sf.entries()
        n_before = len(entries_before)
        estimate = estimate_context(entries_before)
        location = evidence_location(entries_before, st.get("evidence_ids") or [])
        prompt = "Current Date: %s\nQuestion: %s" % (self.q["question_date"], self.q["question"])
        if self.cfg.get("question_day_header"):
            # The question is sent live, so PiG stamps it with today's date; the extension's day
            # header would then contradict the question date. A message opening with a header is
            # left alone, so the question carries the header of the day it is asked on.
            prompt = "[Sent on %s]\n%s" % (parse_date(self.q["question_date"]).strftime("%A %Y-%m-%d"), prompt)
        run = "answer-%d" % (1 + sum(1 for f in st["failed_runs"] if f["run"].startswith("answer")))
        t0 = time.time()
        rpc = RPC(self.cfg["pig"], self.home, self.agent_dir, self.cwd,
                  ["--session", self.session, "--model", self.cfg["model"], "--thinking", self.cfg["thinking"],
                   "--tools", "memory", "--append-system-prompt", self.cfg["actor_instructions"]] + COMMON_FLAGS,
                  os.path.join(self.logs, run + ".jsonl"))
        try:
            rpc.call({"type": "get_state"}, timeout=300)
            r = rpc.call({"type": "prompt", "message": prompt}, timeout=300)
            if not r.get("success"):
                raise PigError("prompt refused: %s" % r.get("error"))
            rpc.pump(lambda e: e.get("type") == "agent_settled", self.cfg["answer_timeout"])
            text = (rpc.call({"type": "get_last_assistant_text"}).get("data") or {}).get("text") or ""
            stats = rpc.call({"type": "get_session_stats"}).get("data") or {}
        except Exception as e:
            rpc.kill()
            rpc.close()
            self.record_failed_run(st, run, e, round(time.time() - t0, 1))
            raise
        rpc.close()
        seconds = time.time() - t0
        try:
            new = self.sf.entries()[n_before:]
            system_msgs = system_messages(new)
            if not system_msgs:
                # A resumed session keeps its system message when nothing changed; read the latest one.
                system_msgs = system_messages(self.sf.entries())[-1:]
            self.check_no_leak(system_msgs)
            tools = sorted({t.get("name") for m in system_msgs for t in (m.get("toolsAdded") or [])})
            if tools != ["memory"]:
                raise PigError("the actor's tools are %s, not only memory" % tools)
            sections = sorted((system_msgs[-1].get("sections") or {}).keys()) if system_msgs else []
            tool_section = ((system_msgs[-1].get("sections") or {}).get("tools") or "") if system_msgs else ""
            if "memory" not in tool_section:
                raise PigError("the system prompt has no tools section describing memory (sections %s)" % sections)
            actor_system_tokens = system_message_tokens(system_msgs[-1]) if system_msgs else 0
            assistants = [e["message"] for e in new if e.get("type") == "message" and (e.get("message") or {}).get("role") == "assistant"]
            results = {}
            for e in new:
                m = e.get("message") or {}
                if e.get("type") == "message" and m.get("role") == "toolResult":
                    results[m.get("toolCallId")] = "".join(b.get("text", "") for b in m.get("content") or [] if b.get("type") == "text")
            calls = []
            for m in assistants:
                for b in m.get("content") or []:
                    if b.get("type") == "toolCall":
                        res = results.get(b.get("id"), "")
                        calls.append({"name": b.get("name"), "arguments": b.get("arguments"), "resultChars": len(res), "result": res[:4000]})
            usage = {k: sum(int((m.get("usage") or {}).get(k) or 0) for m in assistants) for k in ("input", "output", "cacheRead", "reasoning")}
            first_usage = (assistants[0].get("usage") or {}) if assistants else {}
            last = assistants[-1] if assistants else {}
            if not text.strip() or last.get("stopReason") in ("error", "aborted"):
                raise PigError("no answer: stopReason %s %s" % (last.get("stopReason"), last.get("errorMessage", "")))
        except Exception as e:
            self.record_failed_run(st, run, e, round(seconds, 1))
            raise
        debug = self.collect(run)
        compactions_in_answer = [e for e in new if e.get("type") == "compaction"]
        # The first actor request's provider-counted input against the harness's chars/4 count of
        # the same context: the evidence for how the estimate relates to real-use usage.
        first_input = int(first_usage.get("input") or 0) + int(first_usage.get("cacheRead") or 0)
        ans = {"hypothesis": text, "stopReason": last.get("stopReason"), "actorRequests": len(assistants),
               "toolCalls": calls, "toolsOffered": tools, "systemSections": sections, "usage": usage,
               "contextEstimate": estimate, "actorSystemTokens": actor_system_tokens,
               "promptTokens": ceil_quarter(js_len(prompt)), "firstRequestProviderInput": first_input,
               "contextUsageAfter": stats.get("contextUsage"), "evidenceLocation": location,
               "extensionDebug": debug, "compactionsDuringAnswer": len(compactions_in_answer),
               "seconds": round(seconds, 1), "sessionBytesBefore": size_before, "run": run}
        st["answer_seconds"] = seconds
        st["size"] = os.path.getsize(self.session)
        st["pending"] = None
        st["runs"].append({"run": run, "seconds": round(seconds, 1), "requests": debug["requests"]})
        self.save_state(st)
        write_json(path, ans)
        return ans

    # judge ----------------------------------------------------------------
    def judge(self, st, ans, judge_home, out):
        path = os.path.join(self.dir, "judge.json")
        done = read_json(path)
        if done:
            return done
        prompt = judge_prompt(self.q, ans["hypothesis"])
        t0 = time.time()
        args = [self.cfg["pig"], "-p", "--mode", "json", "--no-session", "--no-tools", "--no-extensions",
                "--model", self.cfg["model"], "--thinking", self.cfg["judge_thinking"], "--system-prompt", self.cfg["judge_system"]] + COMMON_FLAGS
        if len(prompt.encode()) < 100000:
            args += ["--no-stdin", "--", prompt]
            data = None
        else:
            data = prompt
        reply, usage, err, attempts = "", {}, "", []
        for attempt in range(3):
            io = {"input": data} if data is not None else {"stdin": subprocess.DEVNULL}
            p = subprocess.run(args, cwd=os.path.join(out, "judge-cwd"), env=pig_env(judge_home, self.agent_dir),
                               capture_output=True, text=True, timeout=self.cfg["judge_timeout"], **io)
            sent, reply = None, ""
            for line in p.stdout.splitlines():
                try:
                    e = json.loads(line)
                except ValueError:
                    continue
                m = e.get("message") or {}
                if e.get("type") == "message_end" and m.get("role") == "user":
                    sent = "".join(b.get("text", "") for b in m.get("content") or [] if b.get("type") == "text")
                if e.get("type") == "message_end" and m.get("role") == "assistant":
                    reply = "".join(b.get("text", "") for b in m.get("content") or [] if b.get("type") == "text")
                    usage = m.get("usage") or {}
            err = p.stderr[-2000:]
            attempts.append({"returncode": p.returncode, "replyChars": len(reply), "promptSeen": sent is not None})
            if sent is None:
                raise PigError("the judge's json stream has no user message, so its prompt cannot be checked (exit %d): %s" % (p.returncode, err[-300:]))
            if sent != prompt:
                raise PigError("the judge prompt PiG sent differs from the official prompt")
            if reply.strip() and p.returncode == 0:
                break
        if not reply.strip() or p.returncode != 0:
            raise PigError("no judge verdict after %d attempts (exit %d): %s" % (len(attempts), p.returncode, err))
        low = reply.strip().lower()
        res = {"prompt": prompt, "reply": reply, "correct": "yes" in low,
               "strict": low == "yes" or low.startswith("yes."), "usage": usage, "attempts": attempts,
               "seconds": round(time.time() - t0, 1)}
        st["judge_seconds"] = res["seconds"]
        self.save_state(st)
        write_json(path, res)
        return res

    # all ------------------------------------------------------------------
    def run(self, tpl, judge_home, out):
        self.setup(tpl)
        st = self.load_state()
        self.restore(st)
        st["attempts"] += 1
        self.save_state(st)
        self.replay(st)
        ans = self.answer(st)
        verdict = self.judge(st, ans, judge_home, out)
        result = build_result(self.q, st, ans, verdict, self.cfg, self.dirname)
        write_json(os.path.join(self.dir, "result.json"), result)
        if not self.cfg["keep_homes"]:
            for sub in ("cache", "state", "docs"):
                shutil.rmtree(os.path.join(self.home, sub), ignore_errors=True)
        return result


def compact_line(rec):
    if "error" in rec:
        return "refused (%s)" % rec["error"]
    c = rec.get("consolidation") or {}
    l1 = "applied" if c.get("applied") else ("requested, not applied: %s" % c.get("reason") if c.get("requested") else "not requested: %s" % c.get("reason"))
    return "estimate %s = %s messages + %s system (PiG tokensBefore %s) -> %s, %d records, summary ~%s tok, %d ext requests, level 1 %s, %.0fs" % (
        rec["estimate"], rec.get("estimate_messages"), rec.get("system_tokens"), rec.get("tokensBefore"), rec.get("estimateAfter"),
        rec.get("observations", 0), rec.get("summaryTokens"), rec["debug"]["requests"], l1, rec["seconds"])


def judge_prompt(q, hypothesis):
    question, answer = q["question"], answer_text(q)
    if is_abstention(q):
        template = JUDGE_ABSTENTION
    else:
        template = {"single-session-user": JUDGE_DEFAULT, "single-session-assistant": JUDGE_DEFAULT,
                    "multi-session": JUDGE_DEFAULT, "temporal-reasoning": JUDGE_TEMPORAL,
                    "knowledge-update": JUDGE_KNOWLEDGE_UPDATE, "single-session-preference": JUDGE_PREFERENCE}.get(q["question_type"])
        if template is None:
            raise PigError("unknown question type %s" % q["question_type"])
    return template.format(question, answer, hypothesis)


def build_result(q, st, ans, verdict, cfg, dirname):
    comps = st["compactions"]
    # Successful runs only: a compaction (or a "Nothing to compact" refusal) that finished. Failed
    # and interrupted runs are counted apart, so the per-compaction figure is not inflated.
    ext_requests = sum(c["debug"]["requests"] for c in comps) + sum(c["debug"]["requests"] for c in st["compaction_failures"])
    level1 = [c.get("consolidation") or {} for c in comps]
    return {
        "question_id": q["question_id"], "dir": dirname, "question_type": q["question_type"], "abstention": is_abstention(q),
        "question": q["question"], "answer": answer_text(q), "question_date": q["question_date"],
        "hypothesis": ans["hypothesis"], "correct": verdict["correct"], "strict_correct": verdict["strict"],
        "judge_reply": verdict["reply"], "judge_attempts": len(verdict.get("attempts") or [1]),
        "compactions": len(comps), "compaction_refusals": len(st["compaction_failures"]),
        "extension_requests": ext_requests,
        "extension_requests_answer": ans["extensionDebug"]["requests"],
        "failed_runs": len(st["failed_runs"]),
        "failed_run_requests": sum(f["debug"]["requests"] for f in st["failed_runs"]),
        "level1_requested": sum(1 for c in level1 if c.get("requested")),
        "level1_applied": sum(1 for c in level1 if c.get("applied")),
        "level1_reasons": [c.get("reason") for c in level1 if not c.get("applied")],
        "extension_tokens": {k: sum(c["debug"]["tokens"].get(k, 0) for c in comps) for k in ("inputTokens", "outputTokens", "cacheReadTokens")},
        "summary_tokens": [c.get("summaryTokens") for c in comps],
        "answer_tool_calls": len(ans["toolCalls"]), "actor_requests": ans["actorRequests"],
        "actor_usage": ans["usage"], "judge_usage": verdict.get("usage"),
        "answer_context_estimate": ans["contextEstimate"], "actor_system_tokens": ans.get("actorSystemTokens"),
        "answer_prompt_tokens": ans.get("promptTokens"), "first_request_provider_input": ans.get("firstRequestProviderInput"),
        "compactions_during_answer": ans["compactionsDuringAnswer"],
        "evidence_location": ans.get("evidenceLocation"),
        "timestamps_adjusted": st.get("timestamps_adjusted", 0),
        "estimate_vs_pig": [[c.get("estimate_messages", c["estimate"]), c.get("tokensBefore")] for c in comps],
        "trigger_estimates": [c["estimate"] for c in comps],
        "replay_seconds": round(st["replay_seconds"], 1), "answer_seconds": round(st["answer_seconds"], 1),
        "judge_seconds": round(st["judge_seconds"], 1), "attempts": st["attempts"],
        "settings": {k: cfg.get(k) for k in RESULT_SETTINGS},
    }


# ─── run ──────────────────────────────────────────────────────────────────────

def auth_status(cfg, home, agent_dir):
    p = subprocess.run([cfg["pig"], "auth", "check", "--no-refresh", "--json", "--model", cfg["model"]],
                       env=pig_env(home, agent_dir), capture_output=True, text=True, timeout=60, stdin=subprocess.DEVNULL)
    try:
        data = json.loads(p.stdout)
    except ValueError:
        return "unreadable (exit %d)" % p.returncode
    def find(v):
        if isinstance(v, dict):
            if isinstance(v.get("status"), str):
                return v["status"]
            for x in v.values():
                s = find(x)
                if s:
                    return s
        if isinstance(v, list):
            for x in v:
                s = find(x)
                if s:
                    return s
        return None
    return find(data) or "unknown"


def error_category(text):
    if RATE_ERROR.search(text):
        return "rate_limit"
    if AUTH_ERROR.search(text):
        return "auth"
    return "other"


def run_one(out, q, cfg, dirname, agent_dir, system_tokens, tpl, judge_home, index, total):
    """One question: a result, None (failed twice, failed.json written) or "stopped" (auth lost)."""
    qid = q["question_id"]
    qd = os.path.join(out, "questions", dirname)
    errors = []
    for attempt in (1, 2):
        if STOP.is_set():
            return "stopped"
        status = auth_status(cfg, judge_home, agent_dir)
        if status != "ready":
            STOP.set()
            log("auth is %s before %s: stopping; sign in and rerun the same command" % (status, dirname))
            return "stopped"
        try:
            t0 = time.time()
            res = Question(out, q, cfg, dirname, agent_dir, system_tokens).run(tpl, judge_home, out)
            if os.path.exists(os.path.join(qd, "failed.json")):
                os.remove(os.path.join(qd, "failed.json"))
            log("[%d/%d] %s %-26s correct=%-5s compactions=%d ext-req=%d tools=%d evidence=%s %4.0fs %s" % (
                index, total, dirname, q["question_type"], res["correct"], res["compactions"], res["extension_requests"],
                res["answer_tool_calls"], res["evidence_location"], time.time() - t0, res["hypothesis"].replace("\n", " ")[:80]))
            return res
        except Exception as e:  # retried once, then recorded
            if isinstance(e, QuotaExhausted) or QUOTA_ERROR.search(str(e)):
                # A usage limit is not a property of the question: stop the run, record nothing,
                # and remove the partial question so a rerun starts it from scratch.
                STOP.set()
                shutil.rmtree(qd, ignore_errors=True)
                log("usage limit reached during %s: stopping without recording it; rerun the same command later (%s)" % (dirname, e))
                return "stopped"
            errors.append({"error": "%s: %s" % (type(e).__name__, e), "category": error_category(str(e))})
            log("%s attempt %d failed (%s): %s" % (dirname, attempt, errors[-1]["category"], errors[-1]["error"]))
            if auth_status(cfg, judge_home, agent_dir) != "ready":
                STOP.set()
                log("auth is no longer ready after %s failed: stopping without recording it" % dirname)
                return "stopped"
    os.makedirs(qd, exist_ok=True)
    cats = sorted({e["category"] for e in errors})
    write_json(os.path.join(qd, "failed.json"), {"question_id": qid, "dir": dirname, "question_type": q["question_type"],
                                                 "errors": [e["error"] for e in errors],
                                                 "category": "other" if "other" in cats else cats[-1],
                                                 "at": datetime.datetime.now(datetime.timezone.utc).isoformat()})
    return None


def cmd_run(args):
    out = os.path.abspath(args.out)
    os.makedirs(os.path.join(out, "questions"), exist_ok=True)
    extension = os.path.abspath(args.extension)
    cfg = {"pig": os.path.abspath(args.pig), "model": args.model, "extension": extension,
           "extension_hash": tree_hash(extension), "extension_git": git_revision(extension),
           "pig_version": pig_version(os.path.abspath(args.pig)),
           "auth": os.path.abspath(args.auth), "window": args.window, "max_tokens": args.max_tokens,
           "reserve_tokens": PIG_RESERVE_TOKENS, "keep_recent_tokens": PIG_KEEP_RECENT, "auto_compaction": True,
           "thinking": args.thinking, "judge_thinking": args.judge_thinking, "chunk": args.chunk,
           "actor_instructions": ACTOR_INSTRUCTIONS, "judge_system": JUDGE_SYSTEM,
           "keep_homes": args.keep_homes, "compact_timeout": args.compact_timeout,
           "answer_timeout": args.answer_timeout, "judge_timeout": args.judge_timeout,
           "question_day_header": args.question_day_header}
    run_path = os.path.join(out, "run.json")
    prev = read_json(run_path)
    if prev and not args.force_settings:
        for k in RESULT_SETTINGS:
            if prev["settings"].get(k) != cfg[k]:
                raise SystemExit("run.json was written with %s=%r; this run has %r (pass --force-settings to override)" % (k, prev["settings"].get(k), cfg[k]))
    with open(args.data) as f:
        questions = json.load(f)
    selected = select(questions, args)
    population = {}
    for q in questions:
        population[q["question_type"]] = population.get(q["question_type"], 0) + 1
    salt = (prev or {}).get("dir_salt") or uuid.uuid4().hex
    dirs = dict((prev or {}).get("dirs") or {})
    for q in selected:
        dirs.setdefault(q["question_id"], opaque_dir(salt, q["question_id"]))
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    selections = list((prev or {}).get("selections") or []) + [{"at": now, "count": len(selected), "force_settings": bool(args.force_settings)}]
    run_doc = {"dataset": os.path.basename(args.data), "dataset_path": os.path.abspath(args.data), "settings": cfg,
               "population": population, "workers": args.workers,
               "selected": [q["question_id"] for q in selected], "types": {q["question_id"]: q["question_type"] for q in selected},
               "selections": selections, "dir_salt": salt, "dirs": dirs,
               "probe": (prev or {}).get("probe"),
               "started": (prev or {}).get("started") or now}
    write_json(run_path, run_doc)
    agent_dir = write_agent_dir(out, cfg)
    judge_home = os.path.join(out, "judge-home")
    os.makedirs(judge_home, exist_ok=True)
    os.makedirs(os.path.join(out, "judge-cwd"), exist_ok=True)
    status = auth_status(cfg, judge_home, agent_dir)
    log("auth %s: %s" % (cfg["model"], status))
    if status != "ready":
        raise SystemExit("auth is not ready (%s); sign in with pig login and rerun" % status)
    tpl = prepare_template(out, cfg, agent_dir)
    probe = prepare_probe(out, cfg, agent_dir, tpl)
    run_doc["probe"] = probe
    write_json(run_path, run_doc)
    todo = [q for q in selected if not os.path.exists(os.path.join(out, "questions", dirs[q["question_id"]], "result.json"))]
    log("%d selected, %d done, %d to run, %d workers" % (len(selected), len(selected) - len(todo), len(todo), args.workers))
    failed = stopped = 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        futs = [pool.submit(run_one, out, q, cfg, dirs[q["question_id"]], agent_dir, probe["system_tokens"], tpl, judge_home, i + 1, len(todo))
                for i, q in enumerate(todo)]
        for f in concurrent.futures.as_completed(futs):
            r = f.result()
            if r is None:
                failed += 1
            elif r == "stopped":
                stopped += 1
    summary = write_report(out)
    log("report: %s" % os.path.join(out, "report.md"))
    print(summary_text(summary))
    if stopped:
        log("%d questions not started or stopped because auth is not ready; sign in and rerun the same command" % stopped)
        return 2
    if failed:
        log("%d questions failed; rerun the same command to retry them" % failed)
        return 1
    return 0


# ─── report ───────────────────────────────────────────────────────────────────

def mean(xs):
    xs = list(xs)
    return sum(xs) / len(xs) if xs else 0.0


def write_report(out):
    run = read_json(os.path.join(out, "run.json"), {})
    settings = run.get("settings") or {}
    dirs = run.get("dirs") or {}
    types = run.get("types") or {}
    selected = run.get("selected") or []
    results, failures, missing, mismatched = [], [], [], []
    for qid in selected:
        d = os.path.join(out, "questions", dirs.get(qid, qid))
        r = read_json(os.path.join(d, "result.json"))
        if r:
            diff = sorted(k for k in RESULT_SETTINGS if (r.get("settings") or {}).get(k) != settings.get(k))
            if diff:
                mismatched.append({"question_id": qid, "settings": diff})
            results.append(r)
            continue
        f = read_json(os.path.join(d, "failed.json"))
        if f:
            failures.append(f)
        else:
            missing.append({"question_id": qid, "question_type": types.get(qid, "?")})
    known = set(dirs.get(q, q) for q in selected)
    qroot = os.path.join(out, "questions")
    outside = sorted(n for n in (os.listdir(qroot) if os.path.isdir(qroot) else []) if n not in known)
    with open(os.path.join(out, "results.jsonl"), "w") as f:
        for r in results:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    # Every selected question is in the denominator: a failed or missing one counts as incorrect.
    by_type = {}
    for qid in selected:
        by_type.setdefault(types.get(qid, "?"), {"correct": 0, "strict": 0, "total": 0, "answered": 0, "failed": 0, "missing": 0})
    for r in results:
        t = by_type.setdefault(r["question_type"], {"correct": 0, "strict": 0, "total": 0, "answered": 0, "failed": 0, "missing": 0})
        t["answered"] += 1
        t["correct"] += int(r["correct"])
        t["strict"] += int(r["strict_correct"])
    for f in failures:
        by_type.setdefault(f["question_type"], {"correct": 0, "strict": 0, "total": 0, "answered": 0, "failed": 0, "missing": 0})["failed"] += 1
    for m in missing:
        by_type.setdefault(m["question_type"], {"correct": 0, "strict": 0, "total": 0, "answered": 0, "failed": 0, "missing": 0})["missing"] += 1
    for t in by_type.values():
        t["total"] = t["answered"] + t["failed"] + t["missing"]
        t["accuracy"] = t["correct"] / t["total"] if t["total"] else 0.0
        t["answered_accuracy"] = t["correct"] / t["answered"] if t["answered"] else None
    n_sel = len(selected)
    abst_ids = [q for q in selected if q.endswith("_abs")]
    abst = [r for r in results if r["abstention"]]
    comps = sum(r["compactions"] for r in results)
    ext = sum(r["extension_requests"] for r in results)
    per_q_requests = [r["extension_requests"] + r["extension_requests_answer"] + r["actor_requests"] + 1 for r in results]
    walls = [r["replay_seconds"] + r["answer_seconds"] + r["judge_seconds"] for r in results]
    evidence = {}
    for r in results:
        e = evidence.setdefault(r["question_type"], {}).setdefault(r.get("evidence_location") or "unknown", {"correct": 0, "total": 0})
        e["total"] += 1
        e["correct"] += int(r["correct"])
    gaps = [r["first_request_provider_input"] / (r["answer_context_estimate"] + (r.get("actor_system_tokens") or 0) + (r.get("answer_prompt_tokens") or 0))
            for r in results if r.get("first_request_provider_input") and r.get("answer_context_estimate")]
    fail_cats = {}
    for f in failures:
        fail_cats[f.get("category", "other")] = fail_cats.get(f.get("category", "other"), 0) + 1
    s = {
        "run": os.path.basename(out), "settings": settings, "probe": run.get("probe"),
        "selected": n_sel, "questions": len(results), "failed": len(failures), "missing": len(missing),
        "failure_categories": fail_cats, "failures": failures, "missing_questions": missing,
        "settings_mismatches": mismatched, "outside_selection": outside,
        "accuracy_by_type": by_type,
        "task_averaged_accuracy": mean(t["accuracy"] for t in by_type.values()),
        "types_present": len(by_type),
        "overall_accuracy": sum(int(r["correct"]) for r in results) / n_sel if n_sel else 0.0,
        "answered_only_accuracy": mean(int(r["correct"]) for r in results) if results else None,
        "strict_task_averaged_accuracy": mean(t["strict"] / t["total"] for t in by_type.values() if t["total"]),
        "abstention": {"correct": sum(int(r["correct"]) for r in abst), "total": len(abst_ids)},
        "evidence_location": evidence,
        "attempts": {"total": sum(r["attempts"] for r in results), "questions_retried": sum(1 for r in results if r["attempts"] > 1),
                     "judge_retries": sum(1 for r in results if r.get("judge_attempts", 1) > 1)},
        "failed_runs": {"runs": sum(r.get("failed_runs", 0) for r in results), "extension_requests": sum(r.get("failed_run_requests", 0) for r in results)},
        "timestamps_adjusted_questions": sum(1 for r in results if r.get("timestamps_adjusted")),
        "provider_vs_estimate": mean(gaps) if gaps else None,
        "compactions_per_question": mean(r["compactions"] for r in results),
        "compaction_refusals": sum(r["compaction_refusals"] for r in results),
        "extension_requests_per_compaction": ext / comps if comps else 0.0,
        "extension_requests_at_answer": sum(r["extension_requests_answer"] for r in results),
        "level1_applied_per_compaction": sum(r["level1_applied"] for r in results) / comps if comps else 0.0,
        "level1_requested_per_compaction": sum(r["level1_requested"] for r in results) / comps if comps else 0.0,
        "summary_tokens_mean": mean(t for r in results for t in r.get("summary_tokens") or [] if t),
        "answer_tool_calls_per_question": mean(r["answer_tool_calls"] for r in results),
        "questions_with_tool_calls": sum(1 for r in results if r["answer_tool_calls"]),
        "actor_requests_per_question": mean(r["actor_requests"] for r in results),
        "requests_per_question": mean(per_q_requests),
        "seconds": {"replay": mean(r["replay_seconds"] for r in results), "answer": mean(r["answer_seconds"] for r in results),
                    "judge": mean(r["judge_seconds"] for r in results), "total": mean(walls)},
        "workers": run.get("workers"), "dataset": run.get("dataset"),
    }
    pop = run.get("population") or {}
    if results and pop:
        n = sum(pop.values())
        workers = max(1, int(run.get("workers") or 1))
        s["extrapolated"] = {"questions": n, "workers": workers,
                             "wall_hours": mean(walls) * n / workers / 3600,
                             "requests": mean(per_q_requests) * n,
                             "task_average_note": "per-type means of this sample; not comparable with fewer than 6 types" if len(by_type) < 6 else ""}
    write_json(os.path.join(out, "summary.json"), s)
    with open(os.path.join(out, "report.md"), "w") as f:
        f.write(summary_markdown(s))
    return s


def bar(p):
    n = round(p * 20)
    return "█" * n + "░" * (20 - n)


def summary_text(s):
    lines = ["Accuracy by Question Type (failed and missing questions count as incorrect):"]
    for t in sorted(s["accuracy_by_type"]):
        v = s["accuracy_by_type"][t]
        extra = []
        if v["failed"]:
            extra.append("%d failed" % v["failed"])
        if v["missing"]:
            extra.append("%d missing" % v["missing"])
        lines.append("%-26s %6s %s (%d/%d)%s" % (t, "%.1f%%" % (100 * v["accuracy"]), bar(v["accuracy"]), v["correct"], v["total"],
                                                  " " + ", ".join(extra) if extra else ""))
    lines.append("")
    lines.append("Task-averaged Accuracy: %.2f%% (mean of %d question types)" % (100 * s["task_averaged_accuracy"], s["types_present"]))
    lines.append("Overall Accuracy: %.2f%% (%d of %d selected questions)" % (
        100 * s["overall_accuracy"], sum(v["correct"] for v in s["accuracy_by_type"].values()), s["selected"]))
    lines.append("Answered %d, failed %d, missing %d" % (s["questions"], s["failed"], s["missing"]))
    return "\n".join(lines)


def conditions_lines(s):
    st = s.get("settings") or {}
    probe = s.get("probe") or {}
    window, reserve = st.get("window"), st.get("reserve_tokens")
    return [
        "| Condition | Value |", "|---|---|",
        "| Dataset | %s |" % s.get("dataset"),
        "| Model (session/level 1, actor, judge) | %s |" % st.get("model"),
        "| contextWindow / maxTokens (models.json override) | %s / %s |" % (window, st.get("max_tokens")),
        "| compaction.reserveTokens / keepRecentTokens | %s / %s |" % (reserve, st.get("keep_recent_tokens")),
        "| compaction.enabled (auto-compaction) | %s |" % st.get("auto_compaction"),
        "| Threshold (window - reserve) | %s |" % ((window - reserve) if window and reserve else None),
        "| System message counted in the trigger (probe, chars/4) | %s |" % probe.get("system_tokens"),
        "| Thinking (actor, level 1) / judge | %s / %s |" % (st.get("thinking"), st.get("judge_thinking")),
        "| Actor instructions (`--append-system-prompt`) | %s |" % json.dumps(st.get("actor_instructions")),
        "| Judge system prompt | %s |" % json.dumps(st.get("judge_system")),
        "| Replay chunk | %s |" % st.get("chunk"),
        "| Workers | %s |" % s.get("workers"),
        "| PiG | %s |" % st.get("pig_version"),
        "| Extension | %s, content %s, git %s |" % (st.get("extension"), st.get("extension_hash"), st.get("extension_git")),
    ]


def summary_markdown(s):
    st = s.get("settings") or {}
    out = ["# LongMemEval: %s" % s["run"], "", "```",
           "Dataset: longmemeval_s",
           "Mode: memory v2 through real PiG sessions, answering with the memory tool",
           "Model: %s (Actor, level 1) / %s (Judge)" % (st.get("model"), st.get("model")),
           "─" * 52, summary_text(s), "```", ""]
    a = s["abstention"]
    out += ["- Task-averaged accuracy is the mean of the per-type accuracies (the Mastra-style headline); overall accuracy is the plain mean over every selected question, as LongMemEval's print_qa_metrics reports them.",
            "- Failed questions: %d %s; missing (selected, never attempted): %d. Both count as incorrect above." % (
                s["failed"], json.dumps(s["failure_categories"]) if s["failure_categories"] else "", s["missing"])]
    if s["answered_only_accuracy"] is not None:
        out.append("- Answered-only accuracy (failed and missing left out, not comparable): %.2f%% over %d" % (100 * s["answered_only_accuracy"], s["questions"]))
    out += ["- Abstention: %d/%d" % (a["correct"], a["total"]),
            "- Strict-verdict task-averaged accuracy: %.2f%%" % (100 * s["strict_task_averaged_accuracy"]),
            "- Attempts: %d in all, %d questions needed more than one; judge retries in %d questions" % (
                s["attempts"]["total"], s["attempts"]["questions_retried"], s["attempts"]["judge_retries"])]
    if s["types_present"] < 6:
        out.append("- Only %d of 6 types present: the task average is not comparable with a full run." % s["types_present"])
    if s["settings_mismatches"]:
        out.append("- **%d results were produced under settings that differ from run.json**: %s" % (
            len(s["settings_mismatches"]), "; ".join("%s (%s)" % (m["question_id"], ",".join(m["settings"])) for m in s["settings_mismatches"])))
    if s["outside_selection"]:
        out.append("- %d question directories are outside this run's selection and are not reported." % len(s["outside_selection"]))
    out += ["", "## Bench conditions", ""] + conditions_lines(s)
    out += ["", "## Accuracy by where the evidence was when answering", "",
            "`tail`: every evidence turn was in the raw turns kept after the latest compaction (they carry no visible date); "
            "`summary`: all of it was removed and only in memory; `none`: no evidence turn (abstention).", "",
            "| Type | Location | Correct |", "|---|---|---|"]
    for t in sorted(s["evidence_location"]):
        for loc in sorted(s["evidence_location"][t]):
            v = s["evidence_location"][t][loc]
            out.append("| %s | %s | %d/%d |" % (t, loc, v["correct"], v["total"]))
    pve = s.get("provider_vs_estimate")
    out += ["", "## Memory and requests", "",
            "| Measure | Value |", "|---|---|",
            "| Compactions per question | %.2f |" % s["compactions_per_question"],
            "| Compaction refusals (Nothing to compact) | %d |" % s["compaction_refusals"],
            "| Extension requests per compaction (successful runs) | %.2f |" % s["extension_requests_per_compaction"],
            "| Failed or interrupted PiG runs / their extension requests | %d / %d |" % (s["failed_runs"]["runs"], s["failed_runs"]["extension_requests"]),
            "| Level 1 requested / applied per compaction | %.2f / %.2f |" % (s["level1_requested_per_compaction"], s["level1_applied_per_compaction"]),
            "| Mean summary size (chars/4) | %.0f |" % s["summary_tokens_mean"],
            "| Extension requests while answering | %d |" % s["extension_requests_at_answer"],
            "| Answer tool calls per question | %.2f (%d questions used the tool) |" % (s["answer_tool_calls_per_question"], s["questions_with_tool_calls"]),
            "| Actor requests per question | %.2f |" % s["actor_requests_per_question"],
            "| Model requests per question (extension + actor + judge) | %.2f |" % s["requests_per_question"],
            "| Provider-counted input / chars/4 estimate at the first actor request | %s |" % ("%.3f" % pve if pve else "n/a"),
            "| Questions whose haystack timestamps were raised to stay monotonic | %d |" % s["timestamps_adjusted_questions"],
            "", "## Time per question", "",
            "Replay and compaction %.0fs, answer %.0fs, judge %.0fs, total %.0fs (means)." % (
                s["seconds"]["replay"], s["seconds"]["answer"], s["seconds"]["judge"], s["seconds"]["total"])]
    if s.get("extrapolated"):
        e = s["extrapolated"]
        out += ["", "## Extrapolated to %d questions" % e["questions"], "",
                "About %.1f wall hours at %d workers and %.0f model requests, from this run's per-question means." % (
                    e["wall_hours"], e["workers"], e["requests"])]
    if s["failures"]:
        out += ["", "## Failures", ""] + ["- %s (%s, %s): %s" % (f["question_id"], f["question_type"], f.get("category", "other"), f["errors"][-1][:300]) for f in s["failures"]]
    if s["missing_questions"]:
        out += ["", "## Missing (selected, not attempted)", ""] + ["- %s (%s)" % (m["question_id"], m["question_type"]) for m in s["missing_questions"]]
    out.append("")
    return "\n".join(out)


def cmd_report(args):
    s = write_report(os.path.abspath(args.out))
    print(summary_text(s))
    return 0


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run", help="run (or resume) a benchmark")
    r.add_argument("--data", required=True)
    r.add_argument("--out", required=True)
    r.add_argument("--pig", default=shutil.which("pig") or "pig")
    r.add_argument("--model", default=DEFAULT_MODEL)
    r.add_argument("--extension", default=DEFAULT_EXTENSION)
    r.add_argument("--auth", default=DEFAULT_AUTH, help="auth.json the run's agent directory symlinks (never read; refreshes write through it)")
    r.add_argument("--window", type=int, default=DEFAULT_WINDOW)
    r.add_argument("--max-tokens", type=int, default=DEFAULT_MAX_TOKENS)
    r.add_argument("--thinking", default="medium", help="actor and session (level 1) thinking level")
    r.add_argument("--judge-thinking", default="medium")
    r.add_argument("--chunk", choices=["turn", "session"], default="turn")
    r.add_argument("--workers", type=int, default=1)
    r.add_argument("--ids", default="")
    r.add_argument("--types", default="")
    r.add_argument("--limit", type=int, default=0)
    r.add_argument("--sample", type=int, default=0)
    r.add_argument("--stratified", action="store_true")
    r.add_argument("--seed", type=int, default=1)
    r.add_argument("--keep-homes", action="store_true", help="keep each home's extension build after it finishes")
    r.add_argument("--force-settings", action="store_true")
    r.add_argument("--compact-timeout", type=int, default=1200)
    r.add_argument("--answer-timeout", type=int, default=1200)
    r.add_argument("--judge-timeout", type=int, default=300)
    r.add_argument("--question-day-header", action="store_true",
                   help="open the question with the extension's day header for its question date (for an extension that writes day headers)")
    rp = sub.add_parser("report", help="rebuild report.md and summary.json")
    rp.add_argument("--out", required=True)
    args = ap.parse_args()
    sys.exit(cmd_run(args) if args.cmd == "run" else cmd_report(args))


if __name__ == "__main__":
    main()
