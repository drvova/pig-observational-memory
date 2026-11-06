;;;; 1b's tests: the curated ranked index (rank, render, pin, the usage fold), Settle (promotion
;;;; and correction flags), the corrections a branch carries and the project root. They write
;;;; only under W4's temporary directory (tests/tool-layer.lisp).

(in-package "OM.LAYER")

(defun mem* (name type &key (description "d") (updated "2026-11-01T10:00:00Z") pinned)
  (make-memory :name name :type type :description description :pinned pinned
               :created (fixed-time "2026-11-01T09:00:00Z") :updated (fixed-time updated)))

(defun usage* (&rest specs)
  "specs: (name :views n :helped n :harmed n :corrected n)."
  (let ((h (make-hash-table :test 'equal)))
    (dolist (s specs h)
      (destructuring-bind (name &key (views 0) (helped 0) (harmed 0) (corrected 0)) s
        (setf (gethash name h) (make-evidence :views views :helped helped :harmed harmed :corrected corrected))))))

(om::deftest curated-rank-orders-by-flag-operator-score-type-time-name
  (let ((ms (list (mem* "b-lesson" "lesson") (mem* "a-lesson" "lesson")
                  (mem* "ref" "reference" :updated "2026-11-02T10:00:00Z")
                  (mem* "proj" "project") (mem* "fb" "feedback") (mem* "usr" "user")
                  (mem* "flagged" "user") (mem* "pinned" "user" :pinned t)
                  (mem* "viewed" "lesson"))))
    (om::check (mapcar #'mem-name
                       (curated-rank ms (usage* '("flagged" :corrected 1) '("pinned" :corrected 2)
                                                '("viewed" :views 4 :harmed 1) '("fb" :helped 1))))
               '("fb" "pinned" "usr" "viewed" "proj" "ref" "a-lesson" "b-lesson" "flagged"))))

(om::deftest curated-render-index-budgets
  (let* ((ms (loop for i from 1 to 6
                   collect (mem* (format nil "m~D" i) "user" :description "A description of some length here")))
         (all (render-index ms 0)))
    (om::check (render-index '() 10) "")
    (om::check (om::has-prefix all (concatenate 'string +index-header+ (format nil "~%~%- m1 [user]: "))) t)
    (multiple-value-bind (text shown) (render-index ms 30)
      (om::check shown '("m1" "m2" "m3" "m4" "m5" "m6"))
      (om::check (om::has-suffix text (format nil "- m1 [user]: A description of some length here~%also: m2, m3, m4, m5, m6")) t))
    (multiple-value-bind (text shown) (render-index ms 12)
      (om::check shown '("m1" "m2" "m3" "m4" "m5" "m6"))
      (om::check text (format nil "~A~%~%also: m1, m2, m3, m4, m5, m6" +index-header+)))
    (multiple-value-bind (text shown) (render-index ms 4)
      (om::check shown '("m1" "m2"))
      (om::check (om::has-suffix text (format nil "~%~%also: m1, m2~%(4 more memories; find them with the memory tool's om.Saved(om.Search(`<words>`)).)")) t))
    (multiple-value-bind (text shown) (render-index ms 2)
      (om::check shown '())
      (om::check text (format nil "~A~%(6 more memories; find them with the memory tool's om.Saved(om.Search(`<words>`)).)" +index-header+)))))

(om::deftest curated-pin-holds-until-reset
  (let ((pin (make-pin)) (calls 0))
    (flet ((render () (incf calls) (values (format nil "index ~D" calls) (list "a"))))
      (om::check (multiple-value-list (pin-get pin #'render)) '("index 1" ("a") t))
      (om::check (multiple-value-list (pin-get pin #'render)) '("index 1" ("a") nil))
      (om::check (pin-shown-names pin) '("a"))
      (pin-reset pin)
      (om::check (pin-shown-names pin) '())
      (om::check (multiple-value-list (pin-get pin #'render)) '("index 2" ("a") t)))))

(om::deftest curated-usage-fold-and-index-for
  (let ((dir (test-dir "index-store")))
    (om::check (index-for dir "" 100) +empty-index+)
    (at-time ("2026-11-01T10:00:00Z")
      (save-memory dir (make-memory :name "tea" :type "user" :description "Drinks tea") "s1")
      (save-memory dir (make-memory :name "build" :type "project" :description "Builds with make") "s1")
      (save-memory dir (make-memory :name "draft" :type "user" :description "Staged" :staged t) "s1"))
    (at-time ("2026-11-01T11:00:00Z")
      (append-usage dir "build" "view") (append-usage dir "build" "view")
      (append-usage dir "build" "corrected" :record "r1")
      (append-usage dir "tea" "forget"))
    (append-line (usage-path dir) "{\"name\":\"tea\",\"event\":\"view\",\"time\":1}")
    (let ((u (curated-usage dir)))
      (om::check (ev-views (gethash "build" u)) 2)
      (om::check (ev-corrected (gethash "build" u)) 1)
      (om::check (ev-views (gethash "tea" u)) 0)
      (om::check (format-rfc3339 (ev-last (gethash "tea" u))) "2026-11-01T11:00:00Z"))
    (multiple-value-bind (text shown) (index-for dir "" 2000)
      (om::check shown '("tea" "build"))
      (om::check (om::has-suffix text (format nil "- tea [user]: Drinks tea~%- build [project]: Builds with make")) t))))

(om::deftest curated-settle-promotes-and-flags
  (let ((dir (test-dir "settle-store")))
    (at-time ("2026-11-01T10:00:00Z")
      (save-memory dir (make-memory :name "quote-me" :type "user" :description "Q" :staged t) "s1")
      (save-memory dir (make-memory :name "twice" :type "user" :description "T" :staged t) "s1" (source-digest "a file s1 read"))
      (save-memory dir (make-memory :name "twice" :type "user" :description "t" :staged t) "s2" (source-digest "a file s2 read"))
      ;; the same source content in two sessions is no independent evidence
      (save-memory dir (make-memory :name "same-readme" :type "user" :description "R" :staged t) "s1" (source-digest "README"))
      (save-memory dir (make-memory :name "same-readme" :type "user" :description "R" :staged t) "s2" (source-digest " README "))
      (save-memory dir (make-memory :name "waiting" :type "user" :description "W" :staged t) "s1")
      (save-memory dir (make-memory :name "green-tea" :type "user" :description "G") "s1"))
    (at-time ("2026-11-01T11:00:00Z")
      (append-usage dir "quote-me" "helped" :quote "the user said so" :user t)
      (let ((later (+ (car (fixed-time "2026-11-01T12:00:00Z")) 1)))
        (multiple-value-bind (promoted flagged)
            (curated-settle dir "" (list (make-correction :id "r1" :subject "Green tea!" :nanos later)
                                         (make-correction :id "r2" :subject "coffee" :nanos later)
                                         (make-correction :id "r3" :subject "tea" :nanos 0)))
          (om::check promoted '(("quote-me" . "a user's words quoted in feedback")
                                ("twice" . "the same save staged in another session")))
          (om::check flagged '("green-tea")))
        ;; A correction already recorded flags nothing again.
        (om::check (nth-value 1 (curated-settle dir "" (list (make-correction :id "r1" :subject "tea" :nanos later)))) '())))
    (om::check (mem-staged (get-memory dir "quote-me")) nil)
    (om::check (mem-staged (get-memory dir "waiting")) t)
    (om::check (mem-staged (get-memory dir "same-readme")) t)
    (om::check (settled-notices '(("x" . "e")) '("a" "b"))
               '("Observational memory: staged memory x is now live (e); it enters the memory index at its next render"
                 "Observational memory: a correction matches the memories a, b; they rank last and are flagged in saved until edited"))))

(om::deftest curated-subject-matches
  (om::check (subject-matches-p "Green tea" "green-tea-time") t)
  (om::check (subject-matches-p "the green tea we drink" "green-tea") t)
  (om::check (subject-matches-p "tea time" "green-tea") nil)
  (om::check (subject-matches-p "  " "tea") nil)
  (om::check (subject-matches-p "TÉ" "té") t))

(om::deftest curated-branch-corrections
  (let* ((lines (list "{\"type\":\"message\",\"id\":\"u1\",\"timestamp\":\"2026-05-01T10:00:00.250123Z\",\"message\":{\"role\":\"user\",\"content\":\"x\"}}"
                      "{\"type\":\"message\",\"id\":\"u2\",\"timestamp\":\"2026-05-01T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"y\",\"timestamp\":1777629601000}}"
                      "{\"type\":\"custom\",\"id\":\"c1\",\"customType\":\"om.observations.recorded\",\"data\":{\"observations\":[{\"id\":\"aaaaaaaaaaa1\",\"tokenCount\":3,\"content\":\"No, Porto.\",\"timestamp\":\"2026-05-01 10:00\",\"relevance\":\"high\",\"sourceEntryIds\":[\"u1\"],\"kind\":\"correction\",\"subject\":\"venue\",\"origin\":\"code\"},{\"id\":\"aaaaaaaaaaa2\",\"tokenCount\":3,\"content\":\"Drop it.\",\"timestamp\":\"2026-05-01 10:00\",\"relevance\":\"high\",\"sourceEntryIds\":[\"u2\"],\"kind\":\"correction\",\"subject\":\"plan\",\"origin\":\"code\"},{\"id\":\"aaaaaaaaaaa3\",\"tokenCount\":3,\"content\":\"A fact.\",\"timestamp\":\"2026-05-01 10:00\",\"relevance\":\"high\",\"sourceEntryIds\":[\"u2\"],\"kind\":\"fact\",\"subject\":\"x\",\"origin\":\"code\"}],\"coversUpToId\":\"u2\"}}"))
         (entries (coerce (mapcar (lambda (l) (om::entry-from-map (om::json-parse l))) lines) 'simple-vector))
         (cs (branch-corrections entries)))
    (om::check (mapcar #'corr-id cs) '("aaaaaaaaaaa1" "aaaaaaaaaaa2"))
    (om::check (mapcar #'corr-subject cs) '("venue" "plan"))
    (om::check (mapcar #'corr-nanos cs) (list 1777629600250123000 1777629601000000000))))

(om::deftest curated-project-root
  (let* ((root (test-dir "proj")) (sub (join-path root "a" "b")))
    (ensure-dir sub)
    (om::check (project-root "") "")
    (om::check (project-root (concatenate 'string sub "/./")) (string-right-trim "/" (sb-ext:native-namestring (probe-file sub))))
    (ensure-dir (join-path root ".git"))
    (om::check (project-root sub) (string-right-trim "/" (sb-ext:native-namestring (probe-file root))))
    (om::check (project-root "/no/such/dir/../x") "/no/such/x")
    (om::check (clean-path "a//b/../../..") "..")
    (om::check (clean-path "/../a/") "/a")))

(om::deftest curated-index-cleanup
  (sb-ext:run-program "/bin/rm" (list "-rf" *test-root*))
  (om::check (probe-file *test-root*) nil))
