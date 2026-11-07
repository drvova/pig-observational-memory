;;;; The layer (docs/lisp-core.md, "Proposing, holding, settling", "Rollback"): each model's Lisp
;;;; definitions of adaptable functions, on disk under <root>/<model dir>/ (root ~/.om/layer, or
;;;; *layer-root*):
;;;;
;;;;   manifest.jsonl        every change, one generation a line (lisp-layer-generation schema)
;;;;   held/index.json       each held adaptable/<name> and the digest of its held file
;;;;                         (layer-held-index schema, the Go layer's shape)
;;;;   held/<name>.lisp      a held definition's text
;;;;   settled/<name>.lisp   the settled definition's text, rewritten after every change
;;;;   blobs/<digest>.lisp   every text ever held, which a rollback restores
;;;;
;;;; The settled definitions are the manifest replayed (a rollback replays to its target); the
;;;; held ones are the files the index records with the digest it records, so a held file written
;;;; outside the store is never loaded. Every load re-reads a text with the safe reader, so a
;;;; definition the checks refuse never runs. A held definition never replaces the default's
;;;; result: it runs beside it until settle-v2 settles or reverts it.

(in-package "OM.LAYER")

(defvar *layer-root* nil "The layer's root: nil is ~/.om/layer (OM_LAYER_ROOT overrides it).")

(defun layer-root ()
  (or *layer-root*
      (let ((env (sb-posix:getenv "OM_LAYER_ROOT")))
        (and env (string/= env "") env))
      (join-path (namestring (user-homedir-pathname)) ".om" "layer")))

(defun model-root (model) (join-path (layer-root) (model-dir model)))

(defun digest (text)
  "layer.Digest: the first 16 hex characters of the SHA-256 of the text."
  (subseq (om::sha256-octets (om::utf8-octets text)) 0 16))

(defstruct ldef
  (model "") (name "") (text "") (digest "") time (state :held)
  ;; fn is the compiled definition (compile-definition)
  (fn nil))

(defun ldef-key (d) (concatenate 'string +kind-adaptable+ "/" (ldef-name d)))

(defun ldef-held-id (d)
  "layer.Definition.HeldID: kind/name@digest of the text."
  (concatenate 'string (ldef-key d) "@" (ldef-digest d)))

;;; The manifest

(defun manifest-path (model) (join-path (model-root model) "manifest.jsonl"))

;;; A generation's number is the gen its line records. A writer numbers the next one after the
;;; highest gen the manifest records, a torn line's included (the {"gen":N prefix of a line a crash
;;; cut off, which no reader decodes), so no number is ever written twice, even after a crash; its
;;; append ends a torn tail first and is fsynced (om::append-jsonl). A reader takes each decoded
;;; line's recorded gen when it is an integer above the line before it's, and that one's plus one
;;; otherwise, so a manifest a count-numbering writer wrote (gen = 1 + the lines it could decode)
;;; reads as it always did.

(defun torn-gen (line)
  "The gen a line that does not decode claims: N of its {\"gen\":N prefix, or nil."
  (let ((prefix "{\"gen\":"))
    (when (om::has-prefix line prefix)
      (let* ((start (length prefix))
             (end (or (position-if-not #'digit-char-p line :start start) (length line))))
        (and (> end start) (parse-integer line :start start :end end))))))

(defun parse-manifest (text)
  "(values generations claimed): the decoded generation lines in order, each one's \"gen\" its
number as a reader takes it (an integer), and the highest gen any line claims, decoded or torn."
  (let ((out '()) (prev 0) (claimed 0))
    (when text
      (dolist (line (om::split text (string #\Newline)))
        (unless (string= (om::trim-space line) "")
          (let ((g (handler-case (om::json-parse line) (error () nil))))
            (if (and (hash-table-p g) (stringp (om::jget g "op")))
                (let* ((rec (om::jget g "gen"))
                       (n (if (and (realp rec) (= rec (truncate rec)) (> (truncate rec) prev)) (truncate rec) (1+ prev))))
                  (setf (gethash "gen" g) n prev n claimed (max claimed n))
                  (push g out))
                (let ((n (and (not (hash-table-p g)) (torn-gen line))))
                  (when n (setf claimed (max claimed n)))))))))
    (values (nreverse out) claimed)))

(defun read-manifest (model)
  "The manifest's generation lines, in order; a line without an op is skipped."
  (values (parse-manifest (read-file (manifest-path model)))))

(defun manifest-generation (manifest)
  "The latest generation's number: 0 for an empty manifest."
  (let ((last (car (last manifest)))) (if last (om::jget last "gen") 0)))

(defun next-generation (model)
  (multiple-value-bind (gens claimed) (parse-manifest (read-file (manifest-path model)))
    (1+ (max claimed (manifest-generation gens)))))

(defun append-generation (model op &key name digest reason target settled)
  "Appends the next generation (next-generation) and returns its number. Members in the
schema's order. The caller holds the layer's lock."
  (let ((gen (next-generation model)))
    (append-line (manifest-path model)
                 (with-output-to-string (out)
                   (format out "{\"gen\":~D,\"time\":" gen)
                   (om::write-json-string (format-rfc3339-nano (now)) out)
                   (write-string ",\"op\":" out) (om::write-json-string op out)
                   (when name
                     (write-string ",\"kind\":" out) (om::write-json-string +kind-adaptable+ out)
                     (write-string ",\"name\":" out) (om::write-json-string name out))
                   (when digest (write-string ",\"digest\":" out) (om::write-json-string digest out))
                   (when target (format out ",\"target\":~D" target))
                   (when reason (write-string ",\"reason\":" out) (om::write-json-string reason out))
                   (when settled (write-string ",\"settled\":true" out))
                   (write-char #\} out)))
    gen))

(defun replay (manifest)
  "The settled state after each generation: a vector whose element g is an alist name -> digest
(element 0 is before the first change; a number no line holds, a torn generation's, changed
nothing, so it holds the state before it)."
  (let ((states (make-array 1 :adjustable t :fill-pointer t :initial-contents '(())))
        (state '()))
    (dolist (g manifest)
      (let ((op (om::jget g "op")) (name (om::jget g "name")) (digest (om::jget g "digest"))
            (n (om::jget g "gen")))
        (loop while (< (length states) n) do (vector-push-extend state states))
        (cond ((string= op "settle")
               (setf state (acons name digest (remove name state :key #'car :test #'equal))))
              ((string= op "forget")
               (setf state (remove name state :key #'car :test #'equal)))
              ;; A settled definition the retire rule reverted: its text no longer stands. A held
              ;; one's revert (no settled member) changes nothing settled.
              ((and (string= op "revert") (eq (om::jget g "settled") :true)
                    (equal (cdr (assoc name state :test #'equal)) digest))
               (setf state (remove name state :key #'car :test #'equal)))
              ((string= op "rollback")
               (let ((target (om::jget g "target")))
                 (setf state (if (and (realp target) (< -1 target (length states)))
                                 (copy-alist (aref states (truncate target)))
                                 state))))))
      (vector-push-extend state states))
    states))

(defun hold-time (manifest name digest)
  "When the held text was held: the time of the latest hold of it."
  (let ((out nil))
    (dolist (g manifest out)
      (when (and (equal (om::jget g "op") "hold") (equal (om::jget g "name") name)
                 (equal (om::jget g "digest") digest))
        (setf out (parse-rfc3339 (om::jget g "time")))))))

;;; Files

(defun blob-path (model digest) (join-path (model-root model) "blobs" (concatenate 'string digest ".lisp")))
(defun held-path (model name) (join-path (model-root model) "held" (concatenate 'string name ".lisp")))
(defun settled-path (model name) (join-path (model-root model) "settled" (concatenate 'string name ".lisp")))
(defun held-index-path (model) (join-path (model-root model) "held" "index.json"))

(defun read-held-index (model)
  (let* ((text (read-file (held-index-path model)))
         (v (and text (handler-case (om::json-parse text) (error () nil)))))
    (if (hash-table-p v) v (make-hash-table :test 'equal))))

(defun write-held-index (model index)
  (write-atomic (held-index-path model)
                (concatenate 'string (om::json-string index) (string #\Newline))))

(defun write-blob (model text)
  (let ((d (digest text)))
    (unless (file-exists-p (blob-path model d)) (write-atomic (blob-path model d) text))
    d))

(defun mirror-settled (model state)
  "Rewrites settled/ to hold exactly the state's texts."
  (let ((dir (join-path (model-root model) "settled")))
    (dolist (name (dir-file-names dir ".lisp"))
      (unless (assoc name state :test #'equal) (delete-file-if-exists (settled-path model name))))
    (loop for (name . d) in state
          for text = (read-file (blob-path model d))
          when text do (write-atomic (settled-path model name) text))))

(defmacro with-layer-lock ((model) &body body)
  `(with-store-lock ((model-root ,model)) ,@body))

;;; The cache. The image keeps each model's loaded layer (*layer-cache*), and each session's
;;; calls dispatch on the one pinned for it (*layer-pins*, session NUL model) from its last
;;; boundary, a session start or a compaction (layer-boundary), to its next: the moments the
;;; request bytes may change anyway. A loaded layer records the manifest's identity when it was
;;; read (manifest-identity: the generation, the file's size, modification time and inode, and a
;;; digest of its last line), and a boundary re-reads the layer when the manifest's identity is
;;; another, so a change another process made (another image, a restart overlap) is seen at this
;;; session's next boundary and never between two. A change this image makes drops the model's
;;; cache and pins (invalidate), so it stands from the next call, as the change's own call
;;; reports it.

(defvar *layer-cache* (make-hash-table :test 'equal))
(defvar *layer-pins* (make-hash-table :test 'equal) "session NUL model -> the loaded layer its calls use.")
(defvar *cache-lock* (sb-thread:make-mutex :name "om.layer cache"))

(defun pin-key (session model) (concatenate 'string (or session "") (string #\Nul) (or model "")))

(defun call-session-id () (if *call* (or (call-session *call*) "") ""))

(defun invalidate (model)
  (sb-thread:with-mutex (*cache-lock*)
    (remhash model *layer-cache*)
    (let ((suffix (concatenate 'string (string #\Nul) model)) (drop '()))
      (maphash (lambda (k v) (declare (ignore v)) (when (om::has-suffix k suffix) (push k drop))) *layer-pins*)
      (dolist (k drop) (remhash k *layer-pins*)))))

(defstruct loaded settled held reports identity)

(defun manifest-identity (model)
  "The manifest as a boundary compares it: (generation size mtime inode digest-of-its-last-line),
nil when there is none."
  (let ((path (manifest-path model)))
    (handler-case
        (let* ((st (sb-posix:stat path))
               (text (or (read-file path) ""))
               (end (length (string-right-trim '(#\Newline) text)))
               (start (let ((p (position #\Newline text :end end :from-end t))) (if p (1+ p) 0))))
          (list (manifest-generation (parse-manifest text)) (sb-posix:stat-size st) (sb-posix:stat-mtime st)
                (sb-posix:stat-ino st) (digest (subseq text start end))))
      (sb-posix:syscall-error () nil))))

(defun cached-layer (model)
  "The image's loaded layer of model, read from disk when it holds none."
  (or (sb-thread:with-mutex (*cache-lock*) (gethash model *layer-cache*))
      (multiple-value-bind (l refused) (load-layer-from-disk model)
        (when refused
          (loop for (name digest reason) in refused
                do (revert-settled model name digest (format nil "the checks refuse it on load: ~A" reason)))
          (setf l (load-layer-from-disk model)))
        (sb-thread:with-mutex (*cache-lock*) (setf (gethash model *layer-cache*) l))
        l)))

(defun load-layer (model)
  "The model's layer the current session's calls use: settled and held definitions, each re-read
and compiled (adaptable.lisp's compile-definition); one that no longer passes is left out and
reported, and a settled one the checks refuse is reverted at once (the retire rule's veto,
rules/evidence.lisp). It is the layer pinned at the session's last boundary (layer-boundary), or,
before its first or after a change this image made, the image's cached one, pinned from then on."
  (let ((key (pin-key (call-session-id) model)))
    (or (sb-thread:with-mutex (*cache-lock*) (gethash key *layer-pins*))
        (let ((l (cached-layer model)))
          (sb-thread:with-mutex (*cache-lock*)
            (or (gethash key *layer-pins*) (setf (gethash key *layer-pins*) l)))))))

(defun layer-boundary (model &optional (session (call-session-id)))
  "A session start or a compaction of session with model: the image's cached layer re-read when
the manifest's identity is no longer the one it was read at, and pinned for session until its
next boundary. Returns the loaded layer."
  (when (and model (string/= model ""))
    (let ((id (manifest-identity model)))
      (sb-thread:with-mutex (*cache-lock*)
        (let ((c (gethash model *layer-cache*)))
          (unless (and c (equal (loaded-identity c) id)) (remhash model *layer-cache*))))
      (let ((l (cached-layer model)))
        (sb-thread:with-mutex (*cache-lock*) (setf (gethash (pin-key session model) *layer-pins*) l))
        l))))

(defun forget-session-pins (session)
  "Drops session's pins (its end), so the table never keeps a closed session's layers."
  (sb-thread:with-mutex (*cache-lock*)
    (let ((prefix (concatenate 'string (or session "") (string #\Nul))) (drop '()))
      (maphash (lambda (k v) (declare (ignore v)) (when (om::has-prefix k prefix) (push k drop))) *layer-pins*)
      (dolist (k drop) (remhash k *layer-pins*)))))

(defun load-layer-from-disk (model)
  ;; The identity first: a change written while the files are read leaves an older identity,
  ;; so the next boundary reads again.
  (let* ((identity (manifest-identity model))
         (manifest (read-manifest model))
         (states (replay manifest))
         (state (aref states (1- (length states))))
         (settled '()) (held '()) (reports '()) (refused '()))
    (flet ((try (name digest text state-kind time)
             (if (null text)
                 (push (format nil "~A ~A: its text is missing" (string-downcase state-kind) name) reports)
                 (handler-case
                     (let ((d (make-ldef :model model :name name :text text :digest digest :time time :state state-kind)))
                       (compile-definition d)
                       (if (eq state-kind :settled) (push d settled) (push d held)))
                   (refused (e)
                     (when (eq state-kind :settled) (push (list name digest (refused-reason e)) refused))
                     (push (format nil "~(~A~) ~A: ~A" state-kind name e) reports))
                   (error (e) (push (format nil "~(~A~) ~A: ~A" state-kind name e) reports))))))
      (loop for (name . d) in state
            do (try name d (read-file (blob-path model d)) :settled nil))
      (let ((index (read-held-index model)))
        (dolist (name (dir-file-names (join-path (model-root model) "held") ".lisp"))
          (let* ((text (read-file (held-path model name)))
                 (want (gethash (concatenate 'string +kind-adaptable+ "/" name) index)))
            (cond ((null want) (push (format nil "held ~A: untracked, never loaded" name) reports))
                  ((not (equal want (digest text))) (push (format nil "held ~A: changed outside the store, never loaded" name) reports))
                  (t (try name want text :held (hold-time manifest name want))))))))
    (values (make-loaded :settled (nreverse settled) :held (nreverse held) :reports (nreverse reports)
                         :identity identity)
            (nreverse refused))))

(defun layer-settled (model) (loaded-settled (load-layer model)))

(defun layer-held (model) (loaded-held (load-layer model)))

;;; Changes

(defun layer-hold (model text)
  "Holds text, one (om.tool:define name lambda-list . body) form, in model's layer after the
checks (compile-definition). Returns the held ldef; signals refused."
  (let ((d (make-ldef :model model :text text :digest (digest text) :state :held)))
    ;; Code is never redacted (a redacted literal changes what it does): a definition holding a
    ;; secret is refused whole (codepolicy.SecretInCode, docs/memory-v2.md "Erasure").
    (when (om::contains-secret-p text)
      (refuse "the definition holds a secret: code is never redacted, so it is refused; read the value from where it lives instead of writing it into the code"))
    (compile-definition d)
    (with-layer-lock (model)
      (write-blob model text)
      (write-atomic (held-path model (ldef-name d)) text)
      (let ((index (read-held-index model)))
        (setf (gethash (ldef-key d) index) (ldef-digest d))
        (write-held-index model index))
      (append-generation model "hold" :name (ldef-name d) :digest (ldef-digest d))
      (setf (ldef-time d) (hold-time (read-manifest model) (ldef-name d) (ldef-digest d))))
    (invalidate model)
    d))

(defun drop-held (model name)
  (delete-file-if-exists (held-path model name))
  (let ((index (read-held-index model)))
    (when (nth-value 1 (gethash (concatenate 'string +kind-adaptable+ "/" name) index))
      (remhash (concatenate 'string +kind-adaptable+ "/" name) index)
      (write-held-index model index))))

(defun current-state (model)
  (let ((states (replay (read-manifest model)))) (aref states (1- (length states)))))

(defun layer-holds-p (model name)
  "Whether model's layer holds a settled or held definition of name: what layer-forget removes.
It changes nothing, so a forget with no user's words is checked before it is staged."
  (and (or (assoc name (current-state model) :test #'equal) (file-exists-p (held-path model name))) t))

(defun check-rollback-target (model target)
  "Signals refused unless target is one of model's generations, 0 to the latest."
  (let ((n (manifest-generation (read-manifest model))))
    (unless (and (integerp target) (<= 0 target n))
      (refuse "generation ~A is not one of ~A's layer: 0 to ~D" target model n))))

(defun layer-forget (model name)
  "Forgets name's settled and held definitions: the default stands at once. Returns the
generation, or signals refused when the model's layer holds no definition of the name."
  (let ((gen nil))
    (with-layer-lock (model)
      (let* ((state (current-state model)) (standing (assoc name state :test #'equal)))
        (unless (or standing (file-exists-p (held-path model name)))
          (refuse "~A's layer holds no definition of ~A" model name))
        (drop-held model name)
        (setf gen (append-generation model "forget" :name name :digest (cdr standing)))
        (mirror-settled model (current-state model))))
    (invalidate model)
    gen))

(defun layer-rollback (model target)
  "Makes the model's settled layer what it was after generation target (0: before its first
change, every default standing) and drops every held definition. Returns the new generation."
  (let ((gen nil))
    (with-layer-lock (model)
      (let ((n (manifest-generation (read-manifest model))))
        (unless (and (integerp target) (<= 0 target n))
          (refuse "generation ~A is not one of ~A's layer: 0 to ~D" target model n)))
      (dolist (name (dir-file-names (join-path (model-root model) "held") ".lisp"))
        (drop-held model name))
      (setf gen (append-generation model "rollback" :target target))
      (mirror-settled model (current-state model)))
    (invalidate model)
    gen))

(defun layer-settle (model events &key (only nil only-p))
  "Settles model's held definitions on the usage ledger's events (settle-v2): each one decided
is settled when clean and reverted otherwise; one with no evidence yet stays held. With only, a
list of held ids (a compaction's snapshot), only those are settled, so a definition held during
the compaction is never settled by it. Returns the notices, (name written-p reason) each."
  (let ((notices '()))
    (dolist (d (if only-p
                   (remove-if-not (lambda (d) (member (ldef-held-id d) only :test #'string=)) (layer-held model))
                   (layer-held model)))
      (multiple-value-bind (decided clean reason)
          (settle-v2 events :kind +kind-adaptable+ :name (ldef-name d) :held-id (ldef-held-id d) :time (ldef-time d))
        (when decided
          (with-layer-lock (model)
            ;; Only while the held file still holds the text the evidence judged.
            (when (equal (digest (or (read-file (held-path model (ldef-name d))) "")) (ldef-digest d))
              (drop-held model (ldef-name d))
              (append-generation model (if clean "settle" "revert") :name (ldef-name d) :digest (ldef-digest d) :reason reason)
              (mirror-settled model (current-state model))
              (push (list (ldef-name d) clean reason) notices))))))
    (invalidate model)
    (nreverse notices)))

(defun layer-generations (model)
  "The model's latest generation (0 before its first change)."
  (manifest-generation (read-manifest model)))
