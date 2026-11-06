;;;; The time handling the engine reads and writes: Go's time.Parse for the timestamp forms
;;;; serialize.ParseTimestamp accepts, civil-date arithmetic for relative dates, and the
;;;; "2006-01-02 15:04" rendering. A time is a Unix time in milliseconds; the zone is UTC (the
;;;; parity harness pins serialize.Location to UTC, as the Go tests do).

(in-package "OM.BASE")

(defun days-from-civil (y m d)
  "Days since 1970-01-01 of the proleptic Gregorian date y-m-d (m may run past 12 or below 1)."
  (multiple-value-bind (carry m0) (floor (1- m) 12)
    (let* ((y (+ y carry)) (m (1+ m0))
           (y (if (<= m 2) (1- y) y))
           (era (floor y 400))
           (yoe (- y (* era 400)))
           (doy (+ (floor (+ (* 153 (+ m (if (> m 2) -3 9))) 2) 5) (1- d)))
           (doe (+ (* yoe 365) (floor yoe 4) (- (floor yoe 100)) doy)))
      (+ (* era 146097) doe -719468))))

(defun civil-from-days (z)
  "The (year month day) of a day count since 1970-01-01."
  (let* ((z (+ z 719468))
         (era (floor z 146097))
         (doe (- z (* era 146097)))
         (yoe (floor (- doe (floor doe 1460) (- (floor doe 36524)) (floor doe 146096)) 365))
         (y (+ yoe (* era 400)))
         (doy (- doe (- (+ (* 365 yoe) (floor yoe 4)) (floor yoe 100))))
         (mp (floor (+ (* 5 doy) 2) 153))
         (d (1+ (- doy (floor (+ (* 153 mp) 2) 5))))
         (m (if (< mp 10) (+ mp 3) (- mp 9))))
    (list (if (<= m 2) (1+ y) y) m d)))

(defun days-in-month (y m)
  (- (days-from-civil y (1+ m) 1) (days-from-civil y m 1)))

(defun weekday-of-days (days)
  "time.Weekday of a day count: Sunday is 0."
  (mod (+ days 4) 7))

(defun format-day (days)
  (destructuring-bind (y m d) (civil-from-days days)
    (format nil "~4,'0D-~2,'0D-~2,'0D" y m d)))

(defun format-millis (ms)
  "fmtLocal: \"2006-01-02 15:04\" in UTC."
  (multiple-value-bind (days rest) (floor ms 86400000)
    (let ((minutes (floor rest 60000)))
      (format nil "~A ~2,'0D:~2,'0D" (format-day days) (floor minutes 60) (mod minutes 60)))))

(defun millis-day (ms) (floor ms 86400000))

;;; Parsing

(defun parse-digits (s start count)
  (and (<= (+ start count) (length s))
       (loop for i from start below (+ start count) always (char<= #\0 (char s i) #\9))
       (parse-integer s :start start :end (+ start count))))

(defun parse-time-string (s &key rfc3339-only)
  "The Unix milliseconds of s in one of the forms serialize.ParseTimestamp reads (time.Parse
of RFC 3339 and its variants, the ISO date-only forms, and the forms without an offset, which
read as UTC here), or nil. With rfc3339-only, only time.RFC3339Nano."
  (let ((n (length s)) (pos 0))
    (labels ((num (count)
               (let ((v (parse-digits s pos count)))
                 (when v (incf pos count))
                 v))
             (at (c) (and (< pos n) (char= (char s pos) c)))
             (fail () (return-from parse-time-string nil)))
      (let ((year (num 4)) (month 1) (day 1) (hour 0) (minute 0) (sec 0) (nanos 0)
            (offset nil) (sep nil) (has-seconds nil))
        (unless year (fail))
        (when (= pos n)
          (when rfc3339-only (fail))
          (return-from parse-time-string (* 86400000 (days-from-civil year 1 1))))
        (unless (at #\-) (fail))
        (incf pos)
        (setf month (or (num 2) (fail)))
        (unless (<= 1 month 12) (fail))
        (when (= pos n)
          (when rfc3339-only (fail))
          (return-from parse-time-string (* 86400000 (days-from-civil year month 1))))
        (unless (at #\-) (fail))
        (incf pos)
        (setf day (or (num 2) (fail)))
        (unless (<= 1 day (days-in-month year month)) (fail))
        (when (= pos n)
          (when rfc3339-only (fail))
          (return-from parse-time-string (* 86400000 (days-from-civil year month day))))
        (cond ((at #\T) (setf sep #\T))
              ((and (at #\Space) (not rfc3339-only)) (setf sep #\Space))
              (t (fail)))
        (incf pos)
        (setf hour (or (num 2) (fail)))
        (unless (at #\:) (fail))
        (incf pos)
        (setf minute (or (num 2) (fail)))
        (when (at #\:)
          (incf pos)
          (setf sec (or (num 2) (fail)) has-seconds t)
          ;; A fractional second may follow the seconds whatever the layout says.
          (when (and (< pos n) (find (char s pos) ".,") (< (1+ pos) n) (char<= #\0 (char s (1+ pos)) #\9))
            (incf pos)
            (let ((start pos))
              (loop while (and (< pos n) (char<= #\0 (char s pos) #\9)) do (incf pos))
              (let* ((digits (subseq s start pos))
                     (nine (subseq (cat digits "000000000") 0 9)))
                (setf nanos (parse-integer nine))))))
        (unless (and (<= hour 23) (<= minute 59) (<= sec 59)) (fail))
        (cond ((= pos n) (when rfc3339-only (fail)))
              ((at #\Z) (incf pos) (setf offset 0))
              ((or (at #\+) (at #\-))
               (let ((sign (if (at #\-) -1 1)))
                 (incf pos)
                 (let ((hh (or (num 2) (fail))) (mm nil))
                   (cond ((at #\:) (incf pos) (setf mm (or (num 2) (fail))))
                         (rfc3339-only (fail))
                         (t (setf mm (or (num 2) (fail)))))
                   (unless (and (< hh 24) (< mm 60)) (fail))
                   (setf offset (* sign (+ (* hh 60) mm))))))
              (t (fail)))
        (unless (= pos n) (fail))
        ;; The forms with an offset have seconds and a T; the ones without one may stop at minutes.
        (when (and offset (or (not has-seconds) (char/= sep #\T))) (fail))
        (when (and rfc3339-only (not has-seconds)) (fail))
        (let ((seconds (+ (* 86400 (days-from-civil year month day)) (* 3600 hour) (* 60 minute) sec
                          (- (* 60 (or offset 0))))))
          (+ (* 1000 seconds) (floor nanos 1000000)))))))

(defun parse-timestamp (v)
  "serialize.ParseTimestamp: epoch milliseconds (a JSON number) or a date string; nil when
unreadable."
  (cond ((null v) nil)
        ((floatp v) (truncate v))
        ((integerp v) v)
        ((stringp v) (let ((s (trim-space v))) (and (plusp (length s)) (parse-time-string s))))
        (t nil)))

(defun format-timestamp (v)
  "serialize.FormatTimestamp."
  (let ((ms (parse-timestamp v)))
    (if ms (format-millis ms) "????-??-?? ??:??")))
