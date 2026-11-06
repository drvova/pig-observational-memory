;;;; The Go layer for the session start (docs/lisp-port-migration.md): what harness.Manager.NoteRetired
;;;; and harness.MigrateLayerV2 read and write in <curated store>/harness, built on the evidence
;;;; port's Go-layer reader (lisp/src/rules/golayer.lisp: the header codec, the manifest, the held
;;;; index, the put generation). It adds:
;;;;
;;;;   - the instructions and tool kinds' checks, with the kernel's check (kernel.Check, Owns) re-run
;;;;     on every load, the floor engaged (a lowered floor is refused by server/migration.lisp);
;;;;   - the tool definitions kept for all models and the legacy definitions/ files they are moved
;;;;     from (curated.Store.ListDefinitions, RefusedDefinitions, migrateDefinitions), so the image
;;;;     names the retired defined queries harness.RetiredQueries names;
;;;;   - the v1 instructions migrations: layer.Store.MigrateInstructions, MigrateToV2 and
;;;;     MigrateWorkerToV2, writing the same files, blobs, held index and manifest lines Go writes.
;;;;
;;;; kernel.Check decides by whether the rows codec reads a text (a tool must decode, fail closed),
;;;; so this file has a strict port of tables/rows_decode.go (it refuses exactly what Go refuses);
;;;; the evidence port's lenient reader is kept for the headers it reads. Loading a kind other than
;;;; instructions or tool signals an error rather than skip its validator.

(defpackage "OM.GOLAYER" (:use "COMMON-LISP"))

(in-package "OM.GOLAYER")

;;; Constants (layer.go, agents/prompts.go)

(defparameter +kinds+ '("format" "schema" "instructions" "summary" "tool" "detector" "remedy")
  "layer.Kinds, in order.")
(defparameter +kind-instructions+ "instructions")
(defparameter +kind-tool+ "tool")
(defparameter +all-models+ "*")
(defparameter +mode-extend+ "extend")
(defparameter +mode-replace+ "replace")
(defparameter +op-put+ "put")
(defparameter +op-migrate+ "migrate")
(defparameter +op-migrate-v2+ "migrate-v2")

(defparameter +instructions-observer+ "observer")
(defparameter +instructions-reflector+ "reflector")
(defparameter +instructions-dropper+ "dropper")
(defparameter +instructions-consolidation+ "consolidation")

;; agents.ConsolidationInstructions, the one seed the instructions kind ships. memory/contracts
;; (TestLispConsolidationSeed) checks this copy against Go's, so an upgrade of the seed fails there.
(defparameter +consolidation-instructions+ "What is worth doing:
- Label a candidate when its subject or kind is not what code guessed; reuse a listed subject before making a new one.
- Merge records that say one thing twice; keep records apart when they differ in any specific.
- Write a reflection only for a conclusion several records support that will still matter after this compaction.
- Settle a possible update only when the conversation shows which value stands.
- Cite a curated memory only when a shown record of the user's own words bears it out.
- Doing nothing is a valid answer: a record left alone stays exactly as code extracted it.
")

(defun seeds ()
  "layer.Shipped for the kinds the port reads: instructions/consolidation (no tool seed ships)."
  (list (cons (seed-key +kind-instructions+ +instructions-consolidation+) +consolidation-instructions+)))

(defun seed-text (key) (cdr (assoc key (seeds) :test #'string=)))

(defun seed-key (kind name) (concatenate 'string kind "/" name))

(defun cat (&rest parts) (apply #'concatenate 'string parts))

(defun trim-right-newlines (s) (string-right-trim '(#\Newline) s))

(defun digest-octets (octets)
  "layer.Digest over bytes."
  (subseq (om::sha256-octets octets) 0 16))

(defun digest (text) (digest-octets (om::utf8-octets text)))

;;; Files

(defun read-octets (path)
  "os.ReadFile: the bytes, or nil when the file is missing or unreadable."
  (handler-case
      (with-open-file (in path :element-type '(unsigned-byte 8) :if-does-not-exist nil)
        (when in
          (let ((bytes (make-array (file-length in) :element-type '(unsigned-byte 8))))
            (read-sequence bytes in)
            bytes)))
    (error () nil)))

(defun octets-text (octets)
  (sb-ext:octets-to-string octets :external-format '(:utf-8 :replacement #.(code-char #xfffd))))

(defun read-text (path)
  (let ((o (read-octets path))) (and o (octets-text o))))

(defun path-exists-p (path)
  "os.Stat succeeded (a dangling link does not)."
  (handler-case (progn (sb-posix:stat path) t) (error () nil)))

(defun list-dir (dir)
  "os.ReadDir: (name . directory-p) for each entry, sorted by name; () for a missing directory.
A symbolic link is not a directory, as a DirEntry reads it."
  (let ((out '()) (d nil))
    (handler-case (setf d (sb-posix:opendir dir)) (error () (return-from list-dir '())))
    (unwind-protect
         (loop for ent = (sb-posix:readdir d)
               until (sb-alien:null-alien ent)
               do (let ((name (sb-posix:dirent-name ent)))
                    (unless (member name '("." "..") :test #'string=)
                      (push (cons name (handler-case
                                           (sb-posix:s-isdir (sb-posix:stat-mode (sb-posix:lstat (cat dir "/" name))))
                                         (error () nil)))
                            out))))
      (sb-posix:closedir d))
    (sort out #'string< :key #'car)))

(defun write-atomic (path content) (om.layer::write-atomic path content))

(defun rename-file* (from to) (sb-posix:rename from to))

;;; The rows codec's decoder (tables/rows_decode.go), for tables only, as tables.Rows.Decode.
;;; It refuses exactly what Go's refuses; its messages are shorter than tables.Error's.

(defstruct tbl name fields rows absent)

(define-condition rows-error (error) ((message :initarg :message :reader rows-error-message))
  (:report (lambda (c s) (write-string (rows-error-message c) s))))

(defun rows-fail (line fmt &rest args)
  (error 'rows-error :message (format nil "line ~D: ~A" (1+ line) (apply #'format nil fmt args))))

(defun name-char-p (c) (or (om::letter-p c) (om::digit-p c) (find c "_-.")))

(defun scan-name (s p)
  (loop while (and (< p (length s)) (name-char-p (char s p))) do (incf p))
  p)

(defun tables-valid-name-p (s) (and (string/= s "") (= (scan-name s 0) (length s))))

(defun row-line-p (line)
  (and (>= (length line) 2) (string= (subseq line 0 2) "  ")
       (or (= (length line) 2) (not (find (char line 2) '(#\Space #\Tab))))))

(defun starts-with-space-p (s) (and (plusp (length s)) (om::space-p (char s 0))))

(defun decode-quoted (line i p)
  "(values value end) for the quoted value opening at p."
  (let ((out (make-string-output-stream)))
    (loop for k from (1+ p) below (length line)
          do (let ((c (char line k)))
               (cond ((char= c #\") (return-from decode-quoted (values (get-output-stream-string out) (1+ k))))
                     ((char/= c #\\) (write-char c out))
                     ((= (1+ k) (length line)) (return))
                     (t (incf k)
                        (case (char line k)
                          (#\" (write-char #\" out))
                          (#\\ (write-char #\\ out))
                          (#\n (write-char #\Newline out))
                          (#\r (write-char #\Return out))
                          (#\t (write-char #\Tab out))
                          (t (rows-fail i "unknown escape \\~C" (char line k))))))))
    (rows-fail i "unterminated quote")))

(defun decode-fields (line i p)
  "decodeFields: (values values absent), failing on what Go reports (a space error included,
which Go reports after a count check that fails either way)."
  (let ((vals '()) (absent '()) (space nil))
    (loop
      (if (and (< p (length line)) (char= (char line p) #\"))
          (multiple-value-bind (v end) (decode-quoted line i p)
            (push v vals) (push nil absent)
            (when (= end (length line)) (return))
            (unless (char= (char line end) #\,) (rows-fail i "text after the closing quote"))
            (setf p (1+ end)))
          (let* ((k (position #\, line :start p))
                 (f (subseq line p (or k (length line)))))
            (cond ((starts-with-space-p f) (setf space t))
                  ((find #\" f) (rows-fail i "quote inside an unquoted value")))
            (push f vals) (push (string= f "") absent)
            (unless k (return))
            (setf p (1+ k)))))
    (values (nreverse vals) (nreverse absent) space)))

(defun decode-table-block (lines i)
  "decodeBlock for a table: (values table next-line); any other block fails, as decodeTables
refuses a scalar or a list."
  (let* ((line (aref lines i)) (p (scan-name line 0)))
    (when (= p 0) (rows-fail i "expected a name"))
    (when (= p (length line)) (rows-fail i "expected ':' or '[' after the name"))
    (unless (char= (char line p) #\[) (rows-fail i "expected a table"))
    (let* ((name (subseq line 0 p)) (q (1+ p)))
      (loop while (and (< q (length line)) (char<= #\0 (char line q) #\9)) do (incf q))
      (when (= q (1+ p)) (rows-fail i "expected a count in [N]"))
      (when (or (= q (length line)) (char/= (char line q) #\])) (rows-fail i "expected ']' after the count"))
      (let ((n (parse-integer line :start (1+ p) :end q)) (p (1+ q)))
        (when (> n most-positive-fixnum) (rows-fail i "count too large"))
        (when (or (= p (length line)) (char/= (char line p) #\{)) (rows-fail i "expected a table"))
        (let ((end (position #\} line :start p)) (fields '()))
          (unless end (rows-fail i "unterminated field list"))
          (dolist (f (om::split (subseq line (1+ p) end) ","))
            (unless (tables-valid-name-p f) (rows-fail i "bad field name ~S" f))
            (when (member f fields :test #'string=) (rows-fail i "duplicate field ~S" f))
            (push f fields))
          (setf fields (nreverse fields))
          (when (or (= (1+ end) (length line)) (char/= (char line (1+ end)) #\:))
            (rows-fail i "expected ':' after the field list"))
          (when (/= (+ end 2) (length line)) (rows-fail i "unexpected text after the table header"))
          (let ((rows '()) (absent '()) (any-absent nil) (j (1+ i)))
            (loop while (and (< j (length lines)) (string/= (aref lines j) "")
                             (find (char (aref lines j) 0) '(#\Space #\Tab)))
                  do (let ((row (aref lines j)))
                       (unless (row-line-p row) (rows-fail j "bad indentation"))
                       (when (= (length rows) n) (rows-fail j "table ~A has more rows than [~D]" name n))
                       (multiple-value-bind (vals abs space) (decode-fields row j 2)
                         (when (/= (length vals) (length fields)) (rows-fail j "row ~D has ~D fields, header has ~D" (1+ (length rows)) (length vals) (length fields)))
                         (when space (rows-fail j "space after comma"))
                         (when (some #'identity abs) (setf any-absent t))
                         (push vals rows) (push abs absent)))
                     (incf j))
            (when (< (length rows) n) (rows-fail i "table ~A has ~D rows, header says [~D]" name (length rows) n))
            (values (make-tbl :name name :fields fields :rows (nreverse rows)
                              :absent (and any-absent (nreverse absent)))
                    j)))))))

(defun rows-decode (text)
  "tables.Rows.Decode: the tables, or a rows-error."
  (let ((lines (map 'vector (lambda (l) (if (and (plusp (length l)) (char= (char l (1- (length l))) #\Return))
                                             (subseq l 0 (1- (length l))) l))
                    (om::split text (string #\Newline))))
        (out '()) (i 0))
    (loop while (< i (length lines))
          do (let ((line (aref lines i)))
               (cond ((string= line "") (incf i))
                     ((find (char line 0) '(#\Space #\Tab)) (rows-fail i "bad indentation"))
                     (t (multiple-value-bind (tb next) (decode-table-block lines i)
                          (push tb out) (setf i next))))))
    (nreverse out)))

(defun try-rows-decode (text)
  "(values tables nil) or (values nil error-message)."
  (handler-case (values (rows-decode text) nil)
    (rows-error (e) (values nil (rows-error-message e)))))

(defun tbl-index (tb field) (or (position field (tbl-fields tb) :test #'string=) -1))

(defun tbl-absent-p (tb r c)
  (let ((row (nth r (tbl-absent tb)))) (and row (< c (length row)) (nth c row))))

(defun tbl-cell (tb r field)
  "Table.Cell: (values value present)."
  (let ((c (tbl-index tb field)))
    (if (or (< c 0) (>= r (length (tbl-rows tb))) (>= c (length (nth r (tbl-rows tb)))) (tbl-absent-p tb r c))
        (values "" nil)
        (values (nth c (nth r (tbl-rows tb))) t))))

;;; The kernel (kernel/kernel.go): Owns and Check, the floor engaged.

(defparameter +owned+
  ;; kernel.owned without the code group (which Owns never matches for a layer kind): (name owner
  ;; kinds), kinds () for every kind; a name owned for every kind is added once, with its first owner.
  (let ((out '()))
    (flet ((add (names owner &optional kinds)
             (dolist (n names)
               (unless (find-if (lambda (e) (and (null (third e)) (string= (om::kernel-normalize-name (first e))
                                                                          (om::kernel-normalize-name n))))
                                out)
                 (setf out (append out (list (list n owner kinds))))))))
      (setf out (list (list "memory" "the memory tool" '())))
      (let ((ledger '("detector" "remedy" "summary")))
        (add '("notes" "reflections" "history" "saved" "recall") "the query engine (a source)")
        (add '("search" "where" "since" "until" "from" "through" "sort" "limit" "count" "group") "the query engine (a stage)")
        (add '("save" "edit" "forget" "feedback" "define" "propose" "harness" "rollback") "the query engine (a write)")
        (add '("om.observations.recorded" "om.reflections.recorded" "om.observations.dropped" "om.folded" "om.subjects.aliased")
             "storage (an om.* record kind)")
        (add '("memory" "decode" "defined" "pass" "detect" "ok" "error" "empty" "shown" "omitted" "columns")
             "the event ledger (a kernel event)" ledger)
        (add '("room" "room-ceiling" "room-budget" "summary-budget") "the room ceiling" ledger)
        (add '("compactAfterTokens" "compactAfterTokensMode" "compactAfterTokensRatio"
               "compactionMaxRetainedTokens" "compactionSummaryMaxTokens" "compactionCatchUpMaxChunks"
               "observationsPoolMaxTokens" "observationsPoolTargetTokens"
               "coverage" "coverage-marker" "cut-point" "fold-boundary" "firstKeptEntryId" "foldThroughEntryId"
               "kernelFloor")
             "the compaction invariants (a setting or boundary)")
        (add '("kernel-consolidate") "the kernel's fixed prompt rules")
        (add '("kernel-observer" "kernel-reflector" "kernel-dropper" "kernel-builder") "the kernel's fixed prompt rules")
        (add '("id" "timestamp" "content" "sources" "sourceEntryIds" "relevance") "the note schema's kernel fields" '("schema"))
        (add '("status" "event_date" "source" "source_role" "source_time") "the note schema's kernel fields" '("schema"))
        (add '("subject-key" "canonical-subjects" "raw_subject" "subject_scope") "the kernel's subject canonicalization")))
    out))

(defun kernel-owns (kind name)
  "kernel.Owns for a layer kind: the owner, or nil."
  (let ((key (om::kernel-normalize-name name)))
    (unless (string= key "")
      (dolist (e +owned+)
        (when (and (string= (om::kernel-normalize-name (first e)) key)
                   (or (null (third e)) (member kind (third e) :test #'string=)))
          (return (second e)))))))

(defun kernel-refusal (kind name field owner found)
  (format nil "refused: the ~A definition ~A names ~A~A, which ~A owns; the kernel cannot be replaced, shadowed or disabled from the layer"
          kind (om::go-quote name) (om::go-quote (or found name))
          (if (and (string/= field "") (string/= field "name")) (format nil " (in its ~A)" field) "")
          owner))

(defun kernel-check (kind name text)
  "kernel.Check: the refusal's message, or nil."
  (let ((owner (kernel-owns kind name)))
    (when owner (return-from kernel-check (kernel-refusal kind name "name" owner nil))))
  (multiple-value-bind (decoded err) (try-rows-decode text)
    (when err
      (return-from kernel-check
        (and (member kind '("tool" "schema" "remedy") :test #'string=)
             (format nil "refused: the ~A definition ~A is not a table the kernel can read, so it cannot be checked: ~A"
                     kind (om::go-quote name) err))))
    (dolist (tb decoded)
      (dolist (column '("name" "field" "setting" "tool"))
        (let ((c (tbl-index tb column)))
          (when (>= c 0)
            (loop for row in (tbl-rows tb) for r from 0
                  unless (tbl-absent-p tb r c)
                    do (let* ((value (nth c row)) (owner (kernel-owns kind value)))
                         (when owner
                           (return-from kernel-check (kernel-refusal kind name column owner value)))))))))
    nil))

;;; Definitions and layer files: the evidence port's Go-layer reader (lisp/src/rules/golayer.lisp:
;;; gdef, go-encode-file, go-decode-file, the manifest, the held index, go-append-generation), with
;;; what the migration needs beyond remedies: the instructions and tool kinds' checks, the kernel's
;;; check on every load, the held index written, and the writes of MigrateInstructions and MigrateToV2.

(defun s0 (v) (or v ""))

(defun def-key (d) (seed-key (om.layer::gdef-kind d) (om.layer::gdef-name d)))

(defun def-replaces-p (d)
  (and (string= (om.layer::gdef-kind d) +kind-instructions+) (string= (s0 (om.layer::gdef-mode d)) +mode-replace+)))

(defun layer-valid-name-p (name) (om.layer::go-valid-name-p name))

(defun now () (om.layer::now))

(defun encode-file (d fallback) (om.layer::go-encode-file d fallback))

(defun decode-file (content) (om.layer::go-decode-file content))

;;; Checks (Store.checkStored, checkMode, checkLifted, stale)

(defun check-mode (d)
  (let ((kind (om.layer::gdef-kind d)) (mode (s0 (om.layer::gdef-mode d))) (name (om.layer::gdef-name d)))
    (if (string/= kind +kind-instructions+)
        (and (string/= mode "")
             (format nil "only an instructions definition has a mode, not the ~A definition ~A" kind (om::go-quote name)))
        (and (not (member mode (list "" +mode-extend+ +mode-replace+) :test #'string=))
             (format nil "the instructions definition ~A has mode ~A; a mode is ~A or ~A"
                     (om::go-quote name) (om::go-quote mode) +mode-extend+ +mode-replace+)))))

(defun check-stored (d)
  "Store.checkStored for the instructions and tool kinds: the refusal's message, or nil."
  (let ((kind (om.layer::gdef-kind d)) (name (om.layer::gdef-name d)) (text (s0 (om.layer::gdef-text d))))
    (cond ((not (member kind +kinds+ :test #'string=))
           (format nil "unknown definition kind ~A; kinds are ~A" (om::go-quote kind) (om::join +kinds+ ", ")))
          ((check-mode d))
          ((string= (om::trim-space (s0 (om.layer::gdef-model d))) "")
           (cat "a definition needs the model it applies to, or " +all-models+))
          ((kernel-check kind name text))
          ((not (layer-valid-name-p name))
           (format nil "definition name ~A must be lowercase letters, digits and hyphens, starting with a letter or digit, at most 64 characters"
                   (om::go-quote name)))
          ((string= (om::trim-space text) "")
           (format nil "the ~A definition ~A has no text" kind (om::go-quote name)))
          ((not (member kind (list +kind-instructions+ +kind-tool+) :test #'string=))
           ;; detector, remedy, format, schema and summary have validators (rules, declare) the
           ;; session start never needs.
           (error "go-layer: the ~A kind's validator is not ported here" kind)))))

(defun stale (d)
  "Store.stale: why d is a stale override, or nil."
  (when (and (string= (om.layer::gdef-kind d) +kind-instructions+) (not (def-replaces-p d)))
    (return-from stale nil))
  (let* ((key (def-key d)) (text (seed-text key))
         (seed (s0 (om.layer::gdef-seed d))) (seed-digest (s0 (om.layer::gdef-seed-digest d))))
    (cond ((and (string= seed "") text)
           (format nil "a seed ~A ships since it was written; the seed stands until it is defined again" key))
          ((string= seed "") nil)
          ((string/= seed key) (format nil "it overrides ~A, not its own name ~A" seed key))
          ((null text) (format nil "the seed it overrides (~A@~A) no longer ships" seed seed-digest))
          ((string/= (digest text) seed-digest)
           (format nil "the seed it overrides changed (~A@~A, now @~A); the new seed stands until it is defined again"
                   seed seed-digest (digest text))))))

;;; The store: a curated store directory, whose layer is <store>/harness (curated.Store.Layer).

(defun model-path (store model) (om.layer::go-model-path store model))
(defun def-path (store model kind name) (om.layer::join-path (model-path store model) kind (cat name ".rows")))
(defun held-path (store model kind name) (om.layer::join-path (model-path store model) "held" kind (cat name ".rows")))
(defun blob-path (store model d) (om.layer::go-blob-path store model d))
(defun held-index-path (store model) (om.layer::join-path (model-path store model) "held" "index.json"))

(defmacro with-locked ((store) &body body)
  "curated.Store.locked: the store's flock, shared with the Go engine."
  `(om.layer::with-store-lock (,store) ,@body))

(defun rows-names (dir)
  "rowsNames: the valid names of the .rows files in dir (not directories), sorted."
  (sort (loop for (name . dirp) in (list-dir dir)
              for base = (and (not dirp) (om::has-suffix name ".rows") (subseq name 0 (- (length name) 5)))
              when (and base (layer-valid-name-p base)) collect base)
        #'string<))

(defun generations (store model) (om.layer::go-generations store model))

(defun current (store model) (om.layer::go-current store model))

(defun recorded (map key) (let ((v (gethash key map))) (if (stringp v) v "")))

(defun held-index (store model) (om.layer::go-held-index store model))

(defun append-generation (store model op &key kind name digest session reason from held)
  "appendGeneration: the generation's number. The caller holds the lock."
  (om.layer::go-append-generation store model op :kind kind :name name :digest digest
                                                 :session session :reason reason :from from :held held))

(defun record-held (store model key d)
  "recordHeld: key's held file recorded as holding the content d names (\"\" removes it)."
  (let ((index (make-hash-table :test 'equal)))
    (maphash (lambda (k v) (setf (gethash k index) v)) (held-index store model))
    (if (string= d "")
        (multiple-value-bind (v ok) (gethash key index)
          (declare (ignore v))
          (unless ok (return-from record-held nil))
          (remhash key index))
        (setf (gethash key index) d))
    (write-atomic (held-index-path store model) (cat (om::json-string index) (string #\Newline)))))

;;; Reading (Store.load, List, recordedFile)

(defun load-file (path model kind name rec)
  "Store.load: the definition (its fallback in gdef-fallback), or (values nil reason)."
  (let ((data (read-octets path)))
    (unless data (return-from load-file (values nil "unreadable")))
    (let ((d (decode-file (octets-text data))))
      (cond ((null d) (values nil "not a layer file"))
            ((not (and (string= (om.layer::gdef-kind d) kind) (string= (om.layer::gdef-name d) name)
                       (string= (om.layer::gdef-model d) model)))
             (values nil "its header names another definition"))
            ((check-stored d))
            ((let ((want (recorded rec (seed-key kind name))))
               (or (string= want "") (string/= want (digest-octets data))))
             (values nil "untracked"))
            ((stale d))
            (t (setf (om.layer::gdef-state d) :written) d)))))

(defun list-defs (store model kind)
  "Store.List for one kind: the definitions it loads, in name order."
  (let ((rec (current store model)))
    (loop for name in (rows-names (om.layer::join-path (model-path store model) kind))
          for d = (load-file (def-path store model kind name) model kind name rec)
          when d collect d)))

(defun recorded-file (path model name rec)
  "recordedFile: the instructions definition at path (its fallback in gdef-fallback) when the
store recorded it and its checks accept it; nil otherwise."
  (let ((data (read-octets path)))
    (when data
      (let ((d (decode-file (octets-text data))))
        (and d (string= (om.layer::gdef-kind d) +kind-instructions+) (string= (om.layer::gdef-name d) name)
             (string= (om.layer::gdef-model d) model)
             (let ((want (recorded rec (seed-key +kind-instructions+ name))))
               (and (string/= want "") (string= want (digest-octets data))))
             (null (check-stored d))
             d)))))

;;; Writing (Store.stamp, putStamped, write)

(defun stamp-lifted (d)
  "stampLifted: (values d nil), or (values nil refusal)."
  (let ((d (om.layer::copy-gdef d)))
    (setf (om.layer::gdef-text d) (trim-right-newlines (s0 (om.layer::gdef-text d))))
    (when (and (string= (om.layer::gdef-kind d) +kind-instructions+) (string= (s0 (om.layer::gdef-mode d)) ""))
      (setf (om.layer::gdef-mode d) +mode-extend+))
    (let ((refusal (check-stored d))) (when refusal (return-from stamp-lifted (values nil refusal))))
    (setf (om.layer::gdef-time d) (now) (om.layer::gdef-seed d) nil (om.layer::gdef-seed-digest d) nil)
    (let ((text (seed-text (def-key d))))
      (when text (setf (om.layer::gdef-seed d) (def-key d) (om.layer::gdef-seed-digest d) (digest text))))
    (values d nil)))

(defun stamp (d)
  "stamp for a definition of a kind without a mode line (the port stamps tool definitions only)."
  (when (string= (om.layer::gdef-kind d) +kind-instructions+)
    (error "go-layer: stamping an instructions text (SplitMode) is not ported"))
  (let ((refusal (check-mode d))) (when refusal (return-from stamp (values nil refusal))))
  (stamp-lifted d))

(defun write-content (store model path content)
  "Store.write: the content kept in model's blobs, then at path."
  (let ((blob (blob-path store model (digest content))))
    (unless (path-exists-p blob) (write-atomic blob content)))
  (write-atomic path content))

(defun same-def-p (old d)
  (and (string= (om.layer::gdef-kind old) (om.layer::gdef-kind d)) (string= (om.layer::gdef-name old) (om.layer::gdef-name d))
       (string= (om.layer::gdef-model old) (om.layer::gdef-model d)) (string= (s0 (om.layer::gdef-text old)) (s0 (om.layer::gdef-text d)))
       (string= (s0 (om.layer::gdef-seed old)) (s0 (om.layer::gdef-seed d)))
       (string= (s0 (om.layer::gdef-seed-digest old)) (s0 (om.layer::gdef-seed-digest d)))
       (string= (s0 (om.layer::gdef-mode old)) (s0 (om.layer::gdef-mode d)))))

(defun put-stamped (store d)
  "putStamped: d written, with its generation, unless the recorded file already holds it."
  (let* ((model (om.layer::gdef-model d))
         (path (def-path store model (om.layer::gdef-kind d) (om.layer::gdef-name d)))
         (data (read-octets path)))
    (when (and data (string= (recorded (current store model) (def-key d)) (digest-octets data)))
      (let ((old (decode-file (octets-text data))))
        (when (and old (same-def-p old d))
          (return-from put-stamped old))))
    (let ((d (om.layer::copy-gdef d)))
      (setf (om.layer::gdef-engine d) "")
      (let ((content (encode-file d "")))
        (write-content store model path content)
        (append-generation store model +op-put+ :kind (om.layer::gdef-kind d) :name (om.layer::gdef-name d)
                                                :digest (digest content) :session (om.layer::gdef-session d)))
      d)))

(defun put-locked (store d)
  "PutLocked: (values d nil), or (values nil refusal); the caller holds the lock."
  (multiple-value-bind (stamped refusal) (stamp d)
    (if refusal (values nil refusal) (values (put-stamped store stamped) nil))))

;;; The legacy definitions (curated/definitions.go) and the retired queries (harness.RetiredQueries)

(defun legacy-dir (store) (om.layer::join-path store "definitions"))

(defun legacy-rows (dir suffix)
  "legacyRows: the valid names of the files in dir ending in suffix, in file-name order."
  (loop for (name . dirp) in (list-dir dir)
        for base = (and (not dirp) (om::has-suffix name suffix) (subseq name 0 (- (length name) (length suffix))))
        when (and base (layer-valid-name-p base)) collect base))

(defun legacy-definition (name path)
  "legacyDefinition: a tool kept for every model, its text the file's row, its session the row's
first source; nil when the file cannot be read."
  (let ((text (read-text path)))
    (when text
      (let ((d (om.layer::make-gdef :kind +kind-tool+ :name name :model +all-models+ :text (trim-right-newlines text)
                                    :session "" :mode "" :engine "")))
        (multiple-value-bind (decoded err) (try-rows-decode (om.layer::gdef-text d))
          (when (and (not err) (= (length decoded) 1))
            (multiple-value-bind (sources ok) (tbl-cell (first decoded) 0 "sources")
              (when ok
                (let ((fields (om::fields sources))) (when fields (setf (om.layer::gdef-session d) (first fields))))))))
        d))))

(defun migrate-definitions-locked (store)
  "migrateDefinitionsLocked: each legacy file becomes a layer file (a name the layer holds keeps
the layer's) and is renamed .migrated; one the layer refuses is renamed .refused; a refused one is
retried and migrated once admitted. An unreadable file stops the migration, as Go returns."
  (let ((dir (legacy-dir store)))
    (flet ((migrate (name legacy was-refused)
             (let ((d (legacy-definition name legacy)) (suffix ".migrated"))
               (unless d (return-from migrate-definitions-locked nil))
               (unless (path-exists-p (def-path store +all-models+ +kind-tool+ name))
                 (multiple-value-bind (put refusal) (put-locked store d)
                   (declare (ignore put))
                   (when refusal (setf suffix ".refused"))))
               (unless (and was-refused (string= suffix ".refused"))
                 (rename-file* legacy (cat (om.layer::join-path dir (cat name ".rows")) suffix))))))
      (let ((names (legacy-rows dir ".rows")) (refused (legacy-rows dir ".rows.refused")))
        (dolist (name names) (migrate name (om.layer::join-path dir (cat name ".rows")) nil))
        (dolist (name refused) (migrate name (om.layer::join-path dir (cat name ".rows.refused")) t))))))

(defun migrate-definitions (store)
  "curated.Store.migrateDefinitions: under the lock, only when there is a legacy file; a failure
leaves the files to be tried again."
  (let ((dir (legacy-dir store)))
    (when (or (legacy-rows dir ".rows") (legacy-rows dir ".rows.refused"))
      (handler-case (with-locked (store) (migrate-definitions-locked store))
        (error (e) (om.server::log-line "layer: legacy definitions: ~A" e))))))

(defun quoted-texts (query)
  "legacyquery.QuotedTexts."
  (let ((out '()) (start nil) (i 0) (n (length query)))
    (loop while (< i n)
          do (let ((c (char query i)))
               (cond ((and start (char= c #\\)) (incf i))
                     ((and (char= c #\") (null start)) (setf start (1+ i)))
                     ((char= c #\") (push (subseq query start i) out) (setf start nil))))
             (incf i))
    (when start (push (subseq query start) out))
    (nreverse out)))

(defun definition-query-ok-p (query)
  "legacyquery.CheckDefinitionBounds: at most 200 characters, each quoted text at most 40."
  (and (<= (length query) 200) (every (lambda (l) (<= (length l) 40)) (quoted-texts query))))

(defun parse-definition-p (name text)
  "Whether curated.parseDefinition reads the definition's row."
  (multiple-value-bind (decoded err) (try-rows-decode text)
    (and (not err) (= (length decoded) 1) (string= (tbl-name (first decoded)) "definition")
         (= (length (tbl-rows (first decoded))) 1)
         (let ((tb (first decoded)))
           (multiple-value-bind (stored ok) (tbl-cell tb 0 "name")
             (and (or (not ok) (string= stored name))
                  (let ((query (values (tbl-cell tb 0 "query"))))
                    (and (string/= (om::trim-space query) "") (definition-query-ok-p query)))))))))

(defun list-definitions (store)
  "curated.Store.ListDefinitions' names, in name order: the legacy files migrated first."
  (migrate-definitions store)
  (sort (loop for d in (list-defs store +all-models+ +kind-tool+)
              when (parse-definition-p (om.layer::gdef-name d) (om.layer::gdef-text d))
                collect (om.layer::gdef-name d))
        #'string<))

(defun refused-definitions (store)
  "curated.Store.RefusedDefinitions' names (migrating again first, as Go does)."
  (migrate-definitions store)
  (legacy-rows (legacy-dir store) ".rows.refused"))

(defun retired-query-names (store)
  "harness.RetiredQueries' names over the curated store: every stored definition, then the legacy
files the layer refused. It moves legacy definitions into the layer as Go does."
  (let ((defined (list-definitions store)))
    (append defined (refused-definitions store))))

;;; The instructions migrations (migrate.go, migrate_v2.go)

(defparameter +migration-reason+
  "recorded before instructions modes; read as an addition: the seed instructions stand again with its text appended after them")

(defun seed-share (seed text)
  "seedShare: the share of seed's distinct non-blank lines text repeats."
  (let ((lines (make-hash-table :test 'equal)) (seen (make-hash-table :test 'equal)) (repeated 0))
    (dolist (l (om::split text (string #\Newline))) (setf (gethash (om::trim-space l) lines) t))
    (dolist (l (om::split seed (string #\Newline)))
      (let ((l (om::trim-space l)))
        (unless (or (string= l "") (gethash l seen))
          (setf (gethash l seen) t)
          (when (gethash l lines) (incf repeated)))))
    (if (zerop (hash-table-count seen)) 0 (/ repeated (hash-table-count seen)))))

(defun migration-message (model d held n)
  "Migration.Message for d, model's instructions definition migrated at generation n."
  (let* ((text (om.layer::gdef-text d))
         (repeats (>= (seed-share (or (seed-text (def-key d)) "") text) 1/2))
         (msg (format nil "Observational memory: the ~A ~A/~A definition for ~A was recorded before instructions modes and is now an addition: the seed instructions stand again with its text appended after them (generation ~D; open it with \"mode: replace\" to replace the seed)"
                      (if held "held" "written") +kind-instructions+ (om.layer::gdef-name d) model n)))
    (if repeats
        (cat msg (format nil "; its text repeats most of the seed's lines, so the prompt now carries the seed and a near-copy of it (~D more bytes), and any seed rule it changed now meets the seed's own: define it again as only its changes, or with \"mode: replace\""
                         (length (om::utf8-octets text))))
        (cat msg "; if it restated or contradicted the seed, the prompt now carries both"))))

(defun migrate-instructions (store model)
  "Store.MigrateInstructions: each instructions file of model recorded before modes made an
addition, written and held; returns the messages in order."
  (let ((out '()))
    (with-locked (store)
      (let ((rec (current store model)) (moved (make-hash-table :test 'equal)))
        (dolist (name (rows-names (om.layer::join-path (model-path store model) +kind-instructions+)))
          (let* ((path (def-path store model +kind-instructions+ name)) (d (recorded-file path model name rec)))
            (when (and d (string= (s0 (om.layer::gdef-mode d)) ""))
              (setf (om.layer::gdef-mode d) +mode-extend+)
              (let* ((content (encode-file d ""))
                     (n (progn (write-content store model path content)
                               (append-generation store model +op-migrate+ :kind +kind-instructions+ :name name
                                                                           :digest (digest content) :session (om.layer::gdef-session d)
                                                                           :reason +migration-reason+))))
                (setf (gethash name moved) (cons (recorded rec (def-key d)) (digest content)))
                (push (migration-message model d nil n) out)))))
        (let ((index (held-index store model)))
          (dolist (name (rows-names (om.layer::join-path (model-path store model) "held" +kind-instructions+)))
            (let* ((path (held-path store model +kind-instructions+ name)) (d (recorded-file path model name index)))
              (when d
                (let ((fallback (s0 (om.layer::gdef-fallback d))) (rewrite nil) (m (gethash name moved)))
                  (when (and m (string= fallback (car m))) (setf fallback (cdr m) rewrite t))
                  (let ((migrate (string= (s0 (om.layer::gdef-mode d)) "")))
                    (when migrate (setf (om.layer::gdef-mode d) +mode-extend+ rewrite t))
                    (when rewrite
                      (let ((content (encode-file d fallback)))
                        (write-atomic path content)
                        (record-held store model (def-key d) (digest content))
                        (when migrate
                          (let ((n (append-generation store model +op-migrate+ :kind +kind-instructions+ :name name
                                                                               :held t :session (om.layer::gdef-session d)
                                                                               :reason +migration-reason+)))
                            (push (migration-message model d t n) out)))))))))))))
    (nreverse out)))

(defun fold-header (name worker)
  (if (string/= worker "")
      (format nil "From the ~A instructions memory v1 kept for its memory model ~A:" name worker)
      (format nil "From the ~A instructions (memory v1):" name)))

(defun fold-footer (name worker)
  (if (string/= worker "")
      (format nil "End of the ~A instructions memory v1 kept for its memory model ~A." name worker)
      (format nil "End of the ~A instructions (memory v1)." name)))

(defun with-fold-block (text name worker body)
  "withFoldBlock: text with name's folded block set to body (in place, else appended), or removed
when body is nil."
  (flet ((join (&rest parts) (om::join (remove "" parts :test #'string=) (format nil "~%~%"))))
    (let ((block (if body (cat (fold-header name worker) (string #\Newline) body (string #\Newline) (fold-footer name worker)) ""))
          (header (fold-header name worker)) (footer (fold-footer name worker)))
      (let ((i (search header text)))
        (when i
          (let ((j (search footer text :start2 i)))
            (when j
              (return-from with-fold-block
                (join (trim-right-newlines (subseq text 0 i)) block
                      (string-left-trim '(#\Newline) (subseq text (+ j (length footer))))))))))
      (if (null body) text (join (trim-right-newlines text) block)))))

(defstruct v2m model name from worker folded removed into why generation)

(defun v2-message (m)
  "V2Migration.Message."
  (let ((what (cond ((v2m-worker m)
                     (format nil "the ~A/~A addition memory v1 kept for its memory model ~A" +kind-instructions+ (v2m-name m) (v2m-from m)))
                    ((and (string/= (v2m-from m) "") (string/= (v2m-from m) (v2m-model m)))
                     (format nil "the ~A/~A addition kept for all models" +kind-instructions+ (v2m-name m)))
                    (t (format nil "the ~A/~A addition for ~A" +kind-instructions+ (v2m-name m) (v2m-model m))))))
    (cond ((v2m-folded m)
           (format nil "Observational memory: memory v2 folded ~A into the ~A/~A instructions for ~A as an addition (generation ~D); memory v2 runs no ~A, and its own text is kept as it was for memory v1"
                   what +kind-instructions+ (v2m-into m) (v2m-model m) (v2m-generation m) (v2m-name m)))
          ((v2m-removed m)
           (format nil "Observational memory: memory v2 removed the ~A block from the ~A/~A instructions for ~A (generation ~D): memory v1's ~A addition is gone or no longer an addition"
                   (v2m-name m) +kind-instructions+ (v2m-into m) (v2m-model m) (v2m-generation m) (v2m-name m)))
          ((v2m-worker m)
           (format nil "Observational memory: memory v2 retires the ~A/~A definition memory v1 kept for its memory model ~A (~A, marked in ~A's generation ~D): memory v2 runs no ~A, so it is not used; its text is kept in the layer for memory v1"
                   +kind-instructions+ (v2m-name m) (v2m-from m) (v2m-why m) (v2m-model m) (v2m-generation m) (v2m-name m)))
          (t (format nil "Observational memory: memory v2 retires the ~A/~A definition for ~A (~A, generation ~D): memory v2 runs no ~A, so it is not used; its text is kept in the layer for memory v1"
                     +kind-instructions+ (v2m-name m) (v2m-model m) (v2m-why m) (v2m-generation m) (v2m-name m))))))

(defparameter +v2-retired-replace+ "a replacement of its seed, which memory v2 has no stage for")
(defparameter +v2-retired-stage+ "instructions of a stage memory v2 does not run")

(defun migrate-to-v2 (store model worker into extend retire)
  "migrateToV2 (worker \"\": MigrateToV2; else MigrateWorkerToV2's body): the folds, the removals,
then the retirements, as v2m structs. A source is (definition digest own)."
  (let ((out '()))
    (with-locked (store)
      (let* ((rec (current store model))
             (worker-rec (if (string/= worker "") (current store worker) (make-hash-table :test 'equal)))
             (inherited (if (string/= model +all-models+) (current store +all-models+) (make-hash-table :test 'equal)))
             (gens (generations store model))
             (fold '()) (retired '()) (gone '()) (own-fold nil) (why (make-hash-table :test 'equal)))
        (labels ((gs (g key) (om::str (om::jget g key)))
                 (migrated-p (name d)
                   (some (lambda (g) (and (string= (gs g "op") +op-migrate-v2+) (string= (gs g "kind") +kind-instructions+)
                                          (string= (gs g "name") name) (string= (gs g "digest") d) (string= (gs g "from") worker)))
                         gens))
                 (effective (name)
                   (let ((key (seed-key +kind-instructions+ name)))
                     (if (string/= worker "")
                         (let ((d (recorded-file (def-path store worker +kind-instructions+ name) worker name worker-rec)))
                           (and d (list d (recorded worker-rec key) t)))
                         (let ((d (recorded-file (def-path store model +kind-instructions+ name) model name rec)))
                           (cond (d (list d (recorded rec key) t))
                                 ((string/= model +all-models+)
                                  (let ((d (recorded-file (def-path store +all-models+ +kind-instructions+ name) +all-models+ name inherited)))
                                    (and d (list d (recorded inherited key) nil))))))))))
          (dolist (name (append extend retire))
            (let ((src (effective name)) (extend-p (member name extend :test #'string=)))
              (when (and extend-p (or (null src) (def-replaces-p (first src))))
                (setf gone (append gone (list name))))
              (when (and src (not (migrated-p name (second src))))
                (cond ((and extend-p (not (def-replaces-p (first src))))
                       (setf fold (append fold (list src)) own-fold (or own-fold (third src))))
                      ((not (third src)))
                      (extend-p (setf retired (append retired (list src)) (gethash name why) +v2-retired-replace+))
                      (t (setf retired (append retired (list src)) (gethash name why) +v2-retired-stage+))))))
          (let* ((target (recorded-file (def-path store model +kind-instructions+ into) model into rec))
                 (has-target (and target t)))
            (when (and (not has-target) (string/= model +all-models+) (not own-fold))
              (setf fold '() gone '()))
            (when (and (or fold gone) (string/= (recorded (held-index store model) (seed-key +kind-instructions+ into)) ""))
              (setf fold '() gone '()))
            (unless has-target
              (setf target (om.layer::make-gdef :kind +kind-instructions+ :name into :model model :mode +mode-extend+
                                                :text "" :session "" :engine ""))
              (when (string/= model +all-models+)
                (let ((d (recorded-file (def-path store +all-models+ +kind-instructions+ into) +all-models+ into inherited)))
                  (when d (setf (om.layer::gdef-text target) (om.layer::gdef-text d)
                                (om.layer::gdef-mode target) (s0 (om.layer::gdef-mode d)))))))
            (let ((text (s0 (om.layer::gdef-text target))) (removed '()))
              (dolist (src fold)
                (setf text (with-fold-block text (om.layer::gdef-name (first src)) worker (om.layer::gdef-text (first src)))))
              (dolist (name gone)
                (let ((next (with-fold-block text name worker nil)))
                  (when (string/= next text) (setf text next removed (append removed (list name))))))
              (when (or fold removed)
                (let ((target (om.layer::copy-gdef target)))
                  (setf (om.layer::gdef-text target) text (om.layer::gdef-engine target) "")
                  (multiple-value-bind (stamped refusal) (stamp-lifted target)
                    (if refusal
                        (progn
                          (dolist (src fold)
                            (when (third src)
                              (setf retired (append retired (list src))
                                    (gethash (om.layer::gdef-name (first src)) why)
                                    (cat "its text could not be folded into " into ": " refusal))))
                          (setf fold '() removed '()))
                        (progn
                          (put-stamped store stamped)
                          (let ((put (length (generations store model))))
                            (dolist (src fold)
                              (append-generation store model +op-migrate-v2+ :kind +kind-instructions+
                                                                             :name (om.layer::gdef-name (first src)) :digest (second src) :from worker
                                                                             :reason (format nil "folded into ~A/~A as an addition (generation ~D)"
                                                                                             +kind-instructions+ into put))
                              (push (make-v2m :model model :from (om.layer::gdef-model (first src)) :worker (string/= worker "")
                                              :name (om.layer::gdef-name (first src)) :folded t :into into :generation put)
                                    out))
                            (dolist (name removed)
                              (push (make-v2m :model model :from worker :worker (string/= worker "") :name name
                                              :removed t :into into :generation put)
                                    out)))))))))
            (dolist (src retired)
              (let* ((name (om.layer::gdef-name (first src)))
                     (n (append-generation store model +op-migrate-v2+ :kind +kind-instructions+ :name name
                                                                       :digest (second src) :from worker
                                                                       :reason (cat "retired under memory v2: " (gethash name why)))))
                (push (make-v2m :model model :from (if (string/= worker "") worker model) :worker (string/= worker "")
                                :name name :why (gethash name why) :generation n)
                      out)))))))
    (nreverse out)))

(defun migrate-layer-v2 (store model worker)
  "harness.MigrateLayerV2 over the curated store directory store: MigrateInstructions for every
model and model, then MigrateToV2 for each, then MigrateWorkerToV2 for worker. Returns the
messages Go tells a host with a UI, in order. A step that fails is dropped whole (its messages
too) and retried at the next start."
  (let* ((models (if (and (string/= model +all-models+) (string/= model "")) (list +all-models+ model) (list +all-models+)))
         (extend (list +instructions-observer+ +instructions-reflector+))
         (retire (list +instructions-dropper+))
         (messages '()))
    (flet ((run (what fn)
             (handler-case (setf messages (append messages (funcall fn)))
               (error (e) (om.server::log-line "layer migration: ~A: ~A" what e)))))
      (dolist (m models)
        (run "instructions modes" (lambda () (migrate-instructions store m))))
      (dolist (m models)
        (run "memory v2" (lambda () (mapcar #'v2-message (migrate-to-v2 store m "" +instructions-consolidation+ extend retire)))))
      (when (and (string/= model +all-models+) (string/= model "")
                 (string/= worker "") (string/= worker model) (string/= worker +all-models+))
        (run "memory v2 (worker)"
             (lambda () (mapcar #'v2-message (migrate-to-v2 store model worker +instructions-consolidation+ extend retire))))))
    messages))
