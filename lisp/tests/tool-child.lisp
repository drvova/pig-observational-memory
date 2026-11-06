;;;; The memory tool in a child SBCL (docs/lisp-port-tool-layer.md, "The memory tool in a child"):
;;;; the round trip, the kill past the deadline, the child's heap cap, a write through the image's
;;;; lock, the image surviving a child that dies, the result's details and usage event, the
;;;; fixed texts' snippets and placeholders, and the Phase 0 trial's tasks solved with the Lisp
;;;; API (offline: reference answers, no model). They write only under a temporary directory.

(in-package "OM.LAYER")

(defun tool-run (code &key (branch #()) curated (model "test/child") strings index-shown)
  "memory-tool-run over a call: (values text is-error details), details decoded."
  (multiple-value-bind (text is-error details)
      (memory-tool-run (om::jobject "code" code "strings" (or strings :null))
                       :call (make-call :branch branch :curated-dir curated :model model :project "/tmp/om-child-project"
                                        :session "s1")
                       :index-shown index-shown)
    (values text is-error (om::json-parse (coerce details 'simple-string)))))

(defun seconds-of (thunk)
  (let ((start (get-internal-real-time)))
    (funcall thunk)
    (/ (- (get-internal-real-time) start) internal-time-units-per-second)))

(om::deftest child-round-trip
  (let ((branch (test-branch)) (*layer-root* (test-dir "child-layer")))
    (multiple-value-bind (text err d)
        (tool-run "(format t \"~a~%\" (mapcar #'note-id (notes))) (format t \"~a~%\" (mapcar #'turn-id (history :search \"usd\")))" :branch branch)
      (om::check text (format nil "(aaaaaaaaaaa1 aaaaaaaaaaa2)~%(a1 u1)~%"))
      (om::check err nil)
      (om::check (om::jget d "outcome") "ok")
      (om::check (coerce (om::jget d "hits") 'list) '("aaaaaaaaaaa1" "aaaaaaaaaaa2"))
      (om::check (coerce (om::jget d "searches") 'list) '("usd")))
    ;; one round trip per call that reads the image's state; accessors run in the child
    (let ((r (run-tool-child "(note-content (first (notes))) (length (notes :kind \"plan\")) (recall \"u1\")"
                             :call (make-call :branch branch :model "test/child"))))
      (om::check (cr-status r) "ok")
      (om::check (cr-text r) "[user] The billing job costs 40 USD a month.")
      (om::check (rec-calls (cr-record r)) 3)
      (om::check (rec-recalled (cr-record r)) '("u1")))
    ;; the last value only when nothing was printed; a record shows as its line
    (om::check (tool-run "(first (notes))" :branch branch) "[fact] The billing job costs 40 USD a month. u1")
    (om::check (tool-run "(princ 1) (+ 1 2)" :branch branch) "1")
    ;; an error the image answers reaches the code as an error it may handle
    (om::check (tool-run "(handler-case (recall \"nope\") (error (e) (format t \"caught: ~a\" e)))" :branch branch)
               "caught: nothing called \"nope\": no record id, entry id or saved name on this branch")
    ;; the strings argument
    (om::check (tool-run "(princ (text \"q\"))" :strings (om::jobject "q" "quoted \"text\"")) "quoted \"text\"")
    ;; a refusal is a compile outcome: nothing ran
    (multiple-value-bind (text err d) (tool-run "(eval 1)")
      (om::check text "Error: refused: eval is denied in model code")
      (om::check err t)
      (om::check (om::jget d "outcome") "compile"))
    ;; Go code is answered with the Lisp form
    (om::check (and (search "runs Common Lisp, not Go" (tool-run "x := om.Notes()")) t) t)))

(om::deftest child-kill-past-deadline
  ;; the child stops a runaway itself at its deadline...
  (multiple-value-bind (text err d) (let ((*tool-timeout-seconds* 1)) (tool-run "(loop)"))
    (om::check text "Error: the code ran past its 1s limit and was stopped")
    (om::check err t)
    (om::check (om::jget d "outcome") "runtime"))
  ;; ...and one that does not stop is a process kill
  (let (r)
    (let ((took (seconds-of (lambda () (setf r (run-tool-child "(loop)" :call (make-call :model "test/child")
                                                                         :timeout 60 :kill-after 1))))))
      (om::check (cr-status r) "crashed")
      (om::check (cr-error r) "the program ran past 1s and its process was killed")
      (om::check (< took 5) t)))
  (om::check (tool-run "(+ 1 1)") "2"))

(om::deftest child-heap-cap
  ;; the child's heap is its own, and small: filling it never reaches the image
  (let ((*child-heap-mb* 160) (before (sb-ext:dynamic-space-size)))
    (let ((r (run-tool-child "(let ((keep '())) (loop (push (make-array 1000000) keep)))"
                             :call (make-call :model "test/child") :timeout 20)))
      (om::check (not (string= (cr-status r) "ok")) t (cr-error r))
      (om::check (and (or (search "heap" (or (cr-error r) "")) (search "storage" (or (cr-error r) ""))
                          (search "process" (or (cr-error r) "")))
                      t)
                 t (cr-error r)))
    ;; with the cooperative check out of the way, the child dies of it, and only the child
    (let ((r (run-tool-child "(let ((keep '())) (loop (push (make-array 1000000) keep)))"
                             :call (make-call :model "test/child") :timeout 20 :heap-limit (expt 2 40))))
      (om::check (and (member (cr-status r) '("crashed" "runtime") :test #'string=) t) t (cr-error r)))
    (om::check (sb-ext:dynamic-space-size) before)
    (om::check (tool-run "(length (list 1 2 3))") "3")))

(om::deftest child-writes-through-the-image-lock
  (let* ((dir (test-dir "child-curated")) (held (sb-thread:make-semaphore)) (took 0))
    (ensure-dir dir)
    (let ((holder (sb-thread:make-thread
                   (lambda () (with-store-lock (dir) (sb-thread:signal-semaphore held) (sleep 1.5))))))
      (sb-thread:wait-on-semaphore held)
      (multiple-value-bind (text err d) (let (out)
                                          (setf took (seconds-of (lambda ()
                                                                   (setf out (multiple-value-list
                                                                              (tool-run "(save \"child-note\" \"project\" \"Saved from a child.\" :body \"The body.\")"
                                                                                        :curated dir))))))
                                          (values-list out))
        (sb-thread:join-thread holder)
        (om::check err nil text)
        ;; the save waited for the lock the image held
        (om::check (> took 1.0) t)
        (om::check (and (search "child-note" text) t) t text)
        (om::check (mem-description (get-as dir "child-note" "/tmp/om-child-project" "s1")) "Saved from a child.")
        (let ((w (aref (om::jget d "writes") 0)))
          (om::check (list (om::jget w "op") (om::jget w "name") (om::jget w "type") (om::jget w "description"))
                     '("save" "child-note" "project" "Saved from a child.")))
        ;; one code event in the model's usage ledger, a write counted ok
        (let ((events (read-model-events (join-path dir "models" (concatenate 'string (model-dir "test/child") ".jsonl")))))
          (om::check (mapcar (lambda (e) (list (om::jget e "kind") (om::jget e "outcome") (om::jget e "engine"))) events)
                     '(("code" "ok" "v2"))))))
    ;; curated.Check still refuses through the child
    (multiple-value-bind (text err) (tool-run "(save \"Bad Name\" \"project\" \"x\")" :curated dir)
      (om::check err t)
      (om::check (and (search "Error:" text) t) t text))))

(om::deftest child-crash-leaves-the-image
  ;; a child that dies in the middle of a call
  (let ((*child-command* (list "/bin/sh" "-c"
                               "echo '{\"ready\":true}'; read req; echo '{\"call\":\"notes\",\"args\":[]}'; read answer; kill -9 $$")))
    (let ((r (run-tool-child "(notes)" :call (make-call :model "test/child"))))
      (om::check (cr-status r) "crashed")
      (om::check (and (search "process ended" (cr-error r)) t) t (cr-error r))
      (om::check (rec-calls (cr-record r)) 1)))
  ;; one that writes something else than the wire
  (let ((*child-command* (list "/bin/sh" "-c" "echo '{\"ready\":true}'; read req; echo garbage; sleep 5")))
    (let ((r (run-tool-child "(notes)" :call (make-call :model "test/child"))))
      (om::check (cr-status r) "crashed")))
  ;; one that never starts
  (let ((*child-command* (list "/nonexistent/om-engine")))
    (multiple-value-bind (text err d) (tool-run "(+ 1 1)")
      (om::check err t)
      (om::check (om::jget d "outcome") "crashed")
      (om::check (and (search "crashed:" text) t) t text)))
  (om::check (tool-run "(* 6 7)") "42"))

(om::deftest child-snippets-and-the-manual
  (let ((branch (test-branch)))
    ;; a Go pointer the summary printed runs as the Lisp call it stands for
    (om::check (tool-run "om.Notes(om.Kind(`plan`))" :branch branch)
               (format nil ";; om.Notes(om.Kind(`plan`)) runs here as (notes :kind \"plan\")~%~A"
                       (tool-run "(notes :kind \"plan\")" :branch branch)))
    (om::check (tool-run "om.Recall(`u1`)" :branch branch)
               (format nil ";; om.Recall(`u1`) runs here as (recall \"u1\")~%[user] The billing job costs 40 USD a month."))
    (om::check (tool-run "(mapcar #'note-id (notes :from \"u2\" :through \"u2\"))" :branch branch) "(\"aaaaaaaaaaa2\")")
    (om::check (tool-run "(mapcar #'note-id (notes :through \"aaaaaaaaaaa1\"))" :branch branch) "(\"aaaaaaaaaaa1\")")
    (om::check (and (search "runs here as (notes :from \"u2\" :through \"u2\")"
                            (tool-run "om.Notes(om.From(`u2`).Through(`u2`))" :branch branch))
                    t)
               t)
    (om::check (go-snippet-lisp "om.History(om.Search(\"loader\").Limit(5))") "(history :limit 5 :search \"loader\")")
    (om::check (go-snippet-lisp "om.Files(om.Since(`2026-01-02`))") "(notes :kind \"file\" :since \"2026-01-02\")")
    (om::check (and (search "has no Lisp form" (tool-run "om.Reflections()" :branch branch)) t) t)
    ;; a fixed text's form run as it stands is refused, Go's and Lisp's alike
    (dolist (code '("om.Recall(`<id>`)" "(recall \"<id>\")" "(history :search \"<words>\")" "(saved :search \"<words>\")"))
      (multiple-value-bind (text err d) (tool-run code :branch branch)
        (om::check (and (om::has-prefix text "Error: nothing ran: `<") t) t text)
        (om::check err t)
        (om::check (om::jget d "outcome") "compile")))
    ;; empty code prints the manual, with the Go definitions listed and never run
    (let ((store (test-dir "child-code")))
      (ensure-dir store)
      (write-atomic (join-path store "units.json")
                    "[{\"name\":\"Count\",\"src\":\"func Count() int { return 1 }\",\"digest\":\"x\",\"written\":\"2026-01-01T00:00:00Z\"}]")
      (multiple-value-bind (text err d)
          (memory-tool-run (om::jobject "code" "  ") :call (make-call :model "m") :code-store store)
        (om::check err nil)
        (om::check (and (search "(notes &key search" text) t) t)
        (om::check (and (search "Kept by the Go memory tool, never run here: Count" text) t) t)
        (om::check (om::has-suffix d ",\"manual\":true}") t)))
    ;; the curated index a start context hands names the Lisp forms; its body is Go's
    (let ((text (lisp-index-text +empty-index+)))
      (om::check (om::has-prefix text +lisp-index-header+) t)
      (om::check (om::has-suffix text "No memories saved yet.") t)
      (om::check (search "om." text) nil))
    (om::check (lisp-index-text (format nil "x~%(2 more memories; find them with the memory tool's ~A.)" +snippet-saved-search+))
               (format nil "x~%(2 more memories; ~A" +lisp-more-line+))
    ;; every Lisp form the fixed texts name is refused as it stands, and runs once filled in
    (dolist (form (list "(save \"<name>\" \"<type>\" \"<description>\" :body \"<body>\")"
                        "(feedback \"<name>\" \"<outcome>\" \"<quote>\")" "(text \"<string name>\")"))
      (om::check (and (om::has-prefix (tool-run form) "Error: nothing ran:") t) t form))))

(om::deftest child-code-event-classified
  (let* ((dir (test-dir "child-event")) (model "test/event")
         (path (join-path dir "models" (concatenate 'string (model-dir model) ".jsonl")))
         (summary "[fact] The billing job costs 40 USD a month. u1")
         (branch (concatenate 'simple-vector (test-branch)
                              (vector (om::make-entry :type "compaction" :id "k1" :summary summary
                                                      :details (om::json-parse "{\"shown\":[\"aaaaaaaaaaa1\"],\"observations\":[{\"id\":\"aaaaaaaaaaa1\"},{\"id\":\"aaaaaaaaaaa2\"}]}"))))))
    (ensure-dir dir)
    (flet ((last-event () (let ((e (car (last (read-model-events path))))) (list (om::jget e "outcome") (om::jget e "target")))))
      (tool-run "(notes :kind \"fact\")" :branch branch :curated dir :model model)
      (om::check (last-event) '("shown" "summary"))
      (tool-run "(notes :kind \"plan\")" :branch branch :curated dir :model model)
      (om::check (last-event) '("omitted" "summary"))
      (tool-run "(notes :kind \"nothing\")" :branch branch :curated dir :model model)
      (om::check (last-event) '("empty" nil))
      (tool-run "(recall \"aaaaaaaaaaa1\")" :branch branch :curated dir :model model)
      (om::check (last-event) '("ok" nil))
      (tool-run "(car 1)" :branch branch :curated dir :model model)
      (om::check (last-event) '("error" nil))
      (om::check (length (read-model-events path)) 5)
      ;; no model or no store: nothing is counted
      (tool-run "(notes)" :branch branch :curated dir :model "")
      (om::check (length (read-model-events path)) 5))))

;;; The Phase 0 trial's tasks through the Lisp tool (docs/lisp-measurements.md)

(defun trial-file (name)
  (namestring (asdf:system-relative-pathname "om-engine" (concatenate 'string "../bench/lisp/trial/" name))))

(om::deftest child-trial-tasks-solvable
  (let* ((root (test-dir "trial-root")) (repo (test-dir "trial-repo"))
         (answers (om::json-parse (coerce (read-file (trial-file "lisp-tool.json")) 'simple-string)))
         (expected (om::json-parse (coerce (read-file (trial-file "expected.json")) 'simple-string)))
         (branch (let ((om.server::*root* root))
                   (ensure-dir repo)
                   (let* ((s (om.server::find-session "trial" "s1"))
                          (params (om::jobject "transcript_path" (trial-file "memory.jsonl") "format" "generic" "cwd" repo)))
                     (om.server::turn-end-level-0 s params)
                     (om.server::read-branch s params "generic"))))
         (solved 0))
    (dolist (id (sort (loop for k being the hash-keys of expected collect k) #'string<))
      (multiple-value-bind (text err) (tool-run (om::jget answers id) :branch branch)
        (let ((got (string-right-trim '(#\Newline #\Space) text)))
          (om::check (list id err got) (list id nil (om::jget expected id)))
          (when (and (not err) (string= got (om::jget expected id))) (incf solved)))))
    (om::check solved 20)))

(om::deftest child-cleanup
  (sb-ext:run-program "/bin/rm" (list "-rf" *test-root*))
  (om::check (probe-file *test-root*) nil))
