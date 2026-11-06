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

(defun read-manifest (model)
  "The manifest's generation lines, in order; a line without an op is skipped."
  (remove-if-not (lambda (g) (stringp (om::jget g "op"))) (read-model-events (manifest-path model))))

(defun append-generation (model op &key name digest reason target)
  "Appends generation n+1 and returns n+1. Members in the schema's order."
  (let ((gen (1+ (length (read-manifest model)))))
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
                   (write-char #\} out)))
    gen))

(defun replay (manifest)
  "The settled state after each generation: a vector whose element g is an alist name -> digest
(element 0 is before the first change)."
  (let ((states (make-array 1 :adjustable t :fill-pointer t :initial-contents '(())))
        (state '()))
    (dolist (g manifest)
      (let ((op (om::jget g "op")) (name (om::jget g "name")) (digest (om::jget g "digest")))
        (cond ((string= op "settle")
               (setf state (acons name digest (remove name state :key #'car :test #'equal))))
              ((string= op "forget")
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

;;; The cache: each model's loaded layer, dropped by every change this image makes.

(defvar *layer-cache* (make-hash-table :test 'equal))
(defvar *cache-lock* (sb-thread:make-mutex :name "om.layer cache"))

(defun invalidate (model)
  (sb-thread:with-mutex (*cache-lock*) (remhash model *layer-cache*)))

(defstruct loaded settled held reports)

(defun load-layer (model)
  "The model's layer: settled and held definitions, each re-read and compiled (adaptable.lisp's
compile-definition); one that no longer passes is left out and reported."
  (or (sb-thread:with-mutex (*cache-lock*) (gethash model *layer-cache*))
      (let ((l (load-layer-from-disk model)))
        (sb-thread:with-mutex (*cache-lock*) (setf (gethash model *layer-cache*) l))
        l)))

(defun load-layer-from-disk (model)
  (let* ((manifest (read-manifest model))
         (states (replay manifest))
         (state (aref states (1- (length states))))
         (settled '()) (held '()) (reports '()))
    (flet ((try (name digest text state-kind time)
             (if (null text)
                 (push (format nil "~A ~A: its text is missing" (string-downcase state-kind) name) reports)
                 (handler-case
                     (let ((d (make-ldef :model model :name name :text text :digest digest :time time :state state-kind)))
                       (compile-definition d)
                       (if (eq state-kind :settled) (push d settled) (push d held)))
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
    (make-loaded :settled (nreverse settled) :held (nreverse held) :reports (nreverse reports))))

(defun layer-settled (model) (loaded-settled (load-layer model)))

(defun layer-held (model) (loaded-held (load-layer model)))

;;; Changes

(defun layer-hold (model text)
  "Holds text, one (om.tool:define name lambda-list . body) form, in model's layer after the
checks (compile-definition). Returns the held ldef; signals refused."
  (let ((d (make-ldef :model model :text text :digest (digest text) :state :held)))
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
      (let ((n (length (read-manifest model))))
        (unless (and (integerp target) (<= 0 target n))
          (refuse "generation ~A is not one of ~A's layer: 0 to ~D" target model n)))
      (dolist (name (dir-file-names (join-path (model-root model) "held") ".lisp"))
        (drop-held model name))
      (setf gen (append-generation model "rollback" :target target))
      (mirror-settled model (current-state model)))
    (invalidate model)
    gen))

(defun layer-settle (model events)
  "Settles model's held definitions on the usage ledger's events (settle-v2): each one decided
is settled when clean and reverted otherwise; one with no evidence yet stays held. Returns the
notices, (name written-p reason) each."
  (let ((notices '()))
    (dolist (d (layer-held model))
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

(defun layer-generations (model) (length (read-manifest model)))
