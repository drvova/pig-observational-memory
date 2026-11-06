;;;; memory/ledger reflections: the Reflection type and its JSON (types.go), ActiveReflections,
;;;; ReflectionAnchor (anchor.go) and ReflectionDateRanges (render.go).

(in-package "OM.KERNEL")

(defstruct refl
  (id "" :type string)
  (content "" :type string)
  (supporting '() :type list)
  (token-count 0 :type integer)
  (supersedes '() :type list))

(defun write-refl-json (r out)
  "encoding/json of ledger.Reflection."
  (write-string "{\"id\":" out)
  (write-json (refl-id r) out)
  (write-string ",\"content\":" out)
  (write-json (refl-content r) out)
  (write-string ",\"supportingObservationIds\":" out)
  (write-json (coerce (refl-supporting r) 'simple-vector) out)
  (write-string ",\"tokenCount\":" out)
  (write-json (refl-token-count r) out)
  (when (refl-supersedes r)
    (write-string ",\"supersedes\":" out)
    (write-json (coerce (refl-supersedes r) 'simple-vector) out))
  (write-char #\} out))

(defun refl-json (r) (with-output-to-string (out) (write-refl-json r out)))

(defun refl-to-value (r) (json-parse (refl-json r)))

(defun reflection-from (m)
  "ReflectionFrom: a decoded reflection, validated, or nil."
  (when (hash-table-p m)
    (let ((count (jget m "tokenCount"))
          (ids (non-empty-strings (jget m "supportingObservationIds")))
          (content (str (jget m "content"))))
      (when (and (memory-id-p (jget m "id")) (string/= content "")
                 (not (contains-any content (format nil "~C~C" #\Return #\Newline)))
                 ids (realp count) (>= count 0))
        (let ((r (make-refl :id (jget m "id") :content content :supporting ids :token-count (truncate count)))
              (list (jget m "supersedes")))
          (when (and (vectorp list) (not (stringp list)))
            (loop for id across list
                  do (when (and (memory-id-p id) (string/= id (refl-id r)) (not (member-str id (refl-supersedes r))))
                       (push-end id (refl-supersedes r)))))
          r)))))

(defun active-reflections (reflections)
  "ActiveReflections: reflections without the ones another in the list supersedes."
  (let ((superseded (make-set)) (by-id (make-set)))
    (dolist (r reflections) (setf (gethash (refl-id r) by-id) r))
    (dolist (r reflections)
      (dolist (id (refl-supersedes r))
        (unless (string= id (refl-id r))
          (let ((other (gethash id by-id)))
            (unless (and other (member-str (refl-id r) (refl-supersedes other)))
              (setf (gethash id superseded) t))))))
    (remove-if (lambda (r) (gethash (refl-id r) superseded)) reflections)))

(defun reflection-anchor (r by-id positions)
  "ReflectionAnchor: (values id index ok), the earliest anchor among the cited observations."
  (let ((id "") (index -1) (ok nil))
    (dolist (cited (refl-supporting r))
      (let ((o (gethash cited by-id)))
        (when o
          (multiple-value-bind (source at anchored) (observation-anchor o positions)
            (when (and (string= id "") (not ok)) (setf id source))
            (when (and anchored (or (not ok) (< at index)))
              (setf id source index at ok t))))))
    (values id index ok)))

(defun reflection-date-ranges (reflections by-id)
  "ReflectionDateRanges over a fold's reflections and records: id -> \"YYYY-MM-DD\" or
\"YYYY-MM-DD to YYYY-MM-DD\"."
  (let ((dates (make-set)))
    (dolist (r reflections dates)
      (let ((first "") (last ""))
        (dolist (id (refl-supporting r))
          (let ((o (gethash id by-id)))
            (when (and o (>= (length (obs-timestamp o)) 10))
              (let ((day (subseq (obs-timestamp o) 0 10)))
                (when (or (string= first "") (string< day first)) (setf first day))
                (when (string> day last) (setf last day))))))
        (cond ((string= first ""))
              ((string= first last) (setf (gethash (refl-id r) dates) first))
              (t (setf (gethash (refl-id r) dates) (cat first " to " last))))))))
