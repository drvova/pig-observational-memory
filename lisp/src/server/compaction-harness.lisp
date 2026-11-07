;;;; The harness's part of a PiG compaction: the one hook point the compaction event calls
;;;; (docs/lisp-port-peer.md, "The harness hook point"; docs/lisp-port-evidence.md). Go's
;;;; buildCompactionV2 (memory/hooks/compaction_v2.go), for a session model with a curated store
;;;; (harness.For), runs the harness detectors inside the cut range's extraction (detectV2),
;;;; reads the model's profile (ApplySummary, the layout and the codec), records the compaction as
;;;; the model's evidence unit (recordCompactionV2), settles the held definitions
;;;; (settleDefinitionsV2) and starts the detector pass. The image runs the detectors, records the
;;;; pass, decides the rules, settles its own held definitions and judges its settled ones
;;;; (om.layer:record-compaction-evidence). It migrates rules written before the layer and applies
;;;; the session model's profile (src/harness/rule-migration.lisp, profile.lisp); it refuses only a
;;;; layer that declares a format. Go detectors are never run (decisions 4 and 5).

(in-package "OM.SERVER")

(defstruct (compaction-harness (:conc-name ch-))
  "One compaction as the harness sees it. Before the compaction: conn, the session model
(its object, nil for none) and model-key (harness.ModelKey), store-dir (the curated store,
\"\" without one), cfg (the connection's rcfg) and event (the decoded om::cevent). After it:
entries (the branch the compaction folded, level 0's batches included), cut (the cut range's
source entries) and result (the om::cresult answered). snapshot is the layer's held definitions
as they stood before the compaction ran (om.layer:take-snapshot)."
  conn model (model-key "") (store-dir "") cfg event entries cut result snapshot
  ;; level1 is the compaction's level-1 result (server/level1.lisp's l1), nil when level 1 did not
  ;; run; held-instructions the model's held consolidation instructions before level 1 ran.
  level1 held-instructions
  ;; The session model's profile (src/harness/profile.lisp), set by compaction-harness-refusal; nil
  ;; is harness.Default.
  profile)

(defun ch-harness-p (ch) (and (string/= (ch-store-dir ch) "") (string/= (ch-model-key ch) "")))

(defun compaction-harness-refusal (ch)
  "What the harness would do at this compaction that the image does not, as a not-ported text,
or nil. It runs before the compaction's first side effect, so the Go engine can run the whole
event instead."
  (when (ch-harness-p ch)
    ;; Rules written before the layer are migrated first (rules.migrate), and the profile is
    ;; applied (src/harness/profile.lisp); only a format the model declared is refused.
    (om.layer::migrate-legacy-rules (ch-store-dir ch))
    (or (om.layer::profile-refusal (ch-store-dir ch) (ch-model-key ch))
        (progn (setf (ch-profile ch) (om.layer::model-profile (ch-store-dir ch) (ch-model-key ch)))
               nil))))

(defun harness-hook-host (ch h)
  "The hook's host h with the harness detectors over the cut range's extraction (detectV2) for
a session model with a store, and the layer's snapshot taken (before level 1)."
  (when (ch-harness-p ch)
    (let ((store (ch-store-dir ch)) (model (ch-model-key ch)))
      (setf (ch-snapshot ch) (om.layer:take-snapshot store model))
      ;; The held consolidation instructions level 1 runs with are credited too (decision 8).
      (let ((held (level1-held-snapshot store model)))
        (when held
          (setf (ch-held-instructions ch) held
                (om.layer::snapshot-ran-held (ch-snapshot ch))
                (sort (append (om.layer::snapshot-ran-held (ch-snapshot ch))
                              (mapcar #'level1-held-id held))
                      #'string<))))
      (setf (om::hhost-detect h)
            (lambda (entries start end folded batches covers)
              (om.layer:harness-detect store model entries start end folded batches covers
                                       :defined (and (string/= covers "") (defined-queries (ch-conn ch) store)))))))
  h)

(defun after-compaction (ch)
  "The hook point after a compaction PiG may persist (Go: recordCompactionV2 and
settleDefinitionsV2, run only while the request is live). It runs after the answer is built and
before it is sent; an error here is logged and never changes the answer."
  ;; promoteCoreV2: the session's constraints and open intentions offered to the core.
  (when (string/= (ch-store-dir ch) "")
    (handler-case (om.layer::core-promote-ledger (ch-store-dir ch) (ch-entries ch) (session-identity) (scope-cwd)
                                                 (host-repo-id) (ch-model-key ch))
      (request-cancelled (e) (error e))
      (error (e) (format *error-output* "om engine: core promote: ~A~%" e))))
  (when (ch-harness-p ch)
    (let ((notices (om.layer:record-compaction-evidence
                    :store (ch-store-dir ch) :model (ch-model-key ch) :session (session-identity)
                    :entries (ch-entries ch)
                    :cut-ids (mapcar #'om::entry-id (coerce (ch-cut ch) 'list))
                    :counts (and (ch-level1 ch) (l1-counts (ch-level1 ch)))
                    :level1 (and (ch-level1 ch) (l1-answered (ch-level1 ch)))
                    :snapshot (ch-snapshot ch))))
      ;; settleDefinitionsV2 for the held instructions the compaction ran with.
      (dolist (n (settle-level1-held (ch-store-dir ch) (ch-model-key ch) (ch-held-instructions ch)))
        (notify-ui n "info"))
      ;; rollbackInsecureV2: a written definition that weakens security rolls the layer back.
      (dolist (n (rollback-insecure-layer (ch-store-dir ch) (ch-model-key ch)))
        (notify-ui n "warning"))
      (dolist (n notices)
        (notify-ui (if (stringp n)
                       n
                       (destructuring-bind (name written reason) n
                         (format nil "Observational memory: layer definition adaptable/~A for ~A ~A (~A)"
                                 name (ch-model-key ch) (if written "settled" "reverted") reason)))
                   "info")))))
