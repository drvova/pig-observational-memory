;;;; File locks: flock(2), the lock memory/filelock takes on unix, so the image and a Go om
;;;; process guarding the same file exclude each other. A flock belongs to one open of the file,
;;;; so two opens in this image exclude each other too.

(in-package "OM.SERVER")

(defconstant +lock-ex+ 2)
(defconstant +lock-nb+ 4)
(defconstant +lock-un+ 8)

(defun flock (fd operation)
  "flock(2): 0 on success, -1 on failure."
  (sb-alien:alien-funcall
   (sb-alien:extern-alien "flock" (function sb-alien:int sb-alien:int sb-alien:int))
   fd operation))

(defun ensure-parent (path)
  "Creates path's missing parent directories, each with mode 0700, as Go's MkdirAll(…, 0o700)."
  (let ((dir (directory-namestring (pathname path))))
    (unless (string= dir "")
      (let ((sofar (if (char= (char dir 0) #\/) "/" "")))
        (dolist (part (om::split (string-trim "/" dir) "/"))
          (unless (string= part "")
            (setf sofar (concatenate 'string sofar part "/"))
            (unless (probe-file sofar)
              (handler-case (sb-posix:mkdir sofar #o700)
                (sb-posix:syscall-error (e)
                  (unless (= (sb-posix:syscall-errno e) sb-posix:eexist) (error e)))))))))))

(defun lock-file (path &key (wait t))
  "Takes an exclusive flock on path (created, mode 0600): the open fd, or nil when wait is nil
and another open holds it."
  (ensure-parent path)
  (let ((fd (sb-posix:open path (logior sb-posix:o-creat sb-posix:o-rdwr) #o600)))
    (if (zerop (flock fd (if wait +lock-ex+ (logior +lock-ex+ +lock-nb+))))
        fd
        (progn (sb-posix:close fd)
               (if wait (error "flock ~A failed" path) nil)))))

(defun unlock-file (fd)
  (flock fd +lock-un+)
  (sb-posix:close fd))

(defmacro with-file-lock ((path) &body body)
  (let ((fd (gensym "FD")))
    `(let ((,fd (lock-file ,path)))
       (unwind-protect (progn ,@body) (unlock-file ,fd)))))
