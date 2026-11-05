;;;; The Lisp side of the parity harness: sbcl --script lisp/parity.lisp <format> <transcript>
;;;; <cwd or -> <whole|turns> <budget>... (memory/selection/lispparity_test.go runs it).

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))
(handler-case (om:main)
  (error (e)
    (format *error-output* "om-engine: ~A~%" e)
    (sb-ext:exit :code 1 :abort t)))
