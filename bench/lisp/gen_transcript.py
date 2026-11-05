"""gen_transcript.py OUT [MB]: writes a deterministic JSONL transcript shaped like a Claude Code
session file (user prompts, assistant text and tool_use blocks, tool_result lines carrying long
outputs, the parentUuid chain, usage figures, some non-ASCII text and \\u escapes), about MB
mebibytes (10 by default)."""
import json
import random
import sys

WORDS = ("the service config port cache deploy build test failed passed retry handler request "
         "response latency billing search index shard replica commit branch merge review lint "
         "format module package import vendor go mod tidy make docker compose kubectl helm "
         "timeout error warning info debug trace span metric gauge counter histogram").split()
EXTRA = ["café", "naïve", "日本語", "Grüße", "🙂", "→", "λ"]


def sentence(rng, n):
    ws = [rng.choice(WORDS) for _ in range(n)]
    if rng.random() < 0.2:
        ws.insert(rng.randrange(len(ws)), rng.choice(EXTRA))
    return " ".join(ws).capitalize() + "."


def output(rng, lines):
    return "\n".join("%s:%d: %s\t%s \"%s\"" % (rng.choice(WORDS) + ".go", rng.randrange(1, 900),
                                                 rng.choice(["ok", "FAIL", "warn"]), sentence(rng, 8),
                                                 rng.choice(WORDS)) for _ in range(lines))


def main():
    out = sys.argv[1]
    target = int(float(sys.argv[2] if len(sys.argv) > 2 else 10) * 1024 * 1024)
    rng = random.Random(42)
    size, turn, parent = 0, 0, None
    base = {"isSidechain": False, "userType": "external", "cwd": "/work/proj",
            "sessionId": "5e2b0c8e-0000-4000-8000-000000000001", "version": "2.1.0", "gitBranch": "main"}

    def emit(f, obj):
        nonlocal size, parent
        line = json.dumps(obj, ensure_ascii=rng.random() < 0.1, separators=(",", ":"))
        f.write(line + "\n")
        size += len(line.encode()) + 1
        parent = obj["uuid"]

    with open(out, "w", encoding="utf-8") as f:
        while size < target:
            turn += 1
            ts = "2026-05-%02dT%02d:%02d:%02d.%03dZ" % (1 + turn // 400 % 28, turn // 20 % 24, turn % 60, turn * 7 % 60, turn % 1000)
            emit(f, dict(base, parentUuid=parent, type="user", uuid="u-%06d" % turn, timestamp=ts,
                         message={"role": "user", "content": sentence(rng, rng.randrange(8, 40))}))
            for step in range(rng.randrange(1, 4)):
                tid = "toolu_%06d_%d" % (turn, step)
                emit(f, dict(base, parentUuid=parent, type="assistant", uuid="a-%06d-%d" % (turn, step), timestamp=ts,
                             requestId="req_%06d" % turn,
                             message={"id": "msg_%06d_%d" % (turn, step), "type": "message", "role": "assistant",
                                      "model": "model-x", "stop_reason": "tool_use",
                                      "content": [{"type": "text", "text": sentence(rng, rng.randrange(10, 60))},
                                                  {"type": "tool_use", "id": tid, "name": rng.choice(["Bash", "Read", "Edit", "Grep"]),
                                                   "input": {"command": "go test ./%s/... -run %s" % (rng.choice(WORDS), rng.choice(WORDS)),
                                                             "timeout": rng.randrange(1000, 120000)}}],
                                      "usage": {"input_tokens": rng.randrange(10, 5000), "output_tokens": rng.randrange(10, 2000),
                                                "cache_read_input_tokens": rng.randrange(0, 90000), "cost": round(rng.random() / 10, 6)}}))
                emit(f, dict(base, parentUuid=parent, type="user", uuid="r-%06d-%d" % (turn, step), timestamp=ts,
                             message={"role": "user", "content": [{"type": "tool_result", "tool_use_id": tid,
                                                                   "content": output(rng, rng.randrange(2, 60)),
                                                                   "is_error": rng.random() < 0.15}]},
                             toolUseResult={"stdout": "", "stderr": "", "interrupted": False}))
            emit(f, dict(base, parentUuid=parent, type="assistant", uuid="a-%06d-end" % turn, timestamp=ts,
                         message={"id": "msg_%06d_end" % turn, "type": "message", "role": "assistant", "model": "model-x",
                                  "stop_reason": "end_turn", "content": [{"type": "text", "text": sentence(rng, rng.randrange(20, 120))}],
                                  "usage": {"input_tokens": 12, "output_tokens": 340}}))
    print(out, size, "bytes", turn, "turns")


main()
