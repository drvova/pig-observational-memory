;;;; W6: the two-way JSON-RPC peer of one connection (docs/lisp-port-peer.md), ported from
;;;; memory/enginerpc/peer.go on Swank's pattern. Each connection has one reader thread, which
;;;; parses lines and routes them and never runs a handler; one write mutex per stream; a worker
;;;; thread per incoming engine-rpc request, under an admission cap; and outgoing calls with
;;;; monotonically increasing integer ids, whose replies the reader hands to the waiting thread.
;;;;
;;;; The om client's methods (docs/protocol.md: no "/" in the name) keep their old semantics:
;;;; they run one at a time, in the order they arrived, on the connection's serial worker, so an
;;;; om client that writes two requests reads two answers in order.
;;;;
;;;; Cancellation is cooperative: $/cancelRequest sets the request's flag, handlers poll it
;;;; (check-cancelled), and a cancelled request still answers (-32800). A thread is never
;;;; interrupted or terminated. A wait for a reply loops around condition-wait with a timeout
;;;; (spurious wakeups; a timed-out wait returns without the mutex), and every pending wait fails
;;;; when the connection closes.

(in-package "OM.SERVER")

(defparameter +host-error+ -32000)
(defparameter +too-many-requests+ -32000)
(defparameter +cancel-method+ "$/cancelRequest")

(defparameter *worker-cap* 32
  "The most engine-rpc requests one connection runs at once; a request beyond it is answered
with an error at once. Each SBCL thread reserves a control stack, so the cap bounds memory.")

(defparameter *host-call-seconds* 60
  "The deadline of a short host call (host/getBranch, host/appendEntry...). The calls that
wait on the user or a model (host-call-long-p) have none: only the request's cancellation and
the connection's loss end them.")

(defvar *peer* nil "The connection of the request this thread answers.")
(defvar *request* nil "The incoming request this thread answers (an inbound), nil for none.")

(define-condition peer-closed (error) ()
  (:report "enginerpc: connection closed"))

(define-condition host-call-error (error)
  ((code :initarg :code :reader host-call-error-code)
   (message :initarg :message :reader host-call-error-message))
  (:report (lambda (c s) (format s "~A" (host-call-error-message c)))))

(defstruct (inbound (:constructor make-inbound (key method)))
  "An incoming request in flight: its id's key, its method and its cancel flag."
  key method (cancelled nil))

(defstruct (slot (:constructor make-slot ()))
  "An outgoing call waiting for its reply."
  (mutex (sb-thread:make-mutex :name "om call"))
  (queue (sb-thread:make-waitqueue :name "om call"))
  (done nil)
  result
  ;; (code . message) for an error reply or a closed connection.
  error)

(defstruct (peer (:constructor %make-peer (stream)))
  stream
  (write-mutex (sb-thread:make-mutex :name "om peer write"))
  (mutex (sb-thread:make-mutex :name "om peer"))
  (idle (sb-thread:make-waitqueue :name "om peer workers"))
  (next-id 0)
  (pending (make-hash-table))
  (inflight (make-hash-table :test 'equal))
  (closed nil)
  (workers 0)
  ;; The om methods' queue and the thread that drains it, made on first use.
  serial
  serial-thread
  ;; The connection's state (conn), made at initialize.
  conn)

(defun id-key (id)
  "A request id as a key: Go compares the raw id text, so 3 and \"3\" are two ids."
  (cond ((and (realp id) (= id (round id))) (format nil "n~D" (round id)))
        ((stringp id) (concatenate 'string "s" id))
        (t (format nil "o~A" (om::json-string id)))))

(defun om-method-p (method)
  "A method of the om client's protocol (docs/protocol.md), run in arrival order."
  (not (find #\/ method)))

;;; Writing

(defun peer-send (p message)
  "Writes message as one line under the write mutex: t, or nil when the stream is gone."
  (let ((line (encode-message message)))
    (sb-thread:with-mutex ((peer-write-mutex p))
      (handler-case
          (let ((s (peer-stream p)))
            (write-string line s)
            (write-char #\Newline s)
            (finish-output s)
            t)
        (error () nil)))))

;;; Incoming requests

(defun check-cancelled ()
  "Signals request-cancelled when the request this thread answers was cancelled ($/cancelRequest,
or the connection closed). Handlers call it between steps."
  (when (and *request* (inbound-cancelled *request*))
    (error 'request-cancelled)))

(defun cancelled-p () (and *request* (inbound-cancelled *request*) t))

(defun worker-done (p key)
  (sb-thread:with-mutex ((peer-mutex p))
    (when key (remhash key (peer-inflight p)))
    (decf (peer-workers p))
    (sb-thread:condition-broadcast (peer-idle p))))

(defun answer (p message request)
  "Runs one request and writes its answer (none for a notification)."
  (let ((*peer* p) (*request* request))
    (let ((reply (if (and request (inbound-cancelled request))
                     (error-response (gethash "id" message) +request-cancelled+ "om engine: the request was cancelled")
                     (handle-message message))))
      (when reply (peer-send p reply)))))

(defun spawn-worker (p message key method)
  "Runs message in a thread of its own under the cap; answers at once when the cap is reached."
  (let ((request (and key (make-inbound key method))) (admitted nil))
    (sb-thread:with-mutex ((peer-mutex p))
      (when (< (peer-workers p) *worker-cap*)
        (incf (peer-workers p))
        (when key (setf (gethash key (peer-inflight p)) request))
        (setf admitted t)))
    (if (not admitted)
        (when key
          (peer-send p (error-response (gethash "id" message) +too-many-requests+
                                       (format nil "om engine: more than ~D requests in flight" *worker-cap*))))
        (sb-thread:make-thread
         (lambda ()
           (unwind-protect (handler-case (answer p message request)
                             (error (e) (log-line "request ~A: ~A" method e)))
             (worker-done p key)))
         :name (format nil "om request ~A" method)))))

(defun serial-loop (p)
  (loop for item = (sb-concurrency:receive-message (peer-serial p))
        until (eq item :stop)
        do (destructuring-bind (message . request) item
             (unwind-protect (if (eq request :reply)
                                 (peer-send p message)
                                 (handler-case (answer p message request)
                                   (error (e) (log-line "request: ~A" e))))
               (worker-done p (and (inbound-p request) (inbound-key request)))))))

(defun enqueue-serial (p message key method &key reply)
  "Queues an om method for the connection's serial worker, in arrival order; with reply,
message is an answer the reader made (a parse error), written in its turn."
  (let ((request (cond (reply :reply) (key (make-inbound key method)))))
    (sb-thread:with-mutex ((peer-mutex p))
      (incf (peer-workers p))
      (when (inbound-p request) (setf (gethash key (peer-inflight p)) request))
      (unless (peer-serial p)
        (setf (peer-serial p) (sb-concurrency:make-mailbox :name "om serial")
              (peer-serial-thread p) (sb-thread:make-thread (lambda () (serial-loop p)) :name "om serial"))))
    (sb-concurrency:send-message (peer-serial p) (cons message request))))

(defun reader-reply (p message)
  "An answer the reader makes itself (a line that is no request): written in the om methods'
order, after the answers to the lines before it."
  (enqueue-serial p message nil nil :reply t))

(defun cancel-inbound (p id)
  (let ((request (sb-thread:with-mutex ((peer-mutex p)) (gethash (id-key id) (peer-inflight p)))))
    (when request (setf (inbound-cancelled request) t))))

;;; Replies to our calls

(defun deliver (p id result error)
  (let ((slot (and (realp id) (= id (round id))
                   (sb-thread:with-mutex ((peer-mutex p))
                     (let ((s (gethash (round id) (peer-pending p))))
                       (remhash (round id) (peer-pending p))
                       s)))))
    (when slot
      (sb-thread:with-mutex ((slot-mutex slot))
        (setf (slot-result slot) result
              (slot-error slot) error
              (slot-done slot) t)
        (sb-thread:condition-broadcast (slot-queue slot))))))

;;; The reader

(defun dispatch (p line)
  "Routes one line: a request to a worker, a notification, or a reply to the call waiting."
  (when (string= (string-trim '(#\Space #\Tab #\Return) line) "")
    (return-from dispatch))
  (let ((message (handler-case (om::json-parse (coerce line 'simple-string))
                   (error () (reader-reply p (error-response :null +parse-error+ "parse error"))
                     (return-from dispatch)))))
    (unless (hash-table-p message)
      (reader-reply p (error-response :null +invalid-request+ "invalid request"))
      (return-from dispatch))
    (multiple-value-bind (id has-id) (gethash "id" message)
      (let ((method (gethash "method" message))
            (request-id (and has-id (not (eq id :null)))))
        (cond ((stringp method)
               (cond ((and (not request-id) (string= method +cancel-method+))
                      (cancel-inbound p (om::jget (om::jget message "params") "id")))
                     ((om-method-p method)
                      (enqueue-serial p message (and request-id (id-key id)) method))
                     (t (spawn-worker p message (and request-id (id-key id)) method))))
              ((and request-id (or (nth-value 1 (gethash "result" message))
                                   (nth-value 1 (gethash "error" message))))
               (let ((err (gethash "error" message)))
                 (deliver p id (gethash "result" message)
                          (and (hash-table-p err)
                               (cons (let ((c (om::jget err "code"))) (if (realp c) (round c) +internal-error+))
                                     (om::str (om::jget err "message")))))))
              (t (reader-reply p (error-response (if has-id id :null) +invalid-request+ "invalid request"))))))))

(defun peer-close (p)
  "The connection has ended: every pending call fails, every request in flight is cancelled,
and the workers are waited for (they poll the flag; their host calls fail at once)."
  (let ((pending '()))
    (sb-thread:with-mutex ((peer-mutex p))
      (setf (peer-closed p) t)
      (maphash (lambda (k v) (declare (ignore k)) (push v pending)) (peer-pending p))
      (clrhash (peer-pending p))
      (maphash (lambda (k v) (declare (ignore k)) (setf (inbound-cancelled v) t)) (peer-inflight p)))
    (dolist (slot pending)
      (sb-thread:with-mutex ((slot-mutex slot))
        (setf (slot-error slot) (cons +internal-error+ "enginerpc: connection closed")
              (slot-done slot) t)
        (sb-thread:condition-broadcast (slot-queue slot))))
    (when (peer-serial p) (sb-concurrency:send-message (peer-serial p) :stop))
    (let ((m (peer-mutex p)))
      (sb-thread:grab-mutex m)
      (unwind-protect
           (loop while (plusp (peer-workers p))
                 do (unless (sb-thread:condition-wait (peer-idle p) m :timeout 0.5)
                      (unless (sb-thread:holding-mutex-p m) (sb-thread:grab-mutex m))))
        (when (sb-thread:holding-mutex-p m) (sb-thread:release-mutex m))))
    (when (peer-serial-thread p) (ignore-errors (sb-thread:join-thread (peer-serial-thread p) :default nil)))
    (when (peer-conn p) (conn-end (peer-conn p)))))

(defun peer-run (p &key (on-line (lambda ())) (stop-p (lambda () nil)))
  "The reader: reads lines until the stream ends (or stop-p), then closes the peer."
  (unwind-protect
       (handler-case
           (loop for line = (read-line (peer-stream p) nil nil)
                 while line
                 do (funcall on-line)
                    (dispatch p line)
                 until (funcall stop-p))
         (error (e) (unless (funcall stop-p) (log-line "connection: ~A" e))))
    (peer-close p)))

;;; Outgoing calls

(defun wait-reply (p slot id)
  "Waits until slot is answered. While waiting, a cancel of the request this thread answers
sends $/cancelRequest for the call once and goes on waiting: the other end answers every
request it received, and a closed connection fails the wait."
  (let ((m (slot-mutex slot)) (cancel-sent nil))
    (loop
      (sb-thread:grab-mutex m)
      (unwind-protect
           (unless (slot-done slot)
             (sb-thread:condition-wait (slot-queue slot) m :timeout 0.1))
        (when (sb-thread:holding-mutex-p m) (sb-thread:release-mutex m)))
      (when (slot-done slot) (return))
      (when (and (not cancel-sent) (cancelled-p))
        (setf cancel-sent t)
        (peer-send p (om::jobject "jsonrpc" "2.0" "method" +cancel-method+ "params" (om::jobject "id" id)))))))

(defun peer-call (p method params &key (deadline *host-call-seconds*))
  "Sends a request and returns its result. Signals peer-closed when the connection is gone,
host-call-error for an error reply, and host-call-error when deadline (seconds, nil for none)
passes first. Never call it holding *sessions-mutex*, a connection's lock or a model's lock."
  (let ((slot (make-slot)) (id 0))
    (sb-thread:with-mutex ((peer-mutex p))
      (when (peer-closed p) (error 'peer-closed))
      (setf id (incf (peer-next-id p))
            (gethash id (peer-pending p)) slot))
    (flet ((forget () (sb-thread:with-mutex ((peer-mutex p)) (remhash id (peer-pending p)))))
      (unless (peer-send p (om::jobject "jsonrpc" "2.0" "id" id "method" method
                                        "params" (or params (om::jobject))))
        (forget)
        (error 'peer-closed))
      (if deadline
          (handler-case (sb-sys:with-deadline (:seconds deadline) (wait-reply p slot id))
            (sb-sys:deadline-timeout ()
              (forget)
              (error 'host-call-error :code +host-error+
                                      :message (format nil "om engine: ~A had no answer in ~D s" method deadline))))
          (wait-reply p slot id))
      (let ((err (slot-error slot)))
        (cond ((null err) (slot-result slot))
              ((string= (cdr err) "enginerpc: connection closed") (error 'peer-closed))
              (t (error 'host-call-error :code (car err) :message (cdr err))))))))
