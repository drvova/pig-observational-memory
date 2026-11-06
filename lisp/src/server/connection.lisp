;;;; W6: one engine-rpc connection's state (docs/lisp-port-peer.md), the image's stand-in for
;;;; the per-process runtime.Runtime an `om serve` engine keeps: PiG keeps its extension process,
;;;; and with it its connection, across sessions, so state per connection lives as long as Go's
;;;; does. It holds the directories initialize named, the configuration (EnsureConfig's
;;;; generation rule), the curated index pin, the context requests' pristine messages, the
;;;; thinning plan, the branch cache, and the compaction trigger's in-flight flag and backoff.
;;;; Its mutex is never held across a peer call.

(in-package "OM.SERVER")

(defstruct (rcfg (:constructor make-rcfg ()))
  "The settings the ported events read (config.Config): passive, curatedMemory, consolidate,
compactionSummaryMaxTokens, curatedMemoryIndexTokens and memoryFormat, the removed worker
`model` setting ((provider . id), config.Config.RemovedWorkerModel), the removed settings set to
a valid value (RemovedSet, in config.RemovedSettings order), whether kernelFloor asks for a
lowering with who and why, and the project trust they were read under (:trusted, :untrusted or
:unknown)."
  (passive nil)
  (curated-memory t)
  (consolidate-off nil)
  (summary-max 0)
  (index-tokens 2000)
  (memory-format "tables")
  (worker-model nil)
  (removed '())
  (floor-lowered nil)
  (trust :unknown))

(defstruct (conn (:constructor %make-conn))
  (mutex (sb-thread:make-mutex :name "om connection"))
  ;; initialize's agentDir and configDirName ("" for none; both "" is om's root and .om), and
  ;; the client environment the engine reads (PI_OBSERVATIONAL_MEMORY_PASSIVE).
  (agent-dir "") (config-dir-name "") (named-dirs nil)
  (env (om::jobject))
  ;; runtime.Runtime's configuration.
  (config (make-rcfg)) (config-loaded nil) (config-generation 0)
  ;; The curated index the current conversation was given (curated.Pin), nil for none.
  (pin nil)
  ;; Server.ctxSeq and ctxPristine.
  (ctx-seq 0) (ctx-pristine #())
  ;; runtime.CachedThinPlan's key and plan.
  (thin-key nil) (thin-plan nil)
  ;; branchCache: the branch last read, decoded.
  (branch #())
  ;; runtime.CompactInFlight and CompactionBackoff (a backoff struct, pig-compaction.lisp).
  (compact-in-flight nil) (backoff-tip nil)
  ;; hooks.extractMu for this connection's session: a PiG process runs one session at a time,
  ;; so it is a per-session mutex, which may be held while a host call waits.
  (extract-mutex (sb-thread:make-mutex :name "om extraction"))
  ;; harness.Manager's retired names (NoteRetired at each session start, server/migration.lisp):
  ;; the retired defined queries no other tool held, which join the skip list (SkipNames). Go
  ;; keeps one Manager per process and PiG one connection per process, so the set lives as long
  ;; as the connection and only grows.
  (retired-names '())
  (ended nil))

(defun make-conn (params)
  (let* ((agent (param-string params "agentDir")) (name (param-string params "configDirName"))
         (env (om::jget params "env")))
    (%make-conn :agent-dir agent :config-dir-name name :named-dirs (or (string/= agent "") (string/= name ""))
                :env (if (hash-table-p env) env (om::jobject)))))

(defun conn-end (c)
  "Ends a connection's state (shutdown, or the connection closed): its caches are dropped."
  (sb-thread:with-mutex ((conn-mutex c))
    (setf (conn-ended c) t (conn-pin c) nil (conn-ctx-seq c) 0 (conn-ctx-pristine c) #()
          (conn-thin-key c) nil (conn-thin-plan c) nil (conn-branch c) #()
          (conn-compact-in-flight c) nil (conn-backoff-tip c) nil)))

(defun current-conn ()
  "The connection's state; an engine-rpc event before initialize (or after shutdown) is refused
as Go's server refuses it."
  (let ((c (and *peer* (peer-conn *peer*))))
    (unless c (rpc-fail +invalid-request+ "not initialized"))
    c))

;;; The event's scope

(defvar *scope* nil "The event's scope: {id, cwd, hasUI}.")

(defun scope-id () (param-string *scope* "id"))
(defun scope-cwd () (param-string *scope* "cwd"))

(defmacro with-event ((conn params) &body body)
  "Runs body for one engine-rpc event: conn is the connection's state and *scope* the event's."
  `(let* ((,conn (current-conn))
          (*scope* (let ((s (om::jget ,params "scope"))) (if (hash-table-p s) s (om::jobject)))))
     (declare (ignorable ,conn))
     ,@body))

;;; Host calls

(defparameter +long-host-calls+ '("models/streamResult" "host/compact" "models/refresh")
  "The host calls that wait on a model or a compaction: no deadline but cancellation and loss.")

(defun host-call (method &rest pairs)
  "One host call for the event's scope: the result object. pairs are the params beyond scope."
  (unless *peer* (error 'peer-closed))
  (peer-call *peer* method (apply #'om::jobject "scope" (scope-id) pairs)
             :deadline (if (member method +long-host-calls+ :test #'string=) nil *host-call-seconds*)))

(defun host-string (method)
  "remoteHost.stringCall: the value, \"\" when the call fails."
  (handler-case (om::str (om::jget (host-call method) "value"))
    (error () "")))

(defun session-identity ()
  "hooks.sessionIdentityOf: the session id, else the session file."
  (let ((id (host-string "host/sessionId")))
    (if (string/= id "") id (host-string "host/sessionFile"))))

(defun session-model-key ()
  "harness.ModelKey of the session model: provider/id, or id, or \"\" without a model."
  (let ((model (handler-case (om::jget (host-call "host/sessionModel") "model") (error () nil))))
    (if (not (hash-table-p model))
        ""
        (let ((id (om::str (om::jget model "id"))) (provider (om::str (om::jget model "provider"))))
          (cond ((string= id "") "")
                ((string/= provider "") (concatenate 'string provider "/" id))
                (t id))))))

(defun get-branch (c)
  "remoteHost.GetBranch through the branch cache: the adapter sends only the entries after the
ones held when it still has the last of them at its place. A simple vector of entries."
  (let* ((cached (sb-thread:with-mutex ((conn-mutex c)) (conn-branch c)))
         (from (length cached))
         (anchor (if (plusp from) (om::entry-id (aref cached (1- from))) "")))
    (when (string= anchor "") (setf from 0))
    (let* ((reply (if (string= anchor "")
                      (host-call "host/getBranch" "from" from)
                      (host-call "host/getBranch" "from" from "anchorId" anchor)))
           (rfrom (let ((f (om::jget reply "from"))) (if (realp f) (round f) 0)))
           (raw (let ((e (om::jget reply "entries"))) (if (vectorp e) e #()))))
      (when (or (< rfrom 0) (> rfrom from))
        (error 'host-call-error :code +host-error+
                                :message (format nil "enginerpc: getBranch answered from ~D for a request from ~D" rfrom from)))
      (let* ((tail (loop for e across raw when (hash-table-p e) collect (om::entry-from-map e)))
             (next (concatenate 'simple-vector (subseq cached 0 rfrom) tail)))
        (sb-thread:with-mutex ((conn-mutex c))
          ;; A null entry would shift the positions the next request names: read it whole next time.
          (setf (conn-branch c) (if (= (length tail) (length raw)) next #())))
        next))))

;;; Configuration (config.Load, runtime.EnsureConfig)

(defun conn-paths (c)
  "config.DefaultPaths as the engine's initialize set them: (values agent-dir project-dir-name)."
  (if (conn-named-dirs c)
      (values (if (string/= (conn-agent-dir c) "")
                  (conn-agent-dir c)
                  (let ((env (sb-posix:getenv "PI_CODING_AGENT_DIR")))
                    (if (and env (string/= env ""))
                        env
                        (format nil "~A/.pi/agent" (string-right-trim "/" (or (sb-posix:getenv "HOME") ""))))))
              (if (string/= (conn-config-dir-name c) "") (conn-config-dir-name c) ".pi"))
      (values (string-right-trim "/" (or *root* "")) ".om")))

(defun positive-integer (v)
  "config.positiveInteger: a whole number above 0, else nil."
  (and (realp v) (> v 0) (= v (ftruncate v)) (truncate v)))

(defparameter +removed-settings+
  '("observeAfterTokens" "reflectAfterTokens" "observerChunkMaxTokens" "compactionCatchUpMaxChunks"
    "consolidateWhenIdle" "model" "fallbackModel" "observationsPoolTargetTokens" "observationsPoolMaxTokens"
    "compactionMaxRetainedTokens" "compactAfterTokens" "compactAfterTokensMode" "compactAfterTokensRatio"
    "workerMemoryMaxTokens" "agentMaxTurns" "agentMaxTokens" "showWorkerNotifications" "memoryEngine")
  "config.RemovedSettings.")

(defun apply-settings (cfg path)
  "readNamespaced(path).apply: the observational-memory object's booleans (passive,
curatedMemory, consolidate), its positive integers (compactionSummaryMaxTokens,
curatedMemoryIndexTokens), memoryFormat, and whether the removed `model` setting is set."
  (let ((nested (om::jget (read-json-file path) "observational-memory")))
    (when (hash-table-p nested)
      (flet ((bool (key setter)
               (multiple-value-bind (v present) (gethash key nested)
                 (when (and present (member v '(:true :false))) (funcall setter (eq v :true))))))
        (bool "passive" (lambda (b) (setf (rcfg-passive cfg) b)))
        (bool "curatedMemory" (lambda (b) (setf (rcfg-curated-memory cfg) b)))
        (bool "consolidate" (lambda (b) (setf (rcfg-consolidate-off cfg) (not b)))))
      (let ((n (positive-integer (gethash "compactionSummaryMaxTokens" nested))))
        (when n (setf (rcfg-summary-max cfg) n)))
      (let ((n (positive-integer (gethash "curatedMemoryIndexTokens" nested))))
        (when n (setf (rcfg-index-tokens cfg) n)))
      (let ((f (gethash "memoryFormat" nested)))
        (when (member f '("tables" "lines") :test #'equal) (setf (rcfg-memory-format cfg) f)))
      ;; config.normalizeSettings: the removed settings set to a valid value (ignored; /om:status
      ;; lists them), and the removed `model` setting (memory v1's worker model), kept only to
      ;; migrate its layer.
      (let ((removed (removed-settings-set nested)))
        (when removed
          (setf (rcfg-removed cfg)
                (remove-if-not (lambda (k) (or (member k removed :test #'string=)
                                               (member k (rcfg-removed cfg) :test #'string=)))
                               +removed-settings+))))
      (let ((m (gethash "model" nested)))
        (when (removed-value-valid-p "model" m)
          (setf (rcfg-worker-model cfg) (cons (om::str (gethash "provider" m)) (gethash "id" m)))))
      ;; config.normalizeFloor: kernelFloor {"lower": bool, "requestedBy", "reason"}, a later
      ;; source overriding; a lowering without who and why leaves the floor engaged (Floor.Lower).
      (let ((f (gethash "kernelFloor" nested)))
        (when (and (hash-table-p f) (member (gethash "lower" f) '(:true :false)))
          (setf (rcfg-floor-lowered cfg)
                (and (eq (gethash "lower" f) :true)
                     (string/= (om::trim-space (om::str (gethash "requestedBy" f))) "")
                     (string/= (om::trim-space (om::str (gethash "reason" f))) ""))))))))

(defun removed-value-valid-p (key v)
  "config.removedValueValid: whether a removed setting's value is one memory v1 applied."
  (cond ((string= key "compactionCatchUpMaxChunks") (and (realp v) (= v (ftruncate v)) (>= v 0)))
        ((string= key "compactAfterTokensMode") (and (member v '("calibrated" "ratio") :test #'equal) t))
        ((string= key "compactAfterTokensRatio") (and (realp v) (> v 0) (< v 1)))
        ((member key '("model" "fallbackModel") :test #'string=)
         (and (hash-table-p v) (stringp (gethash "id" v)) (string/= (gethash "id" v) "")))
        ((string= key "memoryEngine") (and (member v '("v1" "v2") :test #'equal) t))
        ((member key '("consolidateWhenIdle" "showWorkerNotifications") :test #'string=)
         (and (member v '(:true :false)) t))
        (t (and (positive-integer v) t))))

(defun removed-settings-set (nested)
  "The removed settings nested sets to a valid value, in config.RemovedSettings order."
  (loop for key in +removed-settings+
        when (multiple-value-bind (v present) (gethash key nested)
               (and present (removed-value-valid-p key v)))
          collect key))

(defun load-config (c cwd trust)
  (multiple-value-bind (agent-dir dir-name) (conn-paths c)
    (let ((cfg (make-rcfg)))
      (setf (rcfg-trust cfg) trust)
      (apply-settings cfg (format nil "~A/settings.json" agent-dir))
      (when (eq trust :trusted)
        (apply-settings cfg (format nil "~A/~A/settings.json" (string-right-trim "/" cwd) dir-name)))
      (multiple-value-bind (passive set) (env-passive (om::jobject "env" (conn-env c)))
        (when set (setf (rcfg-passive cfg) passive)))
      cfg)))

(defun host-trust ()
  "config.TrustFrom(h.ProjectTrusted()): a failed check is :unknown."
  (handler-case (if (eq (om::jget (host-call "host/projectTrusted") "value") :true) :trusted :untrusted)
    (error () :unknown)))

(defun ensure-config (c)
  "runtime.EnsureConfig: loads the configuration once, with the trust check made without the
lock; a reload during the check makes it ask again, and an unknown trust is not kept."
  (loop
    (multiple-value-bind (loaded generation)
        (sb-thread:with-mutex ((conn-mutex c)) (values (conn-config-loaded c) (conn-config-generation c)))
      (when loaded (return))
      (let ((trust (host-trust)) (cwd (scope-cwd)))
        (sb-thread:with-mutex ((conn-mutex c))
          (cond ((conn-config-loaded c) (return))
                ((/= (conn-config-generation c) generation))
                (t (setf (conn-config c) (load-config c cwd trust)
                         (conn-config-loaded c) (not (eq trust :unknown)))
                   (return))))))))

(defun conn-reload-config (c)
  "runtime.ReloadConfig."
  (sb-thread:with-mutex ((conn-mutex c))
    (incf (conn-config-generation c))
    (setf (conn-config-loaded c) nil)))

(defun conn-cfg (c) (sb-thread:with-mutex ((conn-mutex c)) (conn-config c)))

(defun store-on-p (c)
  "harness.StoreOf's answer: curated memory on and a config home the host names."
  (and (rcfg-curated-memory (conn-cfg c)) (string/= (host-string "host/configHome") "")))

(defun conn-skip-tools (c)
  "harness.MemoryToolNames over the connection's manager (Manager.SkipNames): the memory tool,
then the retired names NoteRetired added, in name order."
  (cons om::+memory-tool+
        (sort (copy-list (sb-thread:with-mutex ((conn-mutex c)) (conn-retired-names c))) #'string<)))
