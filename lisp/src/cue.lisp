;;;; Cues (memory/extract/cue.go; docs/brain-memory.md, decision 4; docs/memory-v2.md, "Cues"):
;;;; one short hint plus an om snippet after a tool call, on an exact structural match of the call
;;;; (its shell command, its path, or a line of its output equal to a recorded error line) against a
;;;; standing record's keys. It abstains by default, cues a record at most once per compaction and at
;;;; most a cap per compaction, with delivery state the om.cue entries after the latest compaction.
;;;; Byte for byte what Go's extract.CueFor decides (contracts/cue.jsonl).

(in-package "OM.ENGINE")

(defconstant +cue-cap+ 3)
(defconstant +cue-text-runes+ 120)

(defun cue-hint (o)
  (let ((kind (obs-kind o)))
    (cond ((string= kind "attempt") "this failed before")
          ((string= kind "preference") "a standing rule names this")
          ((string= kind "correction") "a correction names this")
          ((string= kind "decision") "a decision names this")
          ((string= kind "convention") "a repository convention names this")
          (t ""))))

(defun cueable-p (o)
  (and (string/= (cue-hint o) "") (null (held-rule o))
       (or (string/= (obs-kind o) "attempt") (string= (obs-outcome o) "failed"))))

(defun cue-keys (tool args output cwd)
  "cueKeys: (kind value) pairs, the command else the path (absolute and relative), then the
output's distinct non-blank lines."
  (declare (ignore tool))
  (let ((out '()) (x (make-xstate :cwd cwd)) (command (shell-command args)))
    (cond ((and command (string/= (trim-space command) ""))
           (push-end (list "commands" (trim-space command)) out))
          (t (let ((p (dolist (arg +path-args+ nil)
                        (let ((v (arg-string args arg)))
                          (when (and v (string/= (trim-space v) "")) (return v))))))
               (when p
                 (push-end (list "paths" (path-key x p)) out)
                 (let ((rel (path-display x p)))
                   (unless (string= rel (path-key x p)) (push-end (list "paths" rel) out)))))))
    (let ((seen (make-hash-table :test #'equal)))
      (dolist (line (split output (string #\Newline)))
        (let ((line (clip (trim-space line) +brief-key-arg-runes+)))
          (unless (or (string= line "") (gethash line seen))
            (setf (gethash line seen) t)
            (push-end (list "errors" line) out)))))
    out))

(defun cue-key-matches-p (kind recorded value)
  (or (string= recorded value)
      (and (string= kind "paths") (> (length recorded) 0) (char= (char recorded (1- (length recorded))) #\/)
           (> (length value) (length recorded)) (has-prefix value recorded))))

(defun cue-text (o)
  "CueText."
  (cat "memory cue (unverified): " (cue-hint o) ": " (clip (redact-secrets (obs-content o)) +cue-text-runes+)
       " Read it: om.Recall(`" (obs-id o) "`)"))

(defun cue-for (entries tool args output cwd limit)
  "extract.CueFor: (values record key text) of the cue, or nil when it abstains."
  (when (<= limit 0) (return-from cue-for nil))
  (let ((from (1+ (find-last-compaction-index entries))) (given (make-hash-table :test #'equal)) (n 0))
    (loop for i from from below (length entries)
          for e = (aref entries i)
          when (and (string= (entry-type e) "custom") (string= (entry-custom-type e) "om.cue"))
            do (incf n)
               (let ((id (and (hash-table-p (entry-data e)) (jget (entry-data e) "record"))))
                 (when (and (stringp id) (string/= id "")) (setf (gethash id given) t))))
    (when (>= n limit) (return-from cue-for nil))
    (let ((keys (cue-keys tool args output cwd)))
      (when (null keys) (return-from cue-for nil))
      (let* ((folded (fold-ledger entries)) (active (folded-active folded)) (labels nil))
        (dolist (o (reverse active) nil)
          (unless (or (gethash (obs-id o) given) (not (cueable-p o)))
            (let ((key (cue-matched-key o keys)))
              (when key
                (unless labels (setf labels (folded-labels folded)))
                (let ((label (gethash (obs-id o) labels)))
                  (cond ((member label '("superseded" "retracted" "held") :test #'equal))
                        ((equal label "contested")
                         (let ((sides (cons o (remove-if-not (lambda (side) (cue-standing-p (gethash (obs-id side) labels)))
                                                             (contest-sides active (obs-id o))))))
                           ;; A side cued with another one this compaction was cued with it.
                           (unless (some (lambda (side) (gethash (obs-id side) given)) (rest sides))
                             (let ((text (cue-contest-text sides labels)))
                               (return-from cue-for (if text (values (obs-id o) key text) nil))))))
                        (t (return-from cue-for (values (obs-id o) key (cue-text o))))))))))))))

(defun cue-standing-p (label)
  "standing: a label a contest's side in a cue may carry."
  (not (member label '("superseded" "retracted" "held") :test #'equal)))

(defun cue-matched-key (o keys)
  "matchedKey: the first of keys one of o's keys holds, as kind:value, or nil."
  (dolist (k keys nil)
    (when (some (lambda (r) (cue-key-matches-p (first k) r (second k))) (keys-get (obs-keys o) (first k)))
      (return (cat (first k) ":" (second k))))))

(defun cue-contest-text (sides labels)
  "CueContestText: every side in contest-before-p order, each with its tag, its whole redacted
text and its snippet; nil when fewer than two or their texts pass +cue-text-runes+."
  (when (< (length sides) 2) (return-from cue-contest-text nil))
  (let ((runes 0) (parts '()))
    (dolist (o (stable-sort (copy-list sides) #'contest-before-p))
      (let ((content (redact-secrets (obs-content o))))
        (incf runes (length content))
        (push-end (cat (contest-tag (gethash (obs-id o) labels "") (obs-authority o)) " " content
                       " Read it: om.Recall(`" (obs-id o) "`)")
                  parts)))
    (when (<= runes +cue-text-runes+)
      (cat "memory cue (unverified): a contested record names this, neither side settled: " (join parts "; ")))))

(defun cue-compaction (entries)
  "CueCompaction."
  (let ((i (find-last-compaction-index entries)))
    (if (>= i 0) (entry-id (aref entries i)) "")))
