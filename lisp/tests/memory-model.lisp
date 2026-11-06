;;;; W1, the memory model: subjects and aliases, reflections, level-1 replacements, links, the
;;;; fold, the codecs and the lines format. Cases copied from ledger/subjects_test.go,
;;;; aliases_test.go, replace_test.go, links_test.go, types_test.go and tables/jsonl_test.go.

(in-package "OM")

(defun canonical-alist (table)
  "A canonical-subjects table as a sorted list of (subject canonical extra-scope)."
  (sort (loop for s being the hash-keys of table using (hash-value c)
              collect (list s (canonical-subject c) (canonical-extra-scope c)))
        #'string< :key #'first))

(defun alias (from to) (make-subject-alias :from from :to to))

(defparameter *real-session-subjects*
  '("running" "running training" "running gear" "running shoes" "half marathon training" "fitness routine" "health metrics"))

(deftest canonical-subjects-real-session
  (check (canonical-alist (canonical-subjects *real-session-subjects*))
         '(("fitness routine" "fitness routine" "") ("half marathon training" "half marathon training" "")
           ("health metrics" "health metrics" "") ("running" "running" "") ("running gear" "running" "gear")
           ("running shoes" "running" "shoes") ("running training" "running" "training")))
  (check (canonical-alist (canonical-subjects *real-session-subjects* (list (alias "half marathon training" "running"))))
         '(("fitness routine" "fitness routine" "") ("half marathon training" "running" "half marathon training")
           ("health metrics" "health metrics" "") ("running" "running" "") ("running gear" "running" "gear")
           ("running shoes" "running" "shoes") ("running training" "running" "training")))
  ;; Refused aliases change nothing.
  (dolist (aliases (list (list (alias "half marathon training" "cycling")) (list (alias "running" "running"))))
    (check (canonical-alist (canonical-subjects *real-session-subjects* aliases))
           (canonical-alist (canonical-subjects *real-session-subjects*)))))

(deftest subject-keys
  (dolist (c '(("Running-Shoes" "running shoes") ("running   shoe" "running shoe") ("health metrics!" "health metrics")
               ("taxes & buzzes" "taxes buzzes") ("--" "") ("churches/branches" "churches branches")
               ("user's dog" "user dog") ("User’s dog" "user dog") ("'quoted'" "quoted") ("o'brien" "o'brien")
               ("c++" "c++") ("c#" "c#") ("r&d budget" "r&d budget") ("c" "c")))
    (check (subject-key (first c)) (second c) (first c)))
  (check (canonical-alist (canonical-subjects '("running shoes" "running shoe" "running-shoes" "shoelaces" "trail running shoes!")))
         '(("running shoe" "running shoe" "") ("running shoes" "running shoe" "") ("running-shoes" "running shoe" "")
           ("shoelaces" "shoelaces" "") ("trail running shoes!" "running shoe" "trail"))))

(deftest canonical-subjects-plurals
  (dolist (c '((("user" "user's dog") (("user" "user" "") ("user's dog" "user" "dog")))
               (("news" "new york trip") (("new york trip" "new york trip" "") ("news" "news" "")))
               (("c" "c++" "c#") (("c" "c" "") ("c#" "c#" "") ("c++" "c++" "")))
               (("shoes" "boxes") (("boxes" "boxes" "") ("shoes" "shoes" "")))
               (("status" "statuses") (("status" "status" "") ("statuses" "status" "")))
               (("analysis" "analyses") (("analyses" "analyses" "") ("analysis" "analyses" "")))
               (("story" "bedtime stories") (("bedtime stories" "story" "bedtime") ("story" "story" "")))
               (("car" "cars") (("car" "car" "") ("cars" "cars" "")))))
    (check (canonical-alist (canonical-subjects (first c))) (second c) (first c))))

(deftest canonical-subjects-ties
  (let ((got (canonical-subjects '("trail running shoes" "trail" "running" "running shoes" "trail running"))))
    (check (canonical-extra-scope (gethash "trail running shoes" got)) "trail shoes")
    (check (canonical-subject (gethash "trail running" got)) "running"))
  (check (canonical-alist (canonical-subjects '("running gear" "gear running" "marathon training" "training marathon" "new york" "new new york")))
         '(("gear running" "gear running" "") ("marathon training" "marathon training" "") ("new new york" "new new york" "")
           ("new york" "new new york" "") ("running gear" "gear running" "") ("training marathon" "marathon training" ""))))

(deftest subject-paths
  (check (canonical-alist (canonical-subjects '("running/half marathon" "running/half marathon/pace" "trail running/shoes" "running" "baking/sourdough")))
         '(("baking/sourdough" "baking" "sourdough") ("running" "running" "") ("running/half marathon" "running" "half marathon")
           ("running/half marathon/pace" "running" "half marathon pace") ("trail running/shoes" "running" "trail shoes")))
  (check (normalize-subject-path " Running / half  marathon/ ") "running/half marathon")
  (check (normalize-subject-path "//baking//") "baking")
  (dolist (s '("running/a/b/c" "om/folded" "raw/subject" "subject/scope" "canonical/subjects"))
    (check (nth-value 1 (subject-path s)) nil s)
    (check (nth-value 1 (recordable-subject s)) t s))
  (dolist (s '("search/indexing" "coverage/unit tests" "history/git rebase" "memory/ram" "notes/meeting"))
    (check (multiple-value-list (recordable-subject s)) (list s nil) s))
  (let ((got (canonical-subjects '("running" "running/a/b/c" "om/folded"))))
    (check (list (canonical-subject (gethash "running/a/b/c" got)) (canonical-extra-scope (gethash "running/a/b/c" got))) '("running" "a b c"))
    (check (canonical-subject (gethash "om/folded" got)) "om/folded")))

(deftest subject-alias-checks
  (multiple-value-bind (in-force refused)
      (check-subject-aliases *real-session-subjects*
                             (list (alias "half marathon training" "running") (alias "running" "half marathon training")
                                   (alias "running" "running") (alias "cycling" "running") (alias "raw_subject" "running")
                                   (alias "running" "running shoes") (alias "a/b" "running")
                                   (alias "half marathon training" "half marathon training")))
    (check (mapcar (lambda (a) (list (subject-alias-from a) (subject-alias-to a))) in-force) '())
    (check (mapcar #'cdr refused)
           (list +alias-refused-cycle+ +alias-refused-self+ +alias-refused-absent+ +alias-refused-kernel+
                 +alias-refused-contained+ +alias-refused-path+)))
  (multiple-value-bind (accepted refused)
      (accept-new-subject-aliases *real-session-subjects* (list (alias "half marathon training" "running"))
                                  (list (alias "half marathon training" "running") (alias "fitness routine" "health metrics")
                                        (alias "fitness routine" "running")))
    (check (mapcar (lambda (a) (subject-alias-to a)) accepted) '("running"))
    (check (mapcar #'cdr refused) (list +alias-refused-repeat+))))

(deftest subject-in-text-names
  (check (subject-in-text "My running shoes wore out." '("running" "running shoes" "baking")) "running")
  (check (subject-in-text "I baked bread." '("running" "baking")) "")
  (check (subject-in-text "The half marathon training starts." '("running" "half marathon training")
                          (list (alias "half marathon training" "running")))
         "running")
  (check (subject-table-name "Café Menu!") "notes.café_menu")
  (check (subject-table-name "--") "")
  (check (subject-table-name "user's budget") "notes.user_s_budget"))

(defun ids (&rest ns) (mapcar (lambda (n) (format nil "~(~12,'0x~)" n)) ns))

(defun rec (id content &rest keys)
  (let ((o (make-obs :id id :content content :timestamp "2026-05-01T00:00:00Z" :relevance "medium"
                     :source-ids (list (cat "e-" id)) :token-count 3)))
    (loop for (k v) on keys by #'cddr
          do (ecase k
               (:supersedes (setf (obs-supersedes o) v))
               (:replaces (setf (obs-replaces o) v))
               (:sources (setf (obs-source-ids o) v))
               (:subject (setf (obs-subject o) v))))
    o))

(defun recorded (id covers observations &optional consolidated)
  (let ((data (jobject "observations" (coerce (mapcar #'obs-to-value observations) 'simple-vector) "coversUpToId" covers)))
    (when consolidated (setf (gethash "consolidated" data) :true))
    (make-entry :type "custom" :id id :custom-type "om.observations.recorded" :data data)))

(defun dropped (id covers ids &optional consolidated)
  (let ((data (jobject "observationIds" (coerce ids 'simple-vector) "coversUpToId" covers)))
    (when consolidated (setf (gethash "consolidated" data) :true))
    (make-entry :type "custom" :id id :custom-type "om.observations.dropped" :data data)))

(defun msg (id) (make-entry :type "message" :id id))

(deftest level-1-replacements
  (destructuring-bind (a b c label merge fix) (ids 1 2 3 4 5 6)
    (let* ((branch (vector (msg "m1")
                           (recorded "r1" "m1" (list (rec a "The cache costs 40 USD.") (rec b "Bo owns the cache.")
                                                     (rec c "Ana owns the queue.")))
                           (msg "m2")
                           (recorded "r2" "m2" (list (rec label "The cache costs 40 USD." :replaces (list a))
                                                     (rec merge "Bo owns the cache; Ana owns the queue." :replaces (list b c)
                                                          :sources (list (cat "e-" b) (cat "e-" c))))
                                     t)
                           (dropped "d2" "m2" (list a b c) t)
                           (msg "m3")
                           (recorded "r3" "m3" (list (rec fix "The cache costs 50 USD." :supersedes (list a))))))
           (folded (fold-ledger branch)))
      (check (gethash a (folded-replaced-by folded)) label)
      (check (gethash b (folded-replaced-by folded)) merge)
      (check (standing a (folded-replaced-by folded)) label)
      ;; The correction of a record level 1 relabelled supersedes the relabelled record.
      (check (obs-supersedes (gethash fix (folded-by-id folded))) (list label))
      (check (mapcar #'obs-id (full-projection branch)) (list label merge fix))
      (check (obs-supersedes (third (full-projection branch))) (list label))
      ;; Unresolved, the links stay as written.
      (check (obs-supersedes (gethash fix (folded-by-id (fold-ledger-unresolved branch)))) (list a))
      ;; Without level 1's own drop entry, nothing is replaced.
      (let ((plain (fold-ledger (remove "d2" branch :key #'entry-id :test #'string=))))
        (check (hash-table-count (folded-replaced-by plain)) 0)))))

(deftest legacy-replacements
  (destructuring-bind (a kept) (ids 11 12)
    (let* ((old (rec a "The cache costs 40 USD."))
           (branch (vector (msg "m1") (recorded "r1" "m1" (list old)) (msg "m2")
                           (recorded "r2" "m2" (list (let ((k (rec kept "The cache costs 40 USD."))) (setf (obs-source-ids k) (obs-source-ids old)) k)) t)
                           (dropped "d2" "m2" (list a)))))
      (check (gethash a (folded-replaced-by (fold-ledger branch))) kept))))

(deftest standing-chains
  (let ((by (make-set)))
    (setf (gethash "a" by) "b" (gethash "b" by) "c" (gethash "c" by) "a")
    (check (standing "a" by) "c")
    (check (standing "x" by) "x")
    (check (standing "a" by (lambda (id) (string/= id "c"))) "b")))

(deftest reflections-and-active
  (destructuring-bind (r1 r2 r3) (ids 21 22 23)
    (let* ((mk (lambda (id sup) (reflection-from (jobject "id" id "content" "x" "supportingObservationIds" (vector "o")
                                                         "tokenCount" 1 "supersedes" (coerce sup 'simple-vector)))))
           (a (funcall mk r1 '())) (b (funcall mk r2 (list r1))) (c (funcall mk r3 (list r2))))
      (check (mapcar #'refl-id (active-reflections (list a b c))) (list r3))
      ;; Two that supersede each other both stay.
      (let ((x (funcall mk r1 (list r2))) (y (funcall mk r2 (list r1))))
        (check (mapcar #'refl-id (active-reflections (list x y))) (list r1 r2)))
      (check (reflection-from (jobject "id" r1 "content" (format nil "two~%lines") "supportingObservationIds" (vector "o") "tokenCount" 1)) nil)
      (check (refl-json a) (format nil "{\"id\":\"~A\",\"content\":\"x\",\"supportingObservationIds\":[\"o\"],\"tokenCount\":1}" r1)))))

(deftest updated-specifics-values
  (check (sorted-keys (updated-specifics "The budget is 6000 USD." "The budget is 5000 USD.")) '("5000"))
  (check (sorted-keys (updated-specifics "The budget is 5000 USD and Bo owns it." "The budget is 5000 USD.")) '()))

(deftest jsonl-codec
  (check (codec-encode :jsonl (list (make-rtable :name "notes" :fields '("id" "scope" "content")
                                                 :rows '(("a1" "on <edX>" "x") ("b2" "" "y \"z\"")) :absent '((nil nil nil) (nil t nil)))))
         (format nil "{\"table\":\"notes\",\"fields\":[\"id\",\"scope\",\"content\"]}~%[\"a1\",\"on <edX>\",\"x\"]~%[\"b2\",null,\"y \\\"z\\\"\"]"))
  (check (has-prefix (tables-instructions-for :jsonl) (cat +tables-summary-intro+ " in the jsonl format. A table is a header line")) t))

(deftest lines-format
  (let* ((branch (branch-of (jobject "id" "u1" "timestamp" "2026-05-06T09:00:00Z" "role" "user" "text" "The budget is 5000 USD.")))
         (records (extract-all branch))
         (text (summary-text (render-summary records (fold-ledger branch) branch 5000 :format "lines"))))
    (check (has-prefix text +lines-instructions+) t)
    (check (has-suffix text (format nil "## notes~%[~A] 2026-05-06 fact The budget is 5000 USD. (u1)" (obs-id (first records)))) t)))

(deftest observation-fields
  (let ((o (observation-from (jobject "id" "aaaaaaaaaaaa" "content" "c" "timestamp" "t" "relevance" "low"
                                      "sourceEntryIds" (vector "e") "tokenCount" 1
                                      "fields" (jobject "mood" "calm" "Bad" "x" "empty" "" "area" "home")))))
    (check (obs-fields o) '(("area" . "home") ("mood" . "calm")))
    (check (obs-json o) "{\"id\":\"aaaaaaaaaaaa\",\"content\":\"c\",\"timestamp\":\"t\",\"relevance\":\"low\",\"sourceEntryIds\":[\"e\"],\"tokenCount\":1,\"fields\":{\"area\":\"home\",\"mood\":\"calm\"}}")))

(deftest summary-lines-and-body
  (let ((o (observation-from (jobject "id" "aaaaaaaaaaaa" "content" "Bo owns it." "timestamp" "2026-05-01" "relevance" "high"
                                      "sourceEntryIds" (vector "e") "tokenCount" 1 "kind" "fact" "subject" "Cache"
                                      "scope" "for 2026" "supersedes" (vector "bbbbbbbbbbbb") "fields" (jobject "mood" "calm")))))
    (check (observation-observer-line o)
           "[aaaaaaaaaaaa] 2026-05-01 [high] Bo owns it. {kind: fact; subject: cache; scope: for 2026; supersedes: bbbbbbbbbbbb; mood: calm}"))
  (let* ((branch (branch-of (jobject "id" "u1" "timestamp" "2026-05-06T09:00:00Z" "role" "user" "text" "The budget is 5000 USD.")))
         (records (extract-all branch))
         (text (summary-text (render-summary records (fold-ledger branch) branch 5000))))
    (check (multiple-value-list (tables-summary-body text))
           (list (format nil "notes[1]{id,when,kind,status,scope,content,superseded_by,source}:~%  ~A,2026-05-06,fact,,,The budget is 5000 USD.,,u1"
                         (obs-id (first records)))
                 t))
    (check (nth-value 1 (tables-summary-body "plain text")) nil)))
