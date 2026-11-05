;;;; Running model code (docs/lisp-core.md, "The safe reader", "Running"): each run is a thread
;;;; with a timeout and a heap check, killed when it runs away; its output is captured; the
;;;; memory tool shows the last value only when nothing was printed (the output rule).

(in-package "OM.LAYER")

(defvar *timeout-seconds* 5 "How long one run of model code may take.")

(defvar *heap-limit-bytes* (* 512 1024 1024)
  "How far the heap may grow over its size when a run starts before the run is killed.")

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

;;; Allocation: one allocation of model code holds at most +max-elements+ elements, so a
;;; mistaken size fails at once instead of exhausting the heap before the heap check sees it.

(defparameter +max-elements+ (* 16 1024 1024))

(defun check-size (dimensions)
  (let ((n (if (listp dimensions) (reduce #'* dimensions) dimensions)))
    (unless (and (integerp n) (<= 0 n +max-elements+))
      (refuse "an allocation of ~A elements (at most ~D)" n +max-elements+))))

(defun safe-make-array (dimensions &rest args)
  (check-size dimensions)
  (apply #'make-array dimensions args))

(defun safe-make-string (size &rest args)
  (check-size size)
  (apply #'make-string size args))

(defun safe-make-list (size &rest args)
  (check-size size)
  (apply #'make-list size args))

;;; Running

(defparameter +inherited-specials+
  '(*now* *layer-root* *timeout-seconds* *heap-limit-bytes* *output-limit*)
  "The settings a run's thread sees as the caller sees them (a new thread sees only global
values). *definition-timeout* is added by adaptable.lisp.")

(defun heap-usage () (sb-kernel:dynamic-usage))

(defun run-sandboxed (fn &key (timeout *timeout-seconds*) (heap-limit *heap-limit-bytes*) (package nil))
  "Runs fn in its own thread with *call* and *session-model* bound as now, its output
captured. Returns (values status value output), status one of :ok, :error (value is the
message), :timeout or :heap (the thread was killed)."
  (let* ((call *call*) (model *session-model*)
         (inherited (mapcar (lambda (s) (cons s (symbol-value s))) +inherited-specials+))
         (out (make-string-output-stream))
         (status nil) (value nil)
         (package (or package *package*))
         (thread
           (sb-thread:make-thread
            (lambda ()
              (progv (mapcar #'car inherited) (mapcar #'cdr inherited)
              (let ((*call* call) (*session-model* model) (*standard-output* out)
                    (*package* package) (*read-eval* nil) (*print-readably* nil)
                    (*print-circle* t) (*print-length* 200) (*print-level* 20) (*print-case* :downcase))
                (handler-case
                    (setf value (funcall fn) status :ok)
                  (storage-condition (c)
                    (setf status :error value (format nil "storage exhausted: ~A" (type-of c))))
                  (error (c)
                    (setf status :error
                          value (handler-case (princ-to-string c)
                                  (error () (format nil "an error of type ~(~A~)" (type-of c))))))))))
            :name "om model code"))
         (start (get-internal-real-time))
         (base (heap-usage)))
    (loop
      (multiple-value-bind (result outcome) (sb-thread:join-thread thread :timeout 0.002 :default nil)
        (declare (ignore result))
        (unless (eq outcome :timeout)
          (return)))
      (let ((elapsed (/ (- (get-internal-real-time) start) internal-time-units-per-second)))
        (cond ((> elapsed timeout) (setf status :timeout) (kill thread) (return))
              ((> (- (heap-usage) base) heap-limit) (setf status :heap) (kill thread) (return)))))
    (let ((text (get-output-stream-string out)))
      (values (or status :error)
              (case status
                (:timeout (format nil "the code ran past its ~As limit and was stopped" timeout))
                (:heap (format nil "the code grew the heap past ~D MB and was stopped" (floor heap-limit (* 1024 1024))))
                ((nil) "the code ended without a result")
                (t value))
              (if (> (length text) *output-limit*) (subseq text 0 *output-limit*) text)))))

(defun kill (thread)
  (ignore-errors (sb-thread:terminate-thread thread))
  (handler-case (sb-thread:join-thread thread :timeout 2 :default nil)
    (error () nil))
  (when (sb-thread:thread-alive-p thread)
    (warn "om.layer: a runaway thread of model code could not be stopped")))

(defun compile-form (form)
  "A function of no arguments that runs form: compiled by the runner, never by model code."
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
                               (let ((d (layer-hold model source)))
                                 (push d held)
                                 (format nil "Held definition ~(~A~) for ~A: it runs beside the default at the next compactions and replaces it once the evidence settles it."
                                         (ldef-name d) model))
                               (funcall (compile-form form))))))
                 (show-string last)))
             :timeout timeout :package package)
          (let ((held (nreverse held)))
            (case status
              (:ok (make-tool-result :text (if (string/= output "") output value) :held held))
              (t (make-tool-result :text output :error value :held held)))))))))
