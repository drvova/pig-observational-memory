;;;; Where each harness keeps a session's transcript (the *Path functions of memory/transcript):
;;;; the hooks hand some harnesses' payloads no transcript path, so om finds it as the harness
;;;; names it, from the session id, the working directory and the harness's environment.

(in-package "OM")

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix)
  (require :sb-md5))

(defun getenv-string (name) (or (sb-posix:getenv name) ""))

(defun user-home ()
  "os.UserHomeDir on Unix: $HOME, nil when it is unset or empty."
  (let ((h (getenv-string "HOME"))) (if (string= h "") nil h)))

(defun join-path (&rest parts)
  "filepath.Join."
  (let ((nonempty (remove "" parts :test #'string=)))
    (if nonempty (path-clean (join nonempty "/")) "")))

(defun env-or-home (var &rest under-home)
  "$var, else the home directory joined with under-home; nil without either."
  (let ((v (getenv-string var)))
    (cond ((string/= v "") v)
          ((user-home) (apply #'join-path (user-home) under-home))
          (t nil))))

(defun kode-session-path (cwd session-id)
  "KodeSessionPath: ${KODE_CONFIG_DIR:-${ANYKODE_CONFIG_DIR:-~/.kode}}/projects/<slug>/<session>.jsonl,
the slug being cwd with every byte that is not an ASCII letter or digit as -."
  (if (or (string= cwd "") (string= session-id ""))
      ""
      (let ((root (let ((k (getenv-string "KODE_CONFIG_DIR")))
                    (if (string/= k "") k (env-or-home "ANYKODE_CONFIG_DIR" ".kode")))))
        (if (null root)
            ""
            (let ((slug (with-output-to-string (out)
                          (loop for b across (sb-ext:string-to-octets cwd :external-format :utf-8)
                                do (write-char (if (or (<= 97 b 122) (<= 65 b 90) (<= 48 b 57)) (code-char b) #\-) out)))))
              (join-path root "projects" slug (cat session-id ".jsonl")))))))

(defun copilot-events-path (session-id)
  "CopilotEventsPath: ${COPILOT_HOME:-~/.copilot}/session-state/<session>/events.jsonl."
  (let ((home (and (string/= session-id "") (env-or-home "COPILOT_HOME" ".copilot"))))
    (if home (join-path home "session-state" session-id "events.jsonl") "")))

(defun kimi-wire-path (cwd session-id)
  "KimiWirePath: ${KIMI_SHARE_DIR:-~/.kimi}/sessions/<md5(cwd)>/<session>/wire.jsonl."
  (let ((root (let ((r (getenv-string "KIMI_SHARE_DIR")))
                (if (string/= r "") r (join-path (or (user-home) "") ".kimi")))))
    (join-path root "sessions"
               (format nil "~(~{~2,'0x~}~)" (coerce (sb-md5:md5sum-string cwd :external-format :utf-8) 'list))
               session-id "wire.jsonl")))

(defun subdirectories (dir)
  "The names of dir's subdirectories, sorted (filepath.Glob's order)."
  (sort (mapcar (lambda (p) (car (last (pathname-directory p))))
                (directory (merge-pathnames (make-pathname :directory '(:relative :wild)) (cat dir "/"))
                           :resolve-symlinks nil))
        #'string<))

(defun kiro-session-path (cwd session-id)
  "KiroSessionPath: the V3 sess_<id> (or <id>) directory's messages.jsonl under any workspace
bucket of ${KIRO_HOME:-~/.kiro}/sessions, else the V2 cli/<id>.jsonl, else \"\"."
  (declare (ignore cwd))
  (let ((root (and (string/= session-id "") (env-or-home "KIRO_HOME" ".kiro"))))
    (if (null root)
        ""
        (let ((sessions (join-path root "sessions")))
          (or (dolist (dir (list (cat "sess_" session-id) session-id) nil)
                (dolist (bucket (subdirectories sessions))
                  (let ((p (join-path sessions bucket dir "messages.jsonl")))
                    (when (probe-file p) (return-from kiro-session-path p)))))
              (let ((p (join-path sessions "cli" (cat session-id ".jsonl"))))
                (if (probe-file p) p "")))))))

(defun cline-messages-path (cwd session-id)
  "ClineMessagesPath: ${CLINE_DATA_DIR:-~/.cline/data}/sessions/<id>/<id>.messages.json."
  (declare (ignore cwd))
  (let ((dir (and (string/= session-id "") (env-or-home "CLINE_DATA_DIR" ".cline" "data"))))
    (if dir (join-path dir "sessions" session-id (cat session-id ".messages.json")) "")))

(defun cortex-history-path (cwd session-id)
  "CortexHistoryPath: ${SNOWFLAKE_HOME:-~/.snowflake}/cortex/conversations/<session>.history.jsonl."
  (declare (ignore cwd))
  (let ((root (and (string/= session-id "") (env-or-home "SNOWFLAKE_HOME" ".snowflake"))))
    (if root (join-path root "cortex" "conversations" (cat session-id ".history.jsonl")) "")))

(defun auggie-session-path (cwd conversation-id)
  "AuggieSessionPath: ~/.augment/sessions/<conversation>.json."
  (declare (ignore cwd))
  (if (or (null (user-home)) (string= conversation-id ""))
      ""
      (join-path (user-home) ".augment" "sessions" (cat conversation-id ".json"))))

(defun directory-p (path)
  (handler-case (sb-posix:s-isdir (sb-posix:stat-mode (sb-posix:stat path)))
    (sb-posix:syscall-error () nil)))

(defun crush-db-path (cwd session-id)
  "CrushDBPath: the nearest .crush directory from cwd up to the repository root, else
<cwd>/.crush, holding crush.db."
  (declare (ignore session-id))
  (if (string= cwd "")
      ""
      (loop with dir = cwd
            do (when (directory-p (join-path dir ".crush"))
                 (return (join-path dir ".crush" "crush.db")))
               (when (or (probe-file (join-path dir ".git")) (string= (directory-namestring-of dir) dir))
                 (return (join-path cwd ".crush" "crush.db")))
               (setf dir (directory-namestring-of dir)))))

(defun goose-db-path (cwd session-id)
  "GooseDBPath: ${GOOSE_PATH_ROOT}/data/sessions/sessions.db, else
~/.local/share/goose/sessions/sessions.db on Linux, else \"\"."
  (declare (ignore cwd session-id))
  (let ((root (getenv-string "GOOSE_PATH_ROOT")))
    (cond ((string/= root "") (join-path root "data" "sessions" "sessions.db"))
          ((or (null (user-home)) (not (member :linux *features*))) "")
          (t (join-path (user-home) ".local" "share" "goose" "sessions" "sessions.db")))))

(defun amazon-q-db-path (cwd session-id)
  "AmazonQDBPath: the platform data directory's amazon-q/data.sqlite3."
  (declare (ignore cwd session-id))
  (cond ((null (user-home)) "")
        ((member :darwin *features*) (join-path (user-home) "Library" "Application Support" "amazon-q" "data.sqlite3"))
        ((string/= (getenv-string "XDG_DATA_HOME") "") (join-path (getenv-string "XDG_DATA_HOME") "amazon-q" "data.sqlite3"))
        (t (join-path (user-home) ".local" "share" "amazon-q" "data.sqlite3"))))

(defun amazon-q-conversation (cwd)
  "AmazonQConversation: the conversation Amazon Q keeps for directory cwd, \"\" when none."
  (if (string= cwd "")
      ""
      (let ((rows (handler-case (query-sqlite (amazon-q-db-path "" "") (cat "SELECT value FROM conversations WHERE key = " (sql-quote cwd)))
                    (error () nil))))
        (if (null rows)
            ""
            (str (jget (json-column (jget (first rows) "value")) "conversation_id"))))))
