;;;; W6: the first PiG events over engine-rpc (docs/lisp-port-peer.md; docs/engine-rpc.md):
;;;; event/turnEnd (with the harness detectors, docs/lisp-port-evidence.md) and event/context,
;;;; with the host/* calls they make (event/sessionStart is in pig-session.lisp). Loaded after
;;;; methods.lisp and server/hooks.lisp, so each
;;;; define-method here replaces a not-ported entry. Every other engine-rpc method stays not
;;;; ported, and a ported one refuses (not ported) what it cannot do the way Go does, always
;;;; before its first host write, so the adapter can run the event on the Go engine instead.

(in-package "OM.SERVER")

;;; Turn end

(defun thinning-result (c)
  (om::jobject "thinning" (if (rcfg-passive (conn-cfg c)) :false :true)))

(defun host-append-extraction (batches covers &optional revisions)
  "hooks.appendExtraction over the host: one om.observations.recorded entry per batch with
records, then an om.extracted marker when the last batch stops short; the appends stop at the
first that fails. Returns how many landed."
  (let ((queue '()) (landed 0))
    (dolist (b batches)
      (when (and (om::batch-observations b) (string/= (om::batch-covers b) ""))
        (push (cons "om.observations.recorded" (recorded-data-json (om::batch-observations b) (om::batch-covers b)))
              queue)))
    (when revisions (push (cons "om.revisions.recorded" (om::revisions-data-json revisions)) queue))
    (when (and (string/= covers "") (or (null batches) (string/= (om::batch-covers (car (last batches))) covers)))
      (push (cons "om.extracted" (format nil "{\"coversUpToId\":~A}" (om::json-string covers))) queue))
    (dolist (p (nreverse queue) landed)
      (handler-case (progn (host-call "host/appendEntry" "customType" (car p) "data" (om::raw-json (om::redact-json-text (cdr p))))
                           (incf landed))
        (error (e)
          (log-line "turn end: append ~A: ~A" (car p) e)
          (return landed))))))

(defun host-structure (query)
  "host.StructureReader over the host (host/structure): the decoded answer to query, nil when the
host reads none or the call fails."
  (handler-case (let ((answer (om::jget (host-call "host/structure" "query" query) "answer")))
                  (and (hash-table-p answer) answer))
    (request-cancelled (e) (error e))
    (error () nil)))

(defun scoped (f)
  "f called under this request's peer, scope and request, from whichever thread calls it."
  (let ((peer *peer*) (scope *scope*) (request *request*))
    (lambda (&rest args)
      (let ((*peer* peer) (*scope* scope) (*request* request))
        (apply f args)))))

(defun branch-freshness (branch repo structure)
  "omhost's Call.freshness: the active project records of branch judged against the HEAD
structure view structure answers for repo (a decoded host.Repo), by record id; nil without a
repository, a view or a record to ask about."
  (when (and structure (hash-table-p repo) (string/= (om::str (om::jget repo "repoId")) ""))
    (let* ((records (om::folded-active (om::fold-ledger branch)))
           (q (om::project-query records repo)))
      (unless (om::project-query-empty-p q)
        (let ((answer (funcall structure q)))
          (when answer (om::project-texts (om::project-verdicts records repo answer))))))))

(defun tool-freshness (branch)
  "The memory tool's freshness verdicts over the adapter (host/repo, host/structure)."
  (handler-case (branch-freshness branch (om::jget (host-call "host/repo") "repo") #'host-structure)
    (request-cancelled (e) (error e))
    (error () nil)))

;;; The om client's view (cli.viaLisp): a client that passes "structure": true with the repo it
;;; read answers host/structure on the connection, as the PiG adapter does (memory/gitrepo run
;;; on its side); the image never runs git.

(defun client-repo (params)
  "The repository the om client read (its repo param), nil outside one."
  (let ((repo (om::jget params "repo")))
    (and (hash-table-p repo) (string/= (om::str (om::jget repo "repoId")) "") repo)))

(defun client-structure (params)
  "host.StructureReader over the om client, nil when it answers none (no structure param, or no
repository)."
  (and (eq (om::jget params "structure") :true) (client-repo params) (scoped #'host-structure)))

(defun client-freshness (params branch)
  "The memory tool's freshness verdicts over the om client, read at the first notes read that
shows rows."
  (let ((structure (client-structure params)) (repo (client-repo params)))
    (and structure
         (lambda ()
           (handler-case (branch-freshness branch repo structure)
             (request-cancelled (e) (error e))
             (error () nil))))))

(defun store-dir (c)
  "harness.StoreOf's store: <config home>/observational-memory/memories, nil without one."
  (and (rcfg-curated-memory (conn-cfg c))
       (let ((home (host-string "host/configHome")))
         (and (string/= home "") (format nil "~A/observational-memory/memories" (string-right-trim "/" home))))))

(defun check-harness-store (store)
  "The harness store's part before a turn end's extraction: rules written before the layer are
migrated first (rules.migrate, src/harness/rule-migration.lisp), as Go's rules.For does."
  (om.layer::migrate-legacy-rules store))

(defun extract-turn (c &key store model)
  "hooks.extractTurnV2: level 0 over the entries after the furthest extraction marker, the
harness detectors over what it read (detectV2: the keep-head rule's notes in its batches) when
there is a store and a session model, appended through the host under the session's extraction
mutex, and the detector run recorded once the extraction landed. A branch that cannot be read
appends nothing; an extraction that fails before its first append, or reads a memory call in the
removed query language, is refused (not ported), so the Go engine reads the turn instead."
  (sb-thread:with-mutex ((conn-extract-mutex c))
    (let ((branch (handler-case (get-branch c)
                    (error (e) (log-line "turn end: getBranch: ~A" e) (return-from extract-turn 0))))
          (record nil) (revisions '()))
      (multiple-value-bind (batches covers)
          (handler-case (let* ((start (om::extraction-start-index branch)) (folded (om::fold-ledger branch)))
                          (multiple-value-bind (batches covers revs)
                              (let ((om::*encoding* (session-encoding c branch)))
                                (om::extract branch start (length branch) folded :skip-tools (conn-skip-tools c)
                                                                                  :cwd (scope-cwd)))
                            (multiple-value-bind (batches thunk)
                                (om.layer::harness-detect store model branch start (length branch) folded batches covers
                                                          :defined (and store (string/= covers "") (defined-queries c store)))
                              (setf record thunk revisions revs)
                              (values batches covers))))
            (om.layer::legacy-memory-call (e) (not-ported (format nil "event/turnEnd over this branch (~A)" e)))
            (error (e) (not-ported (format nil "event/turnEnd over this branch (~A)" e))))
        (check-cancelled)
        ;; stampV2: the new project records' body hashes from HEAD.
        (om::stamp-batches #'host-structure batches)
        (let ((landed (host-append-extraction batches covers revisions)))
          (when (and (plusp landed) record)
            (handler-case (funcall record)
              (error (e) (log-line "turn end: detect record: ~A" e))))
          landed)))))

(define-method "event/turnEnd" (params)
  ;; hooks.OnTurnEnd: EnsureConfig, then level 0 unless passive, with the harness detectors when
  ;; the host has a store and a session model (detectV2).
  (with-event (c params)
    (ensure-config c)
    (unless (rcfg-passive (conn-cfg c))
      (let* ((store (store-dir c)) (model (if store (session-model-key) "")))
        (when (and store (string/= model "")) (check-harness-store store))
        ;; The store's kernel floor (src/harness/floor.lisp) applies to the definitions it reads.
        (let ((om::*kernel-floor-lowered* (conn-floor-lowered-p c store)))
          (extract-turn c :store (and (string/= model "") store) :model model))))
    (thinning-result c)))

;;; Context

(defparameter +stale-context+ -32001)

(defun json-copy (v)
  "deepCopy: decoded JSON's objects and arrays copied, strings and numbers shared."
  (cond ((hash-table-p v)
         (let ((out (make-hash-table :test 'equal :size (hash-table-count v))))
           (maphash (lambda (k x) (setf (gethash k out) (json-copy x))) v)
           out))
        ((and (vectorp v) (not (stringp v))) (map 'simple-vector #'json-copy v))
        (t v)))

(defun thin-messages (c messages)
  "hooks.OnContext over the host: day headers, then the thinning planned once per compaction
(keyed by the session and the compaction's id). A branch it cannot read gets headers only.
Returns how many results it thinned; a failure leaves what it did so far (Go recovers too)."
  (ensure-config c)
  (when (or (rcfg-passive (conn-cfg c)) (null messages))
    (return-from thin-messages 0))
  (handler-case
      (progn
        (om::day-headers messages)
        (let* ((branch (handler-case (get-branch c) (error () (return-from thin-messages 0))))
               (i (om::find-last-compaction-index branch)))
          (when (< i 0) (return-from thin-messages 0))
          (let* ((key (format nil "~A~C~A" (session-identity) #\Nul (om::entry-id (aref branch i))))
                 (plan (sb-thread:with-mutex ((conn-mutex c))
                         (if (equal key (conn-thin-key c)) (conn-thin-plan c) :none))))
            (when (eq plan :none)
              (setf plan (om::thin-plan (subseq branch 0 (1+ i)) (conn-skip-tools c) (scope-cwd)))
              (sb-thread:with-mutex ((conn-mutex c)) (setf (conn-thin-key c) key (conn-thin-plan c) plan)))
            (if plan (om::thin-apply messages plan) 0))))
    (request-cancelled (e) (error e))
    (error (e) (log-line "context recovered: ~A" e) 0)))

(define-method "event/context" (params)
  ;; Server.context: the leading messages a request reuses come from the previous request's
  ;; pristine list; -32001 when the image does not hold the request it builds on. The engine
  ;; edits deep copies and answers only the messages it changed.
  (with-event (c params)
    (let* ((seq (param-int params "seq")) (base (param-int params "base")) (reuse (param-int params "reuse"))
           (raw (let ((m (om::jget params "messages"))) (if (and (vectorp m) (not (stringp m))) m #())))
           (pristine nil))
      (sb-thread:with-mutex ((conn-mutex c))
        (let ((held (conn-ctx-pristine c)))
          (when (or (< reuse 0) (and (> reuse 0) (or (/= base (conn-ctx-seq c)) (= base 0) (> reuse (length held)))))
            (rpc-fail +stale-context+ "stale context base"))
          (setf pristine (concatenate 'simple-vector (subseq held 0 reuse)
                                      (map 'simple-vector (lambda (m) (and (hash-table-p m) m)) raw)))
          (setf (conn-ctx-seq c) seq
                (conn-ctx-pristine c) (if (= seq 0) #() pristine))))
      (let* ((copies (map 'simple-vector (lambda (m) (and m (json-copy m))) pristine))
             (edits '()))
        (thin-messages c (loop for m across copies when m collect m))
        (loop for i from 0 below (length copies)
              do (let ((m (aref copies i)))
                   (when (and m (string/= (om::json-string m) (om::json-string (aref pristine i))))
                     (push (om::jobject "index" i "message" m) edits))))
        (om::jobject "edits" (coerce (nreverse edits) 'simple-vector))))))
