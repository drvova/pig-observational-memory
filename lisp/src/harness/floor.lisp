;;;; The kernel floor (memory/kernel/floor.go Floor, memory/harness/layer.go floorOf and
;;;; FloorStatus, memory/layer RecordFloor): the user's settings can lower the floor
;;;; (kernelFloor {"lower": true, "requestedBy", "reason"}, the global settings only: a project's
;;;; never lowers it), and a lowering is recorded in <store>/harness/floor.jsonl, with who asked and
;;;; why, before it takes effect; a request without who or why, or a record that cannot be
;;;; written, leaves it engaged and says why in /om:status. Go keeps one floor per store per
;;;; process; the image keeps one per store per engine-rpc connection (a PiG process keeps one
;;;; connection), and a fresh one per om request (each om hook is its own Go process). While it is
;;;; lowered, om::*kernel-floor-lowered* is bound around the event's layer reads, and the Go layer's
;;;; kernel check (lisp/src/layer/go-layer.lisp kernel-check) admits every definition.

(in-package "OM.SERVER")

(defstruct (kfloor (:conc-name kf-)) lowered refusal)

(defvar *floors* (make-hash-table :test 'eq :weakness :key :synchronized t)
  "A connection (or an om request's token) -> store directory -> its kfloor.")

(defun floor-request (settings-path)
  "config.normalizeFloor over the global settings: (requested-by . reason) for a lowering
request, nil for none."
  (let* ((nested (om::jget (read-json-file settings-path) "observational-memory"))
         (f (and (hash-table-p nested) (gethash "kernelFloor" nested))))
    (when (and (hash-table-p f) (eq (gethash "lower" f) :true))
      (cons (let ((v (gethash "requestedBy" f))) (if (stringp v) v ""))
            (let ((v (gethash "reason" f))) (if (stringp v) v ""))))))

(defun lowering-line (by reason op)
  (format nil "{\"requestedBy\":~A,\"reason\":~A,\"time\":~A,\"op\":~A}"
          (om::json-string by) (om::json-string reason)
          (om::json-string (om.layer::format-rfc3339-nano (cons (om.layer::time-nanos (om.layer::now)) 0)))
          (om::json-string op)))

(defun record-floor (store line)
  "layer.Store.RecordFloor: one line appended to <store>/harness/floor.jsonl under the lock."
  (om.layer::with-store-lock (store)
    (let ((path (om.layer::join-path store "harness" "floor.jsonl")))
      (ensure-directories-exist path)
      (with-open-file (out path :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
        (write-string line out) (write-char #\Newline out) (finish-output out)))))

(defun floor-lower (store kf by reason)
  "Floor.Lower: nil, or the refusal's text."
  (let ((by (trim-space* by)) (reason (trim-space* reason)))
    (when (or (string= by "") (string= reason ""))
      (return-from floor-lower "lowering the kernel floor requires who asked (requestedBy) and why (reason)"))
    (let ((line (lowering-line by reason "lower")))
      (handler-case (record-floor store line)
        (error (e) (return-from floor-lower (princ-to-string e))))
      (setf (kf-lowered kf) (let ((v (om::json-parse line))) (list (om::jget v "requestedBy") (om::jget v "reason") (om::jget v "time"))))
      nil)))

(defun floor-engage (store kf)
  "Floor.Engage: a lowered floor raised again, recorded (the floor is engaged even when that fails)."
  (when (kf-lowered kf)
    (setf (kf-lowered kf) nil)
    (ignore-errors (record-floor store (lowering-line "" "" "engage")))))

(defun trim-space* (s) (om::trim-space s))

(defun floor-of (owner store settings-path)
  "harness.floorOf: owner's floor of store brought in line with the configured request."
  (let* ((stores (or (gethash owner *floors*) (setf (gethash owner *floors*) (make-hash-table :test 'equal))))
         (kf (or (gethash store stores) (setf (gethash store stores) (make-kfloor))))
         (request (floor-request settings-path)))
    (cond ((and request (not (kf-lowered kf)))
           (setf (kf-refusal kf) (floor-lower store kf (car request) (cdr request))))
          ((and request (not (and (string= (first (kf-lowered kf)) (trim-space* (car request)))
                                  (string= (second (kf-lowered kf)) (trim-space* (cdr request))))))
           (floor-engage store kf)
           (setf (kf-refusal kf) (floor-lower store kf (car request) (cdr request))))
          ((and (null request) (kf-lowered kf))
           (setf (kf-refusal kf) nil)
           (floor-engage store kf))
          ((null request) (setf (kf-refusal kf) nil)))
    kf))

(defun floor-status (owner store)
  "harness.FloorStatus."
  (let* ((stores (gethash owner *floors*)) (kf (and stores (gethash store stores))))
    (cond ((null kf) "Kernel floor: engaged")
          ((kf-lowered kf)
           (destructuring-bind (by reason time) (kf-lowered kf)
             (format nil "Kernel floor: LOWERED by ~A at ~A (~A)" by time reason)))
          ((kf-refusal kf) (concatenate 'string "Kernel floor: engaged; the configured lowering was refused: " (kf-refusal kf)))
          (t "Kernel floor: engaged"))))

(defun conn-settings-path (c)
  (multiple-value-bind (agent-dir) (conn-paths c) (format nil "~A/settings.json" agent-dir)))

(defun conn-floor-lowered-p (c store)
  "StoreOf's floor for the connection: whether it is lowered after floorOf."
  (and store (string/= store "") (kf-lowered (floor-of c store (conn-settings-path c))) t))

(defmacro with-conn-floor ((c store) &body body)
  "Runs body with the connection's floor of store applied to the Go layer's kernel check."
  `(let ((om::*kernel-floor-lowered* (conn-floor-lowered-p ,c ,store))) ,@body))
