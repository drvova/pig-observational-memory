;;;; The layer store across processes and crashes (docs/lisp-port-tool-layer.md, "The layer
;;;; across processes" and "Durable appends"): a durable append ends a torn tail and never glues
;;;; two lines; a generation number is never written twice, even after a manifest is torn
;;;; mid-line; a manifest written before (count-numbered) reads as it did; and a change another
;;;; process makes to a model's layer reaches a session at its next boundary (a session start or
;;;; a compaction) and never between two, each session pinned on its own. They write only under a
;;;; temporary directory; one test starts a second SBCL.

(in-package "OM.LAYER")

(defvar *ls-root*
  (format nil "/tmp/om-layer-store-test-~D-~D/" (sb-posix:getpid) (random 1000000 (make-random-state t))))

(defvar *ls-load-file*
  (namestring (merge-pathnames "../load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*))))

(defun ls-dir (name) (join-path *ls-root* name))

(defun write-bytes (path text)
  (ensure-dir (directory-namestring path))
  (with-open-file (out path :direction :output :if-exists :supersede :external-format :utf-8)
    (write-string text out)))

(defun tear-last-line (path keep)
  "Leaves path's last line as its first keep characters, with no newline: a crash mid-write."
  (let* ((text (string-right-trim '(#\Newline) (read-file path)))
         (start (let ((p (position #\Newline text :from-end t))) (if p (1+ p) 0))))
    (write-bytes path (subseq text 0 (+ start keep)))))

(defun manifest-gens (model) (mapcar (lambda (g) (om::jget g "gen")) (read-manifest model)))

(om::deftest append-jsonl-ends-a-torn-tail
  (let ((path (ls-dir "append/a.jsonl")))
    (ensure-dir (ls-dir "append"))
    (om::check (om::append-jsonl path "{\"a\":1}") nil "a new file")
    (om::check (logand (sb-posix:stat-mode (sb-posix:stat path)) #o777) #o600)
    (om::check (om::append-jsonl path "{\"a\":2}") nil "a clean tail")
    (write-bytes path (concatenate 'string (read-file path) "{\"a\":3,\"b"))
    (om::check (om::torn-tail-p path) t)
    (om::check (om::append-jsonl path "{\"a\":4}") t "a torn tail")
    (om::check (read-file path) (format nil "{\"a\":1}~%{\"a\":2}~%{\"a\":3,\"b~%{\"a\":4}~%")
               "the fragment is a line of its own, nothing truncated")
    (om::check (mapcar (lambda (e) (truncate (om::jget e "a"))) (read-model-events path)) '(1 2 4))
    ;; a complete last line without its newline stays the line every reader read
    (write-bytes path "{\"a\":5}")
    (om::check (om::append-jsonl path "{\"a\":6}") t)
    (om::check (read-file path) (format nil "{\"a\":5}~%{\"a\":6}~%"))))

(defun hold-text (n) (format nil "(define test-double (xs) (mapcar (lambda (x) (* ~D x)) xs))" n))

(om::deftest manifest-never-reuses-a-generation
  (let ((*layer-root* (ls-dir "torn")) (model "acme/torn"))
    (layer-hold model (hold-text 3))
    (layer-hold model (hold-text 4))
    (om::check (manifest-gens model) '(1 2))
    ;; a crash tears generation 2 mid-line: it is skipped, and its number stays taken
    (tear-last-line (manifest-path model) 12)
    (om::check (manifest-gens model) '(1))
    (om::check (layer-generations model) 1)
    (om::check (next-generation model) 3 "the torn line's {\"gen\":2 is never numbered again")
    (layer-hold model (hold-text 5))
    (om::check (manifest-gens model) '(1 3))
    (om::check (length (om::split (string-right-trim '(#\Newline) (read-file (manifest-path model))) (string #\Newline))) 3
               "the fragment kept as its own line")
    (om::check (layer-generations model) 3)
    ;; replay holds the state before a torn number at it; a rollback past the last is refused
    (om::check (length (replay (read-manifest model))) 4)
    (om::check (handler-case (layer-rollback model 4) (refused (e) (refused-reason e)))
               "generation 4 is not one of acme/torn's layer: 0 to 3")
    (om::check (layer-rollback model 0) 4)
    ;; torn before its number: nothing claimed, the next is after the last recorded
    (tear-last-line (manifest-path model) 4)
    (om::check (next-generation model) 4)))

(om::deftest manifest-reads-count-numbered-lines
  ;; A manifest a count-numbering writer wrote after a torn line (gen 2 written twice) reads as
  ;; it always did: in order, each above the line before.
  (let ((*layer-root* (ls-dir "legacy")) (model "acme/legacy"))
    (write-bytes (manifest-path model)
                 (format nil "~{~A~%~}"
                         '("{\"gen\":1,\"time\":\"2026-11-01T00:00:00Z\",\"op\":\"settle\",\"kind\":\"adaptable\",\"name\":\"a\",\"digest\":\"0000000000000001\"}"
                           "{\"gen\":2,\"time\":\"2026-11-01T0"
                           "{\"gen\":2,\"time\":\"2026-11-01T00:00:01Z\",\"op\":\"settle\",\"kind\":\"adaptable\",\"name\":\"b\",\"digest\":\"0000000000000002\"}"
                           "{\"gen\":2,\"time\":\"2026-11-01T00:00:02Z\",\"op\":\"forget\",\"kind\":\"adaptable\",\"name\":\"a\"}"
                           "{\"time\":\"2026-11-01T00:00:03Z\",\"op\":\"rollback\",\"target\":2}")))
    (om::check (manifest-gens model) '(1 2 3 4))
    (om::check (current-state model) '(("b" . "0000000000000002") ("a" . "0000000000000001")))
    (om::check (settle-generation model "b" "0000000000000002") 2)
    (om::check (next-generation model) 5)))

(defun ls-double () (om.engine::test-double '(1 2)))

(om::deftest layer-pins-each-session-until-its-boundary
  ;; An outside change (append-generation alone drops no cache, as another process's would not)
  ;; reaches a session at its own boundary only.
  (let ((*layer-root* (ls-dir "pins")) (model "acme/pins"))
    (let ((d (at-time ("2026-11-02T09:00:00Z") (layer-hold model (hold-text 3)))))
      (layer-settle model (evidence-for model (list (ldef-held-id d)))))
    (layer-boundary model "a")
    (layer-boundary model "b")
    (with-call ((make-call :model model :session "a")) (om::check (ls-double) '(3 6) "a: settled"))
    (with-call ((make-call :model model :session "b")) (om::check (ls-double) '(3 6) "b: settled"))
    (with-layer-lock (model)
      (append-generation model "forget" :name "test-double" :digest (cdr (assoc "test-double" (current-state model) :test #'equal)))
      (mirror-settled model (current-state model)))
    (with-call ((make-call :model model :session "a")) (om::check (ls-double) '(3 6) "a: pinned between boundaries"))
    (layer-boundary model "a")
    (with-call ((make-call :model model :session "a")) (om::check (ls-double) '(2 4) "a: its boundary reads the change"))
    (with-call ((make-call :model model :session "b")) (om::check (ls-double) '(3 6) "b: a's boundary leaves b pinned"))
    (layer-boundary model "b")
    (with-call ((make-call :model model :session "b")) (om::check (ls-double) '(2 4) "b: at its own boundary"))
    ;; a boundary with nothing changed keeps the loaded layer
    (let ((l (layer-boundary model "a"))) (om::check (eq (layer-boundary model "a") l) t))
    ;; a change this image makes stands from the next call
    (layer-rollback model 2)
    (with-call ((make-call :model model :session "b")) (om::check (ls-double) '(3 6) "this image's own change at once"))
    (forget-session-pins "a")
    (forget-session-pins "b")))

(defun second-image (root form)
  "Runs form (a string, read in OM.LAYER) in another SBCL with OM_LAYER_ROOT root: its output."
  (let* ((out (make-string-output-stream))
         (p (sb-ext:run-program "sbcl"
                                (list "--noinform" "--non-interactive" "--no-userinit" "--load" *ls-load-file*
                                      "--eval" "(in-package \"OM.LAYER\")" "--eval" "(setf *print-pretty* nil)" "--eval" form)
                                :search t :output out :error out
                                :environment (cons (concatenate 'string "OM_LAYER_ROOT=" root) (sb-ext:posix-environ)))))
    (values (get-output-stream-string out) (sb-ext:process-exit-code p))))

(om::deftest layer-settle-in-another-image
  ;; Two images on one layer: a settle in the second reaches the first at its next compaction
  ;; (layer-boundary), not before; and the second numbers its generations after the first's,
  ;; a torn one included.
  (let* ((root (ls-dir "two")) (*layer-root* root) (model "acme/two"))
    (layer-hold model (hold-text 7))
    (tear-last-line (manifest-path model) 9)
    (layer-boundary model "s")
    (with-call ((make-call :model model :session "s")) (om::check (ls-double) '(2 4) "the default before"))
    (multiple-value-bind (output code)
        (second-image root
                      (format nil "(progn
  (define-adaptable om.engine::test-double (xs)
      (:check (lambda (result xs) (unless (and (listp result) (= (length result) (length xs))) \"a result per element\")))
    \"Doubles every element (a test adaptable).\"
    (mapcar (lambda (x) (* 2 x)) xs))
  (let ((d (let ((*now* (let ((tm (parse-rfc3339 \"2026-11-02T09:00:00Z\"))) (lambda () tm)))) (layer-hold ~S ~S))))
    (format t \"~~&settled: ~~S gens ~~S~~%\"
            (layer-settle ~S (list (om::jobject \"time\" \"2026-11-02T10:00:00Z\" \"kind\" \"pass\" \"engine\" \"v2\" \"stage\" \"compaction\"
                                                \"tokens\" 100 \"outcome\" \"\" \"ranHeld\" (vector (ldef-held-id d)))))
            (mapcar (lambda (g) (om::jget g \"gen\")) (read-manifest ~S)))))"
                              model (hold-text 3) model model))
      (om::check code 0 output)
      (om::check (and (search "settled: ((\"test-double\" T" output) (search "gens (2 3)" output) t) t output))
    (with-call ((make-call :model model :session "s"))
      (om::check (ls-double) '(2 4) "the other image's settle is not seen between boundaries"))
    (layer-boundary model "s")
    (with-call ((make-call :model model :session "s"))
      (om::check (ls-double) '(3 6) "seen at the next compaction"))
    (forget-session-pins "s")))

(om::deftest layer-store-cleanup
  (sb-ext:delete-directory *ls-root* :recursive t))
