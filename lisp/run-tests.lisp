;;;; The spike's tests: sbcl --non-interactive --load lisp/run-tests.lisp
;;;; A tiny runner: deftest registers a test, check compares with equal and records a failure
;;;; without stopping; the process exits 1 when any check failed or any test signalled an error.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; The tests read the port's internal functions, so they live in its package.
(in-package "OM")

(defvar *tests* '())
(defvar *failures* 0)
(defvar *checks* 0)
(defvar *current* nil)

(defmacro deftest (name &body body)
  `(progn (setf *tests* (append (remove ',name *tests* :key #'car) (list (cons ',name (lambda () (block ,name ,@body))))))
          ',name))

(defun check (got want &optional (what ""))
  (incf *checks*)
  (unless (equal got want)
    (incf *failures*)
    (format t "~&FAIL ~(~A~) ~A~%  got:  ~S~%  want: ~S~%" *current* what got want))
  got)

(let ((here (make-pathname :name nil :type nil :defaults *load-truename*)))
  (dolist (file '("regex" "json" "ledger" "extract" "selection" "server" "tool-layer"))
    (load (merge-pathnames (format nil "tests/~A.lisp" file) here))))

;; W1, the memory model.
(load (merge-pathnames "tests/memory-model.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; W2: the transcript readers and the neutral store.
(let ((here (make-pathname :name nil :type nil :defaults *load-truename*)))
  (dolist (file '("readers" "store"))
    (load (merge-pathnames (format nil "tests/~A.lisp" file) here))))

;; W5: the hooks.
(load (merge-pathnames "tests/hooks.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))
;; Cooperative limits for model code (docs/lisp-port-tool-layer.md, "Stopping runaway code").
(load (merge-pathnames "tests/safe-stops.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; 1b: the curated ranked index and Settle.
(load (merge-pathnames "tests/curated-index.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; W6: the two-way peer.
(load (merge-pathnames "tests/peer.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))
;; W3t: the memory tool in a child SBCL.
(load (merge-pathnames "tests/tool-child.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; Compaction on PiG and the remaining PiG events.
(load (merge-pathnames "tests/pig.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; Evidence and retirement (docs/lisp-port-evidence.md).
(load (merge-pathnames "tests/evidence.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; The retired names and the v1 layer migration at a session start (docs/lisp-port-migration.md).
(load (merge-pathnames "tests/migration.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; Level 1 on the image (docs/lisp-port-level1.md).
(load (merge-pathnames "tests/level1.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; Level 0's file tools across harnesses and the JavaScript project slug.
(load (merge-pathnames "tests/filetools.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; The layer store across processes and crashes (docs/lisp-port-tool-layer.md).
(load (merge-pathnames "tests/layer-store.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; Phase 1's safety gates: the forget gate, staging independence, level-1 provenance.
(load (merge-pathnames "tests/safety.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; The secret scanner and role-label escaping (contracts/secrets.md).
(load (merge-pathnames "tests/secrets.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

;; Phase 2: level 0's reading of tool output (memory/extract/output.go).
(load (merge-pathnames "tests/output.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))
;; Phase 2: the encoding fields (contracts/encoding.md).
(load (merge-pathnames "tests/encoding.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))
;; Preferences that weaken security and the unverified framing (contracts/insecure.md).
(load (merge-pathnames "tests/insecure.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(let ((errors 0))
  (dolist (test *tests*)
    (let ((*current* (car test)))
      (handler-case (funcall (cdr test))
        (error (e)
          (incf errors)
          (format t "~&ERROR ~(~A~): ~A~%" (car test) e)))))
  (format t "~&~D tests, ~D checks, ~D failed, ~D errors~%" (length *tests*) *checks* *failures* errors)
  (sb-ext:exit :code (if (and (zerop *failures*) (zerop errors)) 0 1)))
