;;;; Adaptable functions (docs/lisp-core.md, "Live editing"): define-adaptable marks a function a
;;;; model may replace in its layer, with its default and the kernel check its results must pass.
;;;; A call dispatches on the session model: its settled definition runs when it has one (else the
;;;; default), in the sandbox, and its result is checked; a result that fails the check, an error
;;;; or a timeout falls back to the default for that call and counts against the definition. A
;;;; held definition runs beside the primary on a copy of the arguments, its result checked and
;;;; discarded: it never replaces the default's result until it settles, and the run is the
;;;; evidence (ranHeld, and adaptable-failed:<name> on a failure) the next compaction records.

(in-package "OM.LAYER")

(defstruct adaptable
  (name "") (symbol nil) (arity 0) (default nil) (check nil) (doc ""))

(defvar *adaptables* (make-hash-table :test 'equal) "Name -> adaptable.")

(defvar *definition-timeout* 2 "How long one run of a definition may take, in seconds.")

(pushnew '*definition-timeout* +inherited-specials+)

(defmacro define-adaptable (name lambda-list (&key check) &body body)
  "Defines the adaptable function name (a symbol, its package om.engine's): its default is body
over lambda-list (required parameters only), and check, a function of the result and the
arguments, returns nil for a result the kernel accepts or a reason it refuses it. name itself
becomes the dispatcher."
  (let ((doc (if (and (stringp (car body)) (cdr body)) (car body) ""))
        (default (intern (concatenate 'string (symbol-name name) "-DEFAULT") (symbol-package name))))
    (when (intersection lambda-list lambda-list-keywords)
      (error "define-adaptable ~S: only required parameters" name))
    `(progn
       (defun ,default ,lambda-list ,@body)
       (setf (gethash ,(string-downcase (symbol-name name)) *adaptables*)
             (make-adaptable :name ,(string-downcase (symbol-name name)) :symbol ',name
                             :arity ,(length lambda-list) :default #',default
                             :check ,(or check '(constantly nil)) :doc ,doc))
       (defun ,name ,lambda-list
         ,doc
         (call-adaptable ,(string-downcase (symbol-name name)) (list ,@lambda-list)))
       (export ',name (symbol-package ',name))
       ',name)))

(defun adaptable-names ()
  (sort (loop for k being the hash-keys of *adaptables* collect k) #'string<))

;;; Reading a definition

(defun compile-definition (d)
  "Reads d's text with the safe reader into its model's package, checks it is one
(define name lambda-list . body) of an adaptable function with the default's parameters, and
compiles it: sets d's name and fn. Signals refused."
  (let* ((package (model-package (ldef-model d)))
         (forms (safe-read-all (ldef-text d) package)))
    (unless (= (length forms) 1) (refuse "a definition is one (define name (parameters) body...) form"))
    (let ((form (car (first forms))))
      (unless (and (consp form) (eq (car form) 'om.tool:define) (consp (cdr form)) (consp (cddr form)))
        (refuse "a definition is one (define name (parameters) body...) form"))
      (destructuring-bind (name lambda-list &rest body) (cdr form)
        (unless (and (symbolp name) name (eq (symbol-package name) package))
          (refuse "define names an adaptable function: one of ~{~A~^, ~}" (adaptable-names)))
        (let ((a (gethash (string-downcase (symbol-name name)) *adaptables*)))
          (unless a
            (refuse "~(~A~) is not an adaptable function; they are ~{~A~^, ~}" (symbol-name name) (adaptable-names)))
          (unless (and (listp lambda-list)
                       (every (lambda (p) (and (symbolp p) (not (keywordp p)) (not (constantp p))
                                               (not (member p lambda-list-keywords))))
                              lambda-list))
            (refuse "the parameters of ~A are plain names" (adaptable-name a)))
          (unless (= (length lambda-list) (adaptable-arity a))
            (refuse "~A takes ~D ~A" (adaptable-name a) (adaptable-arity a) (if (= 1 (adaptable-arity a)) "argument" "arguments")))
          (setf (ldef-name d) (adaptable-name a))
          (multiple-value-bind (fn warnings failure)
              (handler-bind ((warning #'muffle-warning))
                (let ((*package* package))
                  (compile nil `(lambda ,lambda-list ,@body))))
            (declare (ignore warnings))
            (when failure (refuse "the definition of ~A does not compile" (adaptable-name a)))
            (setf (ldef-fn d) fn))
          d)))))

;;; Evidence the next compaction records

(defvar *evidence* (make-hash-table :test 'equal) "Model -> (ran-held-ids . counts hash).")
(defvar *evidence-lock* (sb-thread:make-mutex :name "om.layer evidence"))

(defun evidence-of (model)
  (or (gethash model *evidence*)
      (setf (gethash model *evidence*) (cons '() (make-hash-table :test 'equal)))))

(defun note-ran-held (d)
  (sb-thread:with-mutex (*evidence-lock*)
    (let ((e (evidence-of (ldef-model d))))
      (pushnew (ldef-held-id d) (car e) :test #'string=))))

(defun count-failure (d)
  (sb-thread:with-mutex (*evidence-lock*)
    (incf (gethash (adaptable-failed-key (ldef-name d)) (cdr (evidence-of (ldef-model d))) 0))))

(defun layer-ran-held (model)
  "The held ids of the model's definitions that ran since the evidence was last taken, sorted."
  (sb-thread:with-mutex (*evidence-lock*)
    (sort (copy-list (car (evidence-of model))) #'string<)))

(defun layer-failures (model)
  "The adaptable-failed:<name> counts since the evidence was last taken, an alist."
  (sb-thread:with-mutex (*evidence-lock*)
    (let ((out '()))
      (maphash (lambda (k v) (push (cons k v) out)) (cdr (evidence-of model)))
      (sort out #'string< :key #'car))))

(defun layer-take-evidence (model)
  "(values ran-held failures) for the compaction pass, and starts a new unit."
  (let ((ran (layer-ran-held model)) (failures (layer-failures model)))
    (sb-thread:with-mutex (*evidence-lock*) (remhash model *evidence*))
    (values ran failures)))

;;; Dispatch

(defun copy-arg (x) (if (and (typep x 'sequence) (not (stringp x))) (copy-seq x) x))

(defun run-definition (d a args)
  "Runs definition d of adaptable a on args in the sandbox and checks the result:
(values ok value reason)."
  (multiple-value-bind (status value)
      (run-sandboxed (lambda () (apply (ldef-fn d) (mapcar #'copy-arg args)))
                     :timeout *definition-timeout* :package (model-package (ldef-model d)))
    (if (eq status :ok)
        (let ((why (handler-case (apply (adaptable-check a) value args)
                     (error (e) (format nil "the check signalled ~A" e)))))
          (if why (values nil value why) (values t value nil)))
        (values nil nil (if (stringp value) value (string-downcase (symbol-name status)))))))

(defvar *last-fallback* nil "The last fallback: (model name reason), for status and tests.")

(defun call-adaptable (name args)
  (let* ((a (or (gethash name *adaptables*) (error "no adaptable function ~A" name)))
         (model *session-model*))
    (if (or (null model) (string= model ""))
        (apply (adaptable-default a) args)
        (let* ((l (load-layer model))
               (settled (find name (loaded-settled l) :key #'ldef-name :test #'string=))
               (held (find name (loaded-held l) :key #'ldef-name :test #'string=))
               (result (if settled
                           (multiple-value-bind (ok value reason) (run-definition settled a args)
                             (if ok
                                 value
                                 (progn (count-failure settled)
                                        (setf *last-fallback* (list model name reason))
                                        (apply (adaptable-default a) args))))
                           (apply (adaptable-default a) args))))
          (when held
            (note-ran-held held)
            (multiple-value-bind (ok value reason) (run-definition held a args)
              (declare (ignore value))
              (unless ok
                (count-failure held)
                (setf *last-fallback* (list model name reason)))))
          result))))
