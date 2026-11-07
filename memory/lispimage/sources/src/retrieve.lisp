;;;; The search behind the memory tool's reads (docs/protocol.md, "Retrieval"; memory/retrieve):
;;;; the tokenizer contract, the exact key index, integer BM25, two-level reciprocal rank fusion,
;;;; status tags and neighbour merging. contracts/tokenize.jsonl and contracts/retrieval.jsonl are
;;;; Go's output, which this file equals byte for byte (lisp/tests/retrieval.lisp). Every class is
;;;; ASCII; a character at or above code 128 is a word character, and lengths are counted in UTF-8
;;;; bytes, so both engines cut and drop the same tokens.

(in-package "OM.ENGINE")

;;; The tokenizer (retrieve/tokenize.go)

(declaim (inline rt-digit-p rt-upper-p rt-lower-p rt-alnum-p))
(defun rt-digit-p (c) (char<= #\0 c #\9))
(defun rt-upper-p (c) (char<= #\A c #\Z))
(defun rt-lower-p (c) (char<= #\a c #\z))
(defun rt-alnum-p (c) (or (rt-digit-p c) (rt-upper-p c) (rt-lower-p c)))
(defun rt-hex-p (c) (or (rt-digit-p c) (char<= #\a c #\f) (char<= #\A c #\F)))
(defun rt-joiner-p (c) (case c ((#\_ #\- #\. #\/ #\:) t)))
(defun rt-word-p (c) (or (rt-alnum-p c) (rt-joiner-p c) (>= (char-code c) 128)))
(defun rt-edge-p (c) (case c ((#\. #\- #\/ #\:) t)))
(defun rt-leading-edge-p (c) (case c ((#\- #\:) t)))
(defun rt-space-p (c) (member c '(#\Space #\Tab #\Newline #\Return #.(code-char 11) #\Page)))

(defun utf8-length (s)
  "len(s) in Go: the string's UTF-8 bytes."
  (loop for c across s
        for code = (char-code c)
        sum (cond ((< code #x80) 1) ((< code #x800) 2) ((< code #x10000) 3) (t 4))))

(defun lower-ascii (s)
  "lowerASCII: the ASCII letters of s lowercased, nothing else."
  (if (find-if #'rt-upper-p s)
      (map 'string (lambda (c) (if (rt-upper-p c) (char-downcase c) c)) s)
      s))

(defun rt-append-once (list s) (if (member-str s list) list (append list (list s))))

(defun word-core-p (s) (some (lambda (c) (or (rt-alnum-p c) (>= (char-code c) 128))) s))

(defun trim-edges (s)
  (let ((start 0) (end (length s)))
    (loop while (and (< start end) (rt-leading-edge-p (char s start))) do (incf start))
    (loop while (and (> end start) (rt-edge-p (char s (1- end)))) do (decf end))
    (subseq s start end)))

(defun raw-tokens (text)
  "RawTokens: the maximal runs of word characters, trimmed of their edges, in order."
  (let ((out '()) (i 0) (n (length text)))
    (loop while (< i n)
          do (if (not (rt-word-p (char text i)))
                 (incf i)
                 (let ((j i))
                   (loop while (and (< j n) (rt-word-p (char text j))) do (incf j))
                   (let ((tok (trim-edges (subseq text i j))))
                     (when (word-core-p tok) (push tok out)))
                   (setf i j))))
    (nreverse out)))

(defun version-p (s)
  "IsVersion."
  (let ((i 0) (n (length s)))
    (flet ((digits () (let ((start i)) (loop while (and (< i n) (rt-digit-p (char s i))) do (incf i)) (> i start))))
      (when (and (< i n) (char-equal (char s i) #\v)) (incf i))
      (unless (digits) (return-from version-p nil))
      (let ((dotted 0))
        (loop while (and (< (1+ i) n) (char= (char s i) #\.) (rt-digit-p (char s (1+ i))))
              do (incf i) (digits) (incf dotted))
        (when (zerop dotted) (return-from version-p nil)))
      (loop while (< i n)
            do (unless (member (char s i) '(#\- #\.)) (return-from version-p nil))
               (incf i)
               (let ((start i))
                 (loop while (and (< i n) (rt-alnum-p (char s i))) do (incf i))
                 (when (= i start) (return-from version-p nil))))
      t)))

(defun sha-p (s)
  "IsSHA."
  (and (<= 7 (length s) 64) (every #'rt-hex-p s) (some #'rt-digit-p s) (notevery #'rt-digit-p s)))

(defun dot-number-p (s) (and (plusp (length s)) (rt-digit-p (char s 0)) (find #\. s) t))

(defun exact-only-p (s) (or (version-p s) (sha-p s) (dot-number-p s)))

(defparameter +min-term-bytes+ 2)

(defun split-joiners (s)
  (let ((out '()) (start 0))
    (loop for i from 0 to (length s)
          do (when (or (= i (length s)) (rt-joiner-p (char s i)))
               (when (> i start) (push (subseq s start i) out))
               (setf start (1+ i))))
    (nreverse out)))

(defun camel-parts (s)
  "camelParts."
  (let ((out '()) (start 0) (n (length s)))
    (loop for i from 1 below n
          for c = (char s i) and prev = (char s (1- i))
          do (when (and (rt-upper-p c)
                        (or (rt-lower-p prev) (rt-digit-p prev)
                            (and (rt-upper-p prev) (< (1+ i) n) (rt-lower-p (char s (1+ i))))))
               (push (subseq s start i) out)
               (setf start i)))
    (nreverse (cons (subseq s start) out))))

(defun token-terms (tok)
  "TokenTerms: the whole token, its joiner segments, then the camelCase parts of a segment of
more than one, lowercased, each once, none shorter than two bytes; none for an exact-only token."
  (unless (exact-only-p tok)
    (let ((out '()))
      (flet ((add (s)
               (when (>= (utf8-length s) +min-term-bytes+)
                 (setf out (rt-append-once out (lower-ascii s))))))
        (add tok)
        (let ((segments (split-joiners tok)))
          (dolist (s segments) (add s))
          (dolist (s segments)
            (let ((parts (camel-parts s)))
              (when (cdr parts) (dolist (p parts) (add p)))))))
      out)))

(defun text-tokens (text)
  "Tokens: (values terms exact)."
  (let ((terms '()) (exact '()))
    (dolist (tok (raw-tokens text))
      (if (exact-only-p tok)
          (setf exact (rt-append-once exact tok))
          (dolist (term (token-terms tok)) (push term terms))))
    (values (nreverse terms) exact)))

(defparameter +error-markers+ '("error" "Error" "ERROR" "FAIL" "panic" "fatal" "Fatal" "FATAL" "exception" "Exception" "Traceback"))

(defun marked-error-line-p (line) (some (lambda (m) (search m line)) +error-markers+))

(defun cut-location (s)
  "cutLocation: s without one leading \"<name>:<digits>[:<digits>]: \", or nil."
  (let ((i 0) (n (length s)))
    (loop while (and (< i n) (char/= (char s i) #\:) (char/= (char s i) #\Space)) do (incf i))
    (when (or (= i 0) (>= i n) (char/= (char s i) #\:)) (return-from cut-location nil))
    (dotimes (k 2)
      (let ((j (1+ i)))
        (loop while (and (< j n) (rt-digit-p (char s j))) do (incf j))
        (when (or (= j (1+ i)) (>= j n) (char/= (char s j) #\:))
          (if (= k 0) (return-from cut-location nil) (return)))
        (setf i j)))
    (when (or (> (+ i 2) n) (char/= (char s (1+ i)) #\Space) (= (+ i 2) n))
      (return-from cut-location nil))
    (subseq s (+ i 2))))

(defun normalize-line (line)
  "NormalizeLine: white space runs as one space, trimmed, leading locations cut."
  (let ((s (with-output-to-string (b)
             (let ((space nil) (any nil))
               (loop for c across line
                     do (if (rt-space-p c)
                            (setf space any)
                            (progn (when space (write-char #\Space b) (setf space nil))
                                   (write-char c b) (setf any t))))))))
    (loop for rest = (cut-location s) while rest do (setf s rest))
    s))

(defparameter +min-error-line-bytes+ 8)

(defun error-lines (text all)
  "ErrorLines."
  (let ((out '()))
    (dolist (line (split text (string #\Newline)) out)
      (when (or all (marked-error-line-p line))
        (let ((n (normalize-line line)))
          (when (>= (utf8-length n) +min-error-line-bytes+) (setf out (rt-append-once out n))))))))

;;; Tags, documents and exact keys (retrieve/search.go)

(defun tag-of (o superseded retracted contested)
  "TagOf: the record's label (label-of, labels.lisp)."
  (label-of o superseded retracted contested))

(defun tags-of (records)
  "TagsOf: id -> tag over the active records in ledger order, their labels (labels-of)."
  (labels-of records))

(defun withdrawn-tag-p (tag) (member-str tag '("superseded" "retracted")))

(defstruct rdoc
  (id "") (text "") (hay "") (keys '()) (episode "") (source "") (tag "believed")
  ;; The analysis (analyze): term frequencies, length, exact set.
  (tf nil) (dl 0) (exact nil))

(defun record-key-values (o)
  (loop for kind in +key-kinds+ append (keys-get (obs-keys o) kind)))

(defun exact-keys (o content)
  "ExactKeys."
  (let ((out '()))
    (flet ((add (s) (when (string/= s "") (setf out (rt-append-once out s)))))
      (dolist (k (record-key-values o)) (add k))
      (when (string/= (obs-target o) "")
        (add (obs-target o))
        (let ((colon (position #\: (obs-target o))))
          (when (and colon (string= (obs-kind o) "value")) (add (subseq (obs-target o) (1+ colon))))))
      (dolist (tok (raw-tokens content)) (when (exact-only-p tok) (add tok)))
      (dolist (line (error-lines content (string= (obs-kind o) "evidence"))) (add line)))
    out))

(defun doc-of (o content tag &key hay)
  "DocOf; hay, when given, replaces the lowercased text as the substring haystack."
  (let ((text (join (append (list content)
                            (remove "" (list (obs-subject o) (obs-scope o) (obs-tool o) (obs-target o)) :test #'string=)
                            (record-key-values o))
                    (string #\Newline))))
    (make-rdoc :id (obs-id o) :text text :hay (or hay (string-downcase text)) :keys (exact-keys o content)
               :episode (obs-episode o) :source (or (first (obs-source-ids o)) "") :tag tag)))

(defvar +no-table+ (make-hash-table :test 'equal) "An empty table no one writes.")

(defun analyze-doc (d terms probes)
  "analyze: the document's length in terms, and only the counts of the searched terms and the
exact keys among the lookups (hash sets), for one search."
  (let ((tf nil) (exact nil) (dl 0))
    (dolist (tok (raw-tokens (rdoc-text d)))
      (unless (exact-only-p tok)
        (dolist (term (token-terms tok))
          (incf dl)
          (when (gethash term terms)
            (unless tf (setf tf (make-hash-table :test 'equal)))
            (incf (gethash term tf 0))))))
    (dolist (k (rdoc-keys d))
      (when (gethash k probes)
        (unless exact (setf exact (make-hash-table :test 'equal)))
        (setf (gethash k exact) t)))
    (setf (rdoc-tf d) (or tf +no-table+) (rdoc-dl d) dl (rdoc-exact d) (or exact +no-table+))
    d))

(defun lookups (q)
  "Lookups."
  (let ((out '()) (n (normalize-line q)))
    (when (string/= n "") (push n out))
    (dolist (tok (raw-tokens q) out) (setf out (rt-append-once out tok)))))

;;; The integer BM25 and RRF

(defparameter +frac-bits+ 16)
(defparameter +max-tf+ 100)
(defparameter +rrf-k+ 60)
(defparameter +rrf-scale+ (ash 1 40))
(defparameter +neighbours+ 2)

(defun log2-fixed (p q)
  "Log2Fixed: log2(p/q) for integers p > q > 0, 16 fractional bits, floored."
  (let ((n (- (integer-length p) (integer-length q))))
    (when (> (ash q n) p) (decf n))
    (let ((m (floor (ash p 30) (ash q n))) (frac 0))
      (dotimes (i +frac-bits+)
        (setf m (ash (* m m) -30) frac (ash frac 1))
        (when (>= m (ash 1 31)) (setf m (ash m -1) frac (logior frac 1))))
      (logior (ash n +frac-bits+) frac))))

(defun bm25-idf (n df) (log2-fixed (+ (* 2 n) 2) (+ (* 2 df) 1)))

(defun saturation (tf dl n total)
  (let ((tf (min tf +max-tf+)))
    (floor (ash (* tf 44 total) +frac-bits+) (+ (* 20 tf total) (* 6 total) (* 18 dl n)))))

(defun rrf (rank) (floor +rrf-scale+ (+ +rrf-k+ rank)))

;;; Matching

(defstruct rquery words tokens raws lookups)

(defun read-query (q)
  "A searched string: its white-space words, each (lowercased . as written), its raw tokens'
terms and its lookups."
  (let ((raws (raw-tokens q)))
    (make-rquery :words (mapcar (lambda (w) (cons (string-downcase w) w)) (fields q))
                 :raws raws :tokens (mapcar #'token-terms raws) :lookups (lookups q))))

(defun token-holds-p (d raw terms)
  (let ((tf (rdoc-tf d)))
    (cond ((gethash raw (rdoc-exact d)) t)
          ((null terms) nil)
          ((plusp (gethash (first terms) tf 0)) t)
          ((null (rest terms)) nil)
          (t (every (lambda (term) (plusp (gethash term tf 0))) (rest terms))))))

(defun word-holds-p (d word)
  "wordHolds: word (lowercased . as written) as a substring of the hay, or through its tokens."
  (or (and (search (car word) (rdoc-hay d)) t)
      (let ((raws (raw-tokens (cdr word))))
        (and raws
             (every (lambda (raw)
                      (let ((terms (token-terms raw)))
                        (and (or (gethash raw (rdoc-exact d)) terms)
                             (token-holds-p d raw terms))))
                    raws)))))

(defun exact-count (d q) (count-if (lambda (l) (gethash l (rdoc-exact d))) (rquery-lookups q)))

(defun query-matches-p (d q)
  (or (plusp (exact-count d q))
      (and (rquery-words q) (every (lambda (w) (word-holds-p d w)) (rquery-words q)))))

;;; Search

(defun retrieval-search (pool searches)
  "retrieve.Search over pool, a vector of rdocs in ledger order: (values results partial), each
result (hit-index exact-p with-indexes)."
  (let* ((pool (coerce pool 'vector)) (n (length pool))
         (qs (mapcar #'read-query searches))
         (total 0)
         (score (make-array n :initial-element 0))
         (exact (make-array n :initial-element nil)))
    (let ((terms (make-hash-table :test 'equal)) (probes (make-hash-table :test 'equal)))
      (dolist (q searches)
        (dolist (l (lookups q)) (setf (gethash l probes) t))
        (dolist (tok (raw-tokens q)) (dolist (term (token-terms tok)) (setf (gethash term terms) t))))
      (loop for d across pool do (analyze-doc d terms probes) (incf total (rdoc-dl d))))
    (dolist (q qs)
      (let* ((terms (let ((out '())) (dolist (ts (rquery-tokens q) out) (dolist (term ts) (setf out (rt-append-once out term))))))
             (weights (mapcar (lambda (term)
                                (let ((df (count-if (lambda (d) (plusp (gethash term (rdoc-tf d) 0))) pool)))
                                  (if (plusp df) (bm25-idf n df) 0)))
                              terms))
             (exact-list '()) (lex-list '()))
        (loop for i from 0 below n
              for d = (aref pool i)
              do (let ((c (exact-count d q)))
                   (when (plusp c) (push (cons i c) exact-list) (setf (aref exact i) t)))
                 (let ((s 0))
                   (loop for term in terms for w in weights
                         do (let ((tf (gethash term (rdoc-tf d) 0)))
                              (when (plusp tf)
                                (incf s (ash (* w (saturation tf (rdoc-dl d) n total)) (- +frac-bits+))))))
                   (when (plusp s) (push (cons i s) lex-list))))
        (dolist (list (list (nreverse exact-list) (nreverse lex-list)))
          (let ((sorted (stable-sort (copy-list list)
                                     (lambda (a b) (if (/= (cdr a) (cdr b)) (> (cdr a) (cdr b)) (> (car a) (car b)))))))
            (loop for e in sorted for r from 1 do (incf (aref score (car e)) (rrf r)))))))
    (let ((hits (loop for i from 0 below n
                      when (and qs (every (lambda (q) (query-matches-p (aref pool i) q)) qs)) collect i))
          (words (make-hash-table)) (partial nil))
      (unless hits
        (let ((all '()))
          (dolist (q qs)
            (dolist (w (rquery-words q))
              (unless (find (car w) all :key #'car :test #'string=) (setf all (append all (list w))))))
          (loop for i from 0 below n
                for c = (count-if (lambda (w) (word-holds-p (aref pool i) w)) all)
                when (plusp c) do (push i hits) (setf (gethash i words) c))
          (setf hits (nreverse hits) partial (and hits t))))
      (setf hits
            (stable-sort hits
                         (lambda (a b)
                           (let ((wa (gethash a words 0)) (wb (gethash b words 0))
                                 (da (withdrawn-tag-p (rdoc-tag (aref pool a))))
                                 (db (withdrawn-tag-p (rdoc-tag (aref pool b)))))
                             (cond ((/= wa wb) (> wa wb))
                                   ((not (eq (and da t) (and db t))) (and db t))
                                   ((not (eq (aref exact a) (aref exact b))) (aref exact a))
                                   ((/= (aref score a) (aref score b)) (> (aref score a) (aref score b)))
                                   (t (> a b)))))))
      (values (merge-neighbours pool hits exact) partial))))

(defun related-p (a b)
  (or (and (string/= (rdoc-episode a) "") (string= (rdoc-episode a) (rdoc-episode b)))
      (and (string/= (rdoc-source a) "") (string= (rdoc-source a) (rdoc-source b)))))

(defun merge-neighbours (pool hits exact)
  (let ((taken (make-hash-table)) (out '()) (n (length pool)))
    (dolist (h hits (nreverse out))
      (unless (gethash h taken)
        (setf (gethash h taken) t)
        (let ((left '()) (right '()))
          (loop for i downfrom (1- h) to 0
                while (and (<= (- h i) +neighbours+) (not (gethash i taken)) (related-p (aref pool h) (aref pool i)))
                do (push i left))
          (dolist (i left) (setf (gethash i taken) t))
          (loop for i from (1+ h) below n
                while (and (<= (- i h) +neighbours+) (not (gethash i taken)) (related-p (aref pool h) (aref pool i)))
                do (push i right) (setf (gethash i taken) t))
          (push (list h (aref exact h) (append left (nreverse right))) out))))))
