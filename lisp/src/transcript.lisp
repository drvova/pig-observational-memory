;;;; memory/transcript: a harness's conversation file read as Pi-shaped entries. Ported: the
;;;; file reader (one JSON object per line, or one document over several lines), Claude Code's
;;;; format (claudecode.go) and the engine's own generic format (generic.go).

(in-package "OM.READERS")

(defun read-file-string (path)
  "The file's text, invalid UTF-8 read as U+FFFD; nil when it is missing."
  (with-open-file (in path :element-type '(unsigned-byte 8) :if-does-not-exist nil)
    (when in
      (let ((bytes (make-array (file-length in) :element-type '(unsigned-byte 8))))
        (read-sequence bytes in)
        (sb-ext:octets-to-string bytes :external-format '(:utf-8 :replacement #.(code-char #xfffd)))))))

(defun scanner-lines (text)
  "bufio.ScanLines: lines split at \\n, a trailing \\r dropped, no empty token after a final
newline."
  (let ((lines (split text (string #\Newline))))
    (when (and lines (string= (car (last lines)) ""))
      (setf lines (butlast lines)))
    (mapcar (lambda (l) (if (has-suffix l (string #\Return)) (subseq l 0 (1- (length l))) l)) lines)))

(defun try-parse-object (text)
  "json.Unmarshal into a map: (values object ok)."
  (handler-case (values (json-parse-object text) t)
    (json-error () (values nil nil))))

(defun read-transcript-lines (path)
  "readLines: the decoded objects of the file's lines; a file whose first line is not one JSON
object is read as one document."
  (let ((text (read-file-string path)))
    (when (null text) (return-from read-transcript-lines nil))
    (let ((out '()) (seen nil) (first-failed nil))
      (dolist (line (scanner-lines text))
        (when (and (not seen) (plusp (length (trim-space line))))
          (setf seen t)
          (setf first-failed (not (nth-value 1 (try-parse-object line)))))
        (multiple-value-bind (m ok) (try-parse-object line)
          (when (and ok m) (push m out))))
      (setf out (nreverse out))
      (if (not first-failed)
          out
          (multiple-value-bind (m ok) (try-parse-object text)
            (if (and ok m) (list m) out))))))

(defun text-block (s) (jobject "type" "text" "text" s))

(defun millis (timestamp)
  (parse-time-string timestamp :rfc3339-only t))

(defun make-message (id timestamp msg aliases)
  "message: a Pi message entry."
  (let ((ms (millis timestamp)))
    (when ms (setf (gethash "timestamp" msg) (coerce ms 'double-float))))
  (let ((raw (jobject "type" "message" "id" id "timestamp" timestamp "message" msg)))
    (when aliases (setf (gethash "omAliases" raw) (coerce aliases 'simple-vector)))
    (entry-from-map raw)))

(defun result-text (v)
  "resultText: a tool result's content as text."
  (cond ((stringp v) v)
        ((vectorp v)
         (let ((out ""))
           (loop for b across v
                 do (when (and (hash-table-p b) (string= (str (jget b "type")) "text"))
                      (unless (string= out "") (setf out (cat out (string #\Newline))))
                      (setf out (cat out (str (jget b "text"))))))
           out))
        ((hash-table-p v)
         (let ((s (str (jget v "output"))))
           (if (string/= s "") s (result-text (jget v "content")))))
        (t "")))

(defun content-blocks (v)
  "blocks: a content value as a block list; a string is one text block."
  (cond ((stringp v) (if (string= (trim-space v) "") nil (list (text-block v))))
        ((vectorp v) (coerce v 'list))
        (t nil)))

(defun jbool-p (v) (eq v :true))

(defun skip-claude-line-p (l)
  (or (some (lambda (flag) (jbool-p (jget l flag)))
            '("isSidechain" "isMeta" "isCompactSummary" "isVisibleInTranscriptOnly" "isApiErrorMessage"))
      (let ((type (str (jget l "type"))))
        (not (or (string= type "user") (string= type "assistant"))))))

(defun splice-preserved (chain by-id)
  "Each compaction's preserved segment (compactMetadata.preservedSegment), relinked into the
walked chain after its anchor."
  (let ((in (make-set)))
    (dolist (l chain) (setf (gethash (str (jget l "uuid")) in) t))
    (dolist (l chain chain)
      (let* ((seg (jget (jget l "compactMetadata") "preservedSegment"))
             (head (str (jget seg "headUuid")))
             (anchor (str (jget seg "anchorUuid")))
             (tail (str (jget seg "tailUuid"))))
        (unless (or (string= head "") (string= tail "") (gethash tail in))
          (let ((segment '()) (seen (make-set)))
            (loop with id = tail
                  while (and (string/= id "") (not (gethash id seen)))
                  do (setf (gethash id seen) t)
                     (let ((line (gethash id by-id)))
                       (when (null line) (setf segment nil) (return))
                       (push line segment)
                       (when (string= id head) (return)))
                     (setf id (str (jget (gethash id by-id) "parentUuid"))))
            (when (and segment (string= (str (jget (first segment) "uuid")) head))
              (let ((at (length chain)))
                (loop for c in chain for i from 0
                      do (when (string= (str (jget c "uuid")) anchor) (setf at (1+ i))))
                (dolist (s segment) (setf (gethash (str (jget s "uuid")) in) t))
                (return-from splice-preserved
                  (splice-preserved (append (subseq chain 0 at) segment (nthcdr at chain)) by-id))))))))))

(defun read-claude-code (lines)
  "ClaudeCode: the active branch walked back from the newest line, across compaction
boundaries, with the preserved segments spliced in."
  (let ((by-id (make-set)) (leaf ""))
    (dolist (l lines)
      (let ((id (str (jget l "uuid"))))
        (unless (string= id "")
          (setf (gethash id by-id) l)
          (when (and (not (jbool-p (jget l "isSidechain")))
                     (member (str (jget l "type")) '("user" "assistant") :test #'string=))
            (setf leaf id)))))
    (let ((chain '()) (seen (make-set)))
      (loop with id = leaf
            while (and (string/= id "") (not (gethash id seen)))
            do (setf (gethash id seen) t)
               (let ((l (gethash id by-id)))
                 (unless l (return))
                 (push l chain)
                 (let ((next (str (jget l "parentUuid"))))
                   (when (string= next "") (setf next (str (jget l "logicalParentUuid"))))
                   (setf id next))))
      ;; push built the chain oldest first already.
      (setf chain (splice-preserved chain by-id))
      (let ((tool-names (make-set)) (out '())
            (g-mid nil) (g-ts nil) (g-ids '()) (g-blocks '()) (group nil))
        (flet ((flush ()
                 (when group
                   (let ((ids (reverse g-ids)))
                     (push (make-message (first ids) g-ts
                                         (jobject "role" "assistant"
                                                  "content" (if g-blocks (coerce (reverse g-blocks) 'simple-vector) :null))
                                         (rest ids))
                           out))
                   (setf group nil g-ids '() g-blocks '()))))
          (dolist (l chain)
            (unless (skip-claude-line-p l)
              (let ((msg (jget l "message"))
                    (id (str (jget l "uuid")))
                    (ts (str (jget l "timestamp"))))
                (cond
                  ((string= (str (jget l "type")) "assistant")
                   (let ((mid (str (jget msg "id"))))
                     (when (or (not group) (string= mid "") (string/= g-mid mid))
                       (flush)
                       (setf group t g-mid mid g-ts ts)))
                   (push id g-ids)
                   (dolist (b (content-blocks (jget msg "content")))
                     (let ((type (str (jget b "type"))))
                       (cond ((string= type "text") (push (text-block (str (jget b "text"))) g-blocks))
                             ((string= type "thinking")
                              (push (jobject "type" "thinking" "thinking" (str (jget b "thinking"))) g-blocks))
                             ((string= type "tool_use")
                              (setf (gethash (str (jget b "id")) tool-names) (str (jget b "name")))
                              (push (jobject "type" "toolCall" "id" (str (jget b "id")) "name" (str (jget b "name"))
                                             "arguments" (jget b "input"))
                                    g-blocks))))))
                  (t
                   (flush)
                   (let ((content '()) (n 0))
                     (dolist (b (content-blocks (jget msg "content")))
                       (let ((type (str (jget b "type"))))
                         (cond
                           ((string= type "tool_result")
                            (let ((rid (if (> n 0) (format nil "~A#~D" id n) id))
                                  (call-id (str (jget b "tool_use_id"))))
                              (incf n)
                              (push (make-message rid ts
                                                  (jobject "role" "toolResult" "toolCallId" call-id
                                                           "toolName" (or (gethash call-id tool-names) "")
                                                           "content" (vector (text-block (result-text (jget b "content"))))
                                                           "isError" (jbool (jbool-p (jget b "is_error"))))
                                                  nil)
                                    out)))
                           ((string= type "text")
                            ;; A local command's output is the CLI's, not the user's.
                            (let ((tx (trim-space (str (jget b "text")))))
                              (unless (or (has-prefix tx "<local-command-stdout>") (has-prefix tx "<local-command-stderr>"))
                                (push (text-block (str (jget b "text"))) content))))
                           ((string= type "image") (push (jobject "type" "image") content)))))
                     (when (and content (= n 0))
                       (push (make-message id ts (jobject "role" "user" "content" (coerce (reverse content) 'simple-vector)) nil)
                             out))))))))
          (flush))
        (nreverse out)))))

(defun read-generic (lines)
  "Generic: the engine's interchange format, one object per line."
  (let ((out '()))
    (loop for l in lines for i from 0
          do (if (string= (str (jget l "type")) "message")
                 (push (entry-from-map l) out)
                 (let* ((id (let ((v (str (jget l "id")))) (if (string= v "") (format nil "g~6,'0D" i) v)))
                        (ts (str (jget l "timestamp")))
                        (body (str (jget l "text")))
                        (role (str (jget l "role"))))
                   (cond
                     ((string= role "user")
                      (push (make-message id ts (jobject "role" "user" "content" (vector (text-block body))) nil) out))
                     ((string= role "assistant")
                      (let ((content '()))
                        (unless (string= body "") (push (text-block body) content))
                        (dolist (tool (content-blocks (jget l "tools")))
                          (push (jobject "type" "toolCall" "id" (str (jget tool "id")) "name" (str (jget tool "name"))
                                         "arguments" (jget tool "arguments"))
                                content))
                        (push (make-message id ts (jobject "role" "assistant" "content" (coerce (reverse content) 'simple-vector)) nil)
                              out)))
                     ((string= role "tool")
                      (push (make-message id ts (jobject "role" "toolResult" "toolCallId" (str (jget l "toolCallId"))
                                                         "toolName" (str (jget l "toolName"))
                                                         "content" (vector (text-block body))
                                                         "isError" (jbool (jbool-p (jget l "isError"))))
                                          nil)
                            out))))))
    (nreverse out)))

(defun read-transcript (format path)
  (let ((lines (read-transcript-lines path)))
    (cond ((string= format "claude-code") (read-claude-code lines))
          ((string= format "generic") (read-generic lines))
          (t (error "transcript format ~A is not ported" format)))))
