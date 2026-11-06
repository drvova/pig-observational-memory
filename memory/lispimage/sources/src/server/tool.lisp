;;;; The memory tool on the image (docs/lisp-port-tool-layer.md, "The memory tool in a child"):
;;;; engine-rpc's tool/call (the PiG adapter), the MCP server's initialize, tools/list and
;;;; tools/call (om mcp), and om call. Each runs the model's Lisp through om.layer's
;;;; memory-tool-run, whose program runs in a child SBCL; the tool's fixed text is the Lisp one
;;;; (toolspec.json's lisp spec, docs/lisp-core.md decision 10). Loaded after methods.lisp and
;;;; events.lisp, so each method here replaces a not-ported entry.

(in-package "OM.SERVER")

;;; The fixed text

(defparameter +mcp-protocol-version+ "2025-06-18" "cli.mcpProtocolVersion.")

(defun tool-description ()
  "cli.toolDescription over the Lisp text: the description, a blank line, the prompt line and
the guidelines, one per line."
  (om::join (append (list (tool-spec-string "description") "" (tool-spec-string "promptSnippet"))
                    (tool-prompt-guidelines))
            (string #\Newline)))

;;; engine-rpc (the PiG adapter)

(defun conn-config-home () (host-string "host/configHome"))

(define-method "tool/call" (params)
  ;; harness.Manager.RunMemory under the event's request context (cancellation kills the child).
  (block tool-call
   (with-event (c params)
    (let ((name (om::jget params "name")))
      (unless (equal name "memory")
        (rpc-fail +invalid-params+ "om engine: unknown tool ~A" name)))
    (ensure-config c)
    (let* ((args (let ((a (om::jget params "params"))) (if (hash-table-p a) a (om::jobject))))
           (home (conn-config-home))
           (project (om.layer::project-root (scope-cwd)))
           (store (and (rcfg-curated-memory (conn-cfg c)) (string/= home "")
                       (format nil "~A/observational-memory/memories" (string-right-trim "/" home))))
           (branch (handler-case (get-branch c)
                     (error (e)
                       (return-from tool-call
                         (om::jobject "text" (format nil "Error: ~A" e) "isError" :true
                                      "details" (om::raw-json (om.layer::details-json
                                                               (om::str (om::jget args "code")) nil
                                                               (om.layer::make-tool-record) "compile")))))))
           (pin (sb-thread:with-mutex ((conn-mutex c)) (conn-pin c)))
           (call (om.layer:make-call :branch branch :curated-dir store :project project
                                     :session (host-string "host/sessionId") :model (session-model-key))))
      (multiple-value-bind (text is-error details)
          (om.layer::memory-tool-run args :call call
                                          :index-shown (and (om.layer::pin-p pin) (om.layer::pin-shown-names pin))
                                          :code-store (om.layer::code-store-dir home project)
                                          :cancelled-p #'cancelled-p)
        (om::jobject "text" text "isError" (om::jbool is-error) "details" (om::raw-json details)))))))

;;; The om client: MCP and om call

(defun om-tool-call (params arguments)
  "The memory tool over the session the client resolved (session_id, transcript_path, format,
cwd; a scratch session for an MCP client with no transcript): (values text is-error)."
  (let* ((format (check-format params))
         (id (param-string params "session_id")))
    (when (string= id "") (rpc-fail +invalid-params+ "om: no session id"))
    (let* ((s (find-session (param-string params "harness" "generic") id))
           ;; The branch is read under the session's mutex only: a memory call takes no engine
           ;; lock in Go either, and the program may run for a while.
           (branch (handler-case (sb-thread:with-mutex ((session-mutex s)) (read-branch s params format))
                     (error (e) (return-from om-tool-call (values (format nil "Error: ~A" e) t)))))
           (cwd (param-string params "cwd"))
           (project (om.layer::project-root cwd))
           (call (om.layer:make-call :branch branch :curated-dir (and (curated-on-p) (curated-dir))
                                     :project project :session id :model "")))
      (multiple-value-bind (text is-error)
          (om.layer::memory-tool-run (if (hash-table-p arguments) arguments (om::jobject))
                                     :call call
                                     :index-shown (om.layer::pin-shown-names (session-pin s))
                                     :code-store (om.layer::code-store-dir *root* project))
        (values text is-error)))))

(define-mcp-method "initialize" (params harness cwd)
  (om::jobject "protocolVersion" +mcp-protocol-version+
               "capabilities" (om::jobject "tools" (om::jobject))
               "serverInfo" (om::jobject "name" "om" "version" *build-id*)
               "instructions" (tool-description)))

(define-mcp-method "tools/list" (params harness cwd)
  (om::jobject "tools" (vector (om::jobject "name" (tool-spec-string "name") "title" (tool-spec-string "label")
                                            "description" (tool-description)
                                            "inputSchema" (om::jget *tool-spec* "parameters")))))

(define-mcp-method "tools/call" (params harness cwd)
  ;; cli.callTool: a failure is a tool error the model reads, never a protocol error.
  (flet ((answer (text is-error)
           (om::jobject "content" (vector (om::jobject "type" "text" "text" text)) "isError" (om::jbool is-error))))
    (let ((name (om::jget params "name")))
      (if (not (equal name "memory"))
          (answer (format nil "om: unknown tool ~A" (om::go-quote (om::str name))) t)
          (multiple-value-call #'answer (om-tool-call *mcp-outer* (om::jget params "arguments")))))))

(define-method "call" (params)
  ;; om call: {"text", "isError"}.
  (check-root params)
  (multiple-value-bind (text is-error) (om-tool-call params (om::jget params "arguments"))
    (om::jobject "text" text "isError" (om::jbool is-error))))
