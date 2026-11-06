;;;; Level 0's file tools across harnesses (extract/filetools_test.go, transcript/filestate_test.go)
;;;; and the JavaScript project slug (transcript/projectdir_test.go). Loaded after tests/readers.lisp,
;;;; whose real-entries and read-sample it uses.

(in-package "OM")

(defun file-records (entries cwd)
  (multiple-value-bind (batches) (extract (coerce entries 'simple-vector) 0 (length entries) (fold-ledger (vector)) :cwd cwd)
    (loop for b in batches
          append (loop for o in (batch-observations b) when (string= (obs-kind o) "file") collect (obs-content o)))))

(defun all-records (entries cwd)
  (multiple-value-bind (batches) (extract (coerce entries 'simple-vector) 0 (length entries) (fold-ledger (vector)) :cwd cwd)
    (loop for b in batches append (mapcar #'obs-content (batch-observations b)))))

(deftest filetools-keys
  (check (mapcar #'tool-key '("Read" "MultiEdit" "NotebookEdit" "read_file" "ReadFile" "apply-patch"))
         '("read" "multiedit" "notebookedit" "readfile" "readfile" "applypatch"))
  (check (file-tool-state (make-tcall :name "write" :args (jobject "command" "strReplace" "path" "a"))) "edited")
  (check (file-tool-state (make-tcall :name "write" :args (jobject "command" "create" "path" "a"))) "written")
  (check (file-tool-state (make-tcall :name "Bash" :args (jobject "command" "ls"))) nil))

(deftest filetools-patch-events
  (check (patch-path-events (format nil "*** Begin Patch~%*** Add File: package.json~%+{}~%*** Update File: src/main.jsx~%@@~%-a~%+b~%*** Delete File: src/legacy.jsx~%*** Update File: src/a.css~%*** Move to: src/b.css~%@@~%-x~%+y~%*** End Patch~%"))
         '(("package.json" "written" "") ("src/main.jsx" "edited" "") ("src/legacy.jsx" "deleted" "")
           ("src/a.css" "moved to" "src/b.css") ("src/b.css" "moved from" "src/a.css")))
  (check (patch-path-events (format nil "*** Begin Patch~%*** Add File: notes.md~%+*** Delete File: keep.md~%*** End Patch")) '(("notes.md" "written" "")))
  (check (patch-path-events "*** Add File: outside.md") '()))

;; Codex's apply_patch as the rollout holds it: each added path written.
(deftest filetools-codex-rollout
  (check (file-records (real-entries "codex" "codex-rollout.jsonl") "/workspace")
         '("package.json written" "index.html written" "src/main.jsx written" "src/styles.css written")))

;; A patch that did not apply touches nothing; its attempt names its path.
(deftest filetools-codex-failed-patch
  (let ((es (read-sample "codex"
                         "{\"type\":\"session_meta\",\"payload\":{\"id\":\"s\",\"cwd\":\"/w\"}}"
                         "{\"type\":\"response_item\",\"timestamp\":\"2026-11-04T10:00:01.000Z\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"fix main\"}]}}"
                         "{\"type\":\"response_item\",\"timestamp\":\"2026-11-04T10:00:02.000Z\",\"payload\":{\"type\":\"custom_tool_call\",\"call_id\":\"c1\",\"name\":\"apply_patch\",\"input\":\"*** Begin Patch\\n*** Update File: src/main.jsx\\n@@\\n-gone\\n+new\\n*** End Patch\\n\"}}"
                         "{\"type\":\"response_item\",\"timestamp\":\"2026-11-04T10:00:03.000Z\",\"payload\":{\"type\":\"custom_tool_call_output\",\"call_id\":\"c1\",\"output\":\"apply_patch verification failed: Failed to find expected lines in src/main.jsx\"}}")))
    (check (file-records es "/w") '())
    (check (find-if (lambda (c) (has-prefix c "apply_patch ")) (all-records es "/w"))
           "apply_patch \"src/main.jsx\" failed: apply_patch verification failed: Failed to find expected lines in src/main.jsx")))

;; Claude Code's Read, Edit, Write, MultiEdit and NotebookEdit through its reader; a failed Edit
;; leaves no state.
(deftest filetools-claude-code
  (let ((es (read-sample "claude-code"
                         "{\"parentUuid\":null,\"isSidechain\":false,\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"Tidy the handlers\"},\"uuid\":\"u1\",\"timestamp\":\"2026-09-24T03:01:00.000Z\",\"cwd\":\"/w/app\",\"sessionId\":\"s1\"}"
                         "{\"parentUuid\":\"u1\",\"isSidechain\":false,\"type\":\"assistant\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"Read\",\"input\":{\"file_path\":\"/w/app/a.go\"}}],\"stop_reason\":\"tool_use\"},\"uuid\":\"a1\",\"timestamp\":\"2026-09-24T03:01:01.000Z\",\"cwd\":\"/w/app\",\"sessionId\":\"s1\"}"
                         "{\"parentUuid\":\"a1\",\"isSidechain\":false,\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"tool_use_id\":\"toolu_1\",\"type\":\"tool_result\",\"content\":\"     1\\tpackage a\\n\"}]},\"uuid\":\"r1\",\"timestamp\":\"2026-09-24T03:01:02.000Z\",\"cwd\":\"/w/app\",\"sessionId\":\"s1\"}"
                         "{\"parentUuid\":\"r1\",\"isSidechain\":false,\"type\":\"assistant\",\"message\":{\"id\":\"msg_2\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"tool_use\",\"id\":\"toolu_2\",\"name\":\"Edit\",\"input\":{\"file_path\":\"/w/app/b.go\",\"old_string\":\"a\",\"new_string\":\"b\",\"replace_all\":false}}],\"stop_reason\":\"tool_use\"},\"uuid\":\"a2\",\"timestamp\":\"2026-09-24T03:01:03.000Z\",\"cwd\":\"/w/app\",\"sessionId\":\"s1\"}"
                         "{\"parentUuid\":\"a2\",\"isSidechain\":false,\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"tool_use_id\":\"toolu_2\",\"type\":\"tool_result\",\"content\":\"The file /w/app/b.go has been updated.\"}]},\"uuid\":\"r2\",\"timestamp\":\"2026-09-24T03:01:04.000Z\",\"cwd\":\"/w/app\",\"sessionId\":\"s1\"}"
                         "{\"parentUuid\":\"r2\",\"isSidechain\":false,\"type\":\"assistant\",\"message\":{\"id\":\"msg_3\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"tool_use\",\"id\":\"toolu_3\",\"name\":\"Write\",\"input\":{\"file_path\":\"/w/app/c.go\",\"content\":\"package c\\n\"}},{\"type\":\"tool_use\",\"id\":\"toolu_4\",\"name\":\"MultiEdit\",\"input\":{\"file_path\":\"/w/app/d.go\",\"edits\":[{\"old_string\":\"a\",\"new_string\":\"b\"}]}},{\"type\":\"tool_use\",\"id\":\"toolu_5\",\"name\":\"NotebookEdit\",\"input\":{\"notebook_path\":\"/w/app/e.ipynb\",\"new_source\":\"x = 1\",\"cell_id\":\"c1\"}},{\"type\":\"tool_use\",\"id\":\"toolu_6\",\"name\":\"Edit\",\"input\":{\"file_path\":\"/w/app/f.go\",\"old_string\":\"zz\",\"new_string\":\"b\"}}],\"stop_reason\":\"tool_use\"},\"uuid\":\"a3\",\"timestamp\":\"2026-09-24T03:01:05.000Z\",\"cwd\":\"/w/app\",\"sessionId\":\"s1\"}"
                         "{\"parentUuid\":\"a3\",\"isSidechain\":false,\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"tool_use_id\":\"toolu_3\",\"type\":\"tool_result\",\"content\":\"File created successfully at: /w/app/c.go\"},{\"tool_use_id\":\"toolu_4\",\"type\":\"tool_result\",\"content\":\"Applied 1 edit to /w/app/d.go\"},{\"tool_use_id\":\"toolu_5\",\"type\":\"tool_result\",\"content\":\"Updated cell c1\"},{\"tool_use_id\":\"toolu_6\",\"type\":\"tool_result\",\"content\":\"<tool_use_error>String to replace not found in file.</tool_use_error>\",\"is_error\":true}]},\"uuid\":\"r3\",\"timestamp\":\"2026-09-24T03:01:06.000Z\",\"cwd\":\"/w/app\",\"sessionId\":\"s1\"}")))
    (check (file-records es "/w/app") '("a.go read" "b.go edited" "c.go written" "d.go edited" "e.ipynb edited"))))

;; apply_patch through a shell: Codex's argv form, a heredoc script, bash -lc; a cat of a patch is not one.
(deftest filetools-patch-through-shell
  (let* ((out (format nil "Process exited with code 0~%Output:~%Success. Updated the following files:~%M app.py~%"))
         (branch (branch-of (jobject "id" "u1" "timestamp" "2026-05-01T10:00:00Z" "role" "user" "text" "Patch it.")
                            (jobject "id" "a1" "timestamp" "2026-05-01T10:01:00Z" "role" "assistant" "text" ""
                                     "tools" (vector (jobject "id" "c1" "name" "shell" "arguments"
                                                              (jobject "command" (vector "apply_patch" (format nil "*** Begin Patch~%*** Update File: app.py~%@@~%-a~%+b~%*** End Patch~%"))))
                                                     (jobject "id" "c2" "name" "exec_command" "arguments"
                                                              (jobject "cmd" (format nil "apply_patch <<'EOF'~%*** Begin Patch~%*** Add File: b.py~%+x~%*** End Patch~%EOF~%")))
                                                     (jobject "id" "c3" "name" "shell" "arguments"
                                                              (jobject "command" (vector "bash" "-lc" (format nil "apply_patch <<'EOF'~%*** Begin Patch~%*** Delete File: c.py~%*** End Patch~%EOF"))))
                                                     (jobject "id" "c4" "name" "exec_command" "arguments"
                                                              (jobject "cmd" (format nil "cat <<'EOF'~%*** Begin Patch~%*** Delete File: d.py~%*** End Patch~%EOF")))))
                            (jobject "id" "r1" "timestamp" "2026-05-01T10:02:00Z" "role" "tool" "toolCallId" "c1" "toolName" "shell" "text" out)
                            (jobject "id" "r2" "timestamp" "2026-05-01T10:02:00Z" "role" "tool" "toolCallId" "c2" "toolName" "exec_command" "text" out)
                            (jobject "id" "r3" "timestamp" "2026-05-01T10:02:00Z" "role" "tool" "toolCallId" "c3" "toolName" "shell" "text" out)
                            (jobject "id" "r4" "timestamp" "2026-05-01T10:02:00Z" "role" "tool" "toolCallId" "c4" "toolName" "exec_command" "text" out))))
    (check (file-records branch "/work/app") '("app.py edited" "b.py written" "c.py deleted"))))

(deftest filetools-js-slug
  (check (js-slug "/home/u/my_proj v2/café") "-home-u-my-proj-v2-caf-")
  (check (js-slug (format nil "/w/日本語/~Cx" (code-char #x1F600))) "-w-------x")
  (with-env (("KODE_CONFIG_DIR" "/k"))
    (check (kode-session-path "/w/my_app é" "s1") "/k/projects/-w-my-app--/s1.jsonl")))
