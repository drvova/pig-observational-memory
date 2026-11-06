;;;; Adaptable functions (docs/lisp-core.md, "Live editing"): define-adaptable marks a function a
;;;; model may replace in its layer, with its default and the kernel check its results must pass.
;;;; A call dispatches on the session model: its settled definition runs when it has one (else the
;;;; default), in the sandbox, and its result is checked; a result that fails the check, an error
;;;; or a timeout falls back to the default for that call and counts against the definition. A
;;;; held definition runs beside the primary on a copy of the arguments, its result checked and
;;;; discarded: it never replaces the default's result until it settles. What each run did is
;;;; kept per session in its interval (rules/evidence.lisp): a held one's failures
;;;; (adaptable-failed:<name> in the next pass), a settled one's runs and failures (the retire
;;;; rule's units), and the timeouts after the second of which a definition is not called again
;;;; until the session's next compaction. A held definition's evidence is the compaction's
;;;; snapshot of the layer (ranHeld), not its calls.

(in-package "OM.LAYER")

(defstruct adaptable
  (name "") (symbol nil) (arity 0) (default nil) (check nil) (doc "")
  ;; coverage: the function decides what the source coverage holds (which entries a record
  ;; cites, which ones level 0 reads), so any failure of its settled definition reverts it at once.
  (coverage nil))

(defvar *adaptables* (make-hash-table :test 'equal) "Name -> adaptable.")

(defvar *definition-timeout* 2 "How long one run of a definition may take, in seconds.")

(pushnew '*definition-timeout* +inherited-specials+)

(defmacro define-adaptable (name lambda-list (&key check coverage) &body body)
  "Defines the adaptable function name (a symbol, its package om.engine's): its default is body
over lambda-list (required parameters only), and check, a function of the result and the
arguments, returns nil for a result the kernel accepts or a reason it refuses it. coverage marks
a function whose failure touches source coverage. name itself becomes the dispatcher."
  (let ((doc (if (and (stringp (car body)) (cdr body)) (car body) ""))
        (default (intern (concatenate 'string (symbol-name name) "-DEFAULT") (symbol-package name))))
    (when (intersection lambda-list lambda-list-keywords)
      (error "define-adaptable ~S: only required parameters" name))
    `(progn
       (defun ,default ,lambda-list ,@body)
       (setf (gethash ,(string-downcase (symbol-name name)) *adaptables*)
             (make-adaptable :name ,(string-downcase (symbol-name name)) :symbol ',name
                             :arity ,(length lambda-list) :default #',default
                             :check ,(or check '(constantly nil)) :doc ,doc :coverage ,(and coverage t)))
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
                  (compile nil `(lambda ,lambda-list ,@(ticked-body body :doc t)))))
            (declare (ignore warnings))
            (when failure (refuse "the definition of ~A does not compile" (adaptable-name a)))
            (setf (ldef-fn d) fn))
          d)))))

;;; Evidence the next compaction records (kept per session: rules/evidence.lisp)

(defun layer-ran-held (model)
  "The held ids a compaction of model would credit now: every held definition in its layer."
  (sort (mapcar #'ldef-held-id (layer-held model)) #'string<))

(defun layer-failures (model)
  "The adaptable-failed:<name> counts of model's held definitions in the current session's
interval, an alist."
  (interval-failures (current-session) model))

(defun layer-take-evidence (model)
  "(values ran-held failures) for a compaction pass of the current session, and starts its new
interval."
  (let ((ran (layer-ran-held model)) (failures (layer-failures model)))
    (take-interval (current-session) model)
    (values ran failures)))

;;; Dispatch

(defun copy-arg (x) (if (and (typep x 'sequence) (not (stringp x))) (copy-seq x) x))

(defun run-definition (d a args)
  "Runs definition d of adaptable a on args in the sandbox and checks the result:
(values ok value reason status), status the run's (:ok, :timeout, :error...)."
  (multiple-value-bind (status value)
      (run-sandboxed (lambda () (apply (ldef-fn d) (mapcar #'copy-arg args)))
                     :timeout *definition-timeout* :package (model-package (ldef-model d)))
    (if (eq status :ok)
        (let ((why (handler-case (apply (adaptable-check a) value args)
                     (error (e) (format nil "the check signalled ~A" e)))))
          (if why (values nil value why :ok) (values t value nil :ok)))
        (values nil nil (if (stringp value) value (string-downcase (symbol-name status))) status))))

(defvar *last-fallback* nil "The last fallback: (model name reason), for status and tests.")

(defun call-adaptable (name args)
  (let* ((a (or (gethash name *adaptables*) (error "no adaptable function ~A" name)))
         (model *session-model*))
    (if (or (null model) (string= model ""))
        (apply (adaptable-default a) args)
        (let* ((l (load-layer model))
               (settled (find name (loaded-settled l) :key #'ldef-name :test #'string=))
               (held (find name (loaded-held l) :key #'ldef-name :test #'string=))
               (result (if (and settled (not (breaker-open-p settled)))
                           (multiple-value-bind (ok value reason status) (run-definition settled a args)
                             (note-run settled ok status)
                             (if ok
                                 value
                                 (progn (setf *last-fallback* (list model name reason))
                                        (when (adaptable-coverage a)
                                          (revert-settled model name (ldef-digest settled)
                                                          (format nil "it failed on source coverage: ~A" reason)))
                                        (apply (adaptable-default a) args))))
                           (apply (adaptable-default a) args))))
          (when (and held (not (breaker-open-p held)))
            (multiple-value-bind (ok value reason status) (run-definition held a args)
              (declare (ignore value))
              (note-run held ok status)
              (unless ok
                (setf *last-fallback* (list model name reason)))))
          result))))
