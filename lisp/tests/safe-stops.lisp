;;;; Cooperative limits (lisp/src/layer/limits.lisp): fuel and the deadline stop code that never
;;;; yields, no handler of model code catches a stop or a storage condition, a stop never lands
;;;; inside an OM.TOOL write, allocations are capped relative to the heap, the last resort marks
;;;; the image for a restart, and each escape route of the sandboxing notes is refused.

(in-package "OM.LAYER")

(defvar *stops-root*
  (format nil "/tmp/om-stops-test-~D-~D/" (sb-posix:getpid) (random 1000000 (make-random-state t))))

(defun stops-dir (name) (join-path *stops-root* name))

(defun stop-text (text &key (steps *fuel-steps*) (timeout 2) curated)
  "The error of running text, or its text when it ran."
  (let ((*fuel-steps* steps))
    (let ((r (run text :timeout timeout :curated curated)))
      (or (tool-result-error r) (tool-result-text r)))))

;;; Fuel

(om::deftest stops-fuel-ends-code-that-never-yields
  ;; a long timeout: only the fuel can stop these
  (dolist (text '("(loop)" "(loop for i from 0 do (incf i))" "(loop named outer for i from 0 collect i)"
                  "(tagbody a (go a))" "(do () (nil))" "(do* ((i 0 (1+ i))) (nil))"
                  "(dolist (x '(1)) a (go a))" "(dotimes (i 1) a (go a))"
                  "(labels ((f () (f))) (f))" "(flet ((g () (loop))) (g))" "(defun spin () (spin)) (spin)"
                  "(let ((f nil)) (setf f (lambda () (funcall f))) (funcall f))"
                  "(let ((f nil)) (setf f (lambda () (apply f '()))) (apply f '()))"
                  "(mapc #'(lambda (x) (loop)) '(1))"))
    (let ((start (get-internal-real-time)))
      (om::check (stop-text text :steps 100000 :timeout 60) "the code ran past its 100000 steps and was stopped" text)
      (om::check (< (seconds-since start) 10) t (format nil "~A stopped by its fuel" text))))
  ;; every API call is a step too
  (om::check (stop-text "(loop (adaptables))" :steps 5000 :timeout 60) "the code ran past its 5000 steps and was stopped")
  ;; the same loops still run when they end
  (om::check (stop-text "(loop for x in '(1 2 3) do (print x))") (format nil "~%1 ~%2 ~%3 "))
  (om::check (stop-text "(let ((n 0)) (dolist (x '(1 2)) a (incf n)) (dotimes (i 3) (incf n)) n)") "5")
  (om::check (stop-text "(do ((i 0 (1+ i))) ((> i 2) i) (declare (fixnum i)))") "3")
  (om::check (stop-text "(loop named outer for i from 0 when (> i 2) do (return-from outer i))") "3")
  (om::check (stop-text "(mapcar #'(lambda (x) (* x x)) '(1 2 3))") "(1 4 9)")
  (om::check (stop-text "(mapcar (lambda (x) (+ x 1)) '(1 2))") "(2 3)")
  (om::check (stop-text "(defun twice (x) \"Doubles x.\" (declare (number x)) (* 2 x)) (twice 4)") "8")
  ;; the deadline stops it as well
  (om::check (stop-text "(loop)" :timeout 0.3) "the code ran past its 0.3s limit and was stopped"))

;;; No handler of model code catches a stop

(om::deftest stops-are-not-caught-by-model-handlers
  (om::check (stop-text "(ignore-errors (loop))" :steps 100000 :timeout 60) "the code ran past its 100000 steps and was stopped")
  (om::check (stop-text "(handler-case (loop) (error () :caught))" :steps 100000 :timeout 60)
             "the code ran past its 100000 steps and was stopped")
  (om::check (stop-text "(handler-case (loop) (:no-error (x) x))" :steps 100000 :timeout 60)
             "the code ran past its 100000 steps and was stopped")
  (om::check (stop-text "(handler-case (car 1) (error () :caught))") ":caught")
  (om::check (stop-text "(ignore-errors (car 1))") "nil")
  ;; a clause that could catch a storage condition or a stop is refused when read
  (dolist (text '("(handler-case 1 (t () 2))" "(handler-case 1 ((or error t) () 2))" "(handler-case 1 ((not integer) () 2))"
                  "(handler-case 1 ((and t) () 2))" "(handler-case 1 (helper () 2))"
                  "(handler-case 1 (condition () 2))" "(handler-case 1 (serious-condition () 2))"
                  "(handler-case 1 (storage-condition () 2))" "(handler-bind ((error #'identity)) 1)"
                  "(restart-case 1)" "(with-simple-restart (x \"y\") 1)"))
    (om::check (refused-p text) t text))
  (om::check (stop-text "(handler-case 1 (t () 2))") "refused: a handler-case clause on t: model code catches only error")
  ;; the model's ERROR as a type leaves storage conditions out
  (om::check (subtypep 'storage-condition 'safe-error) nil)
  (om::check (typep (make-condition 'sb-kernel::heap-exhausted-error) 'safe-error) nil)
  ;; recursion is stopped before the guard page, whatever the handler
  (dolist (text '("(defun f1 (n) (+ 1 (f1 n))) (f1 0)" "(defun f2 (n) (+ 1 (f2 n))) (ignore-errors (f2 0))"
                  "(defun f3 (n) (+ 1 (f3 n))) (handler-case (f3 0) (error () :caught))"
                  "(labels ((f4 (n) (+ 1 (f4 n)))) (f4 0))"))
    (om::check (stop-text text) "the code recursed deeper than its stack allows and was stopped" text))
  ;; a stack exhausted inside CL's own code is an error no model handler catches, and the guard
  ;; page is re-armed: the image survives it every time
  (dotimes (i 3)
    (om::check (stop-text "(let ((x nil)) (dotimes (i 1000000) (setf x (list x))) (handler-case (copy-tree x) (error () :caught)))")
               "storage exhausted: control-stack-exhausted"))
  (om::check (stop-text "(+ 1 2)") "3"))

;;; A stop never lands inside an OM.TOOL write

(defun lock-free-p (dir)
  "Whether this image could take the store's flock now, and its mutex is free."
  (and (null (sb-thread:mutex-owner *lock-mutex*))
       (let ((fd (sb-posix:open (join-path dir ".lock") (logior sb-posix:o-creat sb-posix:o-rdwr) #o600)))
         (unwind-protect (prog1 (zerop (flock fd 6)) (flock fd 8))
           (sb-posix:close fd)))))

(defun store-intact-p (dir)
  "Every memory file of the store parses and passes curated-check."
  (every (lambda (name)
           (handler-case (let ((m (read-memory dir name))) (and m (curated-check m) t))
             (error () nil)))
         (memory-file-names dir)))

(om::deftest stops-leave-the-store-whole
  (let ((dir (stops-dir "curated")))
    ;; a run past its deadline in a loop of saves stops between two saves
    (at-time ("2026-11-01T12:00:00Z")
      (om::check (stop-text "(loop for i from 0 do (save (format nil \"m~D\" i) \"user\" \"A memory\"))" :timeout 0.5 :curated dir)
                 "the code ran past its 0.5s limit and was stopped"))
    (om::check (lock-free-p dir) t "lock free after a stop between saves")
    (om::check (store-intact-p dir) t "store intact after a stop between saves")
    (om::check (plusp (length (memory-file-names dir))) t)
    ;; a run whose save waits on a flock another holder keeps ends in the wait, holding nothing
    (let* ((held (sb-posix:open (join-path dir ".lock") (logior sb-posix:o-creat sb-posix:o-rdwr) #o600))
           (before (memory-file-names dir)))
      (unwind-protect
           (progn
             (om::check (flock held 2) 0)
             (at-time ("2026-11-01T12:00:00Z")
               (om::check (stop-text "(save \"waiting\" \"user\" \"Never written\")" :timeout 0.3 :curated dir)
                          "the code ran past its 0.3s limit and was stopped")))
        (flock held 8)
        (sb-posix:close held))
      (om::check (memory-file-names dir) before "nothing written by the stopped save")
      (om::check (lock-free-p dir) t "lock free after a stop in the flock wait"))
    ;; the last resort waits for the API call: interrupts are deferred inside it, so the
    ;; termination lands only after the call wrote its file and released the lock
    (let ((*restart-needed* nil) (*runaway-log* '()) (*stop-grace-seconds* 0.1)
          (*error-output* (make-string-output-stream))
          (path (join-path dir "slow.md")))
      (multiple-value-bind (status message)
          (run-sandboxed (lambda ()
                           (tool-entry (lambda ()
                                         (call-locked dir (lambda ()
                                                            (sleep 0.6)
                                                            (write-atomic path "whole")))
                                         :written)
                                       '())
                           (loop))
                         :timeout 0.1)
        (om::check status :timeout)
        (om::check message "the code ran past its 0.1s limit and was terminated"))
      (om::check (read-file path) "whole" "the API call finished before the termination")
      (om::check (lock-free-p dir) t "lock free after the last resort")
      (om::check (and (image-needs-restart) t) t))))

;;; Allocation caps relative to the heap

(om::deftest stops-cap-allocations-by-the-heap
  (om::check (allocation-cap-bytes) (floor (sb-ext:dynamic-space-size) 16))
  (let ((elements (1+ (floor (allocation-cap-bytes) 8))))
    (om::check (stop-text (format nil "(length (make-array ~D))" elements))
               (format nil "refused: an allocation of ~D elements (at most ~D of these: one allocation takes at most ~D MB, 1/16 of the heap)"
                       elements (1- elements) (floor (allocation-cap-bytes) (* 1024 1024)))))
  ;; a smaller part of the heap: 1/1024 of it
  (let ((*allocation-fraction* 1/1024)
        (fits (floor (sb-ext:dynamic-space-size) (* 1024 16))))
    (om::check (stop-text (format nil "(length (make-array ~D))" (floor (sb-ext:dynamic-space-size) (* 1024 16)))) (princ-to-string fits))
    (dolist (form (list (format nil "(make-array ~D)" (floor (sb-ext:dynamic-space-size) 4096))
                        (format nil "(make-array '(~D 4))" (floor (sb-ext:dynamic-space-size) 16384))
                        (format nil "(make-array ~D :element-type 'double-float)" (floor (sb-ext:dynamic-space-size) 4096))
                        (format nil "(make-string ~D)" (floor (sb-ext:dynamic-space-size) 2048))
                        (format nil "(make-list ~D)" (floor (sb-ext:dynamic-space-size) 8192))
                        (format nil "(make-hash-table :size ~D)" (floor (sb-ext:dynamic-space-size) 20480))
                        (format nil "(make-hash-table :rehash-size ~D)" (floor (sb-ext:dynamic-space-size) 20480))
                        (format nil "(let ((v (make-array 1 :fill-pointer 1 :adjustable t))) (vector-push-extend 1 v ~D))"
                                (floor (sb-ext:dynamic-space-size) 4096))
                        (format nil "(expt 2 ~D)" (floor (sb-ext:dynamic-space-size) 64))))
      (om::check (and (search "refused: an allocation of" (stop-text form)) t) t form)))
  (om::check (stop-text "(make-hash-table :rehash-size 1000.0)") "refused: a hash table :rehash-size of 1000.0d0 (at most 8.0)")
  (om::check (stop-text "(length (make-string 3 :initial-element #\\a))") "3")
  (om::check (stop-text "(hash-table-count (make-hash-table :size 10 :test 'equal))") "0")
  (om::check (stop-text "(expt 2 100)") "1267650600228229401496703205376")
  ;; an allocation within the cap but past the run's heap allowance stops the run
  (let ((*heap-limit-bytes* (* 8 1024 1024)))
    (om::check (stop-text "(ignore-errors (make-array 2000000))") "the code grew the heap past 8 MB and was stopped")))

;;; The last resort

(om::deftest stops-last-resort-marks-the-image
  (let ((*restart-needed* nil) (*runaway-log* '()) (*stop-grace-seconds* 0.2)
        (log (make-string-output-stream)))
    (om::check (image-needs-restart) nil)
    ;; a function compiled without checks stands for a thread that ignores its stop
    (multiple-value-bind (status message)
        (let ((*error-output* log)) (run-sandboxed (lambda () (loop)) :timeout 0.2))
      (om::check status :timeout)
      (om::check message "the code ran past its 0.2s limit and was terminated"))
    (om::check (and (search "needs a restart" (or (image-needs-restart) "")) t) t)
    (om::check (and (search "model code ignored its timeout stop" (get-output-stream-string log)) t) t)
    (om::check (length *runaway-log*) 1)
    ;; the image goes on
    (om::check (stop-text "(+ 2 2)") "4")))

;;; The escape routes of the sandboxing notes

(om::deftest stops-escape-routes-refused-when-read
  (dolist (text '(;; satisfies, in typep, check-type and deftype
                  "(typep 1 '(satisfies evenp))" "(let ((x 1)) (check-type x (satisfies evenp)))"
                  "(deftype even () '(satisfies evenp))"
                  ;; reading
                  "(read-from-string \"(1)\")" "(read)" "(let ((*read-eval* t)) 1)"
                  ;; package prefixes, SBCL's extended form included
                  "cl::(eval 1)" "(cl::(eval 1))" "om.layer::(refuse \"x\")" "sb-ext::(quit)" "foo:bar" "foo::bar"
                  "(om.tool::notes)" ":foo:bar"
                  ;; rebinding the printer, the reader and the package
                  "(let ((*package* nil)) 1)" "(let ((*readtable* nil)) 1)" "(let ((*print-circle* nil)) 1)"
                  "(setf *print-length* nil)" "(let ((*print-pretty* t)) 1)" "(let ((*print-readably* t)) 1)"
                  "(defparameter *print-level* 1)"
                  ;; dynamic binding and symbols
                  "(progv '(x) '(1) 1)" "(symbol-value 'x)" "(set 'x 1)" "(macro-function 'loop)"
                  "(fdefinition 'car)" "(symbol-function 'car)" "(boundp 'x)"
                  ;; CLOS and conditions
                  "(make-instance 'x)" "(defclass x () ())" "(defmethod m (x) x)" "(defgeneric m (x))"
                  "(find-class 'x)" "(slot-value x 'y)" "(define-condition c () ())" "(make-condition 'error)"
                  "(defstruct s a)" "(signal 'error)"
                  ;; calling code by name
                  "(compile nil '(lambda () 1))" "(eval 1)" "(formatter \"~a\")"))
    (om::check (refused-p text) t text)))

(om::deftest stops-escape-routes-refused-when-run
  ;; ~/ and ~? in a control string built at run time, through every way of calling format
  (dolist (text '("(format nil (concatenate 'string \"~\" \"/x/\") 1)"
                  "(funcall 'format nil \"~?\" \"a\" nil)"
                  "(apply #'format nil (list \"~@?\" \"a\" nil))"
                  "(format nil (format nil \"~A/x/\" #\\~) 1)"
                  "(error (concatenate 'string \"~\" \"/x/\"))"))
    (om::check (and (search "refused: the format directive" (stop-text text)) t) t text))
  ;; check-type's string is shown, never read as a control
  (om::check (stop-text "(let ((x 1)) (check-type x string \"~/foo/\"))") "The value of x is 1, which is not ~/foo/.")
  ;; coerce of a list to a function
  (dolist (text '("(funcall (coerce '(lambda () 1) 'function))" "(coerce 'car 'function)"
                  "(coerce '(lambda () 1) '(function () t))"))
    (om::check (stop-text text) "refused: coerce to a function: it compiles a list as code" text))
  (om::check (stop-text "(coerce '(1 2) 'vector)") "#(1 2)")
  ;; a symbol's own name is never handed out to change
  (om::check (stop-text "(let ((s (string 'notes))) (setf (char s 0) #\\X) (list s (string 'notes)))") "(\"XOTES\" \"NOTES\")")
  (om::check (stop-text "(let ((s (string-trim \" \" 'notes))) (fill s #\\X) (string-left-trim \"\" 'notes))") "NOTES")
  (om::check (symbol-name 'om.tool:notes) "NOTES")
  (om::check (stop-text "(concatenate 'string \"a\" (string #\\b))") "ab")
  (om::check (stop-text "(typecase \"a\" (string :s) (t :o))") ":s")
  ;; nor a string of the API's records
  (let ((n (make-note :content "the engine's record")))
    (om::check (eq (om.tool:note-content n) (note-content n)) nil)
    (om::check (om.tool:note-content n) "the engine's record"))
  ;; circular structures: a CDR cycle is refused, a CAR cycle prints
  (dolist (text '("(let ((x (list 1 2))) (setf (cdr (last x)) x))" "(let ((x (list 1 2))) (setf (cdr x) x) 1)"
                  "(let ((x (list 1 2 3))) (setf (cddr x) x) 1)" "(let ((x (list 1))) (setf (rest x) x) 1)"
                  "(let ((x (list 1 2))) (psetf (cdr x) x) 1)" "(let ((x (list 1 2))) (nconc x x))"
                  "(let ((x (list 1 2))) (apply #'nconc (list x x)))" "(mapcan (let ((x (list 1))) (lambda (y) x)) '(1 2))"
                  "(let ((x (list 1))) (mapcon (lambda (y) x) '(1 2)))" "(let ((x (list 3 1))) (merge 'list x x #'<))"))
    (om::check (stop-text text) "refused: a list whose tail points back into itself (a circular list, which the image cannot walk)" text))
  (om::check (stop-text "(let ((x (list 1 2))) (loop repeat 2 nconc x))") "(1 2 1 2)")
  (om::check (stop-text "(let ((x (list 1 2)) (y (list 3))) (nconc x y) x)") "(1 2 3)")
  (om::check (stop-text "(let ((x (list 1 2))) (setf (cdr x) (list 5)) x)") "(1 5)")
  (om::check (stop-text "(let ((x (list 1))) (setf (car x) x) x)") "#1=(#1#)"))

(om::deftest stops-cleanup
  (sb-ext:run-program "/bin/rm" (list "-rf" *stops-root*))
  (om::check (probe-file *stops-root*) nil))
