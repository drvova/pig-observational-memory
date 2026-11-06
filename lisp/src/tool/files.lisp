;;;; What the curated store and the layer share: Go's time.Time as RFC 3339 reads and writes it
;;;; (nanosecond precision, the zone offset kept), the clock tests pin, atomic writes, directory
;;;; listings, and the store's exclusive lock, an flock(2) on the same file Go's lock takes, so
;;;; the Go engine and the image serialize their writes to one store.

(in-package "OM.LAYER")

;;; Time: a time is (nanos . offset-seconds), nanos since the Unix epoch; nil is Go's zero time.

(defvar *now* nil
  "The clock: nil reads the system clock; tests bind a function returning a time.")

(defun now ()
  "time.Now().UTC()."
  (if *now*
      (funcall *now*)
      (multiple-value-bind (sec usec) (sb-ext:get-time-of-day)
        (cons (+ (* sec 1000000000) (* usec 1000)) 0))))

(defun time-nanos (time) (if time (car time) (zero-nanos)))

(defun zero-nanos ()
  "The nanos of Go's zero time, 0001-01-01T00:00:00Z."
  (* (om::days-from-civil 1 1 1) 86400 1000000000))

(defun zero-time-p (time) (or (null time) (= (car time) (zero-nanos))))

(defun time-after-p (a b) (> (time-nanos a) (time-nanos b)))

(defun time-before-p (a b) (< (time-nanos a) (time-nanos b)))

(defun digits-at (s start count)
  (and (<= (+ start count) (length s))
       (loop for i from start below (+ start count) always (char<= #\0 (char s i) #\9))
       (parse-integer s :start start :end (+ start count))))

(defun parse-rfc3339 (s)
  "time.Parse(time.RFC3339Nano, s) (which time.RFC3339 parses alike): the time, or nil."
  (let ((year (digits-at s 0 4)) (month (digits-at s 5 2)) (day (digits-at s 8 2))
        (hour (digits-at s 11 2)) (minute (digits-at s 14 2)) (sec (digits-at s 17 2))
        (n (length s)))
    (unless (and year month day hour minute sec (> n 19)
                 (char= (char s 4) #\-) (char= (char s 7) #\-) (char= (char s 10) #\T)
                 (char= (char s 13) #\:) (char= (char s 16) #\:)
                 (<= 1 month 12) (<= 1 day (om::days-in-month year month))
                 (<= hour 23) (<= minute 59) (<= sec 59))
      (return-from parse-rfc3339 nil))
    (let ((pos 19) (nanos 0))
      (when (and (< pos n) (char= (char s pos) #\.))
        (let ((start (incf pos)))
          (loop while (and (< pos n) (char<= #\0 (char s pos) #\9)) do (incf pos))
          (when (or (= pos start) (> (- pos start) 9)) (return-from parse-rfc3339 nil))
          (setf nanos (* (parse-integer s :start start :end pos) (expt 10 (- 9 (- pos start)))))))
      (let ((offset
              (cond ((and (= pos (1- n)) (char= (char s pos) #\Z)) 0)
                    ((and (= (- n pos) 6) (find (char s pos) "+-") (char= (char s (+ pos 3)) #\:))
                     (let ((h (digits-at s (+ pos 1) 2)) (m (digits-at s (+ pos 4) 2)))
                       (unless (and h m (< h 24) (< m 60)) (return-from parse-rfc3339 nil))
                       (* (if (char= (char s pos) #\-) -1 1) (+ (* h 3600) (* m 60)))))
                    (t (return-from parse-rfc3339 nil)))))
        (cons (+ (* (- (+ (* 86400 (om::days-from-civil year month day)) (* 3600 hour) (* 60 minute) sec)
                       offset)
                    1000000000)
                 nanos)
              offset)))))

(defun time-fields (time)
  "The local (year month day hour minute second nanos) of time in its own offset."
  (let ((local (+ (time-nanos time) (* (if time (cdr time) 0) 1000000000))))
    (multiple-value-bind (secs nanos) (floor local 1000000000)
      (multiple-value-bind (days rest) (floor secs 86400)
        (destructuring-bind (y m d) (om::civil-from-days days)
          (list y m d (floor rest 3600) (mod (floor rest 60) 60) (mod rest 60) nanos))))))

(defun format-offset (offset)
  (if (zerop offset)
      "Z"
      (multiple-value-bind (h m) (floor (floor (abs offset) 60) 60)
        (format nil "~A~2,'0D:~2,'0D" (if (minusp offset) "-" "+") h m))))

(defun format-rfc3339 (time)
  "time.Format(time.RFC3339)."
  (destructuring-bind (y m d hh mm ss nanos) (time-fields time)
    (declare (ignore nanos))
    (format nil "~4,'0D-~2,'0D-~2,'0DT~2,'0D:~2,'0D:~2,'0D~A" y m d hh mm ss (format-offset (if time (cdr time) 0)))))

(defun format-rfc3339-nano (time)
  "time.Format(time.RFC3339Nano): the fraction without trailing zeros."
  (destructuring-bind (y m d hh mm ss nanos) (time-fields time)
    (let ((fraction (if (zerop nanos) "" (string-right-trim "0" (format nil ".~9,'0D" nanos)))))
      (format nil "~4,'0D-~2,'0D-~2,'0DT~2,'0D:~2,'0D:~2,'0D~A~A" y m d hh mm ss fraction
              (format-offset (if time (cdr time) 0))))))

(defun format-date (time)
  "time.Format(\"2006-01-02\")."
  (destructuring-bind (y m d &rest rest) (time-fields time)
    (declare (ignore rest))
    (format nil "~4,'0D-~2,'0D-~2,'0D" y m d)))

;;; Files

(defun join-path (dir &rest parts)
  "filepath.Join for a directory and plain names: a string path."
  (let ((out (string-right-trim "/" dir)))
    (dolist (p parts out)
      (setf out (concatenate 'string out "/" p)))))

(defun read-file (path)
  "The file's text, or nil when it does not exist."
  (with-open-file (in path :external-format :utf-8 :if-does-not-exist nil)
    (when in
      (let* ((s (make-string (file-length in))) (n (read-sequence s in)))
        (subseq s 0 n)))))

(defun ensure-dir (dir)
  (ensure-directories-exist (concatenate 'string (string-right-trim "/" dir) "/") :mode #o700))

(defun write-atomic (path text)
  "Writes text to path through a temporary file renamed over it."
  (ensure-dir (directory-namestring path))
  (let ((tmp (concatenate 'string path ".tmp")))
    (with-open-file (out tmp :direction :output :if-exists :supersede :external-format :utf-8)
      (write-string text out))
    (sb-posix:chmod tmp #o600)
    (sb-posix:rename tmp path)
    path))

(defun append-line (path line)
  "Appends line and a newline to path, creating it (0600)."
  (ensure-dir (directory-namestring path))
  (with-open-file (out path :direction :output :if-exists :append :if-does-not-exist :create
                            :external-format :utf-8)
    (write-string line out)
    (write-char #\Newline out))
  (sb-posix:chmod path #o600))

(defun file-exists-p (path) (and (probe-file path) t))

(defun delete-file-if-exists (path)
  (when (probe-file path) (delete-file path)))

(defun dir-file-names (dir suffix)
  "The names (without suffix) of the regular files in dir ending in suffix, sorted; () when dir
does not exist."
  (let ((out '()))
    (dolist (p (directory (concatenate 'string (string-right-trim "/" dir) "/*.*") :resolve-symlinks nil))
      (let ((name (file-namestring p)))
        (when (and (plusp (length name)) (om::has-suffix name suffix)
                   (> (length name) (length suffix)))
          (push (subseq name 0 (- (length name) (length suffix))) out))))
    (sort out #'string<)))

;;; The store's lock: flock(LOCK_EX) on <dir>/.lock, as curated.Store.locked takes it.

(defvar *lock-mutex* (sb-thread:make-mutex :name "om.layer store lock")
  "Serializes the image's own holders of store locks (flock locks per open file, not per
thread); held recursively by one thread across two stores.")

(defvar *held-locks* '()
  "The lock files this thread holds, so a nested hold of one does not deadlock.")

(defun flock (fd operation)
  (sb-alien:alien-funcall
   (sb-alien:extern-alien "flock" (function sb-alien:int sb-alien:int sb-alien:int))
   fd operation))

(defun call-locked (dir fn)
  "Runs fn holding the store's lock: the in-image mutex, then flock(LOCK_EX) on <dir>/.lock.
Interrupts are deferred from before the lock is taken until after it is released, so neither an
asynchronous unwind nor a stop lands while it is held and fn writes; the one place they are
allowed is the pure wait for the flock (allow-with-interrupts, which an OM.TOOL call keeps off),
where a run of model code past its budget also ends cooperatively (wait-pause) before holding
the flock."
  (let ((path (join-path dir ".lock")))
    (if (member path *held-locks* :test #'string=)
        (funcall fn)
        (let ((got-mutex nil) (fd nil) (got-flock nil))
          (sb-sys:without-interrupts
            (unwind-protect
                 (progn
                   (sb-sys:allow-with-interrupts
                     (unless (sb-thread:holding-mutex-p *lock-mutex*)
                       (loop until (sb-thread:grab-mutex *lock-mutex* :timeout 0.05)
                             do (wait-check))
                       (setf got-mutex t))
                     (ensure-dir dir)
                     (setf fd (sb-posix:open path (logior sb-posix:o-creat sb-posix:o-rdwr) #o600))
                     ;; LOCK_EX|LOCK_NB, retried: the wait is where a stop may end the call.
                     (loop until (zerop (flock fd 6)) do (wait-pause 0.002))
                     (setf got-flock t))
                   (let ((*held-locks* (cons path *held-locks*)))
                     (funcall fn)))
              (when got-flock (flock fd 8))
              (when fd (sb-posix:close fd))
              (when got-mutex (sb-thread:release-mutex *lock-mutex*))))))))

(defmacro with-store-lock ((dir) &body body)
  `(call-locked ,dir (lambda () ,@body)))
