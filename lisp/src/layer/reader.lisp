;;;; The safe reader (docs/lisp-core.md, "The safe reader"): model code is read by this reader,
;;;; never by CL:READ. It implements the reader syntax model code needs (lists, dotted pairs,
;;;; strings, numbers, characters, quote, backquote, #' and #( ) and refuses everything else at
;;;; read time: a symbol outside the allowed set, a package-qualified symbol (which would intern
;;;; into another package), an escaped symbol, and every # dispatch it does not implement (#.,
;;;; #+, #-, #:, #P, #S, #A, #=, ...). *read-eval* is off around it and it never calls the Lisp
;;;; reader on anything but a token already checked to be a number.
;;;;
;;;; The allowed set is: the CL operators in +allowed-cl+, OM.TOOL's exports, the safe wrappers
;;;; that stand for FORMAT and ERROR, and the symbols of the model's own package (its
;;;; definitions, variables and local names). A new name is interned in the model's package
;;;; unless it names a CL symbol outside the allowed set or an external symbol of SBCL's own
;;;; packages (run-program, ...), which is refused, so a typo for a denied operator is caught
;;;; when the code is read. This guards against mistakes, not attacks (docs/lisp-port-tool-layer.md).

(in-package "OM.LAYER")

(define-condition refused (error)
  ((reason :initarg :reason :reader refused-reason))
  (:report (lambda (c s) (format s "refused: ~A" (refused-reason c)))))

(defun refuse (fmt &rest args)
  (error 'refused :reason (apply #'format nil fmt args)))

(defparameter +allowed-cl+
  '(;; binding, control and definition
    quote function lambda let let* flet labels progn prog1 prog2 if when unless cond case ecase
    otherwise typecase etypecase and or not block return return-from tagbody go loop do do*
    dolist dotimes multiple-value-bind multiple-value-list multiple-value-call values values-list
    setq setf psetf incf decf push pop pushnew destructuring-bind declare ignore ignorable type
    the defun defvar defparameter defconstant handler-case ignore-errors check-type typep
    funcall apply identity constantly complement nth-value
    &optional &rest &key &aux &body &allow-other-keys t nil
    ;; predicates and comparison
    null atom consp listp numberp integerp rationalp floatp realp stringp symbolp characterp
    functionp vectorp arrayp hash-table-p keywordp zerop plusp minusp evenp oddp eq eql equal
    equalp
    ;; numbers
    + - * / = /= < > <= >= 1+ 1- min max abs signum floor ceiling truncate round ffloor mod rem
    expt sqrt exp log gcd lcm float rational rationalize numerator denominator parse-integer
    number integer fixnum ratio real double-float single-float isqrt
    ;; lists
    car cdr caar cadr cdar cddr caddr cdddr cadddr first second third fourth fifth sixth rest
    last butlast nbutlast nthcdr nth cons list list* append nconc reverse nreverse length member
    member-if assoc assoc-if rassoc getf mapcar mapc mapcan maplist mapcon reduce remove
    remove-if remove-if-not remove-duplicates delete delete-if delete-if-not delete-duplicates
    count count-if count-if-not find find-if find-if-not position position-if some every notany
    notevery sort stable-sort merge subseq copy-list copy-seq copy-tree elt aref svref vector
    acons pairlis union intersection set-difference set-exclusive-or subsetp adjoin
    sublis subst substitute substitute-if endp tailp ldiff revappend list-length
    ;; sequences and strings
    map map-into concatenate search mismatch replace fill coerce sequence string string= string/=
    string< string> string<= string>= string-equal string-lessp string-greaterp string-upcase
    string-downcase string-capitalize string-trim string-left-trim string-right-trim char char=
    char/= char< char> char<= char>= char-equal char-upcase char-downcase char-code code-char
    alpha-char-p digit-char-p alphanumericp upper-case-p lower-case-p both-case-p
    schar digit-char character simple-string write-to-string
    ;; output to the call's captured output
    princ prin1 print write-string write-line write-char terpri fresh-line princ-to-string
    prin1-to-string with-output-to-string make-string-output-stream get-output-stream-string
    ;; hash tables and arrays
    make-hash-table gethash remhash maphash hash-table-count clrhash hash-table
    array-dimension vector-push vector-push-extend fill-pointer array boolean keyword
    cons list vector)
  "The CL symbols model code may name.")

(defparameter +denied-cl+
  '(eval compile compile-file load open with-open-file with-open-stream close probe-file
    delete-file rename-file directory ensure-directories-exist file-write-date file-length
    make-pathname pathname merge-pathnames truename user-homedir-pathname
    intern unintern find-symbol find-all-symbols make-symbol gensym gentemp symbol-function
    symbol-value symbol-plist get fdefinition makunbound fmakunbound set boundp fboundp
    find-package make-package delete-package in-package use-package unuse-package export
    unexport import shadow shadowing-import rename-package do-symbols do-external-symbols
    do-all-symbols with-package-iterator list-all-packages package-name defpackage *package*
    read read-from-string read-preserving-whitespace read-line read-char read-delimited-list
    *read-eval* *readtable* set-macro-character set-dispatch-macro-character
    get-macro-character copy-readtable readtable make-dispatch-macro-character
    eval-when load-time-value macrolet symbol-macrolet defmacro define-symbol-macro
    define-compiler-macro proclaim declaim optimize special unwind-protect catch throw
    invoke-debugger *debugger-hook* break warn cerror signal assert describe inspect ed dribble
    room gc apropos trace untrace step time sleep require provide formatter
    defclass defgeneric defmethod make-instance slot-value change-class defstruct
    define-condition make-condition *standard-output* *terminal-io* *query-io*
    y-or-n-p yes-or-no-p machine-instance software-type)
  "The CL operators the design names as denied, refused by name with a clear reason. Every
other CL symbol outside +allowed-cl+ is refused too.")

(defparameter +denied-system-packages+
  '("SB-EXT" "SB-SYS" "SB-ALIEN" "SB-POSIX" "SB-THREAD" "SB-KERNEL" "SB-IMPL" "SB-UNIX"
    "SB-DEBUG" "SB-INTROSPECT" "SB-MOP" "SB-PCL" "SB-C" "SB-VM" "SB-BSD-SOCKETS" "ASDF" "UIOP")
  "Packages whose external symbols' names a new model name may not take.")

(defparameter +wrapped+
  '(("FORMAT" . safe-format) ("ERROR" . safe-error) ("MAKE-ARRAY" . safe-make-array)
    ("MAKE-STRING" . safe-make-string) ("MAKE-LIST" . safe-make-list)
    ;; limits.lisp: the points where code can go on for ever check the run's budget
    ("LOOP" . safe-loop) ("DO" . safe-do) ("DO*" . safe-do*) ("DOLIST" . safe-dolist)
    ("DOTIMES" . safe-dotimes) ("TAGBODY" . safe-tagbody) ("DEFUN" . safe-defun)
    ("LAMBDA" . safe-lambda) ("LABELS" . safe-labels) ("FLET" . safe-flet)
    ("FUNCALL" . safe-funcall) ("APPLY" . safe-apply)
    ;; no handler of model code sees a storage condition
    ("IGNORE-ERRORS" . safe-ignore-errors)
    ;; no list made from code, no allocation past the cap, no circular list
    ("COERCE" . safe-coerce) ("MAKE-HASH-TABLE" . safe-make-hash-table)
    ("VECTOR-PUSH-EXTEND" . safe-vector-push-extend) ("EXPT" . safe-expt)
    ("SETF" . safe-setf) ("PSETF" . safe-psetf) ("NCONC" . safe-nconc) ("MAPCAN" . safe-mapcan)
    ("MAPCON" . safe-mapcon) ("MERGE" . safe-merge)
    ;; no symbol's own name handed to model code to change
    ("STRING" . safe-string) ("STRING-TRIM" . safe-string-trim)
    ("STRING-LEFT-TRIM" . safe-string-left-trim) ("STRING-RIGHT-TRIM" . safe-string-right-trim))
  "CL names model code reads as a checked wrapper.")

(defparameter +denied-names+
  '("SYMBOLS" "PRESENT-SYMBOL" "PRESENT-SYMBOLS" "EXTERNAL-SYMBOL" "EXTERNAL-SYMBOLS")
  "Names that are no CL symbol but that LOOP reads as iterating a package's symbols, which would
hand model code symbols it could call; SYMBOL itself is a CL symbol outside the allowed set.")

(defvar *allowed* nil "Name -> symbol, built on first use: the allowed set but the model's own.")

(defun allowed-table ()
  (or *allowed*
      (let ((h (make-hash-table :test 'equal)))
        (dolist (s +allowed-cl+) (setf (gethash (symbol-name s) h) s))
        (do-external-symbols (s "OM.TOOL") (setf (gethash (symbol-name s) h) s))
        (loop for (name . s) in +wrapped+ do (setf (gethash name h) s))
        (setf *allowed* h))))

(defun denied-reason (name)
  "Why a new name may not be read as a model symbol, or nil."
  (when (member name +denied-names+ :test #'string=)
    (return-from denied-reason (format nil "~(~A~): iterating a package's symbols is denied in model code" name)))
  (multiple-value-bind (s status) (find-symbol name "COMMON-LISP")
    (when (and s (eq status :external))
      (return-from denied-reason
        (if (member s +denied-cl+)
            (format nil "~(~A~) is denied in model code" name)
            (format nil "~(~A~) is not among the operators model code may use" name)))))
  (dolist (p +denied-system-packages+)
    (let ((pkg (find-package p)))
      (when pkg
        (multiple-value-bind (s status) (find-symbol name pkg)
          (when (and s (eq status :external))
            (return-from denied-reason
              (format nil "~(~A~) names ~A's operator, which model code may not use" name p)))))))
  nil)

;;; The model's package

(defun own-symbol (name package)
  "The symbol of name present in package itself (its own definitions), not one it inherits."
  (multiple-value-bind (s status) (find-symbol name package)
    (and s (member status '(:internal :external)) s)))

(defun model-dir (model)
  "layer.ModelDir: the model id with bytes outside [a-z0-9.-] written as _hh."
  (with-output-to-string (out)
    (loop for byte across (sb-ext:string-to-octets model :external-format :utf-8)
          for c = (code-char byte)
          do (if (or (char<= #\a c #\z) (char<= #\0 c #\9) (char= c #\.) (char= c #\-))
                 (write-char c out)
                 (format out "_~(~2,'0x~)" byte)))))

(defun model-package (model)
  "om.layer.<model>: the package a model's code is read into. It uses no package: every symbol
the reader hands back from outside it was resolved by the allowed set. It uses CL and
OM.TOOL only so its values print unqualified: the reader never reads an inherited symbol
it has not resolved through the allowed set first."
  (let ((name (concatenate 'string "OM.LAYER." (string-upcase (model-dir model)))))
    (or (find-package name) (make-package name :use '("COMMON-LISP" "OM.TOOL")))))

(defun resolve-symbol (token package)
  (when (or (find #\| token) (find #\\ token))
    (refuse "the escaped symbol ~A" token))
  (let ((colon (position #\: token)))
    (cond ((and colon (zerop colon) (not (find #\: token :start 1)) (> (length token) 1))
           (intern (string-upcase (subseq token 1)) "KEYWORD"))
          (colon (refuse "the package-qualified symbol ~A: model code names only its own symbols, OM.TOOL's and the allowed operators" token))
          ((every (lambda (c) (char= c #\.)) token)
           (refuse "the token ~A" token))
          (t
           (let ((name (string-upcase token)))
             (multiple-value-bind (s found) (gethash name (allowed-table))
               (cond (found s)
                     ((own-symbol name package))
                     (t (let ((why (denied-reason name)))
                          (when why (refuse "~A" why))
                          (intern name package))))))))))

;;; Numbers: only a token this shape is handed to the Lisp reader.

(defun number-token-p (token)
  (let ((n (length token)) (i 0))
    (labels ((digits () (let ((start i)) (loop while (and (< i n) (digit-char-p (char token i))) do (incf i)) (> i start))))
      (when (and (< i n) (find (char token i) "+-")) (incf i))
      (let ((int (digits)))
        (cond ((= i n) int)
              ((and int (char= (char token i) #\/))
               (incf i) (and (digits) (= i n)))
              (t
               (let ((frac nil))
                 (when (and (< i n) (char= (char token i) #\.))
                   (incf i) (setf frac (digits))
                   (when (and int (= i n)) (return-from number-token-p t)))
                 (and (or int frac)
                      (if (and (< i n) (find (char-downcase (char token i)) "edfsl"))
                          (progn (incf i)
                                 (when (and (< i n) (find (char token i) "+-")) (incf i))
                                 (and (digits) (= i n)))
                          (and frac (= i n)))))))))))

(defun parse-number-token (token)
  (let ((*read-eval* nil) (*read-default-float-format* 'double-float) (*read-base* 10))
    (values (read-from-string token))))

;;; The reader

(defstruct (unquote (:constructor make-unquote (form splice))) form splice)

(defvar *bq-depth* 0)

(defparameter +terminators+ '(#\( #\) #\' #\" #\; #\` #\,))

(defparameter +max-depth+ 400)

(defun whitespace-p (c) (member c '(#\Space #\Tab #\Newline #\Return #\Page #\Linefeed)))

(defstruct (rd (:constructor make-rd (text package))) text package (pos 0) (depth 0))

(defun rd-peek (r) (let ((p (rd-pos r)) (s (rd-text r))) (and (< p (length s)) (char s p))))

(defun rd-next (r) (prog1 (rd-peek r) (incf (rd-pos r))))

(defun skip-blank (r)
  (loop
    (let ((c (rd-peek r)))
      (cond ((null c) (return))
            ((whitespace-p c) (rd-next r))
            ((char= c #\;) (loop for d = (rd-next r) until (or (null d) (char= d #\Newline))))
            ((and (char= c #\#) (< (1+ (rd-pos r)) (length (rd-text r)))
                  (char= (char (rd-text r) (1+ (rd-pos r))) #\|))
             (incf (rd-pos r) 2)
             (let ((nest 1))
               (loop while (plusp nest)
                     do (let ((d (rd-next r)))
                          (cond ((null d) (refuse "an unterminated #| comment"))
                                ((and (char= d #\|) (eql (rd-peek r) #\#)) (rd-next r) (decf nest))
                                ((and (char= d #\#) (eql (rd-peek r) #\|)) (rd-next r) (incf nest)))))))
            (t (return))))))

(defun read-token (r)
  (with-output-to-string (out)
    (loop for c = (rd-peek r)
          while (and c (not (whitespace-p c)) (not (member c +terminators+)))
          do (write-char (rd-next r) out))))

(defun read-string-literal (r)
  (with-output-to-string (out)
    (loop
      (let ((c (rd-next r)))
        (cond ((null c) (refuse "an unterminated string"))
              ((char= c #\") (return))
              ((char= c #\\) (let ((d (rd-next r))) (unless d (refuse "an unterminated string")) (write-char d out)))
              (t (write-char c out)))))))

(defun read-form (r)
  "One form, or :eof."
  (skip-blank r)
  (let ((c (rd-peek r)))
    (cond
      ((null c) :eof)
      ((char= c #\)) (refuse "an unmatched close parenthesis"))
      ((char= c #\() (rd-next r) (read-list-tail r #\)))
      ((char= c #\') (rd-next r) (list 'quote (read-required r)))
      ((char= c #\`)
       (rd-next r)
       (when (plusp *bq-depth*) (refuse "a nested backquote"))
       (let ((*bq-depth* 1)) (bq-expand (read-required r))))
      ((char= c #\,)
       (rd-next r)
       (unless (plusp *bq-depth*) (refuse "a comma outside a backquote"))
       (let ((splice (when (member (rd-peek r) '(#\@ #\.)) (rd-next r) t)))
         (let ((*bq-depth* 0)) (make-unquote (read-required r) splice))))
      ((char= c #\") (rd-next r) (read-string-literal r))
      ((char= c #\#) (rd-next r) (read-dispatch r))
      (t (let ((token (read-token r)))
           (if (number-token-p token)
               (parse-number-token token)
               (resolve-symbol token (rd-package r))))))))

(defun read-required (r)
  (let ((form (read-form r)))
    (when (eq form :eof) (refuse "the code ends inside a form"))
    form))

(defun read-list-tail (r close)
  (when (> (incf (rd-depth r)) +max-depth+) (refuse "forms nested deeper than ~D" +max-depth+))
  (let ((items '()) (tail nil))
    (loop
      (skip-blank r)
      (let ((c (rd-peek r)))
        (cond ((null c) (refuse "the code ends inside a list"))
              ((char= c close) (rd-next r) (return))
              ((and (char= c #\.) items
                    (let ((d (and (< (1+ (rd-pos r)) (length (rd-text r))) (char (rd-text r) (1+ (rd-pos r))))))
                      (or (null d) (whitespace-p d) (member d +terminators+))))
               (rd-next r)
               (setf tail (read-required r))
               (skip-blank r)
               (unless (eql (rd-next r) close) (refuse "more than one form after a dot"))
               (return))
              (t (push (read-required r) items)))))
    (decf (rd-depth r))
    (let ((list (nreverse items)))
      (when tail (setf (cdr (last list)) tail))
      list)))

(defun read-dispatch (r)
  (let ((c (rd-next r)))
    (case c
      ((nil) (refuse "the code ends after #"))
      (#\' (let ((f (read-required r)))
             ;; #'(lambda ...) is the lambda itself: SAFE-LAMBDA expands to (function (lambda ...))
             (if (and (consp f) (eq (car f) 'safe-lambda)) f (list 'function f))))
      (#\( (let ((items (read-list-tail r #\))))
             (unless (listp (cdr (last items))) (refuse "a dotted vector"))
             (coerce items 'simple-vector)))
      (#\\ (let* ((first (or (rd-next r) (refuse "the code ends after #\\")))
                  (rest (read-token r)))
             (if (string= rest "")
                 first
                 (or (name-char (concatenate 'string (string first) rest))
                     (refuse "the character name ~A~A" first rest)))))
      ((#\b #\B #\o #\O #\x #\X)
       (let ((token (read-token r)) (radix (ecase (char-downcase c) (#\b 2) (#\o 8) (#\x 16))))
         (or (handler-case (parse-integer token :radix radix) (error () nil))
             (refuse "the number #~A~A" c token))))
      (t (refuse "the reader syntax #~A (only #', #(, #\\, #b, #o, #x and #| |# are read)" c)))))

;;; Backquote, expanded at read time into LIST, APPEND and QUOTE.

(defun bq-expand (x)
  (cond ((unquote-p x)
         (when (unquote-splice x) (refuse ",@ outside a list"))
         (unquote-form x))
        ((consp x) (bq-list x))
        ((and (vectorp x) (not (stringp x))) (refuse "a backquoted vector"))
        ((and (symbolp x) (not (keywordp x)) x) (list 'quote x))
        (t x)))

(defun bq-list (x)
  (let ((parts '()))
    (loop while (and (consp x) (not (unquote-p x)))
          do (let ((e (car x)))
               (if (and (unquote-p e) (unquote-splice e))
                   (push (list 'copy-list (unquote-form e)) parts)
                   (push (list 'list (bq-expand e)) parts)))
             (setf x (cdr x)))
    (let ((tail (cond ((null x) nil)
                      ((unquote-p x) (when (unquote-splice x) (refuse ",@ after a dot")) (unquote-form x))
                      (t (bq-expand x)))))
      (cons 'append (append (nreverse parts) (list tail))))))

;;; Reading a text

(defun safe-read-all (text package)
  "Every form of text read into package by the safe reader, each with its source text:
a list of (form . source). Signals refused on the first form refused; nothing has run."
  (let ((r (make-rd text package)) (out '()) (*read-eval* nil))
    (loop
      (skip-blank r)
      (let ((start (rd-pos r)) (form (read-form r)))
        (when (eq form :eof) (return))
        (check-form form)
        (push (cons form (subseq text start (rd-pos r))) out)))
    (nreverse out)))

(defparameter +defining+ '(defun safe-defun defvar defparameter defconstant))

(defun check-handler-clauses (clauses)
  "A HANDLER-CASE of model code catches only errors (ERROR, which it reads as SAFE-ERROR, a
type that leaves out storage conditions) and :no-error: a clause on t, a compound type or any
other name could keep going after the stack or the heap ran out."
  (loop for clause in clauses
        for type = (if (consp clause) (car clause) clause)
        unless (or (eq type 'safe-error) (eq type :no-error))
          do (refuse "a handler-case clause on ~(~A~): model code catches only error" type)))

(defun check-form (form)
  "The checks after reading: a defining form names a symbol of the model's own package, and
define is only a top-level form (checked by the runner)."
  (let ((seen (make-hash-table :test 'eq)) (depth 0))
    (labels ((walk (x)
               (when (and (consp x) (not (gethash x seen)))
                 (setf (gethash x seen) t)
                 (when (> (incf depth) 100000) (refuse "a form too large to check"))
                 (when (and (eq (car x) 'handler-case) (listp (cdr x)))
                   (check-handler-clauses (cddr x)))
                 (when (and (member (car x) +defining+) (consp (cdr x)))
                   (let ((name (cadr x)))
                     (unless (and (symbolp name) name (not (keywordp name))
                                  (let ((p (symbol-package name)))
                                    (and p (om::has-prefix (package-name p) "OM.LAYER."))))
                       (refuse "~(~A~) of ~S: model code defines only its own names" (car x) name))))
                 (walk (car x))
                 (walk (cdr x)))))
      (walk form))))
