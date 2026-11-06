;;;; W5, the hooks (docs/lisp-port-hooks.md): the minimal brief and the token estimate
;;;; (memory/serialize/brief_test.go's cases), the summary budget, the compaction over an
;;;; in-memory host, the host summary, day headers and thinning, /om:view, and the server's
;;;; session-start, compact, before-compact, prompt and context.

(in-package "OM")

(defun hentry (json) (entry-from-map (json-parse json)))

(defun brief-range ()
  (mapcar #'hentry
          '("{\"type\":\"message\",\"id\":\"u1\",\"timestamp\":\"2026-05-02T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"Fix the failing\\ndeploy script please\"}]}}"
            "{\"type\":\"message\",\"id\":\"a1\",\"timestamp\":\"2026-05-02T10:00:00Z\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"thinking\",\"thinking\":\"secret plan\"},{\"type\":\"text\",\"text\":\"Looking at it.\"},{\"type\":\"toolCall\",\"id\":\"c1\",\"name\":\"bash\",\"arguments\":{\"command\":\"ls scripts\",\"timeout\":5}},{\"type\":\"toolCall\",\"id\":\"c2\",\"name\":\"memory\",\"arguments\":{\"query\":\"notes\"}}]}}"
            "{\"type\":\"message\",\"id\":\"t1\",\"timestamp\":\"2026-05-02T10:00:00Z\",\"message\":{\"role\":\"toolResult\",\"toolCallId\":\"c1\",\"toolName\":\"bash\",\"content\":[{\"type\":\"text\",\"text\":\"deploy.sh huge output\"}]}}"
            "{\"type\":\"message\",\"id\":\"a2\",\"timestamp\":\"2026-05-02T10:00:00Z\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"id\":\"c3\",\"name\":\"edit\",\"arguments\":{\"path\":\"scripts/deploy.sh\",\"oldText\":\"a\",\"newText\":\"b\"}},{\"type\":\"toolCall\",\"id\":\"c4\",\"name\":\"custom\",\"arguments\":{\"zeta\":\"z\",\"alpha\":\"first\"}}]}}"
            "{\"type\":\"message\",\"id\":\"u2\",\"timestamp\":\"2026-05-02T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"thanks\"}]}}")))

(deftest hooks-minimal-brief
  (let ((nl (string #\Newline)) (entries (brief-range)))
    (multiple-value-bind (text kind) (minimal-brief entries (list "memory") 10000 0)
      (check kind "brief")
      (check text (cat +brief-heading+ nl "user: Fix the failing deploy script please (u1)" nl
                       "* bash \"ls scripts\" (a1)" nl "* edit \"scripts/deploy.sh\" (a2)" nl "* custom \"first\" (a2)")))
    (multiple-value-bind (text kind) (minimal-brief entries (list "memory") 40 0)
      (check kind "pointer")
      (check (and (has-prefix text "user: Fix the") (search "omitted: 5 entries from 2026-05-02 to 2026-05-02 (u1 to u2)" text)
                  (not (search "* bash" text)) (<= (estimate-tokens text) 40) t)
             t "the request line and the pointer"))
    (loop for (max want) in `((33 ,(cat "omitted: 5 entries from 2026-05-02 to 2026-05-02 (u1 to u2); the memory tool searches them ("
                                       +snippet-history-search+ ")."))
                              (9 "omitted: 5 entries (u1 to u2).") (5 "omitted: 5 entries.") (3 "omitted."))
          do (check (minimal-brief entries '() max 0) want (format nil "max ~D" max)))
    (check (minimal-brief entries '() 1000 6) "omitted: 5 entries." "the ceiling under the budget")
    (check (minimal-brief entries '() 6 1000) "omitted: 5 entries." "the budget under the ceiling")
    (dolist (max '(0 -100 1000))
      (check (<= (estimate-tokens (minimal-brief entries '() max 12)) 12) t (format nil "ceiling at max ~D" max)))
    (check (minimal-brief (list (hentry "{\"type\":\"message\",\"id\":\"u1\",\"message\":{\"role\":\"user\",\"content\":\"hi\"}}")) '() -5 1)
           "omitted." "a tiny range")
    (check (minimal-brief '() '() 0 0) "omitted: nothing; the compacted range held no entries.")))

(deftest hooks-token-estimates
  ;; 4 for the name and 16 for {"command":"ls"}: 20 characters, 5 tokens.
  (check (estimate-pi-message (json-parse "{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"name\":\"bash\",\"arguments\":{\"command\":\"ls\"}}]}")) 5)
  (check (estimate-pi-message (json-parse "{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"name\":\"bash\"}]}")) 2 "absent arguments are null")
  (check (estimate-pi-message (json-parse "{\"role\":\"user\",\"content\":[{\"type\":\"image\"}]}")) 1200)
  (check (estimate-pi-message (json-parse "{\"role\":\"bashExecution\",\"command\":\"ls\",\"output\":\"a<b\"}")) 2)
  (check (estimate-pi-message (json-parse "{\"role\":\"other\",\"content\":\"long text\"}")) 0)
  (check (estimate-entry-tokens (hentry "{\"type\":\"branch_summary\",\"id\":\"b\",\"summary\":\"12345\"}")) 2)
  (check (estimate-entry-tokens (hentry "{\"type\":\"custom_message\",\"id\":\"c\",\"content\":[{\"type\":\"text\",\"text\":\"1234\"},{\"type\":\"text\",\"text\":\"5\"}]}")) 2)
  (check (neutral-tokens-before (coerce (brief-range) 'simple-vector) "u2")
         (loop for e in (butlast (brief-range)) sum (estimate-entry-tokens e)) "tokensBefore up to the cut"))

(deftest hooks-summary-budget
  (let ((cfg (make-hconfig)))
    (check (resolve-summary-budget cfg :removed 900) 900 "unknown window: what the range carries")
    (check (resolve-summary-budget cfg :removed -5) 0)
    (check (resolve-summary-budget cfg :context-window 100000 :reserve 0 :tokens-before 90000 :removed 20000) 13616
           "the default reserve stands in")
    (check (resolve-summary-budget cfg :context-window 100000 :reserve 10000 :tokens-before 50000 :removed 20000 :headroom 1000 :max-output 8000)
           8000 "the output cap")
    (setf (hconfig-summary-max cfg) 300)
    (check (resolve-summary-budget cfg :removed 900) 300 "compactionSummaryMaxTokens")))

;;; An in-memory host

(defun memory-host (entries)
  "A host over a list of entries; appends go after the tip with ids s1, s2..."
  (let ((branch (coerce entries 'simple-vector)) (n 0))
    (values (make-hhost :read (lambda () branch)
                        :append (lambda (type data)
                                  (let ((id (format nil "s~D" (incf n))))
                                    (setf branch (concatenate 'simple-vector branch (vector (synthetic-entry id type data))))
                                    id))
                        :cwd "/work")
            (lambda () branch))))

(defun user-lines (n)
  (loop for i from 0 below n
        append (list (hentry (format nil "{\"type\":\"message\",\"id\":\"u~D\",\"timestamp\":\"2026-05-01T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"Host h-~D serves port ~D from /srv/h~D.\"}}" i i (+ 9000 i) i))
                     (hentry (format nil "{\"type\":\"message\",\"id\":\"a~D\",\"timestamp\":\"2026-05-01T10:00:05Z\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"thinking\",\"thinking\":\"~A\"},{\"type\":\"text\",\"text\":\"Noted h-~D.\"}]}}" i (make-string 800 :initial-element #\y) i)))))

(deftest hooks-compaction
  (multiple-value-bind (h branch) (memory-host (user-lines 12))
    (let* ((cut (funcall (hhost-append h) "om.cut" "{\"reason\":\"manual\"}"))
           (c (compact-on h (make-hconfig) "manual" cut 0))
           (types (map 'list #'entry-custom-type (subseq (funcall branch) 25))))
      (check (cresult-kind c) "records")
      (check (has-prefix (cresult-summary c) +tables-summary-intro+) t "a tables summary")
      (check (and (search "intent[" (cresult-summary c)) t) t "the intent section")
      (check (cresult-first-kept c) "s1")
      (check (cresult-tokens-before c) (loop for e in (user-lines 12) sum (estimate-entry-tokens e)))
      (check (car types) "om.observations.recorded" "level 0 ran on the cut range")
      (let ((details (json-parse (cresult-details-json c))))
        (check (jget details "foldThroughEntryId") "s1")
        (check (jget details "engine") "v2")
        (check (jget (jget details "consolidation") "reason") "the session model is unavailable: no session model")
        (check (length (jget details "shown")) 12 "every record shown")
        (check (has-prefix (cresult-details-json c) "{\"type\":\"om.folded\",\"version\":1,\"fullFold\":true,\"observations\":[") t
               "MemoryDetails' field order"))
      (check (has-prefix (cresult-map-json c) "{\"details\":{") t)))
  ;; A budget too small for any record gives the minimal brief; consolidate false is its reason.
  (multiple-value-bind (h) (memory-host (user-lines 3))
    (let ((c (compact-on h (make-hconfig :summary-max 12 :consolidate-off t) "manual" "a2" 0)))
      (check (cresult-kind c) "pointer")
      (check (<= (estimate-tokens (cresult-summary c)) 12) t)
      (check (jget (jget (json-parse (cresult-details-json c)) "consolidation") "reason") "consolidate is false")))
  ;; A cut not on the branch reads nothing.
  (multiple-value-bind (h) (memory-host (user-lines 2))
    (let ((c (compact-on h (make-hconfig) "overflow" "gone" 7)))
      (check (cresult-tokens-before c) 7)
      (check (jget (jget (json-parse (cresult-details-json c)) "consolidation") "reason")
             "an overflow compaction runs level 0 only, so recovery is not delayed"))))

(deftest hooks-host-summary
  (let* ((entries (coerce (append (user-lines 2)
                                  (list (hentry "{\"type\":\"compaction\",\"id\":\"c1\",\"timestamp\":\"2026-05-01T11:00:00Z\",\"summary\":\"## Done\\nthe deploy works\",\"firstKeptEntryId\":\"u1\"}"))
                                  (user-lines 1))
                          'simple-vector)))
    (multiple-value-bind (o covers) (host-summary entries (fold-ledger entries))
      (check (obs-content o) "## Done the deploy works")
      (check (obs-origin o) "host")
      (check (obs-source-ids o) '("c1"))
      (check covers "a0" "the entry before the kept range")
      (check (obs-id o) (hash-id (cat "host" (string (code-char 0)) "c1"))))
    (check (host-summary (coerce (user-lines 2) 'simple-vector) (fold-ledger (coerce (user-lines 2) 'simple-vector))) nil)))

(deftest hooks-day-headers
  (let ((messages (mapcar #'json-parse
                          '("{\"role\":\"user\",\"timestamp\":\"2023-05-30T10:00:00Z\",\"content\":\"one\"}"
                            "{\"role\":\"assistant\",\"timestamp\":\"2023-05-30T10:00:01Z\",\"content\":[]}"
                            "{\"role\":\"user\",\"timestamp\":\"2023-05-30T11:00:00Z\",\"content\":\"same day\"}"
                            "{\"role\":\"user\",\"timestamp\":1685577600000,\"content\":[{\"type\":\"text\",\"text\":\"[Sent on Thursday 2023-06-01]\"}]}"
                            "{\"role\":\"user\",\"content\":\"no time\"}"))))
    (check (day-headers messages) 1)
    (check (json-string (jget (first messages) "content"))
           "[{\"text\":\"[Sent on Tuesday 2023-05-30]\",\"type\":\"text\"},{\"text\":\"one\",\"type\":\"text\"}]")
    (check (jget (third messages) "content") "same day")
    (check (day-header "nope") nil)))

(defun thin-branch ()
  (mapcar #'hentry
          (list "{\"type\":\"message\",\"id\":\"u1\",\"message\":{\"role\":\"user\",\"content\":\"read it\"}}"
                "{\"type\":\"message\",\"id\":\"a1\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"id\":\"c1\",\"name\":\"bash\",\"arguments\":{\"command\":\"cat big.txt\"}}]}}"
                (format nil "{\"type\":\"message\",\"id\":\"t1\",\"message\":{\"role\":\"toolResult\",\"toolCallId\":\"c1\",\"toolName\":\"bash\",\"content\":[{\"type\":\"text\",\"text\":\"~A\"}]}}"
                        (make-string 800 :initial-element #\x))
                "{\"type\":\"message\",\"id\":\"a2\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"id\":\"c2\",\"name\":\"bash\",\"arguments\":{\"command\":\"false\"}}]}}"
                "{\"type\":\"message\",\"id\":\"t2\",\"message\":{\"role\":\"toolResult\",\"toolCallId\":\"c2\",\"toolName\":\"bash\",\"isError\":true,\"content\":[{\"type\":\"text\",\"text\":\"boom\"}]}}"
                "{\"type\":\"message\",\"id\":\"a3\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}"
                "{\"type\":\"compaction\",\"id\":\"k1\",\"summary\":\"s\",\"firstKeptEntryId\":\"u1\"}")))

(deftest hooks-thinning
  (let ((plan (thin-plan (coerce (thin-branch) 'simple-vector) (list "memory") "/work")))
    (check (mapcar #'car plan) '("c1") "the newest round stays whole")
    (check (cdr (first plan)) "[thinned] bash \"cat big.txt\" worked; the full result: memory: om.Recall(`t1`)"))
  (check (thin-plan (coerce (butlast (thin-branch)) 'simple-vector) '() "") nil "no compaction, no plan")
  (let ((messages (mapcar #'json-parse '("{\"role\":\"toolResult\",\"toolCallId\":\"c1\",\"content\":\"long\"}"
                                         "{\"role\":\"toolResult\",\"toolCallId\":\"c9\",\"content\":\"other\"}"))))
    (check (on-context messages (coerce (thin-branch) 'simple-vector) (list "memory") "/work") 1)
    (check (jget (aref (jget (first messages) "content") 0) "text")
           "[thinned] bash \"cat big.txt\" worked; the full result: memory: om.Recall(`t1`)")
    (check (jget (second messages) "content") "other"))
  (let ((dup (mapcar #'json-parse '("{\"role\":\"toolResult\",\"toolCallId\":\"c1\",\"content\":\"a\"}"
                                    "{\"role\":\"toolResult\",\"toolCallId\":\"c1\",\"content\":\"b\"}"))))
    (check (thin-apply dup '(("c1" . "x"))) 0 "a repeated id is never thinned")))

(deftest hooks-view
  (multiple-value-bind (h branch) (memory-host (user-lines 2))
    (multiple-value-bind (batches covers) (extract (funcall branch) 0 4 (fold-ledger (funcall branch)))
      (append-extraction-entries h batches covers))
    (check (view-text (funcall branch) "")
           "Nothing compacted yet: 2 active observations are recorded; /om:view full shows them.")
    (check (view-text (funcall branch) "bogus") "Usage: /om:view [full]")
    (check (nth-value 1 (view-text (funcall branch) "full")) nil "full is copied")
    (check (has-prefix (view-text (funcall branch) "full") "── Reflections ──") t))
  (check (locale-int 1234567) "1,234,567")
  (check (locale-int -12) "-12"))

;;; The server

(deftest hooks-server
  (with-server-dir (dir)
    (om.server:start-server :root dir :socket (format nil "~Aengine.sock" dir) :idle-seconds 0)
    (let ((transcript (format nil "~At.jsonl" dir)))
      (write-lines transcript
                   (loop for i from 0 below 6
                         collect (format nil "{\"id\":\"m~D\",\"timestamp\":\"2026-05-01T10:00:00Z\",\"role\":\"user\",\"text\":\"Host h-~D listens on port ~D.\"}" i i (+ 9000 i))
                         collect (format nil "{\"id\":\"r~D\",\"timestamp\":\"2026-05-01T10:00:05Z\",\"role\":\"assistant\",\"text\":\"Noted.\"}" i)))
      (flet ((call (method &rest extra)
               (json-parse
                (rpc-line (json-string
                           (jobject "jsonrpc" "2.0" "id" 1 "method" method
                                    "params" (apply #'jobject "root" dir "harness" "w5" "session_id" "s1"
                                                    "transcript_path" transcript "format" "generic" "cwd" dir
                                                    extra)))))))
        (let ((r (jget (call "session-start" "with_summary" :true) "result")))
          (check (has-prefix (jget r "text") "The memory tool (MCP server om, tool memory):") t "no summary yet")
          (check (coerce (jget r "remaining") 'list) '("migrate-layer")))
        (call "turn-end")
        (let ((summary (jget (jget (call "compact" "reason" "manual") "result") "text")))
          (check (and (stringp summary) (string/= summary "") t) t "the summary")
          (check (jget (jget (call "session-start" "with_summary" :true) "result") "text")
                 (om.server::start-context summary om.layer::+empty-index+) "the summary at the next start")
          (check (jget (jget (call "prompt" "summary_on_prompt" :true) "result") "text")
                 (om.server::start-context summary om.layer::+empty-index+) "handed once")
          (check (jget (jget (call "prompt" "summary_on_prompt" :true) "result") "text") nil "not twice"))
        (let* ((store (store-load (format nil "~Asessions/w5/s1.jsonl" dir)))
               (types (mapcar (lambda (s) (or (jget (stored-entry s) "customType") (jget (stored-entry s) "type"))) store)))
          (check (subseq types (- (length types) 3)) '("om.cut" "compaction" "om.summary.handed")))
        (let ((c (jget (jget (call "before-compact" "first_kept_entry_id" "m1" "tokens_before" 50) "result") "compaction")))
          (check (jget c "firstKeptEntryId") "m1")
          (check (jget c "tokensBefore") 50d0))
        (let ((r (jget (call "context" "messages" (vector (jobject "role" "user" "timestamp" "2026-05-01T10:00:00Z" "content" "hi"))) "result")))
          (check (jget r "thinned") 0d0)
          (check (jget (aref (jget (aref (jget r "messages") 0) "content") 0) "text") "[Sent on Friday 2026-05-01]"))
        (check (jget (jget (call "view" "args" "full") "result") "notice") :false)
        ;; A curated store with memories: the index ranks them, after a settle that promotes the
        ;; staged one a user's words confirmed and tells the client so (1b).
        (ensure-directories-exist (format nil "~Aobservational-memory/memories/" dir))
        (write-lines (format nil "~Aobservational-memory/memories/x.md" dir) '("---"))
        (write-lines (format nil "~Aobservational-memory/memories/tea.md" dir)
                     '("---" "type: user" "description: Drinks green tea" "created: 2026-05-01T09:00:00Z"
                       "updated: 2026-05-01T09:00:00Z" "staged: true" "staged-by: s0" "---"))
        (write-lines (format nil "~Aobservational-memory/memories/build.md" dir)
                     '("---" "type: project" "description: Builds with make" "created: 2026-05-01T09:00:00Z"
                       "updated: 2026-05-01T09:00:00Z" "---"))
        (write-lines (format nil "~Aobservational-memory/memories/usage.jsonl" dir)
                     '("{\"time\":\"2026-05-01T09:30:00Z\",\"name\":\"tea\",\"event\":\"helped\",\"quote\":\"green tea\",\"user\":true}"))
        (let ((r (jget (call "session-start") "result")))
          (check (has-suffix (jget r "text") (format nil "~%~%- tea [user]: Drinks green tea~%- build [project]: Builds with make")) t
                 "the index")
          (check (coerce (jget r "notices") 'list)
                 '("Observational memory: staged memory tea is now live (a user's words quoted in feedback); it enters the memory index at its next render")))))))
