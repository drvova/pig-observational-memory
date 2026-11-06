;;;; W6: the first PiG events over engine-rpc (docs/lisp-port-peer.md; docs/engine-rpc.md):
;;;; event/sessionStart, event/turnEnd (without the harness detectors) and event/context, with
;;;; the host/* calls they make. Loaded after methods.lisp and server/hooks.lisp, so each
;;;; define-method here replaces a not-ported entry. Every other engine-rpc method stays not
;;;; ported, and a ported one refuses (not ported) what it cannot do the way Go does, always
;;;; before its first host write, so the adapter can run the event on the Go engine instead.

(in-package "OM.SERVER")

;;; Session start

(define-method "event/sessionStart" (params)
  ;; Server.handle: ReloadConfig, then engine.SessionStart: the pin and the namespace reset, a
  ;; detector pass stops, the retired tools' names are read, the v1 layer migrated and the
  ;; curated evidence settled. The image holds no namespace or detector pass; the last three
  ;; act only with a store, which is not ported: that start is refused (after the image's own
  ;; state is reset, which changes nothing the Go engine would see).
  (with-event (c params)
    (conn-reload-config c)
    (sb-thread:with-mutex ((conn-mutex c)) (setf (conn-pin c) nil))
    (ensure-config c)
    (when (store-on-p c)
      (not-ported "event/sessionStart with a curated store (retired tool names, the v1 layer migration, the curated settle)"))
    (om::jobject)))

;;; Turn end

(defun thinning-result (c)
  (om::jobject "thinning" (if (rcfg-passive (conn-cfg c)) :false :true)))

(defun host-append-extraction (batches covers)
  "hooks.appendExtraction over the host: one om.observations.recorded entry per batch with
records, then an om.extracted marker when the last batch stops short; the appends stop at the
first that fails. Returns how many landed."
  (let ((queue '()) (landed 0))
    (dolist (b batches)
      (when (and (om::batch-observations b) (string/= (om::batch-covers b) ""))
        (push (cons "om.observations.recorded" (recorded-data-json (om::batch-observations b) (om::batch-covers b)))
              queue)))
    (when (and (string/= covers "") (or (null batches) (string/= (om::batch-covers (car (last batches))) covers)))
      (push (cons "om.extracted" (format nil "{\"coversUpToId\":~A}" (om::json-string covers))) queue))
    (dolist (p (nreverse queue) landed)
      (handler-case (progn (host-call "host/appendEntry" "customType" (car p) "data" (om::raw-json (cdr p)))
                           (incf landed))
        (error (e)
          (log-line "turn end: append ~A: ~A" (car p) e)
          (return landed))))))

(defun extract-turn (c)
  "hooks.extractTurnV2 without detectV2: level 0 over the entries after the furthest
extraction marker, appended through the host, under the session's extraction mutex. A branch
that cannot be read appends nothing; an extraction that fails before its first append is
refused (not ported), so the Go engine reads the turn instead."
  (sb-thread:with-mutex ((conn-extract-mutex c))
    (let ((branch (handler-case (get-branch c)
                    (error (e) (log-line "turn end: getBranch: ~A" e) (return-from extract-turn 0)))))
      (multiple-value-bind (batches covers)
          (handler-case (om::extract branch (om::extraction-start-index branch) (length branch)
                                     (om::fold-ledger branch) :skip-tools (list om::+memory-tool+)
                                     :cwd (scope-cwd))
            (error (e) (not-ported (format nil "event/turnEnd over this branch (~A)" e))))
        (check-cancelled)
        (host-append-extraction batches covers)))))

(define-method "event/turnEnd" (params)
  ;; hooks.OnTurnEnd: EnsureConfig, then level 0 unless passive. With a store and a session
  ;; model Go runs the harness detectors inside the extraction (detectV2: detect events in the
  ;; model's ledger, the keep-head remedy's notes in the batches), which are not ported, so that
  ;; turn end is refused before anything is appended.
  (with-event (c params)
    (ensure-config c)
    (unless (rcfg-passive (conn-cfg c))
      (when (and (store-on-p c) (string/= (session-model-key) ""))
        (not-ported "event/turnEnd with the harness detectors (detectV2, keep-head)"))
      (extract-turn c))
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
              (setf plan (om::thin-plan (subseq branch 0 (1+ i)) (list om::+memory-tool+) (scope-cwd)))
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
