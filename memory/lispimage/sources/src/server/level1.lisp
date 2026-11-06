;;;; Level 1 on PiG (memory/hooks/consolidate_v2.go consolidateV2, appendLevel1, curatedInput,
;;;; recordCitations, requestOverheadTokens; hooks/definitions_v2.go definitionOfferV2 and
;;;; holdDefinitionsV2; runtime.ResolveSessionModel and resolveCandidate; hooks.sessionHeadersFor;
;;;; agents.RunSingleRequest, Request and logRequest): at most one request, on the session model,
;;;; made in place of PiG's summarizer while the compaction's request is live, through PiG's model
;;;; stream (models/stream, then models/streamResult, whose only deadlines are cancellation and
;;;; connection loss). Any failure appends nothing. The request rule holds by construction: this
;;;; file has the image's only models/stream call (run-single-request), it runs only inside
;;;; event/sessionBeforeCompact, and each run makes at most one call and logs one request event.
;;;;
;;;; Decision 5: the offer is the consolidation instructions only (om::offer); what level 1 defines
;;;; is held in the Go layer (om.golayer:hold-pass), credited in the compaction's ranHeld while held
;;;; and settled by SettleV2 at a later compaction (after-compaction).

(in-package "OM.SERVER")

(defmacro push-end-list (item place) `(setf ,place (append ,place (list ,item))))

;;; The request event (debuglog.RequestEvent, agents.logRequest)

(defvar *request-observers* '()
  "Functions of (event data) called for every model request event, as debuglog.Subscribe's
observers are (tests count requests with them).")

(defvar *request-observers-mutex* (sb-thread:make-mutex :name "om request observers"))

(defun log-request-event (stage model message aborted)
  "logRequest: one event per request, model.request or <stage>.stream_error, data with
\"request\": true."
  (let ((data (om::jobject "request" :true "stage" stage
                           "model" (concatenate 'string (model-string model "provider") "/" (model-id model))))
        (event "model.request"))
    (when aborted (setf (gethash "aborted" data) :true))
    (when message
      (let ((stop (om::str (om::jget message "stopReason"))))
        (setf (gethash "stopReason" data) stop)
        (when (member stop '("error" "aborted") :test #'string=)
          (setf event (concatenate 'string stage ".stream_error")
                (gethash "errorMessage" data) (om::str (om::jget message "errorMessage"))))
        (let ((usage (om::jget message "usage")))
          (when (hash-table-p usage)
            (loop for (key . field) in '(("input" . "inputTokens") ("output" . "outputTokens")
                                         ("cacheRead" . "cacheReadTokens") ("cacheWrite" . "cacheWriteTokens"))
                  for n = (om::usage-number (om::jget usage key))
                  when n do (setf (gethash field data) n))))))
    (log-line "~A ~A" event (om::json-string data))
    (dolist (f (sb-thread:with-mutex (*request-observers-mutex*) (copy-list *request-observers*)))
      (ignore-errors (funcall f event data)))))

(defmacro with-request-observer ((fn) &body body)
  `(let ((f ,fn))
     (sb-thread:with-mutex (*request-observers-mutex*) (push f *request-observers*))
     (unwind-protect (progn ,@body)
       (sb-thread:with-mutex (*request-observers-mutex*) (setf *request-observers* (remove f *request-observers*))))))

;;; The session model (runtime.ResolveSessionModel, resolveCandidate, sessionHeadersFor)

(defstruct resolved model api-key headers env)

(defvar *recheck-times* (make-hash-table :test 'equal) "Providers' last credential recheck (seconds).")
(defvar *recheck-mutex* (sb-thread:make-mutex :name "om credential recheck"))

(defun host-bool (method model)
  (handler-case (eq (om::jget (host-call method "model" model) "value") :true)
    (request-cancelled (e) (error e))
    (error () nil)))

(defun recheck-provider-credential (model provider)
  "recheckProviderCredential: a scoped refresh without network (at most once a minute per
provider), then the configured-auth snapshot again."
  (let ((now (now-seconds)))
    (sb-thread:with-mutex (*recheck-mutex*)
      (let ((last (gethash provider *recheck-times*)))
        (when (and last (< (- now last) 60)) (return-from recheck-provider-credential nil)))
      (setf (gethash provider *recheck-times*) now)))
  (handler-case (sb-sys:with-deadline (:seconds 5)
                  (host-call "models/refresh" "allowNetwork" :false "providers" (vector provider)))
    (request-cancelled (e) (error e))
    (error () nil))
  (host-bool "models/hasConfiguredAuth" model))

(defun usable-headers (headers)
  (and (hash-table-p headers) (loop for v being the hash-values of headers thereis (and (stringp v) (string/= v "")))))

(defun resolve-session-model (model)
  "ResolveSessionModel: (values resolved nil) or (values nil reason)."
  (when (null model) (return-from resolve-session-model (values nil "no session model")))
  (let* ((auth (handler-case (om::jget (host-call "models/getApiKeyAndHeaders" "model" model) "auth")
                 (request-cancelled (e) (error e))
                 (error (e) (om::jobject "ok" :false "error" (princ-to-string e)))))
         (auth (if (hash-table-p auth) auth (om::jobject)))
         (ok (eq (om::jget auth "ok") :true))
         (key (om::jget auth "apiKey"))
         (headers (let ((h (om::jget auth "headers"))) (and (hash-table-p h) h)))
         (provider (let ((p (model-string model "provider"))) (if (string= p "") "unknown" p)))
         (oauth (host-bool "models/isUsingOAuth" model))
         (usable (or (and (stringp key) (string/= key "")) (usable-headers headers)))
         (empty-key (and (stringp key) (string= key "")))
         (configured (host-bool "models/hasConfiguredAuth" model)))
    (when (and ok (not usable) (not oauth) (not empty-key) (not configured))
      (setf configured (recheck-provider-credential model provider)))
    (let ((signs (and ok (not oauth) (not empty-key) configured)))
      (when (or (not ok) (and (not usable) (not signs)))
        (return-from resolve-session-model
          (values nil (if oauth
                          (format nil "authentication failed for provider ~A — OAuth credentials may have expired; run '/login ~A' to re-authenticate"
                                  (om::go-quote provider) provider)
                          (format nil "no API key or auth headers for provider ~A" (om::go-quote provider)))))))
    (let ((request-model model) (base (om::str (om::jget auth "baseUrl"))))
      (when (string/= base "")
        (setf request-model (om::jobject))
        (maphash (lambda (k v) (setf (gethash k request-model) v)) model)
        (setf (gethash "baseUrl" request-model) base))
      (let ((env (let ((e (om::jget auth "env")))
                   (when (hash-table-p e)
                     (let ((out (om::jobject)))
                       (maphash (lambda (k v) (when (stringp v) (setf (gethash k out) v))) e)
                       out)))))
        (values (make-resolved :model request-model :api-key (and (stringp key) key) :headers headers :env env) nil)))))

(defun session-headers-for (r)
  "sessionHeadersFor: Pi's own session headers on a request to Console Go."
  (let* ((model (resolved-model r)) (provider (model-string model "provider")) (base (model-string model "baseUrl")))
    (when (or (member provider '("opencode" "opencode-go") :test #'string=) (search "opencode.ai" base))
      (let ((id (host-string "host/sessionId")))
        (when (string/= id "")
          (let ((headers (om::jobject)))
            (when (resolved-headers r) (maphash (lambda (k v) (setf (gethash k headers) v)) (resolved-headers r)))
            (setf (gethash "x-opencode-session" headers) id (gethash "x-opencode-client" headers) "pi"
                  (resolved-headers r) headers)))))
    r))

;;; One request (agents.RunSingleRequest, Request)

(defun forced-tool-choice (model name)
  (let ((api (model-string model "api")))
    (cond ((member api '("anthropic-messages" "bedrock-converse-stream") :test #'string=) (om::jobject "type" "tool" "name" name))
          ((member api '("openai-completions" "openai-responses" "azure-openai-responses" "openai-codex-responses" "mistral-conversations") :test #'string=) "required")
          ((member api '("google-generative-ai" "google-vertex") :test #'string=) "any"))))

(defstruct l1answer arguments message usage stream-error (made nil) (error nil))

(defun now-unix-millis () (floor (om.layer::time-nanos (om.layer::now)) 1000000))

(defun tool-arguments (call)
  (let ((args (om::jget call "arguments")))
    (cond ((hash-table-p args) args)
          ((stringp args) (let ((v (ignore-errors (om::json-parse args)))) (if (hash-table-p v) v (om::jobject))))
          (t (om::jobject)))))

(defun run-single-request (r max-tokens system user-text name description parameters)
  "RunSingleRequest over PiG's model stream: one forced tool, one response, nothing retried.
error is :aborted (the request's cancel), or a message."
  (when (cancelled-p) (return-from run-single-request (make-l1answer :error :aborted)))
  (let* ((model (resolved-model r))
         (limit (model-max-tokens model))
         (request (om::jobject "systemPrompt" (om::single-request-system system name)
                               "messages" (vector (om::jobject "role" "user"
                                                               "content" (vector (om::jobject "type" "text" "text" user-text))
                                                               "timestamp" (now-unix-millis)))
                               "tools" (vector (om::tool-schema name description parameters))
                               "maxTokens" (if (and (> limit 0) (< limit max-tokens)) limit max-tokens))))
    (when (resolved-api-key r) (setf (gethash "apiKey" request) (resolved-api-key r)))
    (when (and (resolved-headers r) (plusp (hash-table-count (resolved-headers r))))
      (setf (gethash "headers" request) (resolved-headers r)))
    (when (and (resolved-env r) (plusp (hash-table-count (resolved-env r))))
      (setf (gethash "env" request) (resolved-env r)))
    (let ((choice (forced-tool-choice model name))) (when choice (setf (gethash "toolChoice" request) choice)))
    (let ((stream (handler-case (om::str (om::jget (host-call "models/stream" "model" model "request" request) "stream"))
                    (request-cancelled (e) (error e))
                    (error () ""))))
      (when (string= stream "") (return-from run-single-request (make-l1answer :error "model stream unavailable")))
      (let ((message (handler-case (let ((m (om::jget (host-call "models/streamResult" "stream" stream) "message")))
                                     (and (hash-table-p m) m))
                       (request-cancelled (e) (error e))
                       (error () nil))))
        (when (cancelled-p)
          (log-request-event om::+consolidate-stage+ model nil t)
          (return-from run-single-request (make-l1answer :made t :error :aborted)))
        (unless message
          (setf message (om::jobject "role" "assistant" "content" (vector) "stopReason" "error"
                                     "errorMessage" "model stream ended without a message")))
        (unless (nth-value 1 (gethash "role" message)) (setf (gethash "role" message) "assistant"))
        (log-request-event om::+consolidate-stage+ model message nil)
        (let ((a (make-l1answer :message message :made t))
              (stop (om::str (om::jget message "stopReason"))))
          (when (hash-table-p (om::jget message "usage")) (setf (l1answer-usage a) (om::jget message "usage")))
          (if (member stop '("error" "aborted") :test #'string=)
              (let ((err (om::str (om::jget message "errorMessage"))))
                (setf (l1answer-stream-error a)
                      (format nil "~A stream ended with stopReason ~A~@[: ~A~]" om::+consolidate-stage+ (om::go-quote stop)
                              (and (string/= err "") err))))
              (let ((content (om::jget message "content")))
                (when (and (vectorp content) (not (stringp content)))
                  (loop for block across content
                        when (and (hash-table-p block) (equal (om::jget block "type") "toolCall")
                                  (equal (om::jget block "name") name))
                          do (setf (l1answer-arguments a) (tool-arguments block)) (return)))))
          a)))))

;;; The report (ledger.ConsolidationReport)

(defstruct report (requested nil) (applied nil) (reason "") (records 0) (merges 0) (reflections 0)
  (aliases 0) (dropped 0) (refused 0) (confirmed 0) (rejected 0) (cited 0) (promoted 0)
  (harness-offered nil) (defined 0) (pending '()))

(defun report-json (r)
  (with-output-to-string (out)
    (format out "{\"requested\":~A,\"applied\":~A" (if (report-requested r) "true" "false") (if (report-applied r) "true" "false"))
    (when (string/= (report-reason r) "") (write-string ",\"reason\":" out) (om::write-json-string (report-reason r) out))
    (loop for (key . n) in (list (cons "records" (report-records r)) (cons "merges" (report-merges r))
                                 (cons "reflections" (report-reflections r)) (cons "aliases" (report-aliases r))
                                 (cons "dropped" (report-dropped r)) (cons "refused" (report-refused r))
                                 (cons "confirmed" (report-confirmed r)) (cons "rejected" (report-rejected r))
                                 (cons "cited" (report-cited r)) (cons "promoted" (report-promoted r)))
          when (/= n 0) do (format out ",\"~A\":~D" key n))
    (when (report-harness-offered r) (write-string ",\"harnessOffered\":true" out))
    (when (/= (report-defined r) 0) (format out ",\"defined\":~D" (report-defined r)))
    (when (report-pending r) (write-string ",\"pending\":" out) (om::write-json (coerce (report-pending r) 'simple-vector) out))
    (write-char #\} out)))

;;; Level 1's result (level1Result)

(defstruct l1 (entries '()) usage (report (make-report)) counts (answered nil))

(defun level1-held-snapshot (store model) (om.golayer:held-instructions-snapshot store model))

(defun level1-held-id (d) (om.golayer:instructions-held-id d))

(defun settle-level1-held (store model snapshot) (om.golayer:settle-held-instructions store model snapshot))

;;; Appending (appendLevel1)

(defun append-level1 (h result drop covers)
  (let ((queue '()))
    (when (om::vresult-records result)
      (push-end-list (cons "om.observations.recorded"
                           (format nil "{\"observations\":[~{~A~^,~}],\"coversUpToId\":~A,\"consolidated\":true}"
                                   (mapcar #'om::obs-json (om::vresult-records result)) (om::json-string covers)))
                     queue))
    (when (om::vresult-reflections result)
      (push-end-list (cons "om.reflections.recorded"
                           (format nil "{\"reflections\":[~{~A~^,~}],\"coversUpToId\":~A}"
                                   (mapcar #'om::refl-json (om::vresult-reflections result)) (om::json-string covers)))
                     queue))
    (when (om::vresult-aliases result)
      (push-end-list (cons "om.subjects.aliased"
                           (format nil "{\"aliases\":[~{~A~^,~}]~@[,\"observationsThroughId\":~A~]}"
                                   (mapcar #'om::alias-json (subseq (om::vresult-aliases result) 0
                                                                    (min (length (om::vresult-aliases result)) om::+max-subject-aliases-per-entry+)))
                                   (and (string/= covers "") (om::json-string covers))))
                     queue))
    (when drop
      (push-end-list (cons "om.observations.dropped"
                           (format nil "{\"observationIds\":~A,\"coversUpToId\":~A,\"consolidated\":true}"
                                   (om::json-string (coerce drop 'simple-vector)) (om::json-string covers)))
                     queue))
    (loop for (type . data) in queue for n from 0
          do (funcall (om::hhost-append h) type data)
          collect (om::synthetic-entry (format nil "om-v2-level1-~D" n) type data))))

;;; The curated input and the citations (curatedInput, recordCitations)

(defun curated-input (store project session)
  (when (string/= store "")
    (let ((memories (handler-case (om.layer::list-as store project session) (error () (return-from curated-input nil)))))
      (loop for m in (om.layer::curated-rank memories (handler-case (om.layer::curated-usage store) (error () nil)))
            collect (om::make-cmem :name (om.layer::mem-name m) :type (om.layer::mem-type m)
                                     :description (om.layer::mem-description m) :staged (om.layer::mem-staged m))))))

(defun record-citations (store project entries cited)
  "recordCitations: (values recorded promoted)."
  (let ((recorded 0))
    (dolist (c cited)
      (handler-case (progn (om.layer::append-usage store (car c) "cited" :record (om::join (cdr c) " ")) (incf recorded))
        (error (e) (log-line "level 1: cite ~A: ~A" (car c) e))))
    (multiple-value-bind (promoted flagged)
        (handler-case (om.layer::curated-settle store project (om.layer::branch-corrections entries))
          (error (e) (log-line "level 1: settle: ~A" e) (values '() '())))
      (dolist (notice (om.layer::settled-notices promoted flagged)) (host-notify notice "info"))
      (values recorded (length promoted)))))

;;; The offer and the holds (definitionOfferV2, holdDefinitionsV2)

(defun definition-offer (store model)
  "definitionOfferV2 under decision 5: (values offer evidence), nil without a store or while
nothing is due."
  (when (and (string/= store "") (string/= model ""))
    (multiple-value-bind (evidence due) (handler-case (om.golayer:proposal-due store model)
                                          (error (e) (log-line "level 1: proposal check: ~A" e) (values nil nil)))
      (when due
        (values (om::make-offer :evidence (om.golayer:harness-evidence-text evidence store model)) evidence)))))

(defun hold-definitions (store model entries result evidence)
  "holdDefinitionsV2 for the instructions level 1 defined: (values held refused)."
  (let ((defs '()) (refused 0) (held 0) (session (host-string "host/sessionId")))
    (flet ((refuse (what message)
             (incf refused)
             (log-line "level 1: definition ~A refused: ~A" what message)
             (notify-ui (format nil "Observational memory: level-1 definition ~A for ~A refused: ~A" what model message) "warning")))
      (dolist (p (om::vresult-definitions result))
        (destructuring-bind (kind name text) p
          (let ((refusal (om.golayer:check-level1-definition kind name text model entries)))
            (if refusal
                (refuse (concatenate 'string kind "/" name) refusal)
                (push-end-list (om.layer::make-gdef :kind kind :name name :model model :text text :session session) defs)))))
      (when (cancelled-p) (return-from hold-definitions (values 0 refused)))
      (multiple-value-bind (holds refusals)
          (handler-case (om.golayer:hold-pass store model defs evidence session)
            (error (e) (log-line "level 1: proposal mark: ~A" e) (values '() '())))
        (loop for d in defs for r in refusals
              when r do (refuse (concatenate 'string (om.layer::gdef-kind d) "/" (om.layer::gdef-name d)) r))
        (dolist (d holds)
          (incf held)
          (notify-ui (format nil "Observational memory: ~A defined ~A/~A in level 1 (~A); held until a later compaction proves it"
                             model (om.layer::gdef-kind d) (om.layer::gdef-name d) (om.golayer:relation d))
                     "info"))))
    (values held refused)))

;;; consolidateV2

(defparameter +level1-cancelled+ "the compaction was cancelled before the request")

(defun level1-skip (cfg event entries cut own)
  "consolidateV2's first skip reasons, or nil."
  (cond ((rcfg-consolidate-off cfg) "consolidate is false")
        ((rcfg-passive cfg) "passive mode")
        (own "the extension's own compaction (PiG's auto-compaction is off) runs level 0 only")
        ((string= (om::cevent-reason event) "overflow") "an overflow compaction runs level 0 only, so recovery is not delayed")
        ((or (null cut) (< (om::entry-index-for-id entries (om::cevent-first-kept event)) 0)) "the cut range is not on the branch")
        ((cancelled-p) +level1-cancelled+)))

(defun cites-any-p (ids set) (some (lambda (id) (gethash id set)) ids))

(defun consolidate-v2 (c h cfg event entries cut budget &key model store own on-start)
  "consolidateV2: the l1 result; on-start receives it first, so an error after the request still
reports the request and its usage (spent)."
  (declare (ignore c))
  (let* ((out (make-l1)) (rep (l1-report out)))
    (when on-start (funcall on-start out))
    (flet ((skip (reason) (setf (report-reason rep) reason) (return-from consolidate-v2 out)))
      (let ((reason (level1-skip cfg event entries cut own))) (when reason (skip reason)))
      (multiple-value-bind (resolved reason) (resolve-session-model model)
        (unless resolved (skip (concatenate 'string "the session model is unavailable: " reason)))
        (setf resolved (session-headers-for resolved))
        (let* ((rmodel (resolved-model resolved))
               (window (model-int rmodel "contextWindow"))
               (output-cap (let ((n (model-max-tokens rmodel))) (if (> n 0) n budget))))
          (when (<= window 0) (skip "the session model's context window is unknown"))
          (when (<= output-cap 0) (skip "no output cap: the model's output limit is unknown and the summary budget is 0"))
          (let* ((first-kept (om::cevent-first-kept event))
                 (projection (om::full-projection* entries first-kept))
                 (cut-ids (om::make-set)) (candidates '()) (active '()))
            (dolist (e cut) (setf (gethash (om::entry-id e) cut-ids) t))
            (let ((last (om::find-last-compaction-index entries)))
              (when (and (>= last 0) (< last (om::entry-index-for-id entries first-kept)))
                (setf (gethash (om::entry-id (aref entries last)) cut-ids) t)))
            (dolist (o (om::projection-observations projection))
              (if (and (member (om::obs-origin o) '("code" "host") :test #'string=) (cites-any-p (om::obs-source-ids o) cut-ids))
                  (push-end-list o candidates)
                  (push-end-list o active)))
            (unless candidates (skip "no level-0 records in the cut range"))
            (let* ((folded (om::fold-ledger entries))
                   (key (model-key-of model))
                   (project (om.layer::project-root (scope-cwd))))
              (multiple-value-bind (offer evidence) (definition-offer store key)
                (multiple-value-bind (name description parameters) (om::consolidate-tool offer)
                  (let* ((system (om::consolidate-system-prompt (om.golayer:consolidation-instructions store key)))
                         (fixed (om::request-overhead-tokens system name description parameters)))
                    (multiple-value-bind (prompt ok)
                        (om::build-prompt (om::make-pin* :cut cut :skip-tools (om::hhost-skip-tools h) :candidates candidates
                                                         :active active
                                                         :reflections (om::active-reflections (om::projection-reflections projection))
                                                         :instructions (om::cevent-custom-instructions event)
                                                         :available (- window 4096 output-cap fixed)
                                                         :memory-budget (max 0 budget) :branch entries :folded folded
                                                         :curated (curated-input store project (host-string "host/sessionId"))
                                                         :harness offer))
                      (when (or (not ok) (> (+ fixed (om::prompt-tokens prompt) output-cap 4096) window))
                        (skip "the input does not fit: est(prompt + input) + output cap + PiG's 4096 safety margin exceeds the window"))
                      (when (cancelled-p) (skip +level1-cancelled+))
                      (let ((a (run-single-request resolved output-cap system (om::prompt-text prompt) name description parameters)))
                        (setf (report-requested rep) (l1answer-made a) (l1-usage out) (l1answer-usage a))
                        (flet ((fail (reason) (setf (report-reason rep) reason) (return-from consolidate-v2 out)))
                          (cond ((and (eq (l1answer-error a) :aborted) (l1answer-made a)) (fail "aborted: the compaction was cancelled during the request"))
                                ((eq (l1answer-error a) :aborted) (fail +level1-cancelled+))
                                ((l1answer-error a) (fail (l1answer-error a)))
                                ((l1answer-stream-error a) (fail (l1answer-stream-error a)))
                                ((equal (om::jget (l1answer-message a) "stopReason") "length")
                                 (setf (l1-counts out) (list (cons "level1-failed" 1)) (l1-answered out) t)
                                 (fail "the response reached the output cap (stopReason length)")))
                          (let* ((shown-offer (and offer (om::prompt-harness-shown prompt) offer))
                                 (in (om::make-vinput :candidates (om::prompt-candidates prompt) :active (om::prompt-active prompt)
                                                      :reflections (om::prompt-reflections prompt) :folded folded
                                                      :subjects (om::subjects-of (om::projection-observations projection))
                                                      :links (om::prompt-links prompt) :curated (om::prompt-curated prompt)
                                                      :branch entries :offer shown-offer))
                                 (result (om::validate-level1 (l1answer-arguments a) in)))
                            (setf (report-refused rep) (length (om::vresult-refusals result)) (l1-answered out) t)
                            (when (om::vresult-refusals result)
                              (log-line "level 1: refusals: ~A" (om::refusal-summary (om::vresult-refusals result))))
                            (when shown-offer
                              (setf (report-harness-offered rep) t
                                    (report-defined rep) (hold-definitions store key entries result evidence)))
                            (when (string/= (om::vresult-failure result) "")
                              (setf (l1-counts out) (om::level1-counts result 0))
                              (let ((f (assoc "level1-failed" (l1-counts out) :test #'string=)))
                                (if f (setf (cdr f) 1)
                                    (setf (l1-counts out) (sort (cons (cons "level1-failed" 1) (l1-counts out)) #'string< :key #'car))))
                              (fail (om::vresult-failure result)))
                            (multiple-value-bind (drop reflections kept) (om::level1-union result (om::projection-observations projection))
                              (setf (l1-counts out) (om::level1-counts result kept)
                                    (om::vresult-reflections result) reflections
                                    (l1-entries out) (append-level1 h result drop (om::entry-id (car (last cut))))
                                    (report-applied rep) t
                                    (report-records rep) (- (length (om::vresult-records result)) (om::vresult-merges result)
                                                            (om::vresult-link-records result))
                                    (report-confirmed rep) (om::vresult-confirmed result)
                                    (report-rejected rep) (om::vresult-rejected result)
                                    (report-merges rep) (om::vresult-merges result)
                                    (report-reflections rep) (length reflections)
                                    (report-aliases rep) (length (om::vresult-aliases result))
                                    (report-dropped rep) (length drop))
                              (when (and (string/= store "") (om::vresult-cited result))
                                (multiple-value-bind (cited promoted) (record-citations store project entries (om::vresult-cited result))
                                  (setf (report-cited rep) cited (report-promoted rep) promoted)))
                              out)))))))))))))))
