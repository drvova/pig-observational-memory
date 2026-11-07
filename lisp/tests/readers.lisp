;;;; The transcript readers (memory/transcript): the samples and expectations of Go's
;;;; hookformats_test.go, transcript_test.go and real_test.go, and the helpers' Go renderings.

(in-package "OM")

(defvar *readers-test-dir* nil)

(defun readers-test-file (&rest lines)
  "A transcript file holding lines, one per line, in a temporary directory."
  (unless *readers-test-dir*
    (setf *readers-test-dir* (sb-posix:mkdtemp (format nil "~A/om-readers-XXXXXX" (or (sb-posix:getenv "TMPDIR") "/tmp")))))
  (let ((path (format nil "~A/t~D.jsonl" *readers-test-dir* (random 1000000000))))
    (with-open-file (out path :direction :output :external-format :utf-8 :if-exists :supersede)
      (dolist (l lines) (write-string l out) (terpri out)))
    path))

(defun read-sample (format &rest lines)
  (transcript-file-entries format (apply #'readers-test-file lines)))

(defun entry-roles (entries)
  (join (mapcar (lambda (e) (cat (entry-id e) ":" (or (entry-role e) ""))) entries) " "))

(defun nth-block (e n) (aref (jget (entry-message e) "content") n))

(defparameter *readers-tests-here* (make-pathname :name nil :type nil :defaults *load-truename*))

(defun real-entries (format file)
  (transcript-file-entries format (namestring (merge-pathnames (cat "../../memory/transcript/testdata/real/" file) *readers-tests-here*))))

(defun role-counts (entries)
  (let ((counts '()))
    (dolist (e entries)
      (let ((r (or (entry-role e) "")))
        (unless (member r '("" "system") :test #'string=)
          (let ((cell (assoc r counts :test #'string=)))
            (if cell (incf (cdr cell)) (push (cons r 1) counts))))))
    (sort counts #'string< :key #'car)))

(deftest readers-go-renderings
  (check (mapcar #'go-sprint (coerce (json-parse "[1000000, 123456, 1234567, 0.0001, 0.00001, -2.5, 1e21, {\"b\":[1,null,true,\"x y\"],\"a\":{}}, []]") 'list))
         '("1e+06" "123456" "1.234567e+06" "0.0001" "1e-05" "-2.5" "1e+21" "map[a:map[] b:[1 <nil> true x y]]" "[]"))
  (check (mapcar #'unix-time '(1793787302.1d0 -1.5d0 0.0005d0)) '("2026-11-04T10:15:02.099Z" "1969-12-31T23:59:58.500Z" "1970-01-01T00:00:00.000Z"))
  (check (unix-time "1") "")
  (check (unix-seconds 1762300000.9d0) "2025-11-04T23:46:40Z")
  (check (unix-millis 1762300000000d0) "2025-11-04T23:46:40.000Z")
  (check (sql-quote "it's") "'it''s'")
  (check (json-column "{\"a\":1") nil)
  (check (json-column 5d0) 5d0))

(deftest readers-codex
  (let ((es (read-sample "codex"
                         "{\"type\":\"session_meta\",\"payload\":{}}"
                         "{\"type\":\"response_item\",\"timestamp\":\"2026-11-04T10:00:00.000Z\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"<environment_context>x</environment_context>\"}]}}"
                         "{\"type\":\"response_item\",\"timestamp\":\"2026-11-04T10:00:01.000Z\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"list files\"}]}}"
                         "{\"type\":\"response_item\",\"payload\":{\"type\":\"reasoning\",\"summary\":[{\"text\":\"use ls\"}]}}"
                         "{\"type\":\"response_item\",\"payload\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}"
                         "{\"type\":\"response_item\",\"payload\":{\"type\":\"function_call_output\",\"call_id\":\"c1\",\"output\":\"a.go\"}}"
                         "{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"One file.\"}]}}")))
    (check (entry-roles es) "cx000002:user cx000003:assistant cx000005:toolResult cx000006:assistant")
    (check (entry-aliases (second es)) '("cx000004"))
    (check (jget (nth-block (second es) 1) "name") "shell")
    (check (jget (jget (nth-block (second es) 1) "arguments") "cmd") "ls")
    (check (jget (entry-message (third es)) "toolName") "shell")))

(deftest readers-gemini
  (let ((es (read-sample "gemini"
                         "{\"sessionId\":\"3f2a\",\"projectHash\":\"ab12\",\"startTime\":\"2026-11-04T10:15:00.000Z\",\"lastUpdated\":\"2026-11-04T10:15:00.000Z\",\"kind\":\"main\"}"
                         "{\"id\":\"u1\",\"timestamp\":\"2026-11-04T10:15:02.000Z\",\"type\":\"user\",\"content\":[{\"text\":\"List the Go files\"}]}"
                         "{\"id\":\"g1\",\"timestamp\":\"2026-11-04T10:15:04.000Z\",\"type\":\"gemini\",\"content\":\"I'll list them.\",\"thoughts\":[{\"subject\":\"Plan\",\"description\":\"Use ls\",\"timestamp\":\"2026-11-04T10:15:03.000Z\"}],\"model\":\"m\"}"
                         "{\"id\":\"g1\",\"timestamp\":\"2026-11-04T10:15:04.000Z\",\"type\":\"gemini\",\"content\":\"I'll list them.\",\"thoughts\":[],\"model\":\"m\",\"toolCalls\":[{\"id\":\"call-1\",\"name\":\"run_shell_command\",\"args\":{\"command\":\"ls *.go\"},\"result\":[{\"functionResponse\":{\"id\":\"call-1\",\"name\":\"run_shell_command\",\"response\":{\"output\":\"main.go\"}}}],\"status\":\"success\",\"timestamp\":\"2026-11-04T10:15:05.000Z\"}]}"
                         "{\"id\":\"u2\",\"timestamp\":\"2026-11-04T10:15:05.100Z\",\"type\":\"user\",\"content\":[{\"functionResponse\":{\"id\":\"call-1\",\"name\":\"run_shell_command\",\"response\":{\"output\":\"main.go\"}}}]}"
                         "{\"id\":\"x1\",\"timestamp\":\"2026-11-04T10:15:06.000Z\",\"type\":\"info\",\"content\":\"cli note\"}"
                         "{\"id\":\"g2\",\"timestamp\":\"2026-11-04T10:15:07.000Z\",\"type\":\"gemini\",\"content\":\"There is one file: main.go.\",\"thoughts\":[],\"tokens\":null,\"model\":\"m\"}"
                         "{\"$set\":{\"lastUpdated\":\"2026-11-04T10:15:07.000Z\"}}"
                         "{\"id\":\"u3\",\"timestamp\":\"2026-11-04T10:16:00.000Z\",\"type\":\"user\",\"content\":[{\"text\":\"undo me\"}]}"
                         "{\"$rewindTo\":\"u3\"}")))
    (check (entry-roles es) "u1:user g1:assistant u2:toolResult g2:assistant")
    (check (length (jget (entry-message (second es)) "content")) 2)
    (check (jget (nth-block (second es) 1) "name") "run_shell_command")
    (check (jget (entry-message (third es)) "toolCallId") "call-1")
    (check (jget (entry-message (third es)) "isError") :false))
  (let ((es (real-entries "gemini" "gemini-set-patch.jsonl")))
    (check (entry-roles es) "m1:user m2:assistant m2#r0:toolResult m3:user")
    (check (jget (nth-block (second es) 0) "text") "Updated model response")
    (check (jget (nth-block (third es) 0) "text") "masked-output"))
  (check (role-counts (real-entries "gemini" "gemini-legacy-session.json"))
         '(("assistant" . 37) ("toolResult" . 82) ("user" . 3))))

(deftest readers-qwen
  (let ((es (read-sample "qwen"
                         "{\"uuid\":\"a1\",\"parentUuid\":null,\"timestamp\":\"2026-11-04T10:15:02.000Z\",\"type\":\"user\",\"message\":{\"role\":\"user\",\"parts\":[{\"text\":\"List the Go files\"}]}}"
                         "{\"uuid\":\"a2\",\"parentUuid\":\"a1\",\"timestamp\":\"2026-11-04T10:15:04.000Z\",\"type\":\"assistant\",\"message\":{\"role\":\"model\",\"parts\":[{\"text\":\"Checking\",\"thought\":true},{\"text\":\"I'll list them.\"},{\"functionCall\":{\"id\":\"call_1\",\"name\":\"run_shell_command\",\"args\":{\"command\":\"ls *.go\"}}}]}}"
                         "{\"uuid\":\"a3\",\"parentUuid\":\"a2\",\"timestamp\":\"2026-11-04T10:15:05.000Z\",\"type\":\"tool_result\",\"message\":{\"role\":\"user\",\"parts\":[{\"functionResponse\":{\"id\":\"call_1\",\"name\":\"run_shell_command\",\"response\":{\"output\":\"no such file\"}}}]},\"toolCallResult\":{\"callId\":\"call_1\",\"status\":\"error\"}}"
                         "{\"uuid\":\"c1\",\"parentUuid\":\"a3\",\"timestamp\":\"2026-11-04T10:15:06.000Z\",\"type\":\"user\",\"subtype\":\"chat_compression\",\"message\":{\"role\":\"user\",\"parts\":[{\"text\":\"summary\"}]}}"
                         "{\"uuid\":\"a4\",\"parentUuid\":\"c1\",\"timestamp\":\"2026-11-04T10:15:07.000Z\",\"type\":\"assistant\",\"message\":{\"role\":\"model\",\"parts\":[{\"text\":\"There is one file: main.go.\"}]}}")))
    (check (entry-roles es) "a1:user a2:assistant a3:toolResult a4:assistant")
    (check (jget (nth-block (second es) 0) "type") "thinking")
    (check (jget (nth-block (second es) 2) "type") "toolCall")
    (check (jget (entry-message (third es)) "isError") :true)))

(deftest readers-kimi
  (let ((es (read-sample "kimi"
                         "{\"type\":\"metadata\",\"protocol_version\":\"1.10\"}"
                         "{\"timestamp\":1793787302.1,\"message\":{\"type\":\"TurnBegin\",\"payload\":{\"user_input\":\"List the Go files\"}}}"
                         "{\"timestamp\":1793787303.0,\"message\":{\"type\":\"StepBegin\",\"payload\":{\"n\":1}}}"
                         "{\"timestamp\":1793787303.5,\"message\":{\"type\":\"ContentPart\",\"payload\":{\"type\":\"think\",\"think\":\"Use ls.\"}}}"
                         "{\"timestamp\":1793787303.9,\"message\":{\"type\":\"ContentPart\",\"payload\":{\"type\":\"text\",\"text\":\"I'll list them.\"}}}"
                         "{\"timestamp\":1793787304.2,\"message\":{\"type\":\"ToolCall\",\"payload\":{\"type\":\"function\",\"id\":\"tool_abc\",\"function\":{\"name\":\"Shell\",\"arguments\":\"{\\\"command\\\": \\\"ls *.go\\\"}\"},\"extras\":null}}}"
                         "{\"timestamp\":1793787305.0,\"message\":{\"type\":\"ToolResult\",\"payload\":{\"tool_call_id\":\"tool_abc\",\"return_value\":{\"is_error\":true,\"output\":\"permission denied\",\"message\":\"\",\"display\":[],\"extras\":null}}}}"
                         "{\"timestamp\":1793787306.0,\"message\":{\"type\":\"ContentPart\",\"payload\":{\"type\":\"text\",\"text\":\"It failed.\"}}}"
                         "{\"timestamp\":1793787307.0,\"message\":{\"type\":\"TurnEnd\",\"payload\":{}}}")))
    (check (entry-roles es) "kw000001:user kw000003:assistant kw000006:toolResult kw000007:assistant")
    (let ((call (nth-block (second es) 2)))
      (check (jget call "name") "Shell")
      (check (jget (jget call "arguments") "command") "ls *.go"))
    (check (jget (entry-message (third es)) "isError") :true)
    (check (jget (entry-message (third es)) "toolName") "Shell")
    (check (has-prefix (entry-timestamp (first es)) "2026-") t))
  (check (cdr (assoc "toolResult" (role-counts (real-entries "kimi" "kimi-wire.jsonl")) :test #'string=)) 1))

(deftest readers-droid-copilot-pi
  (let ((es (read-sample "droid"
                         "{\"type\":\"session_start\",\"id\":\"s1\",\"title\":\"t\",\"owner\":\"me\",\"version\":2,\"cwd\":\"/p\"}"
                         "{\"type\":\"message\",\"id\":\"m1\",\"timestamp\":\"2026-11-04T10:00:01.000Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"List files\"}]}}"
                         "{\"type\":\"message\",\"id\":\"m2\",\"timestamp\":\"2026-11-04T10:00:02.000Z\",\"parentId\":\"m1\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"thinking\",\"thinking\":\"ls\",\"signature\":\"x\"},{\"type\":\"text\",\"text\":\"Listing.\"},{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"Execute\",\"input\":{\"command\":\"ls\"}}]}}"
                         "{\"type\":\"message\",\"id\":\"m3\",\"timestamp\":\"2026-11-04T10:00:03.000Z\",\"parentId\":\"m2\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"t1\",\"content\":\"a.go\\nb.go\",\"is_error\":false}]}}"
                         "{\"type\":\"compaction_state\",\"id\":\"c1\",\"timestamp\":\"2026-11-04T10:00:03.500Z\",\"summaryText\":\"earlier\"}"
                         "{\"type\":\"message\",\"id\":\"m4\",\"timestamp\":\"2026-11-04T10:00:04.000Z\",\"parentId\":\"m3\",\"message\":{\"role\":\"assistant\",\"content\":\"Two files.\"}}")))
    (check (entry-roles es) "m1:user m2:assistant m3:toolResult m4:assistant")
    (check (jget (entry-message (third es)) "toolName") "Execute")
    (check (jget (nth-block (third es) 0) "text") (format nil "a.go~%b.go"))
    (check (length (jget (entry-message (second es)) "content")) 3))
  (let ((es (read-sample "copilot"
                         "{\"id\":\"e1\",\"parentId\":null,\"timestamp\":\"2026-11-04T10:00:00.000Z\",\"type\":\"session.start\",\"data\":{\"sessionId\":\"s1\"}}"
                         "{\"id\":\"e2\",\"parentId\":\"e1\",\"timestamp\":\"2026-11-04T10:00:01.000Z\",\"type\":\"user.message\",\"data\":{\"content\":\"List files\"}}"
                         "{\"id\":\"e3\",\"parentId\":\"e2\",\"timestamp\":\"2026-11-04T10:00:02.000Z\",\"type\":\"assistant.message\",\"data\":{\"messageId\":\"m1\",\"content\":\"Listing.\",\"reasoningText\":\"use ls\",\"toolRequests\":[{\"toolCallId\":\"c1\",\"name\":\"bash\",\"arguments\":{\"command\":\"ls\"}}]}}"
                         "{\"id\":\"e4\",\"parentId\":\"e3\",\"timestamp\":\"2026-11-04T10:00:02.100Z\",\"type\":\"tool.execution_start\",\"data\":{\"toolCallId\":\"c1\",\"toolName\":\"bash\"}}"
                         "{\"id\":\"e5\",\"parentId\":\"e4\",\"timestamp\":\"2026-11-04T10:00:03.000Z\",\"type\":\"tool.execution_complete\",\"data\":{\"toolCallId\":\"c1\",\"success\":false,\"error\":{\"message\":\"denied\"}}}"
                         "{\"id\":\"e6\",\"parentId\":\"e5\",\"timestamp\":\"2026-11-04T10:00:03.500Z\",\"type\":\"assistant.message_delta\",\"ephemeral\":true,\"data\":{\"deltaContent\":\"x\"}}"
                         "{\"id\":\"e7\",\"parentId\":\"e5\",\"timestamp\":\"2026-11-04T10:00:04.000Z\",\"type\":\"assistant.message\",\"data\":{\"messageId\":\"m2\",\"content\":\"It failed.\"}}"
                         "{\"id\":\"e8\",\"parentId\":\"e7\",\"timestamp\":\"2026-11-04T10:00:05.000Z\",\"type\":\"assistant.message\",\"agentId\":\"sub1\",\"data\":{\"messageId\":\"m3\",\"content\":\"subagent\"}}")))
    (check (entry-roles es) "e2:user e3:assistant e5:toolResult e7:assistant")
    (check (jget (entry-message (third es)) "toolName") "bash")
    (check (jget (entry-message (third es)) "isError") :true)
    (check (jget (nth-block (third es) 0) "text") "denied"))
  (let* ((title "{\"type\":\"title\",\"title\":\"t\"}")
         (es (read-sample "pi"
                          (cat title (make-string (- 255 (length title)) :initial-element #\Space))
                          "{\"type\":\"session\",\"version\":3,\"id\":\"s1\",\"cwd\":\"/p\"}"
                          "{\"type\":\"message\",\"id\":\"e1\",\"parentId\":null,\"timestamp\":\"2026-11-04T10:00:00.000Z\",\"message\":{\"role\":\"user\",\"content\":\"hi\",\"timestamp\":1}}"
                          "{\"type\":\"model_usage\",\"id\":\"e2\",\"parentId\":\"e1\",\"timestamp\":\"2026-11-04T10:00:01.000Z\"}"
                          "{\"type\":\"message\",\"id\":\"e3\",\"parentId\":\"e2\",\"timestamp\":\"2026-11-04T10:00:02.000Z\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"hello\"}],\"timestamp\":2}}")))
    (check (mapcar #'entry-id es) '("e1" "e2" "e3")))
  (check (role-counts (real-entries "pi" "oh-my-pi-session.jsonl"))
         '(("assistant" . 55) ("toolResult" . 56) ("user" . 6))))

(deftest readers-deepagents-cursor-cline
  (let ((es (read-sample "deepagents"
                         "{\"sequence\":0,\"record_id\":\"s0\",\"role\":\"system\",\"content\":\"rules\"}"
                         "{\"sequence\":1,\"record_id\":\"m1\",\"role\":\"user\",\"content\":\"fix the test\"}"
                         "{\"sequence\":2,\"record_id\":\"m2\",\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"Running it.\"},{\"type\":\"tool_use\",\"id\":\"tu1\",\"name\":\"execute\",\"input\":{\"command\":\"pytest\"}}]}"
                         "{\"sequence\":3,\"record_id\":\"m3\",\"role\":\"tool\",\"content\":\"1 passed\",\"name\":\"execute\"}"
                         "{\"sequence\":4,\"record_id\":\"assistant:4\",\"role\":\"assistant\",\"content\":\"Done.\"}")))
    (check (entry-roles es) "m1:user m2:assistant m3:toolResult assistant:4:assistant")
    (check (jget (entry-message (third es)) "toolCallId") "tu1"))
  (let ((es (read-sample "cursor"
                         "{\"type\":\"metadata\",\"metadata\":{\"overview\":\"x\"}}"
                         "{\"role\":\"user\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"List files\"}]}}"
                         "{\"role\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Listing.\"},{\"type\":\"tool_use\",\"name\":\"Shell\",\"input\":{\"command\":\"ls\"}}]}}"
                         "{\"role\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Two files: a.go, b.go.\"}]}}"
                         "{\"type\":\"turn_ended\",\"status\":\"success\"}")))
    (check (entry-roles es) "cu000002:user cu000003:assistant cu000004:assistant")
    (check (jget (nth-block (second es) 1) "id") "cu000003.1"))
  (let ((es (read-sample "cline"
                         "{"
                         "  \"version\": 1, \"sessionId\": \"s1\","
                         "  \"messages\": ["
                         "    {\"id\": \"X1\", \"role\": \"user\", \"content\": [{\"type\": \"text\", \"text\": \"read a\"}], \"ts\": 1762300000000},"
                         "    {\"id\": \"X2\", \"role\": \"assistant\", \"content\": [{\"type\": \"tool_use\", \"id\": \"toolu_1\", \"name\": \"read_file\", \"input\": {\"path\": \"a\"}}], \"ts\": 1762300001000},"
                         "    {\"id\": \"X3\", \"role\": \"user\", \"content\": [{\"type\": \"tool_result\", \"tool_use_id\": \"toolu_1\", \"content\": \"hello\", \"is_error\": false}]},"
                         "    {\"id\": \"X4\", \"role\": \"assistant\", \"content\": [{\"type\": \"text\", \"text\": \"It says hello.\"}]}"
                         "  ]"
                         "}")))
    (check (entry-roles es) "X1:user X2:assistant X3:toolResult X4:assistant")
    (check (jget (entry-message (third es)) "toolName") "read_file")
    (check (entry-timestamp (first es)) "2025-11-04T23:46:40.000Z"))
  (let ((es (real-entries "cline" "cline-string-content.messages.json")))
    (check (length es) 1)
    (check (jget (nth-block (first es) 0) "text") "Bun 1.3.14 is current.")))

(deftest readers-neovate-qoder-iflow
  (let ((es (read-sample "neovate"
                         "{\"type\":\"config\",\"key\":\"model\",\"value\":\"x\"}"
                         "{\"type\":\"message\",\"uuid\":\"u1\",\"parentUuid\":null,\"timestamp\":\"2026-11-05T00:00:00Z\",\"role\":\"user\",\"content\":\"list files\"}"
                         "{\"type\":\"message\",\"uuid\":\"u2\",\"parentUuid\":\"u1\",\"timestamp\":\"2026-11-05T00:00:01Z\",\"role\":\"assistant\",\"content\":[{\"type\":\"reasoning\",\"text\":\"ls\"},{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"bash\",\"input\":{\"command\":\"ls\"}}],\"text\":\"\"}"
                         "{\"type\":\"message\",\"uuid\":\"u3\",\"parentUuid\":\"u2\",\"timestamp\":\"2026-11-05T00:00:02Z\",\"role\":\"tool\",\"content\":[{\"type\":\"tool-result\",\"toolCallId\":\"t1\",\"toolName\":\"bash\",\"input\":{},\"result\":{\"llmContent\":\"a.go\",\"isError\":true}}]}"
                         "{\"type\":\"message\",\"uuid\":\"c1\",\"parentUuid\":null,\"timestamp\":\"2026-11-05T00:01:00Z\",\"role\":\"user\",\"uiContent\":\"Chat history compacted successfully.\",\"content\":[{\"type\":\"text\",\"text\":\"<context_summary>...</context_summary>\"}]}"
                         "{\"type\":\"message\",\"uuid\":\"u4\",\"parentUuid\":\"c1\",\"timestamp\":\"2026-11-05T00:01:01Z\",\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"next\"}]}")))
    (check (entry-roles es) "u1:user u2:assistant u3:toolResult u4:user")
    (check (jget (entry-message (third es)) "isError") :true))
  (check (entry-roles (read-sample "qoder" "{\"type\":\"user\",\"uuid\":\"u1\",\"parentUuid\":null,\"timestamp\":\"2026-11-05T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"hi\"}}"))
         "u1:user")
  (check (entry-roles (read-sample "qoder" "{\"uuid\":\"q1\",\"parentUuid\":null,\"timestamp\":\"2026-11-05T00:00:00Z\",\"type\":\"user\",\"message\":{\"role\":\"user\",\"parts\":[{\"text\":\"hi\"}]}}"))
         "q1:user")
  (let ((es (read-sample "iflow"
                         "{\"uuid\":\"a1\",\"parentUuid\":null,\"timestamp\":\"2026-11-05T10:00:00.000Z\",\"type\":\"user\",\"isSidechain\":false,\"message\":{\"role\":\"user\",\"content\":\"fix bug\"}}"
                         "{\"uuid\":\"a2\",\"parentUuid\":\"a1\",\"timestamp\":\"2026-11-05T10:00:01.000Z\",\"type\":\"assistant\",\"message\":{\"id\":\"msg-1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"thinking\",\"text\":\"look\",\"thinking\":{\"subject\":\"s\",\"description\":\"d\"}},{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"read_file\",\"input\":{\"path\":\"a.go\"}}]}}"
                         "{\"uuid\":\"a3\",\"parentUuid\":\"a2\",\"timestamp\":\"2026-11-05T10:00:02.000Z\",\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"tool_use_id\":\"call_1\",\"type\":\"tool_result\",\"content\":\"package a\"}]}}"
                         "{\"uuid\":\"a9\",\"parentUuid\":null,\"timestamp\":\"2026-11-05T10:05:00.000Z\",\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"<summary>\"},\"isCompactSummary\":true}"
                         "{\"uuid\":\"a10\",\"parentUuid\":\"a9\",\"timestamp\":\"2026-11-05T10:05:00.100Z\",\"type\":\"user\",\"isMeta\":true,\"message\":{\"role\":\"user\",\"content\":\"caveat\"}}"
                         "{\"uuid\":\"a11\",\"parentUuid\":\"a10\",\"timestamp\":\"2026-11-05T10:06:00.000Z\",\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"next\"}}")))
    (check (entry-roles es) "a1:user a2:assistant a3:toolResult a11:user")
    (check (jget (nth-block (second es) 0) "thinking") "look")))

(deftest readers-cortex-auggie-kiro
  (let ((es (read-sample "cortex"
                         "{\"role\":\"user\",\"id\":\"msg_1\",\"content\":[{\"type\":\"text\",\"text\":\"fix bug\",\"is_user_prompt\":true}],\"user_sent_time\":\"2026-11-05T10:00:00Z\"}"
                         "{\"role\":\"assistant\",\"id\":\"msg_2\",\"content\":[{\"type\":\"text\",\"text\":\"draft\"}]}"
                         "{\"role\":\"user\",\"id\":\"msg_3\",\"content\":[{\"type\":\"tool_result\",\"tool_result\":{\"tool_use_id\":\"tu_1\",\"name\":\"bash\",\"content\":[{\"type\":\"text\",\"text\":\"a.go\"}],\"status\":\"error\"}}]}"
                         "{\"role\":\"assistant\",\"id\":\"msg_2\",\"content\":[{\"type\":\"thinking\",\"thinking\":{\"text\":\"t\"}},{\"type\":\"tool_use\",\"tool_use\":{\"tool_use_id\":\"tu_1\",\"name\":\"bash\",\"input\":{\"command\":\"ls\"}}}]}")))
    (check (entry-roles es) "msg_1:user msg_2:assistant msg_3:toolResult")
    (check (length (jget (entry-message (second es)) "content")) 2)
    (check (jget (entry-message (third es)) "isError") :true))
  (let ((es (read-sample "cortex"
                         "{\"role\":\"assistant\",\"id\":\"m1:0\",\"message_id\":\"m1\",\"content\":[{\"type\":\"text\",\"text\":\"a\"}]}"
                         "{\"role\":\"assistant\",\"id\":\"m1:1\",\"message_id\":\"m1\",\"content\":[{\"type\":\"text\",\"text\":\"b\"}]}")))
    (check (length es) 1)
    (check (length (jget (entry-message (first es)) "content")) 2))
  (let ((es (read-sample "auggie"
                         (cat "{\"sessionId\":\"c1\",\"chatHistory\":["
                              "{\"exchange\":{\"request_id\":\"r1\",\"request_message\":\"fix bug\",\"response_text\":\"Looking\",\"request_nodes\":[],\"response_nodes\":[{\"id\":1,\"type\":5,\"content\":\"\",\"tool_use\":{\"tool_use_id\":\"tu1\",\"tool_name\":\"launch-process\",\"input_json\":\"{\\\"command\\\":\\\"ls\\\"}\"}}]},\"completed\":true,\"isHistorySummary\":false},"
                              "{\"exchange\":{\"request_id\":\"r2\",\"request_message\":\"\",\"response_text\":\"Two files.\",\"request_nodes\":[{\"id\":2,\"type\":1,\"tool_result_node\":{\"tool_use_id\":\"tu1\",\"content\":\"a.go b.go\",\"is_error\":false}}],\"response_nodes\":[]},\"completed\":true},"
                              "{\"exchange\":{\"request_id\":\"r3\",\"request_message\":\"summary\",\"response_text\":\"s\"},\"isHistorySummary\":true}]}"))))
    (check (entry-roles es) "r1:user r1:a:assistant r2#0:toolResult r2:a:assistant")
    (check (jget (jget (nth-block (second es) 1) "arguments") "command") "ls"))
  (let ((es (read-sample "kiro"
                         "{\"id\":\"k1\",\"timestamp\":\"2026-11-05T10:00:00Z\",\"payload\":{\"type\":\"user\",\"content\":\"hi\"}}"
                         "{\"id\":\"k1\",\"timestamp\":\"2026-11-05T10:00:01Z\",\"payload\":{\"type\":\"assistant\",\"operationType\":\"Reasoning\",\"content\":\"...\"}}"
                         "{\"id\":\"k1\",\"timestamp\":\"2026-11-05T10:00:01Z\",\"payload\":{\"type\":\"tool_call\",\"toolCallId\":\"c1\",\"toolName\":\"shell\",\"args\":{}}}"
                         "{\"id\":\"k2\",\"timestamp\":\"2026-11-05T10:00:02Z\",\"payload\":{\"type\":\"tool_result\",\"toolCallId\":\"c1\",\"content\":\"ok\",\"success\":true}}"
                         "{\"kind\":\"AssistantMessage\",\"data\":{\"message_id\":\"v2\",\"meta\":{\"timestamp\":1793787302.5},\"content\":[{\"kind\":\"text\",\"data\":\"done\"},{\"kind\":\"toolResult\",\"data\":{\"toolUseId\":\"c1\",\"status\":\"error\",\"content\":[{\"data\":\"x\"},{\"data\":{\"a\":\"<\"}},{\"data\":null}]}}]}}")))
    (check (entry-roles es) "k1:user k1#1:assistant k2:toolResult v2#0:toolResult v2:assistant")
    (check (jget (entry-message (second (cddr es))) "toolName") "shell")
    (check (jget (nth-block (fourth es) 0) "text") (format nil "x~%{\"a\":\"\\u003c\"}"))
    (check (entry-timestamp (fifth es)) "2026-11-04T10:15:02.500Z")))

(deftest readers-real-samples
  ;; real_test.go's checks on the corpus: the counts and ids it asserts.
  (check (role-counts (real-entries "qwen" "qwen-chat-records.jsonl")) (quote (("assistant" . 2) ("toolResult" . 1) ("user" . 1))))
  (let ((es (real-entries "claude-code" "openclaude-preserved-segment.jsonl")))
    (check (length es) 5)
    (check (entry-id (fourth es)) "00000000-0000-4000-8000-000000000013")
    (check (entry-id (fifth es)) "00000000-0000-4000-8000-000000000014"))
  (check (length (real-entries "claude-code" "openclaude-snip.jsonl")) 5)
  (check (cdr (assoc "toolResult" (role-counts (real-entries "neovate" "neovate-tool.jsonl")) :test #'string=)) 1)
  (check (entry-roles (real-entries "copilot" "copilot-user-message.json")) "11111111-1111-1111-1111-111111111111:user"))

(deftest readers-claude-code-origin
  ;; TestClaudeCodeOrigin: a user line with origin.kind other than human is a custom_message,
  ;; never a user entry; its meta lines stay left out; human and no origin stay user entries.
  (let ((es (transcript-file-entries "claude-code" (namestring (merge-pathnames "../../memory/transcript/testdata/synthetic/claude-code-origin.jsonl" *readers-tests-here*)))))
    (check (join (mapcar (lambda (e)
                           (cat (entry-id e) ":"
                                (if (string= (entry-type e) "message")
                                    (entry-role e)
                                    (cat (entry-type e) "(" (entry-custom-type e) ")"))))
                         es)
                 " ")
           "h1:user a1:assistant r1:toolResult a2:assistant n1:custom_message(origin:task-notification) p2:custom_message(origin:peer) a3:assistant u1:user")
    (check (search "<task-notification>" (str (jget (aref (entry-content (fifth es)) 0) "text"))) 0)
    (check (entry-timestamp (fifth es)) "2026-11-06T09:01:00.000Z"))
  (check (om.readers::origin-kind (jobject "origin" (jobject "mode" "user" "source" "cli"))) "")
  (check (om.readers::origin-kind (jobject "origin" (jobject "kind" "human"))) "")
  (check (om.readers::origin-kind (jobject "origin" "peer")) ""))

(deftest readers-sqlite-rows
  ;; real_test.go's Goose rows: a tool request, then its result in the legacy and new shapes.
  (let ((rows '()))
    (loop for n in '("3" "4" "5") for i from 0
          do (let ((m (json-parse-object (read-file-string (namestring (merge-pathnames (format nil "../../memory/transcript/testdata/real/goose-message-~A.json" n) *readers-tests-here*))))))
               (push (jobject "message_id" (cat "msg_" n) "role" (jget m "role")
                              "content_json" (json-string (jget m "content")) "metadata_json" (json-string (jget m "metadata"))
                              "created_timestamp" (coerce i 'double-float))
                     rows)))
    (setf rows (nreverse rows))
    (setf (gethash "content_json" (third rows))
          "[{\"type\":\"toolResponse\",\"id\":\"tool123\",\"toolResult\":{\"status\":\"success\",\"value\":{\"content\":[{\"type\":\"text\",\"text\":\"New format output\"}],\"isError\":false}}}]")
    (let ((es (read-goose-rows rows)))
      (check (length es) 3)
      (check (jget (nth-block (second es) 0) "text") "Tool output text")
      (check (jget (nth-block (third es) 0) "text") "New format output")))
  ;; The dumps, loaded with Python's sqlite3 module and read through query-sqlite.
  (when (look-path "python3")
    (dolist (c '(("crush-db.sql" "crush" "05966aef-d75c-4975-9531-9c4be1c9a43b")
                 ("goose-db.sql" "goose" "20261002_1")))
      (destructuring-bind (dump format session) c
        (let ((db (format nil "~A/~A.db" (or *readers-test-dir* (progn (readers-test-file) *readers-test-dir*)) format)))
          (run-capture (look-path "python3")
                       (list "-I" "-c" (format nil "import sqlite3, sys~%c = sqlite3.connect(sys.argv[1])~%c.executescript(open(sys.argv[2]).read())~%c.commit()")
                             db (namestring (merge-pathnames (cat "../../memory/transcript/testdata/real/" dump) *readers-tests-here*))))
          (let ((counts (role-counts (db-entries format db session))))
            (check (cdr (assoc "user" counts :test #'string=)) 2 dump)
            (check (cdr (assoc "toolResult" counts :test #'string=)) 5 dump))))))
  (check (query-sqlite "/nonexistent/om.db" "SELECT 1") nil))

(deftest readers-cleanup
  ;; Registered last among the readers' tests: removes the samples they wrote.
  (when *readers-test-dir*
    (sb-ext:delete-directory (cat *readers-test-dir* "/") :recursive t)
    (setf *readers-test-dir* nil))
  (check t t))

(defmacro with-env ((&rest pairs) &body body)
  "Runs body with the environment variables set (\"\" unsets one), restoring them after."
  (let ((saved (gensym "SAVED")))
    `(let ((,saved (mapcar (lambda (p) (cons (car p) (sb-posix:getenv (car p)))) (list ,@(mapcar (lambda (p) `(cons ,(first p) ,(second p))) pairs)))))
       (unwind-protect
            (progn ,@(mapcar (lambda (p) `(if (string= ,(second p) "") (sb-posix:unsetenv ,(first p)) (sb-posix:setenv ,(first p) ,(second p) 1))) pairs)
                   ,@body)
         (dolist (s ,saved)
           (if (cdr s) (sb-posix:setenv (car s) (cdr s) 1) (sb-posix:unsetenv (car s))))))))

(deftest readers-session-paths
  (with-env (("KIMI_SHARE_DIR" "/k") ("KODE_CONFIG_DIR" "/k") ("COPILOT_HOME" "/c") ("CLINE_DATA_DIR" "/d")
             ("SNOWFLAKE_HOME" "/s") ("GOOSE_PATH_ROOT" "/g") ("HOME" "/h") ("XDG_DATA_HOME" ""))
    (check (kimi-wire-path "/w/app" "s1") "/k/sessions/88b917843fbc859c6c410d34ba465703/s1/wire.jsonl")
    (check (kode-session-path "/w/my app" "s1") "/k/projects/-w-my-app/s1.jsonl")
    (check (kode-session-path "" "s1") "")
    (check (copilot-events-path "s1") "/c/session-state/s1/events.jsonl")
    (check (cline-messages-path "" "s1") "/d/sessions/s1/s1.messages.json")
    (check (cortex-history-path "" "s1") "/s/cortex/conversations/s1.history.jsonl")
    (check (auggie-session-path "" "c1") "/h/.augment/sessions/c1.json")
    (check (goose-db-path "" "") "/g/data/sessions/sessions.db")
    (check (amazon-q-db-path "" "") "/h/.local/share/amazon-q/data.sqlite3"))
  (let ((root (sb-posix:mkdtemp (format nil "~A/om-paths-XXXXXX" (or (sb-posix:getenv "TMPDIR") "/tmp")))))
    (unwind-protect
         (with-env (("KIRO_HOME" root))
           (check (kiro-session-path "" "abc") "")
           (ensure-directories-exist (cat root "/sessions/cli/"))
           (with-open-file (o (cat root "/sessions/cli/abc.jsonl") :direction :output) (write-line "{}" o))
           (check (kiro-session-path "" "abc") (cat root "/sessions/cli/abc.jsonl"))
           (ensure-directories-exist (cat root "/sessions/b2/sess_abc/"))
           (with-open-file (o (cat root "/sessions/b2/sess_abc/messages.jsonl") :direction :output) (write-line "{}" o))
           (check (kiro-session-path "" "abc") (cat root "/sessions/b2/sess_abc/messages.jsonl"))
           ;; A repository root stops the search for .crush; without one, <cwd>/.crush.
           (ensure-directories-exist (cat root "/repo/.git/"))
           (ensure-directories-exist (cat root "/repo/a/b/"))
           (check (crush-db-path (cat root "/repo/a/b") "") (cat root "/repo/a/b/.crush/crush.db"))
           (ensure-directories-exist (cat root "/repo/a/.crush/"))
           (check (crush-db-path (cat root "/repo/a/b") "") (cat root "/repo/a/.crush/crush.db")))
      (sb-ext:delete-directory (cat root "/") :recursive t))))
