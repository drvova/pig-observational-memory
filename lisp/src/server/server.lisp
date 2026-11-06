;;;; The image's lifecycle: one image per store root, serving every harness and session over a
;;;; unix socket (<root>/engine.sock, or $OM_ENGINE_SOCKET). It takes a file lock beside the
;;;; socket so only one image runs, writes a pid file there, serves each connection with a peer
;;;; of its own (peer.lisp: requests both ways, one message per line), and exits after a
;;;; period with no client (the idle setting). A crash loses only in-memory caches: the next om
;;;; client finds the socket refusing and starts a new image, which removes the stale socket.

(in-package "OM.SERVER")

(define-condition image-locked (error)
  ((path :initarg :path :reader image-locked-path))
  (:report (lambda (c s) (format s "om engine: another image holds ~A" (image-locked-path c)))))

(defstruct (server (:constructor %make-server))
  (root "" :type string)
  (socket-path "" :type string)
  (lock-path "" :type string)
  (pid-path "" :type string)
  listener
  lock-fd
  (idle-seconds 0)
  (stopping nil)
  (connections '())
  (mutex (sb-thread:make-mutex :name "om server"))
  (last-activity 0)
  thread
  (done nil))

(defvar *server* nil "The running server, nil when none.")

(defvar *server-info* (om::jobject)
  "Facts initialize and status report, set by start-server.")

(defun now-seconds () (/ (get-internal-real-time) internal-time-units-per-second))

(defvar *quiet* nil "True turns the log lines off (the tests).")

(defun log-line (fmt &rest args)
  (unless *quiet*
    (format *error-output* "~&om engine ~A: ~?~%" (utc-stamp) fmt args)
    (finish-output *error-output*)))

(defun default-root ()
  (let ((env (sb-posix:getenv "OM_HOME")))
    (if (and env (string/= env ""))
        env
        (format nil "~A/.om" (string-right-trim "/" (or (sb-posix:getenv "HOME") (namestring (user-homedir-pathname))))))))

(defun default-socket (root)
  (let ((env (sb-posix:getenv "OM_ENGINE_SOCKET")))
    (if (and env (string/= env "")) env (format nil "~A/engine.sock" (string-right-trim "/" root)))))

(defun default-idle-seconds ()
  (let ((env (sb-posix:getenv "OM_ENGINE_IDLE_SECONDS")))
    (or (and env (ignore-errors (parse-integer env))) 900)))

(defun sibling-path (socket type)
  "The socket's path with its extension replaced by type: engine.sock -> engine.lock."
  (namestring (make-pathname :type type :defaults (pathname socket))))

(defun connection-count ()
  (let ((s *server*))
    (if s (sb-thread:with-mutex ((server-mutex s)) (length (server-connections s))) 0)))

(defun touch (s)
  (sb-thread:with-mutex ((server-mutex s)) (setf (server-last-activity s) (now-seconds))))

(defun request-stop ()
  "Makes the server stop within a quarter second: the accept loop sees the flag and cleans up."
  (let ((s *server*)) (when s (setf (server-stopping s) t))))

(defun unlink-quietly (path)
  (handler-case (sb-posix:unlink path) (error () nil)))

(defun write-pid-file (path)
  (let ((tmp (concatenate 'string path ".tmp")))
    (with-open-file (out tmp :direction :output :if-exists :supersede)
      (format out "~D~%" (sb-posix:getpid)))
    (sb-posix:chmod tmp #o600)
    (rename-file tmp path)))

(defun serve-connection (s socket)
  "Serves one connection: this thread is the peer's reader (peer.lisp), which hands each
request to a worker and each reply to the call waiting for it."
  (let* ((stream (sb-bsd-sockets:socket-make-stream socket :input t :output t :element-type 'character
                                                           :external-format :utf-8 :buffering :full))
         (p (%make-peer stream)))
    (unwind-protect
         (peer-run p :on-line (lambda () (touch s)) :stop-p (lambda () (server-stopping s)))
      (ignore-errors (close stream :abort t))
      (ignore-errors (sb-bsd-sockets:socket-close socket))
      (sb-thread:with-mutex ((server-mutex s))
        (setf (server-connections s) (remove socket (server-connections s))
              (server-last-activity s) (now-seconds))))))

(defun idle-p (s)
  (and (plusp (server-idle-seconds s))
       (sb-thread:with-mutex ((server-mutex s))
         (and (null (server-connections s))
              (>= (- (now-seconds) (server-last-activity s)) (server-idle-seconds s))))))

(defun cleanup (s)
  (ignore-errors (sb-bsd-sockets:socket-close (server-listener s)))
  (dolist (c (sb-thread:with-mutex ((server-mutex s)) (server-connections s)))
    (ignore-errors (sb-bsd-sockets:socket-close c)))
  (unlink-quietly (server-socket-path s))
  (unlink-quietly (server-pid-path s))
  (ignore-errors (unlock-file (server-lock-fd s)))
  (when (eq *server* s) (setf *server* nil))
  (setf (server-done s) t)
  (log-line "stopped"))

(defun accept-loop (s)
  (let* ((listener (server-listener s))
         (fd (sb-bsd-sockets:socket-file-descriptor listener)))
    (unwind-protect
         (loop until (server-stopping s)
               do (when (sb-sys:wait-until-fd-usable fd :input 0.25)
                    (let ((client (handler-case (sb-bsd-sockets:socket-accept listener) (error () nil))))
                      (when client
                        (setf (sb-bsd-sockets:non-blocking-mode client) nil)
                        (sb-thread:with-mutex ((server-mutex s))
                          (push client (server-connections s))
                          (setf (server-last-activity s) (now-seconds)))
                        (sb-thread:make-thread (lambda () (serve-connection s client)) :name "om connection"))))
                  (when (idle-p s)
                    (log-line "no client for ~D s: exiting" (server-idle-seconds s))
                    (setf (server-stopping s) t)))
      (cleanup s))))

(defun start-server (&key (root (default-root)) socket (idle-seconds (default-idle-seconds)) (thread t))
  "Starts the image's server: takes the lock beside the socket (image-locked when another image
holds it), replaces a stale socket, listens, writes the pid file. With thread, the accept loop
runs in a thread and the server is returned; else it runs here until the server stops."
  (when *server* (error "om engine: a server is already running in this image"))
  (let* ((root (string-right-trim "/" root))
         (socket (or socket (default-socket root)))
         (lock-path (sibling-path socket "lock"))
         (pid-path (sibling-path socket "pid")))
    (ensure-parent (concatenate 'string root "/"))
    (ensure-parent socket)
    (let ((fd (lock-file lock-path :wait nil)))
      (unless fd (error 'image-locked :path lock-path))
      (handler-bind ((error (lambda (e) (declare (ignore e)) (unlock-file fd))))
        ;; We hold the lock, so a socket file left here is a crashed image's.
        (unlink-quietly socket)
        (let ((listener (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
          (sb-bsd-sockets:socket-bind listener socket)
          (sb-posix:chmod socket #o600)
          (sb-bsd-sockets:socket-listen listener 64)
          (setf (sb-bsd-sockets:non-blocking-mode listener) t)
          (write-pid-file pid-path)
          (let ((s (%make-server :root root :socket-path socket :lock-path lock-path :pid-path pid-path
                                 :listener listener :lock-fd fd :idle-seconds idle-seconds
                                 :last-activity (now-seconds))))
            (setf *root* root *server* s
                  *server-info* (om::jobject "pid" (sb-posix:getpid) "root" root "socket" socket
                                             "idleSeconds" idle-seconds))
            (log-line "listening on ~A (root ~A, pid ~D, idle ~D s)" socket root (sb-posix:getpid) idle-seconds)
            (if thread
                (progn (setf (server-thread s) (sb-thread:make-thread (lambda () (accept-loop s)) :name "om accept"))
                       s)
                (progn (accept-loop s) s))))))))

(defun stop-server (&optional (s *server*))
  "Stops s and waits for its accept loop to clean up."
  (when s
    (setf (server-stopping s) t)
    (wait-server s)))

(defun wait-server (s)
  (if (server-thread s)
      (sb-thread:join-thread (server-thread s) :default nil)
      (loop until (server-done s) do (sleep 0.05))))

(defun parse-args (args)
  "--root, --socket, --idle-seconds (each also as name=value)."
  (let ((out '()))
    (loop while args
          do (let* ((a (pop args)) (eq (position #\= a))
                    (name (if eq (subseq a 0 eq) a))
                    (value (if eq (subseq a (1+ eq)) (pop args))))
               (cond ((string= name "--root") (setf (getf out :root) value))
                     ((string= name "--socket") (setf (getf out :socket) value))
                     ((string= name "--idle-seconds") (setf (getf out :idle-seconds) (parse-integer value)))
                     (t (error "om engine: unknown argument ~A (--root, --socket, --idle-seconds)" a)))))
    out))

(defun main ()
  "The dumped image's toplevel: om-engine [--root dir] [--socket path] [--idle-seconds n]."
  (sb-ext:disable-debugger)
  (let ((code 0))
    (handler-case
        (let ((options (parse-args (rest sb-ext:*posix-argv*))))
          (start-server :root (or (getf options :root) (default-root))
                        :socket (getf options :socket)
                        :idle-seconds (or (getf options :idle-seconds) (default-idle-seconds))
                        :thread nil))
      (image-locked (e) (log-line "~A" e) (setf code 3))
      (error (e) (log-line "~A" e) (setf code 1)))
    (finish-output *error-output*)
    (sb-ext:exit :code code :abort t)))
