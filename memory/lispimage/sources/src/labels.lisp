;;;; memory/ledger labels.go: the labels the summary, the search tags and level 1 read, worked
;;;; out from the fold (contracts/truth.md, "Labels"), and the three-way supersession decision.
;;;; In OM.ENGINE beside contest.lisp, whose Contested it reads.

(in-package "OM.ENGINE")

;;; Labels (labels.go)

(defparameter +label-values+ '("believed" "superseded" "retracted" "contested" "held"))

(defun supersession (later earlier)
  "Supersession: \"replace\", \"keep-both\", \"contested\" or \"\"."
  (cond ((not (may-supersede-p (obs-status later) (obs-status earlier))) "")
        ((member-str (obs-id earlier) (obs-supersedes later)) "replace")
        ((negation-conflict-p (obs-content later) (obs-content earlier)) "contested")
        ((not (updates-specific-p (obs-content later) (obs-content earlier))) "")
        ((and (string/= (obs-authority later) "") (string/= (obs-authority earlier) "")
              (< (authority-rank (obs-authority later)) (authority-rank (obs-authority earlier))))
         "contested")
        (t "keep-both")))

(defun label-of (o superseded retracted contested)
  "LabelOf."
  (cond ((or retracted (string= (obs-status o) +status-retracted+)) "retracted")
        (superseded "superseded")
        ((let ((h (held-rule o))) (and h (string/= h ""))) "held")
        (contested "contested")
        (t "believed")))

(defun labels-from (records from)
  "labelsOf: the labels of records by id, the links read from from."
  (let ((by-id (make-set)) (superseded (make-set)) (contested (make-set)) (out (make-set)))
    (dolist (o records) (setf (gethash (obs-id o) by-id) o))
    (maphash (lambda (id v) (declare (ignore v)) (setf (gethash id contested) t)) (contested from))
    (dolist (o from)
      (dolist (id (obs-supersedes o)) (unless (string= id (obs-id o)) (setf (gethash id superseded) t)))
      (dolist (id (obs-conflicts o))
        (when (and (gethash id by-id) (string/= id (obs-id o)))
          (setf (gethash id contested) t (gethash (obs-id o) contested) t)))
      (dolist (id (obs-possibly-updates o))
        (let ((earlier (gethash id by-id)))
          (when (and earlier (string/= id (obs-id o)))
            (let ((d (supersession o earlier)))
              (cond ((string= d "keep-both") (setf (gethash id superseded) t))
                    ((string= d "contested") (setf (gethash id contested) t (gethash (obs-id o) contested) t))))))))
    (let ((retracted (retracted-ids from)))
      (dolist (o records)
        (setf (gethash (obs-id o) out)
              (label-of o (gethash (obs-id o) superseded) (gethash (obs-id o) retracted) (gethash (obs-id o) contested)))))
    out))

(defun labels-of (records) "LabelsOf." (labels-from records records))

(defun folded-labels (f)
  "FoldedLedger.Labels."
  (let ((out (labels-from (folded-observations f) (folded-active f))))
    (maphash (lambda (id v) (declare (ignore v))
               (multiple-value-bind (l ok) (gethash id out)
                 (when (and ok (string/= l "retracted")) (setf (gethash id out) "superseded"))))
             (folded-replaced-by f))
    (maphash (lambda (id v) (declare (ignore v))
               (when (nth-value 1 (gethash id out)) (setf (gethash id out) "retracted")))
             (truth-out (folded-truth f)))
    (maphash (lambda (id challenged)
               (when (and challenged (equal (gethash id out) "believed")) (setf (gethash id out) "contested")))
             (folded-challenged f))
    out))
