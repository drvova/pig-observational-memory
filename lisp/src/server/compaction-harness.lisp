;;;; The harness's part of a PiG compaction: the one hook point the compaction event calls
;;;; (docs/lisp-port-peer.md, "The harness hook point"). Go's buildCompactionV2
;;;; (memory/hooks/compaction_v2.go) does three things for a session model with a curated store
;;;; (harness.For): it reads the model's profile from its usage ledger (ApplySummary, the layout
;;;; and the codec), records the compaction as the model's evidence unit (recordCompactionV2:
;;;; rules.AfterPass's pass event and the rule evaluation), settles the held definitions
;;;; (settleDefinitionsV2) and starts the detector pass. None of that is ported here.
;;;;
;;;; The evidence work package replaces these two functions (and only these): it lifts the
;;;; refusal for what it ports and records and settles in after-compaction. Everything they
;;;; need is in the compaction-harness struct the event fills.

(in-package "OM.SERVER")

(defstruct (compaction-harness (:conc-name ch-))
  "One compaction as the harness sees it. Before the compaction: conn, the session model
(its object, nil for none) and model-key (harness.ModelKey), store-dir (the curated store,
\"\" without one), cfg (the connection's rcfg) and event (the decoded om::cevent). After it:
entries (the branch the compaction folded, level 0's batches included), cut (the cut range's
source entries) and result (the om::cresult answered)."
  conn model (model-key "") (store-dir "") cfg event entries cut result)

(defun compaction-harness-refusal (ch)
  "What the harness would do at this compaction that the image does not, as a not-ported text,
or nil. It runs before the compaction's first side effect, so the Go engine can run the whole
event instead. With a session model and a store, Go records evidence, settles and reads the
model's profile, which are not ported."
  (when (and (string/= (ch-store-dir ch) "") (string/= (ch-model-key ch) ""))
    "event/sessionBeforeCompact for a session model with a curated store (its profile, the pass event, settling held definitions, the detector pass)"))

(defun after-compaction (ch)
  "The hook point after a compaction PiG may persist (Go: recordCompactionV2,
settleDefinitionsV2 and startDetectorPassV2, run only while the request is live). It runs
after the answer is built and before it is sent; an error here is logged and never changes
the answer. Nothing is recorded until the evidence work package fills it."
  (declare (ignore ch))
  nil)
