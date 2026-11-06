;;;; The per-model profile (memory/harness/profile.go, ledger.go Profile): Compute and ComputeWith
;;;; over the model's usage ledger (<store>/models/<model>.jsonl, memory v2's events only, one
;;;; evidence unit per compaction, MinEvidence), ApplyRules (switch-codec), ApplySummary and
;;;; SummaryFormat. A PiG compaction applies the session model's profile as buildCompactionV2 does
;;;; (om::*summary-profile*, read by src/hooks/compaction.lisp); a model whose layer declares a
;;;; format (declare.Active.CodecNames beyond the shipped codecs) is still refused, since declared
;;;; codecs are not ported.

(in-package "OM.LAYER")

(defparameter +codecs+ '("rows" "jsonl") "tables.Codecs()' names, the first the default.")
(defparameter +summary-floor+ 8000 "config.CompactionSummaryFallbackMaxTokens.")
(defparameter +max-summary-halvings+ (integer-length (floor 2000000 8000)) "harness.MaxSummaryHalvings.")

(defstruct (hswitch (:conc-name sw-)) setting to reason at)

(defstruct (hprofile (:conc-name hp-))
  (model "") (codec "rows") (layout "tables") (halvings 0) (switches '()) (events 0))

(defun switch-text (s)
  "Switch.String: \"YYYY-MM-DD HH:MM setting → to: reason\", the time left out when zero."
  (let ((at (if (not (zero-time-p (sw-at s)))
                (destructuring-bind (y m d hh mm &rest rest) (time-fields (cons (time-nanos (sw-at s)) 0))
                  (declare (ignore rest))
                  (format nil "~4,'0D-~2,'0D-~2,'0D ~2,'0D:~2,'0D " y m d hh mm))
                "")))
    (format nil "~A~A → ~A: ~A" at (sw-setting s) (sw-to s) (sw-reason s))))

(defun profile-halvings (p) (max 0 (min (hp-halvings p) +max-summary-halvings+)))

(defun apply-summary (p room)
  "Profile.ApplySummary."
  (if (<= room 0)
      room
      (let ((scaled (ash room (- (profile-halvings p)))))
        (min room (max scaled (min room +summary-floor+) 1)))))

(defun summary-format (p memory-format)
  "Profile.SummaryFormat: lines when the profile switched to it or memoryFormat asks for it."
  (if (or (string= (hp-layout p) "lines") (string= memory-format "lines")) "lines" "tables"))

(defun scale-text (halvings)
  (if (zerop halvings) "the whole room" (format nil "1/~D of the room" (ash 1 halvings))))

;;; Compute

(defstruct (tally (:conc-name t-)) (against 0) (keep 0))

(defun tally-zero-p (x) (and (zerop (t-against x)) (zerop (t-keep x))))
(defun tally-due-p (x) (and (>= (t-against x) +min-evidence+) (> (t-against x) (t-keep x))))
(defun tally-unit (x)
  (cond ((> (t-against x) (t-keep x)) (make-tally :against 1))
        ((> (t-keep x) (t-against x)) (make-tally :keep 1))
        (t (make-tally))))
(defun tally-add (into x) (incf (t-against into) (t-against x)) (incf (t-keep into) (t-keep x)))
(defun tally-reason (x against keep)
  (format nil "~D ~A vs ~D ~A since the last switch (needs at least ~D and a majority)"
          (t-against x) against (t-keep x) keep +min-evidence+))

(defstruct (votes (:conc-name v-)) (codec (make-tally)) (layout (make-tally)) (summary (make-tally)) (codec-name ""))

(defun compute-profile (model events &optional (codecs +codecs+))
  "harness.ComputeWith over the model's ledger events (decoded lines, in order)."
  (let* ((events (remove-if-not (lambda (e) (string= (ev-string e "engine") "v2")) events))
         (codecs (or codecs (list (first +codecs+))))
         (p (make-hprofile :model model :codec (first codecs) :events (length events)))
         (index 0) (codec (make-tally)) (layout (make-tally)) (summary (make-tally))
         (window (make-votes)))
    (labels ((record (setting to reason at)
               (setf (hp-switches p) (append (hp-switches p) (list (make-hswitch :setting setting :to to :reason reason :at at)))))
             (more-codecs-p () (< index (1- (length codecs))))
             (classify (e v)
               (let ((kind (ev-string e "kind")) (outcome (ev-string e "outcome")))
                 (cond ((string= kind "decode")
                        (when (and (string= (ev-string e "codec") (nth index codecs)) (more-codecs-p))
                          (setf (v-codec-name v) (ev-string e "codec"))
                          (if (string= outcome "error") (incf (t-against (v-codec v))) (incf (t-keep (v-codec v)))))
                        (when (string= (hp-layout p) "tables")
                          (cond ((string= outcome "columns") (incf (t-against (v-layout v))))
                                ((string= outcome "ok") (incf (t-keep (v-layout v)))))))
                       ((string= kind "code")
                        (when (string= (ev-string e "target") "summary")
                          (cond ((string= outcome "shown") (incf (t-against (v-summary v))))
                                ((string= outcome "omitted") (incf (t-keep (v-summary v))))))))))
             (apply-window (w at)
               (let ((v (make-votes :codec (tally-unit (v-codec w)) :layout (tally-unit (v-layout w))
                                    :summary (tally-unit (v-summary w)) :codec-name (v-codec-name w))))
                 (when (and (not (tally-zero-p (v-codec v))) (string= (v-codec-name v) (nth index codecs)) (more-codecs-p))
                   (tally-add codec (v-codec v))
                   (when (tally-due-p codec)
                     (incf index)
                     (record "codec" (nth index codecs)
                             (concatenate 'string (tally-reason codec "rows decode errors" "decodes") " in " (v-codec-name v)) at)
                     (setf codec (make-tally))))
                 (when (and (string= (hp-layout p) "tables") (not (tally-zero-p (v-layout v))))
                   (tally-add layout (v-layout v))
                   (when (tally-due-p layout)
                     (setf (hp-layout p) "lines")
                     (record "layout" "lines" (tally-reason layout "rows with wrong columns" "rows with the right columns") at)))
                 (unless (tally-zero-p (v-summary v))
                   (tally-add summary (v-summary v))
                   (cond ((and (tally-due-p summary) (< (hp-halvings p) +max-summary-halvings+))
                          (incf (hp-halvings p))
                          (record "summary" (scale-text (hp-halvings p))
                                  (tally-reason summary "queries for records the summary showed" "for records it omitted") at)
                          (setf summary (make-tally)))
                         ((and (plusp (hp-halvings p)) (>= (t-keep summary) +min-evidence+) (> (t-keep summary) (t-against summary)))
                          (decf (hp-halvings p))
                          (record "summary" (scale-text (hp-halvings p))
                                  (format nil "~D queries for records the summary omitted vs ~D for records it showed since the last switch (needs at least ~D and a majority)"
                                          (t-keep summary) (t-against summary) +min-evidence+) at)
                          (setf summary (make-tally))))))))
      (dolist (e events)
        (if (v2-compaction-p e)
            (progn (apply-window window (parse-rfc3339 (ev-string e "time"))) (setf window (make-votes)))
            (classify e window)))
      (setf (hp-codec p) (nth index codecs))
      p)))

(defun apply-profile-rules (p rules)
  "harness.ApplyRules: an active switch-codec rule moves the first codec to the next."
  (dolist (r rules p)
    (when (and (string= (rule-remedy r) +remedy-switch-codec+) (string= (hp-codec p) (first +codecs+)) (rest +codecs+))
      (setf (hp-codec p) (second +codecs+))
      (setf (hp-switches p)
            (append (hp-switches p)
                    (list (make-hswitch :setting "codec" :to (hp-codec p)
                                        :reason (format nil "rule ~A (~A)" (rule-id r) (rule-state r))
                                        :at (rule-created r))))))))

(defun declared-format-p (store model)
  "Whether the model's layer (its own or every model's, written or held) holds a format
definition, which would add a codec (declare.Active.CodecNames)."
  (loop for m in (remove-duplicates (list +go-all-models+ model) :test #'string=)
        thereis (let ((root (go-model-path store m)))
                  (or (om.golayer::rows-names (join-path root "format"))
                      (om.golayer::rows-names (join-path root "held" "format"))))))

(defun model-profile (store model)
  "harness.Ledger.Profile with the shipped codecs: the profile of model in store."
  (apply-profile-rules (compute-profile model (model-events store model)) (active-rules store model)))

(defun profile-refusal (store model)
  "What a compaction of model would apply that the image does not: a declared format. nil when
the profile is applied."
  (when (and store (string/= store "") model (string/= model "") (declared-format-p store model))
    "event/sessionBeforeCompact for a session model whose layer declares a format (declare.Active.CodecNames)"))

;;; The compaction's use (src/hooks/compaction.lisp reads these through om::*summary-profile*)

(defun om::profile-budget (budget)
  (if om::*summary-profile* (apply-summary om::*summary-profile* budget) budget))

(defun om::profile-format (memory-format)
  (if om::*summary-profile*
      (summary-format om::*summary-profile* memory-format)
      (if (string= memory-format "lines") "lines" "tables")))

(defun om::profile-codec (format)
  (if (and om::*summary-profile* (string/= format "lines") (string= (hp-codec om::*summary-profile*) "jsonl"))
      :jsonl
      :rows))

(defun om::profile-summary-codec (format)
  "details.summaryCodec: the codec's name when it is not the native rows codec."
  (and (eq (om::profile-codec format) :jsonl) "jsonl"))
