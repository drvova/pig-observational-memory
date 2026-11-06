;;;; The Lisp side of the readers' parity harness (memory/transcript/lispparity_test.go):
;;;;   sbcl --script lisp/readers-parity.lisp file <format> <transcript>
;;;;   sbcl --script lisp/readers-parity.lisp db <format> <database> <session>
;;;;   sbcl --script lisp/readers-parity.lisp rows <format> <rows.json>
;;;;   sbcl --script lisp/readers-parity.lisp branch <format> <transcript> <store.jsonl>
;;;;   sbcl --script lisp/readers-parity.lisp all <transcript>
;;;;   sbcl --script lisp/readers-parity.lisp append <store.jsonl> <pairs.jsonl>
;;;; Each prints one line per entry: "entry " and the entry object as encoding/json writes it.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM")

(defun readers-parity-main (args)
  (let ((out (sb-sys:make-fd-stream 1 :output t :external-format :utf-8 :buffering :full)))
    (flet ((print-entries (entries)
             (dolist (e entries)
               (write-string "entry " out)
               (write-json (entry-to-map e) out)
               (write-char #\Newline out))))
      (destructuring-bind (mode format &rest rest) args
        (cond
          ((string= mode "file") (print-entries (transcript-file-entries format (first rest))))
          ;; all <path>: the file read in every format, in name order (format is the path here).
          ((string= mode "all")
           (let ((lines (read-transcript-lines format)))
             (dolist (name (sort (mapcar #'car *transcript-formats*) #'string<))
               (format out "format ~A~%" name)
               (print-entries (funcall (transcript-format name) lines)))))
          ;; append <store> <pairs.jsonl>: each line's after and entry appended as Store.Append does.
          ((string= mode "append")
           (dolist (line (scanner-lines (read-file-string (first rest))))
             (let ((m (json-parse (coerce line 'simple-string))))
               (store-append format (str (jget m "after")) (jget m "entry")))))
          ((string= mode "db") (print-entries (db-entries format (first rest) (second rest))))
          ((string= mode "rows")
           (let ((rows (json-parse (coerce (read-file-string (first rest)) 'simple-string))))
             (print-entries (funcall (db-rows-reader format)
                                     (map 'list (lambda (r) (and (hash-table-p r) r)) rows)))))
          ((string= mode "branch")
           (print-entries (neutral-branch (transcript-file-entries format (first rest)) (store-load (second rest)))))
          (t (error "unknown mode ~A" mode)))))
    (finish-output out)))

(handler-case (readers-parity-main (rest sb-ext:*posix-argv*))
  (error (e)
    (format *error-output* "om-engine: ~A~%" e)
    (sb-ext:exit :code 1 :abort t)))
