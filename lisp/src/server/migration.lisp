;;;; The layer part of a session start (docs/lisp-port-migration.md), on PiG (event/sessionStart)
;;;; and om (session-start): harness.Manager.NoteRetired, which reads the retired defined queries'
;;;; names into the connection's skip list (Manager.SkipNames, harness.MemoryToolNames), and
;;;; engine.MigrateLayer (harness.MigrateLayerV2), which folds memory v1's worker instructions
;;;; into the consolidation instructions, over the Go layer in the curated store
;;;; (lisp/src/layer/go-layer.lisp).
;;;;
;;;; The skip list's lifetime: Go keeps one harness.Manager per engine process, and its retired
;;;; set only grows. A PiG process keeps one connection to the image across its sessions, so the
;;;; set is the connection's (conn-retired-names): it grows at each session start and ends with
;;;; the connection, as Go's ends with the process. An om hook is one Go process per event, whose
;;;; session start reads the names and then exits, so on om only the reading's side effect (the
;;;; legacy definitions moved into the layer) outlives the event, and the image keeps no list.

(in-package "OM.SERVER")

(defun dir-has-entries-p (dir)
  (and (om.golayer::list-dir (string-right-trim "/" dir)) t))

(defun layer-work-p (dir cfg)
  "Whether NoteRetired or MigrateLayerV2 may read or write the layer: tool definitions kept for
all models or legacy definitions (RetiredQueries), written instructions in any model's layer, or
the removed worker `model` setting naming a layer to fold."
  (let ((dir (string-right-trim "/" dir)))
    (or (dir-has-entries-p (format nil "~A/definitions" dir))
        (dir-has-entries-p (format nil "~A/harness/_2a/tool" dir))
        (rcfg-worker-model cfg)
        (loop for (name . dirp) in (om.golayer::list-dir (format nil "~A/harness" dir))
              thereis (and dirp (dir-has-entries-p (format nil "~A/harness/~A/instructions" dir name)))))))

(defun lowered-floor-refusal (dir cfg)
  "A lowered kernel floor admits definitions the kernel refuses and is recorded in the layer's
floor.jsonl (harness.floorOf); the image checks with the floor engaged and records nothing, so a
start with layer work under a lowered floor is the Go engine's: the refusal text, or nil."
  (and (rcfg-floor-lowered cfg) (layer-work-p dir cfg)
       "the session start with a lowered kernel floor and a layer to read (kernelFloor)"))

;;; NoteRetired and the skip list

(defun host-tool-names ()
  "remoteHost.AllTools' names: (values names t), or (values nil nil) when the call fails."
  (handler-case
      (let ((tools (om::jget (host-call "host/allTools") "tools")))
        (values (loop for tool across (if (vectorp tools) tools #())
                      collect (om::str (om::jget tool "name")))
                t))
    (request-cancelled (e) (error e))
    (error () (values nil nil))))

(defun note-retired (c dir)
  "harness.Manager.NoteRetired: the retired defined queries' names (RetiredQueries, which moves
legacy definitions into the layer) join the connection's set, except the memory tool's and a
name another tool of the host's registry holds. An unreadable store or registry adds nothing."
  (let ((retired (handler-case (om.golayer::retired-query-names dir)
                   (error (e) (log-line "retired queries: ~A" e) nil))))
    (when retired
      (multiple-value-bind (taken ok) (host-tool-names)
        (when ok
          (sb-thread:with-mutex ((conn-mutex c))
            (dolist (name retired)
              (when (and (string/= name "") (string/= name om::+memory-tool+)
                         (not (member name taken :test #'string=))
                         (not (member name (conn-retired-names c) :test #'string=)))
                (push name (conn-retired-names c))))))))))

(defun defined-queries (c store)
  "harness.DefinedQueries' names: the stored definitions (ListDefinitions) the skip list holds,
the memory tool aside. The detectors read a call of one as a memory query (legacy-memory-call)."
  (let ((skip (rest (conn-skip-tools c))))
    (when skip
      (loop for name in (handler-case (om.golayer::list-definitions store) (error () '()))
            when (member name skip :test #'string=) collect name))))

;;; MigrateLayer

(defun worker-model-key (cfg session)
  "harness.WorkerModelKey: the key memory v1 kept its worker model's definitions under, from the
removed `model` setting: the registry's model for the setting's provider (else the session
model's) and id when the host finds it, else provider/id; \"\" with no setting or when it names
the session model."
  (let ((ref (rcfg-worker-model cfg)))
    (if (or (null ref) (string= (cdr ref) ""))
        ""
        (let* ((provider (if (string/= (car ref) "") (car ref) (om::str (om::jget session "provider"))))
               (key (if (string/= provider "") (concatenate 'string provider "/" (cdr ref)) (cdr ref)))
               (found (handler-case (om::jget (host-call "models/find" "provider" provider "id" (cdr ref)) "model")
                        (request-cancelled (e) (error e))
                        (error () nil))))
          (when (hash-table-p found) (setf key (model-key-of found)))
          (if (string= key (model-key-of session)) "" key)))))

(defun session-start-layer (c dir cfg)
  "engine.SessionStart's NoteRetired and MigrateLayer over the curated store dir, each migration
told the user through a host with a UI (notify-ui), as Go's MigrateLayerV2 tells it."
  (let ((refusal (lowered-floor-refusal dir cfg)))
    (when refusal (not-ported (format nil "event/sessionStart: ~A" refusal))))
  (note-retired c dir)
  (let* ((session (session-model))
         (messages (om.golayer::migrate-layer-v2 dir (model-key-of session) (worker-model-key cfg session))))
    (dolist (message messages)
      (notify-ui message "info"))))

(defun om-session-start-layer ()
  "The same on om's neutral host (no session model, no tool registry, no UI): the retired names'
side effect, then MigrateLayerV2 for every model. False when a lowered kernel floor leaves the
migration to the Go client (the migrate-layer step)."
  (when (curated-on-p)
    (let ((dir (curated-dir)) (cfg (make-rcfg)))
      (apply-settings cfg (format nil "~A/settings.json" (string-right-trim "/" *root*)))
      (when (lowered-floor-refusal dir cfg)
        (return-from om-session-start-layer nil))
      (handler-case (om.golayer::retired-query-names dir)
        (error (e) (log-line "retired queries: ~A" e)))
      (om.golayer::migrate-layer-v2 dir "" "")))
  t)
