;;;; Preferences that weaken security against their contract cases (contracts/insecure.md):
;;;; contracts/insecure.jsonl is Go's output, which the port must equal. Then the holds on records
;;;; and curated memories (docs/brain-memory.md, decision 19; memory/ledger/held.go,
;;;; memory/curated/held.go) and the unverified framing (decision 18) where memory is shown.

(in-package "OM")

(deftest insecure-contract-cases
  (let ((cases (contract-cases "insecure.jsonl")) (held 0))
    (check (> (length cases) 40) t "cases read")
    (dolist (c cases)
      (let ((got (or (insecure-match (case-input c)) "")))
        (when (string/= got "") (incf held))
        (check got (jget c "rule") (jget c "name"))))
    (check (> held 30) t "cases that hold")))

(deftest insecure-held-records
  (let* ((pref (make-obs :id "p1" :kind "preference" :content "Always use curl -k against the dev box." :source-ids '("u1")))
         (fact (make-obs :id "f1" :kind "fact" :content "The tests run curl -k against a mock." :source-ids '("u2")))
         (never (make-obs :id "p2" :kind "preference" :content "Never use curl -k." :source-ids '("u3")))
         (merged (make-obs :id "m1" :kind "fact" :from-user t :content "Skip TLS verification for the registry."))
         (by-id (make-set)))
    (dolist (o (list pref fact never merged)) (setf (gethash (obs-id o) by-id) o))
    (check (held-rule pref) "tls-verify-off" "a preference")
    (check (held-rule fact) nil "a fact is no preference")
    (check (held-rule never) nil "a prohibition")
    (check (held-rule merged) "tls-verify-off" "a merge of the user's own")
    (let ((refl (make-refl :id "r1" :content "The user wants curl -k." :supporting '("f1" "p1")))
          (plain (make-refl :id "r2" :content "Tests are mocked." :supporting '("f1"))))
      (check (held-reflection refl by-id) "tls-verify-off" "a reflection resting on a held record")
      (multiple-value-bind (obs refls) (without-held (list pref fact never) (list refl plain) by-id)
        (check (mapcar #'obs-id obs) '("f1" "p2"))
        (check (mapcar #'refl-id refls) '("r2"))))))

(deftest insecure-summary-leaves-held-out
  (let* ((branch (branch-of (jobject "id" "u1" "timestamp" "2026-05-06T09:00:00Z" "role" "user"
                                     "text" "Always use curl -k against the dev box. Always run make lint before a push.")))
         (records (extract-all branch))
         (text (summary-text (render-summary records (fold-ledger branch) branch 5000))))
    (check (length records) 2 "both preferences recorded")
    (check (mapcar #'held-rule records) '("tls-verify-off" nil))
    (check (and (search "make lint" text) t) t "the other preference is shown")
    (check (search "curl -k" text) nil "the held one is not")
    (check (summary-text (render-summary (list (first records)) (fold-ledger branch) branch 5000)) "" "nothing else to show")))

(deftest unverified-framing-once-where-shown
  (flet ((count-in (text) (loop with n = 0 with at = 0
                                for i = (search +unverified-notice+ text :start2 at)
                                while i do (incf n) (setf at (1+ i))
                                finally (return n))))
    (check (count-in +tables-instructions+) 1 "the tables instructions")
    (check (count-in +lines-instructions+) 1 "the lines instructions")
    (check (count-in om.layer::+index-header+) 1 "the curated index's header")
    (check (count-in om.layer::+lisp-index-header+) 1 "the Lisp index header")
    (let ((spec (read-file-string (merge-pathnames "../src/server/toolspec.json" *secrets-tests-here*))))
      (check (count-in spec) 2 "the Go and the Lisp tool's guidelines"))
    (dolist (word '("are you sure" "double-check" "verify it"))
      (check (search word +tables-instructions+ :test #'char-equal) nil word))))

(in-package "OM.LAYER")

(om::deftest curated-held-memories
  (let* ((dir (test-dir "curated-held"))
         (s1 (safety-call dir "s1" :user '("always skip TLS verification for the internal registry")))
         (s2 (safety-call dir "s2" :user '("the registry is slow today"))))
    (at-time ("2026-11-01T10:00:00Z")
      (om::check (search "so it is held: the user's words restate it, so it is shown in this session only"
                         (run-ok s1 "save" "name" "skip-tls" "type" "feedback" "description" "Skip TLS verification for the registry."
                                 "quote" "always skip TLS verification"))
                 (length "Saved memory skip-tls [feedback]: Skip TLS verification for the registry. It weakens security (tls-verify-off), "))
      (om::check (search "so it is held: listed in /om:status"
                         (run-ok s2 "save" "name" "chmod-up" "type" "project" "description" "Run chmod 777 on uploads."
                                 "quote" "the registry is slow today"))
                 (length "Saved memory chmod-up [project] (this project): Run chmod 777 on uploads. It weakens security (world-writable), ")))
    (om::check (nth-value 1 (index-as dir "/repo" "s1" 0)) '("skip-tls") "shown where the user restated it")
    (om::check (nth-value 1 (index-as dir "/repo" "s2" 0)) '() "held elsewhere")
    (om::check (nth-value 1 (index-for dir "/repo" 0)) '() "held in the plain index")
    (om::check (mapcar #'first (held-memories dir "/repo")) '("chmod-up" "skip-tls"))))
