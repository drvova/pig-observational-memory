;;;; The Go layer's /om:status lines (memory/harness/layer.go layerStatusLines, layertool.go
;;;; LayerRows, memory/layer Load, List, held, load, WrittenBy (writers, states), LastPass,
;;;; LastProposal, Report.Message) for the kinds the image reads: instructions, tools and
;;;; remedies (the rules and the remedy catalogues). A layer holding a definition of another kind
;;;; (format, schema, summary, detector), or a file the image cannot decode the way Go reports it,
;;;; signals layer-not-ported, and /om:status is then the Go engine's (a not-ported answer).
;;;; Read-only: nothing here writes.

(in-package "OM.GOLAYER")

(define-condition layer-not-ported (error) ((what :initarg :what :reader lnp-what))
  (:report (lambda (c s) (format s "the layer's ~A" (lnp-what c)))))

(defun lnp (fmt &rest args) (error 'layer-not-ported :what (apply #'format nil fmt args)))

(defparameter +read-kinds+ '("instructions" "tool" "remedy"))

(defun all-seeds ()
  "layer.Shipped for the kinds the image reads: the consolidation instructions and the remedy
catalogues (keys kind/name)."
  (append (seeds)
          (loop for remedy being the hash-keys of (om.layer::catalogue-seeds) using (hash-value text)
                collect (cons (seed-key "remedy" remedy) text))))

(defun shipped-text (key) (cdr (assoc key (all-seeds) :test #'string=)))

(defun stale* (d)
  "Store.stale over every seed the image reads."
  (let ((kind (om.layer::gdef-kind d)))
    (when (and (string= kind +kind-instructions+) (not (def-replaces-p d))) (return-from stale* nil))
    (let* ((key (def-key d)) (text (shipped-text key))
           (seed (s0 (om.layer::gdef-seed d))) (seed-digest (s0 (om.layer::gdef-seed-digest d))))
      (cond ((and (string= seed "") text)
             (format nil "a seed ~A ships since it was written; the seed stands until it is defined again" key))
            ((string= seed "") nil)
            ((string/= seed key) (format nil "it overrides ~A, not its own name ~A" seed key))
            ((null text) (format nil "the seed it overrides (~A@~A) no longer ships" seed seed-digest))
            ((string/= (digest text) seed-digest)
             (format nil "the seed it overrides changed (~A@~A, now @~A); the new seed stands until it is defined again"
                     seed seed-digest (digest text)))))))

(defun check-stored* (d)
  "Store.checkStored for the kinds the image reads: the refusal's text, or nil."
  (if (string= (om.layer::gdef-kind d) "remedy")
      (or (check-mode d)
          (and (string= (om::trim-space (s0 (om.layer::gdef-model d))) "")
               (cat "a definition needs the model it applies to, or " +all-models+))
          (kernel-check "remedy" (om.layer::gdef-name d) (s0 (om.layer::gdef-text d)))
          (and (not (om.layer::go-check-remedy d)) (lnp "remedy file ~A refused by its validator" (om.layer::gdef-name d))))
      (check-stored d)))

(defstruct (lreport (:conc-name lr-)) model kind name stale reason)

(defun lreport-message (r)
  "Report.Message."
  (format nil "Observational memory: layer definition ~A/~A for ~A ~A: ~A" (lr-kind r) (lr-name r) (lr-model r)
          (if (lr-stale r) "skipped as a stale override" "skipped") (lr-reason r)))

(defun load-def (path model kind name rec)
  "Store.load: (values d nil) or (values nil report)."
  (flet ((report (stale fmt &rest args)
           (values nil (make-lreport :model model :kind kind :name name :stale stale :reason (apply #'format nil fmt args)))))
    (let ((data (read-octets path)))
      (unless data (lnp "file ~A is unreadable" path))
      (let ((d (decode-file (octets-text data))))
        (unless d (lnp "file ~A does not decode" path))
        (cond ((not (and (string= (om.layer::gdef-kind d) kind) (string= (om.layer::gdef-name d) name)
                         (string= (om.layer::gdef-model d) model)))
               (report nil "its header names ~A/~A for ~A, not the definition its path holds"
                       (om.layer::gdef-kind d) (om.layer::gdef-name d) (om.layer::gdef-model d)))
              ((check-stored* d) (report nil "~A" (check-stored* d)))
              ((let ((want (recorded rec (seed-key kind name)))) (or (string= want "") (string/= want (digest-octets data))))
               (report nil "untracked: the store recorded no such content for it (a file written or edited outside the memory tool); forget it, or define it again so a clean pass and probation judge it"))
              ((stale* d) (report t "~A" (stale* d)))
              (t (setf (om.layer::gdef-state d) :written) (values d nil)))))))

(defun check-kinds (store model)
  "Signals layer-not-ported when model's layer holds a file of a kind the image does not read."
  (dolist (kind +kinds+)
    (unless (member kind +read-kinds+ :test #'string=)
      (when (or (rows-names (om.layer::join-path (model-path store model) kind))
                (rows-names (om.layer::join-path (model-path store model) "held" kind)))
        (lnp "~A definitions for ~A" kind model)))))

(defun owns-held-file-p (store model kind name)
  (let ((data (read-octets (held-path store model kind name))))
    (or (null data) (let ((d (decode-file (octets-text data)))) (or (null d) (string= (s0 (om.layer::gdef-engine d)) "v2"))))))

(defun list-model (store model)
  "Store.List(model, \"\") then Store.held(model): (values written held reports)."
  (check-kinds store model)
  (let ((rec (current store model)) (index (held-index store model)) (written '()) (held '()) (reports '()))
    (dolist (kind +read-kinds+)
      (dolist (name (rows-names (om.layer::join-path (model-path store model) kind)))
        (multiple-value-bind (d r) (load-def (def-path store model kind name) model kind name rec)
          (if d (push d written) (push r reports)))))
    (dolist (kind +read-kinds+)
      (dolist (name (rows-names (om.layer::join-path (model-path store model) "held" kind)))
        (multiple-value-bind (d r) (load-def (held-path store model kind name) model kind name index)
          (cond (r (when (owns-held-file-p store model kind name) (push r reports)))
                ((string= (s0 (om.layer::gdef-engine d)) "v2")
                 (setf (om.layer::gdef-state d) :held) (push d held))))))
    (values (sort-kinds (nreverse written)) (sort-kinds (nreverse held)) (sort-reports (nreverse reports)))))

(defun kind-index (kind) (or (position kind +kinds+ :test #'string=) 99))

(defun sort-kinds (defs)
  (stable-sort defs (lambda (a b) (< (kind-index (om.layer::gdef-kind a)) (kind-index (om.layer::gdef-kind b))))))

(defun sort-reports (reports)
  "List's reports in kind order (written ones before held ones within a model, as Load appends them)."
  reports)

(defun load-harness (store model)
  "Store.Load: (values defs reports), defs a hash table key -> gdef."
  (let ((defs (make-hash-table :test 'equal)) (reports '()))
    (dolist (m (if (and (string/= model "") (string/= model +all-models+)) (list +all-models+ model) (list +all-models+)))
      (multiple-value-bind (written held rs) (list-model store m)
        ;; Load appends the written files' reports, then the held ones', per model.
        (setf reports (append reports rs))
        (dolist (d written) (setf (gethash (def-key d) defs) d))
        (dolist (d held) (setf (gethash (def-key d) defs) d))))
    (values defs reports)))

;;; The manifest's history (manifest.go states, writers, WrittenBy)

(defun gen-key (g) (seed-key (om::str (om::jget g "kind")) (om::str (om::jget g "name"))))
(defun gen-op (g) (om::str (om::jget g "op")))
(defun gen-digest (g) (om::str (om::jget g "digest")))

(defun states (gens)
  "states: the written content after each generation (a vector of hash tables, element 0 empty)."
  (let ((out (make-array 1 :adjustable t :fill-pointer t :initial-contents (list (make-hash-table :test 'equal)))))
    (dolist (g gens out)
      (let ((next (make-hash-table :test 'equal)) (target (om::jget g "target")) (op (gen-op g)))
        (if (and (string= op "rollback") (realp target) (>= target 0) (< target (length out)))
            (let ((prev (aref out (1- (length out)))) (keep (om::jget g "keep")))
              (maphash (lambda (k v) (setf (gethash k next) v)) (aref out (truncate target)))
              (when (and (vectorp keep) (not (stringp keep)))
                (loop for k across keep when (stringp k)
                      do (multiple-value-bind (v ok) (gethash k prev)
                           (if ok (setf (gethash k next) v) (remhash k next))))))
            (let ((key (gen-key g)))
              (maphash (lambda (k v) (setf (gethash k next) v)) (aref out (1- (length out))))
              (cond ((or (string= op "forget") (and (string= op "revert") (string= (gen-digest g) "")))
                     (remhash key next))
                    ((or (string= op "put") (string= op "revert")
                         (and (string= op "migrate") (not (eq (om::jget g "held") :true))))
                     (setf (gethash key next) (gen-digest g))))))
        (vector-push-extend next out)))))

(defstruct (writer (:conc-name w-)) gen-n digest (decided nil) (established nil))

(defun written-by (store model)
  "Store.WrittenBy: key -> writer."
  (let* ((gens (generations store model)) (all (states gens)) (out (make-hash-table :test 'equal)))
    (loop for g in gens for i from 0 for n from 1
          do (let ((key (gen-key g)) (op (gen-op g)))
               (cond ((string= op "put") (setf (gethash key out) (make-writer :gen-n n :digest (gen-digest g))))
                     ((string= op "establish")
                      (let ((w (gethash key out))) (when w (setf (w-decided w) t (w-established w) t))))
                     ((string= op "forget") (remhash key out))
                     ((string= op "migrate")
                      (let ((w (gethash key out)))
                        (when (and w (not (eq (om::jget g "held") :true)))
                          (if (and (w-decided w) (not (w-established w)))
                              (setf (gethash key out) (make-writer :gen-n (w-gen-n w) :digest (gen-digest g) :decided t))
                              (setf (gethash key out) (make-writer :gen-n n :digest (gen-digest g)))))))
                     ((string= op "revert")
                      (if (string= (gen-digest g) "")
                          (remhash key out)
                          (setf (gethash key out) (make-writer :gen-n n :digest (gen-digest g) :decided t))))
                     ((string= op "rollback")
                      (let ((before (aref all i)) (after (aref all (1+ i))) (keys '()))
                        (maphash (lambda (k v) (declare (ignore v)) (push k keys)) before)
                        (maphash (lambda (k v) (declare (ignore v)) (unless (nth-value 1 (gethash k before)) (push k keys))) after)
                        (dolist (k (sort keys #'string<))
                          (let ((b (gethash k before "")) (a (gethash k after "")))
                            (unless (string= b a)
                              (if (string= a "")
                                  (remhash k out)
                                  (setf (gethash k out) (make-writer :gen-n n :digest a)))))))))))
    out))

(defun last-op (store model ops)
  "LastPass / LastProposal: the latest generation (its number) whose op is among ops, or nil."
  (let ((found nil))
    (loop for g in (generations store model) for n from 1
          when (member (gen-op g) ops :test #'string=) do (setf found n))
    found))

(defun last-proposal-events (store model)
  "LastProposal's Events (the usage ledger's length it recorded), or nil without a proposal."
  (let ((found nil))
    (dolist (g (generations store model) found)
      (when (string= (gen-op g) "propose")
        (setf found (let ((e (om::jget g "events"))) (if (realp e) (truncate e) 0)))))))

;;; LayerRows and layerStatusLines

(defun judged-kind-p (kind name)
  "rules.JudgedKind for the kinds the image reads."
  (cond ((string= kind "instructions") (string= name "observer"))
        ((string= kind "remedy") (and (member name om.layer::+remedies+ :test #'string=) t))
        (t nil)))

(defstruct (lrow (:conc-name row-)) model kind name state origin (generation "") (stale "") (flagged ""))

(defun layer-rows (store model)
  "harness.LayerRows without the seeds' rows (which status skips): the effective definitions by
key, then a row per skipped file."
  (multiple-value-bind (defs reports) (load-harness store model)
    (let ((history (make-hash-table :test 'equal)) (rows '()))
      (dolist (m (if (string= model +all-models+) (list +all-models+) (list +all-models+ model)))
        (maphash (lambda (k w) (setf (gethash k history) w)) (written-by store m)))
      (let ((keys (sort (loop for k being the hash-keys of defs collect k) #'string<)))
        (dolist (key keys)
          (let* ((d (gethash key defs))
                 (row (make-lrow :model (om.layer::gdef-model d) :kind (om.layer::gdef-kind d) :name (om.layer::gdef-name d)
                                 :state (if (eq (om.layer::gdef-state d) :held) "held" "written")
                                 :origin (if (shipped-text key)
                                             (cond ((def-replaces-p d) "replaces")
                                                   ((string= (om.layer::gdef-kind d) "instructions") "extends")
                                                   (t "override"))
                                             "own"))))
            (cond ((string= (row-kind row) "tool") (setf (row-state row) "retired"))
                  ((string= (row-state row) "written")
                   (let ((w (gethash key history)))
                     (when w
                       (setf (row-generation row) (om::itoa (w-gen-n w)))
                       (cond ((w-established w) (setf (row-state row) "established"))
                             ((and (not (w-decided w))
                                   (or (judged-kind-p (row-kind row) (row-name row)) (def-replaces-p d)))
                              (setf (row-state row) "probation")))))))
            (push row rows))))
      (dolist (r reports)
        (push (make-lrow :model (lr-model r) :kind (lr-kind r) :name (lr-name r) :state "skipped"
                         :origin (if (shipped-text (seed-key (lr-kind r) (lr-name r))) "override" "own")
                         :stale (if (lr-stale r) (lr-reason r) ""))
              rows))
      (values (nreverse rows) reports))))

(defun layer-status-lines (store model)
  "harness.layerStatusLines."
  (multiple-value-bind (rows reports) (layer-rows store model)
    (let ((by-kind (make-hash-table :test 'equal)) (held '()) (stale '()) (skipped '()) (defined 0)
          (gens (length (generations store model))))
      (dolist (r rows)
        (let ((key (cat (row-kind r) "/" (row-name r))))
          (cond ((and (string= (row-state r) "skipped") (string/= (row-stale r) ""))
                 (push (format nil "~A (~A)" key (row-stale r)) stale))
                ((string= (row-state r) "skipped") (push key skipped))
                (t
                 (when (string= (row-state r) "held") (push key held))
                 (incf defined)
                 (let ((item (cat (row-name r) " " (row-state r))))
                   (when (string/= (row-generation r) "") (setf item (cat item " gen " (row-generation r))))
                   (cond ((string= (row-origin r) "override") (setf item (cat item ", overrides the seed")))
                         ((string= (row-origin r) "extends") (setf item (cat item ", extends the seed")))
                         ((string= (row-origin r) "replaces") (setf item (cat item ", replaces the seed (mode replace)"))))
                   (when (string/= (row-model r) model) (setf item (cat item ", for all models")))
                   (when (string/= (row-flagged r) "") (setf item (cat item ", flagged: " (row-flagged r))))
                   (setf (gethash (row-kind r) by-kind) (append (gethash (row-kind r) by-kind) (list item))))))))
      (setf held (nreverse held) stale (nreverse stale) skipped (nreverse skipped))
      (when (and (zerop gens) (zerop defined) (null stale) (null skipped) (not (last-op store model '("build" "revise"))))
        (return-from layer-status-lines nil))
      (let ((line (format nil "Layer (~A): generation ~D, ~D definitions" model gens defined))
            (proposal (last-op store model '("propose"))))
        (cond (proposal (setf line (format nil "~A, definitions last offered in level 1 at generation ~D" line proposal)))
              ((string/= model +all-models+)
               (setf line (cat line ", definitions are offered in level 1 when the harness evidence changes"))))
        (when held (setf line (cat line ", held: " (om::join held ", "))))
        (append (list line)
                (loop for kind in +kinds+
                      for items = (gethash kind by-kind)
                      when items collect (cat "  " kind ": " (om::join items "; ")))
                (loop for s in stale collect (cat "  stale override skipped, the seed stands: " s))
                (loop for r in reports
                      unless (lr-stale r)
                        collect (cat "  " (let ((m (lreport-message r)))
                                            (if (om::has-prefix m "Observational memory: ") (subseq m 22) m)))))))))
