;;;; The fixed texts' snippet forms in the Lisp memory tool (docs/lisp-port-tool-layer.md, "The
;;;; memory tool in a child"): the placeholders a fixed text names (om.IsPlaceholder), refused
;;;; when code passes one as a value, and the Go pointers the summary, the thinning marks and the
;;;; curated index still print (om.FormatRead's forms, kept byte for byte for the golden corpus),
;;;; which the Lisp tool runs as the Lisp call they stand for.

(in-package "OM.LAYER")

;;; Placeholders (memory/om/snippet.go)

(defparameter +placeholders+
  '(("<id>" . "a record, reflection or entry id from the summary's lines")
    ("<entry id>" . "an entry id from a summary line's source")
    ("<name>" . "a saved memory's name from the index")
    ("<words>" . "the words to search for")
    ("<first id>" . "the span's first entry or record id")
    ("<last id>" . "the span's last entry or record id")
    ("<type>" . "a memory type: user, feedback, project, reference or lesson")
    ("<description>" . "a one-line description")
    ("<body>" . "the memory's text: why it holds and how to apply it")
    ("<outcome>" . "helped or harmed")
    ("<quote>" . "the user's or assistant's words that show it")
    ("<string name>" . "a name from the strings argument")
    ("<definition>" . "a kept definition's name, as kept() lists it"))
  "om.placeholderWhat: each placeholder of the fixed texts and what goes in its place.")

(defun placeholder-p (s) (and (stringp s) (assoc s +placeholders+ :test #'string=) t))

(defun placeholder-error (p)
  "om.PlaceholderError's text."
  (format nil "`~A` is a placeholder from the memory tool's instructions, not a value: replace it with ~A"
          p (cdr (assoc p +placeholders+ :test #'string=))))

(defun form-placeholder (form)
  "The first placeholder string in a read form (its conses walked once, cycles never reached:
the safe reader makes none), or nil."
  (let ((seen (make-hash-table :test 'eq)))
    (labels ((walk (x)
               (cond ((placeholder-p x) (return-from form-placeholder x))
                     ((and (consp x) (not (gethash x seen)))
                      (setf (gethash x seen) t)
                      (walk (car x)) (walk (cdr x)))
                     ((and (vectorp x) (not (stringp x))) (map nil #'walk x)))))
      (walk form)
      nil)))

;;; Go snippets (om.FormatRead) as Lisp calls

(define-condition snippet-error (error) ((why :initarg :why :reader snippet-why))
  (:report (lambda (c s) (write-string (snippet-why c) s))))

(defun snippet-fail (fmt &rest args) (error 'snippet-error :why (apply #'format nil fmt args)))

(defun go-snippet-p (code)
  "Whether code reads as a Go pointer the fixed texts print: it starts with om."
  (om::has-prefix (string-trim '(#\Space #\Tab #\Newline #\Return) code) "om."))

(defun parse-go-snippet (code)
  "Parses one om.Fn(args) call with literal options (om.CheckSnippet's shape) into (fn args
opts): args the literal arguments, opts a list of (option literal...)."
  (let* ((s (string-trim '(#\Space #\Tab #\Newline #\Return) code)) (pos 0) (n (length s)))
    (labels ((peek () (and (< pos n) (char s pos)))
             (ws () (loop while (and (< pos n) (member (char s pos) '(#\Space #\Tab))) do (incf pos)))
             (expect (str)
               (ws)
               (unless (and (<= (+ pos (length str)) n) (string= str s :start2 pos :end2 (+ pos (length str))))
                 (snippet-fail "expected ~A at ~D" str pos))
               (incf pos (length str)))
             (ident ()
               (ws)
               (let ((start pos))
                 (loop while (and (< pos n) (or (alphanumericp (char s pos)) (char= (char s pos) #\_))) do (incf pos))
                 (when (= start pos) (snippet-fail "expected a name at ~D" pos))
                 (subseq s start pos)))
             (raw-string ()
               (incf pos)
               (let ((end (position #\` s :start pos)))
                 (unless end (snippet-fail "an unterminated raw string"))
                 (prog1 (subseq s pos end) (setf pos (1+ end)))))
             (quoted-string ()
               (incf pos)
               (with-output-to-string (out)
                 (loop
                   (when (>= pos n) (snippet-fail "an unterminated string"))
                   (let ((c (char s pos)))
                     (incf pos)
                     (cond ((char= c #\") (return))
                           ((char= c #\\)
                            (when (>= pos n) (snippet-fail "an unterminated string"))
                            (let ((e (char s pos)))
                              (incf pos)
                              (case e
                                (#\n (write-char #\Newline out))
                                (#\t (write-char #\Tab out))
                                (#\r (write-char #\Return out))
                                ((#\\ #\" #\') (write-char e out))
                                (t (snippet-fail "the escape \\~A" e)))))
                           (t (write-char c out)))))))
             (literal ()
               (ws)
               (let ((c (peek)))
                 (cond ((null c) (snippet-fail "an unexpected end"))
                       ((char= c #\`) (raw-string))
                       ((char= c #\") (quoted-string))
                       ((or (digit-char-p c) (char= c #\-))
                        (let ((start pos))
                          (incf pos)
                          (loop while (and (< pos n) (digit-char-p (char s pos))) do (incf pos))
                          (or (parse-integer s :start start :end pos :junk-allowed t) (snippet-fail "a bad number"))))
                       ((alpha-char-p c)
                        (let ((word (ident)))
                          (cond ((string= word "true") t) ((string= word "false") :false)
                                (t (snippet-fail "~A is not a literal" word)))))
                       (t (snippet-fail "~A is not a literal" c)))))
             (literals ()
               (expect "(")
               (ws)
               (if (eql (peek) #\))
                   (progn (incf pos) '())
                   (let ((out (list (literal))))
                     (loop (ws)
                           (case (peek)
                             (#\, (incf pos) (push (literal) out))
                             (#\) (incf pos) (return (nreverse out)))
                             (t (snippet-fail "expected , or ) at ~D" pos)))))))
             (chain ()
               (expect "om.")
               (let ((opts (list (cons (ident) (literals)))))
                 (loop (ws)
                       (if (and (eql (peek) #\.) (< (1+ pos) n) (alpha-char-p (char s (1+ pos))))
                           (progn (incf pos) (push (cons (ident) (literals)) opts))
                           (return (nreverse opts)))))))
      (expect "om.")
      (let ((fn (ident)) (args '()) (opts '()))
        (expect "(")
        (ws)
        (unless (eql (peek) #\))
          (loop
            (ws)
            (if (and (<= (+ pos 3) n) (string= "om." s :start2 pos :end2 (+ pos 3)))
                (setf opts (chain))
                (push (literal) args))
            (ws)
            (case (peek)
              (#\, (incf pos))
              (#\) (return))
              (t (snippet-fail "expected , or ) at ~D" pos)))))
        (expect ")")
        (ws)
        (when (< pos n)
          (snippet-fail "~A after the call: a snippet is one read call" (subseq s pos)))
        (list fn (nreverse args) opts)))))

(defparameter +snippet-reads+
  '(("Notes" "notes" nil) ("Files" "notes" "file") ("Commits" "notes" "commit")
    ("Attempts" "notes" "attempt") ("History" "history" nil) ("Saved" "saved" nil)
    ("Recall" "recall" nil))
  "The om reads a Lisp call stands for: (Go name, Lisp function, the kind it keeps).")

(defparameter +snippet-keys+
  '(("notes" "Search" "Kind" "Where" "Subject" "WithStatus" "From" "Through" "Since" "Until" "Limit")
    ("history" "Search" "Limit")
    ("saved" "Search")
    ("recall" "Offset"))
  "The options each Lisp read takes, by their om option names.")

(defun lisp-literal (v) (if (stringp v) (prin1-to-string v) (princ-to-string v)))

(defun go-snippet-lisp (code)
  "The Lisp form text a Go pointer (om.FormatRead's shape) stands for, or snippet-error with
what has no Lisp form yet."
  (destructuring-bind (fn args opts) (parse-go-snippet code)
    (let ((read (assoc fn +snippet-reads+ :test #'string=)))
      (unless read
        (snippet-fail "om.~A has no Lisp form in the image yet (the Lisp reads are ~{~A~^, ~})"
                      fn (mapcar #'second (remove-duplicates +snippet-reads+ :key #'second :test #'string=))))
      (destructuring-bind (go-name lisp kind) read
        (declare (ignore go-name))
        (let ((allowed (cdr (assoc lisp +snippet-keys+ :test #'string=)))
              (searches '()) (parts '()))
          (cond ((string= lisp "recall")
                 (unless (and (= (length args) 1) (stringp (first args)))
                   (snippet-fail "om.Recall takes one id"))
                 (push (lisp-literal (first args)) parts))
                (args (snippet-fail "om.~A takes only options" fn)))
          (when kind (push (format nil ":kind ~A" (lisp-literal kind)) parts))
          (dolist (o opts)
            (destructuring-bind (name . vals) o
              (unless (member name allowed :test #'string=)
                (snippet-fail "the option om.~A of om.~A has no Lisp form yet" name fn))
              (flet ((one () (unless (= (length vals) 1) (snippet-fail "om.~A takes one value" name)) (first vals)))
                (cond ((string= name "Search") (push (one) searches))
                      ((string= name "Where")
                       (unless (and (= (length vals) 2) (equal (first vals) "kind"))
                         (snippet-fail "om.Where has a Lisp form only for the kind field"))
                       (push (format nil ":kind ~A" (lisp-literal (second vals))) parts))
                      (t (push (format nil ":~A ~A"
                                       (cdr (assoc name '(("Kind" . "kind") ("Subject" . "subject") ("WithStatus" . "status")
                                                          ("From" . "from") ("Through" . "through") ("Since" . "since")
                                                          ("Until" . "until") ("Limit" . "limit") ("Offset" . "offset"))
                                                   :test #'string=))
                                       (lisp-literal (one)))
                               parts))))))
          (when searches
            (push (if (rest searches)
                      (format nil ":search (list ~{~A~^ ~})" (mapcar #'lisp-literal (reverse searches)))
                      (format nil ":search ~A" (lisp-literal (first searches))))
                  parts))
          (format nil "(~A~{ ~A~})" lisp (reverse parts)))))))
