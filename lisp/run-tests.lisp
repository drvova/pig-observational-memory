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

(let ((errors 0))
  (dolist (test *tests*)
    (let ((*current* (car test)))
      (handler-case (funcall (cdr test))
        (error (e)
          (incf errors)
          (format t "~&ERROR ~(~A~): ~A~%" (car test) e)))))
  (format t "~&~D tests, ~D checks, ~D failed, ~D errors~%" (length *tests*) *checks* *failures* errors)
  (sb-ext:exit :code (if (and (zerop *failures*) (zerop errors)) 0 1)))
