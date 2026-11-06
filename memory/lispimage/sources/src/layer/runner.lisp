;;;; Running model code (docs/lisp-core.md, "The safe reader", "Running"): each run is a thread
;;;; under a budget it checks itself (limits.lisp), stopped at its next check when it runs away
;;;; and terminated only as a last resort; its output is captured; the memory tool shows the
;;;; last value only when nothing was printed (the output rule).

(in-package "OM.LAYER")

(defvar *timeout-seconds* 5 "How long one run of model code may take.")

(defvar *heap-limit-bytes* (* 512 1024 1024)
  "How far the heap may grow over its size when a run starts before the run is stopped.")

(defvar *output-limit* 100000 "The characters of output a run keeps.")

(defstruct call
  "One memory tool call's context: what the reads read and who writes."
  (branch #() :type simple-vector)
  (curated-dir nil)
  (project "")
  (session "")
  (model ""))

(defvar *call* nil "The call model code runs in.")

(defvar *session-model* nil "The session model adaptable functions dispatch on.")

(defmacro with-call ((call) &body body)
  `(let* ((*call* ,call) (*session-model* (call-model *call*))) ,@body))

;;; The wrappers model code reads FORMAT and ERROR as

(defun check-control (control)
  (unless (stringp control)
    (refuse "a format control must be a string"))
  (loop for i from 0 below (1- (length control))
        when (char= (char control i) #\~)
          do (let ((j (1+ i)))
               ;; skip the directive's parameters and modifiers
               (loop while (and (< j (length control))
                                (or (digit-char-p (char control j)) (find (char control j) ",:@'#vV+-")))
                     do (when (char= (char control j) #\') (incf j))
                        (incf j))
               (when (and (< j (length control)) (find (char control j) "/?"))
                 (refuse "the format directive ~~~A, which calls or reads code" (char control j))))))

(defun safe-format (destination control &rest args)
  (check-control control)
  (unless (or (null destination) (eq destination t) (streamp destination)
              (and (stringp destination) (array-has-fill-pointer-p destination)))
    (refuse "format writes to nil, t, a stream or a string with a fill pointer"))
  (apply #'format destination control args))

(defun safe-error (control &rest args)
  (check-control control)
  (error 'simple-error :format-control control :format-arguments args))

;;; Allocation: the checked MAKE-ARRAY, MAKE-STRING, MAKE-LIST and MAKE-HASH-TABLE are in
;;; limits.lisp, their caps derived from the dynamic space.

;;; Running

(defparameter +inherited-specials+
  '(*now* *layer-root* *timeout-seconds* *heap-limit-bytes* *output-limit* *fuel-steps*
    *stop-grace-seconds* *allocation-fraction* *stack-reserve-fraction*)
  "The settings a run's thread sees as the caller sees them (a new thread sees only global
values). *definition-timeout* is added by adaptable.lisp.")

(defun seconds-since (start) (/ (- (get-internal-real-time) start) internal-time-units-per-second))

(defun run-sandboxed (fn &key (timeout *timeout-seconds*) (heap-limit *heap-limit-bytes*) (package nil)
                           (steps *fuel-steps*))
  "Runs fn in its own thread with *call* and *session-model* bound as now, its output
captured, under a budget (limits.lisp): steps of fuel, a deadline of timeout seconds and a heap
allowance. Returns (values status value output), status one of :ok, :error (value is the
message), :timeout, :fuel, :heap or :stack.

The run stops itself: the code the safe reader compiled checks the budget at its loop heads,
function entries and calls, and throws to the run's catch tag when it is spent; waits run under
sb-sys:with-deadline. The supervisor (this thread) only asks: past the timeout or the heap
allowance it marks the budget stopped, which the next check sees. A thread that has not ended
*stop-grace-seconds* after that is terminated as the last resort, logged, and the image marked
as needing a restart (image-needs-restart)."
  (let* ((call *call*) (model *session-model*)
         (inherited (mapcar (lambda (s) (cons s (symbol-value s))) +inherited-specials+))
         (out (make-string-output-stream))
         (status nil) (value nil)
         (package (or package *package*))
         (budget (make-run-budget :timeout timeout :heap-limit heap-limit :steps steps))
         (thread
           (sb-thread:make-thread
            (lambda ()
              (progv (mapcar #'car inherited) (mapcar #'cdr inherited)
                (let ((*call* call) (*session-model* model) (*standard-output* out)
                      (*package* package) (*read-eval* nil) (*print-readably* nil)
                      (*print-circle* t) (*print-length* 200) (*print-level* 20) (*print-case* :downcase)
                      (*budget* budget) (exhausted nil))
                  (setf (budget-stack-floor budget) (stack-floor))
                  (let ((stopped
                          (catch (budget-tag budget)
                            (handler-bind ((sb-sys:deadline-timeout
                                             (lambda (c) (declare (ignore c)) (stop-run budget :timeout))))
                              (sb-sys:with-deadline (:seconds timeout :override t)
                                (handler-case
                                    (progn (setf value (funcall fn) status :ok) nil)
                                  (storage-condition (c)
                                    (when (typep c 'sb-kernel::control-stack-exhausted) (setf exhausted t))
                                    (setf status :error value (format nil "storage exhausted: ~A" (type-of c)))
                                    nil)
                                  (error (c)
                                    (setf status :error
                                          value (handler-case (princ-to-string c)
                                                  (error () (format nil "an error of type ~(~A~)" (type-of c)))))
                                    nil)))))))
                    (when stopped (setf status stopped value nil))
                    (when exhausted (rearm-stack-guard))))))
            :name "om model code"))
         (forced (supervise thread budget timeout heap-limit)))
    (when forced (setf status forced))
    (let ((text (get-output-stream-string out)))
      (values (or status :error)
              (case status
                (:timeout (format nil "the code ran past its ~As limit and was ~A" timeout (if forced "terminated" "stopped")))
                (:fuel (format nil "the code ran past its ~D steps and was ~A" steps (if forced "terminated" "stopped")))
                (:heap (format nil "the code grew the heap past ~D MB and was ~A" (floor heap-limit (* 1024 1024)) (if forced "terminated" "stopped")))
                (:stack "the code recursed deeper than its stack allows and was stopped")
                ((nil) "the code ended without a result")
                (t value))
              (if (> (length text) *output-limit*) (subseq text 0 *output-limit*) text)))))

(defun supervise (thread budget timeout heap-limit)
  "Waits for the run's thread, asking it to stop past its timeout or heap allowance. Returns
nil when the thread ended by itself, or the reason it was terminated (the last resort). Its own
waits are under no deadline: a supervisor running inside another run (a definition called from
model code) must not be unwound while its thread runs."
  (let ((start (get-internal-real-time)) (asked nil))
    (sb-sys:with-deadline (:seconds nil :override t)
      (loop
        (multiple-value-bind (result outcome) (sb-thread:join-thread thread :timeout 0.002 :default nil)
          (declare (ignore result))
          (unless (eq outcome :timeout) (return nil)))
        (cond (asked
               (when (> (seconds-since asked) *stop-grace-seconds*)
                 (let ((why (or (budget-stop budget) :timeout)))
                   (last-resort thread why)
                   (return why))))
              ((budget-stop budget) (setf asked (get-internal-real-time)))
              ((> (seconds-since start) timeout)
               (setf (budget-stop budget) :timeout asked (get-internal-real-time)))
              ((> (- (heap-usage) (budget-heap-base budget)) heap-limit)
               (setf (budget-stop budget) :heap asked (get-internal-real-time))))))))

(defun last-resort (thread why)
  "Terminates a thread of model code that ignored its stop: logs it and marks the image as
needing a restart, since the unwind is asynchronous (only model code can be running: every
OM.TOOL call defers interrupts)."
  (let ((line (format nil "om.layer: model code ignored its ~(~A~) stop for ~As; its thread was terminated and the image needs a restart"
                      why *stop-grace-seconds*)))
    (note-restart-needed line)
    (ignore-errors (format *error-output* "~&~A~%" line) (force-output *error-output*))
    (ignore-errors (sb-thread:terminate-thread thread))
    (handler-case (sb-thread:join-thread thread :timeout 2 :default nil)
      (error () nil))
    (when (sb-thread:thread-alive-p thread)
      (note-restart-needed "om.layer: a terminated thread of model code is still running")
      (ignore-errors (format *error-output* "~&om.layer: a terminated thread of model code is still running~%")))))

(defun compile-form (form)
  "A function of no arguments that runs form: compiled by the runner, never by model code. The
compile runs in the run's thread, so the run's timeout covers it; the budget is checked before."
  (tick)
  (handler-bind ((warning #'muffle-warning))
    (compile nil (list 'lambda '() form))))

;;; Showing a value

(defun show-string (v)
  (cond ((stringp v) v)
        ((and (consp v) (every (lambda (x) (or (note-p x) (turn-p x) (memory-p x))) v))
         (format nil "~{~A~^~%~}" (mapcar #'show-string v)))
        ((note-p v) (om.tool:note-line v))
        ((turn-p v) (format nil "~A ~A ~A: ~A" (turn-id v) (turn-when v) (turn-role v) (turn-text v)))
        ((memory-p v) (format nil "~A [~A]: ~A" (mem-name v) (mem-type v) (mem-description v)))
        (t (prin1-to-string v))))

(defvar *remote* nil
  "In a tool child (child.lisp): true, and the OM.TOOL calls that read or write the image's state
are answered by the image over the child's pipe.")

(defun held-message (d model)
  (format nil "Held definition ~(~A~) for ~A: it runs beside the default at the next compactions and replaces it once the evidence settles it."
          (ldef-name d) model))

(defstruct (tool-result (:conc-name tool-result-))
  (text "") (error nil) (held '()))

(defun run-tool-code (text &key (call *call*) (timeout *timeout-seconds*))
  "Runs the memory tool's code text for call's model: every form is read by the safe reader
first (a refusal runs nothing), a top-level (define ...) holds a definition in the model's
layer, every other form is compiled and run in order in one sandboxed thread. The text shown
is what the code printed, or, when it printed nothing, its last value."
  (with-call (call)
    (let* ((model (call-model call))
           (package (model-package model))
           (forms (handler-case (safe-read-all text package)
                    (refused (e) (return-from run-tool-code
                                   (make-tool-result :error (princ-to-string e)))))))
      (let ((held '()))
        (multiple-value-bind (status value output)
            (run-sandboxed
             (lambda ()
               (let ((last nil))
                 (dolist (f forms)
                   (destructuring-bind (form . source) f
                     (setf last
                           (if (and (consp form) (eq (car form) 'om.tool:define))
                               (progn
                                 (tick)
                                 (if *remote*
                                     ;; In a tool child the image holds it (child.lisp).
                                     (remote-invoke "define" (list source))
                                     (let ((d (layer-hold model source)))
                                       (push d held)
                                       (held-message d model))))
                               (funcall (compile-form form))))))
                 (show-string last)))
             :timeout timeout :package package)
          (let ((held (nreverse held)))
            (case status
              (:ok (make-tool-result :text (if (string/= output "") output value) :held held))
              (t (make-tool-result :text output :error value :held held)))))))))
