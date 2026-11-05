;;;; Go's strings, unicode and strconv behaviors the engine relies on, over Lisp strings of
;;;; characters (code points). Go indexes strings by UTF-8 bytes; every port here indexes by
;;;; character, which gives the same results because every byte test the engine makes is on an
;;;; ASCII byte (docs/lisp-spike.md, "Strings").

(in-package "OM.BASE")

(deftype index () '(integer 0 #.array-dimension-limit))

;;; Unicode classes, as Go's unicode package reads them.

(defun gc (c) (sb-unicode:general-category c))

(defun letter-p (c)
  "unicode.IsLetter: category L."
  (member (gc c) '(:lu :ll :lt :lm :lo)))

(defun upper-p (c)
  "unicode.IsUpper: category Lu."
  (eq (gc c) :lu))

(defun digit-p (c)
  "unicode.IsDigit: category Nd."
  (eq (gc c) :nd))

(defun number-p (c)
  "\\p{N}: category N."
  (member (gc c) '(:nd :nl :no)))

(defun space-p (c)
  "unicode.IsSpace: the Latin-1 spaces and the White_Space property."
  (let ((code (char-code c)))
    (if (< code 256)
        (or (<= 9 code 13) (= code 32) (= code #x85) (= code #xa0))
        (and (sb-unicode:whitespace-p c) t))))

(defun print-p (c)
  "unicode.IsPrint: letters, marks, numbers, punctuation, symbols and the ASCII space."
  (or (char= c #\Space)
      (member (gc c) '(:lu :ll :lt :lm :lo :mn :mc :me :nd :nl :no
                       :pc :pd :ps :pe :pi :pf :po :sm :sc :sk :so))))

(defun ascii-word-p (c)
  "RE2's \\w and the ledger's isWordChar: [0-9A-Za-z_]."
  (or (char<= #\a c #\z) (char<= #\A c #\Z) (char<= #\0 c #\9) (char= c #\_)))

(defun lower-char (c)
  "unicode.ToLower: the simple lowercase mapping."
  (if (= (char-code c) #x130) #\i (char-downcase c)))

(defun to-lower (s)
  "strings.ToLower."
  (map 'string #'lower-char s))

;;; strings

(defun str (v)
  "A decoded JSON value as a Go string assertion reads it: the string, else \"\"."
  (if (stringp v) v ""))

(defun cat (&rest parts)
  (apply #'concatenate 'string parts))

(defun join (parts sep)
  "strings.Join."
  (with-output-to-string (out)
    (loop for (p . rest) on parts
          do (write-string p out)
             (when rest (write-string sep out)))))

(defun has-prefix (s prefix)
  (and (<= (length prefix) (length s))
       (string= s prefix :end1 (length prefix))))

(defun has-suffix (s suffix)
  (and (<= (length suffix) (length s))
       (string= s suffix :start1 (- (length s) (length suffix)))))

(defun trim-prefix (s prefix)
  (if (has-prefix s prefix) (subseq s (length prefix)) s))

(defun trim-suffix (s suffix)
  (if (has-suffix s suffix) (subseq s 0 (- (length s) (length suffix))) s))

(defun trim-space (s)
  "strings.TrimSpace."
  (let ((start (or (position-if-not #'space-p s) (length s)))
        (end (let ((p (position-if-not #'space-p s :from-end t))) (if p (1+ p) 0))))
    (if (< start end) (subseq s start end) "")))

(defun trim-left (s cutset)
  (let ((start (or (position-if-not (lambda (c) (find c cutset)) s) (length s))))
    (subseq s start)))

(defun trim-right (s cutset)
  (let ((end (let ((p (position-if-not (lambda (c) (find c cutset)) s :from-end t))) (if p (1+ p) 0))))
    (subseq s 0 end)))

(defun trim (s cutset) (trim-left (trim-right s cutset) cutset))

(defun fields-func (s split-p)
  "strings.FieldsFunc: the runs of characters split-p does not hold for."
  (let ((out '()) (start nil))
    (loop for i from 0 below (length s)
          for c = (char s i)
          do (if (funcall split-p c)
                 (when start (push (subseq s start i) out) (setf start nil))
                 (unless start (setf start i))))
    (when start (push (subseq s start) out))
    (nreverse out)))

(defun fields (s)
  "strings.Fields."
  (fields-func s #'space-p))

(defun one-line (s)
  "strings.Join(strings.Fields(s), \" \")."
  (join (fields s) " "))

(defun search-from (sub s start)
  (search sub s :start2 start))

(defun contains (s sub) (and (search sub s) t))

(defun contains-any (s chars) (and (find-if (lambda (c) (find c chars)) s) t))

(defun split (s sep)
  "strings.Split with a non-empty separator."
  (let ((out '()) (start 0) (n (length sep)))
    (loop for p = (search sep s :start2 start)
          while p
          do (push (subseq s start p) out)
             (setf start (+ p n)))
    (push (subseq s start) out)
    (nreverse out)))

(defun replace-all (s old new)
  "strings.ReplaceAll with a non-empty old."
  (join (split s old) new))

(defun count-sub (s sub)
  "strings.Count with a non-empty sub."
  (1- (length (split s sub))))

(defun last-index (s sub)
  (search sub s :from-end t))

(defun cut (s sep)
  "strings.Cut: before, after, found."
  (let ((p (search sep s)))
    (if p
        (values (subseq s 0 p) (subseq s (+ p (length sep))) t)
        (values s "" nil))))

(defun itoa (n) (format nil "~D" n))

(defun atoi (s)
  "strconv.Atoi of an ASCII decimal: the value, or nil."
  (when (and (plusp (length s))
             (every (lambda (c) (char<= #\0 c #\9)) (if (find (char s 0) "+-") (subseq s 1) s))
             (> (length s) (if (find (char s 0) "+-") 1 0)))
    (parse-integer s)))

(defun utf16-len (s)
  "jsfmt.UTF16Len: a JavaScript string's length."
  (loop for c across s sum (if (>= (char-code c) #x10000) 2 1)))

(defun utf16-head (s n)
  "jsfmt.Head: the first n UTF-16 units; a pair cut in half leaves U+FFFD."
  (with-output-to-string (out)
    (let ((units 0))
      (loop for c across s
            for w = (if (>= (char-code c) #x10000) 2 1)
            do (cond ((<= (+ units w) n) (write-char c out) (incf units w))
                     ((< units n) (write-char (code-char #xfffd) out) (return))
                     (t (return)))))))

(defun estimate-tokens (s)
  "tokens.EstimateStringTokens: UTF-16 length over four, rounded up."
  (floor (+ (utf16-len s) 3) 4))

(defun go-quote (s)
  "strconv.Quote."
  (with-output-to-string (out)
    (write-char #\" out)
    (loop for c across s
          for code = (char-code c)
          do (cond ((char= c #\") (write-string "\\\"" out))
                   ((char= c #\\) (write-string "\\\\" out))
                   ((print-p c) (write-char c out))
                   ((= code 7) (write-string "\\a" out))
                   ((= code 8) (write-string "\\b" out))
                   ((= code 12) (write-string "\\f" out))
                   ((= code 10) (write-string "\\n" out))
                   ((= code 13) (write-string "\\r" out))
                   ((= code 9) (write-string "\\t" out))
                   ((= code 11) (write-string "\\v" out))
                   ((or (< code 32) (= code 127)) (format out "\\x~(~2,'0x~)" code))
                   ((< code #x10000) (format out "\\u~(~4,'0x~)" code))
                   (t (format out "\\U~(~8,'0x~)" code))))
    (write-char #\" out)))

(defun string-list< (a b) (string< a b))

(defun stable-sort-list (list pred &key (key #'identity))
  (stable-sort (copy-list list) pred :key key))

;;; Small collections: Go slices are lists or adjustable vectors here; Go maps of strings are
;;; equal hash tables.

(defun make-set () (make-hash-table :test 'equal))

(defmacro push-end (item place)
  `(setf ,place (append ,place (list ,item))))

(defun member-str (s list) (member s list :test #'string=))
