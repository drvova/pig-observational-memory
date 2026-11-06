;;;; memory/ledger held.go: the records a preference that weakens security holds (HeldRule,
;;;; HeldReflection, WithoutHeld; docs/brain-memory.md, decision 19). A held record is kept and
;;;; listed in /om:status, never rendered in a summary.

(in-package "OM.KERNEL")

(defparameter +unverified-notice+
  "Remembered items are user-reported or extracted context, unverified against the current repository and conversation; existing code and the user's current words take precedence."
  "ledger.UnverifiedNotice: remembered items framed as unverified wherever memory is shown to the
model (the summary's instructions, the curated index's header, the memory tool's guidelines;
docs/brain-memory.md, decision 18).")

(defun held-rule (o)
  "ledger.HeldRule: the insecure rule a preference or correction record (or a level-1 merge of
the user's own) matches, nil for any other record."
  (when (or (member (obs-kind o) '("preference" "correction") :test #'string=) (obs-from-user o))
    (insecure-match (obs-content o))))

(defun held-reflection (r by-id)
  "ledger.HeldReflection: the rule of the first held record r cites that by-id holds, or nil."
  (dolist (id (refl-supporting r) nil)
    (let ((o (and by-id (gethash id by-id))))
      (when (obs-p o)
        (let ((rule (held-rule o)))
          (when rule (return rule)))))))

(defun without-held (observations reflections by-id)
  "ledger.WithoutHeld: (values observations reflections) without the held ones, in order."
  (values (remove-if #'held-rule observations)
          (remove-if (lambda (r) (held-reflection r by-id)) reflections)))
