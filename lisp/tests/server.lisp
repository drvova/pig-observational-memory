;;;; om.server: JSON-RPC framing, the dispatch table, sessions, the image lock, the socket
;;;; round trip, idle exit and the turn-end store (docs/lisp-port-image-client.md).

(in-package "OM")

(setf om.server::*quiet* t)

(defun server-test-dir ()
  (let ((dir (format nil "/tmp/om-srv-~D-~D/" (sb-posix:getpid) (random 1000000))))
    (ensure-directories-exist dir)
    dir))

(defmacro with-server-dir ((var) &body body)
  `(let ((,var (server-test-dir)))
     (unwind-protect (progn ,@body)
       (om.server:stop-server)
       (sb-ext:delete-directory ,var :recursive t))))

(defun rpc-line (line)
  "handle-line's answer as JSON text, nil for none."
  (let ((answer (om.server:handle-line line)))
    (and answer (json-string answer))))

(defun test-connect (path)
  (let ((s (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
    (sb-bsd-sockets:socket-connect s path)
    (values (sb-bsd-sockets:socket-make-stream s :input t :output t :element-type 'character
                                                 :external-format :utf-8 :buffering :full)
            s)))

(defun write-lines (path lines)
  (with-open-file (out path :direction :output :if-exists :supersede :external-format :utf-8)
    (dolist (l lines) (write-string l out) (terpri out))))

(deftest server-framing
  (check (rpc-line "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}")
         "{\"id\":1,\"jsonrpc\":\"2.0\",\"result\":{}}" "ping")
  (check (rpc-line "{\"jsonrpc\":\"2.0\",\"id\":\"a\",\"method\":\"nope\"}")
         "{\"error\":{\"code\":-32601,\"message\":\"method not found: nope\"},\"id\":\"a\",\"jsonrpc\":\"2.0\"}"
         "unknown method")
  (check (rpc-line "{not json") "{\"error\":{\"code\":-32700,\"message\":\"parse error\"},\"id\":null,\"jsonrpc\":\"2.0\"}"
         "parse error")
  (check (rpc-line "{\"id\":2,\"method\":\"ping\"}")
         "{\"error\":{\"code\":-32600,\"message\":\"invalid request\"},\"id\":2,\"jsonrpc\":\"2.0\"}" "no jsonrpc")
  (check (rpc-line "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"ping\",\"params\":[1]}")
         "{\"error\":{\"code\":-32602,\"message\":\"params must be an object\"},\"id\":3,\"jsonrpc\":\"2.0\"}" "array params")
  (check (rpc-line "{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}") nil "a notification is not answered")
  (let ((answer (om.server:handle-line "{\"jsonrpc\":\"2.0\",\"id\":4,\"method\":\"event/agentSettled\",\"params\":{}}")))
    (check (jget (jget answer "error") "code") -32004 "event/agentSettled is not ported")
    (check (has-prefix (jget (jget answer "error") "message") "om engine: event/agentSettled is not ported") t))
  (check (jget (jget (om.server:handle-line "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"event/agentSettled\"}") "error") "code")
         -32004 "engine-rpc methods are listed")
  (check (jget (jget (om.server:handle-line "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"event/turnEnd\"}") "error") "message")
         "not initialized" "a ported engine-rpc event needs initialize")
  ;; The table is one place others extend.
  (om.server:define-method "test/echo" (params) (jobject "got" (jget params "x")))
  (check (rpc-line "{\"jsonrpc\":\"2.0\",\"id\":6,\"method\":\"test/echo\",\"params\":{\"x\":\"<y>\"}}")
         "{\"id\":6,\"jsonrpc\":\"2.0\",\"result\":{\"got\":\"\\u003cy\\u003e\"}}" "define-method")
  (om.server:define-method "test/fail" (params) (error "boom"))
  (check (rpc-line "{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"test/fail\"}")
         "{\"error\":{\"code\":-32603,\"message\":\"internal error: boom\"},\"id\":7,\"jsonrpc\":\"2.0\"}" "a handler's error")
  (remhash "test/echo" om.server:*methods*)
  (remhash "test/fail" om.server:*methods*))

(deftest server-sessions
  (check (om.server::safe-name ".a/b\\c") "a_b_c")
  (check (om.server::store-path "/r/" "mine" "s/1") "/r/sessions/mine/s_1.jsonl")
  (let ((om.server::*root* "/r") (om.server::*sessions* (make-hash-table :test 'equal)))
    (let ((a (om.server::find-session "mine" "s1")))
      (check (eq a (om.server::find-session "mine" "s1")) t "one state per harness and id")
      (check (eq a (om.server::find-session "other" "s1")) nil "the harness is part of the key")
      (check (om.server::session-store-path a) "/r/sessions/mine/s1.jsonl")
      (check (om.server::session-count) 2))))

(deftest server-branch-merge
  ;; neutral.Branch: stored entries after the one they name, in written order; one naming an
  ;; entry the branch lacks at the end; an alias lands after the entry that folded it.
  (let* ((transcript (list (entry-from-map (jobject "type" "message" "id" "a"))
                           (entry-from-map (jobject "type" "message" "id" "b" "omAliases" (vector "b2")))
                           (entry-from-map (jobject "type" "message" "id" "c"))))
         (stored (list (make-stored "a" (jobject "type" "custom" "id" "x1"))
                       (make-stored "x1" (jobject "type" "custom" "id" "x2"))
                       (make-stored "a" (jobject "type" "custom" "id" "x3"))
                       (make-stored "gone" (jobject "type" "custom" "id" "x4"))
                       (make-stored "b2" (jobject "type" "custom" "id" "x5"))
                       (make-stored "" (jobject "type" "custom" "id" "x0"))))
         (branch (neutral-branch transcript stored)))
    (check (map 'list #'entry-id branch) '("x0" "a" "x1" "x2" "x3" "b" "x5" "c" "x4"))
    (check (map 'list #'entry-parent-id branch) '("" "x0" "a" "x1" "x2" "x3" "b" "x5" "c"))))

(deftest server-lock
  (with-server-dir (dir)
    (let ((socket (format nil "~Aengine.sock" dir)))
      (om.server:start-server :root dir :socket socket :idle-seconds 0)
      (check (and (probe-file (format nil "~Aengine.pid" dir)) t) t "pid file")
      (check (om.server::lock-file (format nil "~Aengine.lock" dir) :wait nil) nil "the lock is held")
      (let ((om.server::*server* nil))
        (check (handler-case (progn (om.server:start-server :root dir :socket socket) :started)
                 (om.server:image-locked () :locked))
               :locked "a second image refuses to start"))
      (om.server:stop-server)
      (check (probe-file socket) nil "the socket is removed")
      (check (probe-file (format nil "~Aengine.pid" dir)) nil "the pid file is removed")
      (let ((fd (om.server::lock-file (format nil "~Aengine.lock" dir) :wait nil)))
        (check (and fd t) t "the lock is released")
        (when fd (om.server::unlock-file fd)))
      ;; A socket file a crashed image left is replaced.
      (write-lines socket '("stale"))
      (om.server:start-server :root dir :socket socket :idle-seconds 0)
      (multiple-value-bind (stream sock) (test-connect socket)
        (write-line "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}" stream)
        (finish-output stream)
        (check (read-line stream nil) "{\"id\":1,\"jsonrpc\":\"2.0\",\"result\":{}}" "serves after a stale socket")
        (close stream)
        (sb-bsd-sockets:socket-close sock)))))

(deftest server-socket-round-trip
  (with-server-dir (dir)
    (let ((socket (format nil "~Aengine.sock" dir)))
      (om.server:start-server :root dir :socket socket :idle-seconds 0)
      (multiple-value-bind (stream sock) (test-connect socket)
        ;; Two requests in one write, a blank line, a notification and a bad line: one answer per
        ;; request line, in order.
        (write-string (format nil "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":1}}~%{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}~%~%{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}~%oops~%{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"status\"}~%") stream)
        (finish-output stream)
        (let ((init (json-parse (read-line stream))))
          (check (jget init "id") 1d0)
          (check (jget (jget init "result") "engine") "lisp")
          (check (jget (jget init "result") "root") (string-right-trim "/" dir)))
        (check (read-line stream) "{\"id\":2,\"jsonrpc\":\"2.0\",\"result\":{}}")
        (check (jget (jget (json-parse (read-line stream)) "error") "code") -32700d0 "bad line")
        (let ((status (jget (json-parse (read-line stream)) "result")))
          (check (jget status "connections") 1d0))
        ;; A message with non-ASCII text and an escaped newline stays one line.
        (write-line "{\"jsonrpc\":\"2.0\",\"id\":\"é\\n\",\"method\":\"ping\"}" stream)
        (finish-output stream)
        (check (read-line stream) "{\"id\":\"é\\n\",\"jsonrpc\":\"2.0\",\"result\":{}}")
        ;; shutdown ends this connection's state only; image/stop answers, then the image stops.
        (write-line "{\"jsonrpc\":\"2.0\",\"id\":8,\"method\":\"shutdown\"}" stream)
        (finish-output stream)
        (check (read-line stream) "{\"id\":8,\"jsonrpc\":\"2.0\",\"result\":{}}")
        (check (and (probe-file socket) t) t "shutdown does not stop the image")
        (write-line "{\"jsonrpc\":\"2.0\",\"id\":9,\"method\":\"image/stop\"}" stream)
        (finish-output stream)
        (check (read-line stream) "{\"id\":9,\"jsonrpc\":\"2.0\",\"result\":{}}")
        (om.server::wait-server (or om.server::*server* (return-from server-socket-round-trip)))
        (check (probe-file socket) nil "stopped after shutdown")
        (ignore-errors (close stream))
        (sb-bsd-sockets:socket-close sock)))))

(deftest server-idle-exit
  (with-server-dir (dir)
    (let* ((socket (format nil "~Aengine.sock" dir))
           (s (om.server:start-server :root dir :socket socket :idle-seconds 1)))
      (multiple-value-bind (stream sock) (test-connect socket)
        (sleep 1.4)
        (check (om.server::server-done s) nil "a connected client keeps it running")
        (close stream)
        (sb-bsd-sockets:socket-close sock))
      (loop repeat 40 until (om.server::server-done s) do (sleep 0.1))
      (check (om.server::server-done s) t "exits with no client for the idle period")
      (check (probe-file socket) nil))))

(deftest server-turn-end
  (with-server-dir (dir)
    (om.server:start-server :root dir :socket (format nil "~Aengine.sock" dir) :idle-seconds 0)
    (let ((transcript (format nil "~At.jsonl" dir)))
      (write-lines transcript
                   '("{\"id\":\"m1\",\"timestamp\":\"2026-05-01T10:00:00Z\",\"role\":\"user\",\"text\":\"Deploy on port 8080 and cache in /var/cache/app.\"}"
                     "{\"id\":\"m2\",\"timestamp\":\"2026-05-01T10:00:05Z\",\"role\":\"assistant\",\"text\":\"Running the checks.\"}"))
      (flet ((turn-end (&rest extra)
               (json-parse
                (rpc-line (json-string
                           (jobject "jsonrpc" "2.0" "id" 1 "method" "turn-end"
                                    "params" (apply #'jobject "root" dir "harness" "mine" "session_id" "s1"
                                                    "transcript_path" transcript "format" "generic" "cwd" dir
                                                    extra)))))))
        (let ((r (jget (turn-end) "result")))
          (check (and (plusp (jget r "appended")) t) t "records appended")
          (check (coerce (jget r "remaining") 'list) '("settle-curated")))
        (let* ((store (store-load (format nil "~Asessions/mine/s1.jsonl" dir)))
               (first (stored-entry (first store))))
          (check (stored-after (first store)) "m2" "stored after the tip")
          (check (jget first "customType") "om.observations.recorded")
          (check (jget first "parentId") "m2")
          (check (length (jget first "id")) 10)
          (check (and (search "8080" (json-string first)) t) t "the port is recorded"))
        (check (jget (jget (turn-end) "result") "appended") 0d0 "an unchanged transcript appends nothing")
        (check (jget (jget (turn-end "env" (jobject "PI_OBSERVATIONAL_MEMORY_PASSIVE" "1")) "result") "passive") :true)
        (check (jget (jget (turn-end "format" "nope") "error") "code") -32004d0 "an unknown format")
        (check (jget (jget (turn-end "root" "/elsewhere") "error") "code") -32602d0 "another root")
        (check (jget (jget (turn-end "session_id" "") "error") "message") "om: no session id")))))
