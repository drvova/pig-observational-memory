;;;; The PiG session events over engine-rpc (docs/lisp-port-peer.md, "Events"): session start
;;;; with a curated store, before agent start (the curated-memory system-prompt section), agent
;;;; start, project trust, session compact and shutdown, and /om:view (command/run). Ported from
;;;; memory/enginerpc/server.go's handle and memory/engine (SessionStart, Index, SectionText,
;;;; CuratedCall, SettleCurated) and memory/commands/view.go (RunView, ShowView) with
;;;; memory/clipboard.

(in-package "OM.SERVER")

;;; Host helpers

(defun scope-has-ui () (eq (om::jget *scope* "hasUI") :true))

(defun host-notify (message level)
  "remoteHost.Notify: the host shows message; a failed call is dropped."
  (handler-case (progn (host-call "host/notify" "message" message "level" level) t)
    (request-cancelled (e) (error e))
    (error () nil)))

(defun notify-ui (message level)
  "hooks.notifyUI: notifies only a host with a UI, and never fails."
  (when (scope-has-ui) (host-notify message level)))

(defun session-model ()
  "remoteHost.SessionModel: the model object, nil for none or a failed call."
  (let ((m (handler-case (om::jget (host-call "host/sessionModel") "model")
             (request-cancelled (e) (error e))
             (error () nil))))
    (and (hash-table-p m) m)))

(defun model-string (model key) (om::str (om::jget model key)))

(defun model-id (model)
  "host.ModelID: id, else modelId."
  (let ((id (model-string model "id"))) (if (string/= id "") id (model-string model "modelId"))))

(defun model-key-of (model)
  "harness.ModelKey: provider/id, or the id alone; \"\" for no model."
  (let ((id (model-id model)) (provider (model-string model "provider")))
    (cond ((string= id "") "")
          ((string/= provider "") (concatenate 'string provider "/" id))
          (t id))))

(defun model-int (model key)
  "host.ModelInt: a number truncated, 0 otherwise."
  (let ((v (om::jget model key))) (if (realp v) (truncate v) 0)))

(defun model-max-tokens (model)
  "host.ModelMaxTokens: maxTokens, else maxOutputTokens; 0 when unknown."
  (let ((n (model-int model "maxTokens"))) (if (> n 0) n (model-int model "maxOutputTokens"))))

(defun conn-pin* (c)
  "The connection's curated.Pin, made on first use."
  (sb-thread:with-mutex ((conn-mutex c))
    (or (conn-pin c) (setf (conn-pin c) (om.layer::make-pin)))))

;;; The curated store (engine.CuratedCall)

(defun store-dir-of (home)
  "curated.DefaultDir(home)."
  (format nil "~A/observational-memory/memories" (string-right-trim "/" home)))

(defun curated-call (c)
  "engine.CuratedCall: (values store-dir project cfg) with the configuration read fresh under
the host's trust; nil when curated memory is off or the host names no config home."
  (let* ((trust (host-trust)) (cfg (load-config c (scope-cwd) trust)))
    (when (rcfg-curated-memory cfg)
      (let ((home (host-string "host/configHome")))
        (when (string/= home "")
          (values (store-dir-of home) (om.layer::project-root (scope-cwd)) cfg))))))

(defun settle-curated-pig (c)
  "engine.SettleCurated over the host: the curated evidence check over the branch's user
corrections, each promotion and the flagged memories told through host/notify (NotifySettled,
whatever the UI). An unreadable branch settles nothing."
  (multiple-value-bind (dir project) (curated-call c)
    (when dir
      (let ((branch (handler-case (get-branch c)
                      (request-cancelled (e) (error e))
                      (error () (return-from settle-curated-pig nil)))))
        (multiple-value-bind (promoted flagged)
            (handler-case (om.layer::curated-settle dir project (om.layer::branch-corrections branch))
              (error (e) (log-line "curated settle: ~A" e) (values '() '())))
          (dolist (notice (om.layer::settled-notices promoted flagged))
            (host-notify notice "info")))))))

(defun dir-has-entries-p (pattern)
  (and (directory pattern :resolve-symlinks nil) t))

(defun retired-data-p (dir)
  "Whether the store holds what harness.RetiredQueries reads (the layer's all-models tool
definitions, or the legacy definitions directory, which curated.Store.migrateDefinitions also
writes). The image does not read their names, so such a store is Go's to start."
  (let ((dir (string-right-trim "/" dir)))
    (or (dir-has-entries-p (format nil "~A/definitions/*.*" dir))
        (dir-has-entries-p (format nil "~A/harness/_2a/tool/*.*" dir)))))

(defun migration-data-p (dir cfg)
  "Whether harness.MigrateLayerV2 may have work: written instructions in any model's layer
(layer.Store.MigrateInstructions and MigrateToV2 read them), or the removed worker `model`
setting naming a layer to fold. The migration is not ported, so such a store is Go's to start."
  (or (rcfg-worker-model cfg)
      (dir-has-entries-p (format nil "~A/harness/*/instructions/*.*" (string-right-trim "/" dir)))))

;;; Session start, project trust, agent start, compaction, shutdown

(define-method "event/sessionStart" (params)
  ;; Server.handle: ReloadConfig, then engine.SessionStart: the pin reset, the namespace reset
  ;; and the detector pass stopped (the image holds neither), the retired defined tools' names
  ;; read (NoteRetired), the v1 layer migrated (MigrateLayer) and the curated evidence settled
  ;; (SettleCurated). A store holding retired definitions or a layer to migrate is refused
  ;; before the settle writes anything, and the image then refuses every later event that
  ;; reads the skip list (check-skip-tools): Go's list grows with those names.
  (with-event (c params)
    (conn-reload-config c)
    (om.layer::pin-reset (conn-pin* c))
    (multiple-value-bind (dir project cfg) (curated-call c)
      (declare (ignore project))
      (when dir
        (when (retired-data-p dir)
          (sb-thread:with-mutex ((conn-mutex c)) (setf (conn-retired-unknown c) t))
          (not-ported "event/sessionStart with retired defined tools in the curated store (harness.Manager.NoteRetired)"))
        (when (migration-data-p dir cfg)
          (not-ported "event/sessionStart with a v1 layer to migrate (harness.MigrateLayerV2)"))
        (settle-curated-pig c)))
    (om::jobject)))

(define-method "event/projectTrust" (params)
  ;; Server.handle: ReloadConfig (a /trust decision applies to the next read).
  (with-event (c params)
    (conn-reload-config c)
    (om::jobject)))

(define-method "event/agentStart" (params)
  ;; hooks.OnAgentStart: EnsureConfig, which the context subscription follows.
  (with-event (c params)
    (ensure-config c)
    (thinning-result c)))

(define-method "event/sessionCompact" (params)
  ;; Server.handle: the pin is reset, so the index renders again after a compaction.
  (with-event (c params)
    (om.layer::pin-reset (conn-pin* c))
    (om::jobject)))

(define-method "event/sessionShutdown" (params)
  ;; Server.handle: the namespace reset and the detector pass stopped; the image holds neither
  ;; (a deferred compaction trigger ends with the connection's calls).
  (with-event (c params)
    (om::jobject)))

;;; Before agent start: the system-prompt section

(defparameter +memory-section+ "curated-memory" "enginerpc.MemorySection.")

(defun section-text (index on)
  "engine.SectionText: the tool-note line, then the curated index when it is on and not empty."
  (if (or (not on) (string= index ""))
      +tool-note-line+
      (concatenate 'string +tool-note-line+ (string #\Newline) (string #\Newline) index)))

(define-method "event/beforeAgentStart" (params)
  ;; engine.Index through the connection's pin and engine.SectionText, as one named section
  ;; (systemPromptOptions.sections), never a returned systemPrompt.
  (with-event (c params)
    (multiple-value-bind (dir project cfg) (curated-call c)
      (let ((text (if dir
                      (section-text (values (om.layer::pin-get (conn-pin* c)
                                                               (lambda () (om.layer::index-for dir project (rcfg-index-tokens cfg)))))
                                    t)
                      (section-text "" nil))))
        (om::jobject "sections" (om::jobject +memory-section+ text))))))

;;; /om:view (commands.RunView, ShowView) and the clipboard (memory/clipboard)

(defparameter *clipboard-seconds* 2 "clipboard.Timeout.")

(defun clipboard-commands ()
  "clipboard.Commands for the image's platform: (program args display-env socket)."
  (if (search "Darwin" (software-type))
      '(("pbcopy" () nil nil))
      '(("wl-copy" () "WAYLAND_DISPLAY" "wayland-0")
        ("xclip" ("-selection" "clipboard") "DISPLAY" nil)
        ("xsel" ("--clipboard" "--input") "DISPLAY" nil)
        ("termux-clipboard-set" () nil nil))))

(defun env-set-p (name) (let ((v (sb-posix:getenv name))) (and v (string/= v ""))))

(defun find-program (name)
  "exec.LookPath: the first executable file named name on PATH, or nil."
  (dolist (dir (om::split (or (sb-posix:getenv "PATH") "") ":"))
    (let ((path (format nil "~A/~A" (if (string= dir "") "." (string-right-trim "/" dir)) name)))
      (when (and (handler-case (zerop (sb-posix:access path sb-posix:x-ok)) (error () nil))
                 (not (handler-case (sb-posix:s-isdir (sb-posix:stat-mode (sb-posix:stat path))) (error () t))))
        (return path)))))

(defun usable-clipboard-commands ()
  "clipboard.Usable: the commands found on PATH whose display is set, or whose socket is in
XDG_RUNTIME_DIR."
  (loop for (program args env socket) in (clipboard-commands)
        for path = (and (or (null env) (env-set-p env)
                            (and socket (env-set-p "XDG_RUNTIME_DIR")
                                 (probe-file (format nil "~A/~A" (sb-posix:getenv "XDG_RUNTIME_DIR") socket))))
                        (find-program program))
        when path collect (list path args)))

(defun run-clipboard (path args text)
  "clipboard.Run: the command with text on its stdin, within *clipboard-seconds*; t on exit 0."
  (handler-case
      (let ((p (sb-ext:run-program path args :input :stream :output nil :error nil :wait nil)))
        (handler-case (progn (write-string text (sb-ext:process-input p)) (close (sb-ext:process-input p)))
          (error () (ignore-errors (close (sb-ext:process-input p)))))
        (loop repeat (* 50 *clipboard-seconds*)
              while (sb-ext:process-alive-p p)
              do (sleep 0.02))
        (when (sb-ext:process-alive-p p)
          (sb-ext:process-kill p 9)
          (sb-ext:process-wait p))
        (prog1 (and (eq (sb-ext:process-status p) :exited) (zerop (sb-ext:process-exit-code p)))
          (sb-ext:process-close p)))
    (error () nil)))

(defun clipboard-copy (text)
  "clipboard.CopyIfAvailable: :copied, :failed, or :unavailable when no command can reach a
clipboard here."
  (let ((usable (usable-clipboard-commands)))
    (cond ((null usable) :unavailable)
          ((loop for (path args) in usable thereis (run-clipboard path args text)) :copied)
          (t :failed))))

(defvar *view-clipboard-noted* nil
  "runtime.ShowOnce(\"view-no-clipboard\"): the image is one process, so the note is shown once
per image, as Go shows it once per engine process.")

(defun show-view (output notice)
  "commands.ShowView: a notice as it is, else the output with the clipboard's note."
  (if notice
      (host-notify output "info")
      (let ((outcome (clipboard-copy output)) (nl2 (format nil "~%~%")))
        (case outcome
          (:copied (host-notify (concatenate 'string output nl2 "Copied /om:view output to clipboard.") "info"))
          (:unavailable
           (if (not *view-clipboard-noted*)
               (progn (setf *view-clipboard-noted* t)
                      (host-notify (concatenate 'string output nl2 "Note: no clipboard is available here, so /om:view output is not copied (shown once).") "info"))
               (host-notify output "info")))
          (t (host-notify (concatenate 'string output nl2 "Warning: failed to copy /om:view output to clipboard.") "info"))))))

(defun run-view (c args)
  "commands.RunView: EnsureConfig, the branch (a failure is the command's error), then the view."
  (ensure-config c)
  (let ((branch (handler-case (get-branch c)
                  (request-cancelled (e) (error e))
                  (host-call-error (e) (rpc-fail +host-error+ "~A" (host-call-error-message e)))
                  (peer-closed (e) (error e)))))
    (multiple-value-bind (text notice) (om::view-text branch args)
      (show-view text notice))))

(define-method "command/run" (params)
  ;; Server.handle's MethodCommand: /om:view runs on the image; /om:status reads the harness,
  ;; the code store and the runtime's state, which are not ported.
  (with-event (c params)
    (let ((name (om::str (om::jget params "name"))))
      (cond ((string= name "om:view") (run-view c (om::str (om::jget params "args"))) (om::jobject))
            ((string= name "om:status") (not-ported "command/run om:status"))
            (t (rpc-fail +invalid-params+ "unknown command: ~A" name))))))
