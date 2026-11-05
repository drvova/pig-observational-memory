;;;; A backtracking matcher for the RE2 syntax the engine's patterns use. Go's regexp is RE2:
;;;; leftmost-first matching, which a backtracking matcher reproduces for patterns without
;;;; back-references (RE2 simulates exactly that priority order). The classes are RE2's: \d, \s
;;;; and \w are ASCII ([0-9], [\t\n\f\r ], [0-9A-Za-z_]), \b is an ASCII word boundary, . does
;;;; not match a newline, ^ and $ are text anchors unless (?m), and \p{L} \p{N} read the Unicode
;;;; categories. (?i) folds case by Lisp's simple case mapping, which equals Go's simple folding
;;;; for every character but the few with three-way orbits (k/K/U+212A, s/S/U+017F).
;;;;
;;;; Positions are character indexes, Go's are byte offsets; every caller here slices with the
;;;; indexes it got from the same string, so the results are the same strings.

(in-package "OM")

(defstruct (regex (:constructor %make-regex))
  source matcher (groups 0 :type fixnum) names first anchored)

;;; Parsing to a tree:
;;;   (:char c fold) (:class negated items fold) (:any dotall) (:bol multi) (:eol multi)
;;;   (:wordb) (:nwordb) (:begin-text) (:end-text) (:seq . nodes) (:alt . nodes)
;;;   (:group n node) (:repeat min max greedy node)
;;; Class items: (lo . hi) ranges, :digit :space :word and their negations (:not :digit), and
;;; (:category "L") / (:not-category "L").

(defvar *re-src*)
(defvar *re-pos*)
(defvar *re-groups*)
(defvar *re-names*)

(defun re-peek () (and (< *re-pos* (length *re-src*)) (char *re-src* *re-pos*)))
(defun re-next () (prog1 (char *re-src* *re-pos*) (incf *re-pos*)))
(defun re-error (msg) (error "regex ~S: ~A at ~D" *re-src* msg *re-pos*))

(defun parse-regex (source)
  (let ((*re-src* source) (*re-pos* 0) (*re-groups* 0) (*re-names* (list "")))
    (let ((tree (re-parse-alt (list :fold nil :multi nil :dotall nil))))
      (when (< *re-pos* (length source)) (re-error "unbalanced )"))
      (values tree *re-groups* (coerce (reverse *re-names*) 'simple-vector)))))

(defun re-parse-alt (flags)
  ;; Flags a (?i) sets apply to the rest of the group, every alternative after it included.
  (let ((branches '()) (flags (copy-list flags)))
    (loop
      (multiple-value-bind (node new-flags) (re-parse-concat flags)
        (push node branches)
        (setf flags new-flags))
      (if (eql (re-peek) #\|)
          (re-next)
          (return)))
    (if (rest branches) (cons :alt (nreverse branches)) (first branches))))

(defun re-parse-concat (flags)
  (let ((items '()))
    (loop
      (let ((c (re-peek)))
        (when (or (null c) (char= c #\|) (char= c #\))) (return))
        (multiple-value-bind (atom new-flags) (re-parse-atom flags)
          (if new-flags
              (setf flags new-flags)
              (push (re-parse-repeat atom) items)))))
    (values (cons :seq (nreverse items)) flags)))

(defun re-parse-int ()
  (let ((start *re-pos*))
    (loop while (and (re-peek) (digit-char-p (re-peek))) do (re-next))
    (and (> *re-pos* start) (parse-integer *re-src* :start start :end *re-pos*))))

(defun re-parse-repeat (atom)
  (loop
    (let ((c (re-peek)) min max)
      (case c
        (#\* (re-next) (setf min 0 max nil))
        (#\+ (re-next) (setf min 1 max nil))
        (#\? (re-next) (setf min 0 max 1))
        (#\{
         (let ((save *re-pos*))
           (re-next)
           (let ((lo (re-parse-int)))
             (cond ((null lo) (setf *re-pos* save) (return atom))
                   ((eql (re-peek) #\}) (re-next) (setf min lo max lo))
                   ((eql (re-peek) #\,)
                    (re-next)
                    (let ((hi (re-parse-int)))
                      (if (eql (re-peek) #\})
                          (progn (re-next) (setf min lo max hi))
                          (progn (setf *re-pos* save) (return atom)))))
                   (t (setf *re-pos* save) (return atom))))))
        (t (return atom)))
      (let ((greedy t))
        (when (eql (re-peek) #\?) (re-next) (setf greedy nil))
        (setf atom (list :repeat min max greedy atom))))))

(defun re-parse-flags ()
  "Reads flag letters up to : or ); returns the letters and the terminator."
  (let ((letters '()) (negate nil))
    (loop
      (let ((c (re-next)))
        (case c
          ((#\i #\m #\s #\U) (push (cons c (not negate)) letters))
          (#\- (setf negate t))
          ((#\: #\)) (return (values (nreverse letters) c)))
          (t (re-error "bad flag")))))))

(defun re-apply-flags (flags letters)
  (let ((flags (copy-list flags)))
    (loop for (c . on) in letters
          do (case c
               (#\i (setf (getf flags :fold) on))
               (#\m (setf (getf flags :multi) on))
               (#\s (setf (getf flags :dotall) on))))
    flags))

(defun re-parse-atom (flags)
  "One atom, or (values nil new-flags) for a flag group (?i) that sets flags for the rest."
  (let ((c (re-next)))
    (case c
      (#\(
       (cond
         ((and (eql (re-peek) #\?) (< (1+ *re-pos*) (length *re-src*)) (char= (char *re-src* (1+ *re-pos*)) #\P))
          (re-next) (re-next)
          (unless (eql (re-next) #\<) (re-error "bad named group"))
          (let ((end (position #\> *re-src* :start *re-pos*)))
            (let ((name (subseq *re-src* *re-pos* end)))
              (setf *re-pos* (1+ end))
              (let ((n (incf *re-groups*)))
                (push name *re-names*)
                (let ((node (re-parse-alt flags)))
                  (unless (eql (re-next) #\)) (re-error "missing )"))
                  (list :group n node))))))
         ((eql (re-peek) #\?)
          (re-next)
          (multiple-value-bind (letters terminator) (re-parse-flags)
            (if (char= terminator #\))
                (values nil (re-apply-flags flags letters))
                (let ((node (re-parse-alt (re-apply-flags flags letters))))
                  (unless (eql (re-next) #\)) (re-error "missing )"))
                  node))))
         (t
          (let ((n (incf *re-groups*)))
            (push "" *re-names*)
            (let ((node (re-parse-alt flags)))
              (unless (eql (re-next) #\)) (re-error "missing )"))
              (list :group n node))))))
      (#\[ (re-parse-class flags))
      (#\. (list :any (getf flags :dotall)))
      (#\^ (list :bol (getf flags :multi)))
      (#\$ (list :eol (getf flags :multi)))
      (#\\ (re-parse-escape flags nil))
      (t (list :char c (getf flags :fold))))))

(defun re-escape-char (c)
  (case c
    (#\n #\Newline) (#\t #\Tab) (#\r #\Return) (#\f (code-char 12)) (#\v (code-char 11))
    (#\a (code-char 7))
    (t (if (or (alphanumericp c)) (re-error (format nil "unknown escape \\~A" c)) c))))

(defun re-parse-category ()
  (let ((c (re-next)))
    (if (char= c #\{)
        (let ((end (position #\} *re-src* :start *re-pos*)))
          (prog1 (subseq *re-src* *re-pos* end) (setf *re-pos* (1+ end))))
        (string c))))

(defun re-parse-escape (flags in-class)
  "The atom (or, in a class, the class item) after a backslash."
  (let ((c (re-next)))
    (flet ((item (x) (if in-class x (list :class nil (list x) nil))))
      (case c
        (#\d (item :digit)) (#\D (item '(:not :digit)))
        (#\s (item :space)) (#\S (item '(:not :space)))
        (#\w (item :word)) (#\W (item '(:not :word)))
        (#\p (item (list :category (re-parse-category))))
        (#\P (item (list :not-category (re-parse-category))))
        (#\b (if in-class (re-error "\\b in class") (list :wordb)))
        (#\B (list :nwordb))
        (#\A (list :begin-text))
        (#\z (list :end-text))
        (t (let ((ch (re-escape-char c)))
             (if in-class ch (list :char ch (getf flags :fold)))))))))

(defun re-parse-class (flags)
  (let ((negated nil) (items '()) (first t))
    (when (eql (re-peek) #\^) (re-next) (setf negated t))
    (loop
      (let ((c (re-next)))
        (when (and (char= c #\]) (not first)) (return))
        (setf first nil)
        (let ((lo (if (char= c #\\) (re-parse-escape flags t) c)))
          (if (and (characterp lo) (eql (re-peek) #\-)
                   (< (1+ *re-pos*) (length *re-src*))
                   (char/= (char *re-src* (1+ *re-pos*)) #\]))
              (progn
                (re-next)
                (let* ((h (re-next))
                       (hi (if (char= h #\\) (re-parse-escape flags t) h)))
                  (push (cons lo hi) items)))
              (push (if (characterp lo) (cons lo lo) lo) items)))))
    (list :class negated (nreverse items) (getf flags :fold))))

;;; Matching

(defun category-match-p (name c)
  (let ((g (gc c)))
    (cond ((string= name "L") (member g '(:lu :ll :lt :lm :lo)))
          ((string= name "N") (member g '(:nd :nl :no)))
          ((string= name "Lu") (eq g :lu))
          ((string= name "Ll") (eq g :ll))
          ((string= name "Nd") (eq g :nd))
          (t (error "regex: unsupported category ~A" name)))))

(defun compile-class-test (negated items fold)
  "A class as a predicate: ASCII characters by a table computed once, others by the items."
  (let ((table (make-array 128 :element-type 'bit :initial-element 0)))
    (dotimes (code 128)
      (let ((m (class-match-p items fold (code-char code))))
        (setf (sbit table code) (if (if negated (not m) m) 1 0))))
    (lambda (c)
      (let ((code (char-code c)))
        (if (< code 128)
            (= 1 (sbit table code))
            (let ((m (class-match-p items fold c))) (if negated (not m) m)))))))

(defun class-item-match-p (item c)
  (cond ((consp item)
         (case (car item)
           (:not (not (class-item-match-p (cadr item) c)))
           (:category (category-match-p (cadr item) c))
           (:not-category (not (category-match-p (cadr item) c)))
           (t (char<= (car item) c (cdr item)))))
        ((eq item :digit) (char<= #\0 c #\9))
        ((eq item :space) (member c '(#\Tab #\Newline #\Page #\Return #\Space)))
        ((eq item :word) (ascii-word-p c))
        (t (error "regex: bad class item ~S" item))))

(defun class-match-p (items fold c)
  (or (some (lambda (item) (class-item-match-p item c)) items)
      (and fold
           (let* ((k (fold-key c)) (u (char-upcase k)))
             (some (lambda (x) (and (char/= x c) (some (lambda (item) (class-item-match-p item x)) items)))
                   (list (char-upcase c) (char-downcase c) k u))))))

;;; The case-folding orbits Go's unicode.SimpleFold has and Lisp's char-downcase/char-upcase
;;; miss (SBCL maps a character's case only when the mapping round-trips): each character here
;;; folds to the lowercase one beside it.
(defparameter +extra-folds+
  (let ((h (make-hash-table)))
    (loop for (from to) in '((#x212a #x6b) (#x17f #x73) (#x3c2 #x3c3) (#x2126 #x3c9) (#x212b #xe5)
                             (#xb5 #x3bc) (#x3d0 #x3b2) (#x3d1 #x3b8) (#x3d5 #x3c6) (#x3d6 #x3c0)
                             (#x3f0 #x3ba) (#x3f1 #x3c1) (#x3f5 #x3b5) (#x1e9b #x1e61) (#x1fbe #x3b9)
                             (#x345 #x3b9) (#x1e9e #xdf))
          do (setf (gethash (code-char from) h) (code-char to)))
    h))

(defun fold-key (c)
  (char-downcase (gethash c +extra-folds+ c)))

(declaim (inline fold-equal))
(defun fold-equal (a b)
  (or (char= a b)
      (if (and (< (char-code a) 128) (< (char-code b) 128))
          (char-equal a b)
          (or (char= (fold-key a) (fold-key b)) (char= (char-upcase a) (char-upcase b))))))

(defvar *caps*)

(defun re-compile-node (node)
  "A matcher: (lambda (s i k)) calls k with the position after a match at i, in priority order,
and returns what the first successful k returns."
  (ecase (car node)
    (:char
     (destructuring-bind (c fold) (cdr node)
       (if fold
           (lambda (s i k) (declare (type simple-string s) (type fixnum i) (type function k))
             (and (< i (length s)) (fold-equal (char s i) c) (funcall k (1+ i))))
           (lambda (s i k) (declare (type simple-string s) (type fixnum i) (type function k))
             (and (< i (length s)) (char= (char s i) c) (funcall k (1+ i)))))))
    (:class
     (let ((test (apply #'compile-class-test (cdr node))))
       (declare (type function test))
       (lambda (s i k) (declare (type simple-string s) (type fixnum i) (type function k))
         (and (< i (length s)) (funcall test (char s i)) (funcall k (1+ i))))))
    (:any
     (let ((dotall (second node)))
       (lambda (s i k) (declare (type simple-string s) (type fixnum i) (type function k))
         (and (< i (length s)) (or dotall (char/= (char s i) #\Newline)) (funcall k (1+ i))))))
    (:bol
     (let ((multi (second node)))
       (lambda (s i k) (declare (type simple-string s) (type fixnum i) (type function k))
         (and (or (= i 0) (and multi (char= (char s (1- i)) #\Newline))) (funcall k i)))))
    (:eol
     (let ((multi (second node)))
       (lambda (s i k) (declare (type simple-string s) (type fixnum i) (type function k))
         (and (or (= i (length s)) (and multi (char= (char s i) #\Newline))) (funcall k i)))))
    (:begin-text (lambda (s i k) (declare (ignore s)) (and (= i 0) (funcall k i))))
    (:end-text (lambda (s i k) (and (= i (length s)) (funcall k i))))
    ((:wordb :nwordb)
     (let ((want (eq (car node) :wordb)))
       (lambda (s i k) (declare (type simple-string s) (type fixnum i) (type function k))
         (let ((before (and (> i 0) (ascii-word-p (char s (1- i)))))
               (after (and (< i (length s)) (ascii-word-p (char s i)))))
           (and (eq want (not (eq before after))) (funcall k i))))))
    (:seq
     (let ((parts (mapcar #'re-compile-node (cdr node))))
       (cond ((null parts) (lambda (s i k) (declare (ignore s)) (funcall k i)))
             ((null (cdr parts)) (first parts))
             (t (reduce (lambda (m rest)
                          (declare (type function m rest))
                          (lambda (s i k)
                            (funcall m s i (lambda (j) (funcall rest s j k)))))
                        parts :from-end t)))))
    (:alt
     (let ((parts (mapcar #'re-compile-node (cdr node))))
       (lambda (s i k)
         (loop for m in parts
               thereis (funcall (the function m) s i k)))))
    (:group
     (destructuring-bind (n inner) (cdr node)
       (let ((m (re-compile-node inner)) (a (* 2 n)) (b (1+ (* 2 n))))
         (lambda (s i k)
           (funcall (the function m) s i
                    (lambda (j)
                      (let ((pa (aref *caps* a)) (pb (aref *caps* b)))
                        (setf (aref *caps* a) i (aref *caps* b) j)
                        (or (funcall k j)
                            (progn (setf (aref *caps* a) pa (aref *caps* b) pb) nil)))))))))
    (:repeat
     (destructuring-bind (min max greedy inner) (cdr node)
       (if (member (car inner) '(:char :class :any))
           (re-compile-char-repeat min max greedy inner)
           (re-compile-repeat min max greedy inner))))))

(defun re-compile-char-repeat (min max greedy inner)
  "A repeat of one character test, run as a loop: the same priority order as the general
repeat, without a stack frame per character, so a [\\s\\S]*? over a long text stays shallow."
  (let ((test (let ((one (re-compile-node inner)))
                (declare (type function one))
                (lambda (s i) (funcall one s i (lambda (j) (declare (ignore j)) t))))))
    (declare (type function test))
    (lambda (s i k)
      (declare (type simple-string s) (type fixnum i) (type function k))
      (let ((limit (if max (min (length s) (+ i max)) (length s))))
        (if greedy
            (let ((run (loop for j from i below limit while (funcall test s j) count t)))
              (loop for n from run downto min
                    thereis (funcall k (+ i n))))
            (loop for n from 0
                  for j = (+ i n)
                  do (when (and (>= n min) (funcall k j)) (return t))
                     (when (or (>= j limit) (not (funcall test s j))) (return nil))))))))

(defun re-compile-repeat (min max greedy inner)
       (let ((m (re-compile-node inner)))
         (declare (type function m))
         (labels ((rep (s i count k)
                    (declare (type fixnum i count) (type function k))
                    (flet ((more ()
                             (and (or (null max) (< count max))
                                  (funcall m s i
                                           (lambda (j)
                                             ;; An empty iteration past the minimum ends the loop.
                                             (and (not (and (= j i) (>= count min)))
                                                  (rep s j (1+ count) k))))))
                           (done () (and (>= count min) (funcall k i))))
                      (if greedy
                          (or (more) (done))
                          (or (done) (more))))))
           (lambda (s i k) (rep s i 0 k)))))

(defun compile-regex (source)
  (multiple-value-bind (tree groups names) (parse-regex source)
    (%make-regex :source source :matcher (re-compile-node tree) :groups groups :names names
                 :first (re-first-test tree) :anchored (re-anchored-p tree))))

(defmacro defregex (name source)
  `(defparameter ,name (compile-regex ,source)))

;;; Search shortcuts, which change no result: a pattern that must start with a character test
;;; skips the positions that fail it, and one anchored at the text's start (^ without (?m))
;;; tries position 0 only.

(defun zero-width-p (node) (member (car node) '(:bol :eol :wordb :nwordb :begin-text :end-text)))

(defun re-first-set (node)
  "The tests the first character of every match passes one of, as (chars . node-tests), or
:unknown when the pattern can match empty or starts with a test this does not read."
  (case (car node)
    (:char (destructuring-bind (c fold) (cdr node)
             (if (and fold (< (char-code c) 128))
                 (cons (remove-duplicates (list c (char-upcase c) (char-downcase c))) nil)
                 (if fold (cons nil (list node)) (cons (list c) nil)))))
    ((:class :any) (cons nil (list node)))
    (:group (re-first-set (third node)))
    (:repeat (if (>= (second node) 1) (re-first-set (fifth node)) :unknown))
    (:seq (let ((first (find-if-not #'zero-width-p (cdr node))))
            (if first (re-first-set first) :unknown)))
    (:alt (let ((sets (mapcar #'re-first-set (cdr node))))
            (if (member :unknown sets)
                :unknown
                (cons (reduce #'union (mapcar #'car sets)) (reduce #'append (mapcar #'cdr sets))))))
    (t :unknown)))

(defun re-first-test (node)
  "A test the first character of every match passes, or nil when there is none. An ASCII
pattern character under (?i) reads only its two cases, as Go's folding does for ASCII but k and
s (whose orbits hold U+212A and U+017F, non-ASCII: those go through the node test)."
  (let ((set (re-first-set node)))
    (when (eq set :unknown) (return-from re-first-test nil))
    (destructuring-bind (chars . nodes) set
      (let ((ascii (make-array 128 :element-type 'bit :initial-element 0))
            (other '())
            (tests (mapcar (lambda (n)
                             (ecase (car n)
                               (:class (apply #'compile-class-test (cdr n)))
                               (:any (if (second n) (lambda (c) (declare (ignore c)) t) (lambda (c) (char/= c #\Newline))))
                               (:char (let ((p (second n))) (lambda (c) (fold-equal c p))))))
                           nodes)))
        (dolist (c chars)
          (if (< (char-code c) 128) (setf (sbit ascii (char-code c)) 1) (push c other)))
        ;; k and s fold onto U+212A and U+017F.
        (when (= 1 (sbit ascii (char-code #\k))) (push (code-char #x212a) other))
        (when (= 1 (sbit ascii (char-code #\s))) (push (code-char #x17f) other))
        (lambda (c)
          (let ((code (char-code c)))
            (or (if (< code 128) (= 1 (sbit ascii code)) (and (member c other) t))
                (some (lambda (test) (funcall (the function test) c)) tests))))))))

(defun re-anchored-p (node)
  (case (car node)
    (:bol (not (second node)))
    (:begin-text t)
    (:group (re-anchored-p (third node)))
    (:seq (and (cdr node) (re-anchored-p (second node))))
    (:alt (every #'re-anchored-p (cdr node)))
    (t nil)))

(defun regex-exec (re s start)
  "The leftmost match at or after start: a vector of 2*(groups+1) positions (-1 for a group
that did not take part), or nil."
  (declare (type simple-string s) (type fixnum start))
  (let* ((n (length s))
         (ncap (* 2 (1+ (regex-groups re))))
         (*caps* (make-array ncap :initial-element -1))
         (matcher (regex-matcher re))
         (first (regex-first re))
         (end -1)
         (done (lambda (j) (setf end j) t))
         (last (if (regex-anchored re) (min n 0) n)))
    (declare (type function matcher))
    (loop for i from start to last
          do (when (or (null first) (and (< i n) (funcall (the function first) (char s i))))
               (when (> ncap 2) (fill *caps* -1))
               (when (funcall matcher s i done)
                 (setf (aref *caps* 0) i (aref *caps* 1) end)
                 (return-from regex-exec (copy-seq *caps*)))))
    nil))

(defun simple (s) (coerce s 'simple-string))

(defun regex-match-p (re s)
  (and (regex-exec re (simple s) 0) t))

(defun regex-find-all-index (re s)
  "FindAllStringSubmatchIndex: Go's rule for empty matches (one abutting the previous match is
not reported, and the search moves one character on)."
  (let ((s (simple s)) (pos 0) (prev-end -1) (out '()))
    (loop while (<= pos (length s))
          do (let ((m (regex-exec re s pos)))
               (unless m (return))
               (let ((accept t))
                 (if (= (aref m 1) (aref m 0))
                     (progn (when (= (aref m 0) prev-end) (setf accept nil))
                            (setf pos (1+ (aref m 1))))
                     (setf pos (aref m 1)))
                 (setf prev-end (aref m 1))
                 (when accept (push m out)))))
    (nreverse out)))

(defun match-group (s m g)
  "Group g of match m as a string, nil when it did not take part."
  (let ((a (aref m (* 2 g))) (b (aref m (1+ (* 2 g)))))
    (and (>= a 0) (subseq s a b))))

(defun regex-find-string (re s)
  "FindString: the leftmost match, \"\" for none."
  (let ((m (regex-exec re (simple s) 0)))
    (if m (subseq s (aref m 0) (aref m 1)) "")))

(defun regex-find-submatch (re s)
  "FindStringSubmatch: a list of the groups' strings (\"\" for a group that did not take part),
or nil."
  (let ((m (regex-exec re (simple s) 0)))
    (and m (loop for g from 0 to (regex-groups re) collect (or (match-group s m g) "")))))

(defun regex-find-submatch-index (re s)
  (regex-exec re (simple s) 0))

(defun regex-find-all-strings (re s)
  (mapcar (lambda (m) (subseq s (aref m 0) (aref m 1))) (regex-find-all-index re s)))

(defun regex-find-all-submatch (re s)
  (mapcar (lambda (m) (loop for g from 0 to (regex-groups re) collect (or (match-group s m g) "")))
          (regex-find-all-index re s)))

(defun expand-template (template s m re)
  "Regexp.Expand of a template with $1, ${1} and $name references."
  (with-output-to-string (out)
    (let ((i 0) (n (length template)))
      (loop while (< i n)
            do (let ((c (char template i)))
                 (if (and (char= c #\$) (< (1+ i) n))
                     (let* ((braced (char= (char template (1+ i)) #\{))
                            (start (if braced (+ i 2) (1+ i)))
                            (end (or (position-if-not (lambda (ch) (or (alphanumericp ch) (char= ch #\_))) template :start start) n))
                            (name (subseq template start end)))
                       (when (and braced (or (>= end n) (char/= (char template end) #\}))) (setf name ""))
                       (if (string= name "")
                           (progn (write-char c out) (incf i))
                           (let ((g (or (and (every #'digit-char-p name) (parse-integer name))
                                        (position name (regex-names re) :test #'string=))))
                             (when (and g (<= g (regex-groups re)))
                               (write-string (or (match-group s m g) "") out))
                             (setf i (if braced (1+ end) end)))))
                     (progn (write-char c out) (incf i))))))))

(defun regex-replace-all (re s template)
  "ReplaceAllString: Go's rule for empty matches."
  (let* ((s (simple s)) (n (length s)) (last 0) (search 0))
    (with-output-to-string (out)
      (loop while (<= search n)
            do (let ((m (regex-exec re s search)))
                 (unless m (return))
                 (write-string s out :start last :end (aref m 0))
                 (when (or (> (aref m 1) last) (= (aref m 0) 0))
                   (write-string (expand-template template s m re) out))
                 (setf last (aref m 1))
                 (cond ((> (+ search 1) (aref m 1)) (incf search))
                       (t (setf search (aref m 1))))))
      (write-string s out :start last))))
