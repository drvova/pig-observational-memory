;;;; W5: memory/thin, the context event's day headers and the thinning of aged tool results
;;;; (hooks/thin_v2.go OnContext). Pure functions of a request's messages and the branch up to the
;;;; latest compaction; they edit the messages in place. Times are read in UTC (the image's
;;;; serialize.Location; docs/lisp-port-hooks.md).

(in-package "OM")

(defparameter +day-header-prefix+ "[Sent on ")
(defparameter +weekday-names+ #("Sunday" "Monday" "Tuesday" "Wednesday" "Thursday" "Friday" "Saturday"))

(defun day-header (timestamp)
  "thin.DayHeader: \"[Sent on Tuesday 2023-05-30]\", or nil when timestamp is unreadable."
  (let ((ms (parse-timestamp timestamp)))
    (when ms
      (let ((days (millis-day ms)))
        (cat +day-header-prefix+ (aref +weekday-names+ (weekday-of-days days)) " " (format-day days) "]")))))

(defun day-text-block (text) (jobject "type" "text" "text" text))

(defun day-headers (messages)
  "thin.DayHeaders over a list of message objects: returns how many it marked."
  (let ((marked 0) (previous ""))
    (dolist (m messages marked)
      (when (and (hash-table-p m) (equal (jget m "role") "user"))
        (let ((header (day-header (jget m "timestamp"))))
          (when (and header (string/= header previous))
            (setf previous header)
            (let ((content (jget m "content")))
              (cond ((stringp content)
                     (unless (has-prefix content +day-header-prefix+)
                       (setf (gethash "content" m) (vector (day-text-block header) (day-text-block content)))
                       (incf marked)))
                    ((and (vectorp content) (not (stringp content)))
                     (let ((first (and (plusp (length content)) (aref content 0))))
                       (unless (and (hash-table-p first) (equal (jget first "type") "text") (stringp (jget first "text"))
                                    (has-prefix (jget first "text") +day-header-prefix+))
                         (setf (gethash "content" m) (concatenate 'simple-vector (vector (day-text-block header)) content))
                         (incf marked))))))))))))

(defun role-p (e role) (and (string= (entry-type e) "message") (entry-message e) (equal (entry-role e) role)))

(defun tool-call-ids (e)
  (when (role-p e "assistant")
    (let ((blocks (jget (entry-message e) "content")))
      (when (and (vectorp blocks) (not (stringp blocks)))
        (loop for b across blocks
              when (and (hash-table-p b) (equal (jget b "type") "toolCall") (stringp (jget b "id")) (string/= (jget b "id") ""))
                collect (jget b "id"))))))

(defun thin-result-text (content)
  (cond ((stringp content) content)
        ((and (vectorp content) (not (stringp content)))
         (join (loop for b across content
                     when (and (hash-table-p b) (equal (jget b "type") "text") (stringp (jget b "text")))
                       collect (jget b "text"))
               (string #\Newline)))
        (t "")))

(defun call-line (cwd tool args result-text failed)
  "extract.CallLine: an attempt line for a call and its result."
  (let ((x (make-xstate :cwd cwd :entries (vector))))
    (multiple-value-bind (key display) (call-target x (make-tcall :name tool :args args))
      (declare (ignore key))
      (let ((m (regex-find-submatch +exit-code+ result-text)))
        (attempt-line tool display (if (or failed (and m (string/= (second m) "0"))) "failed" "worked") result-text)))))

(defun thinned-text (line entry-id)
  "thin.Text."
  (cat "[thinned] " line "; the full result: memory: om.Recall(" (raw-snippet-value entry-id) ")"))

(defun call-arguments (entries kept result call-id)
  (loop for i from (1- result) downto kept
        do (let ((e (aref entries i)))
             (when (role-p e "assistant")
               (let ((blocks (jget (entry-message e) "content")))
                 (when (and (vectorp blocks) (not (stringp blocks)))
                   (loop for b across blocks
                         do (when (and (hash-table-p b) (equal (jget b "type") "toolCall") (equal (jget b "id") call-id))
                              (let ((args (jget b "arguments")))
                                (return-from call-arguments (values (and (hash-table-p args) args) t))))))))))
  (values nil nil))

(defun thin-plan (entries skip-tools cwd)
  "thin.Compute: an alist (call-id . thinned text), oldest first; nil without a compaction."
  (let ((c (find-last-compaction-index entries)))
    (when (< c 0) (return-from thin-plan nil))
    (let ((kept (entry-index-for-id (subseq entries 0 c) (entry-first-kept-id (aref entries c)))))
      (when (< kept 0) (return-from thin-plan nil))
      (let ((by-call (make-hash-table :test 'equal)) (last-assistant -1) (newest (make-hash-table :test 'equal)) (out '()))
        (loop for i from kept below c
              do (let ((e (aref entries i)))
                   (when (role-p e "toolResult") (incf (gethash (str (jget (entry-message e) "toolCallId")) by-call 0)))))
        (loop for i from (1- c) downto kept
              do (when (role-p (aref entries i) "assistant") (setf last-assistant i) (return)))
        (loop for i from (1- c) downto kept
              do (let ((ids (tool-call-ids (aref entries i))))
                   (when ids (dolist (id ids) (setf (gethash id newest) t)) (return))))
        (loop for i from kept below last-assistant
              do (let ((e (aref entries i)))
                   (when (and (role-p e "toolResult") (string/= (entry-id e) ""))
                     (let* ((m (entry-message e)) (call-id (str (jget m "toolCallId"))) (name (str (jget m "toolName"))))
                       (unless (or (string= call-id "") (string= name "") (/= (gethash call-id by-call 0) 1)
                                   (gethash call-id newest) (member-str name skip-tools))
                         (multiple-value-bind (args found) (call-arguments entries kept i call-id)
                           (when found
                             (let ((text (thinned-text (call-line cwd name args (thin-result-text (jget m "content"))
                                                                  (eq (jget m "isError") :true))
                                                       (entry-id e))))
                               (when (< (estimate-tokens text) (estimate-entry-tokens e))
                                 (push (cons call-id text) out))))))))))
        (nreverse out)))))

(defun thin-apply (messages plan)
  "thin.Apply: shortens in place the tool results plan names; returns how many it changed."
  (if (null plan)
      0
      (let ((seen (make-hash-table :test 'equal)) (changed 0))
        (dolist (m messages)
          (when (and (hash-table-p m) (equal (jget m "role") "toolResult"))
            (incf (gethash (str (jget m "toolCallId")) seen 0))))
        (dolist (m messages changed)
          (when (and (hash-table-p m) (equal (jget m "role") "toolResult"))
            (let* ((id (str (jget m "toolCallId"))) (text (cdr (assoc id plan :test #'string=))))
              (when (and text (= (gethash id seen 0) 1))
                (setf (gethash "content" m) (vector (day-text-block text)))
                (incf changed))))))))

(defun on-context (messages branch skip-tools cwd &key passive)
  "hooks.OnContext: day headers, then the thinning planned from the branch through its latest
compaction. Returns the number of results thinned."
  (if (or passive (null messages))
      0
      (progn
        (day-headers messages)
        (let ((c (find-last-compaction-index branch)))
          (if (< c 0)
              0
              (thin-apply messages (thin-plan (subseq branch 0 (1+ c)) skip-tools cwd)))))))
