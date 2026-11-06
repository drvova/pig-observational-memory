;;;; JSON as Go's encoding/json decodes it into `any` and encodes it back: an object is an equal
;;;; hash table (Go maps have no order; encoding sorts the keys), an array a simple vector, a
;;;; number a double-float (Go decodes every number as float64), true and false :true and
;;;; :false, null :null. An absent key reads as nil, which the ports treat as Go treats a nil
;;;; interface, as they do :null.

(in-package "OM.BASE")

(define-condition json-error (error) ((message :initarg :message :reader json-error-message))
  (:report (lambda (c s) (format s "json: ~A" (json-error-message c)))))

(defun json-fail (fmt &rest args)
  (error 'json-error :message (apply #'format nil fmt args)))

(defun jget (object key)
  "object[key] for a decoded object, nil for anything else or an absent key."
  (and (hash-table-p object) (values (gethash key object))))

(defun jobject (&rest pairs)
  (let ((h (make-hash-table :test 'equal)))
    (loop for (k v) on pairs by #'cddr do (setf (gethash k h) v))
    h))

(defun jnull-p (v) (or (null v) (eq v :null)))

(defun jtrue-p (v) (eq v :true))

(defun jbool (b) (if b :true :false))

;;; Decoding

(defun json-parse (string)
  "Decodes one JSON value that fills string but for white space; json-error otherwise."
  (let ((pos 0) (n (length string)))
    (declare (type index pos n) (type simple-string string))
    (labels ((ws ()
               (loop while (and (< pos n) (member (char string pos) '(#\Space #\Tab #\Newline #\Return)))
                     do (incf pos)))
             (peek () (if (< pos n) (char string pos) (json-fail "unexpected end")))
             (expect (c)
               (if (and (< pos n) (char= (char string pos) c)) (incf pos) (json-fail "expected ~A at ~D" c pos)))
             (literal (word value)
               (if (and (<= (+ pos (length word)) n) (string= word string :start2 pos :end2 (+ pos (length word))))
                   (progn (incf pos (length word)) value)
                   (json-fail "bad literal at ~D" pos)))
             (value ()
               (ws)
               (let ((c (peek)))
                 (case c
                   (#\{ (object))
                   (#\[ (array))
                   (#\" (jstring))
                   (#\t (literal "true" :true))
                   (#\f (literal "false" :false))
                   (#\n (literal "null" :null))
                   (t (if (or (char= c #\-) (char<= #\0 c #\9)) (jnumber) (json-fail "bad value at ~D" pos))))))
             (object ()
               (expect #\{)
               (let ((h (make-hash-table :test 'equal)))
                 (ws)
                 (if (char= (peek) #\})
                     (incf pos)
                     (loop
                       (ws)
                       (let ((k (jstring)))
                         (ws) (expect #\:)
                         (setf (gethash k h) (value)))
                       (ws)
                       (let ((c (peek)))
                         (incf pos)
                         (cond ((char= c #\,))
                               ((char= c #\}) (return))
                               (t (json-fail "bad object at ~D" pos))))))
                 h))
             (array ()
               (expect #\[)
               (let ((items '()))
                 (ws)
                 (if (char= (peek) #\])
                     (incf pos)
                     (loop
                       (push (value) items)
                       (ws)
                       (let ((c (peek)))
                         (incf pos)
                         (cond ((char= c #\,))
                               ((char= c #\]) (return))
                               (t (json-fail "bad array at ~D" pos))))))
                 (coerce (nreverse items) 'simple-vector)))
             (hex4 ()
               (when (> (+ pos 4) n) (json-fail "short escape"))
               (let ((v (parse-integer string :start pos :end (+ pos 4) :radix 16 :junk-allowed nil)))
                 (incf pos 4)
                 v))
             (jstring ()
               (unless (char= (peek) #\") (json-fail "expected string at ~D" pos))
               (incf pos)
               (with-output-to-string (out)
                 (loop
                   (when (>= pos n) (json-fail "unterminated string"))
                   (let ((c (char string pos)))
                     (incf pos)
                     (cond ((char= c #\") (return))
                           ((char< c #\Space) (json-fail "control character in string"))
                           ((char= c #\\)
                            (when (>= pos n) (json-fail "unterminated escape"))
                            (let ((e (char string pos)))
                              (incf pos)
                              (case e
                                (#\" (write-char #\" out))
                                (#\\ (write-char #\\ out))
                                (#\/ (write-char #\/ out))
                                (#\b (write-char (code-char 8) out))
                                (#\f (write-char (code-char 12) out))
                                (#\n (write-char #\Newline out))
                                (#\r (write-char #\Return out))
                                (#\t (write-char #\Tab out))
                                (#\u
                                 (let ((u (hex4)))
                                   (cond ((<= #xd800 u #xdbff)
                                          ;; A high surrogate pairs with a following \u low one;
                                          ;; alone it is U+FFFD, as Go decodes it.
                                          (if (and (<= (+ pos 6) n) (char= (char string pos) #\\) (char= (char string (1+ pos)) #\u))
                                              (let* ((save pos)
                                                     (lo (progn (incf pos 2) (hex4))))
                                                (if (<= #xdc00 lo #xdfff)
                                                    (write-char (code-char (+ #x10000 (ash (- u #xd800) 10) (- lo #xdc00))) out)
                                                    (progn (setf pos save) (write-char (code-char #xfffd) out))))
                                              (write-char (code-char #xfffd) out)))
                                         ((<= #xdc00 u #xdfff) (write-char (code-char #xfffd) out))
                                         (t (write-char (code-char u) out)))))
                                (t (json-fail "bad escape")))))
                           (t (write-char c out)))))))
             (jnumber ()
               (let ((start pos))
                 (when (char= (peek) #\-) (incf pos))
                 (cond ((and (< pos n) (char= (char string pos) #\0)) (incf pos))
                       ((and (< pos n) (char<= #\1 (char string pos) #\9))
                        (loop while (and (< pos n) (char<= #\0 (char string pos) #\9)) do (incf pos)))
                       (t (json-fail "bad number")))
                 (let ((int-end pos) (frac-start nil) (frac-end nil) (exp 0))
                   (when (and (< pos n) (char= (char string pos) #\.))
                     (incf pos)
                     (setf frac-start pos)
                     (unless (and (< pos n) (char<= #\0 (char string pos) #\9)) (json-fail "bad fraction"))
                     (loop while (and (< pos n) (char<= #\0 (char string pos) #\9)) do (incf pos))
                     (setf frac-end pos))
                   (when (and (< pos n) (char-equal (char string pos) #\e))
                     (incf pos)
                     (let ((sign 1))
                       (when (and (< pos n) (find (char string pos) "+-"))
                         (when (char= (char string pos) #\-) (setf sign -1))
                         (incf pos))
                       (let ((es pos))
                         (loop while (and (< pos n) (char<= #\0 (char string pos) #\9)) do (incf pos))
                         (when (= es pos) (json-fail "bad exponent"))
                         (setf exp (* sign (parse-integer string :start es :end pos))))))
                   ;; The exact rational, rounded once to the nearest double, as strconv.ParseFloat.
                   (let* ((negative (char= (char string start) #\-))
                          (int (parse-integer string :start (if negative (1+ start) start) :end int-end))
                          (frac (if frac-start (parse-integer string :start frac-start :end frac-end) 0))
                          (scale (if frac-start (- frac-end frac-start) 0))
                          (r (* (+ (* int (expt 10 scale)) frac) (expt 10 (- exp scale)))))
                     (when (> (abs r) most-positive-double-float) (json-fail "number out of range"))
                     (let ((d (coerce r 'double-float)))
                       (if negative (if (zerop r) -0d0 (- d)) d)))))))
      (let ((v (value)))
        (ws)
        (unless (= pos n) (json-fail "trailing data at ~D" pos))
        v))))

(defun json-parse-object (string)
  "json.Unmarshal into a map[string]any: the object, nil for null, or json-error."
  (let ((v (json-parse (coerce string 'simple-string))))
    (cond ((hash-table-p v) v)
          ((eq v :null) nil)
          (t (json-fail "not an object")))))

;;; Encoding

(defun go-float-string (f)
  "strconv.FormatFloat as encoding/json writes a float64: 'f' between 1e-6 and 1e21, else 'e'
with the exponent's leading zero dropped."
  (let ((f (coerce f 'double-float)))
    (cond
      ((zerop f) (if (minusp (float-sign f)) "-0" "0"))
      (t
       (multiple-value-bind (k digits) (sb-impl::flonum-to-digits (abs f))
         ;; value = 0.DIGITS * 10^k
         (let* ((sign (if (minusp f) "-" ""))
                (nd (length digits))
                (abs (abs f)))
           (if (or (< abs 1d-6) (>= abs 1d21))
               (let* ((e (1- k))
                      (mant (if (= nd 1) digits (cat (subseq digits 0 1) "." (subseq digits 1))))
                      (es (format nil "~D" (abs e))))
                 ;; FormatFloat pads the exponent to two digits; encoding/json then drops the
                 ;; pad of a negative one (e-07 is written e-7).
                 (cat sign mant "e" (if (minusp e) "-" "+") (if (and (< (length es) 2) (plusp e)) (cat "0" es) es)))
               (cat sign
                    (cond ((<= k 0) (cat "0." (make-string (- k) :initial-element #\0) digits))
                          ((>= k nd) (cat digits (make-string (- k nd) :initial-element #\0)))
                          (t (cat (subseq digits 0 k) "." (subseq digits k))))))))))))

(defun write-json-string (s out &key (escape-html t))
  "encoding/json's string encoding: \\\" \\\\ \\b \\f \\n \\r \\t, other controls as \\u00XX,
<, > and & as \\u003c \\u003e \\u0026 when escape-html, U+2028 and U+2029 always escaped."
  (write-char #\" out)
  (loop for c across s
        for code = (char-code c)
        do (cond ((char= c #\") (write-string "\\\"" out))
                 ((char= c #\\) (write-string "\\\\" out))
                 ((= code 8) (write-string "\\b" out))
                 ((= code 12) (write-string "\\f" out))
                 ((= code 10) (write-string "\\n" out))
                 ((= code 13) (write-string "\\r" out))
                 ((= code 9) (write-string "\\t" out))
                 ((< code 32) (format out "\\u00~(~2,'0x~)" code))
                 ((and escape-html (member c '(#\< #\> #\&))) (format out "\\u00~(~2,'0x~)" code))
                 ((or (= code #x2028) (= code #x2029)) (format out "\\u~(~4,'0x~)" code))
                 (t (write-char c out))))
  (write-char #\" out))

(defstruct (raw-json (:constructor raw-json (text)))
  "JSON text written as it is (json.RawMessage), for a member whose encoding keeps a Go struct's
field order."
  (text "" :type string))

(defun write-json (v out &key (escape-html t))
  "encoding/json over a decoded value: object keys sorted."
  (cond ((raw-json-p v) (write-string (raw-json-text v) out))
        ((hash-table-p v)
         (let ((keys (sort (loop for k being the hash-keys of v collect k) #'string<)))
           (write-char #\{ out)
           (loop for (k . rest) on keys
                 do (write-json-string k out :escape-html escape-html)
                    (write-char #\: out)
                    (write-json (gethash k v) out :escape-html escape-html)
                    (when rest (write-char #\, out)))
           (write-char #\} out)))
        ((stringp v) (write-json-string v out :escape-html escape-html))
        ((vectorp v)
         (write-char #\[ out)
         (loop for i from 0 below (length v)
               do (when (> i 0) (write-char #\, out))
                  (write-json (aref v i) out :escape-html escape-html))
         (write-char #\] out))
        ((listp v)
         (if (null v)
             (write-string "null" out)
             (progn
               (write-char #\[ out)
               (loop for (x . rest) on v
                     do (write-json x out :escape-html escape-html)
                        (when rest (write-char #\, out)))
               (write-char #\] out))))
        ((eq v :true) (write-string "true" out))
        ((eq v :false) (write-string "false" out))
        ((eq v :null) (write-string "null" out))
        ((integerp v) (format out "~D" v))
        ((floatp v) (write-string (go-float-string v) out))
        (t (error "write-json: cannot encode ~S" v))))

(defun json-string (v &key (escape-html t))
  (with-output-to-string (out) (write-json v out :escape-html escape-html)))

(defun json-stringify (v)
  "serialize.JSONStringify: JSON.stringify of a decoded value, keys sorted, no HTML escapes;
\"{}\" for an absent value."
  (if (jnull-p v) "{}" (json-string v :escape-html nil)))
