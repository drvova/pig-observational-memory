;;;; The retired names and the v1 layer migration at a session start (docs/lisp-port-migration.md):
;;;; the strict rows decoder, the kernel's check, the fold blocks, MigrateLayerV2 over a store, the
;;;; legacy definitions, and a PiG session start whose skip list a turn end reads. Byte parity with
;;;; Go is memory/contracts/lispmigration_test.go.

(in-package "OM")

(defun g-instructions (model name text &key (mode ""))
  (om.layer::make-gdef :kind "instructions" :name name :model model :text text :mode mode :session "s0" :engine ""))

(defun g-put (store d)
  "PutLocked of an instructions definition whose mode is settled (stampLifted, putStamped)."
  (om.layer::with-store-lock (store)
    (om.golayer::put-stamped store (om.golayer::stamp-lifted d))))

(defmacro with-fixed-clock (&body body)
  `(let ((om.layer::*now* (lambda () (om.layer::parse-rfc3339 "2026-11-06T08:30:00Z")))) ,@body))

(deftest migration-rows-strict
  (let ((tb (first (om.golayer::rows-decode (format nil "t[2]{a,b}:~%  x,\"y, z\"~%  ,w")))))
    (check (om.golayer::tbl-rows tb) '(("x" "y, z") ("" "w")) "values, quoted and absent")
    (check (om.golayer::tbl-cell tb 1 "a") "" "an absent cell")
    (check (nth-value 1 (om.golayer::tbl-cell tb 1 "a")) nil "absent"))
  (dolist (bad (list (format nil "t[1]{a,b}:~%  x, y") "key: value" (format nil "t[2]{a}:~%  x")
                     (format nil "t[1]{a,a}:~%  x,y") (format nil "t[1]{a}:~%   x") "plain text"))
    (check (and (nth-value 1 (om.golayer::try-rows-decode bad)) t) t bad)))

(deftest migration-kernel-check
  (check (and (om.golayer::kernel-check "tool" "notes" (format nil "definition[1]{name}:~%  notes")) t) t "a kernel-owned name")
  (check (and (search "names \"search\"" (om.golayer::kernel-check "tool" "mine" (format nil "definition[1]{name}:~%  search"))) t)
         t "a kernel-owned value in a naming column")
  (check (and (om.golayer::kernel-check "tool" "mine" "not a table") t) t "a tool the kernel cannot read")
  (check (om.golayer::kernel-check "instructions" "consolidation" "Keep versions whole.") nil "instructions text")
  (check (and (om.golayer::kernel-check "detector" "error" "x") t) t "an event name owned for the ledger kinds")
  (check (om.golayer::kernel-check "tool" "error" (format nil "definition[1]{name}:~%  error")) nil "free for a tool")
  (check (om.layer::model-dir "p/M*") "p_2f_4d_2a" "ModelDir"))

(deftest migration-fold-block
  (let* ((once (om.golayer::with-fold-block "Keep it." "observer" "" "Note ports."))
         (again (om.golayer::with-fold-block once "observer" "" "Note hosts.")))
    (check once (format nil "Keep it.~%~%From the observer instructions (memory v1):~%Note ports.~%End of the observer instructions (memory v1).") "appended")
    (check (count #\F again) 1 "replaced in place")
    (check (om.golayer::with-fold-block again "observer" "" nil) "Keep it." "removed")
    (check (and (search "memory model w/x:" (om.golayer::with-fold-block "" "observer" "w/x" "A.")) t) t "a worker's block")))

(deftest migration-layer-v2
  (with-server-dir (dir)
    (with-fixed-clock
      (let ((store (string-right-trim "/" dir)))
        (g-put store (g-instructions "p/m" "observer" "Note every port." :mode "extend"))
        (g-put store (g-instructions "p/m" "reflector" "Only deploys." :mode "replace"))
        (g-put store (g-instructions "p/m" "dropper" "Never drop a port." :mode "extend"))
        (let ((messages (om.golayer::migrate-layer-v2 store "p/m" "")))
          (check (length messages) 3 "one fold, two retirements")
          (check (and (search "memory v2 folded the instructions/observer addition for p/m" (first messages)) t) t "the fold")
          (check (and (search "a replacement of its seed" (second messages)) t) t "a replacement retired"))
        (let ((c (om.golayer::recorded-file (om.golayer::def-path store "p/m" "instructions" "consolidation") "p/m" "consolidation"
                                            (om.golayer::current store "p/m"))))
          (check (om.layer::gdef-text c)
                 (format nil "From the observer instructions (memory v1):~%Note every port.~%End of the observer instructions (memory v1).")
                 "the consolidation instructions")
          (check (om.layer::gdef-seed c) "instructions/consolidation" "it records the seed it extends"))
        (check (om.golayer::migrate-layer-v2 store "p/m" "") '() "migrated once")
        (check (length (om.golayer::generations store "p/m")) 7 "three puts, the fold's put, three markers")))))

(deftest migration-retired-names
  (with-server-dir (dir)
    (with-fixed-clock
      (let ((store (string-right-trim "/" dir)))
        (dolist (name '("old-one" "notes"))
          (write-file* (format nil "~A/definitions/~A.rows" store name)
                       (format nil "definition[1]{name,query,description,created,sources}:~%  ~A,notes | count,A count.,2026-01-01T00:00:00Z,s1 s2~%" name)))
        (check (om.golayer::retired-query-names store) '("old-one" "notes") "a definition, then a refused one")
        (check (and (probe-file (format nil "~A/definitions/old-one.rows.migrated" store)) t) t "migrated, kept")
        (check (and (probe-file (format nil "~A/definitions/notes.rows.refused" store)) t) t "refused, kept")
        (check (and (search "\"session\":\"s1\"" (om.layer::read-file (format nil "~A/harness/_2a/manifest.jsonl" store))) t) t
               "the put names the first source")
        (check (om.golayer::retired-query-names store) '("old-one" "notes") "read again")))))

(deftest migration-pig-skip-list
  (let* ((call (jobject "type" "message" "id" "a1" "parentId" "u1" "timestamp" "2026-05-01T10:00:01Z"
                        "message" (jobject "role" "assistant" "stopReason" "toolUse"
                                           "content" (vector (jobject "type" "text" "text" "Reading the old notes.")
                                                             (jobject "type" "toolCall" "id" "c1" "name" "notes"
                                                                      "arguments" (jobject "command" "zebra-call"))))))
         (result (jobject "type" "message" "id" "t1" "parentId" "a1" "timestamp" "2026-05-01T10:00:02Z"
                          "message" (jobject "role" "toolResult" "toolCallId" "c1" "toolName" "notes"
                                             "content" (vector (jobject "type" "text" "text" "zebra-result")) "isError" :false)))
         (h (make-phost :branch (vector (pi-message "u1" "" "user" "Check the deploy port.") call result
                                        (pi-message "a2" "t1" "assistant" "The deploy listens on 8080.")))))
    (with-pig (stream dir)
      (let* ((scope (pig-scope dir))
             (store (format nil "~Aobservational-memory/memories" dir))
             (host (lambda (method params)
                     (if (string= method "host/allTools")
                         (jobject "tools" (vector (jobject "name" "bash" "source" "builtin")))
                         (phost-answer h method params)))))
        (setf (phost-home h) (string-right-trim "/" dir))
        (write-file* (format nil "~A/definitions/notes.rows" store)
                     (format nil "definition[1]{name,query,description,created,sources}:~%  notes,notes | count,A count.,,s1~%"))
        (write-file* (format nil "~A/harness/_2a/instructions/observer.rows" store) "untracked")
        (check (json-string (jget (peer-exchange stream 1 "event/sessionStart" (jobject "scope" scope) :host host) "result"))
               "{}" "the session start runs on the image")
        (check (jget (jget (peer-exchange stream 2 "event/turnEnd" (jobject "scope" scope) :host host) "result") "thinning")
               :true "a turn end")
        (let ((appended (json-string (coerce (mapcar #'cdr (phost-appended h)) 'simple-vector))))
          (check (and (search "8080" appended) t) t "the conversation is recorded")
          (check (search "zebra" appended) nil "the retired tool's call is memory plumbing"))))))
