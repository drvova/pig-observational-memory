;;;; The neutral store and branch merge (memory/neutral): neutral_test.go's merge, and the store's
;;;; file format, load rules and host cache.

(in-package "OM")

(defun raw-message (id text)
  (entry-from-map (jobject "type" "message" "id" id "message" (jobject "role" "user" "content" text))))

(deftest store-branch-merge
  (let* ((tr (list (raw-message "u1" "a") (raw-message "u2" "b")))
         (stored (list (make-stored "u1" (jobject "type" "custom" "id" "x1"))
                       (make-stored "x1" (jobject "type" "custom" "id" "x2"))
                       (make-stored "u1" (jobject "type" "custom" "id" "x3"))
                       (make-stored "gone" (jobject "type" "custom" "id" "x4"))
                       (make-stored "" (jobject "type" "custom" "id" "x0"))))
         (b (neutral-branch tr stored)))
    (check (join (mapcar #'entry-id b) " ") "x0 u1 x1 x2 x3 u2 x4")
    (check (entry-parent-id (third b)) "u1")
    (check (entry-parent-id (first b)) "")
    (check (jget (entry-raw (third b)) "parentId") "u1")
    ;; The transcript's entries are copies: their own parent ids are untouched.
    (check (entry-parent-id (first tr)) ""))
  ;; A stored entry naming an id a reader folded into an entry (omAliases) lands after it.
  (let* ((grouped (make-message "a1" "" (jobject "role" "assistant" "content" (vector (text-block "x"))) '("a2" "a3")))
         (b (neutral-branch (list grouped (raw-message "u9" "c")) (list (make-stored "a3" (jobject "type" "custom" "id" "x1"))))))
    (check (mapcar #'entry-id b) '("a1" "x1" "u9"))))

(deftest store-file-format
  (check (safe-name "../a/b\\c") "._a_b_c")
  (check (safe-name ".hidden") "hidden")
  (check (store-for "/root/" "codex" "s/1") "/root/sessions/codex/s_1.jsonl")
  (check (stored-line "x1" (jobject "type" "custom" "id" "y" "data" (jobject "t" "<a> & b" "n" 1d0 "z" :null)))
         "{\"after\":\"x1\",\"entry\":{\"data\":{\"n\":1,\"t\":\"\\u003ca\\u003e \\u0026 b\",\"z\":null},\"id\":\"y\",\"type\":\"custom\"}}")
  (let* ((dir (sb-posix:mkdtemp (format nil "~A/om-store-XXXXXX" (or (sb-posix:getenv "TMPDIR") "/tmp"))))
         (path (cat dir "/sessions/test/s1.jsonl")))
    (check (store-load path) nil "a missing store is empty")
    (store-append path "" (jobject "type" "custom" "id" "x1"))
    (store-append path "x1" (jobject "type" "custom" "id" "x2"))
    (with-open-file (out path :direction :output :if-exists :append)
      (format out "{\"after\":\"x1\"}~%{\"after\":5,\"entry\":{}}~%{\"After\":\"x2\",\"Entry\":{\"id\":\"x3\"}}~%{\"after\":\"x1\",\"ent"))
    (let ((st (store-load path)))
      (check (mapcar #'stored-after st) '("" "x1" "x2"))
      (check (mapcar (lambda (s) (jget (stored-entry s) "id")) st) '("x1" "x2" "x3")))
    (check (and (probe-file (cat path ".lock")) t) t "the lock file is beside the store")
    (check (logand (sb-posix:stat-mode (sb-posix:stat path)) #o777) #o600)
    (sb-ext:delete-directory (cat dir "/") :recursive t)))

(deftest store-host-read-append
  (let* ((dir (sb-posix:mkdtemp (format nil "~A/om-host-XXXXXX" (or (sb-posix:getenv "TMPDIR") "/tmp"))))
         (tpath (cat dir "/t.jsonl"))
         (entries (list (raw-message "u1" "a") (raw-message "u2" "b")))
         (h (make-neutral-host :session "s1" :dir dir :root dir
                               :transcript (make-neutral-transcript :entries (lambda () entries))
                               :store (store-for dir "test" "s1")
                               :now (lambda () 1793787302099))))
    (declare (ignore tpath))
    (check (mapcar #'entry-id (neutral-read h)) '("u1" "u2"))
    (let ((id (neutral-append-custom h "om.extracted" (jobject "through" "u2"))))
      (check (length id) 10)
      (check (has-prefix id "om") t)
      (let ((b (neutral-read h)))
        (check (mapcar #'entry-id b) (list "u1" "u2" id))
        (check (entry-parent-id (third b)) "u2")
        (check (entry-timestamp (third b)) "2026-11-04T10:15:02.099Z")
        (check (entry-custom-type (third b)) "om.extracted"))
      ;; The transcript grows past the entry: the stored entry stays after u2.
      (setf entries (append entries (list (raw-message "u3" "c"))))
      (check (mapcar #'entry-id (neutral-read h)) (list "u1" "u2" id "u3")))
    (sb-ext:delete-directory (cat dir "/") :recursive t)))
