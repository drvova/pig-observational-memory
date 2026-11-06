;;;; Cooperative limits for model code (docs/lisp-port-tool-layer.md, "Stopping runaway code").
;;;;
;;;; A run of model code carries a budget: fuel (steps), a deadline and a heap allowance. The
;;;; code the safe reader compiles checks it at every point where it can go on for ever: the
;;;; head of each LOOP, DO, DO*, DOLIST, DOTIMES and TAGBODY tag, the entry of each function it
;;;; defines (DEFUN, LABELS, FLET, LAMBDA), each FUNCALL and APPLY, and each OM.TOOL call. The
;;;; reader reads those names as the macros and functions below (as it reads FORMAT as
;;;; SAFE-FORMAT), so the check is part of the compiled code, not something model code can leave
;;;; out. A spent budget throws to a catch tag the runner set up: model code can neither name
;;;; CATCH nor UNWIND-PROTECT, and a HANDLER-CASE does not see a throw, so it cannot keep going.
;;;;
;;;; The stop is synchronous: it happens only at those points, never inside an OM.TOOL call,
;;;; which runs with interrupts deferred and its deadline off (TOOL-ENTRY), so a stop never lands
;;;; while the curated store's flock or a layer lock is held. The only asynchronous stop left is
;;;; the supervisor's last resort (runner.lisp), for a thread that ignores its stop past
;;;; *stop-grace-seconds*: it is logged and marks the image as needing a restart.
;;;;
;;;; The same file holds the allocation caps, which are derived from the image's dynamic space,
;;;; and the checks that keep model code from building a list that points back into itself
;;;; (a CDR cycle, which CL's list functions walk for ever without reaching a check).

(in-package "OM.LAYER")

;;; The budget of a run

(defvar *fuel-steps* (expt 2 30)
  "The checks one run of model code may pass (loop heads, function entries, calls) before it
is stopped. The deadline normally stops a run first; fuel makes the stop independent of the
clock.")

(defvar *stop-grace-seconds* 2
  "How long a run may take, once asked to stop, to reach a check before the supervisor's last
resort terminates its thread.")

(defstruct (budget (:constructor %make-budget))
  (fuel 0 :type fixnum)
  (steps 0)
  (deadline 0 :type integer)
  (timeout 0)
  (heap-base 0)
  (heap-limit 0)
  (stop nil)
  (stack-floor 0)
  (tag (list :stop)))

(defvar *budget* nil "The budget of the run of model code this thread is, or nil.")

(defun heap-usage () (sb-kernel:dynamic-usage))

(defun make-run-budget (&key timeout heap-limit (steps *fuel-steps*))
  (%make-budget :fuel (min steps most-positive-fixnum) :steps steps :timeout timeout
                :deadline (+ (get-internal-real-time) (round (* timeout internal-time-units-per-second)))
                :heap-base (heap-usage) :heap-limit heap-limit))

(defun stop-run (b why)
  "Ends the run: throws why to the run's catch tag."
  (unless (budget-stop b) (setf (budget-stop b) why))
  (throw (budget-tag b) (budget-stop b)))

(defun check-budget (b)
  (let ((why (or (budget-stop b)
                 (and (<= (budget-fuel b) 0) :fuel)
                 (and (> (get-internal-real-time) (budget-deadline b)) :timeout)
                 (and (> (- (heap-usage) (budget-heap-base b)) (budget-heap-limit b)) :heap))))
    (when why (stop-run b why))))

(declaim (inline tick))
(defun tick ()
  "The check compiled into model code: one step of fuel; every 1024 steps, and at once when
the supervisor asked to stop, the deadline and the heap."
  (let ((b *budget*))
    (when b
      (let ((fuel (decf (budget-fuel b))))
        (when (or (budget-stop b) (<= fuel 0) (zerop (logand fuel 1023)))
          (check-budget b)))))
  nil)

;;; The control stack. A recursion of model code is stopped at its function entries before it
;;; reaches the guard page: SBCL 2.2.9 recycles a finished thread's stack, and a stack whose
;;; guard page was hit and not re-protected kills the image the next time a thread on it hits
;;; it ("control_stack_guard_page_protected not NIL"). A run that exhausted the stack inside
;;; CL's own code re-arms the guard page before its thread ends (rearm-stack-guard).

(declaim (inline stack-pointer))
(defun stack-pointer () (sb-sys:sap-int (sb-vm::current-sp)))

(defun stack-start ()
  (sb-sys:sap-int (sb-vm::current-thread-offset-sap sb-vm::thread-control-stack-start-slot)))

(defun stack-end ()
  (sb-sys:sap-int (sb-vm::current-thread-offset-sap sb-vm::thread-control-stack-end-slot)))

(defvar *stack-reserve-fraction* 1/8
  "The part of a thread's control stack model code's recursion leaves unused (at least 256 KB).")

(defun stack-floor ()
  "The lowest stack pointer a function entry of model code may have in this thread (the stack
grows down)."
  (+ (stack-start) (max (* 256 1024) (floor (* (- (stack-end) (stack-start)) *stack-reserve-fraction*)))))

(defun page-size () (sb-alien:extern-alien "os_vm_page_size" sb-alien:unsigned-long))

(defun dive (floor) (if (< (stack-pointer) floor) 0 (1+ (dive floor))))

(defun rearm-stack-guard ()
  "After a control-stack-exhausted was handled, recurses into the return guard page (the page
above the guard page) and back, which is what makes SBCL re-protect the guard page."
  (dive (+ (stack-start) (* 2 (page-size)) (floor (page-size) 2)))
  nil)

(declaim (inline tick-entry))
(defun tick-entry ()
  "The check at a function entry of model code: the stack, then one step."
  (let ((b *budget*))
    (when (and b (< (stack-pointer) (budget-stack-floor b)))
      (stop-run b :stack)))
  (tick))

(defun wait-check ()
  "The budget check at a wait inside the API, before it holds anything."
  (let ((b *budget*)) (when b (check-budget b))))

(defun wait-pause (seconds)
  "A pure wait inside the API (the store lock's retry): the budget is checked, then the wait
runs under the run's deadline (sb-sys:with-deadline), so a run past it ends here, at a point
where nothing is held yet. It enables interrupts only where an enclosing allow-with-interrupts
allows them."
  (wait-check)
  (let ((b *budget*))
    (if b
        (let ((left (/ (- (budget-deadline b) (get-internal-real-time)) internal-time-units-per-second)))
          (sb-sys:with-deadline (:seconds (max left 0) :override t)
            (sb-sys:with-interrupts (sleep seconds))))
        (sb-sys:with-interrupts (sleep seconds)))))

;;; OM.TOOL entry points

(defun tool-entry (f args)
  "How every OM.TOOL function runs (lock.lisp wraps them): one check of the budget first, then
the call with interrupts deferred and no deadline, so an asynchronous unwind (the last resort)
can land only in model code, and no stop can land between two writes of one call."
  (tick)
  (let ((values (sb-sys:without-interrupts
                  (sb-sys:with-deadline (:seconds nil :override t)
                    (multiple-value-list (apply f args))))))
    ;; A string the API hands out is a copy: model code may change its strings (fill, setf
    ;; char), and the ones a record holds are the engine's.
    (values-list (mapcar (lambda (v) (if (stringp v) (copy-seq v) v)) values))))

;;; The image's health

(defvar *restart-needed* nil
  "nil, or why the image needs a restart: a thread of model code was terminated (the last
resort), so the image may hold state no unwind cleaned up. The server reads it with
image-needs-restart; nothing here restarts the image.")

(defvar *runaway-log* '() "The last resorts taken, newest first: (time . line).")

(defvar *health-lock* (sb-thread:make-mutex :name "om.layer health"))

(defun image-needs-restart ()
  "nil, or the reason the image should be drained and restarted."
  *restart-needed*)

(defun note-restart-needed (line)
  (sb-thread:with-mutex (*health-lock*)
    (push (cons (get-universal-time) line) *runaway-log*)
    (unless *restart-needed* (setf *restart-needed* line))))

;;; Bodies with checks

(defun split-body (body &key doc)
  "(values head forms): the leading declarations (and docstring when doc) of body, and the rest."
  (let ((head '()))
    (loop while (and (consp body)
                     (or (and (consp (car body)) (eq (caar body) 'declare))
                         (and doc (stringp (car body)) (consp (cdr body)) (not (member-if #'stringp head)))))
          do (push (pop body) head))
    (values (nreverse head) body)))

(defun ticked-body (body &key doc)
  "A function body with the entry check after its declarations (and docstring when doc)."
  (multiple-value-bind (head forms) (split-body body :doc doc)
    `(,@head (tick-entry) ,@forms)))

(defun ticked-tagbody (forms)
  "A TAGBODY body with a check at its start and after every tag."
  (cons '(tick)
        (loop for f in forms
              if (or (symbolp f) (integerp f)) append (list f '(tick))
              else collect f)))

(defun ticked-statement-body (body)
  "The body of DO, DOLIST or DOTIMES: declarations, then a tagbody with checks."
  (multiple-value-bind (head forms) (split-body body)
    `(,@head ,@(ticked-tagbody forms))))

(defmacro safe-tagbody (&body forms) `(tagbody ,@(ticked-tagbody forms)))

(defmacro safe-do (vars end &body body) `(do ,vars ,end ,@(ticked-statement-body body)))

(defmacro safe-do* (vars end &body body) `(do* ,vars ,end ,@(ticked-statement-body body)))

(defmacro safe-dolist (spec &body body) `(dolist ,spec ,@(ticked-statement-body body)))

(defmacro safe-dotimes (spec &body body) `(dotimes ,spec ,@(ticked-statement-body body)))

(defvar +wrapped+) ; reader.lisp: the CL names model code reads as a wrapper

(defun loop-keyword (x)
  "A top-level atom of a LOOP: a name the reader read as a wrapper (DO) is the CL keyword again,
and the destructive NCONC clauses become APPEND, which collects the same lists without linking
them (two links to one list would make a cycle)."
  (cond ((not (symbolp x)) x)
        ((member (symbol-name x) '("NCONC" "NCONCING" "SAFE-NCONC") :test #'string=) 'append)
        ((let ((w (rassoc x +wrapped+))) (and w (find-symbol (car w) "COMMON-LISP"))))
        (t x)))

(defmacro safe-loop (&body body)
  (cond ((every #'consp body) `(loop (tick) ,@body))
        (t (let ((clauses (mapcar #'loop-keyword body)) (g (gensym "STEP")))
             (if (and (symbolp (first clauses)) (string= (symbol-name (first clauses)) "NAMED"))
                 `(loop ,(first clauses) ,(second clauses) for ,g = (tick) then (tick) ,@(cddr clauses))
                 `(loop for ,g = (tick) then (tick) ,@clauses))))))

(defmacro safe-defun (name lambda-list &body body)
  `(defun ,name ,lambda-list ,@(ticked-body body :doc t)))

(defmacro safe-lambda (lambda-list &body body)
  `(function (lambda ,lambda-list ,@(ticked-body body :doc t))))

(defun ticked-definitions (defs)
  (mapcar (lambda (d) (if (and (consp d) (consp (cdr d))) `(,(car d) ,(cadr d) ,@(ticked-body (cddr d) :doc t)) d))
          defs))

(defmacro safe-labels (defs &body body) `(labels ,(ticked-definitions defs) ,@body))

(defmacro safe-flet (defs &body body) `(flet ,(ticked-definitions defs) ,@body))

(defun safe-funcall (f &rest args)
  (tick-entry)
  (apply f args))

(defun safe-apply (f &rest args)
  (tick-entry)
  (apply #'apply f args))

;;; Handlers model code may write

(deftype safe-error ()
  "What model code's ERROR names as a type: an error that is no storage condition, so a
handler of model code never keeps going after the stack or the heap ran out."
  '(and error (not storage-condition)))

(defmacro safe-ignore-errors (&body body)
  `(handler-case (progn ,@body) (safe-error (c) (values nil c))))

;;; Names: STRING and the trims return a symbol's own name, which model code could then change
;;; (and with it the symbol, OM.TOOL's included); the wrappers return a copy.

(deftype safe-string (&optional (size (quote *)))
  "What model code's STRING names as a type: string."
  `(string ,size))

(defun named-copy (designator result)
  (if (and (symbolp designator) (eq result (symbol-name designator))) (copy-seq result) result))

(defun safe-string (x) (named-copy x (string x)))

(defun safe-string-trim (bag x) (named-copy x (string-trim bag x)))

(defun safe-string-left-trim (bag x) (named-copy x (string-left-trim bag x)))

(defun safe-string-right-trim (bag x) (named-copy x (string-right-trim bag x)))

;;; Coerce

(defun function-type-p (type)
  (or (eq type 'function)
      (and (consp type) (member (car type) '(function compiled-function)))
      (handler-case (multiple-value-bind (sub sure) (subtypep type 'function) (or sub (not sure)))
        (error () t))))

(defun safe-coerce (object type)
  (when (function-type-p type)
    (refuse "coerce to a function: it compiles a list as code"))
  (coerce object type))

;;; Allocation caps, derived from the image's dynamic space

(defvar *allocation-fraction* 1/16
  "The part of the dynamic space one allocation of model code may take.")

(defun allocation-cap-bytes ()
  (floor (* (sb-ext:dynamic-space-size) *allocation-fraction*)))

(defun check-allocation (count bytes-each)
  "Refuses an allocation of count elements of bytes-each bytes over the cap, and stops the run
when it would grow the heap past the run's allowance."
  (unless (and (integerp count) (<= 0 count))
    (refuse "an allocation of ~A elements" count))
  (let ((bytes (ceiling (* count bytes-each))) (cap (allocation-cap-bytes)))
    (when (> bytes cap)
      (refuse "an allocation of ~D elements (at most ~D of these: one allocation takes at most ~D MB, 1/~D of the heap)"
              count (floor cap bytes-each) (floor cap (* 1024 1024)) (round (/ *allocation-fraction*))))
    (let ((b *budget*))
      (when (and b (> (+ (- (heap-usage) (budget-heap-base b)) bytes) (budget-heap-limit b)))
        (stop-run b :heap)))))

(defun element-bytes (element-type)
  (let ((u (handler-case (upgraded-array-element-type element-type)
             (error () (refuse "the element type ~S" element-type)))))
    (flet ((bits (n) (cond ((<= n 1) 1/8) ((<= n 2) 1/4) ((<= n 4) 1/2) ((<= n 8) 1)
                           ((<= n 16) 2) ((<= n 32) 4) (t 8))))
      (cond ((and (consp u) (member (car u) '(unsigned-byte signed-byte)))
             (bits (if (integerp (second u)) (second u) 64)))
            ((eq u 'bit) 1/8)
            ((subtypep u 'base-char) 1)
            ((subtypep u 'character) 4)
            ((eq u 'single-float) 4)
            ((subtypep u 'complex) 16)
            (t 8)))))

(defun plist-get (args key default)
  (if (and (listp args) (evenp (length args))) (getf args key default) default))

(defun dimension-count (dimensions)
  (if (listp dimensions)
      (if (every (lambda (d) (and (integerp d) (<= 0 d))) dimensions)
          (reduce #'* dimensions)
          (refuse "the array dimensions ~S" dimensions))
      dimensions))

(defun safe-make-array (dimensions &rest args)
  (check-allocation (dimension-count dimensions) (element-bytes (plist-get args :element-type t)))
  (apply #'make-array dimensions args))

(defun safe-make-string (size &rest args)
  (check-allocation size (element-bytes (plist-get args :element-type 'character)))
  (apply #'make-string size args))

(defun safe-make-list (size &rest args)
  (check-allocation size 16)
  (apply #'make-list size args))

(defparameter +hash-entry-bytes+ 40 "About what one entry of a hash table's storage takes.")

(defun safe-make-hash-table (&rest args)
  (let ((size (plist-get args :size nil)) (rehash (plist-get args :rehash-size nil)))
    (when size (check-allocation size +hash-entry-bytes+))
    (typecase rehash
      (null)
      (integer (check-allocation rehash +hash-entry-bytes+))
      (real (unless (<= rehash 8) (refuse "a hash table :rehash-size of ~A (at most 8.0)" rehash)))))
  (apply #'make-hash-table args))

(defun safe-vector-push-extend (new-element vector &optional extension)
  (when (and (arrayp vector) (array-has-fill-pointer-p vector)
             (>= (fill-pointer vector) (array-total-size vector)))
    (let ((total (array-total-size vector)))
      (check-allocation (+ total (or extension (1+ total))) (element-bytes (array-element-type vector)))))
  (if extension
      (vector-push-extend new-element vector extension)
      (vector-push-extend new-element vector)))

(defun safe-expt (base power)
  "EXPT, refused when an exact result would take more than an allocation may."
  (when (and (integerp power) (typep base '(or rational (complex rational))))
    (let ((bits (* (abs power)
                   (max (integer-length (numerator (realpart base))) (integer-length (denominator (realpart base)))
                        (integer-length (numerator (imagpart base))) (integer-length (denominator (imagpart base)))))))
      (when (> bits 64) (check-allocation (ceiling bits 8) 1))))
  (expt base power))

;;; No cycles along CDRs

(defun reaches-p (from cell)
  "Whether cell is reached from from along CDRs (or from is already circular)."
  (let ((slow from) (fast from))
    (loop
      (when (eq fast cell) (return t))
      (unless (consp fast) (return nil))
      (setf fast (cdr fast))
      (when (eq fast cell) (return t))
      (unless (consp fast) (return nil))
      (setf fast (cdr fast) slow (cdr slow))
      (when (eq fast slow) (return t)))))

(defun circular-p (x)
  (let ((slow x) (fast x))
    (loop
      (unless (and (consp fast) (consp (cdr fast))) (return nil))
      (setf fast (cddr fast) slow (cdr slow))
      (when (eq fast slow) (return t)))))

(defun refuse-cycle ()
  (refuse "a list whose tail points back into itself (a circular list, which the image cannot walk)"))

(defun checked-cdr (x) (cdr x))

(defun (setf checked-cdr) (value x)
  (when (and (consp x) (reaches-p value x)) (refuse-cycle))
  (setf (cdr x) value))

(defun cdr-place (place)
  "The place a SETF of a CDR-family place writes through CHECKED-CDR, or nil."
  (when (and (consp place) (consp (cdr place)) (null (cddr place)) (symbolp (car place)))
    (let ((x (second place)))
      (case (car place)
        ((cdr rest) `(checked-cdr ,x))
        (cddr `(checked-cdr (cdr ,x)))
        (cdddr `(checked-cdr (cddr ,x)))
        (cdar `(checked-cdr (car ,x)))))))

(defun checked-pairs (pairs)
  (loop for tail on pairs by #'cddr
        append (if (consp (cdr tail))
                   (list (or (cdr-place (car tail)) (car tail)) (cadr tail))
                   (list (car tail)))))

(defmacro safe-setf (&rest pairs) `(setf ,@(checked-pairs pairs)))

(defmacro safe-psetf (&rest pairs) `(psetf ,@(checked-pairs pairs)))

(defun check-unshared (lists)
  "Refuses lists (all but the last proper) when one cons is in two of them, or one is circular."
  (let ((seen (make-hash-table :test 'eq)))
    (loop for l in lists
          do (when (circular-p l) (refuse-cycle))
             (loop for c on l while (consp c)
                   do (when (gethash c seen) (refuse-cycle))
                      (setf (gethash c seen) t)))))

(defun safe-nconc (&rest lists)
  (check-unshared lists)
  (apply #'nconc lists))

(defun safe-mapcan (f list &rest lists)
  (apply #'safe-nconc (apply #'mapcar f list lists)))

(defun safe-mapcon (f list &rest lists)
  (apply #'safe-nconc (apply #'maplist f list lists)))

(defun safe-merge (type a b predicate &rest args)
  (when (and (listp a) (listp b)) (check-unshared (list a b)))
  (apply #'merge type a b predicate args))
