;;;; W5: the parts of memory/serialize and memory/tokens the hooks read (docs/lisp-port-hooks.md):
;;;; tokens.EstimateEntryTokens over tokens.EstimatePiMessage, serialize.textOnly and the user
;;;; text of serialize.HistoryText, and serialize.MinimalBrief, the summary of a cut range no
;;;; record fits.

(in-package "OM")

;;; tokens

(defparameter +estimated-image-chars+ 4800)

(defun json-chars (value)
  "jsonChars: the UTF-16 length of value's JSON without HTML escapes; null is 4."
  (if (or (null value) (eq value :null))
      4
      (utf16-len (json-string value :escape-html nil))))

(defun js-truthy-p (v)
  (cond ((or (null v) (eq v :null) (eq v :false)) nil)
        ((stringp v) (string/= v ""))
        ((realp v) (/= v 0))
        (t t)))

(defun content-chars (content)
  (cond ((stringp content) (utf16-len content))
        ((and (vectorp content) (not (stringp content)))
         (loop for raw across content
               sum (let ((type (and (hash-table-p raw) (jget raw "type"))))
                     (cond ((equal type "text") (let ((s (jget raw "text"))) (if (stringp s) (utf16-len s) 0)))
                           ((equal type "image") +estimated-image-chars+)
                           (t 0)))))
        (t 0)))

(defun string-chars (v) (if (stringp v) (utf16-len v) 0))

(defun estimate-pi-message (message)
  "tokens.EstimatePiMessage: a message's characters over four, rounded up."
  (let* ((role (jget message "role"))
         (chars
           (cond
             ((equal role "system")
              (+ (content-chars (jget message "content"))
                 (let ((sections (jget message "sections")))
                   (if (hash-table-p sections)
                       (loop for v being the hash-values of sections sum (string-chars v))
                       0))
                 (let ((added (jget message "toolsAdded"))) (if (js-truthy-p added) (json-chars added) 0))))
             ((member role '("user" "custom" "toolResult") :test #'equal) (content-chars (jget message "content")))
             ((equal role "assistant")
              (let ((blocks (jget message "content")))
                (if (and (vectorp blocks) (not (stringp blocks)))
                    (loop for b across blocks
                          sum (let ((type (and (hash-table-p b) (jget b "type"))))
                                (cond ((equal type "text") (string-chars (jget b "text")))
                                      ((equal type "thinking") (string-chars (jget b "thinking")))
                                      ((equal type "toolCall")
                                       (multiple-value-bind (args present) (gethash "arguments" b)
                                         (+ (string-chars (jget b "name")) (json-chars (and present args)))))
                                      (t 0))))
                    0)))
             ((equal role "bashExecution") (+ (string-chars (jget message "command")) (string-chars (jget message "output"))))
             ((member role '("branchSummary" "compactionSummary") :test #'equal) (string-chars (jget message "summary")))
             (t (return-from estimate-pi-message 0)))))
    (ceiling chars 4)))

(defun estimate-entry-tokens (e)
  "tokens.EstimateEntryTokens: one source entry's context footprint."
  (let ((type (entry-type e)))
    (cond ((string= type "message") (if (entry-message e) (estimate-pi-message (entry-message e)) 0))
          ((string= type "custom_message")
           (let ((c (entry-content e)))
             (cond ((stringp c) (if (string/= c "") (estimate-tokens c) 0))
                   ((and (vectorp c) (not (stringp c)))
                    (loop for b across c
                          sum (if (and (hash-table-p b) (equal (jget b "type") "text")
                                       (stringp (jget b "text")) (string/= (jget b "text") ""))
                                  (estimate-tokens (jget b "text"))
                                  0)))
                   (t 0))))
          ((string= type "branch_summary") (if (stringp (entry-summary e)) (estimate-tokens (entry-summary e)) 0))
          (t 0))))

;;; serialize

(defun text-only (content)
  "serialize.textOnly: a string as it is, else the text blocks joined by newlines."
  (cond ((stringp content) content)
        ((and (vectorp content) (not (stringp content)))
         (join (loop for b across content
                     when (and (hash-table-p b) (equal (jget b "type") "text") (stringp (jget b "text")))
                       collect (jget b "text"))
               (string #\Newline)))
        (t "")))

(defun history-user-text (e)
  "hooks.v2UserText: a user message's text as HistoryText reads it, or nil for any other entry
or an empty one."
  (when (and (string= (entry-type e) "message") (entry-message e) (equal (entry-role e) "user"))
    (let ((text (text-only (jget (entry-message e) "content"))))
      (and (string/= (trim-space text) "") text))))

;;; MinimalBrief

(defparameter +brief-heading+
  (cat "Brief of the compacted conversation: its opening request and one line per tool call. The memory tool searches the full text ("
       +snippet-history-search+ ")."))
(defparameter +brief-smallest+ "omitted.")
(defparameter +brief-key-args+ '("command" "path" "file_path" "filePath" "pattern" "query" "url" "name"))

(defun brief-clip (text n)
  "serialize.clipRunes: text on one line, cut to n runes with an ellipsis when longer."
  (let ((line (one-line text)))
    (cond ((<= (length line) n) line)
          ((<= n 1) "…")
          (t (cat (subseq line 0 (1- n)) "…")))))

(defun opening-request (entries)
  (dolist (e entries (values "" ""))
    (when (and (string= (entry-type e) "message") (entry-message e) (equal (entry-role e) "user"))
      (let ((text (trim-space (text-only (jget (entry-message e) "content")))))
        (when (string/= text "") (return (values (redact-secrets text) (entry-id e))))))))

(defun request-line (text id)
  (let ((line (cat "user: " (one-line text))))
    (if (string/= id "") (cat line " (" id ")") line)))

(defun key-arg (arguments)
  (when (hash-table-p arguments)
    (flet ((ok (v) (and (stringp v) (string/= (trim-space v) "") v)))
      (dolist (k +brief-key-args+)
        (let ((v (ok (gethash k arguments)))) (when v (return-from key-arg (redact-secrets (one-line v))))))
      (dolist (k (sort (loop for k being the hash-keys of arguments collect k) #'string<))
        (let ((v (ok (gethash k arguments)))) (when v (return-from key-arg (redact-secrets (one-line v))))))))
  "")

(defun tool-call-lines (entries skip-tools)
  (let ((out '()))
    (dolist (e entries (nreverse out))
      (when (and (string= (entry-type e) "message") (entry-message e) (equal (entry-role e) "assistant"))
        (let ((blocks (jget (entry-message e) "content")))
          (when (and (vectorp blocks) (not (stringp blocks)))
            (loop for b across blocks
                  do (when (and (hash-table-p b) (equal (jget b "type") "toolCall"))
                       (let ((name (str (jget b "name"))))
                         (unless (or (string= name "") (member-str name skip-tools))
                           (let ((line (cat "* " name)) (arg (key-arg (jget b "arguments"))))
                             (when (string/= arg "") (setf line (cat line " " (go-quote (brief-clip arg +brief-key-arg-runes+)))))
                             (when (string/= (entry-id e) "") (setf line (cat line " (" (entry-id e) ")")))
                             (push line out))))))))))))

(defun entry-count-text (n) (if (= n 1) "1 entry" (format nil "~D entries" n)))

(defun entry-date (e)
  (let ((ts (entry-timestamp e))) (if (>= (length ts) 10) (subseq ts 0 10) "")))

(defun omitted-pointer (entries)
  (if (null entries)
      "omitted: nothing; the compacted range held no entries."
      (let* ((first (first entries)) (last (car (last entries)))
             (p (cat "omitted: " (entry-count-text (length entries)))))
        (let ((from (entry-date first)) (to (entry-date last)))
          (when (and (string/= from "") (string/= to "")) (setf p (cat p " from " from " to " to))))
        (when (and (string/= (entry-id first) "") (string/= (entry-id last) ""))
          (setf p (cat p " (" (entry-id first) " to " (entry-id last) ")")))
        (cat p "; the memory tool searches them (" +snippet-history-search+ ")."))))

(defun short-pointer (entries)
  (if (null entries)
      "omitted: nothing."
      (let ((p (cat "omitted: " (entry-count-text (length entries))))
            (first (entry-id (first entries))) (last (entry-id (car (last entries)))))
        (when (and (string/= first "") (string/= last "")) (setf p (cat p " (" first " to " last ")")))
        (cat p "."))))

(defun count-pointer (entries)
  (if (null entries) "omitted: nothing." (cat "omitted: " (entry-count-text (length entries)) ".")))

(defun minimal-brief (entries skip-tools max-tokens ceiling-tokens)
  "serialize.MinimalBrief: (values text kind), kind \"brief\" or \"pointer\". entries is a list
of the cut range's source entries."
  (let ((limit ceiling-tokens))
    (when (and (> max-tokens 0) (or (<= limit 0) (< max-tokens limit))) (setf limit max-tokens))
    (flet ((fits (text) (or (<= limit 0) (<= (estimate-tokens text) limit))))
      (multiple-value-bind (opening opening-id) (opening-request entries)
        (when (> max-tokens 0)
          (let* ((lines (append (list +brief-heading+)
                                (and (string/= opening "") (list (request-line opening opening-id)))
                                (tool-call-lines entries skip-tools)))
                 (full (join lines (string #\Newline))))
            (when (fits full) (return-from minimal-brief (values full "brief")))))
        (let ((pointer (omitted-pointer entries)))
          (when (string/= opening "")
            (let ((clip (if (> max-tokens 0) (length (one-line opening)) +brief-key-arg-runes+)))
              (loop while (and (> clip 0)
                               (not (fits (cat (request-line (brief-clip opening clip) opening-id) (string #\Newline) pointer))))
                    do (setf clip (floor (* clip 3) 4)))
              (when (> clip 0)
                (return-from minimal-brief
                  (values (cat (request-line (brief-clip opening clip) opening-id) (string #\Newline) pointer) "pointer")))))
          (dolist (form (list pointer (short-pointer entries) (count-pointer entries)))
            (when (fits form) (return-from minimal-brief (values form "pointer"))))
          (values +brief-smallest+ "pointer"))))))
