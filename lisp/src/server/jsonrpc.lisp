;;;; JSON-RPC 2.0, one message per line both ways (docs/lisp-port-image-client.md, "Protocol"):
;;;; decoding a request line, running its method from the dispatch table, encoding the answer.

(in-package "OM.SERVER")

(defparameter +parse-error+ -32700)
(defparameter +invalid-request+ -32600)
(defparameter +method-not-found+ -32601)
(defparameter +invalid-params+ -32602)
(defparameter +internal-error+ -32603)
(defparameter +engine-error+ -32000)
;; A method the image knows but whose engine work is not ported from Go yet. The Go client falls
;; back to its own engine on it (OM_LISP_FALLBACK).
(defparameter +not-ported+ -32004)

(define-condition rpc-error (error)
  ((code :initarg :code :reader rpc-error-code)
   (message :initarg :message :reader rpc-error-message))
  (:report (lambda (c s) (format s "~A (~D)" (rpc-error-message c) (rpc-error-code c)))))

(defun rpc-fail (code fmt &rest args)
  (error 'rpc-error :code code :message (apply #'format nil fmt args)))

(defun not-ported (what)
  "Answers the request with the not-ported error. Call it before any side effect, so a client
that falls back to the Go engine finds nothing half done."
  (rpc-fail +not-ported+ "om engine: ~A is not ported to the Lisp engine yet" what))

(defvar *methods* (make-hash-table :test 'equal)
  "Method name -> function of the params object (an equal hash table) returning the result.")

(defmacro define-method (name (params) &body body)
  "Registers a JSON-RPC method. Every method of the image is registered with this, so the
dispatch table is one place (methods.lisp holds the protocol's; other work packages add theirs
in their own files, or redefine a not-ported one)."
  `(progn (setf (gethash ,name *methods*) (lambda (,params) (declare (ignorable ,params)) ,@body))
          ,name))

(defun define-not-ported (&rest names)
  (dolist (name names)
    (let ((name name))
      (setf (gethash name *methods*) (lambda (params) (declare (ignore params)) (not-ported name))))))

(defun response (id result)
  (om::jobject "jsonrpc" "2.0" "id" id "result" (if (null result) (om::jobject) result)))

(defun error-response (id code message)
  (om::jobject "jsonrpc" "2.0" "id" id "error" (om::jobject "code" code "message" message)))

(defun encode-message (message)
  "The message as one line of JSON, without the newline: strings escape their newlines."
  (om::json-string message))

(defun handle-line (line)
  "The answer to one received line, as a decoded object, or nil for a notification (a request
without an id), which is run and not answered."
  (let ((request (handler-case (om::json-parse (coerce line 'simple-string))
                   (error () (return-from handle-line
                               (error-response :null +parse-error+ "parse error"))))))
    (multiple-value-bind (id has-id) (if (hash-table-p request) (gethash "id" request) (values nil nil))
      (let ((id (if has-id id :null))
            (method (om::jget request "method"))
            (params (om::jget request "params")))
        (cond ((or (not (hash-table-p request)) (not (equal (om::jget request "jsonrpc") "2.0"))
                   (not (stringp method)))
               (error-response id +invalid-request+ "invalid request"))
              ((not (or (null params) (eq params :null) (hash-table-p params)))
               (and has-id (error-response id +invalid-params+ "params must be an object")))
              (t
               (let ((handler (gethash method *methods*))
                     (params (if (hash-table-p params) params (om::jobject))))
                 (if (null handler)
                     (and has-id (error-response id +method-not-found+ (format nil "method not found: ~A" method)))
                     (handler-case (let ((result (funcall handler params)))
                                     (and has-id (response id result)))
                       (rpc-error (e)
                         (and has-id (error-response id (rpc-error-code e) (rpc-error-message e))))
                       (error (e)
                         (and has-id (error-response id +internal-error+
                                                     (format nil "internal error: ~A" e)))))))))))))
