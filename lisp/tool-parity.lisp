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

(defparameter +usage-junk+
  (format nil "~{~A~^~%~}"
          '("{\"name\":\"m0-note\",\"event\":\"view\",\"time\":5}"
            "not json"
            "{\"name\":\"\",\"event\":\"view\"}"
            "{\"NAME\":\"m5-note\",\"Event\":\"view\",\"time\":\"2026-11-01T09:00:00Z\"}"
            "{\"name\":\"m5-note\",\"event\":\"view\",\"user\":\"yes\"}"
            "{\"name\":\"m5-note\",\"event\":\"helped\",\"user\":true,\"quote\":null}"
            "[1,2]"
            "null"
            "{\"name\":\"m5-note\",\"event\":\"view\"")))

(defun utc-rfc3339 (time) (format-rfc3339 (cons (car time) 0)))

(defun index-step (n op dir step args pin)
  "The index ops of the curated script, as lisptool_test.go's goCurated prints them."
  (let ((project (or (om::jget step "project") ""))
        (budget (let ((b (gethash "budget" args))) (if b (parse-integer b) 0))))
    (cond
      ((string= op "record")
       (append-usage dir (gethash "name" args) (gethash "event" args)
                     :record (and (string= (gethash "event" args) "cited") (gethash "record" args)))
       (line "~D recorded" n))
      ((string= op "junk")
       (ensure-dir dir)
       (with-open-file (out (usage-path dir) :direction :output :if-exists :append :if-does-not-exist :create
                                             :external-format :utf-8)
         (write-string +usage-junk+ out))
       (line "~D junk" n))
      ((string= op "user-pin")
       (let ((path (store-path dir (gethash "name" args))))
         (write-atomic path (replace-first (read-file path) (format nil "~%---~%") (format nil "~%pinned: true~%---~%")))
         (line "~D pinned" n)))
      ((string= op "usage")
       (let ((usage (curated-usage dir)) (names '()))
         (maphash (lambda (k v) (declare (ignore v)) (push k names)) usage)
         (line "~D usage ~A" n
               (om::join (loop for name in (sort names #'string<)
                               for e = (gethash name usage)
                               collect (format nil "~A:~D/~D/~D/~D/~D/~A" name (ev-views e) (ev-helped e) (ev-harmed e)
                                               (ev-edits e) (ev-corrected e)
                                               (if (zero-time-p (ev-last e)) "" (utc-rfc3339 (ev-last e)))))
                         " "))))
      ((string= op "rank")
       (let ((usage (curated-usage dir)))
         (line "~D rank ~A" n
               (om::join (loop for m in (curated-rank (list-memories dir project) usage)
                               collect (if (curated-flagged-p m (usage-of usage (mem-name m)))
                                           (concatenate 'string (mem-name m) "!")
                                           (mem-name m)))
                         ","))))
      ((string= op "index")
       (multiple-value-bind (text shown) (index-for dir project budget)
         (line "~D index ~A ~A" n (om::join shown ",") (one-line-json text))))
      ((string= op "index-as")
       (multiple-value-bind (text shown) (index-as dir project (or (om::jget step "session") "") budget)
         (line "~D index ~A ~A" n (om::join shown ",") (one-line-json text))))
      ((string= op "list-as")
       (line "~D list ~A" n (om::join (mapcar #'mem-name (list-as dir project (or (om::jget step "session") ""))) ",")))
      ((string= op "describe-as")
       (handler-case (line "~D describe ~A" n (one-line-json (curated-describe (get-as dir (gethash "name" args) project (or (om::jget step "session") "")))))
         (curated-error (e) (line "~D error ~A" n (one-line-json (curated-error-message e))))))
      ((string= op "pin")
       (when (equal (gethash "reset" args) "true") (pin-reset pin))
       (multiple-value-bind (text shown rendered) (pin-get pin (lambda () (index-for dir project budget)))
         (line "~D pin ~A ~A ~A ~A" n (if rendered "true" "false") (om::join shown ",")
               (om::join (pin-shown-names pin) ",") (one-line-json text))))
      ((string= op "settle")
       (let ((corrections (loop for c across (or (om::jget step "corrections") #())
                                collect (make-correction :id (om::jget c "id") :subject (om::jget c "subject")
                                                         :nanos (car (parse-rfc3339 (om::jget c "time")))))))
         (multiple-value-bind (promoted flagged) (curated-settle dir project corrections)
           (line "~D settle ~A | ~A" n
                 (one-line-json (om::join (loop for (name . evidence) in promoted collect (format nil "~A=~A" name evidence)) ";"))
                 (om::join flagged ",")))))
      (t (return-from index-step nil)))
    t))

(defun run-curated (dir script)
  (loop with pin = (make-pin)
        for step across script
        for n from 1
        do (let* ((op (om::jget step "op"))
                  (time (parse-rfc3339 (om::jget step "time")))
                  (*now* (lambda () time))
                  (user (strings-of (om::jget step "user")))
                  (conversation (om::jget step "conversation"))
                  (tools (strings-of (om::jget step "tools")))
                  (c (make-curated-call :dir dir :project (or (om::jget step "project") "")
                                        :source (or (om::jget step "session") "")
                                        :evidence (and (vectorp conversation)
                                                       (let ((texts (strings-of conversation)))
                                                         (lambda (q) (quoted-in-p texts q))))
                                        :user-evidence (lambda (q) (quoted-in-p user q))
                                        :source-of (lambda (q) (quoted-source tools q))))
                  (args (let ((h (make-hash-table :test 'equal)) (a (om::jget step "args")))
                          (when (hash-table-p a) (maphash (lambda (k v) (setf (gethash k h) v)) a))
                          h)))
             (cond
               ((index-step n op dir step args pin))
               ((string= op "layer-change")
                (record-layer-change dir (gethash "event" args) (gethash "key" args) (or (gethash "model" args) "")
                                     (or (om::jget step "session") "") (gethash "quote" args)
                                     (let ((tg (gethash "target" args))) (and tg (string/= tg "") (parse-integer tg))))
                (line "~D recorded" n))
               ((string= op "staged")
                (line "~D staged ~A" n (one-line-json (om::join (mapcar #'staged-change-string (staged-changes dir (or (om::jget step "project") ""))) "; "))))
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
