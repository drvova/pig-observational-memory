;;;; The rows codec read back for the store files the harness keeps (memory/tables rows_decode.go,
;;;; as far as the files Go and the image write need it): a layer file's header and a rule's table,
;;;; each one table "name[N]{f1,f2,...}:" with N rows indented two spaces, values separated by
;;;; commas, a quoted value with \" \\ \n \r \t escapes, and an empty value absent. Hand-edited
;;;; files Go's decoder would still accept (scalars, lists, extra spacing) are refused here: a
;;;; rule or header the image cannot read is skipped, as Go skips one it cannot read.

(in-package "OM.LAYER")

(define-condition rows-error (error) ((why :initarg :why :reader rows-error-why))
  (:report (lambda (c s) (format s "rows: ~A" (rows-error-why c)))))

(defun rows-fail (fmt &rest args) (error 'rows-error :why (apply #'format nil fmt args)))

(defun rows-name-char-p (c) (or (alphanumericp c) (find c "_-.")))

(defun rows-cells (line start)
  "The cells of a row line from start: a list of (value . absent-p)."
  (let ((i start) (n (length line)) (out '()))
    (loop
      (cond
        ((and (< i n) (char= (char line i) #\"))
         (let ((buf (make-string-output-stream)))
           (incf i)
           (loop
             (when (>= i n) (rows-fail "unterminated quote"))
             (let ((c (char line i)))
               (cond ((char= c #\") (incf i) (return))
                     ((char= c #\\)
                      (when (>= (1+ i) n) (rows-fail "bad escape"))
                      (let ((e (char line (1+ i))))
                        (write-char (case e (#\" #\") (#\\ #\\) (#\n #\Newline) (#\r #\Return) (#\t #\Tab)
                                      (t (rows-fail "bad escape \\~A" e)))
                                    buf))
                      (incf i 2))
                     (t (write-char c buf) (incf i)))))
           (push (cons (get-output-stream-string buf) nil) out)))
        (t
         (let* ((end (or (position #\, line :start i) n))
                (v (string-trim " " (subseq line i end))))
           (push (if (string= v "") (cons "" t) (cons v nil)) out)
           (setf i end))))
      (cond ((>= i n) (return))
            ((char= (char line i) #\,)
             (incf i)
             (when (>= i n) (push (cons "" t) out) (return)))
            (t (rows-fail "expected a comma"))))
    (nreverse out)))

(defun rows-decode-table (text)
  "Decodes text, one rows table: (values name fields rows absent), rows a list of lists of
strings and absent a parallel list of lists of booleans. Signals rows-error."
  (let* ((lines (remove "" (mapcar (lambda (l) (string-right-trim '(#\Return) l))
                                   (om::split text (string #\Newline)))
                        :test #'string=))
         (head (or (first lines) (rows-fail "empty")))
         (p (or (position-if-not #'rows-name-char-p head) (rows-fail "no header")))
         (name (subseq head 0 p)))
    (unless (and (plusp p) (char= (char head p) #\[)) (rows-fail "expected name[N]{fields}:"))
    (let* ((close (or (position #\] head :start p) (rows-fail "no ]")))
           (count (handler-case (parse-integer head :start (1+ p) :end close) (error () (rows-fail "bad count"))))
           (open (1+ close)))
      (unless (and (< open (length head)) (char= (char head open) #\{)) (rows-fail "expected {fields}"))
      (let* ((brace (or (position #\} head :start open) (rows-fail "no }")))
             (fields (om::split (subseq head (1+ open) brace) ",")))
        (unless (string= (subseq head (1+ brace)) ":") (rows-fail "expected : after the fields"))
        (unless (= (length (rest lines)) count) (rows-fail "~D rows for a count of ~D" (length (rest lines)) count))
        (let ((rows '()) (absent '()))
          (dolist (line (rest lines))
            (unless (and (> (length line) 2) (string= (subseq line 0 2) "  ") (char/= (char line 2) #\Space))
              (rows-fail "a row is indented two spaces"))
            (let ((cells (rows-cells line 2)))
              (unless (<= (length cells) (length fields)) (rows-fail "more values than fields"))
              (push (append (mapcar #'car cells) (make-list (- (length fields) (length cells)) :initial-element ""))
                    rows)
              (push (append (mapcar #'cdr cells) (make-list (- (length fields) (length cells)) :initial-element t))
                    absent)))
          (values name fields (nreverse rows) (nreverse absent)))))))

(defun rows-encode-row (name fields row)
  "tables.Rows.Encode of one table of one row, an empty value absent."
  (om::encode-table (om::make-rtable :name name :fields fields :rows (list row)
                                     :absent (list (mapcar (lambda (v) (string= v "")) row)))))

(defun rows-cell (fields row field)
  "The row's value of field, \"\" when the table has no such field."
  (let ((i (position field fields :test #'string=))) (if i (nth i row) "")))
