;;;; W4's tests: the safe reader (each denied form refused), the runner (output rule, timeout,
;;;; heap), adaptable dispatch, the layer (hold, settle, revert, forget, rollback), the evidence
;;;; rule, the curated store and the om.tool reads. They write only under a temporary directory.

(in-package "OM.LAYER")

(defvar *test-root*
  (format nil "/tmp/om-w4-test-~D-~D/" (sb-posix:getpid) (random 1000000 (make-random-state t))))

(defun test-dir (name) (join-path *test-root* name))

(defun fixed-time (s) (parse-rfc3339 s))

(defmacro at-time ((s) &body body)
  `(let ((*now* (let ((tm (fixed-time ,s))) (lambda () tm)))) ,@body))

(defun refused-p (text)
  (handler-case (progn (safe-read-all text (model-package "test/reader")) nil)
    (refused () t)))

(defun run (text &key (model "test/run") curated branch (timeout 2))
  (let ((*timeout-seconds* timeout))
    (run-tool-code text :call (make-call :model model :curated-dir curated
                                         :branch (or branch #())))))

;;; The safe reader

(om::deftest reader-refuses-denied-forms
  (dolist (text '("(eval '(+ 1 2))" "(compile nil '(lambda () 1))" "(load \"x.lisp\")"
                  "(open \"/etc/passwd\")" "(with-open-file (s \"x\") s)" "(delete-file \"x\")"
                  "(run-program \"/bin/ls\" '())" "(intern \"X\")" "(find-symbol \"CAR\")"
                  "(symbol-function 'car)" "(fdefinition 'car)" "(find-package \"CL\")"
                  "(in-package \"CL-USER\")" "(read-from-string \"(1)\")" "(make-package \"P\")"
                  "(set 'x 1)" "(symbol-value 'x)" "(unwind-protect 1 2)" "(eval-when () 1)"
                  "(defmacro m () 1)" "(declaim (optimize (safety 0)))"
                  "(locally (declare (optimize (safety 0))) 1)" "(load-time-value 1)"
                  "(macrolet ((m () 1)) (m))" "(sleep 1)" "(make-instance 'x)"
                  "#.(+ 1 2)" "#+sbcl 1" "#-sbcl 1" "#:foo" "#p\"/tmp\"" "#s(foo)" "#1=(1 . #1#)"
                  "#a((1))" "(cl:car '(1))" "(cl::eval 1)" "(om.layer::refuse \"x\")" "(sb-ext:quit)"
                  "|EVAL|" "ev\\al" "(quit)" "(getenv \"HOME\")" "(defun car (x) x)"
                  "(defvar *print-length* 1)" "(defparameter notes 1)" "'(a ,b)" "``(a)"
                  "(loop for s being the external-symbols of \"SB-EXT\" collect s)"
                  "(loop for s being each symbol of \"CL\" collect s)" "(symbol-name 'car)"))
    (om::check (refused-p text) t text)))

(om::deftest reader-reads-model-code
  (om::check (refused-p "(loop for x in '(1 2) collect (* x x))") nil)
  (om::check (refused-p "(let ((v #(1 2)) (c #\\a) (s #\\Space)) (list v c s #x1f #b101 1/2 1.5 -3))") nil)
  (om::check (refused-p "(defun helper (x) (format nil \"~a\" x)) #| a comment |# ; and this") nil)
  (om::check (refused-p "`(a ,(+ 1 2) ,@(list 3) . ,(list 4))") nil)
  (om::check (tool-result-text (run "`(a ,(+ 1 2) ,@(list 3 4) . 5)")) "(a 3 3 4 . 5)")
  (om::check (tool-result-text (run "(list #\\a #x1f 1/2 :key \"s\")")) "(#\\a 31 1/2 :key \"s\")")
  ;; a refusal names the reason and nothing runs
  (let ((r (run "(save \"x\" \"user\" \"never saved\") (eval 1)")))
    (om::check (tool-result-error r) "refused: eval is denied in model code")))

(om::deftest reader-checks-format-controls
  (om::check (tool-result-error (run "(format nil \"~/cl-user::foo/\" 1)"))
             "refused: the format directive ~/, which calls or reads code")
  (om::check (tool-result-error (run "(format nil \"~?\" \"~a\" '(1))"))
             "refused: the format directive ~?, which calls or reads code")
  (om::check (tool-result-text (run "(format nil \"~a-~5,'0d\" \"x\" 42)")) "x-00042")
  (om::check (tool-result-error (run "(error \"~/x/\")"))
             "refused: the format directive ~/, which calls or reads code")
  (om::check (tool-result-error (run "(error \"plain ~a\" 1)")) "plain 1"))

(om::deftest reader-keeps-om-tool-locked
  (om::check (tool-result-text (run "(let ((notes 3)) notes)")) "3")
  (om::check (and (search "Lock on package OM.TOOL" (or (tool-result-error (run "(flet ((notes () 1)) (notes))")) "")) t) t)
  (om::check (and (search "Lock on package" (or (tool-result-error (run "(defun om.tool::notes () 1)")) "refused")) t) nil))

;;; The runner

(om::deftest runner-output-rule
  (om::check (tool-result-text (run "(+ 1 2)")) "3")
  (om::check (tool-result-text (run "(print 5) 7")) (format nil "~%5 "))
  (om::check (tool-result-text (run "(write-string \"hi\") (+ 1 2)")) "hi")
  (om::check (tool-result-text (run "\"a string\"")) "a string")
  (om::check (tool-result-text (run "(defun sq (x) (* x x)) (sq 4)" :model "test/defs")) "16")
  ;; the model's own definitions stay in its package for its later calls
  (om::check (tool-result-text (run "(sq 5)" :model "test/defs")) "25")
  (om::check (tool-result-error (run "(sq 5)" :model "test/other")) "The function om.layer.test_2fother::sq is undefined."))

(om::deftest runner-stops-runaways
  (let ((start (get-internal-real-time)))
    (om::check (tool-result-error (run "(loop)" :timeout 0.3)) "the code ran past its 0.3s limit and was stopped")
    (om::check (< (/ (- (get-internal-real-time) start) internal-time-units-per-second) 3) t "stopped in time"))
  (om::check (tool-result-error (run "(defun f (n) (+ 1 (f n))) (f 0)"))
             "storage exhausted: control-stack-exhausted")
  (om::check (tool-result-error (run "(make-array 100000000)"))
             "refused: an allocation of 100000000 elements (at most 16777216)")
  (let ((*heap-limit-bytes* (* 64 1024 1024)))
    (om::check (tool-result-error (run "(let ((l '())) (loop (push (make-array 1000000) l)))" :timeout 20))
               "the code grew the heap past 64 MB and was stopped"))
  ;; the image goes on
  (om::check (tool-result-text (run "(+ 2 2)")) "4"))

;;; The evidence rule

(defun pass-event (time &key (tokens 100) ran (level1 nil) counts (outcome "ok") (kind "pass") errors cut)
  (let ((e (om::jobject "time" time "kind" kind "engine" "v2" "outcome" outcome "tokens" (float tokens 1d0))))
    (when (string= kind "pass") (setf (gethash "stage" e) "compaction"))
    (when ran (setf (gethash "ranHeld" e) (coerce ran 'simple-vector)))
    (when level1 (setf (gethash "level1" e) :true))
    (when counts (setf (gethash "counts" e) (apply #'om::jobject (loop for (k . n) in counts append (list k (float n 1d0))))))
    (when errors (setf (gethash "errors" e) (apply #'om::jobject (loop for (k . v) in errors append (list k v)))))
    (when cut (setf (gethash "cut" e) cut))
    e))

(om::deftest settle-v2-rule
  (let ((held (fixed-time "2026-11-01T10:00:00Z")) (id "instructions/consolidation@0123456789abcdef"))
    (flet ((settle (events &rest keys)
             (multiple-value-list (apply #'settle-v2 events :held-id id :time held keys))))
      ;; no evidence: the compaction before the hold, an empty one, one that did not run it
      (om::check (settle (list (pass-event "2026-11-01T09:00:00Z" :ran (list id) :level1 t)
                               (pass-event "2026-11-01T11:00:00Z" :ran (list id) :level1 t :outcome "empty")
                               (pass-event "2026-11-01T11:00:00Z" :ran '("other") :level1 t))
                         :kind "instructions" :name "consolidation")
                 '(nil nil ""))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :level1 t)) :kind "instructions" :name "consolidation")
                 '(t t "level 1 ran under it at 1 later compaction with no loss counted"))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :level1 t :counts '(("level1-union-kept" . 2)))
                               (pass-event "2026-11-01T12:00:00Z" :ran (list id) :level1 t :counts '(("level1-failed" . 1))))
                         :kind "instructions" :name "consolidation")
                 '(t nil "level 1 ran under it at 2 later compactions and its code checks counted level1-failed 1, level1-union-kept 2"))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :tokens 0)) :kind "remedy" :name "keep-head")
                 '(nil nil ""))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :tokens 120)
                               (pass-event "2026-11-01T12:00:00Z" :ran (list id) :tokens 30))
                         :kind "remedy" :name "keep-head")
                 '(t t "the turns under it (150 source tokens over 2 later compactions) counted no failure it could cause"))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :kind "detector-pass" :counts '(("defined:d" . 0))))
                         :kind "detector" :name "d" :detector :code)
                 '(t t "it counted at 1 later detector pass with no error"))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :kind "detector-pass" :errors '(("d" . "panic")) :cut "e9"))
                         :kind "detector" :name "d" :detector :code)
                 '(t nil "its run after the compaction at e9 gave no count: panic"))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :kind "detector-pass" :counts '(("defined:d" . 3))))
                         :kind "detector" :name "d" :detector :retired)
                 '(nil nil ""))
      (om::check (settle (list (pass-event "2026-11-01T11:00:00Z" :ran (list id) :counts '(("adaptable-failed:rank-notes" . 1))))
                         :kind "adaptable" :name "rank-notes")
                 '(t nil "it failed its kernel check, errored or timed out 1 time over 1 later compaction"))
      ;; sub-millisecond order is kept
      (om::check (settle (list (pass-event "2026-11-01T10:00:00.000000001Z" :ran (list id))) :kind "remedy" :name "keep-head")
                 '(t t "the turns under it (100 source tokens over 1 later compaction) counted no failure it could cause"))
      (om::check (settle (list (pass-event "2026-11-01T10:00:00Z" :ran (list id))) :kind "remedy" :name "keep-head")
                 '(nil nil "")))))

;;; Adaptable dispatch and the layer

(define-adaptable om.engine::test-double (xs)
    (:check (lambda (result xs) (unless (and (listp result) (= (length result) (length xs))) "a result per element")))
  "Doubles every element (a test adaptable)."
  (mapcar (lambda (x) (* 2 x)) xs))

(defun evidence-for (model ran &key (time "2026-11-02T10:00:00Z") counts)
  (list (pass-event time :ran ran :counts counts)))

(om::deftest layer-dispatch-settle-revert
  (let ((*layer-root* (test-dir "layer1")) (model "acme/dispatch"))
    (with-call ((make-call :model model))
      (layer-take-evidence model)
      (om::check (om.engine::test-double '(1 2)) '(2 4) "the default with an empty layer")
      ;; hold: the default's result stands; the held one runs beside it
      (let ((d (at-time ("2026-11-02T09:00:00Z")
                 (layer-hold model "(define test-double (xs) (mapcar (lambda (x) (* 3 x)) xs))"))))
        (om::check (ldef-name d) "test-double")
        (om::check (om.engine::test-double '(1 2)) '(2 4) "held: the default's result")
        (om::check (layer-ran-held model) (list (ldef-held-id d)))
        ;; no evidence yet: it stays held
        (om::check (layer-settle model (evidence-for model '("other"))) '())
        (om::check (mapcar #'ldef-name (layer-held model)) '("test-double"))
        ;; a later compaction that ran it with tokens settles it, and it runs from then on
        (om::check (layer-settle model (evidence-for model (list (ldef-held-id d))))
                   '(("test-double" t "the turns under it (100 source tokens over 1 later compaction) counted no failure it could cause")))
        (om::check (om.engine::test-double '(1 2)) '(3 6) "settled: the definition's result")
        (om::check (read-file (settled-path model "test-double")) (ldef-text d)))
      ;; a held definition that fails its check is counted and reverted; the settled one stands
      (layer-take-evidence model)
      (let ((bad (at-time ("2026-11-02T11:00:00Z") (layer-hold model "(define test-double (xs) (list 1))"))))
        (om::check (om.engine::test-double '(1 2)) '(3 6))
        (multiple-value-bind (ran failures) (layer-take-evidence model)
          (om::check ran (list (ldef-held-id bad)))
          (om::check failures '(("adaptable-failed:test-double" . 1))))
        (om::check (layer-settle model (evidence-for model (list (ldef-held-id bad)) :time "2026-11-02T12:00:00Z"
                                                     :counts '(("adaptable-failed:test-double" . 1))))
                   '(("test-double" nil "it failed its kernel check, errored or timed out 1 time over 1 later compaction")))
        (om::check (layer-held model) '())
        (om::check (om.engine::test-double '(1 2)) '(3 6)))
      ;; forget restores the default at once; rollback brings the settled one back
      (let ((before (layer-generations model)))
        (om::check (integerp (layer-forget model "test-double")) t)
        (om::check (om.engine::test-double '(1 2)) '(2 4) "forgotten: the default")
        (om::check (probe-file (settled-path model "test-double")) nil)
        (layer-rollback model before)
        (om::check (om.engine::test-double '(1 2)) '(3 6) "rolled back: the settled one")
        (layer-rollback model 0)
        (om::check (om.engine::test-double '(1 2)) '(2 4) "rolled back to 0: the default"))
      (om::check (handler-case (layer-forget model "test-double") (refused (e) (refused-reason e)))
                 "acme/dispatch's layer holds no definition of test-double"))))

(om::deftest layer-falls-back-on-failure
  (let ((*layer-root* (test-dir "layer2")) (model "acme/fallback") (*definition-timeout* 0.3))
    (with-call ((make-call :model model))
      (dolist (text '("(define test-double (xs) (list 1))"
                      "(define test-double (xs) (error \"no\"))"
                      "(define test-double (xs) (loop))"))
        (layer-rollback model 0)
        (layer-take-evidence model)
        (let ((d (at-time ("2026-11-03T09:00:00Z") (layer-hold model text))))
          (layer-settle model (evidence-for model (list (ldef-held-id d)) :time "2026-11-03T10:00:00Z"))
          (om::check (mapcar #'ldef-name (layer-settled model)) '("test-double") text)
          (layer-take-evidence model)
          (om::check (om.engine::test-double '(1 2)) '(2 4) text)
          (om::check (layer-failures model) '(("adaptable-failed:test-double" . 1)) text))))))

(om::deftest layer-refuses-bad-definitions
  (let ((*layer-root* (test-dir "layer3")) (model "acme/refuse"))
    (flet ((why (text) (handler-case (progn (layer-hold model text) nil) (refused (e) (refused-reason e)))))
      (om::check (why "(define nothing-adaptable (x) x)")
                 "nothing-adaptable is not an adaptable function; they are phrase-note, rank-notes, test-double")
      (om::check (why "(define test-double (a b) a)") "test-double takes 1 argument")
      (om::check (why "(define test-double (xs) (eval xs))") "eval is denied in model code")
      (om::check (why "(defun test-double (xs) xs)") "a definition is one (define name (parameters) body...) form")
      (om::check (why "(define test-double (&rest xs) xs)") "the parameters of test-double are plain names")
      (om::check (layer-held model) '()))
    ;; a held file changed outside the store is never loaded
    (let ((d (layer-hold model "(define test-double (xs) xs)")))
      (write-atomic (held-path model "test-double") "(define test-double (xs) (reverse xs))")
      (invalidate model)
      (om::check (layer-held model) '())
      (om::check (loaded-reports (load-layer model)) '("held test-double: changed outside the store, never loaded"))
      (om::check (ldef-name d) "test-double"))))

(om::deftest layer-through-the-memory-tool
  (let ((*layer-root* (test-dir "layer4")) (model "acme/tool"))
    (let ((r (run "(define rank-notes (notes words) (reverse notes))" :model model)))
      (om::check (tool-result-error r) nil)
      (om::check (om::has-prefix (tool-result-text r) "Held definition rank-notes for acme/tool") t))
    (om::check (tool-result-text (run "(layer)" :model model))
               (format nil "(\"held rank-notes ~A\" \"generation 1\")" (digest "(define rank-notes (notes words) (reverse notes))")))
    (om::check (tool-result-text (run "(layer-forget 'rank-notes)" :model model))
               "Forgot rank-notes from acme/tool's layer (generation 2); the default stands.")
    (om::check (tool-result-error (run "(let ((x 1)) (define rank-notes (n w) n))" :model model))
               "(define rank-notes ...) is a top-level form of the memory tool")
    (om::check (tool-result-text (run "(layer-rollback 0)" :model model))
               "Rolled acme/tool's layer back to generation 0 (generation 3).")
    ;; the manifest's lines carry the schema's members
    (let ((lines (read-manifest model)))
      (om::check (mapcar (lambda (g) (gethash "op" g)) lines) '("hold" "forget" "rollback"))
      (om::check (gethash "kind" (first lines)) "adaptable")
      (om::check (gethash "target" (third lines)) 0d0))))

;;; The curated store

(defun args (&rest pairs)
  (let ((h (make-hash-table :test 'equal)))
    (loop for (k v) on pairs by #'cddr do (setf (gethash k h) v))
    h))

(om::deftest curated-check-refuses
  (flet ((why (&rest keys)
           (handler-case (progn (curated-check (apply #'make-memory keys)) nil)
             (curated-error (e) (curated-error-message e)))))
    (om::check (why :name "Bad" :type "user" :description "d")
               "memory name \"Bad\" must be lowercase letters, digits and hyphens, starting with a letter or digit, at most 64 characters")
    (om::check (why :name "ok" :type "nope" :description "d")
               "memory type \"nope\" must be one of user, feedback, project, reference, lesson")
    (om::check (why :name "ok" :type "user" :description (format nil "two~%lines"))
               "memory \"ok\" needs a one-line description")
    (om::check (why :name "ok" :type "user" :description "d" :staged-by '("s1"))
               "memory \"ok\" names staging sessions but is not staged")
    (om::check (why :name "ok" :type "user" :description "d" :body (make-string 10000 :initial-element #\x))
               "memory \"ok\" is 10052 characters as recall shows it, and one recall shows at most 10000: shorten the body")
    (om::check (why :name "ok" :type "user" :description "d") nil)))

(om::deftest curated-writes
  (let* ((dir (test-dir "curated1"))
         (c (make-curated-call :dir dir :project "/work" :source "s1"
                               :user-evidence (lambda (q) (quoted-in-p '("I always drink green tea in the morning") q)))))
    (at-time ("2026-11-01T12:00:00Z")
      (om::check (multiple-value-list (curated-run c "save" (args "name" "tea" "type" "user" "description" "Drinks  green tea" "quote" "always drink GREEN tea")))
                 '("Saved memory tea [user]: Drinks green tea" nil))
      (om::check (read-file (store-path dir "tea"))
                 (format nil "---~%type: user~%description: Drinks green tea~%sources: s1~%created: 2026-11-01T12:00:00Z~%updated: 2026-11-01T12:00:00Z~%---~%"))
      (om::check (multiple-value-list (curated-run c "save" (args "name" "tea" "type" "user" "description" "x")))
                 '(nil "a memory named \"tea\" already exists; change it with edit name=tea instead"))
      (om::check (nth-value 1 (curated-run c "save" (args "name" "cafe" "type" "user" "description" "x" "quote" "short")))
                 "save's quote needs quote=\"...\": at least 8 characters from this conversation that show the memory helped or misled you")
      (om::check (nth-value 1 (curated-run c "save" (args "name" "cafe" "type" "user" "description" "x" "quote" "never said by the user")))
                 "the quote is not a user's words in this conversation: copy words verbatim from a user message that confirm the memory, or save without quote to stage it")
      (om::check (curated-run c "save" (args "name" "proj" "type" "project" "description" "Builds with make" "scope" "project" "body" "  Run make all.  "))
                 "Staged memory proj [project] (this project): Builds with make. It shows in saved in this session and stays out of the memory index until evidence confirms it: a user's words (feedback name=proj outcome=helped quote=\"...\"), a compaction citing it, or the same save in another session."))
    (at-time ("2026-11-02T08:30:00Z")
      (om::check (curated-run (make-curated-call :dir dir :project "/work" :source "s2") "save"
                              (args "name" "proj" "type" "project" "description" "builds  WITH make" "scope" "project"))
                 "Staged memory proj [project] (this project): Builds with make. Another session staged the same memory, which is the evidence to make it live: it enters the memory index at its next render.")
      (om::check (curated-run c "edit" (args "name" "tea" "description" "Drinks green tea daily" "type" ""))
                 "Updated memory tea [user]: Drinks green tea daily")
      (om::check (nth-value 1 (curated-run c "edit" (args "name" "tea" "type" " "))) "nothing to change: give a new type, description or body")
      (om::check (curated-run c "feedback" (args "name" "tea" "outcome" "helped" "quote" "drink green tea"))
                 "Recorded that memory tea helped.")
      (om::check (curated-describe (get-memory dir "proj"))
                 (format nil "proj [project] (this project) (staged): Builds with make~%~%Run make all.~%~%saved 2026-11-01, updated 2026-11-02, sessions: s1, s2"))
      (om::check (curated-run c "forget" (args "name" "tea")) "Forgot memory tea.")
      (om::check (nth-value 1 (curated-run c "forget" (args "name" "tea"))) "no memory by that name")
      (om::check (mapcar #'mem-name (list-memories dir "/work")) '("proj"))
      (om::check (mapcar #'mem-name (list-memories dir "/elsewhere")) '()))
    (om::check (mapcar (lambda (g) (gethash "event" g)) (read-model-events (usage-path dir)))
               '("save" "save" "staged" "edit" "helped" "forget"))
    (om::check (first (om::split (read-file (usage-path dir)) (string #\Newline)))
               "{\"time\":\"2026-11-01T12:00:00Z\",\"name\":\"tea\",\"event\":\"save\"}")
    ;; a file with a field no memory takes is refused on load, and listed nowhere
    (write-atomic (store-path dir "odd") (format nil "---~%type: user~%description: d~%colour: red~%created: 2026-11-01T12:00:00Z~%updated: 2026-11-01T12:00:00Z~%---~%"))
    (om::check (handler-case (get-memory dir "odd") (curated-error (e) (curated-error-message e)))
               "memory \"odd\" has fields no memory takes: colour")))

;;; The reads

(defun msg (id role text &optional (time "2026-11-01T10:00:00Z"))
  (om::entry-from-map (om::jobject "type" "message" "id" id "timestamp" time
                                   "message" (om::jobject "role" role "content" text))))

(defun recorded (id covers &rest observations)
  (om::entry-from-map
   (om::jobject "type" "custom" "id" id "customType" "om.observations.recorded"
                "data" (om::jobject "coversUpToId" covers
                                    "observations" (coerce observations 'simple-vector)))))

(defun obs (id content source &key (kind "fact"))
  (om::jobject "id" id "content" content "timestamp" "2026-11-01T10:00:00Z" "relevance" "high"
               "sourceEntryIds" (vector source) "tokenCount" 5d0 "kind" kind "origin" "code"))

(defun test-branch ()
  (vector (msg "u1" "user" "The billing job costs 40 USD a month.")
          (msg "a1" "assistant" "Noted: billing at 40 USD.")
          (msg "u2" "user" "We will move the queue to Bo next Friday.")
          (recorded "r1" "u2" (obs "aaaaaaaaaaa1" "The billing job costs 40 USD a month." "u1")
                    (obs "aaaaaaaaaaa2" "We will move the queue to Bo next Friday." "u2" :kind "plan"))
          (msg "u3" "user" "What does billing cost?")))

(om::deftest tool-reads
  (let ((branch (test-branch)) (*layer-root* (test-dir "layer5")))
    (flet ((text (code &rest keys) (let ((r (apply #'run code :branch branch keys))) (or (tool-result-error r) (tool-result-text r)))))
      (om::check (text "(mapcar #'note-id (notes))") "(\"aaaaaaaaaaa1\" \"aaaaaaaaaaa2\")")
      (om::check (text "(notes :search \"billing usd\")") "[fact] The billing job costs 40 USD a month. u1")
      (om::check (text "(mapcar #'note-id (notes :kind \"plan\"))") "(\"aaaaaaaaaaa2\")")
      (om::check (text "(length (notes :limit 1))") "1")
      (om::check (text "(notes :limit 0)") "Limit(0): a limit is 1 to 200")
      ;; no record has every word: ranked by matched words
      (om::check (text "(mapcar #'note-id (notes :search \"queue bo billing\"))") "(\"aaaaaaaaaaa2\" \"aaaaaaaaaaa1\")")
      ;; history leaves out the current turn, newest first
      (om::check (text "(mapcar #'turn-id (history))") "(\"u2\" \"a1\" \"u1\")")
      (om::check (text "(mapcar #'turn-id (history :search \"usd\"))") "(\"a1\" \"u1\")")
      (om::check (text "(recall \"u1\")") "[user] The billing job costs 40 USD a month.")
      (om::check (text "(recall \"aaaaaaaaaaa1\")")
                 (format nil "[fact] The billing job costs 40 USD a month. u1~%~%[user u1] The billing job costs 40 USD a month."))
      (om::check (text "(recall \"nope\")") "nothing called \"nope\": no record id, entry id or saved name on this branch")
      ;; a settled phrase-note that drops a specific falls back to the default
      (let* ((model "acme/reads")
             (d (at-time ("2026-11-01T11:00:00Z")
                  (layer-hold model "(define phrase-note (n) (concatenate 'string \"note \" (note-anchor n)))"))))
        (layer-settle model (evidence-for model (list (ldef-held-id d))))
        (layer-take-evidence model)
        (om::check (text "(note-line (first (notes)))" :model model) "[fact] The billing job costs 40 USD a month. u1")
        (om::check (layer-failures model) '(("adaptable-failed:phrase-note" . 1)))
        (om::check (third *last-fallback*) "the line drops 40, usd")))))

(om::deftest tool-curated-through-code
  (let ((branch (test-branch)) (dir (test-dir "curated2")))
    (flet ((text (code) (let ((r (run code :branch branch :curated dir)))
                          (or (tool-result-error r) (tool-result-text r)))))
      (at-time ("2026-11-01T12:00:00Z")
        (om::check (text "(save \"billing-cost\" \"project\" \"Billing costs 40 USD a month\" :quoting \"billing job costs 40 USD\")")
                   "Saved memory billing-cost [project]: Billing costs 40 USD a month")
        (om::check (text "(mapcar #'memory-name (saved))") "(\"billing-cost\")")
        (om::check (text "(recall \"billing-cost\")")
                   (format nil "billing-cost [project]: Billing costs 40 USD a month~%~%saved 2026-11-01, updated 2026-11-01"))
        (om::check (text "(save \"Bad\" \"user\" \"x\")")
                   "memory name \"Bad\" must be lowercase letters, digits and hyphens, starting with a letter or digit, at most 64 characters")
        (om::check (text "(feedback \"billing-cost\" \"helped\" \"not in this conversation at all\")")
                   "the quote is not in this conversation: copy words verbatim from a user or assistant message that show the memory helped or misled you")))))

(om::deftest tool-layer-cleanup
  (sb-ext:run-program "/bin/rm" (list "-rf" *test-root*))
  (om::check (probe-file *test-root*) nil))
