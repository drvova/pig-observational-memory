;;;; A compaction as the session model's evidence unit (docs/lisp-core.md decisions 6 to 8;
;;;; memory/rules v2.go afterCompaction, RecordDetection; memory/hooks/harness_v2.go
;;;; recordCompactionV2): the usage ledger's detect and pass events written in Go's shape under the
;;;; store's flock, the v2 rules decided (evaluate) and written, then the image's held definitions
;;;; settled on the passes that ran with them (settle-v2), and its settled definitions judged by
;;;; the retire rule.
;;;;
;;;; Evidence is a snapshot (decision 8): take-snapshot, at the compaction, lists every held
;;;; definition in the layer as it stood (the image's own, adaptable/<name>@<digest>, and the Go
;;;; layer's held remedy definitions of the model, which the image applies), and the pass's ranHeld
;;;; is that list. What a definition did between two compactions (its failures, its runs, its
;;;; timeouts) is kept per session and model (an interval), so one session's failures never count
;;;; against another session's compaction; it lives in the image's memory, and a crash loses it as
;;;; it loses Go's detector runs not yet written: what reaches the ledger survives.
;;;;
;;;; The retire rule (decision 6), for a settled definition: a compaction in which it ran and
;;;; failed at least once is a failing unit, one in which it ran and never failed a passing unit,
;;;; one in which it never ran neither. Each conclusive unit is a line of the model's
;;;; <layer>/units.jsonl (schema lisp-layer-unit). Three failing units in a row, or more than three
;;;; among the last ten, revert it through a new generation (op revert, settled true). A failure
;;;; of a definition of an adaptable function that touches source coverage, and a settled text the
;;;; checks refuse on load, revert it at once. Within an interval, a definition is no longer called
;;;; after its second timeout (the breaker).

(in-package "OM.LAYER")

;;; The usage ledger's lines (curated.ModelEvent, json.Marshal: fields in declaration order,
;;; omitempty, map keys sorted)

(defun counts-object (alist)
  "An alist as a JSON object, nil when it is empty (omitted)."
  (when alist
    (let ((h (make-hash-table :test 'equal)))
      (loop for (k . n) in alist do (setf (gethash k h) n))
      h)))

(defun model-event-line (&key model kind (outcome "") stage (tokens 0) counts kinds revisions engine level1 ran-held
                              roots harm (turns 0) (repo "") (family "") ran-written)
  (with-output-to-string (out)
    (flet ((member* (name value)
             (write-char #\, out) (om::write-json-string name out) (write-char #\: out) (om::write-json value out)))
      (write-string "{\"time\":" out) (om::write-json-string (format-rfc3339-nano (cons (time-nanos (now)) 0)) out)
      (member* "model" model) (member* "kind" kind) (member* "outcome" outcome)
      (when (and stage (string/= stage "")) (member* "stage" stage))
      (when (/= tokens 0) (member* "tokens" tokens))
      (let ((c (counts-object counts))) (when c (member* "counts" c)))
      (let ((c (counts-object kinds))) (when c (member* "kinds" c)))
      (let ((c (counts-object revisions))) (when c (member* "revisions" c)))
      (when engine (member* "engine" engine))
      (when level1 (member* "level1" :true))
      (when ran-held (member* "ranHeld" (coerce ran-held 'simple-vector)))
      ;; The settlement fields (curated.ModelEvent: roots, harm, turns, repo, family, ranWritten).
      (when roots (member* "roots" (coerce roots 'simple-vector)))
      (when harm (member* "harm" harm))
      (when (/= turns 0) (member* "turns" turns))
      (when (string/= repo "") (member* "repo" repo))
      (when (string/= family "") (member* "family" family))
      (when ran-written (member* "ranWritten" (coerce ran-written 'simple-vector)))
      (write-char #\} out))))

(defun record-model-locked (store model line)
  "curated.Store.RecordModelLocked's write: one line appended to model's ledger."
  (append-line (models-path store model) line))

(defun revisions () (copy-alist +count-revisions+))

(defun record-detection (store model findings tokens)
  "rules.RecordDetection: one detect event of model with the source tokens the extraction read;
nothing for a run that read none."
  (when (and model (string/= model "") (plusp tokens))
    (let ((line (model-event-line :model model :kind "detect" :tokens tokens :engine "v2"
                                  :counts (remove-if-not #'plusp (findings-counts findings) :key #'cdr)
                                  :kinds (findings-dropped-kinds findings) :revisions (revisions))))
      (with-store-lock (store) (record-model-locked store model line)))
    t))

(defun since-v2-compaction (events)
  (let ((start 0))
    (loop for e in events for i from 1 when (v2-compaction-p e) do (setf start i))
    start))

(defun after-pass (store model &key empty level1 counts ran-held ran-written roots (repo "") (family "") (turns 0))
  "rules.AfterPass with Engine v2 (afterCompaction): records one compaction of model as its
evidence unit and decides its v2 rules, writing the ones that changed. counts are the kernel
counts the compaction adds (an alist), level1 whether its level-1 request answered, ran-held the
held ids it ran with, ran-written its own written definitions' ids, roots, repo and family its
unit (unit-of) and turns the assistant messages it removed. Returns the transitions. Rules written before the layer are migrated first
(rules.migrate, src/harness/rule-migration.lisp)."
  (migrate-legacy-rules store)
  (let ((catalog (catalog-of (go-effective-remedies store model))) (transitions '()))
    (with-store-lock (store)
      (let ((events (model-events store model)))
        (if empty
            (record-model-locked store model (model-event-line :model model :kind "pass" :outcome "empty"
                                                               :stage "compaction" :engine "v2" :ran-held ran-held))
            (let ((tokens 0) (sum '()) (kinds '()) (codec 0))
              (dolist (e (nthcdr (since-v2-compaction events) events))
                (when (string= (ev-string e "engine") "v2")
                  (cond ((string= (ev-string e "kind") "detect")
                         (incf tokens (ev-int e "tokens"))
                         (loop for (k . n) in (ev-counts e "counts") do (setf sum (alist-incf sum k n)))
                         (loop for (k . n) in (ev-counts e "kinds") do (setf kinds (alist-incf kinds k n))))
                        ((and (string= (ev-string e "kind") "decode") (string= (ev-string e "outcome") "error"))
                         (incf codec)))))
              (loop for (k . n) in counts when (plusp n) do (setf sum (alist-incf sum k n)))
              (when (and (plusp tokens) (plusp codec))
                (setf sum (alist-incf sum +detect-decode-failure+ codec))
                (setf sum (alist-incf sum +count-codec-decode-failure+ codec)))
              (record-model-locked store model
                                   (model-event-line :model model :kind "pass" :stage "compaction" :tokens tokens
                                                     :counts sum :kinds kinds :revisions (revisions) :engine "v2"
                                                     :level1 level1 :ran-held ran-held
                                                     :roots roots :harm (harm-of sum) :turns turns :repo repo :family family
                                                     :ran-written ran-written))
              (multiple-value-bind (changed ts)
                  (evaluate-rules model (model-events store model) (list-rules store) (cons (time-nanos (now)) 0) catalog)
                (dolist (r changed) (write-rule store r))
                (setf transitions ts))))))
    transitions))

;;; The snapshot (decision 8)

(defstruct snapshot (model "") (ran-held '()) (held-ids '()) (ran-written '()))

(defun take-snapshot (store model)
  "The held definitions in model's layer as it stands now, for the compaction about to run: the
image's own and the Go layer's held remedy definitions of the model (recordCompactionV2's
snapshot.List filtered to State held and the model's own). Take it before level 1 runs, so a
definition level 1 holds is never credited with the compaction that proposed it."
  (let ((ids (append (mapcar #'ldef-held-id (layer-held model))
                     (and store (string/= store "")
                          (loop for d in (go-held-remedies store model)
                                when (string= (gdef-model d) model) collect (gdef-held-id d))))))
    (make-snapshot :model model :ran-held (sort (copy-list ids) #'string<)
                   :held-ids (mapcar #'ldef-held-id (layer-held model))
                   :ran-written (and store (string/= store "") (go-written-ids store model)))))

(defun go-written-ids (store model)
  "The held ids (kind/name@digest of the text as declared) of model's own written definitions
in the Go layer, sorted: recordCompactionV2's ranWritten (om.golayer::written-ids, loaded after
this file: its consolidation instructions and remedies)."
  (let ((f (find-symbol "WRITTEN-IDS" "OM.GOLAYER")))
    (and f (fboundp f) (sort (copy-list (funcall f store model)) #'string<))))

;;; The interval: what each definition did since the session's last compaction

(defstruct interval
  (held-failures (make-hash-table :test 'equal))  ; name -> failures of its held definition
  (settled (make-hash-table :test 'equal))        ; name -> (digest runs . failures)
  (timeouts (make-hash-table :test 'equal))       ; held id -> timeouts
  (broken (make-hash-table :test 'equal)))        ; held id -> t, the breaker open

(defvar *intervals* (make-hash-table :test 'equal) "session NUL model -> interval.")
(defvar *intervals-lock* (sb-thread:make-mutex :name "om.layer intervals"))

(defun interval-key (session model) (concatenate 'string (or session "") (string #\Nul) (or model "")))

(defun current-session () (if *call* (call-session *call*) ""))

(defun interval-of (session model)
  (let ((key (interval-key session model)))
    (or (gethash key *intervals*) (setf (gethash key *intervals*) (make-interval)))))

(defun take-interval (session model)
  "The session's interval for model, and a new one started."
  (sb-thread:with-mutex (*intervals-lock*)
    (let ((key (interval-key session model)))
      (prog1 (or (gethash key *intervals*) (make-interval))
        (remhash key *intervals*)))))

(defun breaker-open-p (d)
  (sb-thread:with-mutex (*intervals-lock*)
    (gethash (ldef-held-id d) (interval-broken (interval-of (current-session) (ldef-model d))))))

(defun note-run (d ok status)
  "Records one run of d in the session's interval: a settled one's run and failure, a held one's
failure, and a timeout toward the breaker (open after the second)."
  (sb-thread:with-mutex (*intervals-lock*)
    (let ((i (interval-of (current-session) (ldef-model d))))
      (if (eq (ldef-state d) :settled)
          (let ((cell (gethash (ldef-name d) (interval-settled i))))
            (unless (and cell (string= (car cell) (ldef-digest d)))
              (setf cell (list* (ldef-digest d) 0 0) (gethash (ldef-name d) (interval-settled i)) cell))
            (incf (cadr cell))
            (unless ok (incf (cddr cell))))
          (unless ok (incf (gethash (ldef-name d) (interval-held-failures i) 0))))
      (when (eq status :timeout)
        (when (>= (incf (gethash (ldef-held-id d) (interval-timeouts i) 0)) 2)
          (setf (gethash (ldef-held-id d) (interval-broken i)) t))))))

;;; The retire rule

(defparameter +retire-in-a-row+ 3)
(defparameter +retire-window+ 10)
(defparameter +retire-in-window+ 3 "More than this many failing units among the last window revert.")

(defun units-path (model) (join-path (model-root model) "units.jsonl"))

(defun settle-generation (model name digest)
  "The generation that settled name's standing text digest: the latest settle generation of it,
0 for none."
  (let ((out 0))
    (dolist (g (read-manifest model) out)
      (when (and (equal (om::jget g "op") "settle") (equal (om::jget g "name") name) (equal (om::jget g "digest") digest)
                 (realp (om::jget g "gen")))
        (setf out (truncate (om::jget g "gen")))))))

(defun append-unit (model name digest outcome failures)
  "One conclusive unit of name's settled text digest (schema lisp-layer-unit), under the layer's
lock, so another process's unit never lands inside it."
  (with-layer-lock (model)
   (append-line (units-path model)
               (with-output-to-string (out)
                 (write-string "{\"time\":" out) (om::write-json-string (format-rfc3339-nano (now)) out)
                 (write-string ",\"name\":" out) (om::write-json-string name out)
                 (write-string ",\"digest\":" out) (om::write-json-string digest out)
                 (format out ",\"gen\":~D" (settle-generation model name digest))
                 (write-string ",\"outcome\":" out) (om::write-json-string outcome out)
                 (format out ",\"failures\":~D}" failures)))))

(defun units-of (model name digest)
  "The conclusive units of name's settled text digest since the generation that settled it,
oldest first: :pass or :fail each."
  (let ((gen (settle-generation model name digest)))
    (loop for u in (read-model-events (units-path model))
          when (and (equal (om::jget u "name") name) (equal (om::jget u "digest") digest)
                    (eql (om::jget u "gen") (coerce gen 'double-float)))
            collect (if (equal (om::jget u "outcome") "fail") :fail :pass))))

(defun retire-decision (units)
  "The retire rule over a settled definition's conclusive units, oldest first: nil, or the reason
to revert it."
  (let* ((recent (last units +retire-window+))
         (failing (count :fail recent)))
    (cond ((and (>= (length units) +retire-in-a-row+) (every (lambda (u) (eq u :fail)) (last units +retire-in-a-row+)))
           (format nil "it failed its kernel check, errored or timed out at ~D compactions in a row" +retire-in-a-row+))
          ((> failing +retire-in-window+)
           (format nil "it failed its kernel check, errored or timed out at ~D of its last ~D compactions that ran it"
                   failing (length recent)))
          (t nil))))

(defun revert-settled (model name digest reason)
  "Reverts name's settled definition (when digest still stands) through a new generation: the
default answers from the next call. Returns t when it reverted."
  (let ((done nil))
    (with-layer-lock (model)
      (when (equal (cdr (assoc name (current-state model) :test #'equal)) digest)
        (append-generation model "revert" :name name :digest digest :reason reason :settled t)
        (mirror-settled model (current-state model))
        (setf done t)))
    (invalidate model)
    done))

(defun judge-settled (model interval)
  "Writes the interval's conclusive units of model's settled definitions and reverts the ones the
retire rule refuses. Returns the notices, (name nil reason) each."
  (let ((notices '()) (standing (current-state model)))
    (loop for name being the hash-keys of (interval-settled interval) using (hash-value cell)
          do (destructuring-bind (digest runs . failures) cell
               (when (and (plusp runs) (equal (cdr (assoc name standing :test #'equal)) digest))
                 (append-unit model name digest (if (plusp failures) "fail" "pass") failures)
                 (let ((why (retire-decision (units-of model name digest))))
                   (when (and why (revert-settled model name digest why))
                     (push (list name nil why) notices))))))
    (nreverse notices)))

;;; The hook point

(defun cut-empty-p (entries cut-ids)
  "Whether no record of the fold of entries cites an entry the compaction removes."
  (let ((cut (make-hash-table :test 'equal)))
    (dolist (id cut-ids) (setf (gethash id cut) t))
    (notany (lambda (o) (some (lambda (id) (gethash id cut)) (om::obs-source-ids o)))
            (om::folded-observations (om::fold-ledger entries)))))

(defun assistant-turns (entries cut)
  "hooks.assistantTurns: the assistant messages among the entries whose ids are in cut."
  (count-if (lambda (e) (and (gethash (om::entry-id e) cut) (string= (om::entry-type e) "message")
                             (equal (om::entry-role e) "assistant")))
            entries))

(defun record-compaction-evidence (&key store model session entries cut-ids counts level1 snapshot)
  "recordCompactionV2 for the image: records one compaction of session as model's evidence unit
and settles and judges the image's definitions on it. store is the curated store's directory
(<config home>/observational-memory/memories); entries the branch the compaction folded (with the
records level 0 and level 1 appended); cut-ids the ids of the source entries it removes; counts
level 1's code checks (an alist, rules.Level1Counts' keys) and level1 whether its request
answered; snapshot what take-snapshot returned before level 1 ran. Returns the notices for the
user: strings (the rule transitions) and (name written-p reason) lists (settled, reverted and
retired definitions). Without a store or a model nothing is recorded."
  (when (or (null store) (string= store "") (null model) (string= model ""))
    (return-from record-compaction-evidence '()))
  (let* ((snapshot (or snapshot (make-snapshot :model model)))
         (interval (take-interval session model))
         (counts (copy-alist counts)))
    (maphash (lambda (name n)
               (when (find (concatenate 'string +kind-adaptable+ "/" name "@")
                           (snapshot-held-ids snapshot) :test (lambda (p id) (om::has-prefix id p)))
                 (setf counts (alist-incf counts (adaptable-failed-key name) n))))
             (interval-held-failures interval))
    (let* ((cut (let ((h (make-hash-table :test 'equal))) (dolist (id cut-ids h) (setf (gethash id h) t))))
           (transitions (multiple-value-bind (roots repo family)
                            (unit-of (om::folded-observations (om::fold-ledger entries)) cut)
                          (after-pass store model :empty (cut-empty-p entries cut-ids) :level1 level1
                                                  :counts counts :ran-held (snapshot-ran-held snapshot)
                                                  :ran-written (snapshot-ran-written snapshot)
                                                  :roots roots :repo repo :family family
                                                  :turns (assistant-turns entries cut))))
           (events (model-events store model))
           (settled (layer-settle model events :only (snapshot-held-ids snapshot)))
           (retired (judge-settled model interval)))
      (append (mapcar #'transition-message transitions) settled retired))))

(defun interval-failures (session model)
  "The held definitions' failure counts in session's current interval for model, an alist
(adaptable-failed:<name> . n) sorted, without taking it."
  (sb-thread:with-mutex (*intervals-lock*)
    (let ((out '()))
      (maphash (lambda (k v) (push (cons (adaptable-failed-key k) v) out))
               (interval-held-failures (interval-of session model)))
      (sort out #'string< :key #'car))))

(defun layer-settled-runs (model)
  "The current session's runs of model's settled definitions in its interval: an alist name ->
(runs . failures), sorted."
  (sb-thread:with-mutex (*intervals-lock*)
    (let ((out '()))
      (maphash (lambda (k v) (push (cons k (cdr v)) out)) (interval-settled (interval-of (current-session) model)))
      (sort out #'string< :key #'car))))

;;; Decision 24's settlement rules (memory/rules/settle_v2.go: settleNullArm, UnitOf, RootOf,
;;; HarmOf, FailureCounts; docs/memory-v2.md, "Settling a definition"), checked byte for byte
;;; against Go (TestLispSettleParity, TestLispUnitParity).

(defparameter +harm-counts+ '("dropped-then-queried" "level1-specifics-refused" "level1-union-kept"))
(defparameter +instructions-failures+ '("level1-failed" "level1-refused"))
(defparameter +detector-failures+ '("uncited-assistant-specifics" "cross-scope-supersede" "decode-failure"))
(defparameter +cost-tokens-per-failure+ 1000)
(defparameter +cost-turns-per-failure+ 10)
(defparameter +max-roots+ 64)

(defun failure-counts (kind) (if (string= kind "instructions") +instructions-failures+ +detector-failures+))

(defun harm-of (counts)
  "rules.HarmOf over an alist of counts."
  (loop for k in +harm-counts+ sum (or (cdr (assoc k counts :test #'string=)) 0)))

(defun root-of (o)
  "rules.RootOf: the digest of \"root\", the record's kind and its content, NUL-separated."
  (digest (concatenate 'string "root" (string #\Nul) (om::obs-kind o) (string #\Nul) (om::obs-content o))))

(defun path-ext (p)
  "rules.pathExt: path.Ext of p's last element, ASCII lower-cased."
  (let ((slash (position #\/ p :from-end t)) (dot (position #\. p :from-end t)))
    (if (or (null dot) (and slash (< dot slash)))
        ""
        (map 'string (lambda (c) (if (char<= #\A c #\Z) (char-downcase c) c)) (subseq p dot)))))

(defun commonest (counts)
  "rules.commonest over a hash table of counts: the highest, ties to the smaller key; \"\" for none."
  (let ((best "") (n 0))
    (maphash (lambda (k c) (when (or (> c n) (and (= c n) (string< k best))) (setf best k n c))) counts)
    best))

(defun unit-of (observations cut)
  "rules.UnitOf: (values roots repo family) of the records citing an entry in cut (a hash set of
entry ids): the roots of the ones that are not tool output, sorted, at most +max-roots+; the
commonest frame.repoId; the commonest path extension of their paths keys."
  (let ((roots (make-hash-table :test 'equal)) (repos (make-hash-table :test 'equal))
        (families (make-hash-table :test 'equal)))
    (dolist (o observations)
      (when (some (lambda (id) (gethash id cut)) (om::obs-source-ids o))
        (unless (member (om::obs-source o) '("tool-observed" "external-untrusted") :test #'string=)
          (setf (gethash (root-of o) roots) t))
        (let ((repo (om::alist-member (om::obs-frame o) "repoId")))
          (when (string/= repo "") (incf (gethash repo repos 0))))
        (dolist (p (om::keys-get (om::obs-keys o) "paths"))
          (let ((ext (path-ext p))) (when (string/= ext "") (incf (gethash ext families 0)))))))
    (let ((sorted (sort (loop for r being the hash-keys of roots collect r) #'string<)))
      (values (if (> (length sorted) +max-roots+) (subseq sorted 0 +max-roots+) sorted)
              (commonest repos) (commonest families)))))

(defun go-list-string (items)
  "fmt's %v of a []string."
  (format nil "[~{~A~^ ~}]" items))

(defun scope-string (scope)
  "layer.Scope.String: nil is everywhere."
  (if (null scope)
      "everywhere"
      (flet ((names (l) (mapcar (lambda (v) (if (string= v "") "none" v)) l)))
        (format nil "repositories ~A, task families ~A"
                (go-list-string (names (first scope))) (go-list-string (names (second scope)))))))

(defstruct (settle-unit (:conc-name su-)) (failures 0) (harm 0) (turns 0) (repo "") (family ""))

(defun prefixed-p (list prefix) (some (lambda (s) (and (stringp s) (om::has-prefix s prefix))) list))

(defun settle-null-arm (events kind name held-id time text)
  "rules.settleNullArm: (values decided clean reason scope)."
  (let* ((prefix (concatenate 'string kind "/" name "@")) (keys (failure-counts kind))
         (seen (make-hash-table :test 'equal)) (treated '()) (controls '()) (n-treated 0) (n-controls 0)
         (under-it 0) (decided-at -1) (n +min-evidence+))
    (dolist (e events)
      (let ((harm (ev e "harm")))
        (when (and (v2-compaction-p e) (string/= (ev-string e "outcome") "empty") (realp harm)
                   (or (string/= kind "instructions") (om::jtrue-p (ev e "level1"))))
          (let ((at (event-time e)))
            (when at
              (let ((independent nil))
                (dolist (r (ev-list e "roots"))
                  (unless (gethash r seen) (setf independent t (gethash r seen) t)))
                (let ((u (make-settle-unit :harm (truncate harm) :turns (ev-int e "turns")
                                           :repo (ev-string e "repo") :family (ev-string e "family")
                                           :failures (let ((counts (ev-counts e "counts")))
                                                       (loop for k in keys sum (or (cdr (assoc k counts :test #'string=)) 0)))))
                      (ran (or (prefixed-p (ev-list e "ranHeld") prefix) (prefixed-p (ev-list e "ranWritten") prefix))))
                  (cond ((and (member held-id (ev-list e "ranHeld") :test #'equal) (time-after-p at time))
                         (incf under-it)
                         (when independent (push u treated) (incf n-treated)))
                        ((and (not ran) independent) (push u controls) (incf n-controls))))
                (when (= n-treated n) (setf decided-at n-controls) (return))
                (when (>= under-it (* 2 n)) (return))))))))
    (setf treated (reverse treated) controls (reverse controls))
    (when (< decided-at 0)
      (return-from settle-null-arm
        (if (>= under-it (* 2 n))
            (values t nil (format nil "its ~D compactions came from ~D independent evidence ~A (needs ~D): repeated text is one root"
                                  under-it n-treated (plural-word n-treated "root") n)
                    nil)
            (values nil nil "" nil))))
    (when (< decided-at (* 2 n))
      (return-from settle-null-arm
        (values t nil (format nil "no null arm: ~D compactions without it to compare against (needs ~D)" decided-at (* 2 n)) nil)))
    (let* ((b (subseq controls (- decided-at (* 2 n)) (- decided-at n)))
           (a (subseq controls (- decided-at n) decided-at)))
      (flet ((sums (us) (values (loop for u in us sum (su-failures u)) (loop for u in us sum (su-harm u))
                                (loop for u in us sum (su-turns u)))))
        (multiple-value-bind (ft ht tt) (sums treated)
          (multiple-value-bind (fa ha ta) (sums a)
            (multiple-value-bind (fb hb tb) (sums b)
              (when (> ht (max ha hb))
                (return-from settle-null-arm
                  (values t nil (format nil "harm: ~D over ~D compactions under it, against ~D and ~D in two windows without it" ht n ha hb) nil)))
              (let* ((effect (- (min fa fb) ft)) (spread (abs (- fa fb)))
                     (added-turns (max 0 (- tt (max ta tb))))
                     (cost (+ (floor (* (if (string= kind "instructions") (om::estimate-tokens text) 0) n 1000) +cost-tokens-per-failure+)
                              (floor (* added-turns 1000) +cost-turns-per-failure+)))
                     (figures (format nil "failures ~D under it over ~D compactions against ~D and ~D without it (spread ~D), cost ~D.~3,'0D"
                                      ft n fa fb spread (floor cost 1000) (mod cost 1000))))
                (if (let ((net (- (* effect 1000) cost))) (or (<= net (* 2 spread 1000)) (< net 1000)))
                    (values t nil (concatenate 'string "no net effect after cost: " figures) nil)
                    (values t t (concatenate 'string "its effect beat the null arm after cost: " figures)
                            (list (sort (remove-duplicates (mapcar #'su-repo treated) :test #'string=) #'string<)
                                  (sort (remove-duplicates (mapcar #'su-family treated) :test #'string=) #'string<))))))))))))

(export (quote (take-snapshot record-compaction-evidence harness-detect legacy-memory-call)))
