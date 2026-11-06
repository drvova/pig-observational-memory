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
  (coerce (om::neutral-branch (transcript-entries session (param-string params "transcript_path") format)
                              (om::store-load (session-store-path session)))
          'simple-vector))

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
