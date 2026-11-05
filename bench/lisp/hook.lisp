;;;; hook.lisp: a hook-shaped SBCL program for docs/lisp-measurements.md. It does what a per-turn
;;;; `om hook` process must at least do: read a JSONL transcript, parse every line as JSON (with
;;;; the small parser below, no library), then append one line to a file under an exclusive
;;;; flock(2) on <file>.lock, as memory/filelock does, and exit.
;;;;
;;;;   hook TRANSCRIPT OUT     prints "<lines> lines <values> values" and appends a JSON line to OUT
;;;;   hook --keep TRANSCRIPT OUT  the same, holding every parsed line until exit (as an engine
;;;;                           holding the branch does), and prints the heap in use after a GC
;;;;   hook --heap             prints the dynamic-space size in MB and exits

(defpackage :hook (:use :cl) (:export #:main #:parse-json))
(in-package :hook)

(declaim (optimize (speed 3) (safety 1) (debug 0)))

(defvar *values* 0)
(declaim (type fixnum *values*))

(define-condition json-error (error) ((pos :initarg :pos))
  (:report (lambda (c s) (format s "bad JSON at ~d" (slot-value c 'pos)))))

(declaim (inline ws))
(defun ws (s i)
  (declare (simple-string s) (fixnum i))
  (loop while (and (< i (length s)) (member (schar s i) '(#\Space #\Tab #\Newline #\Return)))
        do (incf i))
  i)

(defun parse-string (s i)
  "S at I is just past the opening quote; returns the string and the index past the closing one."
  (declare (simple-string s) (fixnum i))
  (let ((end (position #\" s :start i)))
    ;; Fast path: no escape before the closing quote.
    (when (and end (not (find #\\ s :start i :end end)))
      (return-from parse-string (values (subseq s i end) (1+ end)))))
  (let ((out (make-string-output-stream)))
    (loop
      (when (>= i (length s)) (error 'json-error :pos i))
      (let ((c (schar s i)))
        (cond ((char= c #\") (return (values (get-output-stream-string out) (1+ i))))
              ((char= c #\\)
               (let ((e (schar s (1+ i))))
                 (incf i 2)
                 (case e
                   (#\n (write-char #\Newline out))
                   (#\t (write-char #\Tab out))
                   (#\r (write-char #\Return out))
                   (#\b (write-char #\Backspace out))
                   (#\f (write-char #\Page out))
                   (#\u (let ((code (parse-integer s :start i :end (+ i 4) :radix 16)))
                          (incf i 4)
                          (when (and (<= #xD800 code #xDBFF) (< (+ i 5) (length s))
                                     (char= (schar s i) #\\) (char= (schar s (1+ i)) #\u))
                            (let ((lo (parse-integer s :start (+ i 2) :end (+ i 6) :radix 16)))
                              (incf i 6)
                              (setf code (+ #x10000 (ash (- code #xD800) 10) (- lo #xDC00)))))
                          (write-char (code-char code) out)))
                   (t (write-char e out)))))
              (t (write-char c out) (incf i)))))))

(defun parse-number (s i)
  (declare (simple-string s) (fixnum i))
  (let ((j i) (float nil))
    (declare (fixnum j))
    (loop while (and (< j (length s)) (find (schar s j) "+-0123456789.eE"))
          do (when (find (schar s j) ".eE") (setf float t)) (incf j))
    (values (if float
                (let ((*read-default-float-format* 'double-float))
                  (let ((*read-eval* nil)) (read-from-string s t nil :start i :end j)))
                (parse-integer s :start i :end j))
            j)))

(defun parse-value (s i)
  "Returns the JSON value at I in S and the index after it: objects are equal hash tables,
arrays simple vectors, true/false/null :true/:false/:null."
  (declare (simple-string s) (fixnum i))
  (setf i (ws s i))
  (when (>= i (length s)) (error 'json-error :pos i))
  (incf *values*)
  (let ((c (schar s i)))
    (case c
      (#\{ (let ((h (make-hash-table :test #'equal)))
             (setf i (ws s (1+ i)))
             (if (char= (schar s i) #\})
                 (values h (1+ i))
                 (loop
                   (unless (char= (schar s i) #\") (error 'json-error :pos i))
                   (multiple-value-bind (k j) (parse-string s (1+ i))
                     (setf j (ws s j))
                     (unless (char= (schar s j) #\:) (error 'json-error :pos j))
                     (multiple-value-bind (v j2) (parse-value s (1+ j))
                       (setf (gethash k h) v
                             i (ws s j2))))
                   (case (schar s i)
                     (#\, (setf i (ws s (1+ i))))
                     (#\} (return (values h (1+ i))))
                     (t (error 'json-error :pos i)))))))
      (#\[ (let ((items '()))
             (setf i (ws s (1+ i)))
             (if (char= (schar s i) #\])
                 (values (vector) (1+ i))
                 (loop
                   (multiple-value-bind (v j) (parse-value s i)
                     (push v items)
                     (setf i (ws s j)))
                   (case (schar s i)
                     (#\, (incf i))
                     (#\] (return (values (coerce (nreverse items) 'simple-vector) (1+ i))))
                     (t (error 'json-error :pos i)))))))
      (#\" (parse-string s (1+ i)))
      (#\t (values :true (+ i 4)))
      (#\f (values :false (+ i 5)))
      (#\n (values :null (+ i 4)))
      (t (parse-number s i)))))

(defun parse-json (string)
  (values (parse-value (coerce string 'simple-string) 0)))

(defun flock (fd op)
  (sb-alien:alien-funcall
   (sb-alien:extern-alien "flock" (function sb-alien:int sb-alien:int sb-alien:int)) fd op))

(defconstant +lock-ex+ 2)
(defconstant +lock-un+ 8)

(defun append-locked (path line)
  "Appends LINE to PATH holding an exclusive flock on PATH.lock, as memory/filelock does."
  (let ((lock (sb-posix:open (concatenate 'string path ".lock")
                             (logior sb-posix:o-creat sb-posix:o-rdwr) #o600)))
    (unwind-protect
         (progn
           (unless (zerop (flock lock +lock-ex+)) (error "flock failed"))
           (with-open-file (out path :direction :output :if-exists :append
                                     :if-does-not-exist :create :external-format :utf-8)
             (write-line line out))
           (flock lock +lock-un+))
      (sb-posix:close lock))))

(defun run (transcript out &optional keep)
  (let ((lines 0) (kept '()))
    (declare (fixnum lines))
    (with-open-file (in transcript :external-format :utf-8)
      (loop for line = (read-line in nil) while line
            unless (zerop (length line))
              do (let ((v (parse-json line))) (when keep (push v kept))) (incf lines)))
    (append-locked out (format nil "{\"hook\":\"stop\",\"lines\":~d,\"values\":~d}" lines *values*))
    (format t "~d lines ~d values~%" lines *values*)
    (when keep
      (sb-ext:gc :full t)
      (format t "heap in use with ~d lines held: ~,1f MB~%" (length kept)
              (/ (sb-kernel:dynamic-usage) 1048576.0)))))

(defun main ()
  ;; Without this an unhandled condition enters the interactive debugger and waits on stdin,
  ;; which would hang a hook.
  (sb-ext:disable-debugger)
  (let ((args (rest sb-ext:*posix-argv*)))
    (handler-case
        (cond ((equal args '("--heap"))
               (format t "~d~%" (floor (sb-ext:dynamic-space-size) (* 1024 1024))))
              ((= (length args) 2) (run (first args) (second args)))
              ((and (= (length args) 3) (equal (first args) "--keep")) (run (second args) (third args) t))
              (t (format *error-output* "usage: hook [--keep] TRANSCRIPT OUT | hook --heap~%")
                 (sb-ext:exit :code 2)))
      (sb-sys:interactive-interrupt () (sb-ext:exit :code 130 :abort t))
      (error (e) (format *error-output* "hook: ~a~%" e) (sb-ext:exit :code 1)))
    (finish-output)))
