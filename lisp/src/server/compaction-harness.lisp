;;;; The harness's part of a PiG compaction: the one hook point the compaction event calls
;;;; (docs/lisp-port-peer.md, "The harness hook point"; docs/lisp-port-evidence.md). Go's
;;;; buildCompactionV2 (memory/hooks/compaction_v2.go), for a session model with a curated store
;;;; (harness.For), runs the harness detectors inside the cut range's extraction (detectV2),
;;;; reads the model's profile (ApplySummary, the layout and the codec), records the compaction as
;;;; the model's evidence unit (recordCompactionV2), settles the held definitions
;;;; (settleDefinitionsV2) and starts the detector pass. The image runs the detectors, records the
;;;; pass, decides the rules, settles its own held definitions and judges its settled ones
;;;; (om.layer:record-compaction-evidence). It refuses what it would do differently: a profile
;;;; that may not be the default (the profile is not ported), and a store the harness reads only in
;;;; Go (rules written before the layer). Go detectors are never run (decisions 4 and 5).

(in-package "OM.SERVER")

(defstruct (compaction-harness (:conc-name ch-))
  "One compaction as the harness sees it. Before the compaction: conn, the session model
(its object, nil for none) and model-key (harness.ModelKey), store-dir (the curated store,
\"\" without one), cfg (the connection's rcfg) and event (the decoded om::cevent). After it:
entries (the branch the compaction folded, level 0's batches included), cut (the cut range's
source entries) and result (the om::cresult answered). snapshot is the layer's held definitions
as they stood before the compaction ran (om.layer:take-snapshot)."
  conn model (model-key "") (store-dir "") cfg event entries cut result snapshot)

(defun ch-harness-p (ch) (and (string/= (ch-store-dir ch) "") (string/= (ch-model-key ch) "")))

(defun profile-may-differ-p (store model)
  "Whether harness.ComputeWith and ApplyRules could give model a profile other than the default:
a v2 decode event or a code event on the summary in its ledger (the votes the profile counts), or
an active switch-codec rule. The profile is not ported, so such a compaction is refused."
  (or (some (lambda (e) (and (string= (om.layer::ev-string e "engine") "v2")
                             (or (string= (om.layer::ev-string e "kind") "decode")
                                 (and (string= (om.layer::ev-string e "kind") "code")
                                      (string= (om.layer::ev-string e "target") "summary")))))
            (om.layer::model-events store model))
      (om.layer::rules-have-p (om.layer::active-rules store model) "switch-codec")))

(defun compaction-harness-refusal (ch)
  "What the harness would do at this compaction that the image does not, as a not-ported text,
or nil. It runs before the compaction's first side effect, so the Go engine can run the whole
event instead."
  (when (ch-harness-p ch)
    (cond ((om.layer::go-legacy-rules-p (ch-store-dir ch))
           "event/sessionBeforeCompact with rules written before the layer (rules.migrate)")
          ((profile-may-differ-p (ch-store-dir ch) (ch-model-key ch))
           "event/sessionBeforeCompact for a session model whose profile may not be the default (harness.ComputeWith)"))))

(defun harness-hook-host (ch h)
  "The hook's host h with the harness detectors over the cut range's extraction (detectV2) for
a session model with a store, and the layer's snapshot taken (before level 1)."
  (when (ch-harness-p ch)
    (let ((store (ch-store-dir ch)) (model (ch-model-key ch)))
      (setf (ch-snapshot ch) (om.layer:take-snapshot store model))
      (setf (om::hhost-detect h)
            (lambda (entries start end folded batches covers)
              (om.layer:harness-detect store model entries start end folded batches covers)))))
  h)

(defun after-compaction (ch)
  "The hook point after a compaction PiG may persist (Go: recordCompactionV2 and
settleDefinitionsV2, run only while the request is live). It runs after the answer is built and
before it is sent; an error here is logged and never changes the answer."
  (when (ch-harness-p ch)
    (let ((notices (om.layer:record-compaction-evidence
                    :store (ch-store-dir ch) :model (ch-model-key ch) :session (session-identity)
                    :entries (ch-entries ch)
                    :cut-ids (mapcar #'om::entry-id (coerce (ch-cut ch) 'list))
                    :snapshot (ch-snapshot ch))))
      (dolist (n notices)
        (notify-ui (if (stringp n)
                       n
                       (destructuring-bind (name written reason) n
                         (format nil "Observational memory: layer definition adaptable/~A for ~A ~A (~A)"
                                 name (ch-model-key ch) (if written "settled" "reverted") reason)))
                   "info")))))
