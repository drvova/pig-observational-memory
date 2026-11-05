;;;; The Lisp side of W4's parity test (memory/contracts/lisptool_test.go):
;;;;   sbcl --script lisp/tool-parity.lisp curated <store dir> <script.json>
;;;;   sbcl --script lisp/tool-parity.lisp settle <cases.json>
;;;;   sbcl --script lisp/tool-parity.lisp layer <layer root>   (writes a layer for the schema check)
;;;; Each prints one line per step or case, as the Go side does.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM.LAYER")

(defun line (fmt &rest args)
  (write-string (apply #'format nil fmt args))
  (terpri))

(defun json-file (path) (om::json-parse (read-file path)))

(defun strings-of (v) (and (vectorp v) (coerce v 'list)))

(defun one-line-json (s) (om::json-string s))

(defun run-curated (dir script)
  (loop for step across script
        for n from 1
        do (let* ((op (om::jget step "op"))
                  (time (parse-rfc3339 (om::jget step "time")))
                  (*now* (lambda () time))
                  (user (strings-of (om::jget step "user")))
                  (conversation (om::jget step "conversation"))
                  (c (make-curated-call :dir dir :project (or (om::jget step "project") "")
                                        :source (or (om::jget step "session") "")
                                        :evidence (and (vectorp conversation)
                                                       (let ((texts (strings-of conversation)))
                                                         (lambda (q) (quoted-in-p texts q))))
                                        :user-evidence (lambda (q) (quoted-in-p user q))))
                  (args (let ((h (make-hash-table :test 'equal)) (a (om::jget step "args")))
                          (when (hash-table-p a) (maphash (lambda (k v) (setf (gethash k h) v)) a))
                          h)))
             (cond
               ((string= op "describe")
                (handler-case (line "~D describe ~A" n (one-line-json (curated-describe (get-memory dir (gethash "name" args)))))
                  (curated-error (e) (line "~D error ~A" n (one-line-json (curated-error-message e))))))
               ((string= op "list")
                (line "~D list ~A" n (om::join (mapcar #'mem-name (list-memories dir (or (gethash "project" args) ""))) ",")))
               (t
                (multiple-value-bind (text err) (curated-run c op args)
                  (if err
                      (line "~D error ~A" n (one-line-json err))
                      (line "~D ok ~A" n (one-line-json text)))))))))

(defun run-settle (cases)
  (loop for c across cases
        for n from 1
        do (multiple-value-bind (decided clean reason)
               (settle-v2 (strings-of (om::jget c "events"))
                          :kind (om::jget c "kind") :name (om::jget c "name")
                          :held-id (om::jget c "heldId") :time (parse-rfc3339 (om::jget c "time"))
                          :detector (let ((d (om::jget c "detector")))
                                      (cond ((equal d "code") :code) ((equal d "retired") :retired) (t :other))))
             (line "~D ~A ~A ~A" n (if decided "decided" "held") (if clean "clean" "unclean") (one-line-json reason)))))

(defun run-layer (root)
  (let* ((*layer-root* root) (model "acme/parity-1")
         (*now* (let ((tm (parse-rfc3339 "2026-11-01T10:00:00.5Z"))) (lambda () tm)))
         (d (layer-hold model "(define rank-notes (notes words) (reverse notes))"))
         (events (list (om::jobject "time" "2026-11-01T11:00:00Z" "kind" "pass" "engine" "v2" "stage" "compaction"
                                    "outcome" "ok" "tokens" 10d0 "ranHeld" (vector (ldef-held-id d))))))
    (line "settle ~S" (layer-settle model events))
    (layer-hold model "(define phrase-note (n) (note-line n))")
    (layer-forget model "rank-notes")
    (layer-rollback model 2)
    (line "generations ~D" (layer-generations model))))

(handler-case
    (let ((args (cdr sb-ext:*posix-argv*)))
      (cond ((equal (first args) "curated") (run-curated (second args) (json-file (third args))))
            ((equal (first args) "settle") (run-settle (json-file (second args))))
            ((equal (first args) "layer") (run-layer (second args)))
            (t (error "usage: tool-parity.lisp curated <dir> <script.json> | settle <cases.json>"))))
  (error (e)
    (format *error-output* "tool-parity: ~A~%" e)
    (sb-ext:exit :code 1 :abort t)))
