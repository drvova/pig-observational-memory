;;;; memory/neutral: the engine's own entries for a harness that owns its transcript (store.go),
;;;; the branch merge that places them in the transcript (Branch), and the host's read and append
;;;; over the two (host.go's read and appendEntry). The compaction entry points (compact.go) need
;;;; the hooks and are W5's.

(in-package "OM")

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

(defparameter +aliases-key+ "omAliases"
  "AliasesKey: the raw member a reader lists, on an entry built from several source lines, the
source ids other than the entry's own.")

(defstruct (stored (:constructor make-stored (after entry)))
  "Stored: an entry object and the id of the entry it follows (\"\" puts it first)."
  (after "" :type string)
  entry)

;;; Files

(defun mkdir-all (dir mode)
  "os.MkdirAll: dir and its missing parents, each created with mode."
  (let ((path "") (parts (split (path-clean dir) "/")))
    (dolist (part parts)
      (setf path (cond ((and (string= part "") (string= path "")) "/")
                       ((string= path "/") (cat "/" part))
                       ((string= path "") part)
                       (t (cat path "/" part))))
      (unless (or (string= part "") (probe-file (cat path "/")))
        (handler-case (sb-posix:mkdir path mode)
          (sb-posix:syscall-error (e)
            (unless (= (sb-posix:syscall-errno e) sb-posix:eexist) (error e))))))))

(defun posix-flock (fd operation)
  "flock(2), retried on EINTR; the lock Go's filelock.WithLock takes, so both engines' writers
exclude each other."
  (loop
    (let ((r (sb-alien:alien-funcall
              (sb-alien:extern-alien "flock" (function sb-alien:int sb-alien:int sb-alien:int))
              fd operation)))
      (cond ((zerop r) (return t))
            (t (let ((errno (sb-alien:get-errno)))
                 (unless (= errno sb-posix:eintr)
                   (error "flock: ~A" (sb-int:strerror errno)))))))))

(defmacro with-file-lock ((path) &body body)
  "filelock.WithLock: body under an exclusive flock on path (created 0600 when missing)."
  (let ((fd (gensym "FD")))
    `(let ((,fd (sb-posix:open ,path (logior sb-posix:o-creat sb-posix:o-rdwr) #o600)))
       (unwind-protect
            (progn (posix-flock ,fd 2) ; LOCK_EX
                   (unwind-protect (progn ,@body) (posix-flock ,fd 8))) ; LOCK_UN
         (sb-posix:close ,fd)))))

(defun append-octets (path octets)
  "Appends octets to path, created 0600 when missing, in one write when the system allows."
  (let ((fd (sb-posix:open path (logior sb-posix:o-creat sb-posix:o-wronly sb-posix:o-append) #o600)))
    (unwind-protect
         (sb-sys:with-pinned-objects (octets)
           (let ((done 0) (n (length octets)))
             (loop while (< done n)
                   do (let ((w (sb-posix:write fd (sb-sys:sap+ (sb-sys:vector-sap octets) done) (- n done))))
                        (incf done w)))))
      (sb-posix:close fd))))

(defun file-version (path)
  "fileVersion: a file's identity, size and modification time, \"\" when it cannot be read.
SBCL's stat has whole seconds, so a file changed within the last two seconds has no version
(it is read again) rather than one a same-second write could leave unchanged."
  (handler-case
      (let ((st (sb-posix:stat path)))
        (if (>= (sb-posix:stat-mtime st) (- (get-universal-time) 2208988800 2))
            ""
            (format nil "~D:~D:~D" (sb-posix:stat-ino st) (sb-posix:stat-size st) (sb-posix:stat-mtime st))))
    (sb-posix:syscall-error () "")))

(defun stat-version (&rest paths)
  "statVersion: each path's file-version, \"-\" for one that is missing, each followed by ;.
\"\" when any is too fresh to trust."
  (let ((parts (mapcar (lambda (p) (if (probe-file p) (file-version p) "-")) paths)))
    (if (member "" parts :test #'string=) "" (format nil "~{~A;~}" parts))))

;;; Store

(defun safe-name (s)
  "SafeName: a session id as one file name: path separators and NUL become _, one leading dot
is dropped."
  (map 'string (lambda (c) (if (member c '(#\/ #\\ #.(code-char 0))) #\_ c)) (trim-prefix s ".")))

(defun store-for (root harness session-id)
  "StoreFor: <root>/sessions/<harness>/<session>.jsonl."
  (path-join (path-join (path-join root "sessions") harness) (cat (safe-name session-id) ".jsonl")))

(defun stored-field (object name)
  "A struct field as encoding/json finds it: the exact key, else a key equal under case folding.
(Go takes the last such key in document order; a decoded object keeps no order, so this takes
the first in sorted order. The engine writes only the exact keys.)"
  (multiple-value-bind (v ok) (gethash name object)
    (if ok
        v
        (let ((key (find-if (lambda (k) (string-equal k name))
                            (sort (loop for k being the hash-keys of object collect k) #'string<))))
          (and key (gethash key object))))))

(defun decode-stored (line)
  "One store line as Load decodes it into Stored, or nil when it does not decode or has no
entry."
  (handler-case
      (let ((m (json-parse (coerce line 'simple-string))))
        (when (hash-table-p m)
          (let ((after (stored-field m "after")) (entry (stored-field m "entry")))
            (when (and (or (jnil-p after) (stringp after)) (hash-table-p entry))
              (make-stored (if (stringp after) after "") entry)))))
    (json-error () nil)))

(defun store-load (path)
  "Store.Load: every stored entry in the order written; a missing store is empty and a line
that does not decode (a write cut off) is skipped."
  (let ((text (read-file-string path)))
    (when text
      (loop for line in (scanner-lines text)
            for st = (decode-stored line)
            when st collect st))))

(defun stored-line (after entry)
  "json.Marshal(Stored{after, entry})."
  (with-output-to-string (out)
    (write-string "{\"after\":" out)
    (write-json-string after out)
    (write-string ",\"entry\":" out)
    (write-json entry out)
    (write-char #\} out)))

(defun store-append (path after entry)
  "Store.Append: entry written after the entry with id after, one line under the store's lock."
  (let ((line (stored-line after entry)))
    (mkdir-all (directory-namestring-of path) #o700)
    (with-file-lock ((cat path ".lock"))
      (append-octets path (sb-ext:string-to-octets (cat line (string #\Newline)) :external-format :utf-8)))))

(defun directory-namestring-of (path)
  "filepath.Dir."
  (let ((i (position #\/ path :from-end t)))
    (cond ((null i) ".")
          ((zerop i) "/")
          (t (path-clean (subseq path 0 i))))))

;;; Branch

(defun entry-aliases (e)
  "Aliases: the source ids a reader folded into e."
  (let ((v (jget (entry-raw e) +aliases-key+)))
    (cond ((stringp v) nil)
          ((vectorp v) (remove-if-not #'stringp (coerce v 'list)))
          ((listp v) (remove-if-not #'stringp v))
          (t nil))))

(defun neutral-branch (transcript stored)
  "Branch: the transcript with each stored entry placed right after the entry it names (stored
entries after one entry keep their written order), one naming an entry the branch no longer
holds at the end, and parent ids relinked to the merged order."
  (let ((after (make-set)) (placed (make-set)) (alias (make-set)) (orphans '()) (out '()))
    (dolist (e transcript)
      (setf (gethash (entry-id e) placed) t)
      (dolist (a (entry-aliases e)) (setf (gethash a alias) (entry-id e))))
    (dolist (st stored)
      (let ((id (str (jget (stored-entry st) "id"))))
        (unless (string= id "") (setf (gethash id placed) t))))
    (dolist (st stored)
      (let ((e (entry-from-map (stored-entry st))) (target (stored-after st)))
        (multiple-value-bind (id ok) (gethash target alias)
          (when (and ok (not (gethash target placed))) (setf target id)))
        (if (and (string/= target "") (not (gethash target placed)))
            (push e orphans)
            (setf (gethash target after) (append (gethash target after) (list e))))))
    (labels ((emit (e)
               (push e out)
               (let ((children (gethash (entry-id e) after)))
                 (remhash (entry-id e) after)
                 (dolist (c children) (emit c)))))
      (let ((first (gethash "" after)))
        (dolist (c first) (emit c)))
      (remhash "" after)
      ;; Each transcript entry is a copy, as Go's Branch works on Entry values; the raw objects
      ;; are shared, as Go's maps are.
      (dolist (e transcript) (emit (copy-entry e)))
      (dolist (e (nreverse orphans)) (emit e)))
    (let ((parent ""))
      (loop for e in (setf out (nreverse out))
            do (setf (entry-parent-id e) parent)
               (when (entry-raw e) (setf (gethash "parentId" (entry-raw e)) parent))
               (setf parent (entry-id e))))
    out))

;;; Host: the transcript and the store read as one branch, and appends after its tip.

(defstruct neutral-transcript
  "A harness transcript: entries reads it, version says when it may have changed (nil for one
that cannot say)."
  entries version)

(defun file-transcript (format path)
  "transcript.File."
  (make-neutral-transcript :entries (lambda () (transcript-file-entries format path))
                           :version (lambda () (stat-version path))))

(defun db-transcript (format path session)
  "transcript.DB: its version covers the database and its write-ahead log."
  (make-neutral-transcript :entries (lambda () (db-entries format path session))
                           :version (lambda () (stat-version path (cat path "-wal")))))

(defstruct neutral-host
  session dir root transcript store
  ;; now returns the Unix time in milliseconds entries are stamped with; nil is the clock.
  now
  (lock (sb-thread:make-mutex :name "neutral host"))
  cached-transcript (transcript-version "") cached-stored (store-version ""))

(defun neutral-now-ms (h)
  (if (neutral-host-now h)
      (funcall (neutral-host-now h))
      (multiple-value-bind (sec usec) (sb-ext:get-time-of-day)
        (+ (* sec 1000) (floor usec 1000)))))

(defun neutral-read (h)
  "read: the merged branch of the transcript and the store, each parsed again only when its file
changed."
  (sb-thread:with-recursive-lock ((neutral-host-lock h))
    (let ((transcript '()) (tr (neutral-host-transcript h)))
      (when tr
        (let ((version (if (neutral-transcript-version tr) (funcall (neutral-transcript-version tr)) "")))
          (if (and (neutral-transcript-version tr) (string/= version "")
                   (string= version (neutral-host-transcript-version h)))
              (setf transcript (neutral-host-cached-transcript h))
              (setf transcript (funcall (neutral-transcript-entries tr))
                    (neutral-host-cached-transcript h) transcript
                    (neutral-host-transcript-version h) version))))
      (let ((version (file-version (neutral-host-store h))))
        (when (or (string= version "") (string/= version (neutral-host-store-version h)))
          (setf (neutral-host-cached-stored h) (store-load (neutral-host-store h))
                (neutral-host-store-version h) version)))
      (neutral-branch transcript (neutral-host-cached-stored h)))))

(defun new-entry-id ()
  "newID: \"om\" and eight hex digits from the system's random source."
  (with-open-file (in "/dev/urandom" :element-type '(unsigned-byte 8))
    (let ((b (make-array 4 :element-type '(unsigned-byte 8))))
      (read-sequence b in)
      (format nil "om~(~{~2,'0x~}~)" (coerce b 'list)))))

(defun neutral-append-entry (h entry)
  "appendEntry: entry stamped with an id, the tip as its parent and the time, and stored after
the tip; returns the id."
  (let* ((branch (neutral-read h))
         (tip (if branch (entry-id (car (last branch))) ""))
         (id (new-entry-id)))
    (setf (gethash "id" entry) id
          (gethash "parentId" entry) tip
          (gethash "timestamp" entry) (format-unix-nanos (* (neutral-now-ms h) 1000000) t))
    (sb-thread:with-recursive-lock ((neutral-host-lock h))
      (let ((before (file-version (neutral-host-store h))))
        (store-append (neutral-host-store h) tip entry)
        ;; The cache grows by this line, as Load decodes it, only when it was current before.
        (if (and (string/= before "") (string= before (neutral-host-store-version h)))
            (setf (neutral-host-cached-stored h)
                  (append (neutral-host-cached-stored h) (list (make-stored tip (json-parse (json-string entry)))))
                  (neutral-host-store-version h) (file-version (neutral-host-store h)))
            (setf (neutral-host-store-version h) ""))))
    id))

(defun neutral-append-custom (h custom-type data)
  "AppendEntry: a custom entry stored after the tip."
  (neutral-append-entry h (jobject "type" "custom" "customType" custom-type "data" data)))
