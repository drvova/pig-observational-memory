;;;; Compaction on PiG and the remaining PiG events (docs/lisp-port-peer.md): the trigger and
;;;; its backoff, session_before_compact (refusals before any write, level 0 on PiG's cut, the
;;;; cancellation crossing to a host call in flight), session start over a curated store, the
;;;; system-prompt section and its pin, and /om:view, over a scripted host.

(in-package "OM")

(defstruct (phost (:constructor %make-phost))
  "A scripted PiG host: its branch, what the image called and appended, the notifications, and
the answers for the trigger's calls."
  (branch #()) (calls '()) (appended '()) (notices '())
  (usage nil) (compact-error nil) (model :null) (home "") (trusted :false))

(defun make-phost (&rest args)
  (apply #'%make-phost args))

(defun phost-answer (h method params)
  (push method (phost-calls h))
  (cond ((string= method "host/getBranch")
         (let ((from (round (jget params "from"))) (branch (phost-branch h)))
           (if (and (plusp from) (<= from (length branch))
                    (equal (jget params "anchorId") (jget (aref branch (1- from)) "id")))
               (jobject "from" from "entries" (subseq branch from))
               (jobject "from" 0 "entries" branch))))
        ((string= method "host/appendEntry")
         (push (cons (jget params "customType") (jget params "data")) (phost-appended h))
         (setf (phost-branch h)
               (concatenate 'simple-vector (phost-branch h)
                            (vector (jobject "type" "custom" "id" (format nil "x~D" (length (phost-appended h)))
                                             "customType" (jget params "customType") "data" (jget params "data")))))
         (jobject))
        ((string= method "host/notify")
         (push (cons (jget params "level") (jget params "message")) (phost-notices h))
         (jobject))
        ((string= method "host/projectTrusted") (jobject "value" (phost-trusted h)))
        ((string= method "host/configHome") (jobject "value" (phost-home h)))
        ((string= method "host/sessionModel") (jobject "model" (phost-model h)))
        ((string= method "host/sessionId") (jobject "value" "s1"))
        ((string= method "host/isIdle") (jobject "value" :true))
        ((string= method "host/getContextUsage") (jobject "usage" (or (phost-usage h) :null)))
        ((string= method "host/compact")
         (if (phost-compact-error h) (list :error -32000 (phost-compact-error h)) (jobject)))
        (t (list :error -32601 (cat "method not found: " method)))))

(defun phost-fn (h) (lambda (method params) (phost-answer h method params)))

(defun phost-serve-until (stream h done-p)
  "Answers the image's host calls (the trigger's deferred step) until done-p holds."
  (loop until (funcall done-p)
        do (let ((m (peer-read stream)))
             (when (null m) (return))
             (when (and (stringp (jget m "method")) (nth-value 1 (gethash "id" m)))
               (let ((r (phost-answer h (jget m "method") (jget m "params"))))
                 (peer-send-line stream
                                 (if (and (consp r) (eq (car r) :error))
                                     (jobject "jsonrpc" "2.0" "id" (jget m "id") "error" (jobject "code" (second r) "message" (third r)))
                                     (jobject "jsonrpc" "2.0" "id" (jget m "id") "result" r))))))))

(defun notice-with-p (h text)
  (some (lambda (n) (search text (cdr n))) (phost-notices h)))

(defun write-file* (path text)
  (ensure-directories-exist path)
  (with-open-file (out path :direction :output :if-exists :supersede :external-format :utf-8)
    (write-string text out)))

(defmacro with-pig ((stream dir) &body body)
  "A started image on a fresh directory and an initialized engine-rpc connection to it, its
agent directory dir (settings.json there)."
  (let ((socket (gensym)) (sock (gensym)))
    `(with-server-dir (,dir)
       (let ((,socket (format nil "~Aengine.sock" ,dir)))
         (om.server:start-server :root ,dir :socket ,socket :idle-seconds 0)
         (multiple-value-bind (,stream ,sock) (test-connect ,socket)
           (unwind-protect
                (progn (peer-init ,stream "agentDir" ,dir "configDirName" ".pig")
                       ,@body)
             (close ,stream)
             (sb-bsd-sockets:socket-close ,sock)))))))

(defun pig-scope (dir) (jobject "id" "1" "cwd" dir "hasUI" :true))

(defun pig-turn (prefix request)
  "A user request, an assistant reply that reports usage, and a final reply."
  (vector (pi-message (cat prefix "u") "" "user" request)
          (pi-message (cat prefix "a") (cat prefix "u") "assistant" (cat "Working on: " request))))

(deftest pig-trigger-and-backoff
  (let ((h (make-phost :branch (pig-turn "p" "Deploy on port 8080.")
                       :model (jobject "provider" "p" "id" "m" "contextWindow" 1000)
                       :usage (jobject "tokens" 950 "contextWindow" 1000)
                       :compact-error "Nothing to compact (session too small)"))
        (id 10))
    (with-pig (stream dir)
      (write-file* (format nil "~Asettings.json" dir)
                   "{\"compaction\": {\"enabled\": false, \"reserveTokens\": 100}, \"observational-memory\": {\"consolidate\": false}}")
      (let ((scope (pig-scope dir)))
        (flet ((settle () (peer-exchange stream (incf id) "event/agentSettled" (jobject "scope" scope) :host (phost-fn h))))
          ;; Over window - reserveTokens (950 > 900): the trigger asks PiG to compact, and PiG's
          ;; "Nothing to compact" suspends it at this tip.
          (check (json-string (jget (settle) "result")) "{}" "agent settled answers")
          (phost-serve-until stream h (lambda () (notice-with-p h "Nothing to compact")))
          (check (count "host/compact" (phost-calls h) :test #'string=) 1 "one compaction asked for")
          (check (and (notice-with-p h "context ~950 tokens is over PiG's threshold 900") t) t "the trigger's notice")
          (check (car (first (phost-notices h))) "error" "the refusal is an error notice")
          ;; The same tip: suspended, so the usage is not even read.
          (setf (phost-calls h) '())
          (settle)
          (check (member "host/getContextUsage" (phost-calls h) :test #'string=) nil "suspended at the same tip")
          (check (member "host/compact" (phost-calls h) :test #'string=) nil "no compaction while suspended")
          ;; A new tip lifts the suspension; this time PiG compacts.
          (setf (phost-branch h) (concatenate 'simple-vector (phost-branch h) (pig-turn "q" "And the saver.")))
          (setf (phost-compact-error h) nil (phost-notices h) '() (phost-calls h) '())
          (settle)
          (phost-serve-until stream h (lambda () (notice-with-p h "compaction complete")))
          (check (count "host/compact" (phost-calls h) :test #'string=) 1 "the new tip compacts")
          ;; Unknown usage never triggers.
          (setf (phost-usage h) (jobject "tokens" :null "contextWindow" 1000) (phost-calls h) '() (phost-notices h) '())
          (settle)
          (check (member "host/compact" (phost-calls h) :test #'string=) nil "unknown usage")
          (check (phost-notices h) '() "nothing said")
          ;; Under the threshold, nothing.
          (setf (phost-usage h) (jobject "tokens" 800 "contextWindow" 1000) (phost-calls h) '())
          (settle)
          (check (member "host/compact" (phost-calls h) :test #'string=) nil "under the threshold")
          ;; A model override of reserveTokens moves the threshold below 800.
          (write-file* (format nil "~Asettings.json" dir)
                       "{\"compaction\": {\"enabled\": false, \"reserveTokens\": 100, \"modelOverrides\": {\"p/m\": {\"reserveTokens\": 300}}}}")
          (setf (phost-calls h) '() (phost-notices h) '())
          (settle)
          (phost-serve-until stream h (lambda () (notice-with-p h "compaction complete")))
          (check (and (notice-with-p h "threshold 700") t) t "the model's override")
          ;; With PiG's auto-compaction on (re-read at every check), never.
          (write-file* (format nil "~Asettings.json" dir) "{\"compaction\": {\"enabled\": true, \"reserveTokens\": 100}}")
          (setf (phost-usage h) (jobject "tokens" 950 "contextWindow" 1000) (phost-calls h) '())
          (settle)
          (check (member "host/compact" (phost-calls h) :test #'string=) nil "PiG compacts on its own"))))))

(defun before-compact-data (branch first-kept &key (reason "threshold"))
  (jobject "preparation" (jobject "firstKeptEntryId" first-kept "tokensBefore" 3000
                                  "settings" (jobject "enabled" :true "reserveTokens" 16384 "keepRecentTokens" 20000))
           "branchEntries" branch "reason" reason))

(deftest pig-before-compact
  (let* ((branch (concatenate 'simple-vector (pig-turn "p" "Deploy on port 8080 and cache in /var/cache/app.")
                              (pig-turn "q" "Now the saver.")))
         (h (make-phost :branch branch)))
    (with-pig (stream dir)
      (write-file* (format nil "~Asettings.json" dir) "{\"observational-memory\": {\"consolidate\": false}}")
      (let ((scope (pig-scope dir)))
        ;; No session model, level 1 off: level 0 over the cut range, then the compaction.
        (let* ((r (jget (peer-exchange stream 1 "event/sessionBeforeCompact"
                                       (jobject "scope" scope "data" (before-compact-data branch "qu")) :host (phost-fn h))
                        "result"))
               (compaction (jget (jget r "result") "compaction")))
          (check (jget compaction "firstKeptEntryId") "qu" "PiG's cut exactly")
          (check (jget compaction "tokensBefore") 3000d0)
          (check (jget compaction "summary") "omitted: 2 entries (pu to pa)." "the range is too small for records: the pointer")
          (check (and (search "8080" (json-string (jget (jget compaction "details") "observations"))) t) t "the details carry the record")
          (check (jget (jget (jget compaction "details") "consolidation") "reason") "consolidate is false")
          (check (jget r "thinning") :true)
          (check (mapcar #'car (reverse (phost-appended h))) '("om.observations.recorded" "om.extracted") "level 0 recorded the cut range"))
        ;; Level 1 on with a session model the host has no auth for: the image runs it, and
        ;; skips the request with Go's reason (docs/lisp-port-level1.md).
        (write-file* (format nil "~Asettings.json" dir) "{}")
        (peer-exchange stream 2 "event/projectTrust" (jobject "scope" scope) :host (phost-fn h))
        (setf (phost-model h) (jobject "provider" "p" "id" "m") (phost-appended h) '())
        (let ((r (peer-exchange stream 3 "event/sessionBeforeCompact"
                                (jobject "scope" scope "data" (before-compact-data branch "qu")) :host (phost-fn h))))
          (check (jget (jget (jget (jget (jget (jget r "result") "result") "compaction") "details") "consolidation") "reason")
                 "the session model is unavailable: no API key or auth headers for provider \"p\"" "level 1 runs on the image")
          (check (and (not (member "models/stream" (phost-calls h) :test #'equal)) t) t "no request"))
        ;; An overflow compaction runs level 0 only, so it is the image's.
        (let ((r (peer-exchange stream 4 "event/sessionBeforeCompact"
                                (jobject "scope" scope "data" (before-compact-data branch "qu" :reason "overflow")) :host (phost-fn h))))
          (check (jget (jget (jget (jget (jget (jget r "result") "result") "compaction") "details") "consolidation") "reason")
                 "an overflow compaction runs level 0 only, so recovery is not delayed"))
        ;; A session model with a curated store: the harness records the compaction as the
        ;; model's evidence unit (docs/lisp-port-evidence.md).
        (setf (phost-home h) dir (phost-appended h) '())
        (let* ((r (peer-exchange stream 5 "event/sessionBeforeCompact"
                                 (jobject "scope" scope "data" (before-compact-data branch "qu" :reason "overflow")) :host (phost-fn h)))
               (ledger (format nil "~A/observational-memory/memories/models/p_2fm.jsonl" (string-right-trim "/" dir))))
          (check (and (jget (jget r "result") "result") t) t "the harness runs at the compaction")
          (check (om.layer::ev-string (car (last (om.layer::read-model-events ledger))) "kind") "pass" "the pass event")
          ;; The model's profile is applied (docs/lisp-port-status.md): a decode vote changes
          ;; nothing yet, and the compaction is the image's.
          (om.layer::append-line ledger "{\"time\":\"2026-11-02T09:00:00Z\",\"model\":\"p/m\",\"kind\":\"decode\",\"outcome\":\"error\",\"codec\":\"rows\",\"engine\":\"v2\"}")
          (let ((r (peer-exchange stream 6 "event/sessionBeforeCompact"
                                  (jobject "scope" scope "data" (before-compact-data branch "qu" :reason "overflow")) :host (phost-fn h))))
            (check (and (jget (jget r "result") "result") t) t "the profile is applied"))
          ;; A layer that declares a format is still refused before any write.
          (write-file* (format nil "~A/observational-memory/memories/harness/p_2fm/format/mine.rows" (string-right-trim "/" dir)) "x")
          (setf (phost-appended h) '())
          (let ((r (peer-exchange stream 7 "event/sessionBeforeCompact"
                                  (jobject "scope" scope "data" (before-compact-data branch "qu" :reason "overflow")) :host (phost-fn h))))
            (check (jget (jget r "error") "code") -32004d0 "a declared format is not ported")
            (check (phost-appended h) '() "nothing appended")))))))

(deftest pig-before-compact-cancel
  ;; PiG cancels the compaction while the image waits on host/getBranch: the image passes the
  ;; cancel on for that call, goes on waiting for its answer, and answers no result.
  (let* ((branch (pig-turn "p" "Deploy on port 8080."))
         (h (make-phost :branch branch)) (cancel-for nil) (branch-call nil))
    (with-pig (stream dir)
      (write-file* (format nil "~Asettings.json" dir) "{\"observational-memory\": {\"consolidate\": false}}")
      (peer-send-line stream (jobject "jsonrpc" "2.0" "id" 7 "method" "event/sessionBeforeCompact"
                                      "params" (jobject "scope" (pig-scope dir) "data" (before-compact-data branch "pa"))))
      (let ((answer
              (loop for m = (peer-read stream)
                    do (cond ((null m) (return nil))
                             ((equal (jget m "method") "$/cancelRequest") (setf cancel-for (jget (jget m "params") "id")))
                             ((and (equal (jget m "method") "host/getBranch") (null branch-call))
                              ;; Hold this call: cancel the compaction instead.
                              (setf branch-call (jget m "id"))
                              (peer-send-line stream (jobject "jsonrpc" "2.0" "method" "$/cancelRequest" "params" (jobject "id" 7)))
                              ;; The image passes the cancel on, then still waits for the answer.
                              (let ((c (peer-read stream)))
                                (check (jget c "method") "$/cancelRequest" "the cancel crosses to the host call")
                                (setf cancel-for (jget (jget c "params") "id")))
                              (peer-send-line stream (jobject "jsonrpc" "2.0" "id" branch-call
                                                              "error" (jobject "code" -32800 "message" "context canceled"))))
                             ((stringp (jget m "method"))
                              (let ((r (phost-answer h (jget m "method") (jget m "params"))))
                                (peer-send-line stream (if (consp r)
                                                           (jobject "jsonrpc" "2.0" "id" (jget m "id") "error" (jobject "code" (second r) "message" (third r)))
                                                           (jobject "jsonrpc" "2.0" "id" (jget m "id") "result" r)))))
                             ((equal (jget m "id") 7d0) (return m))))))
        (check (and branch-call t) t "the compaction read the branch")
        (check cancel-for branch-call "the cancel names the host call in flight")
        (check (jget (jget answer "result") "result") :null "a cancelled compaction answers no result")
        (check (multiple-value-list (gethash "error" answer)) '(nil nil) "not an error")))))

(deftest pig-session-and-section
  (let ((h (make-phost :branch (pig-turn "p" "Deploy on port 8080."))))
    (with-pig (stream dir)
      (let* ((scope (pig-scope dir))
             (store (format nil "~Aobservational-memory/memories/" dir)))
        (setf (phost-home h) (string-right-trim "/" dir))
        (ensure-directories-exist store)
        (om.layer::save-memory (string-right-trim "/" store)
                               (om.layer::make-memory :name "tea" :type "user" :description "Drinks green tea") "s0")
        (check (json-string (jget (peer-exchange stream 1 "event/sessionStart" (jobject "scope" scope) :host (phost-fn h)) "result"))
               "{}" "a session start over a fresh store")
        (let ((text (jget (jget (jget (peer-exchange stream 2 "event/beforeAgentStart" (jobject "scope" scope) :host (phost-fn h)) "result")
                                "sections")
                          "curated-memory")))
          (check (has-prefix text om.server::+tool-note-line+) t "the tool-note line first")
          (check (and (search "tea" text) t) t "the index lists the memory"))
        ;; The pin holds the index for the conversation, until a compaction resets it.
        (om.layer::save-memory (string-right-trim "/" store)
                               (om.layer::make-memory :name "port" :type "project" :description "Deploys on 8080") "s0")
        (flet ((section (id)
                 (jget (jget (jget (peer-exchange stream id "event/beforeAgentStart" (jobject "scope" scope) :host (phost-fn h)) "result")
                             "sections")
                       "curated-memory")))
          (check (search "Deploys on 8080" (section 3)) nil "pinned")
          (peer-exchange stream 4 "event/sessionCompact" (jobject "scope" scope) :host (phost-fn h))
          (check (and (search "Deploys on 8080" (section 5)) t) t "rendered again after a compaction"))
        ;; A store with a file the layer never recorded, and a legacy definition the kernel
        ;; refuses, starts on the image: the untracked file is left alone, the definition is
        ;; renamed .refused (curated.Store.migrateDefinitions), and later events run.
        (write-file* (format nil "~Aharness/_2a/instructions/observer.rows" store) "x")
        (check (json-string (jget (peer-exchange stream 6 "event/sessionStart" (jobject "scope" scope) :host (phost-fn h)) "result"))
               "{}" "an untracked instructions file")
        (write-file* (format nil "~Adefinitions/old.rows" store) "x")
        (check (json-string (jget (peer-exchange stream 7 "event/sessionStart" (jobject "scope" scope) :host (phost-fn h)) "result"))
               "{}" "a legacy definition")
        (check (and (probe-file (format nil "~Adefinitions/old.rows.refused" store)) t) t "refused by the kernel check")
        (check (jget (jget (peer-exchange stream 8 "event/turnEnd" (jobject "scope" scope) :host (phost-fn h)) "result") "thinning")
               :true "a turn end after it")
        (check (json-string (jget (peer-exchange stream 9 "event/agentStart" (jobject "scope" scope) :host (phost-fn h)) "result"))
               "{\"thinning\":true}" "agent start")
        (check (json-string (jget (peer-exchange stream 10 "event/sessionShutdown" (jobject "scope" scope) :host (phost-fn h)) "result"))
               "{}" "session shutdown")))))

(deftest pig-view-command
  (let ((h (make-phost :branch (pig-turn "p" "Deploy on port 8080."))))
    (with-pig (stream dir)
      (let ((scope (pig-scope dir)))
        (check (json-string (jget (peer-exchange stream 1 "command/run" (jobject "scope" scope "name" "om:view" "args" "") :host (phost-fn h)) "result"))
               "{}" "the view answers")
        (check (and (search "No visible observations." (cdr (first (phost-notices h)))) t) t "nothing recorded: empty sections")
        (setf (phost-notices h) '())
        (peer-exchange stream 2 "command/run" (jobject "scope" scope "name" "om:view" "args" "bogus") :host (phost-fn h))
        (check (phost-notices h) '(("info" . "Usage: /om:view [full]")) "the usage line")
        (setf (phost-notices h) '())
        (check (json-string (jget (peer-exchange stream 3 "command/run" (jobject "scope" scope "name" "om:status") :host (phost-fn h)) "result"))
               "{}" "status runs on the image")
        (check (and (search "── Memory ──" (cdr (first (phost-notices h)))) t) t "the status report")))))
