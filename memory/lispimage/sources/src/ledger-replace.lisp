;;;; memory/ledger replace.go: level 1's replacements (Observation.Replaces, the legacy rule for
;;;; consolidated batches written without it), read while a fold walks the branch, and Standing,
;;;; which reads every link through them to the record standing for its target.

(in-package "OM.KERNEL")

(defstruct (tracker (:constructor %make-tracker))
  (by (make-set))
  ;; pending: covers-up-to id -> list of (id by merge), level-1 records' Replaces waiting for
  ;; level 1's drop entry at that entry.
  (pending (make-set))
  ;; legacy: covers-up-to id -> the consolidated batch's records with no Replaces.
  (legacy (make-set))
  lookup hidden)

(defun make-tracker (lookup hidden) (%make-tracker :lookup lookup :hidden hidden))

(defun tracker-set (tr id rep merge)
  (unless (or (string= id "") (string= rep "") (string= id rep))
    (unless (and merge (tracker-hidden tr) (funcall (tracker-hidden tr) id))
      (unless (nth-value 1 (gethash id (tracker-by tr)))
        (setf (gethash id (tracker-by tr)) rep)))))

(defun tracker-recorded (tr observations covers consolidated)
  (when consolidated
    (let ((legacy '()))
      (dolist (o observations)
        (if (null (obs-replaces o))
            (push-end o legacy)
            (dolist (id (obs-replaces o))
              (push-end (list id (obs-id o) (> (length (obs-replaces o)) 1)) (gethash covers (tracker-pending tr))))))
      (when legacy (setf (gethash covers (tracker-legacy tr)) legacy)))))

(defun tracker-dropped (tr ids covers consolidated)
  (multiple-value-bind (pairs ok) (gethash covers (tracker-pending tr))
    (when (and ok consolidated)
      (remhash covers (tracker-pending tr))
      (dolist (p pairs)
        (destructuring-bind (id by merge) p
          (when (member-str id ids) (tracker-set tr id by merge))))))
  (multiple-value-bind (batch ok) (gethash covers (tracker-legacy tr))
    (when ok
      (remhash covers (tracker-legacy tr))
      (dolist (id ids)
        (unless (nth-value 1 (gethash id (tracker-by tr)))
          (let ((old (funcall (tracker-lookup tr) id)))
            (when old
              (multiple-value-bind (rep merge) (legacy-replacement old batch)
                (when (string/= rep "") (tracker-set tr id rep merge))))))))))

(defun includes-all-p (a b) (every (lambda (s) (member-str s a)) b))
(defun same-set-p (a b) (and (includes-all-p a b) (includes-all-p b a)))

(defun legacy-replacement (old batch)
  "legacyReplacement: (values rep merge), rep \"\" when none or more than one may have."
  (when (some (lambda (r) (member-str (obs-id old) (obs-supersedes r))) batch)
    (return-from legacy-replacement (values "" nil)))
  (dolist (r batch)
    (when (and (string/= (obs-id r) (obs-id old)) (string= (obs-content r) (obs-content old))
               (same-set-p (obs-source-ids r) (obs-source-ids old)))
      (return-from legacy-replacement (values (obs-id r) nil))))
  (let ((found ""))
    (dolist (r batch)
      (unless (or (string= (obs-id r) (obs-id old))
                  (not (includes-all-p (obs-source-ids r) (obs-source-ids old)))
                  (added-specifics (obs-content old) (list (obs-content r))))
        (when (string/= found "") (return-from legacy-replacement (values "" nil)))
        (setf found (obs-id r))))
    (values found (string/= found ""))))

(defun hidden-by-p (id observations dropped reflections)
  "hiddenBy: a standing supersede or citation would hide id."
  (or (some (lambda (o) (and (string/= (obs-id o) id) (not (gethash (obs-id o) dropped)) (member-str id (obs-supersedes o))))
            observations)
      (some (lambda (r) (member-str id (refl-supporting r))) reflections)))

(defun standing (id replaced-by &optional follow)
  "Standing: the record that stands for id now."
  (let ((seen (make-set)))
    (setf (gethash id seen) t)
    (loop
      (multiple-value-bind (next ok) (gethash id replaced-by)
        (when (or (not ok) (gethash next seen) (and follow (not (funcall follow next))))
          (return id))
        (setf (gethash next seen) t id next)))))

(defun resolve-ids (ids self replaced-by follow)
  "resolveIDs: (values ids changed)."
  (if (or (null ids) (zerop (hash-table-count replaced-by)))
      ids
      (let ((changed nil) (out '()))
        (dolist (id ids)
          (let ((s (standing id replaced-by follow)))
            (when (string/= s id) (setf changed t))
            (if (or (string= s self) (member-str s out))
                (setf changed t)
                (push-end s out))))
        (if changed out ids))))

(defun resolve-observation-links (o replaced-by &optional follow)
  "ResolveObservationLinks: a copy of o with its links read through replaced-by."
  (let ((c (copy-obs o)))
    (setf (obs-supersedes c) (resolve-ids (obs-supersedes o) (obs-id o) replaced-by follow)
          (obs-possibly-updates c) (resolve-ids (obs-possibly-updates o) (obs-id o) replaced-by follow)
          (obs-conflicts c) (resolve-ids (obs-conflicts o) (obs-id o) replaced-by follow))
    c))

(defun resolve-reflection-citations (r replaced-by &optional follow)
  (if (zerop (hash-table-count replaced-by))
      r
      (let ((c (copy-refl r)))
        (setf (refl-supporting c) (resolve-ids (refl-supporting r) "" replaced-by follow))
        c)))
