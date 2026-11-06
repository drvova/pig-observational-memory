;;;; Compaction on PiG over engine-rpc (docs/lisp-port-peer.md, "Compaction"): the trigger at
;;;; agent_settled (hooks.maybeTriggerCompactionV2 with PiG's auto-compaction off) and
;;;; session_before_compact on PiG's exact cut at level 0 (engine.BeforeCompact,
;;;; hooks.handleBeforeCompactV2 and buildCompactionV2 over the OM package's hook). Level 1 is
;;;; not ported: a compaction that would make its request is refused before any side effect,
;;;; so the Go engine runs it. The harness's part of a compaction goes through the hook point in
;;;; compaction-harness.lisp.

(in-package "OM.SERVER")

;;; PiG's own compaction settings (config.ReadPiAutoCompaction, ReadPiCompactionTokens)

(defparameter +pig-default-reserve-tokens+ 16384 "config.PiGDefaultReserveTokens.")

(defun pig-settings-paths (c cwd trust)
  "The settings files PiG merges: the global one, then the project's when it is trusted."
  (multiple-value-bind (agent-dir dir-name) (conn-paths c)
    (append (list (format nil "~A/settings.json" agent-dir))
            (when (eq trust :trusted)
              (list (format nil "~A/~A/settings.json" (string-right-trim "/" cwd) dir-name))))))

(defun pig-compaction-object (path)
  (let ((v (om::jget (read-json-file path) "compaction"))) (and (hash-table-p v) v)))

(defun pi-auto-compaction-p (c cwd trust)
  "config.ReadPiAutoCompaction as LiveConfig applies it: the trusted project's
compaction.enabled, else the global one, else on."
  (dolist (path (reverse (pig-settings-paths c cwd trust)) t)
    (let ((v (om::jget (pig-compaction-object path) "enabled")))
      (when (member v '(:true :false)) (return (eq v :true))))))

(defun pi-compaction-token (v)
  "config.piCompactionToken: a non-negative whole number, else nil."
  (and (realp v) (>= v 0) (= v (ftruncate v)) (<= v (1- (expt 2 53))) (truncate v)))

(defun reserve-tokens-for (c cwd trust provider id)
  "config.PiCompactionTokens.ReserveTokensFor over ReadPiCompactionTokens: the model's
modelOverrides entry, else compaction.reserveTokens, else PiG's default; the project's file
over the global one, field by field."
  (let ((reserve nil) (overrides (make-hash-table :test 'equal)))
    (dolist (path (pig-settings-paths c cwd trust))
      (let ((compaction (pig-compaction-object path)))
        (when compaction
          (let ((n (pi-compaction-token (om::jget compaction "reserveTokens")))) (when n (setf reserve n)))
          (let ((o (om::jget compaction "modelOverrides")))
            (when (hash-table-p o)
              (maphash (lambda (key value)
                         (let ((n (pi-compaction-token (om::jget value "reserveTokens"))))
                           (when n (setf (gethash key overrides) n))))
                       o))))))
    (or (and (or (string/= provider "") (string/= id ""))
             (gethash (concatenate 'string provider "/" id) overrides))
        reserve
        +pig-default-reserve-tokens+)))

;;; The threshold (hooks.ReadV2Threshold)

(defun read-threshold (c cfg)
  "PiG's threshold inputs: (values tokens window reserve), tokens nil while the usage is
unknown, window the reported one else the session model's (0 unknown)."
  (let* ((model (session-model))
         (window (model-int model "contextWindow"))
         (reserve (reserve-tokens-for c (scope-cwd) (rcfg-trust cfg) (model-string model "provider") (model-id model)))
         (tokens nil))
    (handler-case
        (let ((usage (om::jget (host-call "host/getContextUsage") "usage")))
          (when (hash-table-p usage)
            (let ((w (om::jget usage "contextWindow"))) (when (and (realp w) (> w 0)) (setf window (truncate w))))
            (let ((n (om::jget usage "tokens"))) (when (realp n) (setf tokens (truncate n))))))
      (request-cancelled (e) (error e))
      (error () nil))
    (values tokens window reserve)))

(defun threshold-exceeded-p (tokens window reserve)
  "V2Threshold.Exceeded: context > window - reserveTokens, false while either is unknown."
  (and tokens (> window 0) (> tokens (- window reserve))))

;;; The trigger's state (runtime.CompactInFlight, CompactionBackoff)

(defstruct (backoff (:conc-name backoff-)) (identity "") (boundary "") (reason "") (tip ""))

(defun try-start-compact (c)
  "runtime.TryStartCompact: claims the connection's compaction slot; nil when it is taken."
  (sb-thread:with-mutex ((conn-mutex c))
    (unless (conn-compact-in-flight c) (setf (conn-compact-in-flight c) t))))

(defun set-compact-in-flight (c v) (sb-thread:with-mutex ((conn-mutex c)) (setf (conn-compact-in-flight c) v)))
(defun compact-in-flight-p (c) (sb-thread:with-mutex ((conn-mutex c)) (conn-compact-in-flight c)))
(defun set-backoff (c b) (sb-thread:with-mutex ((conn-mutex c)) (setf (conn-backoff-tip c) b)))

(defun leaf-id (entries) (if (plusp (length entries)) (om::entry-id (aref entries (1- (length entries)))) ""))

(defun latest-compaction-id (entries)
  (let ((i (om::find-last-compaction-index entries))) (if (>= i 0) (om::entry-id (aref entries i)) "")))

(defun active-suspension (c entries)
  "hooks.ActiveV2CompactionSuspension: the backoff that holds on this branch, or nil: a
\"Nothing to compact\" answer holds until the branch tip changes."
  (let ((b (sb-thread:with-mutex ((conn-mutex c)) (conn-backoff-tip c))))
    (and b (string/= (backoff-tip b) "")
         (string= (backoff-identity b) (session-identity))
         (string= (backoff-tip b) (leaf-id entries))
         b)))

(defun nothing-to-compact-p (message)
  "hooks.nothingToCompact: a refusal asking again at the same tip cannot change."
  (some (lambda (prefix) (and (>= (length message) (length prefix)) (string= prefix message :end2 (length prefix))))
        '("Nothing to compact" "Already compacted" "Invalid compaction.")))

;;; The trigger (hooks.MaybeTriggerCompaction, maybeTriggerCompactionV2)

(defparameter *idle-recheck-attempts* 10 "hooks.IdleRecheckAttempts.")
(defparameter *idle-recheck-seconds* 0.1 "hooks.IdleRecheckDelay.")

(defun wait-for-idle ()
  "hooks.waitForIdle: polls host/isIdle within the bounds above."
  (loop for attempt from 0 below (max 1 *idle-recheck-attempts*)
        do (let ((idle (handler-case (eq (om::jget (host-call "host/isIdle") "value") :true)
                         (error () (return nil)))))
             (when idle (return t))
             (when (< (1+ attempt) *idle-recheck-attempts*) (sleep *idle-recheck-seconds*)))
        finally (return nil)))

(defun deferred-compaction (c cfg identity)
  "maybeTriggerCompactionV2's deferred step: idle, still over the threshold, then host/compact,
whose answer comes once PiG's compaction has finished (it runs session_before_compact meanwhile,
with the slot held: the extension's own compaction)."
  (unless (wait-for-idle)
    (set-compact-in-flight c nil)
    (notify-ui "Observational memory: compaction deferred — agent became busy before compaction" "info")
    (return-from deferred-compaction))
  (let ((entries (handler-case (get-branch c)
                   (error () (set-compact-in-flight c nil) (return-from deferred-compaction)))))
    (unless (multiple-value-call #'threshold-exceeded-p (read-threshold c cfg))
      (set-compact-in-flight c nil)
      (notify-ui "Observational memory: compaction skipped — the context is no longer over PiG's threshold" "info")
      (return-from deferred-compaction))
    (let ((tip (leaf-id entries)))
      (flet ((failed (message)
               (when (nothing-to-compact-p message)
                 (set-backoff c (make-backoff :identity identity :boundary (latest-compaction-id entries)
                                              :reason message :tip tip)))
               (set-compact-in-flight c nil)
               (notify-ui (concatenate 'string "Observational memory: " message) "error")))
        (handler-case
            (progn (host-call "host/compact")
                   (set-backoff c nil)
                   (set-compact-in-flight c nil)
                   (notify-ui "Observational memory: compaction complete" "info"))
          (host-call-error (e) (failed (host-call-error-message e)))
          (peer-closed () (failed "enginerpc: connection closed")))))))

(defvar *trigger-threads* '() "The deferred trigger steps started, newest first (tests wait on them).")

(defun start-deferred-compaction (c cfg identity)
  (let ((peer *peer*) (scope *scope*))
    (let ((thread (sb-thread:make-thread
                   (lambda ()
                     (let ((*peer* peer) (*scope* scope) (*request* nil))
                       (handler-case (deferred-compaction c cfg identity)
                         (error (e)
                           (set-compact-in-flight c nil)
                           (notify-ui (format nil "Observational memory: compact threw: ~A" e) "error")))))
                   :name "om compaction trigger")))
      (sb-thread:with-mutex ((conn-mutex c))
        (setf *trigger-threads* (cons thread (remove-if-not #'sb-thread:thread-alive-p *trigger-threads*))))
      thread)))

(defun maybe-trigger-compaction (c)
  "hooks.MaybeTriggerCompaction: with PiG's auto-compaction off, asks PiG to compact when the
reported context exceeds window - reserveTokens (read under the configuration's trust), never
while the usage is unknown, never while a \"Nothing to compact\" answer holds at this tip. The
deferred step runs in a thread of its own; the event answers at once. Returns that thread, or
nil when nothing was scheduled."
  (ensure-config c)
  (let ((cfg (conn-cfg c)))
    (when (or (rcfg-passive cfg) (pi-auto-compaction-p c (scope-cwd) (rcfg-trust cfg)) (not (try-start-compact c)))
      (return-from maybe-trigger-compaction nil))
    (let ((deferred nil))
      (unwind-protect
           (let ((entries (handler-case (get-branch c) (error () (return-from maybe-trigger-compaction nil)))))
             (when (active-suspension c entries) (return-from maybe-trigger-compaction nil))
             (multiple-value-bind (tokens window reserve) (read-threshold c cfg)
               (unless (threshold-exceeded-p tokens window reserve) (return-from maybe-trigger-compaction nil))
               (set-backoff c nil)
               (let ((identity (session-identity)))
                 (notify-ui (format nil "Observational memory: context ~~~A tokens is over PiG's threshold ~A (window - reserveTokens) and PiG's auto-compaction is off; compacting (level 0)"
                                    (om::locale-int tokens) (om::locale-int (- window reserve)))
                            "info")
                 (setf deferred t)
                 (start-deferred-compaction c cfg identity))))
        (unless deferred (set-compact-in-flight c nil))))))

(define-method "event/agentSettled" (params)
  ;; Server.handle: hooks.OnAgentSettledEvent (the trigger), then engine.SettleCurated.
  (with-event (c params)
    (maybe-trigger-compaction c)
    (settle-curated-pig c)
    (om::jobject)))

;;; session_before_compact (engine.BeforeCompact, DecodeBeforeCompact)

(defun string-list (v)
  (if (and (vectorp v) (not (stringp v))) (loop for x across v when (stringp x) collect x) '()))

(defun message-list (v)
  (if (and (vectorp v) (not (stringp v))) (loop for x across v when (hash-table-p x) collect x) '()))

(defun whole-number (v) (if (and (realp v) (> v 0)) (truncate v) 0))

(defun decode-before-compact (data)
  "engine.DecodeBeforeCompact: PiG's payload as the hook's event (a missing or malformed member
stays empty)."
  (let* ((prep (let ((p (om::jget data "preparation"))) (and (hash-table-p p) p)))
         (ops (om::jget prep "fileOps"))
         (settings (om::jget prep "settings"))
         (branch (om::jget data "branchEntries")))
    (om::make-cevent
     :first-kept (om::str (om::jget prep "firstKeptEntryId"))
     :tokens-before (let ((n (om::jget prep "tokensBefore"))) (if (realp n) (truncate n) 0))
     :branch (if (and (vectorp branch) (not (stringp branch)))
                 (coerce (loop for e across branch when (hash-table-p e) collect (om::entry-from-map e)) 'simple-vector)
                 #())
     :reason (om::str (om::jget data "reason"))
     :custom-instructions (om::str (om::jget data "customInstructions"))
     :messages-to-summarize (message-list (om::jget prep "messagesToSummarize"))
     :turn-prefix (message-list (om::jget prep "turnPrefixMessages"))
     :split-turn (eq (om::jget prep "isSplitTurn") :true)
     :previous-summary (om::str (om::jget prep "previousSummary"))
     :file-ops (append (mapcar (lambda (p) (list p "read")) (string-list (om::jget ops "read")))
                       (mapcar (lambda (p) (list p "written")) (string-list (om::jget ops "written")))
                       (mapcar (lambda (p) (list p "edited")) (string-list (om::jget ops "edited"))))
     :reserve (whole-number (om::jget settings "reserveTokens")))))

(defparameter +v2-fallback-summary+
  (concatenate 'string "omitted: the compacted conversation; the memory tool searches it ("
               om::+snippet-history-search+ ")."))

(defun recovered-compaction-json (event reason &optional (skip-tools (list om::+memory-tool+)) consolidation usage)
  "hooks.v2RecoveredCompaction with the report's reason: the minimal brief of the cut range in
its pointer form, bounded by the range, else the fallback summary."
  (let ((summary +v2-fallback-summary+))
    (ignore-errors
     (let* ((cut (om::source-entries (om::cut-range (om::cevent-branch event) event)))
            (removed (+ (om::source-tokens cut) (om::estimate-tokens (om::previous-summary (om::cevent-branch event) event))))
            (brief (om::minimal-brief cut skip-tools 0 removed)))
       (when (and (stringp brief) (string/= brief "")) (setf summary brief))))
    (om::cresult-map-json
     (om::make-cresult :summary summary :first-kept (om::cevent-first-kept event)
                       :tokens-before (om::cevent-tokens-before event)
                       :usage usage
                       :details-json (om::details-json '() '() '() (om::cevent-first-kept event)
                                                       (or consolidation (om::consolidation-json reason)) '())))))

(defun pig-hook-config (cfg)
  "The hook's settings (om::hconfig) from the connection's configuration."
  (om::make-hconfig :passive (rcfg-passive cfg) :consolidate-off (rcfg-consolidate-off cfg)
                    :summary-max (rcfg-summary-max cfg) :memory-format (rcfg-memory-format cfg)))

(defun pig-hook-host (c)
  "The hook's host over engine-rpc: the branch through the cache, appends through
host/appendEntry (a failed append is logged and dropped, as Go drops its error)."
  (om::make-hhost
   :read (lambda () (get-branch c))
   :append (lambda (type data-json)
             (handler-case (host-call "host/appendEntry" "customType" type "data" (om::raw-json data-json))
               (error (e) (log-line "compaction: append ~A: ~A" type e)))
             "")
   :cwd (scope-cwd)
   :skip-tools (conn-skip-tools c)))

(define-method "event/sessionBeforeCompact" (params)
  ;; engine.BeforeCompact: always a compaction on PiG's exact cut, never a decline or a
  ;; cancel; an error anywhere after the refusals is recovered into the minimal compaction; only
  ;; the request's own cancellation ($/cancelRequest, which crosses to the host call in flight)
  ;; answers no result, as Go's handler returns nil for a cancelled request.
  (with-event (c params)
   (block answer-before-compact
    (ensure-config c)
    (let* ((cfg (conn-cfg c))
           (thinning (if (rcfg-passive cfg) :false :true))
           (data (om::jget params "data")))
      (flet ((answer (result) (om::jobject "result" result "thinning" thinning)))
        (when (cancelled-p) (return-from answer-before-compact (answer :null)))
        (let* ((event (decode-before-compact data))
               (model (session-model))
               (store (if (rcfg-curated-memory cfg)
                          (let ((home (host-string "host/configHome"))) (if (string/= home "") (store-dir-of home) ""))
                          ""))
               (own (compact-in-flight-p c))
               (ch (make-compaction-harness :conn c :model model :model-key (model-key-of model)
                                            :store-dir store :cfg cfg :event event)))
          (let ((refusal (compaction-harness-refusal ch)))
            (when refusal (not-ported refusal)))
          (handler-case
              (multiple-value-bind (result entries cut)
                  (sb-thread:with-mutex ((conn-extract-mutex c))
                    (let ((hh (harness-hook-host ch (pig-hook-host c))))
                      (om::build-compaction hh event (pig-hook-config cfg)
                                            :model model :own-trigger own
                                            :context-window (model-int model "contextWindow")
                                            :max-output (model-max-tokens model)
                                            :level1 (lambda (entries cut budget)
                                                      (let ((r (consolidate-v2 c hh cfg event entries cut budget
                                                                               :model model :store store :own own
                                                                               :on-start (lambda (r) (setf (ch-level1 ch) r)))))
                                                        (values (l1-entries r) (report-applied (l1-report r))
                                                                (report-json (l1-report r)) (l1-usage r)))))))
                (check-cancelled)
                (setf (ch-result ch) result (ch-entries ch) entries (ch-cut ch) cut)
                (handler-case (after-compaction ch)
                  (error (e) (log-line "after compaction: ~A" e)))
                (answer (om::jobject "compaction" (om::raw-json (om::cresult-map-json result)))))
            (error (e)
              (cond ((cancelled-p) (answer :null))
                    ((string= (om::cevent-first-kept event) "") (error e))
                    (t (log-line "compaction recovered: ~A" e)
                       ;; A request made before the error was paid for: the report and usage stand.
                       (let* ((spent (ch-level1 ch)) (rep (and spent (l1-report spent))))
                         (when (and rep (not (report-applied rep)) (string= (report-reason rep) ""))
                           (setf (report-reason rep) (format nil "panic: ~A" e)))
                         (answer (om::jobject "compaction"
                                              (om::raw-json (recovered-compaction-json event (format nil "panic: ~A" e) (conn-skip-tools c)
                                                                                       (and rep (report-json rep))
                                                                                       (and spent (l1-usage spent)))))))))))))))))
