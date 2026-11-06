"""Judge harness for the LongMemEval bench (docs/brain-memory.md, decision 21).

Python 3 standard library only, no model access of its own: the caller hands every function that
needs a verdict an ``ask(prompt, rep)`` callable, so tests script the judge and lme.py plugs in
the configured judge (lme.py ``judge``). What it adds to the bench's judging:

- **Mechanical first.** An answer that matches its gold answer exactly (after normalization), or a
  numeric gold answer that is the only number in the answer, or a configured accept pattern, is
  scored without the judge. A mechanical stage only fails an answer when a configured reject
  pattern says so; everything else goes to the judge.
- **Three runs, majority vote.** Every judged prompt is asked ``runs`` times (3) and the verdict is
  the majority; the unanimity rate is reported.
- **Controls and decoys on every pass.** Twelve authored update scenarios give 12 known-correct and
  12 known-wrong controls and 12 decoys in each of TWIST's four wrong-answer classes: a stale value
  stated as current, the correct answer with one detail changed, a fluent non-answer, and the old
  and new values given without saying which holds. They are judged with the same template as the
  bench's knowledge-update questions, shuffled among the real prompts. A pass is void when, in any
  of its runs, the judge accepts more than 1 of 12 in any decoy class or labels fewer than 11 of
  12 controls in either control group correctly.
- **Paired comparison and the ship rule.** ``paired`` reports gained and lost questions, McNemar's
  exact p-value, a paired-bootstrap 95% interval of the accuracy difference, and the losses on a
  control set both arms passed before; ``ship_rule`` applies the Tier 1 promotion rule.
"""

import concurrent.futures
import hashlib
import json
import math
import random
import re
import unicodedata

RUNS = 3
CONTROL_GROUP = 12
# A control group passes with at least 11 of 12 labelled correctly; a decoy class with at most 1
# of 12 accepted (arxiv_evaluation.md: 2609.38021's control threshold, TWIST's decoy classes).
CONTROL_MIN_CORRECT = 11
DECOY_MAX_ACCEPTED = 1

POSITIVE, NEGATIVE = "positive", "negative"
DECOY_CLASSES = ("stale_as_current", "perturbed_detail", "fluent_non_answer", "unordered_both")
CONTROL_CLASSES = (POSITIVE, NEGATIVE) + DECOY_CLASSES

# Twelve authored update scenarios: the question, the current value (gold), the value it replaced
# (old), a correct answer, an unrelated wrong answer, the correct answer with one detail changed,
# and a fluent answer that gives no value.
SCENARIOS = [
    {"question": "What is my current daily step goal?", "gold": "12,000 steps", "old": "10,000 steps",
     "correct": "Your current daily step goal is 12,000 steps.",
     "wrong": "Your daily step goal is 4,500 steps.",
     "perturbed": "Your current step goal is 12,000 steps a week.",
     "non_answer": "Step goals are a great way to stay active, and it is worth picking one that fits your routine and adjusting it as your fitness improves."},
    {"question": "Which city does my sister live in now?", "gold": "Lisbon", "old": "Porto",
     "correct": "Your sister lives in Lisbon now.",
     "wrong": "Your sister lives in Oslo.",
     "perturbed": "Your brother lives in Lisbon now.",
     "non_answer": "It sounds like your sister has moved around a fair bit; keeping in touch across cities can take some planning, but it is usually worth it."},
    {"question": "What time is my weekly team meeting now?", "gold": "Tuesday at 10am", "old": "Monday at 9am",
     "correct": "Your weekly team meeting is on Tuesday at 10am.",
     "wrong": "Your weekly team meeting is on Friday at 4pm.",
     "perturbed": "Your weekly team meeting is on Thursday at 10am.",
     "non_answer": "Regular team meetings help keep everyone aligned, and choosing a slot that works for the whole team is often the hardest part."},
    {"question": "How much is my monthly gym membership now?", "gold": "$45", "old": "$39",
     "correct": "Your gym membership now costs $45 a month.",
     "wrong": "Your gym membership costs $120 a month.",
     "perturbed": "Your gym membership now costs $45 a year.",
     "non_answer": "Gym prices vary a lot by location and amenities, so it is a good idea to review what you get for the fee every so often."},
    {"question": "What is the name of my current manager?", "gold": "Priya", "old": "Daniel",
     "correct": "Your current manager is Priya.",
     "wrong": "Your manager is Gregor.",
     "perturbed": "Your current manager is Priyanka.",
     "non_answer": "A good relationship with your manager makes a big difference; regular one-on-ones are a great way to keep it healthy."},
    {"question": "Which port does my development server use now?", "gold": "8443", "old": "8080",
     "correct": "Your development server now runs on port 8443.",
     "wrong": "Your development server runs on port 3000.",
     "perturbed": "Your production server now runs on port 8443.",
     "non_answer": "Development servers can run on many ports, and picking one that does not clash with other services keeps things simple."},
    {"question": "How many hours a night am I aiming to sleep now?", "gold": "8 hours", "old": "7 hours",
     "correct": "You are now aiming for 8 hours of sleep a night.",
     "wrong": "You are aiming for 5 hours of sleep a night.",
     "perturbed": "You are now aiming for 8 hours of sleep on weekends only.",
     "non_answer": "Sleep is one of the most important parts of a healthy routine, and a consistent schedule helps a lot with feeling rested."},
    {"question": "What breed is my new dog?", "gold": "a border collie", "old": "a beagle",
     "correct": "Your new dog is a border collie.",
     "wrong": "Your new dog is a poodle.",
     "perturbed": "Your new cat is a border collie.",
     "non_answer": "Congratulations on the new dog! Every breed has its own temperament, and the first few weeks are a wonderful time to bond."},
    {"question": "Which language am I learning at the moment?", "gold": "Japanese", "old": "Italian",
     "correct": "You are learning Japanese at the moment.",
     "wrong": "You are learning Swahili at the moment.",
     "perturbed": "You are teaching Japanese at the moment.",
     "non_answer": "Learning a new language is a rewarding challenge; a little practice every day tends to work better than long, rare sessions."},
    {"question": "What is my current savings target for the trip?", "gold": "$3,000", "old": "$2,000",
     "correct": "Your current savings target for the trip is $3,000.",
     "wrong": "Your savings target for the trip is $750.",
     "perturbed": "Your current savings target for the trip is $3,000 per person per month.",
     "non_answer": "Saving for a trip is much easier with a clear plan, and setting aside a fixed amount each payday can help you get there."},
    {"question": "Which branch do I deploy from now?", "gold": "release", "old": "main",
     "correct": "You deploy from the release branch now.",
     "wrong": "You deploy from the gh-pages branch.",
     "perturbed": "You merge into the release branch now, and deploy from staging.",
     "non_answer": "Choosing a deployment branch is an important decision, and keeping it protected with reviews is a good practice."},
    {"question": "How far is my next race?", "gold": "a half marathon", "old": "a 10K",
     "correct": "Your next race is a half marathon.",
     "wrong": "Your next race is a 400-meter sprint.",
     "perturbed": "Your next race is a half marathon relay.",
     "non_answer": "Training for a race is exciting! Building up your distance gradually and resting well will help you arrive at the start line ready."},
]


def control_items():
    """Every control and decoy item: a pseudo question (knowledge-update, so it is judged with the
    bench's knowledge-update template), the response to judge and the verdict a sound judge gives."""
    items = []
    for i, s in enumerate(SCENARIOS):
        q = {"question_id": "control-%02d" % i, "question_type": "knowledge-update",
             "question": s["question"], "answer": s["gold"]}
        responses = {
            POSITIVE: s["correct"],
            NEGATIVE: s["wrong"],
            "stale_as_current": s["correct"].replace(s["gold"], s["old"]),
            "perturbed_detail": s["perturbed"],
            "fluent_non_answer": s["non_answer"],
            "unordered_both": "It is either %s or %s." % ((s["old"], s["gold"]) if i % 2 == 0 else (s["gold"], s["old"])),
        }
        for cls in CONTROL_CLASSES:
            items.append({"id": "%s/%s" % (q["question_id"], cls), "class": cls, "question": q,
                          "response": responses[cls], "expect": cls == POSITIVE})
    return items


# ─── mechanical stage ─────────────────────────────────────────────────────────

NUMBER = re.compile(r"\d+(?:\.\d+)?")
# An answer that negates or hedges is never scored by the number rule.
NEGATION = re.compile(r"\b(?:not|no|never|none|nor|isn t|wasn t|don t|doesn t|didn t|unsure|unknown|unclear|maybe|perhaps|either)\b")


def normalize(text):
    """Lower case, NFKC, punctuation dropped (a decimal point and digit groups kept as digits),
    whitespace collapsed."""
    t = unicodedata.normalize("NFKC", str(text)).lower()
    t = re.sub(r"(?<=\d),(?=\d{3}\b)", "", t)
    t = re.sub(r"[^\w\s.]", " ", t)
    t = re.sub(r"\.(?!\d)", " ", t)
    return " ".join(t.split())


def mechanical(q, hypothesis, matcher=None):
    """("exact" | "regex", verdict) when code decides the answer, else (None, None).

    Abstention and preference questions always go to the judge. A matcher is a question's
    configured {"accept": regex, "reject": regex}; reject is checked first."""
    if q.get("question_id", "").endswith("_abs") or q.get("question_type") == "single-session-preference":
        return None, None
    if matcher:
        if matcher.get("reject") and re.search(matcher["reject"], hypothesis, re.I):
            return "regex", False
        if matcher.get("accept") and re.search(matcher["accept"], hypothesis, re.I):
            return "regex", True
    gold = q.get("answer")
    gold = gold if isinstance(gold, str) else json.dumps(gold)
    g, h = normalize(gold), normalize(hypothesis)
    if not g:
        return None, None
    if g == h:
        return "exact", True
    if (NUMBER.fullmatch(g) and NUMBER.findall(h) == [g] and q.get("question_type") != "temporal-reasoning"
            and not NEGATION.search(h)):
        # The gold answer is one number and it is the only number the answer gives. Temporal
        # questions go to the judge, whose template forgives an off-by-one count.
        return "regex", True
    return None, None


# ─── judging ──────────────────────────────────────────────────────────────────

def parse_reply(reply):
    """The bench's verdict rule (LongMemEval's evaluate_qa.py): a reply containing "yes"."""
    return "yes" in (reply or "").strip().lower()


def prompt_key(prompt):
    return hashlib.sha256(prompt.encode("utf-8")).hexdigest()


def run_pass(questions, ask, prompt_for, runs=RUNS, matchers=None, seed=0, workers=1):
    """One judging pass: ``questions`` are dicts with the question fields and ``hypothesis``.

    Returns the per-question verdicts (method, votes, verdict), the control results by class and
    run, and whether the pass is valid. Every judged prompt (questions, controls and decoys) is
    asked in one seeded shuffled order, so the judge sees controls mixed blind among real items."""
    matchers = matchers or {}
    verdicts, jobs = {}, []
    for q in questions:
        method, verdict = mechanical(q, q["hypothesis"], matchers.get(q["question_id"]))
        if method:
            verdicts[q["question_id"]] = {"method": method, "verdict": verdict, "votes": None}
        else:
            jobs.append(("question", q["question_id"], prompt_for(q, q["hypothesis"])))
    items = control_items()
    for it in items:
        jobs.append(("control", it["id"], prompt_for(it["question"], it["response"])))
    order = list(range(len(jobs) * runs))
    random.Random(seed).shuffle(order)
    keys = [divmod(n, runs) for n in order]
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, workers)) as pool:
        got = list(pool.map(lambda k: ask(jobs[k[0]][2], k[1]), keys))
    replies = dict(zip(keys, got))
    controls = {}
    for j, (kind, ident, prompt) in enumerate(jobs):
        rs = [replies[(j, rep)] for rep in range(runs)]
        votes = [parse_reply(r) for r in rs]
        rec = {"method": "judge", "votes": votes, "verdict": sum(votes) * 2 > runs,
               "unanimous": len(set(votes)) == 1, "replies": rs}
        if kind == "question":
            verdicts[ident] = rec
        else:
            controls[ident] = rec
    validity = validate_controls(items, controls, runs)
    judged = [v for v in verdicts.values() if v["method"] == "judge"]
    return {"verdicts": verdicts, "controls": controls, "validity": validity,
            "counts": {"exact": sum(1 for v in verdicts.values() if v["method"] == "exact"),
                       "regex": sum(1 for v in verdicts.values() if v["method"] == "regex"),
                       "judge": len(judged),
                       "unanimous": sum(1 for v in judged if v["unanimous"])}}


def validate_controls(items, controls, runs=RUNS):
    """Per class and run: how many items the judge accepted. A pass is void when a decoy class has
    more than DECOY_MAX_ACCEPTED accepted in any run, or a control group fewer than
    CONTROL_MIN_CORRECT labelled correctly in any run."""
    by_class = {cls: [0] * runs for cls in CONTROL_CLASSES}
    for it in items:
        rec = controls.get(it["id"])
        if rec is None:
            continue
        for rep, vote in enumerate(rec["votes"]):
            if vote:
                by_class[it["class"]][rep] += 1
    reasons = []
    for rep in range(runs):
        if by_class[POSITIVE][rep] < CONTROL_MIN_CORRECT:
            reasons.append("run %d: %d of %d known-correct controls accepted" % (rep + 1, by_class[POSITIVE][rep], CONTROL_GROUP))
        if CONTROL_GROUP - by_class[NEGATIVE][rep] < CONTROL_MIN_CORRECT:
            reasons.append("run %d: %d of %d known-wrong controls accepted" % (rep + 1, by_class[NEGATIVE][rep], CONTROL_GROUP))
        for cls in DECOY_CLASSES:
            if by_class[cls][rep] > DECOY_MAX_ACCEPTED:
                reasons.append("run %d: %d of %d %s decoys accepted" % (rep + 1, by_class[cls][rep], CONTROL_GROUP, cls))
    return {"void": bool(reasons), "reasons": reasons, "accepted": by_class}


# ─── paired comparison ────────────────────────────────────────────────────────

def mcnemar_exact(b, c):
    """Two-sided exact McNemar p-value for b and c discordant pairs (binomial, p = 1/2)."""
    n = b + c
    if n == 0:
        return 1.0
    k = min(b, c)
    tail = sum(math.comb(n, i) for i in range(k + 1)) / 2.0 ** n
    return min(1.0, 2 * tail)


def bootstrap_ci(diffs, resamples=10000, seed=0, level=0.95):
    """Paired-bootstrap interval of the mean of per-question differences (cand - base, each -1, 0
    or 1), as accuracy points (fractions)."""
    n = len(diffs)
    if n == 0:
        return 0.0, 0.0
    rng = random.Random(seed)
    means = sorted(sum(diffs[rng.randrange(n)] for _ in range(n)) / n for _ in range(resamples))
    lo = means[int(math.floor((1 - level) / 2 * resamples))]
    hi = means[min(resamples - 1, int(math.ceil((1 + level) / 2 * resamples)) - 1)]
    return lo, hi


def paired(base, cand, control_ids=None, resamples=10000, seed=0):
    """Paired outcomes of two arms over the questions both answered: ``base`` and ``cand`` map a
    question id to its verdict. control_ids are questions both arms passed before (the both-passed
    control set); without it the set is the questions both pass here, reported for next time."""
    ids = sorted(set(base) & set(cand))
    gained = [q for q in ids if cand[q] and not base[q]]
    lost = [q for q in ids if base[q] and not cand[q]]
    diffs = [int(cand[q]) - int(base[q]) for q in ids]
    lo, hi = bootstrap_ci(diffs, resamples, seed)
    both = [q for q in ids if base[q] and cand[q]]
    out = {"n": len(ids), "base_correct": sum(base[q] for q in ids), "cand_correct": sum(cand[q] for q in ids),
           "gained": gained, "lost": lost, "net": len(gained) - len(lost),
           "mcnemar_p": mcnemar_exact(len(gained), len(lost)),
           "diff": (sum(diffs) / len(ids)) if ids else 0.0, "ci95": [lo, hi],
           "both_passed": both}
    if control_ids is not None:
        ctl = [q for q in control_ids if q in cand]
        out["control"] = {"n": len(ctl), "missing": len(control_ids) - len(ctl),
                          "lost": [q for q in ctl if not cand[q]]}
    return out


# Tier 1's promotion rule (arxiv_evaluation.md, Q6): non-negative net flips with the interval's lower
# bound at least -1 point, control-set losses within judge noise (1%), and no void judge pass. The
# stale-as-current and over-application rates need labelled stale answers this bench does not
# have; they are reported as not measured, and Tier 0 checks stale-as-current in the summary.
CI_FLOOR = -0.01
CONTROL_LOSS_CEILING = 0.01


def ship_rule(stats, base_validity, cand_validity):
    checks = []
    checks.append(("net flips >= 0", stats["net"] >= 0, "%+d (%d gained, %d lost)" % (stats["net"], len(stats["gained"]), len(stats["lost"]))))
    checks.append(("95% CI lower bound >= -1 point", stats["ci95"][0] >= CI_FLOOR, "[%.1f, %.1f] points" % (100 * stats["ci95"][0], 100 * stats["ci95"][1])))
    ctl = stats.get("control")
    if ctl and ctl["n"]:
        rate = len(ctl["lost"]) / ctl["n"]
        checks.append(("control-set losses <= 1%", rate <= CONTROL_LOSS_CEILING, "%d of %d (%.1f%%)" % (len(ctl["lost"]), ctl["n"], 100 * rate)))
    else:
        checks.append(("control-set losses <= 1%", False, "no both-passed control set given"))
    checks.append(("base judge pass valid", not base_validity["void"], "; ".join(base_validity["reasons"]) or "controls and decoys passed"))
    checks.append(("candidate judge pass valid", not cand_validity["void"], "; ".join(cand_validity["reasons"]) or "controls and decoys passed"))
    if base_validity["void"] or cand_validity["void"]:
        verdict = "void"
    else:
        verdict = "ship" if all(ok for _, ok, _ in checks) else "do not ship"
    return {"verdict": verdict, "checks": [{"rule": r, "ok": ok, "value": v} for r, ok, v in checks],
            "not_measured": ["stale-as-current rate of answers", "over-application rate"]}
