;;;; The Lisp side of the evidence parity test (memory/contracts/lispevidence_test.go):
;;;;   sbcl --script lisp/evidence-parity.lisp rules <store dir> <steps.json>
;;;; runs the steps on the store (detect events, raw ledger lines, compaction passes, the rules
;;;; that apply, settle decisions) and prints one line per step, as the Go side does.
;;;;   sbcl --script lisp/evidence-parity.lisp files <store dir> <layer root>
;;;; writes a store and a layer through the image's evidence path, for the schema check.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM.LAYER")

(defun line (fmt &rest args) (write-string (apply #'format nil fmt args)) (terpri))

(defun alist-of (h)
  (let ((out '()))
    (when (hash-table-p h) (maphash (lambda (k v) (push (cons k (truncate v)) out)) h))
    (sort out #'string< :key #'car)))

(defun strings (v) (if (and (vectorp v) (not (stringp v))) (coerce v 'list) '()))

(defun run-rules (store steps-path)
  (let ((steps (om::json-parse (coerce (read-file steps-path) 'simple-string))) (n 0))
    (loop for step across steps
          do (incf n)
             (let* ((op (om::jget step "op")) (model (om::str (om::jget step "model")))
                    (tm (parse-rfc3339 (om::str (om::jget step "time"))))
                    (*now* (lambda () tm)))
               (cond
                 ((string= op "detect")
                  (record-detection store model (make-findings :counts (alist-of (om::jget step "counts"))
                                                               :dropped-kinds (alist-of (om::jget step "kinds")))
                                    (truncate (or (om::jget step "tokens") 0)))
                  (line "~D detect" n))
                 ((string= op "raw")
                  (with-store-lock (store) (append-line (models-path store model) (om::jget step "line")))
                  (line "~D raw" n))
                 ((string= op "pass")
                  (let ((ts (after-pass store model :empty (om::jtrue-p (om::jget step "empty"))
                                                    :level1 (om::jtrue-p (om::jget step "level1"))
                                                    :counts (alist-of (om::jget step "counts"))
                                                    :ran-held (strings (om::jget step "ranHeld"))
                                                    :ran-written (strings (om::jget step "ranWritten"))
                                                    :roots (strings (om::jget step "roots"))
                                                    :repo (om::str (om::jget step "repo")) :family (om::str (om::jget step "family"))
                                                    :turns (truncate (or (om::jget step "turns") 0)))))
                    (line "~D pass ~A" n (om::json-string (coerce (sort (mapcar #'transition-message ts) #'string<) 'simple-vector)))))
                 ((string= op "rules")
                  (let ((rules (active-rules store model)))
                    (line "~D rules ~A keep-head=~A" n
                          (om::join (mapcar (lambda (r) (format nil "~A:~A:~A" (rule-id r) (rule-remedy r) (rule-state r))) rules) ",")
                          (if (rules-have-p rules +remedy-keep-head+) "yes" "no"))))
                 ((string= op "settle")
                  (multiple-value-bind (decided clean reason scope)
                      (settle-v2 (model-events store model) :kind (om::jget step "kind") :name (om::jget step "name")
                                                            :held-id (om::jget step "heldId")
                                                            :time (parse-rfc3339 (om::jget step "heldTime"))
                                                            :text (om::str (om::jget step "text")))
                    (line "~D settle ~A ~A ~A ~A" n (if decided "decided" "held") (if clean "clean" "unclean")
                          (om::json-string reason) (om::json-string (scope-string scope)))))
                 (t (error "unknown step ~A" op)))))))

(defun obs-list (v) (if (vectorp v) (loop for m across v for o = (om::observation-from m) when o collect o) '()))

(defun run-detect (cases-path)
  "Each case: DetectV2's findings over branch (covered: the ids it covered; notes and known:
records) and KeepHead over its uncited entries, printed as the Go side prints them."
  (let ((cases (om::json-parse (coerce (read-file cases-path) 'simple-string))) (n 0)
        (tm (parse-rfc3339 "2026-11-05T12:00:00Z")))
    (loop for c across cases
          do (incf n)
             (let* ((*now* (lambda () tm))
                    (branch (map 'simple-vector #'om::entry-from-map (om::jget c "branch")))
                    (ids (strings (om::jget c "covered")))
                    (covered (remove-if-not (lambda (e) (member (om::entry-id e) ids :test #'string=)) branch))
                    (notes (obs-list (om::jget c "notes"))) (known (obs-list (om::jget c "known")))
                    (f (detect-v2 branch covered notes known '())))
               (multiple-value-bind (kept refused)
                   (keep-head branch (findings-uncited f) (strings (om::jget c "chunkOrder")) (om::str (om::jget c "covers"))
                              (append known notes))
                 (line "~D counts=~A uncited=~A kinds=~A kept=[~{~A~^,~}] refused=~A" n
                       (om::json-string (counts-object (findings-counts f)))
                       (om::json-string (coerce (findings-uncited f) 'simple-vector))
                       (om::json-string (or (counts-object (findings-dropped-kinds f)) (make-hash-table)))
                       (mapcar #'om::obs-json kept)
                       (om::json-string (coerce refused 'simple-vector))))))))

(defun write-files (store root)
  "A store and a layer written through the evidence path: detect and pass events, a harness rule,
a settled definition retired after three failing units, and its units."
  (let ((*layer-root* root) (model "acme/files"))
    (loop for k from 1 to 3
          do (let ((tm (parse-rfc3339 (format nil "2026-11-02T0~D:00:00Z" k))))
               (let ((*now* (lambda () tm)))
                 (record-detection store model (make-findings :counts (list (cons +detect-uncited+ k))) 100)
                 (after-pass store model :ran-held '("adaptable/x@0123456789abcdef")))))
    (with-call ((make-call :model model :session "s"))
      (let* ((d (let ((*now* (let ((tm (parse-rfc3339 "2026-11-02T04:00:00Z"))) (lambda () tm))))
                  (layer-hold model "(define rank-notes (notes words) (list 1))"))))
        (let ((*now* (let ((tm (parse-rfc3339 "2026-11-02T05:00:00Z"))) (lambda () tm))))
          (layer-settle model (list (om::jobject "time" "2026-11-02T05:00:00Z" "kind" "pass" "engine" "v2" "stage" "compaction"
                                                 "tokens" 100 "outcome" "" "ranHeld" (vector (ldef-held-id d))))))
        (loop for k from 6 to 8
              do (let ((tm (parse-rfc3339 (format nil "2026-11-02T0~D:00:00Z" k))))
                   (let ((*now* (lambda () tm)))
                     (om.engine::rank-notes (list (make-note :id "a") (make-note :id "b")) '("x"))
                     (record-compaction-evidence :store store :model model :session "s" :entries #()
                                                 :snapshot (take-snapshot store model)))))))))

(let ((args (cdr sb-ext:*posix-argv*)))
  (cond ((string= (first args) "rules") (run-rules (second args) (third args)))
        ((string= (first args) "files") (write-files (second args) (third args)))
        ((string= (first args) "detect") (run-detect (second args)))
        (t (error "usage: evidence-parity.lisp rules <store> <steps.json> | files <store> <layer root>"))))
