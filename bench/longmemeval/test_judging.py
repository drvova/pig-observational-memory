"""Tests for the judge harness with scripted fake judges; no model is asked.

    python3 -m unittest discover -s bench/longmemeval -p 'test_*.py'
"""

import json
import os
import re
import sys
import tempfile
import unittest
from types import SimpleNamespace

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import judging  # noqa: E402
import lme  # noqa: E402

RESPONSE = re.compile(r"Model Response: (.*)\n\n(?:Is the model|Does the model)", re.S)


def response_of(prompt):
    return RESPONSE.search(prompt).group(1)


class Scripted:
    """A judge that accepts exactly the responses in ``accept``, optionally flipping chosen runs,
    and records every prompt it was asked."""

    def __init__(self, accept, flip=None):
        self.accept, self.flip, self.asked = set(accept), flip or {}, []

    def __call__(self, prompt, rep):
        self.asked.append((prompt, rep))
        r = response_of(prompt)
        ok = r in self.accept
        if (r, rep) in self.flip:
            ok = not ok
        return "yes" if ok else "no"


def positives():
    return {it["response"] for it in judging.control_items() if it["class"] == judging.POSITIVE}


def questions():
    return [
        {"question_id": "q1", "question_type": "single-session-user", "question": "Where?", "answer": "Lisbon", "hypothesis": "Lisbon."},
        {"question_id": "q2", "question_type": "multi-session", "question": "How many?", "answer": "3", "hypothesis": "You have 3 of them."},
        {"question_id": "q3", "question_type": "multi-session", "question": "Which?", "answer": "the red one", "hypothesis": "It was the red one, I think."},
        {"question_id": "q4", "question_type": "single-session-user", "question": "Who?", "answer": "Priya", "hypothesis": "I don't see that."},
        {"question_id": "q5_abs", "question_type": "single-session-user", "question": "When?", "answer": "not mentioned", "hypothesis": "not mentioned"},
    ]


class ControlsTest(unittest.TestCase):
    def test_scenarios(self):
        self.assertEqual(len(judging.SCENARIOS), judging.CONTROL_GROUP)
        items = judging.control_items()
        self.assertEqual(len(items), judging.CONTROL_GROUP * len(judging.CONTROL_CLASSES))
        for s in judging.SCENARIOS:
            self.assertIn(s["gold"], s["correct"])
            self.assertNotIn(s["gold"], s["wrong"])
            self.assertNotEqual(s["perturbed"], s["correct"])
        for it in items:
            if it["class"] == "stale_as_current":
                s = next(x for x in judging.SCENARIOS if x["question"] == it["question"]["question"])
                self.assertIn(s["old"], it["response"])
                self.assertNotIn(s["gold"], it["response"])
            if it["class"] == "unordered_both":
                s = next(x for x in judging.SCENARIOS if x["question"] == it["question"]["question"])
                self.assertIn(s["old"], it["response"])
                self.assertIn(s["gold"], it["response"])
        self.assertEqual(len({it["response"] for it in items}), len(items))


class MechanicalTest(unittest.TestCase):
    def test_rules(self):
        m = judging.mechanical
        q = {"question_id": "a", "question_type": "single-session-user", "answer": "Lisbon"}
        self.assertEqual(m(q, "lisbon."), ("exact", True))
        self.assertEqual(m(q, "It is Lisbon."), (None, None))
        n = {"question_id": "b", "question_type": "multi-session", "answer": "3"}
        self.assertEqual(m(n, "You have 3 of them."), ("regex", True))
        self.assertEqual(m(n, "Not 3, but 4."), (None, None))
        self.assertEqual(m(n, "It isn't 3."), (None, None))
        self.assertEqual(m(dict(n, question_type="temporal-reasoning"), "3 days"), (None, None))
        self.assertEqual(m({"question_id": "c_abs", "answer": "x"}, "x"), (None, None))
        self.assertEqual(m({"question_id": "d", "question_type": "single-session-preference", "answer": "x"}, "x"), (None, None))
        money = {"question_id": "e", "question_type": "multi-session", "answer": "$1,200"}
        self.assertEqual(m(money, "It cost $1,200 in total."), ("regex", True))
        self.assertEqual(m(money, "It cost $1,200, then $1,500."), (None, None))
        self.assertEqual(m(money, "$1,200"), ("exact", True))
        matcher = {"accept": r"\bred\b", "reject": r"\bblue\b"}
        self.assertEqual(m(q, "red", matcher), ("regex", True))
        self.assertEqual(m(q, "red or blue", matcher), ("regex", False))


class PassTest(unittest.TestCase):
    def test_sound_judge_valid_and_majority(self):
        qs = questions()
        flip = {("It was the red one, I think.", 0): True}
        for it in judging.control_items():
            if it["class"] == "stale_as_current":
                flip[(it["response"], 1)] = True
                break
        judge = Scripted(positives() | {"It was the red one, I think."}, flip)
        res = judging.run_pass(qs, judge, lme.judge_prompt, seed=7)
        v = res["verdicts"]
        self.assertEqual(v["q1"]["method"], "exact")
        self.assertEqual(v["q2"]["method"], "regex")
        self.assertEqual(v["q3"], dict(v["q3"], method="judge", votes=[False, True, True], verdict=True, unanimous=False))
        self.assertFalse(v["q4"]["verdict"])
        self.assertEqual(v["q5_abs"]["method"], "judge")
        self.assertFalse(res["validity"]["void"], res["validity"]["reasons"])
        self.assertEqual(res["validity"]["accepted"]["stale_as_current"], [0, 1, 0])
        self.assertEqual(res["counts"], {"exact": 1, "regex": 1, "judge": 3, "unanimous": 2})
        # Every judged prompt is asked three times; controls are mixed among the questions.
        prompts = [p for p, _ in judge.asked]
        self.assertEqual(len(prompts), 3 * (3 + len(judging.control_items())))
        first_question = min(i for i, p in enumerate(prompts) if "Which?" in p)
        last_control = max(i for i, p in enumerate(prompts) if "step goal" in p)
        self.assertLess(first_question, last_control)

    def test_lenient_judge_voids_pass(self):
        items = judging.control_items()
        stale = [it["response"] for it in items if it["class"] == "stale_as_current"]
        both = [it["response"] for it in items if it["class"] == "unordered_both"]
        # One accepted decoy per class is within the threshold; two void the pass.
        ok = judging.run_pass([], Scripted(positives() | {stale[0], both[0]}), lme.judge_prompt)
        self.assertFalse(ok["validity"]["void"])
        bad = judging.run_pass([], Scripted(positives() | set(stale[:2])), lme.judge_prompt)
        self.assertTrue(bad["validity"]["void"])
        self.assertTrue(all("stale_as_current" in r for r in bad["validity"]["reasons"]))
        self.assertEqual(len(bad["validity"]["reasons"]), 3)

    def test_substring_judge_fails_perturbed_detail(self):
        def substring(prompt, rep):
            gold = re.search(r"Correct Answer: (.*)\n\nModel Response", prompt, re.S).group(1)
            return "yes" if gold in response_of(prompt) else "no"
        res = judging.run_pass([], substring, lme.judge_prompt)
        self.assertTrue(res["validity"]["void"])
        reasons = " ".join(res["validity"]["reasons"])
        self.assertIn("perturbed_detail", reasons)
        self.assertIn("unordered_both", reasons)

    def test_control_groups(self):
        # A judge that rejects everything fails the known-correct group.
        res = judging.run_pass([], Scripted(set()), lme.judge_prompt)
        self.assertTrue(res["validity"]["void"])
        self.assertIn("known-correct", res["validity"]["reasons"][0])
        wrong = {it["response"] for it in judging.control_items() if it["class"] == judging.NEGATIVE}
        res = judging.run_pass([], Scripted(positives() | set(list(wrong)[:2])), lme.judge_prompt)
        self.assertTrue(any("known-wrong" in r for r in res["validity"]["reasons"]))


class StatsTest(unittest.TestCase):
    def test_mcnemar(self):
        self.assertEqual(judging.mcnemar_exact(0, 0), 1.0)
        self.assertAlmostEqual(judging.mcnemar_exact(10, 0), 2 / 1024)
        self.assertAlmostEqual(judging.mcnemar_exact(3, 7), judging.mcnemar_exact(7, 3))
        self.assertEqual(judging.mcnemar_exact(5, 5), 1.0)

    def test_paired_and_ship_rule(self):
        base = {"q%d" % i: i < 80 for i in range(100)}
        cand = dict(base, q80=True, q81=True, q82=True, q83=True, q5=False)
        s = judging.paired(base, cand, control_ids=["q%d" % i for i in range(50)], resamples=2000, seed=1)
        self.assertEqual((s["n"], len(s["gained"]), len(s["lost"]), s["net"]), (100, 4, 1, 3))
        self.assertEqual(s["control"], {"n": 50, "missing": 0, "lost": ["q5"]})
        lo, hi = s["ci95"]
        self.assertLessEqual(lo, s["diff"])
        self.assertGreaterEqual(hi, s["diff"])
        self.assertEqual(judging.paired(base, cand, resamples=2000, seed=1)["ci95"], [lo, hi])
        valid = {"void": False, "reasons": []}
        rule = judging.ship_rule(s, valid, valid)
        # One control loss in 50 is 2%, above judge noise.
        self.assertEqual(rule["verdict"], "do not ship")
        s2 = judging.paired(base, dict(base, q80=True, q81=True), control_ids=["q%d" % i for i in range(50)], resamples=2000)
        self.assertEqual(judging.ship_rule(s2, valid, valid)["verdict"], "ship")
        self.assertEqual(judging.ship_rule(s2, valid, {"void": True, "reasons": ["x"]})["verdict"], "void")
        self.assertEqual(judging.ship_rule(judging.paired(base, base), valid, valid)["verdict"], "do not ship")


class CommandTest(unittest.TestCase):
    """lme.py judge and compare over a fake run directory, with a judge command scripted in Python."""

    def make_run(self, root, name, hyps):
        out = os.path.join(root, name)
        dirs = {}
        for qid, (qtype, answer, hyp, self_ok) in hyps.items():
            d = "d" + qid
            dirs[qid] = d
            os.makedirs(os.path.join(out, "questions", d))
            lme.write_json(os.path.join(out, "questions", d, "result.json"), {
                "question_id": qid, "question_type": qtype, "question": "Q " + qid + "?", "answer": answer,
                "hypothesis": hyp, "correct": self_ok})
        lme.write_json(os.path.join(out, "run.json"), {"settings": {"model": "answerer", "pig": "pig"},
                                                        "selected": sorted(hyps) + ["missing"], "dirs": dirs,
                                                        "types": {"missing": "multi-session"}})
        return out

    def test_judge_and_compare(self):
        accept = sorted(positives() | {"A good answer."})
        with tempfile.TemporaryDirectory() as root:
            script = os.path.join(root, "judge.py")
            with open(script, "w") as f:
                f.write("import json, re, sys\nok = set(json.load(open(%r)))\np = sys.stdin.read()\n"
                        "r = re.search(r'Model Response: (.*)\\n\\n(?:Is the model|Does the model)', p, re.S).group(1)\n"
                        "print('yes' if r in ok else 'no')\n" % os.path.join(root, "accept.json"))
            lme.write_json(os.path.join(root, "accept.json"), accept)
            base = self.make_run(root, "base", {
                "a": ("multi-session", "Lisbon", "Lisbon", True),
                "b": ("multi-session", "Porto", "No idea.", False),
                "c": ("multi-session", "Oslo", "A good answer.", False)})
            cand = self.make_run(root, "cand", {
                "a": ("multi-session", "Lisbon", "Lisbon", True),
                "b": ("multi-session", "Porto", "A good answer.", True),
                "c": ("multi-session", "Oslo", "A good answer.", True)})
            cmd = "%s -I %s" % (sys.executable, script)
            args = SimpleNamespace(judge_model="", judge_command=cmd, judge_family="family-b", answer_family="family-a",
                                   judge_thinking="medium", judge_timeout=60, label="fake", runs=3, seed=0, workers=4,
                                   matchers="", force_settings=False)
            for out in (base, cand):
                self.assertEqual(lme.cmd_judge(SimpleNamespace(out=out, **vars(args))), 0)
            summary = lme.read_json(os.path.join(cand, "judges", "fake", "summary.json"))
            self.assertEqual((summary["correct"], summary["n"], summary["absent"]), (3, 4, 1))
            self.assertFalse(summary["validity"]["void"])
            # A second pass asks nothing: every reply is cached.
            lme.cmd_judge(SimpleNamespace(out=cand, **vars(args)))
            self.assertEqual(lme.read_json(os.path.join(cand, "judges", "fake", "summary.json"))["asked"], 0)
            with self.assertRaises(SystemExit):
                lme.cmd_judge(SimpleNamespace(out=cand, **dict(vars(args), answer_family="Family-B")))
            report = os.path.join(root, "compare.md")
            ids = os.path.join(root, "both.json")
            lme.cmd_compare(SimpleNamespace(base=base, cand=cand, judge="fake", cand_judge="", control_ids="",
                                            write_control_ids=ids, resamples=500, seed=0, report=report))
            text = open(report).read()
            self.assertIn("1 gained, 0 lost, net +1", text)
            self.assertEqual(json.load(open(ids))["ids"], ["a", "c"])


if __name__ == "__main__":
    unittest.main()
