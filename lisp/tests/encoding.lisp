;;;; The encoding fields (contracts/encoding.md): the lexicon, versions, time hints and authority
;;;; against contracts/encoding.jsonl (which memory/ledger's TestEncodingContractCases reads too),
;;;; and level 0's records with a writer and a frame, byte for byte as memory/extract's
;;;; TestEncodeRecordsBytes writes them.

(in-package "OM")

(defparameter *encoding-tests-here* (make-pathname :name nil :type nil :defaults *load-truename*))

(defun encoding-cases ()
  (let ((path (merge-pathnames "../../contracts/encoding.jsonl" *encoding-tests-here*)))
    (loop for line in (split (read-file-string path) (string #\Newline))
          when (plusp (length line)) collect (json-parse (coerce line 'simple-string)))))

(deftest encoding-contract-cases
  (let ((cases (encoding-cases)))
    (check (>= (length cases) 50) t "cases read")
    (dolist (c cases)
      (let ((kind (jget c "kind")) (input (jget c "input")) (want (jget c "want")) (name (jget c "name")))
        (cond ((string= kind "hedge") (check (hedge-of input) want name))
              ((string= kind "versions") (check (versions-in input) (coerce want 'list) name))
              ((string= kind "timeHint")
               (let* ((m (json-parse input))
                      (o (make-obs :content (str (jget m "content")) :timestamp (str (jget m "timestamp"))
                                   :kind (str (jget m "kind")) :status (str (jget m "status"))
                                   :event-date (str (jget m "eventDate")))))
                 (check (time-hint-of o) want name)))
              ((string= kind "authority") (check (authority-of input) want name))
              ((string= kind "customSource") (check (source-of-custom-type input) want name))
              ((string= kind "pushback") (check (if (pushback-p input) "true" "false") want name))
              ((string= kind "concedes") (check (if (concedes-p input) "true" "false") want name))
              ((string= kind "restates")
               (let ((m (json-parse input)))
                 (check (if (restates-claim-p (make-obs :content (jget m "restatement")) (make-obs :content (jget m "claim"))) "true" "false")
                        want name)))
              ((string= kind "episode")
               (let ((m (json-parse input))) (check (episode-id (jget m "session") (jget m "entry")) want name)))
              (t (check kind "a known kind" name)))))))

(deftest encoding-keys-alist
  (let* ((a (keys-add (keys-add (keys-add '() "paths" " /a ") "paths" "/a") "nope" "x"))
         (b (keys-add a "paths" "/b"))
         (c (keys-add b "commands" "make")))
    (check a '(("paths" "/a")))
    (check b '(("paths" "/a" "/b")))
    (check c '(("paths" "/a" "/b") ("commands" "make")) "write order")
    (check (keys-union '(("errors" "x")) c) '(("paths" "/a" "/b") ("commands" "make") ("errors" "x")))))

(deftest encoding-round-trip
  (let* ((text "{\"id\":\"0123456789ab\",\"content\":\"x\",\"timestamp\":\"2026-11-02 10:00\",\"relevance\":\"medium\",\"sourceEntryIds\":[\"e1\"],\"tokenCount\":3,\"source\":\"user-said\",\"authority\":\"user\",\"hedge\":\"hedged\",\"timeHint\":\"after:2026-11-02\",\"episode\":\"ep1\",\"keys\":{\"commands\":[\"go test ./...\"]},\"frame\":{\"repoId\":\"r\",\"head\":\"h\"},\"writer\":{\"harness\":\"pig\",\"engine\":\"go\"}}")
         (o (observation-from (json-parse text))))
    (check (obs-json o) text "round trip")
    (let ((bad (observation-from (json-parse "{\"id\":\"0123456789ab\",\"content\":\"x\",\"timestamp\":\"t\",\"relevance\":\"medium\",\"sourceEntryIds\":[\"e1\"],\"tokenCount\":0,\"source\":\"nope\",\"authority\":\"root\",\"hedge\":1,\"timeHint\":\"after:soon\",\"keys\":{\"paths\":[\"\",3,\"/a\"],\"other\":[\"x\"]},\"frame\":\"x\"}"))))
      (check (obs-json bad) "{\"id\":\"0123456789ab\",\"content\":\"x\",\"timestamp\":\"t\",\"relevance\":\"medium\",\"sourceEntryIds\":[\"e1\"],\"tokenCount\":0,\"keys\":{\"paths\":[\"/a\"]}}" "lenient"))))

(defun encoding-test-branch ()
  (branch-of (jobject "id" "u1" "timestamp" "2026-05-01T10:00:00Z" "role" "user" "text" "I think the port is 8080. Pin react@18.3.1 please.")
             (jobject "id" "a1" "timestamp" "2026-05-01T10:01:00Z" "role" "assistant" "text" "Apparently the tests are flaky."
                      "tools" (vector (jobject "id" "c1" "name" "bash" "arguments" (jobject "command" "make test"))))
             (jobject "id" "r1" "timestamp" "2026-05-01T10:02:00Z" "role" "tool" "toolCallId" "c1" "toolName" "bash"
                      "text" (format nil "boom: no rule~%Command exited with code 2"))
             (jobject "id" "a2" "timestamp" "2026-05-01T10:03:00Z" "role" "assistant" "text" ""
                      "tools" (vector (jobject "id" "c2" "name" "write" "arguments" (jobject "path" "src/a.go"))))
             (jobject "id" "r2" "timestamp" "2026-05-01T10:04:00Z" "role" "tool" "toolCallId" "c2" "toolName" "write" "text" "ok")))

(deftest encoding-level0-records
  (let* ((branch (encoding-test-branch))
         (*encoding* (list :writer (make-record-writer :harness "golden" :session "s1" :engine "go")
                           :frame (make-record-frame :repo-id "r1" :branch "main" :head "abc")
                           :root "/work"))
         (records (multiple-value-bind (batches) (extract branch 0 (length branch) (fold-ledger (vector)) :cwd "/work")
                    (loop for b in batches append (batch-observations b)))))
    (check (format nil "~{~A~%~}" (mapcar #'obs-json records))
           (read-file-string (merge-pathnames "../../memory/extract/testdata/encoding-records.jsonl" *encoding-tests-here*)))))

(deftest encoding-level1-least-authority
  ;; consolidate.TestLeastAuthorityThroughLevel1.
  (let* ((user (make-obs :id "u" :authority "user" :hedge "stated"))
         (tool (make-obs :id "t" :authority "tool" :hedge "unverified"))
         (legacy (make-obs :id "l"))
         (writer (make-record-writer :harness "pig" :session "s" :engine "go"))
         (v (make-vstate :in (make-vinput :writer writer)
                         :shown (let ((h (make-hash-table :test 'equal)))
                                  (dolist (o (list user tool legacy) h) (setf (gethash (obs-id o) h) o))))))
    (flet ((held (&rest ids)
             (let ((r (make-obs :authority "user" :hedge "hedged")))
               (v-least-provenance v r ids)
               (list (obs-authority r) (obs-hedge r) (obs-writer r)))))
      (check (held "u" "t") (list "tool" "unverified" writer) "user and tool")
      (check (held "u") (list "user" "hedged" writer) "user alone")
      (check (held "u" "l") (list "" "hedged" writer) "a legacy source"))))
