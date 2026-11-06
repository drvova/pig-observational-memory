;;;; The SQLite-backed readers (memory/transcript sqlite.go, sqliterows.go): a session's rows read
;;;; through the sqlite3 command (-readonly -json) or Python's built-in sqlite3 module, as Go's
;;;; QuerySQLite does, so nothing links an SQLite library; then each harness's rows reader.

(in-package "OM")

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

(defparameter *sqlite-commands* (list "sqlite3" "python3")
  "SQLiteCommands: the programs tried, in order; a test points them at what it has.")

(defparameter +sqlite-python+
  (format nil "import json, sqlite3, sys, urllib.parse~%c = sqlite3.connect(\"file:\" + urllib.parse.quote(sys.argv[1]) + \"?mode=ro\", uri=True)~%c.row_factory = sqlite3.Row~%print(json.dumps([dict(r) for r in c.execute(sys.argv[2])]))"))

(define-condition no-sqlite (error) ()
  (:report "om reads this harness's SQLite sessions with the sqlite3 command or python3; install either"))

(defun executable-file-p (path)
  (and (probe-file path)
       (handler-case (and (not (sb-posix:s-isdir (sb-posix:stat-mode (sb-posix:stat path))))
                          (progn (sb-posix:access path sb-posix:x-ok) t))
         (sb-posix:syscall-error () nil))))

(defun look-path (name)
  "exec.LookPath: a name with a slash as it is, else the first executable on PATH."
  (if (find #\/ name)
      (and (executable-file-p name) name)
      (dolist (dir (split (or (sb-posix:getenv "PATH") "") ":") nil)
        (let ((p (cat (if (string= dir "") "." dir) "/" name)))
          (when (executable-file-p p) (return p))))))

(defun run-capture (bin args)
  "Runs bin with args: (values stdout stderr exit-code), both read as UTF-8."
  (let* ((err (make-string-output-stream)) (code nil)
         (out (with-output-to-string (o)
                (let ((p (sb-ext:run-program bin args :output o :error err :input nil
                                                      :external-format '(:utf-8 :replacement #.(code-char #xfffd)))))
                  (setf code (sb-ext:process-exit-code p))))))
    (values out (get-output-stream-string err) code)))

(defun query-sqlite (path query)
  "QuerySQLite: the rows of a read-only query on the database at path, as decoded objects; a
missing database has none."
  (unless (probe-file path) (return-from query-sqlite nil))
  (dolist (name *sqlite-commands* (error 'no-sqlite))
    (let ((bin (look-path name)))
      (when bin
        (multiple-value-bind (out err code)
            (if (search "python" name)
                (run-capture bin (list "-I" "-c" +sqlite-python+ path query))
                (run-capture bin (list "-readonly" "-json" path query)))
          (unless (eql code 0)
            (error "~A ~A: exit status ~A: ~A" name path code (trim-space err)))
          (when (string= (trim-space out) "") (return nil))
          (let ((rows (handler-case (json-parse (coerce out 'simple-string))
                        (json-error (e) (error "~A ~A: ~A" name path e)))))
            (unless (or (eq rows :null) (and (vectorp rows) (not (stringp rows))))
              (error "~A ~A: the rows are not a JSON array" name path))
            (return (if (eq rows :null) nil
                        (map 'list (lambda (r) (if (hash-table-p r) r nil)) rows)))))))))

(defun sql-quote (s)
  "SQLQuote: s as an SQL string literal."
  (cat "'" (replace-all s "'" "''") "'"))

;;; sqliterows.go

(defun read-crush-rows (rows)
  "CrushRows: Crush's messages rows."
  (let ((out '()))
    (dolist (r rows)
      (let ((id (str (jget r "id"))) (role (str (jget r "role"))) (ts (unix-seconds (jget r "created_at")))
            (summary (jget r "is_summary_message")))
        (unless (or (and (floatp summary) (/= summary 0)) (string= id ""))
          (let ((content '()) (n 0))
            (dolist (p (jlist (json-column (jget r "parts"))))
              (let ((data (jget p "data")) (type (str (jget p "type"))))
                (cond
                  ((string= type "text")
                   (unless (jbool-p (jget data "hidden")) (push (text-block (str (jget data "text"))) content)))
                  ((string= type "reasoning") (push (thinking-block (str (jget data "thinking"))) content))
                  ((string= type "tool_call")
                   (let ((args (str (jget data "input"))))
                     (multiple-value-bind (parsed ok) (try-json-any args)
                       (when ok (setf args parsed)))
                     (push (tool-call-block (str (jget data "id")) (str (jget data "name")) args) content)))
                  ((string= type "tool_result")
                   (let ((rid (numbered-id id n)))
                     (incf n)
                     (push (tool-result-message rid ts (str (jget data "tool_call_id")) (str (jget data "name"))
                                                (str (jget data "content")) (jbool-p (jget data "is_error")))
                           out))))))
            (when (and (member-str role '("user" "assistant")) content)
              (push (role-message id ts role (reverse content)) out))))))
    (nreverse out)))

(defun read-goose-rows (rows)
  "GooseRows: Goose's messages rows."
  (let ((out '()))
    (dolist (r rows)
      (let* ((id (str (jget r "message_id"))) (role (str (jget r "role")))
             (meta (let ((m (json-column (jget r "metadata_json")))) (and (hash-table-p m) m))))
        (unless (or (jbool-p (jget meta "compactionSummary")) (string= id "")
                    ;; Turn context Goose injects as a user message the user never sees.
                    (and (eq (jget meta "userVisible") :false) (string= role "user")))
          (let ((ts (unix-seconds (jget r "created_timestamp"))) (content '()) (n 0))
            (dolist (b (jlist (json-column (jget r "content_json"))))
              (let ((type (str (jget b "type"))))
                (cond
                  ((string= type "text") (push (text-block (str (jget b "text"))) content))
                  ((string= type "thinking") (push (thinking-block (str (jget b "thinking"))) content))
                  ((string= type "toolRequest")
                   (let ((value (jget (jget b "toolCall") "value")))
                     (push (tool-call-block (str (jget b "id")) (str (jget value "name")) (jget value "arguments")) content)))
                  ((string= type "toolResponse")
                   (let* ((res (jget b "toolResult")) (value (jget res "value")) (error-p nil) (body ""))
                     (cond ((hash-table-p value)
                            (setf error-p (jbool-p (jget value "isError")) body (result-text (jget value "content"))))
                           ((and (vectorp value) (not (stringp value)))
                            (setf body (result-text value))))
                     (when (string= (str (jget res "status")) "error")
                       (setf error-p t body (go-sprint (jget res "error"))))
                     (let ((rid (numbered-id id n)))
                       (incf n)
                       (push (make-message rid ts (jobject "role" "toolResult" "toolCallId" (str (jget b "id"))
                                                           "content" (vector (text-block body)) "isError" (jbool error-p))
                                           nil)
                             out)))))))
            (when (and (member-str role '("user" "assistant")) content (not (and (string= role "user") (> n 0))))
              (push (role-message id ts role (reverse content)) out))))))
    (nreverse out)))

(defun read-amazon-q-rows (rows)
  "AmazonQRows: Amazon Q Developer CLI's conversation row."
  (when rows
    (let* ((state (let ((s (json-column (jget (first rows) "value")))) (and (hash-table-p s) s)))
           (out '()))
      (loop for h in (jlist (jget state "history")) for i from 0
            do (let* ((user (jget h "user")) (assistant (jget h "assistant")) (body nil))
                 (dolist (k '("Response" "ToolUse"))
                   (when (hash-table-p (jget assistant k)) (setf body (jget assistant k))))
                 (let ((mid (str (jget body "message_id")))
                       (ts (str (jget user "timestamp")))
                       (content (jget user "content")))
                   (when (string= mid "") (setf mid (format nil "~A-~D" (str (jget state "conversation_id")) i)))
                   (let ((p (jget content "Prompt")))
                     (when (hash-table-p p)
                       (push (role-message (cat mid ":u") ts "user" (list (text-block (str (jget p "prompt"))))) out)))
                   (let ((tr (jget content "ToolUseResults")))
                     (when (hash-table-p tr)
                       (loop for r in (jlist (jget tr "tool_use_results")) for j from 0
                             do (let ((parts '()))
                                  (dolist (c (content-blocks (jget r "content")))
                                    (cond ((stringp (jget c "Text")) (push (jget c "Text") parts))
                                          ((jpresent-p c "Json") (push (json-string (let ((v (jget c "Json"))) (if (null v) :null v))) parts))))
                                  (push (make-message (format nil "~A:r~D" mid j) ts
                                                      (jobject "role" "toolResult" "toolCallId" (str (jget r "tool_use_id"))
                                                               "content" (vector (text-block (join-lines (nreverse parts))))
                                                               "isError" (jbool (string= (str (jget r "status")) "Error")))
                                                      nil)
                                        out)))))
                   (when body
                     (let ((ac '()))
                       (let ((tx (str (jget body "content"))))
                         (unless (string= tx "") (push (text-block tx) ac)))
                       (dolist (u (jlist (jget body "tool_uses")))
                         (push (tool-call-block (str (jget u "id")) (str (jget u "name")) (jget u "args")) ac))
                       (when ac (push (role-message mid ts "assistant" (reverse ac)) out)))))))
      (nreverse out))))

;;; DBFormats: the query for one session's rows, in order, and their reader.

(defparameter *db-formats*
  (list (list "crush"
              (lambda (s) (cat "SELECT id, role, parts, created_at, is_summary_message FROM messages WHERE session_id = " (sql-quote s) " ORDER BY created_at, rowid"))
              'read-crush-rows)
        (list "goose"
              (lambda (s) (cat "SELECT message_id, role, content_json, created_timestamp, metadata_json FROM messages WHERE session_id = " (sql-quote s) " ORDER BY created_timestamp, id"))
              'read-goose-rows)
        (list "amazon-q"
              (lambda (s) (cat "SELECT key, value FROM conversations WHERE json_extract(value, '$.conversation_id') = " (sql-quote s)))
              'read-amazon-q-rows)))

(defun db-format (name)
  (or (assoc name *db-formats* :test #'string=) (error "transcript: unknown database format ~A" name)))

(defun db-rows-reader (name) (third (db-format name)))

(defun db-entries (format path session)
  "DB.Entries: the session's rows of the database at path, read in the named format."
  (destructuring-bind (query reader) (rest (db-format format))
    (funcall reader (query-sqlite path (funcall query session)))))
