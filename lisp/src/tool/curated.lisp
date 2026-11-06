;;;; memory/curated, the agent-written memory: the memory file (store.go format and parse), the
;;;; one validator (staging.go Check) run on every write and load, the writes (Save, Update,
;;;; Forget, the usage ledger) and the memory tool's write ops with their texts (tools.go
;;;; Call.Run, Describe). The files are the Go store's, in the same directory and under the same
;;;; lock, so the Go engine and the image read each other's memories.
;;;;
;;;; The ranked index, the usage fold and Settle are in curated-index.lisp. Not ported
;;;; (docs/lisp-port-tool-layer.md): the retired defined queries, which are only detected: a name
;;;; one holds is refused for a save, and forgetting or giving feedback on one is refused here.

(in-package "OM.LAYER")

(defparameter +memory-types+ '("user" "feedback" "project" "reference" "lesson"))

(defparameter +recall-page-chars+ 10000 "serialize.RecallPageChars.")

(defparameter +min-quote-chars+ 8)
(defparameter +max-quote-chars+ 400)

(define-condition curated-error (error)
  ((message :initarg :message :reader curated-error-message))
  (:report (lambda (c s) (write-string (curated-error-message c) s))))

(define-condition not-found (curated-error) ()
  (:default-initargs :message "no memory by that name"))

(defun fail (fmt &rest args)
  (error 'curated-error :message (apply #'format nil fmt args)))

(defstruct (memory (:conc-name mem-))
  (name "") (type "") (description "") (body "") (project "")
  (sources '()) created updated (staged nil) (staged-by '()) (pinned nil) retired)

(defun q (s) (om::go-quote s))

(defun replace-first (s old new)
  "strings.Replace(s, old, new, 1)."
  (let ((p (search old s)))
    (if p (concatenate 'string (subseq s 0 p) new (subseq s (+ p (length old)))) s)))

(defun one-line (s) (om::one-line s))

(defun store-path (dir name) (join-path dir (concatenate 'string name ".md")))

(defun usage-path (dir) (join-path dir "usage.jsonl"))

(defun valid-name-p (name)
  "^[a-z0-9][a-z0-9-]{0,63}$"
  (and (stringp name) (<= 1 (length name) 64)
       (every (lambda (c) (or (char<= #\a c #\z) (char<= #\0 c #\9) (char= c #\-))) name)
       (char/= (char name 0) #\-)))

(defun validate-name (name)
  (unless (valid-name-p name)
    (fail "memory name ~A must be lowercase letters, digits and hyphens, starting with a letter or digit, at most 64 characters" (q name))))

(defun valid-type-p (type) (and (member type +memory-types+ :test #'string=) t))

(defun type-error-text (type)
  (format nil "memory type ~A must be one of ~A" (q type) (om::join +memory-types+ ", ")))

(defun scope-note (m) (if (string/= (mem-project m) "") " (this project)" ""))

(defun state-note (m)
  (cond ((mem-staged m) " (staged)") ((mem-pinned m) " (pinned)") (t "")))

(defun curated-describe (m)
  "curated.Describe: a memory in full, as om.Recall(name) shows it."
  (with-output-to-string (b)
    (format b "~A [~A]~A~A: ~A~%" (mem-name m) (mem-type m) (scope-note m) (state-note m) (mem-description m))
    (when (string/= (mem-body m) "")
      (format b "~%~A~%" (mem-body m)))
    (format b "~%saved ~A, updated ~A" (format-date (mem-created m)) (format-date (mem-updated m)))
    (when (mem-sources m)
      (format b ", sessions: ~A" (om::join (mem-sources m) ", ")))))

(defun curated-check (m)
  "curated.Check: the validator every write and load runs. Signals curated-error."
  (validate-name (mem-name m))
  (unless (valid-type-p (mem-type m)) (fail "~A" (type-error-text (mem-type m))))
  (when (or (string= (om::trim-space (mem-description m)) "")
            (string/= (one-line (mem-description m)) (mem-description m)))
    (fail "memory ~A needs a one-line description" (q (mem-name m))))
  (when (and (not (mem-staged m)) (mem-staged-by m))
    (fail "memory ~A names staging sessions but is not staged" (q (mem-name m))))
  (let ((n (length (curated-describe m))))
    (when (> n +recall-page-chars+)
      (fail "memory ~A is ~D characters as recall shows it, and one recall shows at most ~D: shorten the body"
            (q (mem-name m)) n +recall-page-chars+)))
  m)

;;; The file

(defun format-memory (m)
  (with-output-to-string (b)
    (write-string "---" b) (terpri b)
    (format b "type: ~A~%" (mem-type m))
    (format b "description: ~A~%" (mem-description m))
    (when (string/= (mem-project m) "") (format b "project: ~A~%" (mem-project m)))
    (when (mem-sources m) (format b "sources: ~A~%" (om::join (mem-sources m) ", ")))
    (format b "created: ~A~%" (format-rfc3339 (mem-created m)))
    (format b "updated: ~A~%" (format-rfc3339 (mem-updated m)))
    (when (mem-staged m)
      (format b "staged: true~%")
      (when (mem-staged-by m) (format b "staged-by: ~A~%" (om::join (mem-staged-by m) ", "))))
    (when (mem-pinned m) (format b "pinned: true~%"))
    (unless (zero-time-p (mem-retired m))
      (format b "retired: ~A~%" (format-rfc3339 (mem-retired m))))
    (write-string "---" b) (terpri b)
    (when (string/= (mem-body m) "")
      (write-string (mem-body m) b) (terpri b))))

(defun comma-list (value)
  (loop for s in (om::split value ",")
        for v = (om::trim-space s)
        when (string/= v "") collect v))

(defun parse-memory (name text)
  "curated.parse: (values memory unknown-keys), or curated-error."
  (multiple-value-bind (rest ok) (if (om::has-prefix text (format nil "---~%"))
                                     (values (subseq text 4) t)
                                     (values text nil))
    (unless ok (fail "memory ~A has no front matter" (q name)))
    (multiple-value-bind (header body found) (om::cut rest (format nil "~%---~%"))
      (unless found
        (if (om::has-suffix rest (format nil "~%---"))
            (setf header (subseq rest 0 (- (length rest) 4)) body "")
            (fail "memory ~A has unterminated front matter" (q name))))
      (let ((m (make-memory :name name)) (unknown '()))
        (dolist (line (om::split header (string #\Newline)))
          (multiple-value-bind (key value found) (om::cut line ":")
            (when found
              (let ((value (om::trim-space value)) (key (om::trim-space key)))
                (cond ((string= key "type") (setf (mem-type m) value))
                      ((string= key "description") (setf (mem-description m) value))
                      ((string= key "project") (setf (mem-project m) value))
                      ((string= key "sources") (setf (mem-sources m) (append (mem-sources m) (comma-list value))))
                      ((string= key "created") (setf (mem-created m) (parse-rfc3339 value)))
                      ((string= key "updated") (setf (mem-updated m) (parse-rfc3339 value)))
                      ((string= key "staged") (setf (mem-staged m) (string= value "true")))
                      ((string= key "staged-by") (setf (mem-staged-by m) (append (mem-staged-by m) (comma-list value))))
                      ((string= key "pinned") (setf (mem-pinned m) (string= value "true")))
                      ((string= key "retired") (setf (mem-retired m) (parse-rfc3339 value)))
                      (t (push key unknown)))))))
        (setf unknown (reverse unknown))
        (setf (mem-body m) (om::trim-space body))
        (unless (and (valid-type-p (mem-type m)) (string/= (mem-description m) ""))
          (fail "memory ~A needs a valid type and a description" (q name)))
        (values m unknown)))))

;;; Reads

(defun read-memory (dir name)
  "Store.read: the memory without the load check, and its unknown keys."
  (validate-name name)
  (let ((text (read-file (store-path dir name))))
    (unless text (error 'not-found))
    (parse-memory name text)))

(defun load-check (m unknown)
  (when unknown
    (fail "memory ~A has fields no memory takes: ~A" (q (mem-name m)) (om::join unknown ", ")))
  (curated-check m))

(defun get-memory (dir name)
  "Store.Get: a memory the validator refuses is refused."
  (multiple-value-bind (m unknown) (read-memory dir name)
    (load-check m unknown)))

(defun visible-p (m project) (or (string= (mem-project m) "") (string= (mem-project m) project)))

(defun seen-by-p (m session) (or (not (mem-staged m)) (member session (mem-staged-by m) :test #'string=)))

(defun get-in (dir name project)
  (let ((m (get-memory dir name)))
    (unless (and (visible-p m project) (zero-time-p (mem-retired m))) (error 'not-found))
    m))

(defun hidden-in-p (m events session)
  "curated.HiddenIn: session staged a forget of m since m was saved."
  (and (string/= session "")
       (some (lambda (e) (and (string= (ul-event e) "forget-staged") (string= (ul-session e) session)
                              (not (time-before-p (usage-event-time e) (mem-created m)))))
             events)))

(defun pending-revision (m events session)
  "curated.PendingRevision: the last edit session staged of m since m was saved, (type
description body), unless a save or an edit for every session came after it; nil for none."
  (let ((rev nil))
    (when (string/= session "")
      (dolist (e events)
        (unless (time-before-p (usage-event-time e) (mem-created m))
          (let ((event (ul-event e)))
            (cond ((or (string= event "save") (string= event "edit")) (setf rev nil))
                  ((and (string= event "edit-staged") (string= (ul-session e) session) (ul-revision e))
                   (setf rev (ul-revision e))))))))
    rev))

(defun with-revision (m events session)
  "withRevision: m with the edit session staged applied (a copy when there is one)."
  (let ((rev (pending-revision m events session)))
    (if rev
        (let ((c (copy-memory m)))
          (setf (mem-type c) (first rev) (mem-description c) (second rev) (mem-body c) (third rev))
          c)
        m)))

(defun get-as (dir name project session)
  "Store.GetAs: get-in, a staged memory only in its staging sessions, none whose forget session
staged, and as session's staged edit left it."
  (let ((m (get-in dir name project)) (events (gethash name (usage-events-by-name dir))))
    (unless (seen-by-p m session) (error 'not-found))
    (when (hidden-in-p m events session) (error 'not-found))
    (with-revision m events session)))

(defun read-in (dir name project)
  (let ((m (read-memory dir name)))
    (unless (and (visible-p m project) (zero-time-p (mem-retired m))) (error 'not-found))
    m))

(defun memory-file-names (dir)
  (remove-if-not #'valid-name-p (dir-file-names dir ".md")))

(defun list-memories (dir project)
  "Store.List: the readable, unretired memories that apply in project, in name order."
  (loop for name in (memory-file-names dir)
        for m = (handler-case (get-memory dir name) (curated-error () nil))
        when (and m (visible-p m project) (zero-time-p (mem-retired m))) collect m))

(defun list-as (dir project session)
  "Store.ListAs: list-memories as session sees them (seen-by-p, not hidden-in-p, with-revision)."
  (let ((listed (list-memories dir project)) (events (usage-events-by-name dir)))
    (loop for m in listed
          when (and (seen-by-p m session) (not (hidden-in-p m (gethash (mem-name m) events) session)))
            collect (with-revision m (gethash (mem-name m) events) session))))

(defun source-digest (text)
  "curated.SourceDigest: sha256 hex of the text, whitespace collapsed."
  (om::sha256-octets (om::utf8-octets (one-line text))))

;;; Writes

(defun redact-memory (m)
  "redactMemory: m with every secret in its description and body redacted, in place."
  (setf (mem-description m) (om::redact-secrets (mem-description m))
        (mem-body m) (om::redact-secrets (mem-body m)))
  m)

(defun write-memory (dir m)
  (redact-memory m)
  (write-atomic (store-path dir (mem-name m)) (format-memory m)))

(defun write-checked (dir m)
  (redact-memory m)
  (curated-check m)
  (write-memory dir m))

(defun add-source (sources source)
  (if (or (string= source "") (member source sources :test #'string=))
      sources
      (append sources (list source))))

(defun same-description-p (a b) (string-equal (one-line a) (one-line b)))

(defun append-usage (dir name event &key quote user record session source model revision)
  "Store.appendUsage: one usage line, in the Go struct's member order. revision is a staged
edit's (type description body)."
  (ensure-dir dir)
  (append-line (usage-path dir)
               (with-output-to-string (out)
                 (flet ((member* (key value &optional (comma t))
                          (when comma (write-char #\, out))
                          (om::write-json-string key out)
                          (write-char #\: out)
                          (om::write-json-string value out)))
                   (write-char #\{ out)
                   (member* "time" (format-rfc3339 (now)) nil)
                   (member* "name" name)
                   (member* "event" event)
                   ;; The ledger keeps a quote redacted (contracts/secrets.md).
                   (when (and quote (string/= quote "")) (member* "quote" (om::redact-secrets quote)))
                   (when user (write-string ",\"user\":true" out))
                   (when (and record (string/= record "")) (member* "record" record))
                   (when (and session (string/= session "")) (member* "session" session))
                   (when (and source (string/= source "")) (member* "source" source))
                   (when (and model (string/= model "")) (member* "model" model))
                   (when revision
                     (write-string ",\"revision\":{" out)
                     (member* "type" (first revision) nil)
                     (member* "description" (om::redact-secrets (second revision)))
                     (when (string/= (third revision) "") (member* "body" (om::redact-secrets (third revision))))
                     (write-char #\} out))
                   (write-char #\} out)))))

(defun defined-query-path (dir name)
  "Where a retired defined query of the name lives: the layer's all-models tool file, or the
legacy definitions directory."
  (list (join-path dir "harness" "_2a" "tool" (concatenate 'string name ".rows"))
        (join-path dir "definitions" (concatenate 'string name ".rows"))))

(defun defined-query-p (dir name) (some #'file-exists-p (defined-query-path dir name)))

(defun save-memory (dir m source &optional (digest ""))
  "Store.SaveCiting (Store.Save without a digest): (values saved joined)."
  (validate-name (mem-name m))
  (unless (valid-type-p (mem-type m)) (fail "~A" (type-error-text (mem-type m))))
  (when (string= (om::trim-space (mem-description m)) "")
    (fail "a memory needs a one-sentence description"))
  (setf (mem-pinned m) nil (mem-retired m) nil)
  ;; Redacted before anything compares it, so a staging of the same save matches the stored one.
  (redact-memory m)
  (unless (mem-staged m) (setf (mem-staged-by m) '()))
  (let ((saved nil) (joined nil))
    (with-store-lock (dir)
      (let ((existing nil) (err nil))
        (handler-case (setf existing (get-memory dir (mem-name m)))
          (curated-error (e) (setf err e)))
        (cond
          ((and existing (zero-time-p (mem-retired existing)))
           (cond
             ((and (mem-staged existing) (mem-staged m) (visible-p existing (mem-project m))
                   (same-description-p (mem-description existing) (mem-description m)))
              (setf (mem-staged-by existing) (add-source (mem-staged-by existing) source)
                    (mem-sources existing) (add-source (mem-sources existing) source)
                    (mem-updated existing) (now))
              (write-checked dir existing)
              (setf saved existing joined t))
             ((and (mem-staged existing) (not (seen-by-p existing source)))
              (fail "the name ~A is staged by another session with another description; choose another name" (q (mem-name m))))
             ((not (visible-p existing (mem-project m)))
              (fail "the name ~A is taken by a memory scoped to another project; choose another name" (q (mem-name m))))
             (t (fail "a memory named ~A already exists; change it with edit name=~A instead" (q (mem-name m)) (mem-name m)))))
          ((and err (not (typep err 'not-found)))
           (if (handler-case (progn (read-memory dir (mem-name m)) t) (curated-error () nil))
               (fail "the name ~A is taken by a memory the validator refuses (~A); fix it with edit name=~A or retire it with forget name=~A"
                     (q (mem-name m)) err (mem-name m) (mem-name m))
               (fail "the name ~A is taken by an unreadable memory file; choose another name" (q (mem-name m))))))
        (unless saved
          (when (defined-query-p dir (mem-name m))
            (fail "the name ~A is taken by a defined query; choose another name" (q (mem-name m))))
          (let ((now (now)))
            (setf (mem-description m) (one-line (mem-description m))
                  (mem-created m) now (mem-updated m) now
                  (mem-sources m) (add-source '() source))
            (when (mem-staged m) (setf (mem-staged-by m) (add-source '() source)))
            (setf saved m)
            (write-checked dir m)))))
    (if (mem-staged saved)
        (append-usage dir (mem-name m) (if joined "staged" "save") :session source :source digest)
        (append-usage dir (mem-name m) (if joined "staged" "save")))
    (values saved joined)))

(defun apply-edit (m source &key (type nil type-p) (description nil description-p) (body nil body-p))
  "applyEdit: m with the edit applied in place, as session source makes it."
  (when type-p
    (unless (valid-type-p type) (fail "~A" (type-error-text type)))
    (setf (mem-type m) type))
  (when description-p
    (when (string= (om::trim-space description) "")
      (fail "a memory needs a one-sentence description"))
    (when (and (mem-staged m) (not (same-description-p (mem-description m) description)))
      (setf (mem-staged-by m) (add-source '() source)))
    (setf (mem-description m) (one-line description)))
  (when body-p (setf (mem-body m) (om::trim-space body)))
  m)

(defun update-memory (dir name project source &rest edit &key quote &allow-other-keys)
  "Store.Update, and Store.UpdateQuoted with a quote: the edit line keeps the user's words."
  (let ((updated nil) (edit (loop for (k v) on edit by #'cddr unless (eq k :quote) append (list k v))))
    (with-store-lock (dir)
      (let ((m (read-in dir name project)))
        (unless (seen-by-p m source) (error 'not-found))
        (apply #'apply-edit m source edit)
        (setf (mem-updated m) (now)
              (mem-sources m) (add-source (mem-sources m) source))
        (redact-memory m)
        (setf updated m)
        (write-checked dir m)))
    (if quote
        (append-usage dir name "edit" :quote quote :user t)
        (append-usage dir name "edit"))
    updated))

(defun stage-edit (dir name project session &rest edit)
  "Store.StageEdit: session sees the memory as the edit leaves it, every other session as it was."
  (let ((m (copy-memory (get-as dir name project session))))
    (apply #'apply-edit m session edit)
    (redact-memory m)
    (curated-check m)
    (append-usage dir name "edit-staged" :session session
                  :revision (list (mem-type m) (mem-description m) (mem-body m)))
    m))

(defun forget-memory* (dir name project &key quote)
  "Store.Forget, and Store.ForgetQuoted with a quote: retires the memory, keeping its file."
  (with-store-lock (dir)
    (let ((m (read-in dir name project)))
      (setf (mem-retired m) (now))
      (write-memory dir m)))
  (if quote
      (append-usage dir name "forget" :quote quote :user t)
      (append-usage dir name "forget")))

(defun stage-forget (dir name project session)
  "Store.StageForget: hides the memory in session, leaving it shared."
  (read-in dir name project)
  (append-usage dir name "forget-staged" :session session))

(defparameter +quoted-change-events+ '("forget" "edit" "layer-forget" "layer-rollback")
  "quotedChangeEvents: the events whose user quote counts toward +max-forgets-per-quote+.")

(defun quoted-changes (dir quote)
  "Store.QuotedChanges: compared as the ledger keeps quotes (redacted)."
  (let ((quote (om::redact-secrets quote)))
    (count-if (lambda (l) (and (member (ul-event l) +quoted-change-events+ :test #'string=)
                               (ul-user l) (string= (ul-quote l) quote)))
              (read-usage-lines dir))))

(defun record-layer-change (dir event key model session quote target)
  "Store.RecordLayerChange: target is a rollback's generation, nil for a forget."
  (append-usage dir key event :quote quote :user (and quote (string/= quote "")) :session session :model model
                              :record (and target (princ-to-string target))))

(defun layer-key (kind name) (concatenate 'string "layer:" kind "/" name))

(defun staged-change-string (c)
  "StagedChange.String: c is (op name session model detail)."
  (destructuring-bind (op name session model detail) c
    (cond ((string= op "edit") (concatenate 'string "edit " name " (" session "): " detail))
          ((string= op "layer forget") (concatenate 'string "layer forget " model " " name " (" session ")"))
          ((string= op "layer rollback") (concatenate 'string "layer rollback " model " to generation " detail " (" session ")"))
          (t (concatenate 'string op " " name " (" session ")")))))

(defun staged-changes (dir project)
  "Store.StagedChanges: the staged forgets and edits of the memories that apply in project, then
the layer's staged forgets and rollbacks no confirmed one came after; each (op name session
model detail)."
  (let ((events (usage-events-by-name dir)) (out '()) (layer '()))
    (dolist (m (list-memories dir project))
      (let ((seen (make-hash-table :test 'equal)) (mevents (gethash (mem-name m) events)))
        (dolist (e mevents)
          (unless (or (string= (ul-session e) "") (time-before-p (usage-event-time e) (mem-created m)))
            (let ((event (ul-event e)) (session (ul-session e)))
              (cond ((string= event "forget-staged")
                     (unless (gethash (cons "forget" session) seen)
                       (setf (gethash (cons "forget" session) seen) t)
                       (push (list "forget" (mem-name m) session "" "") out)))
                    ((string= event "edit-staged")
                     (unless (gethash (cons "edit" session) seen)
                       (setf (gethash (cons "edit" session) seen) t)
                       (let ((rev (pending-revision m mevents session)))
                         (when rev
                           (push (list "edit" (mem-name m) session "" (om::redact-secrets (second rev))) out)))))))))))
    (dolist (line (read-usage-lines dir))
      (let ((event (ul-event line)))
        (cond ((or (string= event "layer-forget-staged") (string= event "layer-rollback-staged"))
               (let ((c (if (string= event "layer-rollback-staged")
                            (list "layer rollback" "" (ul-session line) (ul-model line) (ul-record line))
                            (list "layer forget" (if (om::has-prefix (ul-name line) "layer:") (subseq (ul-name line) 6) (ul-name line))
                                  (ul-session line) (ul-model line) ""))))
                 (unless (member c layer :test #'equal) (setf layer (append layer (list c))))))
              ((or (string= event "layer-forget") (string= event "layer-rollback"))
               (setf layer (remove-if (lambda (have)
                                        (and (string= (fourth have) (ul-model line))
                                             (or (and (string= event "layer-rollback") (string= (first have) "layer rollback"))
                                                 (and (string= event "layer-forget") (string= (first have) "layer forget")
                                                      (string= (concatenate 'string "layer:" (second have)) (ul-name line))))))
                                      layer))))))
    (append (nreverse out) layer)))

(defparameter +layer-rollback-key+ "layer:rollback")

(defparameter +max-forgets-per-quote+ 3 "curated.MaxForgetsPerQuote.")

(defun normalize-quote (quote)
  (let ((quote (one-line quote)))
    (when (< (length quote) +min-quote-chars+)
      (fail "feedback needs quote=\"...\": at least ~D characters from this conversation that show the memory helped or misled you" +min-quote-chars+))
    (if (> (length quote) +max-quote-chars+) (subseq quote 0 +max-quote-chars+) quote)))

;;; The memory tool's writes (Call.Run)

(defstruct (curated-call (:conc-name cc-))
  (dir "") (project "") (source "")
  ;; evidence: is a normalized quote in the conversation; nil accepts any quote
  (evidence nil)
  ;; user-evidence: is a normalized quote a user's words; nil finds none
  (user-evidence nil)
  ;; source-of: the source-digest of the tool output holding a normalized quote, "" for none;
  ;; nil finds none
  (source-of nil))

(defun user-quoted-p (c quote)
  (and (cc-user-evidence c) (funcall (cc-user-evidence c) quote)))

(defun arg (args key) (gethash key args))

(defun arg-p (args key) (nth-value 1 (gethash key args)))

(defun curated-run (c op args)
  "curated.Call.Run: (values text nil) or (values nil error-text). args is an equal hash table
of the write's string arguments; a key absent is an argument not given."
  (handler-case (values (curated-run* c op args) nil)
    (curated-error (e) (values nil (curated-error-message e)))))

(defun curated-run* (c op args)
  (let ((name (or (arg args "name") "")) (dir (cc-dir c)))
    (cond
      ((string= op "save")
       (let ((m (make-memory :name name :type (or (arg args "type") "") :description (or (arg args "description") "")
                             :body (om::trim-space (or (arg args "body") "")))))
         (let ((scope (or (arg args "scope") "")))
           (cond ((member scope '("" "global") :test #'string=))
                 ((string= scope "project")
                  (when (string= (cc-project c) "") (fail "no project directory is known; save it as global instead"))
                  (setf (mem-project m) (cc-project c)))
                 (t (fail "scope ~A must be global or project" (q scope)))))
         (let ((digest "") (user-quote ""))
         (if (arg-p args "quote")
             (let ((normalized (handler-case (normalize-quote (arg args "quote"))
                                 (curated-error (e)
                                   (fail "~A" (replace-first (curated-error-message e) "feedback needs" "save's quote needs"))))))
               (if (user-quoted-p c normalized)
                   (setf user-quote normalized)
                   (progn
                     (when (cc-source-of c) (setf digest (or (funcall (cc-source-of c) normalized) "")))
                     (when (string= digest "")
                       (fail "the quote is not a user's words in this conversation: copy words verbatim from a user message that confirm the memory, or save without quote to stage it"))
                     (setf (mem-staged m) t))))
             (setf (mem-staged m) t))
         (let* ((saved (save-memory dir m (cc-source c) digest))
                (held (save-held-text c saved user-quote)))
          (concatenate 'string
           (if (mem-staged saved)
               (cond ((<= (length (mem-staged-by saved)) 1)
                      (format nil "Staged memory ~A [~A]~A: ~A. It shows in saved in this session and stays out of the memory index until evidence confirms it: a user's words (feedback name=~A outcome=helped quote=\"...\"), a compaction citing it, or the same save in another session quoting other tool output it rests on."
                              (mem-name saved) (mem-type saved) (scope-note saved) (mem-description saved) (mem-name saved)))
                     ((independent-stagings-p saved (gethash (mem-name saved) (usage-events-by-name dir)))
                      (format nil "Staged memory ~A [~A]~A: ~A. Another session staged the same memory from other source content, which is the evidence to make it live: it enters the memory index at its next render."
                              (mem-name saved) (mem-type saved) (scope-note saved) (mem-description saved)))
                     (t
                      (format nil "Staged memory ~A [~A]~A: ~A. Another session staged the same memory, but not from other source content it quotes, so it is no independent evidence: it stays staged until a user's words (feedback name=~A outcome=helped quote=\"...\"), a compaction citing it, or a session quoting other tool output confirms it."
                              (mem-name saved) (mem-type saved) (scope-note saved) (mem-description saved) (mem-name saved))))
               (format nil "Saved memory ~A [~A]~A: ~A" (mem-name saved) (mem-type saved) (scope-note saved) (mem-description saved)))
           held)))))
      ((string= op "edit")
       (let ((edit '()))
         (when (and (arg-p args "type") (string/= (om::trim-space (arg args "type")) ""))
           (setf edit (list* :type (arg args "type") edit)))
         (when (and (arg-p args "description") (string/= (om::trim-space (arg args "description")) ""))
           (setf edit (list* :description (arg args "description") edit)))
         (when (arg-p args "body")
           (setf edit (list* :body (arg args "body") edit)))
         (unless edit (fail "nothing to change: give a new type, description or body"))
         (multiple-value-bind (quote quoted) (gate-quote args "edit")
           (let ((m (handler-case (read-in dir name (cc-project c)) (curated-error () nil))))
             (if (and m (seen-by-p m (cc-source c)) (not (and (mem-staged m) (= 1 (length (mem-staged-by m))))))
                 (edit-shared c m edit quote quoted)
                 (let ((m (apply #'update-memory dir name (cc-project c) (cc-source c) edit)))
                   (format nil "Updated memory ~A [~A]: ~A" (mem-name m) (mem-type m) (mem-description m))))))))
      ((string= op "forget")
       (let ((quote (gate-quote args "forget")))
       (handler-case
           (let ((m (handler-case (read-in dir name (cc-project c)) (curated-error () nil))))
             (cond ((and m (not (seen-by-p m (cc-source c)))) (error 'not-found))
                   ((null m) (forget-memory* dir name (cc-project c)))
                   ((and (mem-staged m) (= 1 (length (mem-staged-by m)))) (forget-memory* dir name (cc-project c)))
                   (t (return-from curated-run* (forget-shared c m quote))))
             (format nil "Forgot memory ~A." name))
         (not-found (e)
           (when (and (valid-name-p name) (defined-query-p dir name))
             (fail "~A is a retired defined query; the image does not forget defined queries (the Go engine does)" name))
           (error e)))))
      ((string= op "feedback")
       (let ((outcome (or (arg args "outcome") "")))
         (unless (member outcome '("helped" "harmed") :test #'string=)
           (fail "outcome must be \"helped\" or \"harmed\""))
         (let ((quote (normalize-quote (or (arg args "quote") ""))))
           (when (and (cc-evidence c) (not (funcall (cc-evidence c) quote)))
             (fail "the quote is not in this conversation: copy words verbatim from a user or assistant message that show the memory helped or misled you"))
           (handler-case (get-as dir name (cc-project c) (cc-source c))
             (curated-error (e)
               (when (and (typep e 'not-found) (valid-name-p name) (defined-query-p dir name))
                 (fail "~A is a retired defined query; the image records no feedback on defined queries" name))
               (error e)))
           (let ((user (and (string= outcome "helped") (user-quoted-p c quote))))
             (append-usage dir name outcome :quote quote :user user)
             (let ((text (format nil "Recorded that memory ~A ~A." name outcome))
                   (m (handler-case (get-as dir name (cc-project c) (cc-source c)) (curated-error () nil))))
               (cond ((not (and user m)) text)
                     ((memory-held-rule m)
                      (if (restates-p quote)
                          (progn (release-memory dir name (cc-source c) quote)
                                 (concatenate 'string text " " (released-note (memory-held-rule m))))
                          text))
                     ((mem-staged m)
                      (concatenate 'string text " The quote is a user's words, the evidence a staged memory needs: it goes live at the next check and enters the memory index at its next render."))
                     (t text)))))))
      (t (fail "unknown memory write ~A" (q op))))))

(defun gate-quote (args what)
  "curated.GateQuote: (values normalized-quote quoted), the quote nil when none was given."
  (if (arg-p args "quote")
      (values (handler-case (normalize-quote (arg args "quote"))
                (curated-error (e)
                  (fail "~A" (replace-first (curated-error-message e) "feedback needs" (concatenate 'string what "'s quote needs")))))
              t)
      (values nil nil)))

(defun covers (c quote)
  "Call.Covers: (values ok reason), quote nil when none was given."
  (cond ((null quote) (values nil "no user's words ask for it"))
        ((not (user-quoted-p c quote)) (values nil "the quote is not a user's words in this conversation"))
        ((>= (quoted-changes (cc-dir c) quote) +max-forgets-per-quote+)
         (values nil (format nil "the quote already confirmed ~D changes, the most one quote confirms; more changes need the user's words for each" +max-forgets-per-quote+)))
        (t (values t ""))))

(defun forget-shared (c m quote)
  "Call.forgetShared: retired for every session on a user's words that cover one more change,
else hidden in this session and staged."
  (let ((dir (cc-dir c)) (name (mem-name m)))
    (multiple-value-bind (ok reason) (covers c quote)
      (when ok
        (forget-memory* dir name (cc-project c) :quote quote)
        (return-from forget-shared (format nil "Forgot memory ~A." name)))
      (stage-forget dir name (cc-project c) (cc-source c))
      (format nil "Hid memory ~A in this session and staged its forget: ~A, so other sessions still see it. Forgetting it for every session needs words copied verbatim from a user message asking for it, quoted with the forget." name reason))))

(defun edit-shared (c m edit quote quoted)
  "Call.editShared: made for every session on a user's words that cover one more change, else
made in this session only and staged."
  (declare (ignore quoted))
  (let ((dir (cc-dir c)))
    (multiple-value-bind (ok reason) (covers c quote)
      (if ok
          (let ((u (apply #'update-memory dir (mem-name m) (cc-project c) (cc-source c) :quote quote edit)))
            (format nil "Updated memory ~A [~A]: ~A" (mem-name u) (mem-type u) (mem-description u)))
          (let ((s (apply #'stage-edit dir (mem-name m) (cc-project c) (cc-source c) edit)))
            (format nil "Changed memory ~A [~A] in this session only and staged the edit: ~A, so other sessions still see it as it was. Changing it for every session needs words copied verbatim from a user message asking for it, quoted with the edit." (mem-name s) (mem-type s) reason))))))

(defun quoted-in-p (texts quote)
  "omhost.quotedIn over texts: quote, whitespace collapsed and case ignored, in one of them."
  (let ((needle (string-downcase (one-line quote))))
    (some (lambda (text) (search needle (string-downcase (one-line text)))) texts)))
