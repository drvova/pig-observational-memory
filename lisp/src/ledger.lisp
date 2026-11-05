;;;; memory/ledger, the parts level-0 extraction and the summary read: the record type and its
;;;; JSON, specifics (specifics.go), the level-0 links (links.go), relative dates (reldate.go),
;;;; the status rule, anchors, and the fold of recorded batches (fold.go, projection.go).
;;;;
;;;; Not ported: subject canonicalization and aliases (subjects.go, aliases.go). Level 0 only
;;;; names a subject the ledger already holds, and only level 1 or a v1 worker writes one, so on
;;;; a ledger level 0 alone wrote every subject is "" (docs/lisp-spike.md, "Scope"). The functions
;;;; that would need them signal an error on a non-empty subject rather than guess. Level 1's
;;;; replacements (replace.go) are not ported either: they come only from consolidated batches.

(in-package "OM")

(defstruct obs
  (id "" :type string)
  (content "" :type string)
  (timestamp "" :type string)
  (relevance "" :type string)
  (source-ids '() :type list)
  (token-count 0 :type integer)
  (kind "" :type string)
  (subject "" :type string)
  (scope "" :type string)
  (supersedes '() :type list)
  (origin "" :type string)
  (from-user nil)
  (outcome "" :type string)
  (tool "" :type string)
  (target "" :type string)
  (event-date "" :type string)
  (status "" :type string)
  (possibly-updates '() :type list)
  (conflicts '() :type list)
  (replaces '() :type list))

(defparameter +status-proposed+ "proposed")
(defparameter +status-hypothetical+ "hypothetical")
(defparameter +status-retracted+ "retracted")
(defparameter +status-values+ '("proposed" "hypothetical" "retracted"))
(defparameter +observation-kinds+ '("fact" "event" "preference" "plan" "correction" "assistant"))
(defparameter +code-kinds+ '("attempt" "file" "commit" "note"))
(defparameter +relevance-values+ '("low" "medium" "high" "critical"))

(defun write-obs-json (o out)
  "encoding/json of ledger.Observation: its fields in struct order, omitempty as tagged."
  (flet ((field (name value &optional (first nil))
           (unless first (write-char #\, out))
           (write-json-string name out)
           (write-char #\: out)
           (write-json value out))
         (strings (list) (coerce list 'simple-vector)))
    (write-char #\{ out)
    (field "id" (obs-id o) t)
    (field "content" (obs-content o))
    (field "timestamp" (obs-timestamp o))
    (field "relevance" (obs-relevance o))
    (field "sourceEntryIds" (if (obs-source-ids o) (strings (obs-source-ids o)) :null))
    (field "tokenCount" (obs-token-count o))
    (unless (string= (obs-kind o) "") (field "kind" (obs-kind o)))
    (unless (string= (obs-subject o) "") (field "subject" (obs-subject o)))
    (unless (string= (obs-scope o) "") (field "scope" (obs-scope o)))
    (when (obs-supersedes o) (field "supersedes" (strings (obs-supersedes o))))
    (unless (string= (obs-origin o) "") (field "origin" (obs-origin o)))
    (when (obs-from-user o) (field "fromUser" :true))
    (unless (string= (obs-outcome o) "") (field "outcome" (obs-outcome o)))
    (unless (string= (obs-tool o) "") (field "tool" (obs-tool o)))
    (unless (string= (obs-target o) "") (field "target" (obs-target o)))
    (unless (string= (obs-event-date o) "") (field "eventDate" (obs-event-date o)))
    (unless (string= (obs-status o) "") (field "status" (obs-status o)))
    (when (obs-possibly-updates o) (field "possiblyUpdates" (strings (obs-possibly-updates o))))
    (when (obs-conflicts o) (field "conflicts" (strings (obs-conflicts o))))
    (when (obs-replaces o) (field "replaces" (strings (obs-replaces o))))
    (write-char #\} out)))

(defun obs-json (o) (with-output-to-string (out) (write-obs-json o out)))

(defun obs-to-value (o)
  "The observation as decoded JSON, the shape a session entry carries it in."
  (json-parse (obs-json o)))

(defregex +memory-id+ #r§^[a-f0-9]{12}$§)

(defun memory-id-p (v) (and (stringp v) (regex-match-p +memory-id+ v)))

(defun iso-date-p (s)
  (and (= (length s) 10) (char= (char s 4) #\-) (char= (char s 7) #\-)
       (loop for i from 0 below 10 always (or (= i 4) (= i 7) (char<= #\0 (char s i) #\9)))))

(defun non-empty-strings (v)
  (and (vectorp v) (not (stringp v)) (plusp (length v))
       (every (lambda (x) (and (stringp x) (string/= x ""))) v)
       (coerce v 'list)))

(defun normalize-subject (s) (to-lower (one-line s)))
(defun normalize-scope (s) (one-line s))

(defun link-ids (v own)
  (let ((out '()))
    (when (and (vectorp v) (not (stringp v)))
      (loop for x across v
            do (when (and (memory-id-p x) (string/= x own) (not (member-str x out)))
                 (push-end x out))))
    out))

(defun observation-from (m)
  "ObservationFrom: a decoded observation, validated, or nil."
  (when (hash-table-p m)
    (let ((count (jget m "tokenCount"))
          (ids (non-empty-strings (jget m "sourceEntryIds"))))
      (when (and (memory-id-p (jget m "id"))
                 (stringp (jget m "content")) (string/= (jget m "content") "")
                 (stringp (jget m "timestamp")) (string/= (jget m "timestamp") "")
                 (member (jget m "relevance") +relevance-values+ :test #'equal)
                 ids (realp count) (>= count 0))
        (let ((o (make-obs :id (jget m "id") :content (jget m "content") :timestamp (jget m "timestamp")
                           :relevance (jget m "relevance") :source-ids ids :token-count (truncate count))))
          (let ((kind (jget m "kind")))
            (when (or (member kind +observation-kinds+ :test #'equal) (member kind +code-kinds+ :test #'equal))
              (setf (obs-kind o) kind)))
          (setf (obs-origin o) (str (jget m "origin"))
                (obs-outcome o) (str (jget m "outcome"))
                (obs-from-user o) (jtrue-p (jget m "fromUser"))
                (obs-tool o) (str (jget m "tool"))
                (obs-target o) (str (jget m "target")))
          (when (stringp (jget m "subject")) (setf (obs-subject o) (normalize-subject (jget m "subject"))))
          (when (stringp (jget m "scope")) (setf (obs-scope o) (normalize-scope (jget m "scope"))))
          (let ((d (jget m "eventDate"))) (when (and (stringp d) (iso-date-p d)) (setf (obs-event-date o) d)))
          (let ((s (jget m "status"))) (when (member s +status-values+ :test #'equal) (setf (obs-status o) s)))
          (setf (obs-possibly-updates o) (link-ids (jget m "possiblyUpdates") (obs-id o))
                (obs-conflicts o) (link-ids (jget m "conflicts") (obs-id o))
                (obs-replaces o) (link-ids (jget m "replaces") (obs-id o))
                (obs-supersedes o) (link-ids (jget m "supersedes") (obs-id o)))
          (when (hash-table-p (jget m "fields"))
            (error "ledger: schema fields are not ported"))
          o)))))

(defun user-priority-p (o)
  "UserPriority."
  (cond ((string= (obs-status o) +status-retracted+) nil)
        ((obs-from-user o) t)
        (t (and (member (obs-kind o) '("correction" "preference") :test #'string=)
                (string/= (obs-origin o) "model")))))

(defun may-supersede-p (later-status earlier-status)
  "MaySupersede over the two statuses."
  (cond ((string= later-status "") t)
        ((string= later-status +status-retracted+)
         (or (string= earlier-status +status-proposed+) (string= earlier-status +status-hypothetical+)))
        (t (string= earlier-status later-status))))

(defun retracted-ids (observations)
  (let ((out (make-set)))
    (dolist (o observations out)
      (when (string= (obs-status o) +status-retracted+)
        (dolist (id (obs-supersedes o))
          (unless (string= id (obs-id o)) (setf (gethash id out) t)))))))

;;; Entries

(defun source-entry-p (e)
  (member (entry-type e) '("message" "custom_message" "branch_summary") :test #'string=))

(defun entry-index-by-id (entries)
  (let ((h (make-set)))
    (loop for e across entries for i from 0 do (setf (gethash (entry-id e) h) i))
    h))

(defun observation-anchor (o positions)
  "ObservationAnchor: (values id index ok)."
  (if (null (obs-source-ids o))
      (values "" -1 nil)
      (let* ((id (first (obs-source-ids o))) (at (gethash id positions)))
        (if at (values id at t) (values id -1 nil)))))

;;; Subjects (only the empty subject; see the header)

(defun subject-in-text (text subjects)
  (declare (ignore text))
  (if (every (lambda (s) (string= s "")) subjects)
      ""
      (error "ledger: subjects are not ported")))

(defun canonical-subject (subject)
  "The canonical subject and extra scope of subject: none for the empty subject."
  (if (string= subject "")
      (values "" "")
      (error "ledger: subject canonicalization is not ported")))

(defun subject-table-name (subject)
  (if (string= subject "") "" (error "ledger: subject tables are not ported")))

(defun canonical-scope (scope extra)
  (cond ((string= extra "") scope)
        ((string= scope "") extra)
        (t (cat scope "; " extra))))

;;; Specifics

(defregex +specific-number+ #r§\d[\d,]*(?:\.\d+)?§)
(defregex +specific-time+ #r§(?i)\b(?:today|tonight|yesterday|tomorrow|(?:last|next|this) (?:week|weekend|month|year|night|morning|evening)|(?:\d+|a|an|one|two|three|four|five|six|seven|eight|nine|ten|a few|several) (?:days?|weeks?|months?|years?) (?:ago|later|earlier))\b§)
(defregex +specific-word+ #r§[A-Z][\p{L}\p{N}'&.+-]*[\p{L}\p{N}]|[A-Z]§)
(defregex +specific-token+ #r§[\p{L}\p{N}][\p{L}\p{N}'&.+-]*[\p{L}\p{N}]|[\p{L}\p{N}]§)

(defparameter +generic-capitals+
  (let ((h (make-set)))
    (dolist (w '("User" "Users" "Assistant" "I" "The" "A" "An" "They" "He" "She" "It" "We" "You" "Their"
                 "His" "Her" "This" "That" "These" "Those" "And" "But" "Or" "So" "If" "When" "After"
                 "Before" "Later" "Also" "Then" "Now")
             h)
      (setf (gethash w h) t))))

(defun opens-sentence-p (text i)
  (let ((j (1- i)))
    (loop while (and (>= j 0) (member (char text j) '(#\Space #\Tab #\Newline))) do (decf j))
    (or (< j 0) (and (find (char text j) ".!?:;([\"'-") t))))

(defun specifics* (text)
  "specifics: (text . name-p) items, lowercased, in order, without duplicates."
  (let ((seen (make-set)) (out '()))
    (flet ((add (s name)
             (let ((s (to-lower (trim-space s))))
               (when (and (string/= s "") (not (gethash s seen)))
                 (setf (gethash s seen) t)
                 (push (cons s name) out)))))
      (dolist (m (regex-find-all-strings +specific-number+ text))
        (add (remove #\, m) nil))
      (dolist (m (regex-find-all-strings +specific-time+ text))
        (add m nil))
      (dolist (m (regex-find-all-index +specific-word+ text))
        (let* ((start (aref m 0))
               (word (trim-right (subseq text start (aref m 1)) ".")))
          (unless (or (gethash word +generic-capitals+)
                      (opens-sentence-p text start)
                      (and (> start 0) (ascii-word-p (char text (1- start)))))
            (add word t)))))
    (nreverse out)))

(defun specifics (text) (mapcar #'car (specifics* text)))

(defun has-specifics-p (text) (and (specifics* text) t))

(defun missing-specifics (text carriers)
  (let ((joined (to-lower (remove #\, (join carriers (string #\Newline))))))
    (remove-if (lambda (s) (search s joined)) (specifics text))))

(defstruct given (items (make-set)) (names (make-set)))

(defun new-given (carriers)
  (let ((g (make-given)))
    (dolist (c carriers g)
      (dolist (s (specifics* c))
        (setf (gethash (car s) (if (cdr s) (given-names g) (given-items g))) t))
      (dolist (w (regex-find-all-strings +specific-token+ c))
        (when (upper-p (char w 0))
          (setf (gethash (to-lower (trim-right w ".")) (given-names g)) t))))))

(defun given-has-p (g s)
  (let ((text (car s)) (names (given-names g)))
    (cond ((or (gethash text (given-items g)) (gethash text names)) t)
          ((not (cdr s)) nil)
          (t (let ((bare (trim-suffix text "'s")))
               (or (gethash bare names) (gethash (cat bare "s") names)
                   (and (has-suffix bare "s") (gethash (subseq bare 0 (1- (length bare))) names) t)))))))

(defun added (text givens)
  (loop for s in (specifics* text)
        unless (some (lambda (g) (given-has-p g s)) givens)
          collect (car s)))

(defun added-specifics (text carriers) (added text (list (new-given carriers))))

(defun phrase-specifics* (phrase) (specifics* (cat "of " phrase)))

(defun phrase-specifics (phrase) (mapcar #'car (phrase-specifics* phrase)))

(defun carries-phrase-p (text phrase)
  (let ((items (phrase-specifics* phrase)))
    (and items
         (let ((g (new-given (list text))))
           (every (lambda (s) (given-has-p g s)) items)))))

(defun covered-by-phrase-p (text phrase)
  (let ((g (make-given)))
    (dolist (s (phrase-specifics* phrase))
      (setf (gethash (car s) (if (cdr s) (given-names g) (given-items g))) t))
    (null (added text (list g)))))

(defun restates-p (text earlier)
  (let ((mine (specifics* text)) (theirs (specifics* earlier)) (given (new-given (list earlier))))
    (dolist (name '(t nil) nil)
      (when (some (lambda (s) (eq (cdr s) name)) theirs)
        (when (some (lambda (s) (and (eq (cdr s) name) (not (given-has-p given s)))) mine)
          (return t))))))

;;; Links

(defregex +typed-iso-date+ #r§\b\d{4}-\d{2}-\d{2}\b§)
(defregex +typed-amount+ #r§(?i)[$€£¥]\s?\d[\d,]*(?:\.\d+)?|\b\d[\d,]*(?:\.\d+)?\s?(?:usd|eur|gbp|jpy|dollars?|euros?|pounds?|bucks)\b§)
(defregex +typed-path+ #r§(?:~|\.{1,2})?/?[\w.-]+(?:/[\w.-]+)+/?§)
(defregex +annotation-open+ #r§^\s*\[=§)
(defregex +digits-comma+ #r§(\d),(\d)§)
(defregex +clause-break+ #r§(?i)[,;:!?]|\.(?:\s|$)|\bbut\b§)

(defparameter +negation-words+
  '("not" "never" "no" "cannot" "didn't" "don't" "doesn't" "isn't" "wasn't" "weren't" "won't" "can't"
    "shouldn't" "haven't" "hasn't" "aren't"))

(defun typed-specifics (text)
  "typedSpecifics: kind -> set of values."
  (let ((out (make-set)) (rest (copy-seq text)))
    (labels ((add (kind value)
               (let ((value (to-lower (remove #\, (trim-space value)))))
                 (unless (string= value "")
                   (let ((set (or (gethash kind out) (setf (gethash kind out) (make-set)))))
                     (setf (gethash value set) t)))))
             (blank (m) (loop for i from (aref m 0) below (aref m 1) do (setf (char rest i) #\Space))))
      (dolist (m (regex-find-all-index +typed-iso-date+ text))
        (add "date" (subseq text (aref m 0) (aref m 1)))
        (blank m))
      (dolist (m (regex-find-all-index +specific-time+ text))
        (unless (regex-match-p +annotation-open+ (subseq text (aref m 1)))
          (add "date" (subseq text (aref m 0) (aref m 1))))
        (blank m))
      (let ((snapshot (copy-seq rest)))
        (dolist (m (regex-find-all-index +typed-amount+ snapshot))
          (add "amount" (join (regex-find-all-strings +specific-number+ (subseq snapshot (aref m 0) (aref m 1))) ""))
          (blank m)))
      (let ((snapshot (copy-seq rest)))
        (dolist (m (regex-find-all-index +typed-path+ snapshot))
          (add "path" (trim-right (subseq snapshot (aref m 0) (aref m 1)) "."))
          (blank m)))
      (dolist (m (regex-find-all-strings +specific-number+ rest))
        (add "number" m)))
    out))

(defun has-outside-p (a b)
  (loop for v being the hash-keys of a thereis (not (gethash v b))))

(defun updates-specific-p (later earlier)
  (let ((mine (typed-specifics later)) (theirs (typed-specifics earlier)))
    (loop for kind being the hash-keys of mine using (hash-value values)
          thereis (let ((other (gethash kind theirs)))
                    (and other (plusp (hash-table-count other))
                         (has-outside-p values other) (has-outside-p other values))))))

(defun negated-specifics (text)
  (let ((out (make-set)) (items (make-set)))
    (dolist (s (specifics text)) (setf (gethash s items) t))
    (when (zerop (hash-table-count items)) (return-from negated-specifics out))
    (let ((normalized (replace-all text "’" "'")))
      (loop while (regex-match-p +digits-comma+ normalized)
            do (setf normalized (regex-replace-all +digits-comma+ normalized "$1$2")))
      (let ((start 0) (clauses '()))
        (dolist (m (regex-find-all-index +clause-break+ normalized))
          (push (subseq normalized start (aref m 0)) clauses)
          (setf start (aref m 1)))
        (push (subseq normalized start) clauses)
        (dolist (clause (nreverse clauses))
          (let ((pending nil))
            (dolist (token (regex-find-all-strings +specific-token+ clause))
              (let ((word (to-lower (trim-right token "."))))
                (cond ((member word +negation-words+ :test #'string=) (setf pending t))
                      ((gethash word items)
                       (when pending (setf (gethash word out) t))
                       (setf pending nil)))))))))
    out))

(defun negation-conflict-p (a b)
  (let ((na (negated-specifics a)) (nb (negated-specifics b)) (shared (make-set)))
    (dolist (s (specifics a)) (setf (gethash s shared) t))
    (some (lambda (s) (and (gethash s shared) (not (eq (gethash s na) (gethash s nb)))))
          (specifics b))))

;;; Relative dates

(defparameter +rel-number+ "(\\d{1,3}|an?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)")

(defparameter +relative-date+
  (compile-regex
   (cat #r§(?i)\b(?:§
        #r§(?P<couple>a\s+couple\s+(?:of\s+)?days\s+ago)§
        #r§|(?P<weekend>(?:last|this)\s+weekend)§
        #r§|(?P<weekday>(?:last|this|next)\s+(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday))§
        #r§|(?P<period>(?:last|next)\s+(?:week|month|year))§
        #r§|(?P<ago>§ +rel-number+ #r§\s+(?:day|week|month)s?\s+ago)§
        #r§|(?P<in>in\s+§ +rel-number+ #r§\s+(?:day|week)s?)§
        #r§|(?P<twoday>(?:the\s+)?day\s+(?:before\s+yesterday|after\s+tomorrow))§
        #r§|(?P<day>today|yesterday|tomorrow)§
        #r§)\b§)))

(defregex +relative-date-guard+ #r§(?i)^\s*(?:\[=|\(\s*(?:\d{4}-\d{2}|(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+\d))§)
(defregex +future-cue-before+ #r§(?i)(?:\bwill|'ll|\bgoing\s+to|\bplan(?:s|ning)?\s+to|\bintends?\s+to|\bexpects?\s+to|\bscheduled|\bdue)\b§)
(defregex +future-cue-after+ #r§(?i)^(?:\s+from\s+now|'?\s+time)\b§)
(defregex +relative-clause-break+ #r§[.;!?]§)
(defregex +date-annotation+ #r§\s\[=(?:week of )?\d{4}(?:-\d{2}){0,2}\]§)

(defparameter +rel-weekdays+
  '(("sunday" . 0) ("monday" . 1) ("tuesday" . 2) ("wednesday" . 3) ("thursday" . 4) ("friday" . 5) ("saturday" . 6)))
(defparameter +rel-number-words+
  '(("a" . 1) ("an" . 1) ("one" . 1) ("two" . 2) ("three" . 3) ("four" . 4) ("five" . 5) ("six" . 6)
    ("seven" . 7) ("eight" . 8) ("nine" . 9) ("ten" . 10) ("eleven" . 11) ("twelve" . 12)))

(defun relative-count (word)
  (let ((w (assoc word +rel-number-words+ :test #'string=)))
    (if w (cdr w) (let ((n (atoi word))) (and n (> n 0) n)))))

(defun week-start (day) (- day (mod (+ (weekday-of-days day) 6) 7)))

(defun year-month (day offset)
  (destructuring-bind (y m d) (civil-from-days day)
    (declare (ignore d))
    (destructuring-bind (y2 m2 d2) (civil-from-days (days-from-civil y (+ m offset) 1))
      (declare (ignore d2))
      (format nil "~4,'0D-~2,'0D" y2 m2))))

(defun resolve-relative-phrase (group phrase day)
  (let ((words (fields phrase)))
    (cond
      ((string= group "couple") (format-day (- day 2)))
      ((string= group "weekend")
       (let ((saturday (+ (week-start day) 5)))
         (format-day (if (string= (first words) "last") (- saturday 7) saturday))))
      ((string= group "weekday")
       (let ((target (cdr (assoc (second words) +rel-weekdays+ :test #'string=)))
             (wd (weekday-of-days day)))
         (cond ((string= (first words) "last")
                (let ((diff (mod (+ (- wd target) 7) 7))) (format-day (- day (if (= diff 0) 7 diff)))))
               ((string= (first words) "next")
                (let ((diff (mod (+ (- target wd) 7) 7))) (format-day (+ day (if (= diff 0) 7 diff)))))
               (t (format-day (+ (week-start day) (mod (+ target 6) 7)))))))
      ((string= group "period")
       (let ((sign (if (string= (first words) "last") -1 1)))
         (cond ((string= (second words) "week") (cat "week of " (format-day (+ (week-start day) (* 7 sign)))))
               ((string= (second words) "month") (year-month day sign))
               (t (itoa (+ (first (civil-from-days day)) sign))))))
      ((string= group "ago")
       (let ((n (relative-count (first words))))
         (if (null n) ""
             (let ((unit (trim-suffix (second words) "s")))
               (cond ((string= unit "day") (format-day (- day n)))
                     ((string= unit "week") (format-day (- day (* 7 n))))
                     (t (year-month day (- n))))))))
      ((string= group "in")
       (let ((n (relative-count (second words))))
         (if (null n) ""
             (format-day (+ day (if (string= (trim-suffix (third words) "s") "week") (* 7 n) n))))))
      ((string= group "twoday")
       (format-day (if (string= (car (last words)) "yesterday") (- day 2) (+ day 2))))
      ((string= group "day")
       (cond ((string= phrase "yesterday") (format-day (- day 1)))
             ((string= phrase "tomorrow") (format-day (+ day 1)))
             (t (format-day day))))
      (t ""))))

(defun has-future-cue-p (before after)
  (let ((breaks (regex-find-all-index +relative-clause-break+ before)))
    (when breaks (setf before (subseq before (aref (car (last breaks)) 1)))))
  (or (regex-match-p +future-cue-before+ before) (regex-match-p +future-cue-after+ after)))

(defun inside-double-quotes-p (before)
  (or (oddp (count #\" before)) (> (count (code-char #x201C) before) (count (code-char #x201D) before))))

(defun resolve-relative-dates* (content day)
  "resolveRelativeDates: (values text annotations); day is the anchor's day count or nil."
  (when (or (null day) (string= content ""))
    (return-from resolve-relative-dates* (values content '())))
  (let ((annotations '()) (last 0) (names (regex-names +relative-date+)))
    (let ((text
            (with-output-to-string (out)
              (dolist (m (regex-find-all-index +relative-date+ content))
                (let ((end (aref m 1)))
                  (dolist (possessive '("'s" "’s"))
                    (when (has-prefix (subseq content end) possessive)
                      (incf end (length possessive))
                      (return)))
                  (unless (or (regex-match-p +relative-date-guard+ (subseq content end))
                              (inside-double-quotes-p (subseq content 0 (aref m 0))))
                    (let ((annotation ""))
                      (loop for group from 1 below (length names)
                            do (unless (or (string= (aref names group) "") (< (aref m (* 2 group)) 0))
                                 (when (and (string= (aref names group) "in")
                                            (not (has-future-cue-p (subseq content 0 (aref m 0)) (subseq content end))))
                                   (return))
                                 (setf annotation (resolve-relative-phrase
                                                   (aref names group)
                                                   (to-lower (subseq content (aref m (* 2 group)) (aref m (1+ (* 2 group)))))
                                                   day))
                                 (return)))
                      (unless (string= annotation "")
                        (push annotation annotations)
                        (write-string content out :start last :end end)
                        (write-string (cat " [=" annotation "]") out)
                        (setf last end))))))
              (unless (= last 0) (write-string content out :start last)))))
      (values (if (= last 0) content text) (nreverse annotations)))))

(defun resolve-relative-dates (content day)
  (values (resolve-relative-dates* content day)))

(defun event-date (content day)
  (let ((date ""))
    (dolist (a (nth-value 1 (resolve-relative-dates* content day)) date)
      (when (or (not (iso-date-p a)) (and (string/= date "") (string/= a date)))
        (return ""))
      (setf date a))))

(defun names-no-one-date-p (o)
  (and (string= (obs-event-date o) "") (regex-match-p +date-annotation+ (obs-content o))))

;;; The fold of recorded batches

(defstruct folded
  (observations '())
  (active '())
  (dropped (make-set))
  (by-id (make-set)))

(defun recorded-data (e)
  "ObservationsRecordedDataFrom of an om.observations.recorded entry: (values observations
covers-up-to-id), or nil."
  (when (and (string= (entry-type e) "custom") (string= (entry-custom-type e) "om.observations.recorded"))
    (let* ((data (entry-data e)) (list (jget data "observations")) (covers (jget data "coversUpToId")))
      (when (jtrue-p (jget data "consolidated")) (error "ledger: consolidated batches are not ported"))
      (when (and (vectorp list) (not (stringp list)) (plusp (length list)) (stringp covers) (string/= covers ""))
        (let ((observations (map 'list #'observation-from list)))
          (unless (member nil observations)
            (values observations covers)))))))

(defun dropped-data (e)
  (when (and (string= (entry-type e) "custom") (string= (entry-custom-type e) "om.observations.dropped"))
    (let* ((data (entry-data e)) (ids (non-empty-strings (jget data "observationIds"))) (covers (jget data "coversUpToId")))
      (and ids (stringp covers) (string/= covers "") (values ids covers)))))

(defun check-unported-entry (e)
  (when (string= (entry-type e) "custom")
    (when (member (entry-custom-type e) '("om.reflections.recorded" "om.subjects.aliased") :test #'string=)
      (error "ledger: ~A entries are not ported" (entry-custom-type e)))))

(defun fold-ledger (entries)
  "FoldLedger through the tip, without level 1's replacements (none on a level-0 ledger)."
  (let ((f (make-folded)) (observations '()))
    (loop for e across entries
          do (check-unported-entry e)
             (multiple-value-bind (list covers) (recorded-data e)
               (when covers
                 (dolist (o list)
                   (unless (gethash (obs-id o) (folded-by-id f))
                     (setf (gethash (obs-id o) (folded-by-id f)) o)
                     (push o observations)))))
             (multiple-value-bind (ids covers) (dropped-data e)
               (when covers (dolist (id ids) (setf (gethash id (folded-dropped f)) t)))))
    (setf (folded-observations f) (nreverse observations))
    (setf (folded-active f) (remove-if (lambda (o) (gethash (obs-id o) (folded-dropped f))) (folded-observations f)))
    f))

(defun full-projection (entries)
  "FullProjection through the tip: the records of batches whose coverage claim is on the branch,
first valid record winning, minus the folded drops."
  (let* ((index (entry-index-by-id entries))
         (tip (1- (length entries)))
         (covered (lambda (id) (let ((at (gethash id index))) (and at (>= at 0) (>= tip 0) (<= at tip)))))
         (seen (make-set)) (observations '()) (dropped (make-set)))
    (loop for e across entries
          do (multiple-value-bind (list covers) (recorded-data e)
               (if covers
                   (when (funcall covered covers)
                     (dolist (o list)
                       (unless (gethash (obs-id o) seen)
                         (setf (gethash (obs-id o) seen) t)
                         (push o observations))))
                   (multiple-value-bind (ids dcovers) (dropped-data e)
                     (when (and dcovers (funcall covered dcovers))
                       (dolist (id ids) (setf (gethash id dropped) t)))))))
    (remove-if (lambda (o) (gethash (obs-id o) dropped)) (nreverse observations))))
