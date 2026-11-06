;;;; Last of W4's files: every OM.TOOL function is made an entry point (tool-entry, limits.lisp:
;;;; one budget check, then the call with interrupts deferred and no deadline), and OM.TOOL is
;;;; locked once its API is defined, so model code can neither redefine nor rebind its functions
;;;; (an flet of om.tool:notes is a package-lock violation).

(in-package "OM.LAYER")

(defvar *tool-entries* (make-hash-table :test 'eq)
  "OM.TOOL function name -> the entry point it was wrapped in, so a reload wraps it once.")

(sb-ext:without-package-locks
  (do-external-symbols (s "OM.TOOL")
    (when (and (fboundp s) (not (macro-function s)) (not (special-operator-p s))
               (not (eq (fdefinition s) (gethash s *tool-entries*))))
      (let* ((f (fdefinition s))
             (name s)
             ;; In a tool child, a call that reads or writes the image's state is one round
             ;; trip to the image (child.lisp), still under tool-entry's check and deferral.
             (entry (lambda (&rest args)
                      (if (and *remote* (remote-function-p name))
                          (tool-entry (lambda (&rest a) (remote-invoke (string-downcase (symbol-name name)) a)) args)
                          (tool-entry f args)))))
        (setf (documentation entry 'function) (documentation f 'function))
        (setf (gethash s *tool-entries*) entry
              (fdefinition s) entry)))))

(sb-ext:lock-package "OM.TOOL")
