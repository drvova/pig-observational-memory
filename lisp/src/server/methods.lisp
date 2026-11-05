;;;; The dispatch table: every method the image answers (docs/lisp-port-image-client.md,
;;;; "Methods"). The events of docs/protocol.md take the session fields the Go client resolved
;;;; (root, harness, session_id, transcript_path, format, cwd, env) plus the event's own; the
;;;; engine-rpc methods (docs/engine-rpc.md) are listed so they answer "not ported" rather than
;;;; "method not found". A work package that ports one redefines it with define-method in its
;;;; own file, loaded after this one.

(in-package "OM.SERVER")

(define-method "initialize" (params)
  (let ((version (om::jget params "protocolVersion")))
    (when (and version (not (eql version 1d0)) (not (eql version 1)))
      (rpc-fail +invalid-params+ "om engine: protocol version ~A is not supported (1)" version)))
  (let ((out (om::jobject "protocolVersion" 1 "name" "om" "engine" "lisp")))
    (maphash (lambda (k v) (setf (gethash k out) v)) *server-info*)
    out))

(define-method "ping" (params) (om::jobject))

(define-method "status" (params)
  (let ((out (om::jobject "sessions" (session-count) "connections" (connection-count))))
    (maphash (lambda (k v) (setf (gethash k out) v)) *server-info*)
    out))

(define-method "shutdown" (params)
  (request-stop)
  (om::jobject))

;;; docs/protocol.md

(define-method "turn-end" (params)
  ;; engine.SettleCurated follows level 0 in every turn-end; curated memory is W4's, so the Go
  ;; client runs that step after the image answers.
  (with-session (s params)
    (let ((out (turn-end-level-0 s params)))
      (setf (gethash "remaining" out) (vector "settle-curated"))
      out)))

(define-method "prompt" (params)
  ;; Level 0 when the harness has no turn-end hook; handing the summary needs the start context
  ;; (the memory tool's guidance and the curated index), which is not ported.
  (when (eq (om::jget params "summary_on_prompt") :true)
    (not-ported "prompt with the summary handed"))
  (if (eq (om::jget params "run_level0") :true)
      (with-session (s params)
        (let ((out (turn-end-level-0 s params)))
          (setf (gethash "remaining" out) (vector "settle-curated"))
          out))
      (progn (check-root params) (om::jobject))))

(define-not-ported "session-start" "compact" "before-compact" "context")

;;; The memory tool, over MCP and om call

(defvar *mcp-methods* (make-hash-table :test 'equal)
  "MCP method -> function of (params harness cwd) returning the MCP result.")

(defmacro define-mcp-method (name (params harness cwd) &body body)
  `(progn (setf (gethash ,name *mcp-methods*)
                (lambda (,params ,harness ,cwd) (declare (ignorable ,params ,harness ,cwd)) ,@body))
          ,name))

(define-mcp-method "ping" (params harness cwd) (om::jobject))

(define-method "mcp" (params)
  ;; One MCP message the client received: {"message": the MCP answer, or null for a
  ;; notification}. MCP's own errors go inside the answer; "not ported" is this request's error,
  ;; so the client can answer the message itself.
  (check-root params)
  (let* ((message (om::jget params "message"))
         (method (om::jget message "method")))
    (unless (and (hash-table-p message) (stringp method))
      (rpc-fail +invalid-params+ "om engine: mcp needs a message with a method"))
    (multiple-value-bind (id has-id) (gethash "id" message)
      (let ((handler (gethash method *mcp-methods*)))
        (cond ((not has-id) (om::jobject "message" :null))
              ((null handler) (not-ported (format nil "the MCP method ~A" method)))
              (t (om::jobject "message"
                              (response id (funcall handler (or (om::jget message "params") (om::jobject))
                                                    (param-string params "harness")
                                                    (param-string params "cwd"))))))))))

(define-not-ported "call")

;;; docs/engine-rpc.md (the PiG adapter's process engine)

(define-not-ported "event/sessionStart" "event/projectTrust" "event/beforeAgentStart"
  "event/agentStart" "event/turnEnd" "event/agentSettled" "event/sessionBeforeCompact"
  "event/sessionCompact" "event/sessionShutdown" "event/context" "tool/call" "tool/renderCall"
  "tool/renderResult" "command/run")
