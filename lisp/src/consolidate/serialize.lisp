;;;; Level 1's view of the cut range: memory/serialize's SerializeConversation,
;;;; SerializeBranchEntries and SerializeSourceAddressedBranchEntries (serialize.go), the
;;;; source-addressed blocks the level-1 input shows of the compacted conversation. Timestamps
;;;; are rendered in UTC, as every other Lisp rendering is (docs/lisp-port-hooks.md, "Time zone").

(in-package "OM")

(defparameter +non-text+ "[non-text content omitted]")

(defparameter +source-omission-marker+
  (format nil "~%~%[… middle omitted: source exceeds observer input budget; original source remains in the session ledger …]~%~%"))

(defun utf16-tail (s n)
  "jsfmt.Tail: the last n UTF-16 units; a pair cut in half leaves U+FFFD."
  (let ((units 0) (chars '()))
    (loop for i from (1- (length s)) downto 0
          for c = (char s i)
          for w = (if (>= (char-code c) #x10000) 2 1)
          do (cond ((<= (+ units w) n) (push c chars) (incf units w))
                   ((< units n) (push (code-char #xfffd) chars) (return))
                   (t (return))))
    (coerce chars 'string)))

(defun json-array-list (v) (if (and (vectorp v) (not (stringp v))) (coerce v 'list) nil))

(defun text-and-placeholders (content)
  "textAndPlaceholders with no thinking: text, tool calls as [name(args)], else the placeholder."
  (cond ((stringp content) content)
        ((not (and (vectorp content) (not (stringp content)))) +non-text+)
        (t (join (loop for block across content
                       for part = (cond ((not (hash-table-p block)) +non-text+)
                                        ((equal (jget block "type") "text")
                                         (if (stringp (jget block "text")) (jget block "text") +non-text+))
                                        ((equal (jget block "type") "thinking") nil)
                                        ((and (equal (jget block "type") "toolCall") (stringp (jget block "name")))
                                         (cat "[" (jget block "name") "(" (json-stringify (jget block "arguments")) ")]"))
                                        (t +non-text+))
                       when part collect part)
                 (string #\Newline)))))

(defun non-empty-lines (text)
  (join (remove "" (split text (string #\Newline)) :test #'string=) (string #\Newline)))

(defun without-tool-calls (content tools)
  "withoutToolCalls: content minus its toolCall blocks naming one of tools."
  (if (or (not (and (vectorp content) (not (stringp content)))) (null tools))
      content
      (coerce (loop for b across content
                    unless (and (hash-table-p b) (equal (jget b "type") "toolCall")
                                (stringp (jget b "name")) (string/= (jget b "name") "")
                                (member-str (jget b "name") tools))
                      collect b)
              'simple-vector)))

(defun skipped-tool-result-p (msg tools)
  (and (equal (jget msg "role") "toolResult")
       (stringp (jget msg "toolName")) (string/= (jget msg "toolName") "")
       (member-str (jget msg "toolName") tools) t))

(defun bash-execution-text (msg)
  "BashExecutionText."
  (let* ((command (if (stringp (jget msg "command")) (jget msg "command") ""))
         (output (if (stringp (jget msg "output")) (jget msg "output") ""))
         (text (cat "$ " command)) (notes '()) (code (jget msg "exitCode")))
    (when (string/= (trim-space output) "") (setf text (cat text (string #\Newline) output)))
    (when (and (realp code) (/= code 0)) (push-end (format nil "exit code ~D" (truncate code)) notes))
    (when (eq (jget msg "cancelled") :true) (push-end "cancelled" notes))
    (when (eq (jget msg "truncated") :true) (push-end "output truncated" notes))
    (if notes (cat text (string #\Newline) "(" (join notes "; ") ")") text)))

(defun js-string (v)
  (cond ((or (null v) (eq v :null)) "undefined")
        ((stringp v) v)
        (t (json-stringify v))))

(defun serialize-message (msg skip-tools)
  "SerializeConversation for one message, or \"\"."
  (let ((ts (format-timestamp (jget msg "timestamp"))) (role (jget msg "role")))
    (cond ((equal role "user") (cat "[User @ " ts "]: " (text-only (jget msg "content"))))
          ((equal role "assistant")
           (let ((body (non-empty-lines (text-and-placeholders (without-tool-calls (jget msg "content") skip-tools)))))
             (if (string= body "") "" (cat "[Assistant @ " ts "]: " body))))
          ((equal role "bashExecution")
           (if (eq (jget msg "excludeFromContext") :true) "" (cat "[Bash @ " ts "]: " (bash-execution-text msg))))
          ((skipped-tool-result-p msg skip-tools) "")
          (t (cat "[Tool result for " (js-string (jget msg "toolName")) " @ " ts "]: " (text-only (jget msg "content")))))))

(defun entry-timestamp-value (e) (if (string= (entry-timestamp e) "") nil (entry-timestamp e)))

(defun serialize-branch-entry (e skip-tools)
  "SerializeBranchEntries for one entry."
  (let ((type (entry-type e)))
    (cond ((string= type "message")
           (if (hash-table-p (entry-message e)) (serialize-message (entry-message e) skip-tools) ""))
          ((string= type "custom_message")
           (let* ((c (entry-content e))
                  (text (cond ((stringp c) c)
                              ((and (vectorp c) (not (stringp c))) (text-only c))
                              (t ""))))
             (cat "[" (if (string= (entry-custom-type e) "") "Custom" (cat "Custom (" (entry-custom-type e) ")"))
                  " @ " (format-timestamp (entry-timestamp-value e)) "]: " text)))
          ((and (string= type "branch_summary") (stringp (entry-summary e)))
           (cat "[Branch summary @ " (format-timestamp (entry-timestamp-value e)) "]: " (entry-summary e)))
          (t ""))))

(defun source-renderable-p (e) (member-str (entry-type e) '("message" "custom_message" "branch_summary")))

(defun observer-renders-p (e)
  (and (string/= (entry-id e) "") (source-renderable-p e)
       (string/= (trim-space (serialize-branch-entry e '())) "")))

(defun truncate-source-block (label rendered max-tokens)
  "truncateSourceBlockToTokenBudget: (values text ok)."
  (let ((required (cat label (string #\Newline) +source-omission-marker+)))
    (when (> (estimate-tokens required) max-tokens) (return-from truncate-source-block (values "" nil)))
    (let ((full (cat label (string #\Newline) rendered)))
      (when (<= (estimate-tokens full) max-tokens) (return-from truncate-source-block (values full t)))
      (let* ((max-chars (max 1 (* max-tokens 4)))
             (retained (- max-chars (utf16-len required)))
             (head (ceiling retained 2))
             (tail (- retained head)))
        (values (cat label (string #\Newline) (utf16-head rendered head) +source-omission-marker+
                     (if (> tail 0) (utf16-tail rendered tail) ""))
                t)))))

(defun serialize-source-addressed (entries &key (max-tokens 0) skip-tools)
  "SerializeSourceAddressedBranchEntries: (values text entry-ids covers-up-to-id)."
  (let ((blocks '()) (entry-ids '()) (skipped '()) (estimated 0))
    (dolist (e entries)
      (when (and (string/= (entry-id e) "") (source-renderable-p e))
        (let ((rendered (serialize-branch-entry e skip-tools)))
          (if (string= (trim-space rendered) "")
              (when (and skip-tools (observer-renders-p e)) (push-end (entry-id e) skipped))
              (let* ((label (cat "[Source entry id: " (entry-id e) "]"))
                     (block (cat label (string #\Newline) rendered))
                     (cost (estimate-tokens (cat (if blocks (format nil "~%~%") "") block))))
                (when (and (> max-tokens 0) (> (+ estimated cost) max-tokens))
                  (when blocks (return))
                  (multiple-value-bind (excerpt ok) (truncate-source-block label rendered max-tokens)
                    (when ok
                      (push-end excerpt blocks)
                      (setf entry-ids (append entry-ids skipped (list (entry-id e))) skipped '()))
                    (return)))
                (push-end block blocks)
                (setf entry-ids (append entry-ids skipped (list (entry-id e))) skipped '())
                (incf estimated cost))))))
    (let ((covers ""))
      (when blocks
        (setf entry-ids (append entry-ids skipped) covers (car (last entry-ids))))
      (values (join blocks (format nil "~%~%")) entry-ids covers))))
