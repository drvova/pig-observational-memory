;;;; run.lisp: sbcl --script run.lisp CODE-FILE runs a Lisp memory-tool snippet against the mock,
;;;; the way the Go tool runs one: forms in order in a package that uses CL (om functions are
;;;; om:<name>), the output is what the code printed, then the last form's value unless it is nil
;;;; or the last form is a printing call. The last stderr line is "STATUS ok", "STATUS
;;;; reader-error: ..." (a paren, package or other read error) or "STATUS error: ..." (at run time);
;;;; "PRINTED-CHARS n" before it gives the length of the printed part of stdout.

(load (merge-pathnames "om.lisp" *load-truename*))

(defpackage :om-user (:use :cl))

(defparameter *printers*
  '(format print princ prin1 write write-line write-string terpri fresh-line pprint))

(defun status (fmt &rest args)
  (format *error-output* "~&STATUS ~?~%" fmt args)
  (finish-output *error-output*))

(defun run (code)
  (let ((*package* (find-package :om-user))
        (*read-eval* nil)
        (forms '()))
    (handler-case
        (with-input-from-string (in code)
          (loop for form = (read in nil in) until (eq form in) do (push form forms)))
      (error (e)
        (format t "error: ~a~%" e)
        (status "reader-error: ~a" (substitute #\Space #\Newline (princ-to-string e)))
        (return-from run)))
    (setf forms (nreverse forms))
    (let ((printed (make-string-output-stream)))
      (flet ((emit-printed ()
               ;; The printed part goes out first; its length on stderr lets the trial score the
               ;; alternative rule (the value shown only when nothing was printed).
               (let ((text (get-output-stream-string printed)))
                 (write-string text)
                 (format *error-output* "~&PRINTED-CHARS ~d~%" (length text))
                 (when (and (plusp (length text)) (char/= (char text (1- (length text))) #\Newline))
                   (terpri)))))
        (handler-case
            (let ((value nil) (last (car (last forms))))
              (let ((*standard-output* printed))
                (dolist (f forms) (setf value (eval f))))
              (emit-printed)
              (unless (or (null value) (and (consp last) (member (car last) *printers*)))
                (if (atom value) (princ value) (prin1 value))
                (terpri))
              (status "ok"))
          (error (e)
            (emit-printed)
            (format t "error: ~a~%" e)
            (status "error: ~a" (substitute #\Space #\Newline (princ-to-string e)))))))))

(let ((file (second sb-ext:*posix-argv*)))
  (run (with-open-file (in file :external-format :utf-8)
         (let ((s (make-string (file-length in))))
           (subseq s 0 (read-sequence s in))))))
(finish-output)
