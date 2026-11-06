;;;; Helpers the transcript readers share (memory/transcript): Go's fmt.Sprint over decoded JSON,
;;;; the Unix-time renderings, joinLines, jsonColumn, and the assistant-group builder Codex and
;;;; Kimi use. W2 of docs/lisp-core.md; the readers themselves are in formats.lisp and sqlite.lisp.

(in-package "OM")

(defun jpresent-p (object key)
  "Whether key is a member of a decoded object (Go's `_, ok := m[key]`), whatever its value."
  (and (hash-table-p object) (nth-value 1 (gethash key object))))

(defun jnil-p (v)
  "A decoded value Go compares equal to nil: absent or JSON null."
  (or (null v) (eq v :null)))

(defun jlist (v)
  "A decoded JSON array as a list (Go's v.([]any)), nil for anything else."
  (if (and (vectorp v) (not (stringp v))) (coerce v 'list) nil))

(defun jvector (list)
  "A non-empty list as a JSON array, nil (Go's nil slice, encoded null) for an empty one."
  (and list (coerce list 'simple-vector)))

(defun copy-jobject (h)
  "A shallow copy of a decoded object."
  (let ((out (make-hash-table :test 'equal)))
    (when (hash-table-p h)
      (maphash (lambda (k v) (setf (gethash k out) v)) h))
    out))

(defun go-float-g (f)
  "strconv.FormatFloat(f, 'g', -1, 64), which fmt's %v writes for a float64."
  (let ((f (coerce f 'double-float)))
    (if (zerop f)
        (if (minusp (float-sign f)) "-0" "0")
        (multiple-value-bind (dp digits) (sb-impl::flonum-to-digits (abs f))
          (let* ((sign (if (minusp f) "-" ""))
                 (nd (length digits))
                 (exp (1- dp)))
            (if (or (< exp -4) (>= exp 6))
                (let ((es (format nil "~D" (abs exp))))
                  (cat sign (subseq digits 0 1)
                       (if (> nd 1) (cat "." (subseq digits 1)) "")
                       "e" (if (minusp exp) "-" "+")
                       (if (< (length es) 2) (cat "0" es) es)))
                (cat sign
                     (cond ((<= dp 0) (cat "0." (make-string (- dp) :initial-element #\0) digits))
                           ((>= dp nd) (cat digits (make-string (- dp nd) :initial-element #\0)))
                           (t (cat (subseq digits 0 dp) "." (subseq digits dp)))))))))))

(defun go-sprint (v)
  "fmt.Sprint of a decoded JSON value: maps with sorted keys as map[k:v ...], arrays as
[a b], nil as <nil>."
  (cond ((jnil-p v) "<nil>")
        ((stringp v) v)
        ((eq v :true) "true")
        ((eq v :false) "false")
        ((floatp v) (go-float-g v))
        ((integerp v) (format nil "~D" v))
        ((hash-table-p v)
         (let ((keys (sort (loop for k being the hash-keys of v collect k) #'string<)))
           (cat "map[" (join (mapcar (lambda (k) (cat k ":" (go-sprint (gethash k v)))) keys) " ") "]")))
        ((vectorp v) (cat "[" (join (map 'list #'go-sprint v) " ") "]"))
        (t (format nil "~A" v))))

(defun format-unix-nanos (ns fraction)
  "A Unix time in nanoseconds as UTC 2006-01-02T15:04:05Z, with .000 (milliseconds, truncated)
when fraction."
  (multiple-value-bind (secs nanos) (floor ns 1000000000)
    (multiple-value-bind (days rest) (floor secs 86400)
      (format nil "~AT~2,'0D:~2,'0D:~2,'0D~AZ" (format-day days)
              (floor rest 3600) (mod (floor rest 60) 60) (mod rest 60)
              (if fraction (format nil ".~3,'0D" (floor nanos 1000000)) "")))))

(defun unix-time (v)
  "unixTime: Unix seconds (a JSON number, fraction kept to the millisecond) as
2006-01-02T15:04:05.000Z, \"\" for anything else."
  (if (floatp v)
      (let* ((sec (truncate v))
             (frac (- v (coerce sec 'double-float)))
             (nsec (truncate (* frac 1d9))))
        (format-unix-nanos (+ (* sec 1000000000) nsec) t))
      ""))

(defun unix-millis (ms)
  "unixMillis: Unix milliseconds as unixTime writes them."
  (unix-time (/ (coerce ms 'double-float) 1000d0)))

(defun unix-seconds (v)
  "unixSeconds: whole Unix seconds as RFC 3339 in UTC, \"\" for anything but a number."
  (if (floatp v) (format-unix-nanos (* (truncate v) 1000000000) nil) ""))

(defun join-lines (parts) (join parts (string #\Newline)))

(defun json-column (v)
  "jsonColumn: a TEXT column holding JSON, decoded; nil when it does not decode; a non-string
as it is."
  (if (stringp v)
      (handler-case (let ((x (json-parse (coerce v 'simple-string)))) (if (eq x :null) nil x))
        (json-error () nil))
      v))

(defun try-json-any (s)
  "json.Unmarshal of s into any: (values value ok)."
  (handler-case (values (json-parse (coerce s 'simple-string)) t)
    (json-error () (values nil nil))))

(defun first-string (l &rest keys)
  (dolist (k keys "")
    (let ((s (str (jget l k))))
      (unless (string= s "") (return s)))))

;;; The assistant group of Codex and Kimi: consecutive assistant-side items under the first id.

(defstruct (rgroup (:constructor make-rgroup (timestamp)))
  timestamp (ids '()) (blocks '()))

(defmacro with-assistant-group ((add flush out) &body body)
  "Binds (add id ts block) and (flush) over a group pushed onto the list in place out."
  (let ((g (gensym "GROUP")))
    `(let ((,g nil))
       (flet ((,flush ()
                (when ,g
                  (let ((ids (reverse (rgroup-ids ,g))))
                    (push (make-message (first ids) (rgroup-timestamp ,g)
                                        (jobject "role" "assistant" "content" (jvector (reverse (rgroup-blocks ,g))))
                                        (rest ids))
                          ,out))
                  (setf ,g nil)))
              (,add (id ts block)
                (unless ,g (setf ,g (make-rgroup ts)))
                (push id (rgroup-ids ,g))
                (push block (rgroup-blocks ,g))))
         (declare (ignorable #',flush #',add))
         ,@body))))
