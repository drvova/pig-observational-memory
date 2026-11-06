;;;; memory/ledger subjects.go and aliases.go: subject words and keys, subject paths, word
;;;; containment, the aliases a reflector or level 1 recorded (om.subjects.aliased), the kernel's
;;;; check of them, the canonical subjects every view groups by, the subject a text names, and
;;;; SubjectTableName (render_tables.go). The kernel-owned names a subject path may not spell
;;;; (kernel.OwnsForEveryKind) are listed here from memory/kernel/kernel.go.

(in-package "OM.KERNEL")

(defparameter +subject-path-separator+ "/")
(defparameter +max-subject-path-depth+ 3)
(defparameter +max-subject-aliases-per-entry+ 16)

;;; kernel.OwnsForEveryKind

(defparameter +owner-subjects+ "the kernel's subject canonicalization")

(defun kernel-normalize-name (name)
  "kernel.normalize: lowercased and trimmed, without - _ . : / and spaces."
  (remove-if (lambda (c) (find c "-_.:/ ")) (to-lower (trim-space name))))

(defparameter +kernel-kindless-names+
  ;; The names kernel.go owns for every kind, in the order it adds them, each with its owner;
  ;; a name already owned keeps its first owner (kernel.taken).
  (let ((out '()))
    (flet ((add (names owner)
             (dolist (n names)
               (unless (find (kernel-normalize-name n) out :key #'car :test #'string=)
                 (push-end (cons (kernel-normalize-name n) owner) out)))))
      (add '("memory") "the memory tool")
      (add '("notes" "reflections" "history" "saved" "recall") "the query engine (a source)")
      (add '("search" "where" "since" "until" "from" "through" "sort" "limit" "count" "group") "the query engine (a stage)")
      (add '("save" "edit" "forget" "feedback" "define" "propose" "harness" "rollback") "the query engine (a write)")
      (add '("om.observations.recorded" "om.reflections.recorded" "om.observations.dropped" "om.folded" "om.subjects.aliased")
           "storage (an om.* record kind)")
      (add '("compactAfterTokens" "compactAfterTokensMode" "compactAfterTokensRatio"
             "compactionMaxRetainedTokens" "compactionSummaryMaxTokens" "compactionCatchUpMaxChunks"
             "observationsPoolMaxTokens" "observationsPoolTargetTokens"
             "coverage" "coverage-marker" "cut-point" "fold-boundary" "firstKeptEntryId" "foldThroughEntryId"
             "kernelFloor")
           "the compaction invariants (a setting or boundary)")
      (add '("kernel-consolidate") "the kernel's fixed prompt rules")
      (add '("kernel-observer" "kernel-reflector" "kernel-dropper" "kernel-builder") "the kernel's fixed prompt rules")
      (add '("subject-key" "canonical-subjects" "raw_subject" "subject_scope") +owner-subjects+))
    out))

(defun owns-for-every-kind (name)
  "kernel.OwnsForEveryKind: (values owner ok)."
  (let ((key (kernel-normalize-name name)))
    (if (string= key "")
        (values "" nil)
        (let ((e (find key +kernel-kindless-names+ :key #'car :test #'string=)))
          (if e (values (cdr e) t) (values "" nil))))))

;;; Words and keys

(defun in-word-symbol-p (r) (find r "'+#&"))

(defun subject-words (subject)
  "subjectWords: the lowercase words of a subject."
  (let ((subject (replace-all (to-lower subject) "’" "'")) (out '()))
    (dolist (f (fields-func subject (lambda (r) (and (not (letter-p r)) (not (digit-p r)) (not (in-word-symbol-p r))))))
      (let ((f (trim-suffix (trim f "'") "'s")))
        (when (find-if (lambda (r) (or (letter-p r) (digit-p r))) f)
          (push-end f out))))
    out))

(defun singular-candidates (word)
  "singularCandidates: the words word may be the plural of, most specific first."
  (let ((out '()))
    (flet ((add (c) (when (and (>= (length c) 3) (string/= c word)) (push-end c out))))
      (when (has-suffix word "ies") (add (cat (subseq word 0 (- (length word) 3)) "y")))
      (when (has-suffix word "ses") (add (cat (subseq word 0 (- (length word) 3)) "sis")))
      (when (has-suffix word "es")
        (let ((stem (subseq word 0 (- (length word) 2))))
          (when (some (lambda (end) (has-suffix stem end)) '("s" "x" "z" "ch" "sh")) (add stem))))
      (when (has-suffix word "s")
        (let ((stem (subseq word 0 (1- (length word)))))
          (when (and (not (has-suffix stem "s")) (>= (length stem) 4)) (add stem)))))
    out))

(defun words-with-singulars (text)
  (let ((words (make-set)))
    (dolist (w (subject-words text) words)
      (setf (gethash w words) t)
      (dolist (c (singular-candidates w)) (setf (gethash c words) t)))))

(defun contains-words-p (whole part)
  "ContainsWords: every word of part is a word of whole, either side's plural read as its
singular."
  (let ((words (words-with-singulars whole)))
    (every (lambda (w) (or (gethash w words) (some (lambda (c) (gethash c words)) (singular-candidates w))))
           (subject-words part))))

(defun contains-subject-words-p (whole part)
  "containsSubjectWords."
  (let ((words (words-with-singulars whole)))
    (every (lambda (w) (gethash w words)) (subject-words part))))

(defun subject-key (subject)
  "SubjectKey: a subject's words joined by one space."
  (join (subject-words subject) " "))

(defun normalize-subject-segment (segment) (to-lower (join (fields segment) " ")))

(defun normalize-subject-path (subject)
  "NormalizeSubjectPath."
  (if (not (search +subject-path-separator+ subject))
      (normalize-subject-segment subject)
      (join (remove "" (mapcar #'normalize-subject-segment (split subject +subject-path-separator+)) :test #'string=)
            +subject-path-separator+)))

(defun subject-path (subject)
  "SubjectPath: (values segments ok)."
  (let ((normalized (normalize-subject-path subject)))
    (when (string= normalized "") (return-from subject-path (values '() t)))
    (let ((segments (split normalized +subject-path-separator+)))
      (cond ((= (length segments) 1) (values segments t))
            ((> (length segments) +max-subject-path-depth+) (values segments nil))
            (t (loop for i from 2 to (length segments)
                     do (when (nth-value 1 (owns-for-every-kind (join (subseq segments 0 i) +subject-path-separator+)))
                          (return-from subject-path (values segments nil))))
               (values segments t))))))

(defun recordable-subject (subject)
  "RecordableSubject: (values stored refused)."
  (let ((normalized (normalize-subject-path subject)))
    (multiple-value-bind (segments ok) (subject-path normalized)
      (if ok (values normalized nil) (values (join segments " ") t)))))

(defun join-words (&rest parts)
  (join (remove "" parts :test #'string=) " "))

;;; Containment

(defstruct canonical (subject "") (extra-scope ""))

(defstruct sgroup subject key words count)

(defun containment-subjects (subjects)
  "containmentSubjects: subject -> canonical over the whole set by word containment."
  (let ((out (make-set)) (vocabulary (make-set)) (order '()))
    (dolist (s subjects)
      (unless (or (string= s "") (gethash s out))
        (setf (gethash s out) (make-canonical :subject s))
        (push s order)
        (dolist (w (subject-words s)) (setf (gethash w vocabulary) t))))
    (labels ((match (w)
               (or (find-if (lambda (c) (gethash c vocabulary)) (singular-candidates w)) w))
             (word-set (s)
               (let ((set (make-set)))
                 (dolist (w (subject-words s)) (setf (gethash (match w) set) t))
                 (let ((words (sort (loop for w being the hash-keys of set collect w) #'string<)))
                   (values (join words " ") set (length words))))))
      (let ((by-key (make-set)) (key-of (make-set)))
        (dolist (s order)
          (multiple-value-bind (key set n) (word-set s)
            (unless (string= key "")
              (setf (gethash s key-of) key)
              (let ((g (gethash key by-key)))
                (cond ((null g) (setf (gethash key by-key) (make-sgroup :subject s :key key :words set :count n)))
                      ((string< s (sgroup-subject g)) (setf (sgroup-subject g) s)))))))
        (let ((groups (sort (loop for g being the hash-values of by-key collect g)
                            (lambda (a b) (if (/= (sgroup-count a) (sgroup-count b))
                                              (< (sgroup-count a) (sgroup-count b))
                                              (string< (sgroup-key a) (sgroup-key b))))))
              (target (make-set)))
          (dolist (g groups)
            (setf (gethash (sgroup-key g) target) g)
            (dolist (c groups)
              (when (>= (sgroup-count c) (sgroup-count g)) (return))
              (when (loop for w being the hash-keys of (sgroup-words c) always (gethash w (sgroup-words g)))
                (setf (gethash (sgroup-key g) target) c)
                (return))))
          (maphash (lambda (s key)
                     (let ((tg (gethash key target)) (extra '()))
                       (unless (string= (sgroup-key tg) key)
                         (dolist (w (subject-words s))
                           (unless (gethash (match w) (sgroup-words tg)) (push-end w extra))))
                       (setf (gethash s out) (make-canonical :subject (sgroup-subject tg) :extra-scope (join extra " ")))))
                   key-of))))
    out))

;;; Bases and the alias graph

(defstruct base-set (bases (make-set)) (spelling (make-set)) (contained (make-set)))

(defun subject-base (subject)
  "subjectBase: (values root rest)."
  (multiple-value-bind (segments ok) (subject-path subject)
    (if (or (not ok) (< (length segments) 2))
        (values subject "")
        (values (first segments) (subject-key (join (rest segments) " "))))))

(defun read-bases (subjects)
  (let ((set (make-base-set)) (roots '()))
    (dolist (s subjects)
      (unless (or (string= s "") (nth-value 1 (gethash s (base-set-bases set))))
        (multiple-value-bind (root rest) (subject-base s)
          (setf (gethash s (base-set-bases set)) (cons root rest))
          (push-end root roots)
          (let ((k (subject-key root)))
            (multiple-value-bind (current ok) (gethash k (base-set-spelling set))
              (when (and (string/= k "") (or (not ok) (string< root current)))
                (setf (gethash k (base-set-spelling set)) root)))))))
    (setf (base-set-contained set) (containment-subjects roots))
    set))

(defun base-present-p (set k) (nth-value 1 (gethash k (base-set-spelling set))))

(defun contained-of (set root)
  (or (gethash root (base-set-contained set)) (make-canonical)))

(defun base-parent (set k)
  (multiple-value-bind (root ok) (gethash k (base-set-spelling set))
    (if (not ok)
        k
        (let ((p (subject-key (canonical-subject (contained-of set root)))))
          (if (string/= p "") p k)))))

(defstruct subject-alias (from "") (to "") (reason ""))

(defun subject-alias-equal (a b)
  (and (string= (subject-alias-from a) (subject-alias-from b))
       (string= (subject-alias-to a) (subject-alias-to b))
       (string= (subject-alias-reason a) (subject-alias-reason b))))

(defun alias-retraction-p (a)
  (let ((k (subject-key (subject-alias-from a))))
    (and (string/= k "") (string= k (subject-key (subject-alias-to a))))))

(defstruct alias-graph set (edges (make-set)) (in-force '()))

(defun alias-resolve (g k)
  (loop repeat (+ (* 2 (hash-table-count (alias-graph-edges g))) 2)
        do (multiple-value-bind (next ok) (gethash k (alias-graph-edges g))
             (if ok
                 (setf k next)
                 (let ((p (base-parent (alias-graph-set g) k)))
                   (if (string/= p k) (setf k p) (return))))))
  k)

(defparameter +alias-refused-empty+ "from or to has no letters or digits")
(defparameter +alias-refused-path+ "from and to must be topics, not subject paths")
(defparameter +alias-refused-self+ "from and to are the same subject, and from is not aliased")
(defparameter +alias-refused-kernel+ "names a field of the notes source")
(defparameter +alias-refused-absent+ "from or to is not a subject present in the memory")
(defparameter +alias-refused-repeat+ "the alias is already recorded")
(defparameter +alias-refused-contained+ "to already contains every word of from, so from's notes would read as about to; containment already groups to under from")
(defparameter +alias-refused-cycle+ "would close a cycle of aliases")

(defun alias-refusal (g a)
  (let ((from (subject-key (subject-alias-from a))) (to (subject-key (subject-alias-to a)))
        (edges (alias-graph-edges g)) (set (alias-graph-set g)))
    (cond ((or (string= from "") (string= to "")) +alias-refused-empty+)
          ((or (search +subject-path-separator+ (subject-alias-from a)) (search +subject-path-separator+ (subject-alias-to a)))
           +alias-refused-path+)
          ((some (lambda (side) (multiple-value-bind (owner owned) (owns-for-every-kind side)
                                  (and owned (string= owner +owner-subjects+))))
                 (list (subject-alias-from a) (subject-alias-to a)))
           +alias-refused-kernel+)
          ((or (not (base-present-p set from)) (not (base-present-p set to))) +alias-refused-absent+)
          ((string= from to) (if (nth-value 1 (gethash from edges)) "" +alias-refused-self+))
          ((equal (gethash from edges) to) +alias-refused-repeat+)
          ((contains-subject-words-p (subject-alias-to a) (subject-alias-from a)) +alias-refused-contained+)
          (t (let ((k to))
               (loop repeat (+ (* 2 (hash-table-count edges)) 2)
                     do (when (string= k from) (return-from alias-refusal +alias-refused-cycle+))
                        (multiple-value-bind (next ok) (gethash k edges)
                          (if ok
                              (setf k next)
                              (let ((p (base-parent set k)))
                                (if (string= p k) (return) (setf k p))))))
               "")))))

(defun alias-apply (g a)
  "apply: (values reason replaced ok)."
  (let ((reason (alias-refusal g a)))
    (when (string/= reason "") (return-from alias-apply (values reason nil nil)))
    (let ((from (subject-key (subject-alias-from a))) (replaced nil) (ok nil))
      (when (nth-value 1 (gethash from (alias-graph-edges g)))
        (let ((in (find from (alias-graph-in-force g) :key (lambda (x) (subject-key (subject-alias-from x))) :test #'string=)))
          (when in
            (setf replaced in ok t)
            (setf (alias-graph-in-force g) (remove in (alias-graph-in-force g) :test #'eq :count 1))))
        (remhash from (alias-graph-edges g)))
      (unless (alias-retraction-p a)
        (setf (gethash from (alias-graph-edges g)) (subject-key (subject-alias-to a)))
        (push-end a (alias-graph-in-force g)))
      (values "" replaced ok))))

(defun check-subject-aliases (subjects aliases)
  "CheckSubjectAliases: (values in-force refused), refused a list of (alias . reason)."
  (let ((g (make-alias-graph :set (read-bases subjects))) (refused '()))
    (dolist (a aliases)
      (let ((reason (alias-apply g a)))
        (when (string/= reason "") (push-end (cons a reason) refused))))
    (values (alias-graph-in-force g) refused)))

(defun accept-new-subject-aliases (subjects recorded proposed)
  "AcceptNewSubjectAliases: (values accepted refused)."
  (let ((g (make-alias-graph :set (read-bases subjects))) (accepted '()) (refused '()))
    (dolist (a recorded) (alias-apply g a))
    (dolist (a proposed)
      (multiple-value-bind (reason replaced ok) (alias-apply g a)
        (if (string/= reason "")
            (push-end (cons a reason) refused)
            (progn
              (when ok
                (let ((prev (find replaced accepted :test #'subject-alias-equal)))
                  (when prev (setf accepted (remove prev accepted :test #'eq :count 1)))))
              (push-end a accepted)))))
    (values accepted refused)))

;;; Canonical subjects

(defun canonical-subjects (subjects &optional aliases)
  "CanonicalSubjectsWithAliases: subject -> canonical."
  (let* ((set (read-bases subjects)) (g (make-alias-graph :set set)) (out (make-set)))
    (dolist (a aliases) (alias-apply g a))
    (maphash
     (lambda (s base)
       (destructuring-bind (root . rest) base
         (let* ((k (subject-key root)) (c (contained-of set root)) (aliased "") (carried ""))
           (if (nth-value 1 (gethash k (alias-graph-edges g)))
               (setf aliased root)
               (let ((p (base-parent set k)))
                 (when (and (string/= p k) (nth-value 1 (gethash p (alias-graph-edges g))))
                   (setf aliased (canonical-subject c) carried (canonical-extra-scope c)))))
           (if (string= aliased "")
               (setf (gethash s out) (make-canonical :subject (canonical-subject c)
                                                     :extra-scope (join-words (canonical-extra-scope c) rest)))
               (let* ((target (canonical-subject
                               (contained-of set (or (gethash (alias-resolve g k) (base-set-spelling set)) ""))))
                      (have (make-set)) (kept '()))
                 (dolist (w (subject-words target)) (setf (gethash w have) t))
                 (dolist (w (subject-words aliased)) (unless (gethash w have) (push-end w kept)))
                 (setf (gethash s out) (make-canonical :subject target
                                                       :extra-scope (join-words (join kept " ") carried rest))))))))
     (base-set-bases set))
    out))

(defun subjects-of (observations) (mapcar #'obs-subject observations))

(defun observation-subjects (observations &optional aliases)
  "ObservationSubjectsWithAliases."
  (canonical-subjects (subjects-of observations) aliases))

(defun canonical-of (canonical subject)
  "The canonical a map holds for subject, the zero one when none."
  (or (gethash subject canonical) (make-canonical)))

(defun canonical-scope (scope c)
  "CanonicalScope: scope, then the canonical's extra scope, joined by \"; \"."
  (let ((extra (canonical-extra-scope c)))
    (cond ((string= extra "") scope)
          ((string= scope "") extra)
          (t (cat scope "; " extra)))))

(defun subject-in-text (text subjects &optional aliases)
  "SubjectInText: the canonical subject text names among subjects and the aliases' subjects."
  (let ((text-words (words-with-singulars text)))
    (when (zerop (hash-table-count text-words)) (return-from subject-in-text ""))
    (let ((present (append subjects
                           (loop for a in aliases append (list (subject-alias-from a) (subject-alias-to a)))))
          (best "") (best-words 0))
      (dolist (subject present)
        (let ((words (subject-words subject)))
          (when (and words
                     (every (lambda (w) (or (gethash w text-words)
                                            (some (lambda (c) (gethash c text-words)) (singular-candidates w))))
                            words)
                     (or (> (length words) best-words)
                         (and (= (length words) best-words) (string< subject best))))
            (setf best subject best-words (length words)))))
      (if (string= best "")
          ""
          (let ((c (gethash best (canonical-subjects present aliases))))
            (if (and c (string/= (canonical-subject c) ""))
                (canonical-subject c)
                (normalize-subject best)))))))

(defun subject-table-name (subject)
  "SubjectTableName: notes.<subject as a table name>, \"\" for a subject with no letter or digit."
  (let ((b (make-string-output-stream)) (pending nil) (empty t))
    (loop for r across (to-lower subject)
          do (if (or (letter-p r) (digit-p r) (char= r #\-) (char= r #\.))
                 (progn (when (and pending (not empty)) (write-char #\_ b))
                        (setf pending nil empty nil)
                        (write-char r b))
                 (setf pending t)))
    (let ((name (trim (get-output-stream-string b) "-.")))
      (if (string= name "") "" (cat "notes." name)))))

;;; om.subjects.aliased

(defun subject-alias-from-value (v)
  "subjectAliasFrom: a decoded alias, or nil."
  (when (hash-table-p v)
    (let ((from (normalize-subject (str (jget v "from"))))
          (to (normalize-subject (str (jget v "to"))))
          (reason (join (fields (str (jget v "reason"))) " ")))
      (unless (or (string= from "") (string= to ""))
        (make-subject-alias :from from :to to :reason reason)))))

(defun subject-alias-list (v)
  "subjectAliasList: at most 16 well-formed aliases."
  (let ((out '()) (n 0))
    (when (and (vectorp v) (not (stringp v)))
      (loop for item across v
            until (= n +max-subject-aliases-per-entry+)
            do (let ((a (subject-alias-from-value item)))
                 (when a (push-end a out) (incf n)))))
    out))

(defun subjects-aliased-data (e)
  "SubjectsAliasedEntry: (values aliases observations-through-id) of a valid entry, or nil."
  (when (and (string= (entry-type e) "custom") (string= (entry-custom-type e) "om.subjects.aliased")
             (hash-table-p (entry-data e)))
    (let ((aliases (subject-alias-list (jget (entry-data e) "aliases"))))
      (when aliases
        (values aliases (str (jget (entry-data e) "observationsThroughId")))))))

(defun subject-alias-to-value (a)
  (let ((m (jobject "from" (subject-alias-from a) "to" (subject-alias-to a))))
    (unless (string= (subject-alias-reason a) "") (setf (gethash "reason" m) (subject-alias-reason a)))
    m))
