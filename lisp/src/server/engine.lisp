;;;; The engine work the image does today, over the spike's pipeline (OM package): the neutral
;;;; store (memory/neutral/store.go: load, append, Branch), the transcript read with a cache, the
;;;; settings level 0 reads, and the turn-end extraction (hooks/extract_v2.go extractTurnV2 and
;;;; appendExtraction). Every om:: reference of the server is in this file.
;;;;
;;;; Not here yet, and answered "not ported" (methods.lisp): a branch with a compaction (the
;;;; spike's ExtractionStartIndex has no fold boundary), the transcript formats the spike does not
;;;; read, the detectors (no v2 rules exist on a neutral host's fresh store) and curated memory,
;;;; whose settle step the Go client still runs ("remaining").

(in-package "OM.SERVER")

(defparameter +ported-formats+ '("generic" "claude-code"))

;;; Clock and ids

(defun unix-now ()
  "(values seconds microseconds) since the Unix epoch."
  (multiple-value-bind (sec usec) (sb-ext:get-time-of-day) (values sec usec)))

(defun utc-stamp ()
  "The time as Go's \"2006-01-02T15:04:05.000Z\" in UTC, the form neutral.Host stamps entries with."
  (multiple-value-bind (sec usec) (unix-now)
    (multiple-value-bind (s mi h d mo y) (decode-universal-time (+ sec 2208988800) 0)
      (format nil "~4,'0D-~2,'0D-~2,'0DT~2,'0D:~2,'0D:~2,'0D.~3,'0DZ" y mo d h mi s (floor usec 1000)))))

(defun new-entry-id ()
  "neutral.newID: \"om\" and eight hex digits from the system's random source."
  (with-open-file (in "/dev/urandom" :element-type '(unsigned-byte 8))
    (format nil "om~{~(~2,'0x~)~}" (loop repeat 4 collect (read-byte in)))))

;;; Settings

(defun read-json-file (path)
  (let ((text (om::read-file-string path)))
    (and text (handler-case (let ((v (om::json-parse (coerce text 'simple-string))))
                              (and (hash-table-p v) v))
                (error () nil)))))

(defun env-passive (params)
  "config.ReadEnvPassive over the client's environment (the image's own is not the hook's):
(values passive set)."
  (let ((raw (om::jget (om::jget params "env") "PI_OBSERVATIONAL_MEMORY_PASSIVE")))
    (if (not (stringp raw))
        (values nil nil)
        (let ((v (string-downcase (om::trim-space raw))))
          (cond ((member v '("1" "true" "yes" "on") :test #'string=) (values t t))
                ((member v '("0" "false" "no" "off") :test #'string=) (values nil t))
                (t (values nil nil)))))))

(defun passive-p (params)
  "config.Load's passive on a neutral host: the root's settings.json, then the environment.
A project's settings are never read (neutral.Host.ProjectTrusted is false)."
  (multiple-value-bind (passive set) (env-passive params)
    (if set
        passive
        (let ((nested (om::jget (read-json-file (format nil "~A/settings.json" *root*)) "observational-memory")))
          (eq (om::jget nested "passive") :true)))))

;;; The store

(defun load-store (path)
  "Store.Load: (after . entry-map) per line in order; a missing store is empty, and a line that
does not decode or carries no entry object is skipped."
  (let ((text (om::read-file-string path)) (out '()))
    (when text
      (dolist (line (om::scanner-lines text))
        (multiple-value-bind (m ok) (om::try-parse-object line)
          (let ((entry (and ok (om::jget m "entry"))))
            (when (hash-table-p entry)
              (push (cons (om::str (om::jget m "after")) entry) out))))))
    (nreverse out)))

(defun append-line (path line)
  "Store.Append's write: one line under the store's lock (<store>.lock), the file created 0600."
  (ensure-parent path)
  (with-file-lock ((concatenate 'string path ".lock"))
    (let ((new (not (probe-file path))))
      (with-open-file (out path :direction :output :if-exists :append :if-does-not-exist :create
                                :external-format :utf-8)
        (write-string line out)
        (write-char #\Newline out))
      (when new (sb-posix:chmod path #o600)))))

(defun custom-entry-line (after custom-type data-json id parent stamp)
  "The store line json.Marshal writes for neutral.Host.appendEntry's custom entry: Stored's
fields in order, the entry map's keys sorted, and data-json (a Go struct's encoding, fields in
declaration order) as it is."
  (flet ((js (s) (om::json-string s)))
    (format nil "{\"after\":~A,\"entry\":{\"customType\":~A,\"data\":~A,\"id\":~A,\"parentId\":~A,\"timestamp\":~A,\"type\":\"custom\"}}"
            (js after) (js custom-type) data-json (js id) (js parent) (js stamp))))

(defun entry-aliases (e)
  (let ((v (om::jget (om::entry-raw e) "omAliases")))
    (and (vectorp v) (not (stringp v)) (remove-if-not #'stringp (coerce v 'list)))))

(defun merge-branch (transcript stored)
  "neutral.Branch: the transcript entries with the stored ones placed in, each right after the
entry it names (in written order), one naming an entry the branch does not hold at the end,
and parent ids relinked to the merged order. Returns a simple vector of entries."
  (let ((after (make-hash-table :test 'equal)) (placed (make-hash-table :test 'equal))
        (alias (make-hash-table :test 'equal)) (orphans '()) (out '()))
    (dolist (e transcript)
      (setf (gethash (om::entry-id e) placed) t)
      (dolist (a (entry-aliases e)) (setf (gethash a alias) (om::entry-id e))))
    (dolist (st stored)
      (let ((id (om::jget (cdr st) "id")))
        (when (and (stringp id) (string/= id "")) (setf (gethash id placed) t))))
    (dolist (st stored)
      (let ((e (om::entry-from-map (cdr st))) (a (car st)))
        (let ((id (gethash a alias)))
          (when (and id (not (gethash a placed))) (setf a id)))
        (if (and (string/= a "") (not (gethash a placed)))
            (push e orphans)
            (setf (gethash a after) (append (gethash a after) (list e))))))
    (labels ((emit (root)
               ;; Depth first, children in order, without recursion: a long chain of stored
               ;; entries (each after the previous) is as deep as the store is long.
               (let ((stack (list root)))
                 (loop while stack
                       do (let* ((e (pop stack)) (children (gethash (om::entry-id e) after)))
                            (push e out)
                            (remhash (om::entry-id e) after)
                            (setf stack (append children stack)))))))
      (let ((first (gethash "" after)))
        (remhash "" after)
        (dolist (c first) (emit c)))
      (dolist (e transcript) (emit e))
      (dolist (e (nreverse orphans)) (emit e)))
    (let ((v (coerce (nreverse out) 'simple-vector)) (parent ""))
      (loop for i from 0 below (length v)
            do (let ((e (om::copy-entry (aref v i))))
                 (setf (om::entry-parent-id e) parent)
                 (when (om::entry-raw e) (setf (gethash "parentId" (om::entry-raw e)) parent))
                 (setf (aref v i) e parent (om::entry-id e))))
      v)))

;;; The transcript

(defun transcript-key (path format)
  "The cache key of a transcript file: path, format, size and modification time; nil (do not
cache) when the file was modified in the last two seconds, since the time has a one-second
grain and a rewrite of the same size within it would read stale."
  (handler-case
      (let ((st (sb-posix:stat path)))
        (and (< (sb-posix:stat-mtime st) (- (unix-now) 2))
             (list path format (sb-posix:stat-size st) (sb-posix:stat-mtime st))))
    (error () nil)))

(defun transcript-entries (session path format)
  (if (string= path "")
      '()
      (let ((key (transcript-key path format)))
        (if (and key (equal key (session-transcript-key session)))
            (session-transcript-entries session)
            (let ((entries (om::read-transcript format path)))
              (setf (session-transcript-key session) key
                    (session-transcript-entries session) entries)
              entries)))))

(defun check-format (params)
  (let ((format (param-string params "format" "generic")))
    (unless (member format +ported-formats+ :test #'string=)
      (not-ported (format nil "the ~A transcript format" format)))
    format))

(defun read-branch (session params format)
  "Host.GetBranch: the transcript merged with the session's store."
  (merge-branch (transcript-entries session (param-string params "transcript_path") format)
                (load-store (session-store-path session))))

;;; Level 0 at a turn end

(defun append-entry (session tip custom-type data-json)
  "neutral.Host.AppendEntry after tip: returns the new entry's id."
  (let ((id (new-entry-id)))
    (append-line (session-store-path session) (custom-entry-line tip custom-type data-json id tip (utc-stamp)))
    id))

(defun recorded-data-json (observations covers)
  "ledger.ObservationsRecordedData's encoding (consolidated false, omitted)."
  (format nil "{\"observations\":[~{~A~^,~}],\"coversUpToId\":~A}"
          (mapcar #'om::obs-json observations) (om::json-string covers)))

(defun append-extraction (session branch batches covers)
  "hooks.appendExtraction: one om.observations.recorded entry per batch with records, then an
om.extracted marker when the last batch stops short of what the extraction read. Returns the
number appended."
  (let ((queue '()))
    (dolist (b batches)
      (when (and (om::batch-observations b) (string/= (om::batch-covers b) ""))
        (push (cons "om.observations.recorded" (recorded-data-json (om::batch-observations b) (om::batch-covers b)))
              queue)))
    (when (and (string/= covers "") (or (null batches) (string/= (om::batch-covers (car (last batches))) covers)))
      (push (cons "om.extracted" (format nil "{\"coversUpToId\":~A}" (om::json-string covers))) queue))
    (let ((tip (if (plusp (length branch)) (om::entry-id (aref branch (1- (length branch)))) "")))
      (dolist (p (nreverse queue))
        (setf tip (append-entry session tip (car p) (cdr p)))))
    (length queue)))

(defun turn-end-level-0 (session params)
  "hooks.OnTurnEnd on a neutral host: level 0 over the entries after the furthest extraction
marker, appended to the store. Passive mode does nothing. Returns a result object; an error
inside the extraction is recovered (extraction never fails a turn) and named in it."
  (when (passive-p params)
    (return-from turn-end-level-0 (om::jobject "appended" 0 "passive" :true)))
  (let* ((format (check-format params))
         (branch (read-branch session params format)))
    (when (find "compaction" branch :key #'om::entry-type :test #'string=)
      (not-ported "level 0 on a branch with a compaction"))
    (handler-case
        (let ((start (om::extraction-start-index branch)))
          (multiple-value-bind (batches covers)
              (om::extract branch start (length branch) (om::fold-ledger branch) :cwd (param-string params "cwd"))
            (om::jobject "appended" (append-extraction session branch batches covers))))
      (error (e)
        (format *error-output* "om engine: turn-end level 0 recovered: ~A~%" e)
        (om::jobject "appended" 0 "recovered" (princ-to-string e))))))
