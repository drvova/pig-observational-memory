;;;; The Go layer's remedy definitions (memory/layer: layer.go, held.go, manifest.go), as far as
;;;; the rules need them: the harness's rules and the models' remedy catalogues live in the curated
;;;; store's layer (<store>/harness/<model dir>/remedy/<name>.rows, a header table, a blank line
;;;; and the text), recorded by each model's manifest.jsonl and, for held ones, held/index.json.
;;;; The image reads them as Go's layer.Store.Load does (a file the manifest or the held index does
;;;; not record is untracked and never loaded; a stale override of a seed is skipped; only memory
;;;; v2's holds) and writes a rule as layer.Store.PutLocked does (blob, file, one put
;;;; generation), under the store's lock, so the Go engine reads every file the image writes.
;;;;
;;;; Not ported: the kernel floor's name check (a rule's name is a 12-hex hash and a catalogue's
;;;; is a shipped seed, neither kernel-owned), the other kinds, and the legacy rules directory's
;;;; migration (<store>/rules/*.rows): a store that still has one is refused by the callers.

(in-package "OM.LAYER")

(defparameter +go-all-models+ "*")
(defparameter +kind-remedy+ "remedy")
(defparameter +layer-header-table+ "layer")
(defparameter +layer-header-fields+ '("kind" "name" "model" "time" "session" "overrides" "fallback" "mode"))

(defun go-layer-dir (store) (join-path store "harness"))
(defun go-model-path (store model) (join-path (go-layer-dir store) (model-dir model)))
(defun go-def-path (store model name) (join-path (go-model-path store model) +kind-remedy+ (concatenate 'string name ".rows")))
(defun go-held-path (store model name)
  (join-path (go-model-path store model) "held" +kind-remedy+ (concatenate 'string name ".rows")))
(defun go-blob-path (store model digest) (join-path (go-model-path store model) "blobs" (concatenate 'string digest ".rows")))

(defun go-valid-name-p (name)
  "layer.validName: ^[a-z0-9][a-z0-9-]{0,63}$."
  (and (<= 1 (length name) 64)
       (every (lambda (c) (or (char<= #\a c #\z) (char<= #\0 c #\9) (char= c #\-))) name)
       (char/= (char name 0) #\-)))

(defun model-of-dir (dir)
  "layer.ModelOfDir: the model id a directory name stands for, nil for one ModelDir never makes."
  (let ((bytes '()) (i 0) (n (length dir)))
    (loop while (< i n)
          do (let ((c (char dir i)))
               (if (char/= c #\_)
                   (progn (push (char-code c) bytes) (incf i))
                   (let ((v (and (<= (+ i 3) n) (ignore-errors (parse-integer dir :start (1+ i) :end (+ i 3) :radix 16)))))
                     (unless (and v (every (lambda (ch) (digit-char-p ch 16)) (subseq dir (1+ i) (+ i 3))))
                       (return-from model-of-dir nil))
                     (push v bytes) (incf i 3)))))
    (let ((model (handler-case (sb-ext:octets-to-string (coerce (nreverse bytes) '(vector (unsigned-byte 8)))
                                                       :external-format :utf-8)
                   (error () nil))))
      (and model (string/= model "") (string= (model-dir model) dir) model))))

;;; The manifest (manifest.go Generations, states)

(defun go-generations (store model)
  "layer.Store.Generations: the manifest's lines that decode with an op, in order."
  (remove-if-not (lambda (g) (let ((op (om::jget g "op"))) (and (stringp op) (string/= op ""))))
                 (read-model-events (join-path (go-model-path store model) "manifest.jsonl"))))

(defun go-current (store model)
  "layer.Store.current: each kind/name's digest after the latest generation (states)."
  (let ((states (list (make-hash-table :test 'equal))))
    (dolist (g (go-generations store model))
      (let ((next (make-hash-table :test 'equal)) (op (om::jget g "op")) (target (om::jget g "target")))
        (if (and (string= op "rollback") (integerp target) (>= target 0) (< target (length states)))
            (let ((prev (first states)) (keep (om::jget g "keep")))
              (maphash (lambda (k v) (setf (gethash k next) v)) (nth (- (length states) 1 target) states))
              (when (and (vectorp keep) (not (stringp keep)))
                (loop for k across keep when (stringp k)
                      do (multiple-value-bind (v ok) (gethash k prev)
                           (if ok (setf (gethash k next) v) (remhash k next))))))
            (let ((key (concatenate 'string (om::str (om::jget g "kind")) "/" (om::str (om::jget g "name"))))
                  (digest (om::str (om::jget g "digest"))))
              (maphash (lambda (k v) (setf (gethash k next) v)) (first states))
              (cond ((or (string= op "forget") (and (string= op "revert") (string= digest "")))
                     (remhash key next))
                    ((or (string= op "put") (string= op "revert")
                         (and (string= op "migrate") (not (eq (om::jget g "held") :true))))
                     (setf (gethash key next) digest)))))
        (push next states)))
    (first states)))

(defun go-held-index (store model)
  "layer.Store.heldIndex: a missing or unreadable index records nothing."
  (let* ((text (read-file (join-path (go-model-path store model) "held" "index.json")))
         (v (and text (handler-case (om::json-parse text) (error () nil)))))
    (if (and (hash-table-p v) (loop for x being the hash-values of v always (stringp x)))
        v
        (make-hash-table :test 'equal))))

;;; Files (layer.go encodeFile, decodeFile, load)

(defstruct gdef kind name model time session mode engine text seed seed-digest fallback (state :written))

(defun gdef-key (d) (concatenate 'string (gdef-kind d) "/" (gdef-name d)))

(defun gdef-held-id (d)
  "layer.Definition.HeldID for a kind with no mode line: kind/name@digest of the text."
  (concatenate 'string (gdef-key d) "@" (digest (gdef-text d))))

(defun go-encode-file (d fallback)
  (let* ((overrides (if (string/= (or (gdef-seed d) "") "") (concatenate 'string (gdef-seed d) "@" (gdef-seed-digest d)) ""))
         (row (list (gdef-kind d) (gdef-name d) (gdef-model d) (format-rfc3339-nano (cons (time-nanos (gdef-time d)) 0))
                    (or (gdef-session d) "") overrides fallback (or (gdef-mode d) "")))
         (fields +layer-header-fields+))
    (when (string/= (or (gdef-engine d) "") "")
      (setf fields (append fields '("engine")) row (append row (list (gdef-engine d)))))
    (concatenate 'string (rows-encode-row +layer-header-table+ fields row) (string #\Newline) (string #\Newline)
                 (string-right-trim '(#\Newline) (gdef-text d)) (string #\Newline))))

(defun go-decode-file (content)
  "layer.decodeFile: the definition (its fallback in gdef-fallback), or nil."
  (let* ((nl1 (position #\Newline content))
         (nl2 (and nl1 (position #\Newline content :start (1+ nl1)))))
    (when (and nl2 (< (1+ nl2) (length content)) (char= (char content (1+ nl2)) #\Newline))
      (handler-case
          (multiple-value-bind (name fields rows) (rows-decode-table (subseq content 0 nl2))
            (when (and (string= name +layer-header-table+) (= (length rows) 1))
              (let* ((row (first rows)) (cell (lambda (f) (rows-cell fields row f)))
                     (overrides (funcall cell "overrides"))
                     (d (make-gdef :kind (funcall cell "kind") :name (funcall cell "name") :model (funcall cell "model")
                                   :session (funcall cell "session") :mode (funcall cell "mode")
                                   :engine (funcall cell "engine") :fallback (funcall cell "fallback")
                                   :time (parse-rfc3339 (funcall cell "time"))
                                   :text (string-right-trim '(#\Newline) (subseq content (+ nl2 2))))))
                (if (string= overrides "")
                    d
                    (let ((at (position #\@ overrides)))
                      (when (and at (> at 0) (< (1+ at) (length overrides)))
                        (setf (gdef-seed d) (subseq overrides 0 at) (gdef-seed-digest d) (subseq overrides (1+ at)))
                        d))))))
        (rows-error () nil)))))

(defun go-stale-reason (d)
  "layer.Store.stale for a remedy definition: \"\" when it is not a stale override."
  (let ((seed (gethash (gdef-name d) (catalogue-seeds))))
    (cond ((and (null (gdef-seed d)) seed) "a seed ships since it was written")
          ((null (gdef-seed d)) "")
          ((string/= (gdef-seed d) (gdef-key d)) "it overrides another name")
          ((null seed) "the seed it overrides no longer ships")
          ((string/= (digest seed) (gdef-seed-digest d)) "the seed it overrides changed")
          (t ""))))

(defun go-check-remedy (d)
  "layer.Store.checkStored for a remedy definition: no mode, a model, a valid name, a text the
rows codec reads (the kernel's fail-closed kinds), and the catalogue's own check."
  (and (string= (or (gdef-mode d) "") "") (string/= (om::trim-space (gdef-model d)) "")
       (go-valid-name-p (gdef-name d)) (string/= (om::trim-space (gdef-text d)) "")
       (handler-case (progn (rows-decode-table (gdef-text d)) t) (rows-error () nil))
       (validate-remedy d)))

(defun go-load (path model name recorded)
  "layer.Store.load of a remedy file: the definition, or nil when Go would report it."
  (let* ((content (read-file path)) (d (and content (go-decode-file content))))
    (and d (string= (gdef-kind d) +kind-remedy+) (string= (gdef-name d) name) (string= (gdef-model d) model)
         (go-check-remedy d)
         (equal (gethash (concatenate 'string +kind-remedy+ "/" name) recorded) (digest content))
         (string= (go-stale-reason d) "")
         d)))

(defun go-rows-names (dir)
  (remove-if-not #'go-valid-name-p (dir-file-names dir ".rows")))

(defun go-list-remedies (store model)
  "layer.Store.List(model, remedy): the written remedy definitions, by name."
  (let ((recorded (go-current store model)))
    (loop for name in (go-rows-names (join-path (go-model-path store model) +kind-remedy+))
          for d = (go-load (go-def-path store model name) model name recorded)
          when d collect d)))

(defun go-held-remedies (store model)
  "layer.Store.held for the remedy kind: memory v2's held remedy definitions of model."
  (let ((index (go-held-index store model)))
    (loop for name in (go-rows-names (join-path (go-model-path store model) "held" +kind-remedy+))
          for d = (go-load (go-held-path store model name) model name index)
          when (and d (equal (gdef-engine d) "v2"))
            collect (progn (setf (gdef-state d) :held) d))))

(defun go-models (store)
  "layer.Store.Models: the models the layer holds a directory for, sorted."
  (let ((out '()))
    (dolist (p (directory (concatenate 'string (string-right-trim "/" (go-layer-dir store)) "/*/") :resolve-symlinks nil))
      (let* ((name (car (last (pathname-directory p)))) (model (and (stringp name) (model-of-dir name))))
        (when model (push model out))))
    (sort out #'string<)))

(defun go-effective-remedies (store model)
  "layer.Store.Load(model)'s remedy definitions: for all models, then the model's own, written
then held, a later one replacing an earlier of its name. An alist name -> gdef, sorted."
  (let ((h (make-hash-table :test 'equal)))
    (dolist (m (if (and model (string/= model "") (string/= model +go-all-models+))
                   (list +go-all-models+ model)
                   (list +go-all-models+)))
      (dolist (d (go-list-remedies store m)) (setf (gethash (gdef-name d) h) d))
      (dolist (d (go-held-remedies store m)) (setf (gethash (gdef-name d) h) d)))
    (sort (loop for k being the hash-keys of h using (hash-value v) collect (cons k v)) #'string< :key #'car)))

;;; Writing (layer.go PutLocked, putStamped, write; manifest.go appendGeneration)

(defun go-append-generation (store model op &key kind name digest)
  (let* ((gen (1+ (length (go-generations store model))))
         (line (with-output-to-string (out)
                 (format out "{\"gen\":~D,\"time\":" gen)
                 (om::write-json-string (format-rfc3339-nano (cons (time-nanos (now)) 0)) out)
                 (write-string ",\"op\":" out) (om::write-json-string op out)
                 (when kind (write-string ",\"kind\":" out) (om::write-json-string kind out))
                 (when name (write-string ",\"name\":" out) (om::write-json-string name out))
                 (when digest (write-string ",\"digest\":" out) (om::write-json-string digest out))
                 (write-char #\} out))))
    (append-line (join-path (go-model-path store model) "manifest.jsonl") line)
    gen))

(defun go-put-remedy (store model name text)
  "layer.Store.PutLocked of a remedy definition (the caller holds the store's lock): written
again only when the file the manifest records holds another text; returns t when it wrote."
  (let* ((text (string-right-trim '(#\Newline) text))
         (seed (gethash name (catalogue-seeds)))
         (d (make-gdef :kind +kind-remedy+ :name name :model model :text text :session "" :mode "" :engine ""
                       :time (now) :seed (and seed (concatenate 'string +kind-remedy+ "/" name))
                       :seed-digest (and seed (digest seed)))))
    (unless (go-check-remedy d) (error "the remedy definition ~A is refused" name))
    (let* ((path (go-def-path store model name)) (old (read-file path)) (recorded (go-current store model)))
      (when (and old (equal (gethash (gdef-key d) recorded) (digest old)))
        (let ((o (go-decode-file old)))
          (when (and o (equal (gdef-kind o) (gdef-kind d)) (equal (gdef-name o) name) (equal (gdef-model o) model)
                     (equal (gdef-text o) text) (equal (gdef-seed o) (gdef-seed d))
                     (equal (gdef-seed-digest o) (gdef-seed-digest d)) (equal (or (gdef-mode o) "") ""))
            (return-from go-put-remedy nil))))
      (let* ((content (go-encode-file d "")) (dg (digest content)))
        (unless (file-exists-p (go-blob-path store model dg)) (write-atomic (go-blob-path store model dg) content))
        (write-atomic path content)
        (go-append-generation store model "put" :kind +kind-remedy+ :name name :digest dg)
        t))))

(defun go-legacy-rules-p (store)
  "Whether the store still has rules written before the layer (<store>/rules/*.rows), which only
Go migrates (rules.migrate)."
  (and (dir-file-names (join-path store "rules") ".rows") t))
