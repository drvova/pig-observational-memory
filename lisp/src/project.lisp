;;;; memory/project/project.go: project knowledge (docs/brain-memory.md, decision 20;
;;;; contracts/encoding.md, "Project records"). Which records are per-repository project records,
;;;; the one symbol a record names, the questions a compaction asks the adapter's HEAD structure
;;;; view (host/structure) and the freshness verdicts its answer gives. Queries and answers are
;;;; decoded JSON objects, as host/structure takes and returns them; contracts/project.jsonl holds
;;;; the cases both engines check. Nothing of the structure is stored.

(in-package "OM.ENGINE")

(defparameter +project-max-records+ 200 "project.MaxRecords.")
(defparameter +project-max-symbols+ 20 "project.MaxSymbols.")
(defparameter +project-max-diffs+ 8 "project.MaxDiffs.")
(defparameter +project-short-sha+ 7 "project.ShortSHA.")

(defun project-record-p (o)
  "project.IsRecord: framed in a repository, and a decision, a convention, or the user's own rule,
correction or stated fact."
  (and (string/= (alist-member (obs-frame o) "repoId") "")
       (let ((kind (obs-kind o)))
         (or (member-str kind '("decision" "convention"))
             (and (member-str kind '("preference" "correction" "fact"))
                  (string= (obs-source o) "user-said"))))))

(defun project-symbol-token (token)
  "symbolToken: token's identifier when it reads as a code symbol, else nil."
  (let* ((call (has-suffix token "()"))
         (name (if call (subseq token 0 (- (length token) 2)) token))
         (code-like call))
    (when (or (string= name "") (> (length name) 120)) (return-from project-symbol-token nil))
    (loop for i from 0 below (length name)
          for c = (char name i)
          do (cond ((char= c #\_) (setf code-like t))
                   ((char<= #\a c #\z)
                    (when (and (< (1+ i) (length name)) (char<= #\A (char name (1+ i)) #\Z)) (setf code-like t)))
                   ((char<= #\A c #\Z))
                   ((char<= #\0 c #\9) (when (= i 0) (return-from project-symbol-token nil)))
                   (t (return-from project-symbol-token nil))))
    (and code-like name)))

(defun project-symbol-in (text)
  "project.SymbolIn: the one code symbol text names in backticks, \"\" for none or more than one."
  (let ((found "") (start 0))
    (loop
      (let ((open (position #\` text :start start)))
        (unless open (return found))
        (let ((end (position #\` text :start (1+ open))))
          (unless end (return found))
          (let ((name (project-symbol-token (subseq text (1+ open) end))))
            (setf start (1+ end))
            (when (and name (string/= name found))
              (when (string/= found "") (return ""))
              (setf found name))))))))

(defun project-symbol (o)
  "project.Symbol: a project record's one symbols key, else the one symbol its text names."
  (cond ((not (project-record-p o)) "")
        ((keys-get (obs-keys o) "symbols")
         (let ((symbols (keys-get (obs-keys o) "symbols"))) (if (= (length symbols) 1) (first symbols) "")))
        (t (project-symbol-in (obs-content o)))))

(defun project-rel-path (p)
  "relPath: a frame's path as a path of HEAD's tree, \"\" for none."
  (if (or (string= p "") (has-prefix p "/") (find #\\ p))
      ""
      (let ((c (path-clean p)))
        (if (or (string= c ".") (string= c "..") (has-prefix c "../")) "" c))))

(defun frame-of (o name) (alist-member (obs-frame o) name))

(defun project-anchors (records repo)
  "anchors: the project records of repo (a decoded host.Repo) with a path or a symbol, the newest
+project-max-records+, in ledger order."
  (let* ((id (str (jget repo "repoId")))
         (out (remove-if-not (lambda (o)
                               (and (string/= id "") (string= (frame-of o "repoId") id) (project-record-p o)
                                    (or (string/= (project-rel-path (frame-of o "path")) "")
                                        (string/= (frame-of o "symbol") ""))))
                             records)))
    (if (> (length out) +project-max-records+) (last out +project-max-records+) out)))

(defun project-query-object (paths symbols blobs diffs)
  "A host.StructureQuery object of the parts given, empty ones left out."
  (let ((q (jobject)))
    (when paths (setf (gethash "paths" q) (coerce paths 'simple-vector)))
    (when symbols (setf (gethash "symbols" q) (coerce symbols 'simple-vector)))
    (when blobs (setf (gethash "blobs" q) (coerce blobs 'simple-vector)))
    (when diffs (setf (gethash "diffs" q) (coerce diffs 'simple-vector)))
    q))

(defun project-query (records repo)
  "project.Query: each anchored project record's path, its symbol, and its path's blob at the commit
it was framed at when it has no body hash; the newest +project-max-symbols+ symbols; nothing twice."
  (let ((paths '()) (symbols '()) (blobs '()) (seen (make-set)))
    (dolist (o (project-anchors records repo))
      (let ((p (project-rel-path (frame-of o "path"))) (sym (frame-of o "symbol")) (at (frame-of o "head")))
        (when (and (string/= p "") (not (gethash (cat "p" (string #\Nul) p) seen)))
          (setf (gethash (cat "p" (string #\Nul) p) seen) t)
          (push-end p paths))
        (let ((key (cat "s" (string #\Nul) p (string #\Nul) sym)))
          (when (and (string/= sym "") (not (gethash key seen)))
            (setf (gethash key seen) t)
            (push-end (jobject "path" p "symbol" sym) symbols)))
        (let ((key (cat "b" (string #\Nul) p (string #\Nul) at)))
          (when (and (string/= p "") (string= (frame-of o "bodyHash") "") (string/= at "")
                     (not (gethash key seen)))
            (setf (gethash key seen) t)
            (push-end (jobject "path" p "at" at) blobs)))))
    (when (> (length symbols) +project-max-symbols+) (setf symbols (last symbols +project-max-symbols+)))
    (project-query-object paths symbols blobs nil)))

(defun project-query-empty-p (q) (zerop (hash-table-count q)))

(defstruct freshness (id "") (kind "") (sha "") (path "") (head "") (blob ""))

(defun project-short (sha) (if (> (length sha) +project-short-sha+) (subseq sha 0 +project-short-sha+) sha))

(defun freshness-text (v)
  "Verdict.Text."
  (if (string= (freshness-kind v) "gone")
      (cat "gone at " (project-short (freshness-sha v)))
      (cat "changed since " (project-short (freshness-sha v)) ", unverified")))

(defun jlist-of (v) (if (and (vectorp v) (not (stringp v))) (coerce v 'list) '()))

(defun project-verdicts (records repo answer)
  "project.Verdicts: records judged against the view's answer, in record order."
  (let ((head (str (jget answer "head"))) (paths (make-set)) (symbols (make-set)) (blobs (make-set)) (out '()))
    (when (string= head "") (return-from project-verdicts '()))
    (dolist (p (jlist-of (jget answer "paths"))) (setf (gethash (str (jget p "path")) paths) p))
    (dolist (s (jlist-of (jget answer "symbols")))
      (setf (gethash (cat (str (jget s "path")) (string #\Nul) (str (jget s "symbol"))) symbols) s))
    (dolist (b (jlist-of (jget answer "blobs")))
      (setf (gethash (cat (str (jget b "path")) (string #\Nul) (str (jget b "at"))) blobs) b))
    (dolist (o (project-anchors records repo) out)
      (let* ((p (project-rel-path (frame-of o "path")))
             (pa (gethash p paths))
             (sym (frame-of o "symbol"))
             (sa (gethash (cat p (string #\Nul) sym) symbols))
             (at (frame-of o "head"))
             (gone (and (string/= p "") pa (jtrue-p (jget pa "known")) (not (jtrue-p (jget pa "exists"))))))
        (when (and (string/= sym "") sa (jtrue-p (jget sa "known")) (not (jtrue-p (jget sa "found"))))
          (setf gone t))
        (if gone
            (push-end (make-freshness :id (obs-id o) :kind "gone" :sha head :path p :head head) out)
            (let ((blob (and pa (str (jget pa "blob")))))
              (when (and (string/= p "") pa (jtrue-p (jget pa "known")) (string/= blob "") (string/= at ""))
                (let ((recorded (frame-of o "bodyHash")))
                  (when (string= recorded "")
                    (unless (string= at head)
                      (let ((b (gethash (cat p (string #\Nul) at) blobs)))
                        (when (and b (jtrue-p (jget b "known"))) (setf recorded (str (jget b "blob")))))))
                  (when (and (string/= recorded "") (string/= recorded blob))
                    (push-end (make-freshness :id (obs-id o) :kind "changed" :sha at :path p :head head :blob blob)
                              out))))))))))

(defun project-texts (verdicts)
  "project.Texts: the verdicts' texts by record id, nil for none."
  (when verdicts
    (let ((out (make-set)))
      (dolist (v verdicts out) (setf (gethash (freshness-id v) out) (freshness-text v))))))

(defun project-stamp-query (records)
  "project.StampQuery: the paths of the new project records with no body hash."
  (let ((paths '()))
    (dolist (o records)
      (let ((p (project-rel-path (frame-of o "path"))))
        (when (and (project-record-p o) (string= (frame-of o "bodyHash") "") (string/= p "") (not (member-str p paths)))
          (push-end p paths))))
    (project-query-object paths nil nil nil)))

(defun set-frame-member (o name value)
  "o's frame with name set to value, in +frame-members+ order."
  (setf (obs-frame o)
        (member-alist +frame-members+ (lambda (n) (if (string= n name) value (alist-member (obs-frame o) n))))))

(defun project-stamp (records answer)
  "project.Stamp: each new project record framed at the answer's commit takes its path's blob there
as its body hash. Returns whether any did."
  (let ((head (str (jget answer "head"))) (blobs (make-set)) (set nil))
    (dolist (p (jlist-of (jget answer "paths")))
      (when (and (jtrue-p (jget p "known")) (jtrue-p (jget p "exists")) (string/= (str (jget p "blob")) ""))
        (setf (gethash (str (jget p "path")) blobs) (str (jget p "blob")))))
    (dolist (o records set)
      (when (and (project-record-p o) (string= (frame-of o "bodyHash") "") (string/= head "")
                 (string= (frame-of o "head") head))
        (let ((blob (gethash (project-rel-path (frame-of o "path")) blobs)))
          (when blob (set-frame-member o "bodyHash" blob) (setf set t)))))))

(defun project-diff-query (verdicts)
  "project.DiffQuery: the diffs of the changed verdicts, at most +project-max-diffs+."
  (let ((diffs '()) (seen '()))
    (dolist (v verdicts)
      (let ((key (cat (freshness-path v) (string #\Nul) (freshness-sha v))))
        (when (and (string= (freshness-kind v) "changed") (not (member-str key seen)) (< (length diffs) +project-max-diffs+))
          (push key seen)
          (push-end (jobject "path" (freshness-path v) "from" (freshness-sha v)) diffs))))
    (project-query-object nil nil nil diffs)))
