;;;; Evidence and retirement (docs/lisp-port-evidence.md): the retire rule's thresholds, the
;;;; breaker, the immediate vetoes, evidence kept per session, the pass event's shape, rule
;;;; evaluation writing a harness rule, and keep-head at level 0. They write only under a
;;;; temporary directory.

(in-package "OM.LAYER")

(defvar *ev-root*
  (format nil "/tmp/om-evidence-test-~D-~D/" (sb-posix:getpid) (random 1000000 (make-random-state t))))

(defun ev-dir (name) (join-path *ev-root* name))

(defun define-test-cover ()
  "Defines test-cover, a test adaptable that touches source coverage, for one test (eval, so the
other tests' lists of adaptable functions never show it)."
  (eval '(define-adaptable om.engine::test-cover (xs)
          (:check (lambda (result xs) (unless (= (length result) (length xs)) "a result per element")) :coverage t)
          "Copies its elements."
          (copy-list xs))))

(defmacro at ((s) &body body)
  `(let ((*now* (let ((tm (parse-rfc3339 ,s))) (lambda () tm)))) ,@body))

(defun settle-now (model text hold-time settle-time)
  "Holds text and settles it on one pass that ran it."
  (let ((d (at (hold-time) (layer-hold model text))))
    (layer-settle model (list (om::jobject "time" settle-time "kind" "pass" "engine" "v2" "stage" "compaction"
                                           "tokens" 100 "outcome" "" "ranHeld" (vector (ldef-held-id d)))))
    d))

(om::deftest retire-thresholds
  (om::check (retire-decision '()) nil)
  (om::check (retire-decision '(:fail :fail)) nil "two failing units are not enough")
  (om::check (and (retire-decision '(:fail :fail :fail)) t) t "three in a row")
  (om::check (and (retire-decision '(:pass :pass :fail :fail :fail)) t) t "three in a row after passes")
  (om::check (retire-decision '(:fail :fail :pass :fail :pass)) nil "three failing, never three in a row")
  (om::check (and (retire-decision '(:fail :fail :pass :fail :fail :pass)) t) t "four failing of six: more than three")
  (om::check (and (retire-decision '(:fail :fail :pass :fail :fail :pass :fail)) t) t "five of the last ten")
  (om::check (and (retire-decision '(:fail :pass :fail :pass :fail :pass :fail)) t) t "four of the last ten")
  (om::check (retire-decision '(:fail :pass :fail :pass :fail :pass :pass :pass :pass :pass)) nil "three of ten")
  (om::check (retire-decision '(:fail :fail :pass :fail :pass :fail :pass :pass :pass :pass :pass :pass))
             nil "the oldest failure left the window of ten")
  (om::check (retire-decision '(:fail :pass :fail :pass :fail :pass :fail :pass :pass :pass :pass))
             nil "the first of four failures is outside the last ten"))

(defun failing-text () "(define test-double (xs) (list 1))")

(om::deftest retire-after-failing-compactions
  (let* ((*layer-root* (ev-dir "retire")) (store (ev-dir "retire-store")) (model "acme/retire"))
    (with-call ((make-call :model model :session "s1"))
      (let ((d (settle-now model (failing-text) "2026-11-02T09:00:00Z" "2026-11-02T10:00:00Z")))
        (om::check (mapcar #'ldef-name (layer-settled model)) '("test-double"))
        (loop for k from 1 to 3
              do (om::check (om.engine::test-double '(1 2)) '(2 4) "the default answers a failing call")
                 (let ((notices (at ((format nil "2026-11-02T1~D:00:00Z" k))
                                  (record-compaction-evidence :store store :model model :session "s1"
                                                              :entries #() :snapshot (take-snapshot store model)))))
                   (om::check (and (find "test-double" notices :key (lambda (n) (and (listp n) (first n))) :test #'equal) t)
                              (= k 3) (format nil "compaction ~D" k))))
        (om::check (layer-settled model) '() "reverted after three failing units")
        (let ((g (car (last (read-manifest model)))))
          (om::check (list (om::jget g "op") (om::jget g "digest") (om::jget g "settled")) (list "revert" (ldef-digest d) :true)))
        (om::check (length (read-model-events (units-path model))) 3)
        (om::check (om.engine::test-double '(1 2)) '(2 4))))))

(om::deftest retire-ignores-compactions-that-never-ran-it
  (let* ((*layer-root* (ev-dir "inconclusive")) (store (ev-dir "inconclusive-store")) (model "acme/inconclusive"))
    (with-call ((make-call :model model :session "s1"))
      (settle-now model (failing-text) "2026-11-02T09:00:00Z" "2026-11-02T10:00:00Z")
      ;; a failing unit, then compactions in which it never ran, then two more failing ones:
      ;; three failing units in a row among the conclusive ones
      (flet ((compaction (time) (at (time) (record-compaction-evidence :store store :model model :session "s1" :entries #()))))
        (om.engine::test-double '(1 2))
        (compaction "2026-11-02T11:00:00Z")
        (compaction "2026-11-02T12:00:00Z")
        (compaction "2026-11-02T13:00:00Z")
        (om::check (length (layer-settled model)) 1 "inconclusive units count for neither")
        (om.engine::test-double '(1 2))
        (compaction "2026-11-02T14:00:00Z")
        (om::check (length (layer-settled model)) 1)
        (om.engine::test-double '(1 2))
        (compaction "2026-11-02T15:00:00Z")
        (om::check (layer-settled model) '() "the third conclusive failing unit reverts it")))))

(om::deftest retire-is-per-session
  (let* ((*layer-root* (ev-dir "sessions")) (store (ev-dir "sessions-store")) (model "acme/sessions"))
    (settle-now model (failing-text) "2026-11-02T09:00:00Z" "2026-11-02T10:00:00Z")
    ;; session a's failures never count against session b's compactions
    (with-call ((make-call :model model :session "a")) (om.engine::test-double '(1 2)))
    (loop for k from 1 to 3
          do (at ((format nil "2026-11-02T1~D:00:00Z" k))
               (record-compaction-evidence :store store :model model :session "b" :entries #())))
    (om::check (length (layer-settled model)) 1)
    (om::check (read-model-events (units-path model)) '())
    ;; a held definition's failure in session a is counted by a's compaction only
    (let ((held (at ("2026-11-02T14:00:00Z") (layer-hold model "(define test-double (xs) (error \"no\"))"))))
      (with-call ((make-call :model model :session "a")) (om.engine::test-double '(1)))
      (om::check (interval-failures "a" model) '(("adaptable-failed:test-double" . 1)))
      (om::check (interval-failures "b" model) '())
      (let ((entries (vector (om::make-entry :type "message" :id "u1" :message (om::jobject "role" "user" "content" "hi"))
                             (om::entry-from-map
                              (om::json-parse "{\"type\":\"custom\",\"id\":\"r1\",\"customType\":\"om.observations.recorded\",\"data\":{\"observations\":[{\"id\":\"aaaaaaaaaaa1\",\"content\":\"hi\",\"timestamp\":\"2026-11-02 09:00\",\"relevance\":\"low\",\"sourceEntryIds\":[\"u1\"],\"tokenCount\":3}],\"coversUpToId\":\"u1\"}}")))))
        (flet ((last-pass () (car (last (model-events store model)))))
          (at ("2026-11-02T15:00:00Z")
            (record-compaction-evidence :store store :model model :session "b" :entries entries :cut-ids '("u1")
                                        :snapshot (take-snapshot store model)))
          (om::check (ev-list (last-pass) "ranHeld") (list (ldef-held-id held)) "the snapshot credits every held definition")
          (om::check (ev-count (last-pass) "adaptable-failed:test-double") 0)
          (at ("2026-11-02T16:00:00Z")
            (record-compaction-evidence :store store :model model :session "a" :entries entries :cut-ids '("u1")
                                        :snapshot (take-snapshot store model)))
          (om::check (ev-count (last-pass) "adaptable-failed:test-double") 1 "session a's own compaction counts it")
          (om::check (interval-failures "a" model) '() "and starts a new interval"))))))

(om::deftest breaker-stops-after-the-second-timeout
  (let* ((*layer-root* (ev-dir "breaker")) (store (ev-dir "breaker-store")) (model "acme/breaker")
         (*definition-timeout* 0.2))
    (with-call ((make-call :model model :session "s1"))
      (settle-now model "(define test-double (xs) (loop))" "2026-11-02T09:00:00Z" "2026-11-02T10:00:00Z")
      (dotimes (i 4) (om::check (om.engine::test-double '(1 2)) '(2 4)))
      (om::check (layer-settled-runs model) '(("test-double" 2 . 2)) "not called after its second timeout")
      (at ("2026-11-02T11:00:00Z") (record-compaction-evidence :store store :model model :session "s1" :entries #()))
      (om.engine::test-double '(1 2))
      (om::check (layer-settled-runs model) '(("test-double" 1 . 1)) "called again after the next compaction"))))

(om::deftest veto-on-load-and-coverage
  (let* ((*layer-root* (ev-dir "veto")) (model "acme/veto"))
    (with-call ((make-call :model model :session "s1"))
      ;; a settled text the checks refuse on load is reverted at once
      (let ((d (settle-now model "(define test-double (xs) (mapcar (lambda (x) (* 3 x)) xs))"
                           "2026-11-02T09:00:00Z" "2026-11-02T10:00:00Z")))
        (om::check (om.engine::test-double '(1)) '(3))
        (write-atomic (blob-path model (ldef-digest d)) "(define test-double (xs) (eval xs))")
        (invalidate model)
        (om::check (om.engine::test-double '(1)) '(2) "the default stands")
        (let ((g (car (last (read-manifest model)))))
          (om::check (list (om::jget g "op") (om::jget g "settled")) '("revert" :true))))
      ;; a failure of a definition that touches source coverage reverts it at once
      (define-test-cover)
      (unwind-protect
           (progn
             (settle-now model "(define test-cover (xs) (list 1 2 3))" "2026-11-02T11:00:00Z" "2026-11-02T12:00:00Z")
             (om::check (mapcar #'ldef-name (layer-settled model)) '("test-cover"))
             (om::check (funcall 'om.engine::test-cover '(1)) '(1))
             (om::check (layer-settled model) '()))
        (remhash "test-cover" *adaptables*)))))

(om::deftest pass-event-shape
  (let ((store (ev-dir "shape")) (model "acme/shape"))
    (at ("2026-11-02T09:00:00.5Z")
      (record-detection store model (make-findings :counts (list (cons +detect-uncited+ 2) (cons +detect-dropped-queried+ 0))
                                                   :dropped-kinds '())
                        120))
    (at ("2026-11-02T10:00:00Z")
      (after-pass store model :counts '(("level1-refused" . 1) ("level1-failed" . 0)) :level1 t :ran-held '("adaptable/x@0123456789abcdef")))
    (at ("2026-11-02T11:00:00Z") (after-pass store model :empty t))
    (om::check (om::split (string-right-trim '(#\Newline) (read-file (models-path store model))) (string #\Newline))
               (list "{\"time\":\"2026-11-02T09:00:00.5Z\",\"model\":\"acme/shape\",\"kind\":\"detect\",\"outcome\":\"\",\"tokens\":120,\"counts\":{\"uncited-assistant-specifics\":2},\"revisions\":{\"decode-failure\":2,\"dropped-then-queried\":2,\"uncited-assistant-specifics\":2},\"engine\":\"v2\"}"
                     "{\"time\":\"2026-11-02T10:00:00Z\",\"model\":\"acme/shape\",\"kind\":\"pass\",\"outcome\":\"\",\"stage\":\"compaction\",\"tokens\":120,\"counts\":{\"level1-refused\":1,\"uncited-assistant-specifics\":2},\"revisions\":{\"decode-failure\":2,\"dropped-then-queried\":2,\"uncited-assistant-specifics\":2},\"engine\":\"v2\",\"level1\":true,\"ranHeld\":[\"adaptable/x@0123456789abcdef\"],\"harm\":0}"
                     "{\"time\":\"2026-11-02T11:00:00Z\",\"model\":\"acme/shape\",\"kind\":\"pass\",\"outcome\":\"empty\",\"stage\":\"compaction\",\"engine\":\"v2\"}"))))

(om::deftest evaluate-writes-a-keep-head-rule
  (let ((store (ev-dir "evaluate")) (model "acme/eval"))
    (loop for k from 1 to 3
          do (at ((format nil "2026-11-02T0~D:00:00Z" k))
               (record-detection store model (make-findings :counts (list (cons +detect-uncited+ k))) 100))
             (at ((format nil "2026-11-02T0~D:30:00Z" k)) (after-pass store model)))
    (let ((rules (active-rules store model)))
      (om::check (mapcar #'rule-remedy rules) '("keep-head") "require-kind is not a v2 remedy; keep-head is next")
      (om::check (rule-state (first rules)) +state-probation+)
      (om::check (list (rule-baseline-events (first rules)) (rule-baseline-tokens (first rules)) (rule-start (first rules)))
                 '(6 300 6))
      (om::check (om::jget (first (go-generations store model)) "op") "put"))))

(om::deftest keep-head-at-level-0
  (let* ((store (ev-dir "keephead")) (model "acme/keephead")
         (entries (vector (om::make-entry :type "message" :id "u1" :timestamp "2026-11-02T09:00:00.000Z"
                                          :message (om::jobject "role" "user" "content" "What should I pack?"))
                          (om::make-entry :type "message" :id "a1" :timestamp "2026-11-02T09:01:00.000Z"
                                          :message (om::jobject "role" "assistant" "stopReason" "stop"
                                                                "content" (vector (om::jobject "type" "text" "text" "Pack 3 jackets for Oslo."))))))
         (folded (om::fold-ledger entries)))
    ;; with no rule, the run is counted and nothing is added
    (multiple-value-bind (batches covers) (om::extract entries 0 2 folded :skip-tools '("memory"))
      (multiple-value-bind (out record) (harness-detect store model entries 0 2 folded batches covers)
        (om::check (length (loop for b in out append (om::batch-observations b)))
                   (length (loop for b in batches append (om::batch-observations b))))
        (funcall record)
        (om::check (ev-count (first (model-events store model)) +detect-uncited+)
                   (if (find "a1" (loop for b in batches append (om::batch-observations b))
                             :key (lambda (o) (first (om::obs-source-ids o))) :test #'equal)
                       0 1))))))
