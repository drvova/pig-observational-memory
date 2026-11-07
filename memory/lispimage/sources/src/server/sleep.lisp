;;;; The sleep pass at the image's idle (docs/brain-memory.md, "Shape" and decision 13;
;;;; docs/lisp-core.md, decision 9): once no client has been connected for the idle-sleep period,
;;;; the image starts one child SBCL that runs the pass (om::sleep-pass, src/sleep.lisp) over
;;;; every session store under the root and exits; it makes no model request. The child holds
;;;; <root>/sleep/.lock, the lock `om sleep` takes (memory/cli/sleep.go), and checkpoints each store
;;;; it finished in <root>/sleep/state.json (its size when slept, engine-private), so a client that
;;;; connects and kills it loses at most the store in hand. A store's record is appended after its
;;;; tip with the same line Go's neutral host writes, its secrets redacted.

(in-package "OM.SERVER")

(defvar *sleep-child* nil "The running idle-sleep child's process, nil when none.")
(defvar *slept-at* nil "The server's last-activity time the idle pass last started for.")

(defun sleep-idle-seconds ()
  "OM_SLEEP_IDLE_SECONDS (default 60; 0 turns the idle pass off)."
  (let ((env (sb-posix:getenv "OM_SLEEP_IDLE_SECONDS")))
    (or (and env (ignore-errors (parse-integer env))) 60)))

(defun sleep-idle-on-p (root)
  "The root's settings: sleepIdle false turns the idle pass off."
  (let ((nested (om::jget (read-json-file (format nil "~A/settings.json" root)) "observational-memory")))
    (not (and (hash-table-p nested) (eq (om::jget nested "sleepIdle") :false)))))

(defun sleep-lock-path (root) (format nil "~A/sleep/.lock" (string-right-trim "/" root)))
(defun sleep-state-path (root) (format nil "~A/sleep/state.json" (string-right-trim "/" root)))

(defun store-files (root)
  "Every session store <root>/sessions/<harness>/<session>.jsonl, sorted."
  (sort (mapcar #'namestring (directory (format nil "~A/sessions/*/*.jsonl" (string-right-trim "/" root))))
        #'string<))

(defun store-key (root path)
  "A store's key in the state file: <harness>/<file>."
  (let ((prefix (format nil "~A/sessions/" (string-right-trim "/" (namestring (truename root))))))
    (if (om::has-prefix path prefix) (subseq path (length prefix)) path)))

(defun file-size (path)
  (handler-case (sb-posix:stat-size (sb-posix:stat path)) (error () -1)))

(defun read-sleep-state (root)
  (let* ((m (read-json-file (sleep-state-path root))) (stores (and m (om::jget m "stores"))))
    (if (hash-table-p stores) stores (om::jobject))))

(defun write-sleep-state (root stores)
  "Writes the state file through a temporary file and a rename, so a kill leaves the old one."
  (let* ((path (sleep-state-path root)) (tmp (concatenate 'string path ".tmp")))
    (ensure-parent path)
    (with-open-file (out tmp :direction :output :if-exists :supersede :external-format :utf-8)
      (format out "{\"version\":1,\"stores\":~A}~%" (om::json-string stores)))
    (rename-file tmp path)))

(defun sleep-store (root path trigger)
  "One pass over a store's entries (the store alone: its records, compactions and earlier sleep
records), appended after its tip; the data's JSON text, nil when nothing was appended."
  (let* ((branch (coerce (om::neutral-branch '() (om::store-load path)) 'simple-vector))
         (n (length branch)))
    (when (or (zerop n) (let ((at (om::sleep-indexes branch))) (and at (= (car (last at)) (1- n)))))
      (return-from sleep-store nil))
    (let* ((cfg (hook-config (om::jobject)))
           (data (om::sleep-pass branch trigger :decay (om::hconfig-decay cfg) :decay-horizon (om::hconfig-decay-horizon cfg))))
      (when data
        (let ((tip (om::entry-id (aref branch (1- n)))) (id (new-entry-id)))
          (append-line path (custom-entry-line tip "om.sleep" (om::redact-json-text data) id tip (utc-stamp)))
          data)))))

(defun sleep-all (root trigger)
  "The pass over every store under root, under the sleep lock: :busy when another pass holds it,
else the number of stores a record was appended to. A store whose size equals its checkpoint is
skipped."
  (let ((fd (lock-file (sleep-lock-path root) :wait nil)))
    (unless fd (return-from sleep-all :busy))
    (unwind-protect
         (let ((stores (read-sleep-state root)) (count 0))
           (dolist (path (store-files root) count)
             (let ((key (store-key root path)) (size (file-size path)))
               (unless (eql (om::jget stores key) size)
                 (when (sleep-store root path trigger) (incf count))
                 (setf (gethash key stores) (file-size path))
                 (write-sleep-state root stores)))))
      (unlock-file fd))))

(defun sleep-child-main (root)
  "The idle-sleep child's toplevel: the pass over root's stores, then exit."
  (let ((code 0))
    (handler-case (sleep-all root "idle")
      (error (e) (format *error-output* "om sleep child: ~A~%" e) (setf code 1)))
    (finish-output *error-output*)
    (sb-ext:exit :code code :abort t)))

(defun sleep-child-argv (root)
  "The child's program and arguments, as the memory tool's child is started (layer/child.lisp)."
  (if (symbol-value (find-symbol "*IMAGE-CHILD*" "OM.LAYER"))
      (list (namestring sb-ext:*runtime-pathname*) "--sleep-child" root)
      (list (namestring sb-ext:*runtime-pathname*) "--core" (namestring sb-ext:*core-pathname*)
            "--noinform" "--disable-ldb" "--lose-on-corruption" "--end-runtime-options"
            "--non-interactive" "--no-sysinit" "--no-userinit"
            "--load" (namestring (asdf:system-relative-pathname "om-engine" "load.lisp"))
            "--eval" (format nil "(om.server::sleep-child-main ~S)" root))))

(defun start-sleep-child (root)
  (handler-case
      (setf *sleep-child*
            (sb-ext:run-program (first (sleep-child-argv root)) (rest (sleep-child-argv root))
                                :input nil :output nil :error nil :wait nil :search nil))
    (error (e) (log-line "idle sleep: ~A" e) nil)))

(defun idle-sleep (s)
  "The accept loop's idle hook: starts the child once per idle period (no connection for
sleep-idle-seconds since the last activity), and kills a running child once a client connects."
  (let ((child *sleep-child*))
    (when child
      (cond ((not (sb-ext:process-alive-p child))
             (ignore-errors (sb-ext:process-close child))
             (setf *sleep-child* nil))
            ((plusp (connection-count))
             (ignore-errors (sb-ext:process-kill child 15))
             (ignore-errors (sb-ext:process-wait child))
             (ignore-errors (sb-ext:process-close child))
             (log-line "idle sleep: a client connected; stopped (checkpointed)")
             (setf *sleep-child* nil)))))
  (let ((secs (sleep-idle-seconds)))
    (when (and (plusp secs) (null *sleep-child*))
      (let ((at (sb-thread:with-mutex ((server-mutex s))
                  (and (null (server-connections s))
                       (>= (- (now-seconds) (server-last-activity s)) secs)
                       (not (eql *slept-at* (server-last-activity s)))
                       (server-last-activity s)))))
        (when (and at (sleep-idle-on-p (server-root s)))
          (setf *slept-at* at)
          (when (start-sleep-child (server-root s))
            (log-line "idle sleep: started")))))))

(setf *idle-hook* 'idle-sleep)
