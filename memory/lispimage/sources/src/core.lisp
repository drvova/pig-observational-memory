;;;; The shared core log (docs/brain-memory.md, phase 6; memory/core): one append-only JSON-lines
;;;; file, <store>/core/log.jsonl, written by one writer at a time under <store>/core/.lock, each
;;;; line carrying a generation; the consumed-through markers beside it; the promotion gate; the
;;;; start-context block and the status lines. Byte for byte what memory/core writes and prints
;;;; (contracts/core.jsonl, lisp/tests/core.lisp).

(in-package "OM.LAYER")

;;; Words, keys and ids

(defun core-space-p (c) (member c '(#\Space #\Tab #\Newline #\Return #\Page #.(code-char 11))))

(defun core-fields (s) (om::fields-func s #'core-space-p))

(defun core-one-line (s) (om::join (core-fields s) " "))

(defun lower-ascii (s)
  (map 'string (lambda (c) (if (char<= #\A c #\Z) (char-downcase c) c)) s))

(defun norm-key (s) (lower-ascii (core-one-line s)))

(defun sha256-hex (s) (om::sha256-octets (om::utf8-octets s)))

(defparameter +nul+ (string (code-char 0)))

(defun item-id (kind scope key text)
  "core.ItemID."
  (subseq (sha256-hex (om::cat "core" +nul+ kind +nul+ scope +nul+ key +nul+ text)) 0 12))

(defparameter +scope-user+ "user")
(defparameter +scope-session+ "session")
(defparameter +scope-global+ "global")
(defparameter +scope-project-prefix+ "project:")

(defun project-scope (repo-id root)
  "core.ProjectScope."
  (cond ((string/= repo-id "") (om::cat +scope-project-prefix+ repo-id))
        ((string/= root "") (om::cat +scope-project-prefix+ root))
        (t "")))

(defun scope-rank (s)
  (cond ((string= s +scope-global+) 0)
        ((string= s +scope-user+) 1)
        ((om::has-prefix s +scope-project-prefix+) 2)
        ((string= s +scope-session+) 3)
        (t -1)))

(defun intersect-scopes (scopes)
  "core.Intersect."
  (let ((out ""))
    (loop for s in scopes for i from 0
          do (when (string= s "") (return-from intersect-scopes ""))
             (if (zerop i)
                 (setf out s)
                 (let ((ra (scope-rank out)) (rb (scope-rank s)))
                   (setf out (cond ((or (minusp ra) (minusp rb)) "")
                                   ((and (= ra rb 2)) (if (string= out s) out ""))
                                   ((>= ra rb) out)
                                   (t s)))
                   (when (string= out "") (return-from intersect-scopes "")))))
    out))

(defun scope-visible-p (scope reader-scopes)
  "core.Visible."
  (cond ((or (string= scope +scope-user+) (string= scope +scope-global+)) t)
        ((om::has-prefix scope +scope-project-prefix+) (and (member scope reader-scopes :test #'string=) t))
        (t nil)))

;;; Evidence roots and paths

(defun evidence-root (project path quote)
  "core.EvidenceRoot."
  (let ((path (om::trim-prefix (core-one-line path) "./")))
    (when (and (string/= project "") (om::has-prefix path (om::cat project "/")))
      (setf path (subseq path (1+ (length project)))))
    (if (string/= path "")
        (sha256-hex (om::cat "file" +nul+ project +nul+ path))
        (sha256-hex (om::cat "text" +nul+ (norm-key quote))))))

(defun project-root-digest (project) (sha256-hex (om::cat "project" +nul+ project)))

(defun user-root (entry-id text) (sha256-hex (om::cat "user" +nul+ entry-id +nul+ (norm-key text))))

(defparameter +read-commands+ '("cat" "head" "tail" "nl" "less" "more" "bat" "sed" "grep" "rg" "awk"))

(defun looks-like-path-p (w) (and (find-if (lambda (c) (find c "./")) w) t))

(defun command-path (command)
  "core.CommandPath."
  (let ((words (core-fields command)))
    (when (or (< (length words) 2) (not (member (first words) +read-commands+ :test #'string=)))
      (return-from command-path ""))
    (loop for w in (reverse (rest words))
          for word = (om::trim w "\"'")
          do (cond ((or (string= word "") (om::has-prefix word "-")))
                   ((looks-like-path-p word) (return-from command-path word))
                   (t (return-from command-path ""))))
    ""))

(defun grep-path (text quote)
  "core.GrepPath."
  (let ((needle (norm-key quote)))
    (dolist (line (om::split text (string #\Newline)) "")
      (when (search needle (norm-key line))
        (let* ((n (length line))
               (i (or (position-if (lambda (c) (or (char= c #\:) (core-space-p c))) line) n)))
          (when (or (zerop i) (>= i n) (char/= (char line i) #\:)) (return ""))
          (let* ((path (subseq line 0 i))
                 (j (or (position-if-not #'digit-char-p line :start (1+ i)) n)))
            (when (or (= j (1+ i)) (>= j n) (not (find (char line j) ":-")) (not (looks-like-path-p path)))
              (return ""))
            (return path)))))))

;;; The per-model hint

(defun model-family (model)
  "core.ModelFamily."
  (let* ((id (lower-ascii (subseq model (1+ (or (position #\/ model :from-end t) -1)))))
         (i (or (position-if-not (lambda (c) (char<= #\a c #\z)) id) (length id))))
    (if (zerop i) id (subseq id 0 i))))

(defun model-hint (writer reader)
  "core.Hint."
  (let ((w (model-family writer)))
    (if (or (string= w "") (string= w (model-family reader))) "" (om::cat "written by a " w " model"))))

;;; Lessons stripped of their values

(defparameter +core-value+ "<value>")

(defun value-word-p (w)
  (loop for i from 0 below (length w)
        for c = (char w i)
        thereis (or (digit-char-p c) (find c "_/\\@=:.#$%")
                    (and (> i 0) (char<= #\A c #\Z) (char<= #\a (char w (1- i)) #\z)))))

(defun strip-values (text)
  "core.StripValues."
  (let ((spans (with-output-to-string (out)
                 (loop
                   (let ((i (position #\` text)))
                     (unless i (return))
                     (let ((j (position #\` text :start (1+ i))))
                       (when (or (null j) (find #\Newline text :start (1+ i) :end j)) (return))
                       (write-string text out :end i)
                       (write-string +core-value+ out)
                       (setf text (subseq text (1+ j))))))
                 (write-string text out)))
        (out '()))
    (dolist (w (core-fields spans))
      (let* ((lead (or (position-if-not (lambda (c) (find c "([{\"'")) w) (length w)))
             (trail (let ((p (position-if-not (lambda (c) (find c ".,;:!?)]}\"'")) w :from-end t :start lead)))
                      (if p (1+ p) lead)))
             (core (subseq w lead trail)))
        (when (and (string/= core +core-value+) (string/= core "") (value-word-p core))
          (setf w (om::cat (subseq w 0 lead) +core-value+ (subseq w trail))))
        (unless (and (string= w +core-value+) out (string= (first out) +core-value+))
          (push w out))))
    (om::join (nreverse out) " ")))

;;; Lines, items and the fold

(defstruct cline
  (gen 0) (op "") (id "") (of "") (kind "") (key "") (text "") (scope "") (via "")
  (roots '()) (record "") (tombstones '()) (base 0) (writer-model "") (writer-session "") (time ""))

(defstruct citem
  (id "") (kind "") (key "") (text "") (scope "") (via "") (record "") (roots '())
  (writer-model "") (writer-session "") (born 0) (gen 0) (status "") (with '()) (replaced-by ""))

(defstruct cstate (gen 0) (next 1) (items '()) (by-id (make-hash-table :test 'equal))
  (tombstoned (make-hash-table :test 'equal)) (torn 0))

(defparameter +core-ops+ '("promote" "supersede" "contest" "forget" "scope"))

(defun citem-live-p (it) (member (citem-status it) '("believed" "contested") :test #'string=))

(defun core-forgotten-p (st id) (nth-value 1 (gethash id (cstate-tombstoned st))))

(defun core-item (st id) (gethash id (cstate-by-id st)))

(defun live-by-key (st kind scope key)
  (remove-if-not (lambda (it) (and (citem-live-p it) (string= (citem-kind it) kind) (string= (citem-scope it) scope)
                                   (string= (citem-key it) key)))
                 (cstate-items st)))

(defun json-strings (v)
  (if (and (vectorp v) (not (stringp v))) (loop for x across v collect (om::str x)) '()))

(defun line-from-json (m)
  "A decoded line as Go's json.Unmarshal into core.Line reads it, nil when it would not decode or
is no line (generation 0 or less, an unknown op)."
  (flet ((s (k) (let ((v (om::jget m k))) (cond ((or (null v) (eq v :null)) "") ((stringp v) v) (t (return-from line-from-json nil)))))
         (n (k) (let ((v (om::jget m k)))
                  (cond ((or (null v) (eq v :null)) 0)
                        ((and (realp v) (= v (ftruncate v))) (truncate v))
                        (t (return-from line-from-json nil)))))
         (l (k) (let ((v (om::jget m k)))
                  (cond ((or (null v) (eq v :null)) '())
                        ((and (vectorp v) (not (stringp v)) (every #'stringp v)) (coerce v 'list))
                        (t (return-from line-from-json nil))))))
    (unless (hash-table-p m) (return-from line-from-json nil))
    (let* ((w (om::jget m "writer"))
           (line (make-cline :gen (n "gen") :op (s "op") :id (s "id") :of (s "of") :kind (s "kind") :key (s "key")
                             :text (s "text") :scope (s "scope") :via (s "via") :roots (l "roots") :record (s "record")
                             :tombstones (l "tombstones") :base (n "base") :time (s "time"))))
      (cond ((or (null w) (eq w :null)))
            ((hash-table-p w)
             (let ((wm (om::jget w "model")) (ws (om::jget w "session")))
               (unless (and (or (null wm) (eq wm :null) (stringp wm)) (or (null ws) (eq ws :null) (stringp ws)))
                 (return-from line-from-json nil))
               (setf (cline-writer-model line) (om::str wm) (cline-writer-session line) (om::str ws))))
            (t (return-from line-from-json nil)))
      (when (and (> (cline-gen line) 0) (member (cline-op line) +core-ops+ :test #'string=))
        line))))

(defun core-torn-gen (raw)
  (if (om::has-prefix raw "{\"gen\":")
      (let ((end (or (position-if-not #'digit-char-p raw :start 7) (length raw))))
        (if (and (> end 7) (<= (- end 7) 12)) (parse-integer raw :start 7 :end end) 0))
      0))

(defun core-fold (text through)
  "core.Fold."
  (let ((st (make-cstate)) (lines 0) (highest 0))
    (dolist (raw (om::split text (string #\Newline)))
      (unless (string= (om::trim-space raw) "")
        (incf lines)
        (let ((line (handler-case (line-from-json (om::json-parse raw)) (error () nil))))
          (cond ((null line)
                 (incf (cstate-torn st))
                 (setf highest (max highest (core-torn-gen raw))))
                (t (setf highest (max highest (cline-gen line)))
                   (when (or (minusp through) (<= (cline-gen line) through))
                     (core-apply st line)))))))
    (setf (cstate-next st) (1+ (max highest lines)))
    st))

(defun uncontest (st it)
  "Takes it out of the pairs it stands in: a partner left alone is believed again."
  (dolist (w (citem-with it))
    (let ((other (core-item st w)))
      (when (and other (string= (citem-status other) "contested"))
        (setf (citem-with other) (remove (citem-id it) (citem-with other) :test #'string=))
        (unless (citem-with other) (setf (citem-status other) "believed")))))
  (setf (citem-with it) '()))

(defun core-apply (st line)
  (setf (cstate-gen st) (max (cstate-gen st) (cline-gen line)))
  (flet ((add (status)
           (when (or (core-forgotten-p st (cline-id line))
                     (and (string/= (cline-record line) "") (core-forgotten-p st (cline-record line))))
             (return-from add nil))
           (let ((it (core-item st (cline-id line))))
             (if it
                 (progn
                   (dolist (r (cline-roots line))
                     (unless (member r (citem-roots it) :test #'string=)
                       (setf (citem-roots it) (append (citem-roots it) (list r)))))
                   (setf (citem-gen it) (cline-gen line))
                   it)
                 (let ((it (make-citem :id (cline-id line) :kind (cline-kind line) :key (cline-key line) :text (cline-text line)
                                       :scope (cline-scope line) :via (cline-via line) :record (cline-record line)
                                       :roots (copy-list (cline-roots line)) :writer-model (cline-writer-model line)
                                       :writer-session (cline-writer-session line) :born (cline-gen line)
                                       :gen (cline-gen line) :status status)))
                   (setf (cstate-items st) (append (cstate-items st) (list it))
                         (gethash (citem-id it) (cstate-by-id st)) it)
                   it)))))
    (let ((op (cline-op line)))
      (cond
        ((string= op "promote") (add "believed"))
        ((string= op "supersede")
         (let ((old (core-item st (cline-of line))) (it (add "believed")))
           (when (and it old (not (eq old it)))
             (setf (citem-status old) "superseded" (citem-replaced-by old) (citem-id it) (citem-gen old) (cline-gen line))
             (uncontest st old))))
        ((string= op "contest")
         (let ((old (core-item st (cline-of line))) (it (add "contested")))
           (when (and it old (not (eq old it)) (citem-live-p old))
             (setf (citem-status old) "contested" (citem-gen old) (cline-gen line))
             (unless (member (citem-id it) (citem-with old) :test #'string=)
               (setf (citem-with old) (append (citem-with old) (list (citem-id it)))))
             (unless (member (citem-id old) (citem-with it) :test #'string=)
               (setf (citem-with it) (append (citem-with it) (list (citem-id old)))))
             (setf (citem-status it) "contested"))))
        ((string= op "forget")
         (dolist (id (cons (cline-id line) (cline-tombstones line)))
           (when (and (string/= id "") (not (core-forgotten-p st id)))
             (setf (gethash id (cstate-tombstoned st)) (cline-gen line))))
         (dolist (it (cstate-items st))
           (when (or (core-forgotten-p st (citem-id it))
                     (and (string/= (citem-record it) "") (core-forgotten-p st (citem-record it))))
             (setf (citem-status it) "forgotten" (citem-text it) "" (citem-gen it) (cline-gen line))
             (unless (core-forgotten-p st (citem-id it))
               (setf (gethash (citem-id it) (cstate-tombstoned st)) (cline-gen line)))
             (uncontest st it))))
        ((string= op "scope")
         (let ((it (core-item st (cline-id line))))
           (when (and it (string/= (cline-scope line) ""))
             (setf (citem-scope it) (cline-scope line) (citem-gen it) (cline-gen line)))))))))

(defun encode-cline (line)
  "core.Encode: encoding/json of core.Line, its text and key redacted."
  (with-output-to-string (out)
    (let ((first t))
      (flet ((key (k) (unless first (write-char #\, out)) (setf first nil)
               (om::write-json-string k out) (write-char #\: out))
             (strs (v) (write-char #\[ out)
               (loop for (x . rest) on v do (om::write-json-string x out) (when rest (write-char #\, out)))
               (write-char #\] out)))
        (write-char #\{ out)
        (key "gen") (format out "~D" (cline-gen line))
        (key "op") (om::write-json-string (cline-op line) out)
        (key "id") (om::write-json-string (cline-id line) out)
        (loop for (k v) in `(("of" ,(cline-of line)) ("kind" ,(cline-kind line)) ("key" ,(om::redact-secrets (cline-key line)))
                             ("text" ,(om::redact-secrets (cline-text line))) ("scope" ,(cline-scope line)) ("via" ,(cline-via line)))
              when (string/= v "") do (key k) (om::write-json-string v out))
        (when (cline-roots line) (key "roots") (strs (cline-roots line)))
        (when (string/= (cline-record line) "") (key "record") (om::write-json-string (cline-record line) out))
        (when (cline-tombstones line) (key "tombstones") (strs (cline-tombstones line)))
        (key "base") (format out "~D" (cline-base line))
        (when (or (string/= (cline-writer-model line) "") (string/= (cline-writer-session line) ""))
          (key "writer") (write-char #\{ out)
          (let ((wfirst t))
            (loop for (k v) in `(("model" ,(cline-writer-model line)) ("session" ,(cline-writer-session line)))
                  when (string/= v "") do (unless wfirst (write-char #\, out)) (setf wfirst nil)
                                          (om::write-json-string k out) (write-char #\: out) (om::write-json-string v out)))
          (write-char #\} out))
        (key "time") (om::write-json-string (cline-time line) out)
        (write-char #\} out)))))

;;; The log on disk

(defun core-dir (store-dir) (join-path store-dir "core"))
(defun core-log-path (core-dir) (join-path core-dir "log.jsonl"))
(defun core-consumed-path (core-dir) (join-path core-dir "consumed.jsonl"))

(defun core-read (core-dir &optional (through -1))
  "Log.ReadThrough: the fold through a generation (every line when negative)."
  (core-fold (or (read-file (core-log-path core-dir)) "") through))

(defun core-commit (core-dir decide)
  "Log.Commit: decide is called with the state and an emit function, under the one-writer lock;
each emitted line is numbered, stamped, appended and folded at once. The lines written."
  (let ((out '()))
    (with-store-lock (core-dir)
      (let ((st (core-read core-dir)))
        (funcall decide st
                 (lambda (line)
                   (let ((line (copy-cline line)))
                     (setf (cline-gen line) (cstate-next st)
                           (cline-time line) (format-rfc3339 (now)))
                     (append-line (core-log-path core-dir) (encode-cline line))
                     (setf (cline-text line) (om::redact-secrets (cline-text line))
                           (cline-key line) (om::redact-secrets (cline-key line)))
                     (core-apply st line)
                     (incf (cstate-next st))
                     (push line out))))))
    (nreverse out)))

(defun core-consumed (core-dir session)
  "Log.Consumed: (values through ok)."
  (let ((through 0) (ok nil) (text (read-file (core-consumed-path core-dir))))
    (when text
      (dolist (raw (om::split text (string #\Newline)))
        (let ((m (handler-case (om::json-parse raw) (error () nil))))
          (when (hash-table-p m)
            (let ((s (om::jget m "session")) (n (om::jget m "through")) (time (om::jget m "time")))
              (when (and (stringp s) (string/= s "") (string= s session)
                         (or (null n) (eq n :null) (and (realp n) (= n (ftruncate n))))
                         (or (null time) (eq time :null) (stringp time)))
                (setf through (if (realp n) (truncate n) 0) ok t)))))))
    (values through ok)))

(defun core-pull (core-dir session)
  "Log.Pull: the state through the newest generation, the session's marker moved to it."
  (let ((st nil))
    (with-store-lock (core-dir)
      (setf st (core-read core-dir))
      (when (string/= session "")
        (multiple-value-bind (through ok) (core-consumed core-dir session)
          (unless (and ok (= through (cstate-gen st)))
            (append-line (core-consumed-path core-dir)
                         (with-output-to-string (out)
                           (write-string "{\"session\":" out) (om::write-json-string session out)
                           (format out ",\"through\":~D,\"time\":" (cstate-gen st))
                           (om::write-json-string (format-rfc3339 (now)) out)
                           (write-char #\} out)))))))
    st))

(defun core-exists-p (core-dir) (file-exists-p (core-log-path core-dir)))

;;; The gate

(defstruct ccand (kind "") (key "") (text "") (scope "") (via "") (roots '()) (tool-only nil) (one-time nil)
  (record "") (writer-model "") (writer-session "") (base 0))

(defun unique-roots (roots)
  (let ((out '()))
    (dolist (r roots (nreverse out))
      (unless (or (string= r "") (member r out :test #'string=)) (push r out)))))

(defun core-decide (st c)
  "core.Decide: (values line-or-nil verdict)."
  (cond ((ccand-tool-only c) (return-from core-decide (values nil "refused: tool output never promotes")))
        ((ccand-one-time c) (return-from core-decide (values nil "refused: a one-time permission is never a standing rule")))
        ((or (string= (ccand-scope c) "") (string= (ccand-scope c) +scope-session+))
         (return-from core-decide (values nil "refused: no shared scope"))))
  (let* ((key (let ((k (norm-key (ccand-key c)))) (if (string= k "") (norm-key (ccand-text c)) k)))
         (id (item-id (ccand-kind c) (ccand-scope c) key (ccand-text c)))
         (roots (unique-roots (ccand-roots c))))
    (when (or (core-forgotten-p st id) (and (string/= (ccand-record c) "") (core-forgotten-p st (ccand-record c))))
      (return-from core-decide (values nil "refused: forgotten")))
    (let ((line (make-cline :id id :kind (ccand-kind c) :key key :text (ccand-text c) :scope (ccand-scope c) :roots roots
                            :record (ccand-record c) :base (ccand-base c) :writer-model (ccand-writer-model c)
                            :writer-session (ccand-writer-session c)))
          (it (core-item st id)))
      (when (and it (citem-live-p it))
        (let ((added (remove-if (lambda (r) (member r (citem-roots it) :test #'string=)) roots)))
          (unless added (return-from core-decide (values nil "same")))
          (setf (cline-op line) "promote" (cline-via line) "fast" (cline-roots line) added (cline-record line) "")
          (return-from core-decide (values line "fast"))))
      (let ((via (ccand-via c)))
        (when (and (string= via "") (>= (length roots) 2)) (setf via "roots"))
        (when (string= via "") (return-from core-decide (values nil "pending")))
        (setf (cline-via line) via)
        (let ((live (live-by-key st (ccand-kind c) (ccand-scope c) key)))
          (unless live
            (setf (cline-op line) "promote")
            (return-from core-decide (values line "promote")))
          (let* ((newest (car (last live)))
                 (seen (or (>= (ccand-base c) (citem-gen newest))
                           (and (string/= (ccand-writer-session c) "") (string= (citem-writer-session newest) (ccand-writer-session c))))))
            (setf (cline-of line) (citem-id newest))
            (if (and (member via '("quote" "edit") :test #'string=) seen)
                (progn (setf (cline-op line) "supersede") (values line "supersede"))
                (progn (setf (cline-op line) "contest") (values line "contest")))))))))

(defun core-promote-all (core-dir cands)
  "Log.PromoteAll: (values lines verdicts)."
  (let ((verdicts '()))
    (let ((lines (core-commit core-dir
                              (lambda (st emit)
                                (dolist (c cands)
                                  (multiple-value-bind (line v) (core-decide st c)
                                    (push v verdicts)
                                    (when line (funcall emit line))))))))
      (values lines (nreverse verdicts)))))

;;; Candidates from a session's ledger

(defun record-core-scope (o by-id session)
  (flet ((own (o) (let ((s (om::obs-scope o)))
                    (if (or (string= s +scope-user+) (string= s +scope-global+) (om::has-prefix s +scope-project-prefix+)) s session))))
    (if (< (length (om::obs-replaces o)) 2)
        (own o)
        (intersect-scopes (mapcar (lambda (id) (let ((src (gethash id by-id))) (if src (own src) session)))
                                  (om::obs-replaces o))))))

(defun untrusted-source-p (o)
  (member (om::obs-source o) '("tool-observed" "other-session" "external-untrusted" "host-summary") :test #'string=))

(defun core-candidates (records labels closed scope writer-model writer-session base one-time-p)
  "core.Candidates: records (obs), labels an id -> label table, closed a list of ids."
  (let ((by-id (make-hash-table :test 'equal)) (out '()))
    (dolist (o records) (setf (gethash (om::obs-id o) by-id) o))
    (dolist (o records (nreverse out))
      (when (and (equal (gethash (om::obs-id o) labels) "believed") (string/= (om::obs-kind o) "forget"))
        (let* ((wm (om::alist-member (om::obs-writer o) "model")) (ws (om::alist-member (om::obs-writer o) "session"))
               (c (make-ccand :text (om::obs-content o) :record (om::obs-id o) :base base
                              :writer-model (if (string/= wm "") wm writer-model)
                              :writer-session (if (string/= ws "") ws writer-session)
                              :tool-only (and (untrusted-source-p o) t))))
          (when (cond ((and (string= (om::obs-kind o) "preference") (string= (om::obs-status o) ""))
                       (setf (ccand-kind c) "rule" (ccand-key c) (om::obs-subject o))
                       (when (or (string= (om::obs-source o) "user-said") (om::obs-from-user o)) (setf (ccand-via c) "quote"))
                       t)
                      ((and (string= (om::obs-status o) "proposed") (not (member (om::obs-id o) closed :test #'string=)))
                       (setf (ccand-kind c) "intention")
                       (cond ((string= (om::obs-source o) "user-said") (setf (ccand-via c) "quote"))
                             ((and (string= (om::obs-source o) "assistant-claim") (string/= (om::obs-derived-from o) ""))
                              (setf (ccand-via c) "request")))
                       t))
            (when (and (string= (ccand-via c) "quote") (om::obs-source-ids o))
              (setf (ccand-roots c) (list (user-root (first (om::obs-source-ids o)) (om::obs-content o)))))
            (setf (ccand-scope c) (record-core-scope o by-id scope))
            (when (and one-time-p (funcall one-time-p (om::obs-content o))) (setf (ccand-one-time c) t))
            (push c out)))))))

(defun forget-cline (forget forgotten scope writer-model writer-session base)
  "core.ForgetLine: (values line ok)."
  (let* ((fs (om::obs-scope forget))
         (scopes (cond ((or (string= fs "user")) (list scope +scope-user+))
                       ((or (string= fs "project") (om::has-prefix fs +scope-project-prefix+)) (list scope))
                       (t (return-from forget-cline (values nil nil)))))
         (ids '()))
    (flet ((add (id) (unless (or (string= id "") (member id ids :test #'string=)) (setf ids (append ids (list id))))))
      (dolist (tomb (om::obs-forgets forget)) (add (first tomb)))
      (dolist (o forgotten)
        (add (om::obs-id o))
        (dolist (s scopes)
          (unless (string= s "")
            (add (item-id "rule" s (norm-key (if (string/= (om::trim-space (om::obs-subject o)) "") (om::obs-subject o) (om::obs-content o)))
                          (om::obs-content o)))
            (add (item-id "intention" s (norm-key (om::obs-content o)) (om::obs-content o)))))))
    (if ids
        (values (make-cline :op "forget" :id (om::obs-id forget) :scope (car (last scopes)) :tombstones ids :base base
                            :writer-model writer-model :writer-session writer-session)
                t)
        (values nil nil))))

;;; The start-context block and the status lines

(defparameter +core-section-header+
  (om::cat "## Shared memory" (string #\Newline) (string #\Newline)
           "Carried over from earlier sessions by code: the user's standing constraints and the open intentions first. "
           "Remembered items are user-reported or extracted context, unverified against the current repository and conversation; existing code and the user's current words take precedence."
           " A line marked conflicts is a contradiction no session settled: ask the user which holds."))

(defparameter +core-max-shown+ 10)

(defun core-shown (st kind scopes)
  (remove-if-not (lambda (it) (and (citem-live-p it) (string= (citem-kind it) kind) (scope-visible-p (citem-scope it) scopes)))
                 (cstate-items st)))

(defun core-item-line (it model)
  (let ((notes (list (om::cat "core " (citem-id it)))))
    (when (citem-with it) (setf notes (append notes (list (om::cat "conflicts " (om::join (citem-with it) ", "))))))
    (let ((h (model-hint (citem-writer-model it) model)))
      (when (string/= h "") (setf notes (append notes (list h)))))
    (om::cat "- " (om::redact-secrets (citem-text it)) " (" (om::join notes "; ") ")")))

(defun core-section (st scopes model)
  "core.Section."
  (let ((parts '()) (nl (string #\Newline)))
    (loop for (kind title) in '(("rule" "Constraints the user stated:")
                                ("intention" "Open intentions:")
                                ("lesson" "Lessons from other projects, their values left out:"))
          for items = (core-shown st kind scopes)
          when items
            do (let ((lines (list title)))
                 (loop for it in items for i from 0
                       do (when (= i +core-max-shown+)
                            (setf lines (append lines (list (format nil "(~D more; /om:status lists the core.)" (- (length items) +core-max-shown+)))))
                            (return))
                          (setf lines (append lines (list (core-item-line it model)))))
                 (push (om::join lines nl) parts)))
    (if parts
        (om::cat +core-section-header+ nl nl (om::join (nreverse parts) (om::cat nl nl)))
        "")))

(defun core-status-lines (st through pulled)
  "core.StatusLines."
  (let ((counts (make-hash-table :test 'equal)) (live 0) (contested 0))
    (dolist (it (cstate-items st))
      (when (citem-live-p it) (incf live) (incf (gethash (citem-kind it) counts 0)))
      (when (string= (citem-status it) "contested") (incf contested)))
    (let ((line (format nil "Core log: generation ~D, ~D live items (~D rules, ~D intentions, ~D lessons, ~D memories), ~D contested, ~D tombstones"
                        (cstate-gen st) live (gethash "rule" counts 0) (gethash "intention" counts 0) (gethash "lesson" counts 0)
                        (gethash "memory" counts 0) contested (hash-table-count (cstate-tombstoned st)))))
      (when (> (cstate-torn st) 0) (setf line (format nil "~A, ~D torn lines skipped" line (cstate-torn st))))
      (append (list (om::cat line ".")
                    (if pulled
                        (format nil "This session read the core through generation ~D." through)
                        "This session has not read the core."))
              (loop for it in (cstate-items st)
                    when (string= (citem-status it) "contested")
                      collect (format nil "- contested ~A ~A (~A): ~A; conflicts ~A" (citem-kind it) (citem-id it) (citem-scope it)
                                      (om::redact-secrets (citem-text it)) (om::join (citem-with it) ", ")))))))
