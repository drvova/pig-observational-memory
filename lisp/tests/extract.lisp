;;;; extract: cases from extract/shell_test.go and extract/status_test.go, and whole extractions
;;;; over small branches.

(in-package "OM")

(defun events-string (command)
  (format nil "[~{~A~^ ~}]"
          (mapcar (lambda (e) (format nil "{~A ~A ~A}" (first e) (second e) (third e))) (shell-path-events command))))

(deftest shell-path-events
  (dolist (c '(("rm a.go" "[{a.go deleted }]")
               ("rm -rf build/ 'my file.txt'" "[{build/ deleted } {my file.txt deleted }]")
               ("rm \"$HOME/x\"" "[]")
               ("rm ~/x *.o" "[]")
               ("rm a.go | tee log" "[]")
               ("rm a.go || true" "[]")
               ("rm a.go &" "[]")
               ("rm $(cat list)" "[]")
               ("FOO=1 rm a.go" "[]")
               ("pushd x && rm a.go" "[]")
               ("git -C sub rm a.go" "[]")
               ("git rm --cached a.go" "[]")
               ("git rm -r old/" "[{old/ deleted }]")
               ("git mv a.go b/ c.go" "[]")
               ("git checkout main" "[]")
               ("git checkout main -- a.go" "[]")
               ("git checkout -- a.go b.go" "[{a.go reverted } {b.go reverted }]")
               ("git restore --staged a.go" "[]")
               ("git restore --staged --worktree a.go" "[{a.go reverted }]")
               ("git restore --source=HEAD~1 a.go" "[]")
               ("echo 'unclosed" "[]")
               ("rm -i main.go" "[]")
               ("rm -rI build" "[]")
               ("rm --interactive=always a.go" "[]")
               ("rm --interactive=never a.go" "[{a.go deleted }]")
               ("git restore -p main.go" "[]")
               ("git restore --patch main.go" "[]")
               ("git checkout -p -- main.go" "[]")))
    ;; Go prints an empty other field as nothing before the closing brace: "{a.go deleted }".
    (check (events-string (first c)) (second c) (first c))))

(deftest commit-from
  (multiple-value-bind (hash subject ok) (commit-from "git commit -am \"x\"" (format nil "[feature/a-b 0123abcd] Add the loader~% 2 files changed"))
    (check (list hash subject ok) '("0123abcd" "Add the loader" t)))
  (check (nth-value 2 (commit-from "git log" "[main 0123abc] x")) nil))

(deftest statuses
  (dolist (c '(("We will migrate the orders table to Postgres." "proposed")
               ("Let's plan to ship build 42 on Friday." "proposed")
               ("Next we bump the chart to 2.4." "proposed")
               ("I’ll move the cron to 3am." "proposed")
               ("If we move to Postgres, we will need 3 replicas." "hypothetical")
               ("Suppose the budget is 6000 USD." "hypothetical")
               ("What if we shard by tenant into 4 shards." "hypothetical")
               ("We migrated the orders table to Postgres." "")
               ("Will we migrate to Postgres in 2 weeks?" "")))
    (check (status-of-sentence (first c)) (second c) (first c))))

(deftest retractions-and-corrections
  (dolist (c '(("Actually, scrap the trail race idea." t "trail race")
               ("Scrap the trail race plan." t "trail race")
               ("Forget the whole trail race idea, it was silly." t "whole trail race")
               ("Actually, no trail races after all." t "trail races")
               ("I’m not doing the trail race." t "trail race")
               ("Should I scrap the trail race idea?" nil "")
               ("never mind" t "")
               ("No problem after all." nil "")))
    (multiple-value-bind (ok phrase) (retraction-of (first c))
      (check (list ok phrase) (rest c) (first c))))
  (check (ctarget-phrase (correction-of "Actually the database port is 6000, not 5432.")) "5432")
  (check (ctarget-phrase (correction-of "Let's use port 5433 instead of 5432.")) "5432")
  (check (nth-value 1 (correction-of "Not only Redis but also Postgres.")) nil))

(deftest sentences-and-units
  (check (split-line "Do these: 1. Stretch for 5 minutes. 2. Ice it.") '("Do these:" "1. Stretch for 5 minutes." "2. Ice it."))
  (check (sentences (format nil "Run it. Check the logs!~%- one~%- two~%~%```~%code. Here.~%```~%### Done. Next")) '("Run it." "Check the logs!" "one" "two" "Done." "Next"))
  (check (bare-ack-p "ok, thanks!") t)
  (check (bare-ack-p "ok, port 8080") nil)
  (check (question-p "Is it ready?\" ") t)
  (check (without-markup (format nil "<system-reminder>x~%y</system-reminder> Fix it.")) "Fix it."))

(defun branch-of (&rest lines)
  "A generic transcript's entries from its decoded lines."
  (coerce (read-generic (mapcar (lambda (l) (json-parse-object (json-string l))) lines)) 'simple-vector))

(defun extract-all (branch)
  (multiple-value-bind (batches covers) (extract branch 0 (length branch) (fold-ledger (vector)))
    (values (loop for b in batches append (batch-observations b)) covers)))

(deftest extraction-attempts-and-files
  (let ((branch (branch-of (jobject "id" "u1" "timestamp" "2026-05-01T10:00:00Z" "role" "user" "text" "Fix the build.")
                           (jobject "id" "a1" "timestamp" "2026-05-01T10:01:00Z" "role" "assistant" "text" "Running the tests."
                                    "tools" (vector (jobject "id" "c1" "name" "bash" "arguments" (jobject "command" "make test"))))
                           (jobject "id" "r1" "timestamp" "2026-05-01T10:02:00Z" "role" "tool" "toolCallId" "c1" "toolName" "bash"
                                    "text" (format nil "boom~%Command exited with code 2"))
                           (jobject "id" "a2" "timestamp" "2026-05-01T10:03:00Z" "role" "assistant" "text" ""
                                    "tools" (vector (jobject "id" "c2" "name" "write" "arguments" (jobject "path" "a.go"))
                                                    (jobject "id" "c3" "name" "bash" "arguments" (jobject "command" "make test"))))
                           (jobject "id" "r2" "timestamp" "2026-05-01T10:04:00Z" "role" "tool" "toolCallId" "c2" "toolName" "write" "text" "ok")
                           (jobject "id" "r3" "timestamp" "2026-05-01T10:04:00Z" "role" "tool" "toolCallId" "c3" "toolName" "bash" "text" "PASS"))))
    (multiple-value-bind (records covers) (extract-all branch)
      (check covers "r3")
      (check (mapcar #'obs-content records)
             '("Running the tests. [bash \"make test\" failed: boom]"
               "a.go written"
               "Running the tests. [bash \"make test\" worked]"))
      (check (obs-supersedes (third records)) (list (obs-id (first records))))
      (check (obs-source-ids (first records)) '("a1" "r1"))
      (check (obs-timestamp (first records)) "2026-05-01 10:02")
      (check (obs-id (first records)) (hash-id (cat (obs-content (first records)) (string (code-char 0)) "a1,r1"))))))

(deftest extraction-conversation
  (let ((branch (branch-of (jobject "id" "u1" "timestamp" "2026-05-06T09:00:00Z" "role" "user"
                                    "text" "We will deploy build 42 on Friday. The demo with Acme is tomorrow.")
                           (jobject "id" "u2" "timestamp" "2026-05-06T09:05:00Z" "role" "user"
                                    "text" "Actually, scrap that, deploy build 43 on Friday."))))
    (let ((records (extract-all branch)))
      (check (mapcar (lambda (o) (list (obs-kind o) (obs-status o) (obs-content o) (obs-event-date o))) records)
             '(("fact" "proposed" "We will deploy build 42 on Friday." "")
               ("fact" "" "The demo with Acme is tomorrow [=2026-05-07]." "2026-05-07")
               ("correction" "" "Actually, scrap that, deploy build 43 on Friday." "")))
      ;; A correction naming no phrase replaces only a record on its subject (status_test.go).
      (check (obs-supersedes (third records)) (quote ())))))
