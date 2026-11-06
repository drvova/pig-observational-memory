;;;; Per-session state, keyed by harness and session id: the session's mutex (two events of one
;;;; session never run at once in the image), the transcript cache, and when it was last used.
;;;; Each event also takes the session's engine lock file, the one om's Go engine takes
;;;; (<store>.engine.lock), so a Go om process and the image never work on one session at once.

(in-package "OM.SERVER")

(defvar *root* nil "The store root the image serves ($OM_HOME, else ~/.om).")

(defstruct (session (:constructor %make-session))
  (harness "" :type string)
  (id "" :type string)
  (mutex (sb-thread:make-mutex :name "om session"))
  (store-path "" :type string)
  ;; The transcript's path, format and version (size and modification time) when its entries
  ;; were read, and the entries.
  transcript-key
  transcript-entries
  (last-used 0))

(defvar *sessions* (make-hash-table :test 'equal))
(defvar *sessions-mutex* (sb-thread:make-mutex :name "om sessions"))

(defun safe-name (s)
  "neutral.SafeName: a leading dot dropped, then /, \\ and NUL made _."
  (map 'string (lambda (c) (if (member c '(#\/ #\\ #\Nul)) #\_ c))
       (if (om::has-prefix s ".") (subseq s 1) s)))

(defun store-path (root harness id)
  "neutral.StoreFor: <root>/sessions/<harness>/<session>.jsonl."
  (format nil "~A/sessions/~A/~A.jsonl" (string-right-trim "/" root) harness (safe-name id)))

(defun find-session (harness id)
  (sb-thread:with-mutex (*sessions-mutex*)
    (let ((key (concatenate 'string harness (string #\Nul) id)))
      (or (gethash key *sessions*)
          (setf (gethash key *sessions*)
                (%make-session :harness harness :id id :store-path (store-path *root* harness id)))))))

(defun session-count ()
  (sb-thread:with-mutex (*sessions-mutex*) (hash-table-count *sessions*)))

(defun param-string (params key &optional (default ""))
  (let ((v (om::jget params key)))
    (if (and (stringp v) (string/= v "")) v default)))

(defun check-root (params)
  "A client names the root it reads; the image serves one root, and refuses another's request
rather than write into the wrong store."
  (let ((root (param-string params "root")))
    (when (and (string/= root "")
               (string/= (string-right-trim "/" root) (string-right-trim "/" *root*)))
      (rpc-fail +invalid-params+ "om engine: this image serves the root ~A, not ~A" *root* root))))

(defun call-with-session (params fn)
  (check-root params)
  (let ((id (param-string params "session_id")))
    (when (string= id "") (rpc-fail +invalid-params+ "om: no session id"))
    (let ((s (find-session (param-string params "harness" "generic") id)))
      (sb-thread:with-mutex ((session-mutex s))
        (setf (session-last-used s) (get-universal-time))
        (with-file-lock ((concatenate 'string (session-store-path s) ".engine.lock"))
          (funcall fn s))))))

(defmacro with-session ((var params) &body body)
  "Runs body with var the event's session, holding its mutex and its engine lock file."
  `(call-with-session ,params (lambda (,var) ,@body)))
