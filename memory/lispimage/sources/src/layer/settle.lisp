;;;; The evidence rule, ported from memory/rules (proposal_v2.go SettleV2, settleCodeDetector,
;;;; pluralWord; v2.go isV2Compaction; grading.go eventTime) over the usage ledger's events
;;;; (<store>/models/<model>.jsonl, contracts/schemas/model-event.schema.json), decoded JSON.
;;;; One compaction is one unit of evidence, and only a compaction pass that records having run
;;;; with exactly the held text (ranHeld names its held id) after the hold counts.
;;;;
;;;; The image adds one kind, "adaptable" (a Lisp definition of an adaptable function), settled
;;;; by the rule's code branch for kinds whose evidence is the turns that ran under them (tokens
;;;; over later compactions), with the one judgement the design adds: a held definition that
;;;; failed its kernel check, errored or timed out on any of those compactions (the pass's
;;;; "adaptable-failed:<name>" count) is reverted, as settleCodeDetector reverts a detector one of
;;;; whose runs gave no count.

(in-package "OM.LAYER")

(defparameter +kind-adaptable+ "adaptable")

(defparameter +level1-losses+ '("level1-failed" "level1-specifics-refused" "level1-union-kept"))

(defun adaptable-failed-key (name) (concatenate 'string "adaptable-failed:" name))

(defun defined-count-key (name) (concatenate 'string "defined:" name))

(defun ev (event key) (om::jget event key))

(defun ev-string (event key) (let ((v (ev event key))) (if (stringp v) v "")))

(defun ev-int (event key)
  (let ((v (ev event key))) (if (realp v) (truncate v) 0)))

(defun ev-list (event key)
  (let ((v (ev event key))) (if (and (vectorp v) (not (stringp v))) (coerce v 'list) '())))

(defun ev-counts (event key)
  "A counts member as an alist (key . n) sorted by key."
  (let ((h (ev event key)) (out '()))
    (when (hash-table-p h)
      (maphash (lambda (k v) (when (realp v) (push (cons k (truncate v)) out))) h))
    (sort out #'string< :key #'car)))

(defun v2-compaction-p (e)
  "rules.isV2Compaction: any memory v2 compaction pass, empty ones included."
  (and (string= (ev-string e "kind") "pass") (string= (ev-string e "engine") "v2")
       (string= (ev-string e "stage") "compaction")))

(defun event-time (e) (parse-rfc3339 (ev-string e "time")))

(defun plural-word (n word)
  (cond ((= n 1) word)
        ((om::has-suffix word "s") (concatenate 'string word "es"))
        (t (concatenate 'string word "s"))))

(defun read-model-events (path)
  "The usage ledger's events at path, in order; lines that do not decode are skipped."
  (let ((text (read-file path)))
    (when text
      (loop for line in (om::split text (string #\Newline))
            for v = (and (string/= (om::trim-space line) "")
                         (handler-case (om::json-parse line) (error () nil)))
            when (hash-table-p v) collect v))))

(defun settle-v2 (events &key kind name held-id time (detector :other))
  "rules.SettleV2 over events for the held definition of kind named name, held at time (an
RFC 3339 time as parse-rfc3339 reads it) with held id held-id: (values decided clean reason).
detector says what a held detector parses as: :code (a Go detector, judged by its runs), :retired
(a query detector, never judged) or :other."
  (when (string= kind "detector")
    (case detector
      (:retired (return-from settle-v2 (values nil nil "")))
      (:code (return-from settle-v2 (settle-code-detector events name held-id time)))))
  (let ((tokens 0) (answered 0) (compactions 0) (counts (make-hash-table :test 'equal)))
    (dolist (e events)
      (when (and (v2-compaction-p e) (string/= (ev-string e "outcome") "empty")
                 (member held-id (ev-list e "ranHeld") :test #'equal))
        (let ((at (event-time e)))
          (when (and at (time-after-p at time))
            (incf compactions)
            (incf tokens (ev-int e "tokens"))
            (when (om::jtrue-p (ev e "level1")) (incf answered))
            (loop for (k . n) in (ev-counts e "counts")
                  when (plusp n) do (incf (gethash k counts 0) n))))))
    (flet ((losses (keys)
             (om::join (loop for k in keys
                             when (plusp (gethash k counts 0))
                               collect (format nil "~A ~D" k (gethash k counts)))
                       ", ")))
      (cond
        ((string= kind "instructions")
         (when (zerop answered) (return-from settle-v2 (values nil nil "")))
         (let ((lost (losses +level1-losses+)))
           (if (string/= lost "")
               (values t nil (format nil "level 1 ran under it at ~D later ~A and its code checks counted ~A"
                                     answered (plural-word answered "compaction") lost))
               (values t t (format nil "level 1 ran under it at ~D later ~A with no loss counted"
                                   answered (plural-word answered "compaction"))))))
        ((zerop tokens) (values nil nil ""))
        ((and (string= kind +kind-adaptable+) (plusp (gethash (adaptable-failed-key name) counts 0)))
         (let ((n (gethash (adaptable-failed-key name) counts)))
           (values t nil (format nil "it failed its kernel check, errored or timed out ~D ~A over ~D later ~A"
                                 n (if (= n 1) "time" "times") compactions (plural-word compactions "compaction")))))
        (t (values t t (format nil "the turns under it (~D source tokens over ~D later ~A) counted no failure it could cause"
                               tokens compactions (plural-word compactions "compaction"))))))))

(defun settle-code-detector (events name held-id time)
  "rules.settleCodeDetector."
  (let ((counted 0))
    (dolist (e events)
      (when (and (string= (ev-string e "kind") "detector-pass") (string= (ev-string e "engine") "v2")
                 (member held-id (ev-list e "ranHeld") :test #'equal))
        (let ((at (event-time e)))
          (when (and at (time-after-p at time))
            (let ((errors (ev e "errors")))
              (multiple-value-bind (why failed) (if (hash-table-p errors) (gethash name errors) (values nil nil))
                (when failed
                  (return-from settle-code-detector
                    (values t nil (format nil "its run after the compaction at ~A gave no count: ~A"
                                          (ev-string e "cut") (if (stringp why) why "")))))))
            (let ((counts (ev e "counts")))
              (when (and (hash-table-p counts) (nth-value 1 (gethash (defined-count-key name) counts)))
                (incf counted)))))))
    (if (zerop counted)
        (values nil nil "")
        (values t t (format nil "it counted at ~D later detector ~A with no error" counted (plural-word counted "pass"))))))
