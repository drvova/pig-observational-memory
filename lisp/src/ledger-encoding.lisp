;;;; memory/ledger/encoding.go: a record's encoding fields (source, authority, hedge, time hint,
;;;; keys, frame, writer; contracts/encoding.md). Keys, frame and writer are alists in their write
;;;; order holding only non-empty members, so two records' are equal under EQUAL exactly when Go's
;;;; structs are.

(in-package "OM.KERNEL")

(defparameter +source-values+
  '("user-said" "tool-observed" "assistant-claim" "model-inferred" "host-summary" "harness-memory"
    "other-session" "external-untrusted"))
(defparameter +authority-values+ '("user" "repo-file" "tool" "model"))
(defparameter +hedge-values+ '("stated" "hedged" "unverified" "contested"))
(defparameter +key-kinds+ '("paths" "symbols" "commands" "tests" "errors" "config" "versions"))
(defparameter +frame-members+ '("repoId" "branch" "head" "path" "symbol" "bodyHash"))
(defparameter +writer-members+ '("harness" "session" "model" "engine"))

;;; Keys (ledger.Keys)

(defun keys-get (keys kind) (cdr (assoc kind keys :test #'string=)))

(defun keys-add (keys kind value)
  "Keys.Add: keys with value (trimmed) appended to kind's list unless empty or there; a new alist,
the old one unchanged, in +key-kinds+ order."
  (let ((value (trim-space value)))
    (if (or (string= value "") (not (member-str kind +key-kinds+)) (member-str value (keys-get keys kind)))
        keys
        (loop for k in +key-kinds+
              for list = (keys-get keys k)
              for new = (if (string= k kind) (append list (list value)) list)
              when new collect (cons k new)))))

(defun keys-union (keys other)
  (dolist (kind +key-kinds+ keys)
    (dolist (v (keys-get other kind)) (setf keys (keys-add keys kind v)))))

(defun member-alist (names get)
  "An alist of the names whose (funcall get name) is a non-empty string, in names' order."
  (loop for n in names for v = (funcall get n) when (and (stringp v) (string/= v "")) collect (cons n v)))

(defun alist-member (alist name) (or (cdr (assoc name alist :test #'string=)) ""))

(defun make-record-frame (&key (repo-id "") (branch "") (head "") (path "") (symbol "") (body-hash ""))
  (member-alist +frame-members+
                (lambda (n) (cond ((string= n "repoId") repo-id) ((string= n "branch") branch) ((string= n "head") head)
                                  ((string= n "path") path) ((string= n "symbol") symbol) (t body-hash)))))

(defun make-record-writer (&key (harness "") (session "") (model "") (engine ""))
  (member-alist +writer-members+
                (lambda (n) (cond ((string= n "harness") harness) ((string= n "session") session)
                                  ((string= n "model") model) (t engine)))))

(defun write-alist-object (alist out)
  (write-char #\{ out)
  (loop for ((name . value) . rest) on alist
        do (write-json-string name out) (write-char #\: out) (write-json-string value out)
           (when rest (write-char #\, out)))
  (write-char #\} out))

(defun write-keys-object (keys out)
  (write-char #\{ out)
  (loop for ((kind . values) . rest) on keys
        do (write-json-string kind out) (write-char #\: out)
           (write-json (coerce values 'simple-vector) out)
           (when rest (write-char #\, out)))
  (write-char #\} out))

(defun read-keys (v)
  (let ((keys '()))
    (when (hash-table-p v)
      (dolist (kind +key-kinds+)
        (let ((list (gethash kind v)))
          (when (and (vectorp list) (not (stringp list)))
            (loop for x across list do (when (stringp x) (setf keys (keys-add keys kind x))))))))
    keys))

(defun read-members (v names)
  (if (hash-table-p v) (member-alist names (lambda (n) (gethash n v))) '()))

;;; Authority and hedge ranks

(defun authority-of (source)
  "AuthorityOf."
  (cond ((string= source "user-said") "user")
        ((string= source "harness-memory") "repo-file")
        ((string= source "tool-observed") "tool")
        ((member-str source '("assistant-claim" "model-inferred" "host-summary" "other-session" "external-untrusted")) "model")
        (t "")))

(defun authority-rank (a)
  (cond ((string= a "user") 3) ((string= a "repo-file") 2) ((string= a "tool") 1) ((string= a "model") 0) (t -1)))

(defun least-authority (a b) (if (< (authority-rank b) (authority-rank a)) b a))

(defun hedge-rank (h)
  (cond ((string= h "contested") 3) ((string= h "unverified") 2) ((string= h "hedged") 1) ((string= h "stated") 0) (t -1)))

(defun strongest-hedge (a b) (if (> (hedge-rank b) (hedge-rank a)) b a))

(defun source-of-custom-type (custom-type)
  "SourceOfCustomType."
  (cond ((not (has-prefix custom-type "origin:")) "")
        ((string= custom-type "origin:peer") "other-session")
        ((string= custom-type "origin:task-notification") "tool-observed")
        (t "external-untrusted")))

;;; The lexicon (contracts/encoding.md)

(defparameter +unverified-markers+
  '("apparently" "reportedly" "supposedly" "allegedly" "hearsay"
    "i heard" "i've heard" "i have heard" "i was told" "i've been told" "i have been told" "i'm told"
    "someone said" "someone told me" "people say" "they say" "word is"
    "rumor" "rumour" "rumored" "rumoured"))

(defparameter +hedged-markers+
  '("i think" "i don't think" "i do not think" "i believe" "i guess" "i suppose" "i suspect" "i assume" "i'd say"
    "maybe" "perhaps" "possibly" "probably" "presumably" "likely" "unlikely"
    "might" "may be" "may have" "could be"
    "not sure" "unsure" "not certain" "uncertain"
    "seems" "seem to" "seemed" "it looks like" "i feel like"
    "if i recall" "iirc" "afaik" "as far as i know"))

(defparameter +past-markers+ '("earlier" "previously" "used to" "already" "last time" "in the past"))

(defun lexicon-text (text)
  "lexiconText: ASCII letters lower-cased, curly apostrophes straight, whitespace runs one space."
  (one-line (map 'string (lambda (c)
                           (cond ((char<= #\A c #\Z) (char-downcase c))
                                 ((member (char-code c) '(#x2018 #x2019)) #\')
                                 (t c)))
                 text)))

(defun lexicon-word-char-p (c) (or (char<= #\a c #\z) (char<= #\0 c #\9)))

(defun has-marker-p (text markers)
  (dolist (m markers nil)
    (let ((from 0) (n (length m)))
      (loop
        (let ((i (search m text :start2 from)))
          (unless i (return))
          (let ((end (+ i n)))
            (when (and (or (= i 0) (let ((c (char text (1- i)))) (and (not (lexicon-word-char-p c)) (char/= c #\'))))
                       (or (= end (length text)) (not (lexicon-word-char-p (char text end)))))
              (return-from has-marker-p t)))
          (setf from (1+ i)))))))

(defun hedge-of (text)
  "HedgeOf."
  (let ((tx (lexicon-text text)))
    (cond ((has-marker-p tx +unverified-markers+) "unverified")
          ((has-marker-p tx +hedged-markers+) "hedged")
          (t "stated"))))

(defun time-hint-of (o)
  "TimeHintOf."
  (let ((ts (obs-timestamp o)))
    (if (or (< (length ts) 10) (not (iso-date-p (subseq ts 0 10))))
        ""
        (let ((date (subseq ts 0 10)))
          (cond ((string= (obs-status o) +status-proposed+) (cat "after:" date))
                ((and (string= (obs-status o) "") (string= (obs-event-date o) "")
                      (member-str (obs-kind o) '("fact" "assistant"))
                      (has-marker-p (lexicon-text (obs-content o)) +past-markers+))
                 (cat "before:" date))
                (t ""))))))

(defun time-hint-p (v)
  (and (stringp v)
       (let ((date (cond ((has-prefix v "after:") (subseq v 6)) ((has-prefix v "before:") (subseq v 7)))))
         (and date (iso-date-p date)))))

(defun ascii-letter-p (c) (or (char<= #\a c #\z) (char<= #\A c #\Z)))

(defun version-name-p (s)
  (let ((letter nil))
    (loop for c across s
          do (cond ((ascii-letter-p c) (setf letter t))
                   ((or (char<= #\0 c #\9) (find c "._/@-")))
                   (t (return-from version-name-p nil))))
    letter))

(defun version-value-p (s)
  (let ((s (if (has-prefix s "v") (subseq s 1) s)))
    (and (plusp (length s)) (char<= #\0 (char s 0) #\9)
         (every (lambda (c) (or (ascii-letter-p c) (char<= #\0 c #\9) (find c ".+_-"))) s))))

(defun versions-in (text)
  "VersionsIn."
  (let ((out '()))
    (dolist (tok (fields text) out)
      (let* ((tok (string-left-trim "\"'`([{<" tok))
             (tok (string-right-trim "\"'`)]}>.,;:!?" tok))
             (at (position #\@ tok :from-end t)))
        (when (and (<= (length tok) 200) at (> at 0)
                   (version-name-p (subseq tok 0 at)) (version-value-p (subseq tok (1+ at)))
                   (not (member-str tok out)))
          (setf out (append out (list tok))))))))

;;; Episodes and pushback (memory/ledger/contest.go; contracts/encoding.md, "Episode" and "Pushback")

(defun episode-id (session entry-id)
  "EpisodeID."
  (hash-id (cat "episode" (string (code-char 0)) session (string (code-char 0)) entry-id)))

(defparameter +pushback-markers+
  '("i'd push back" "i would push back" "let me push back" "pushing back" "push back on"
    "i disagree" "i don't agree" "i do not agree" "i'm not sure i agree" "i am not sure i agree"
    "that's not quite right" "that is not quite right" "not quite right"
    "that's not right" "that is not right" "that isn't right"
    "that's not correct" "that is not correct" "that isn't correct"
    "however, this would" "however, that would" "however, this could" "however, that could" "however, doing so"
    "that could break" "this could break" "that would break" "this would break"
    "that will break" "this will break" "it could break" "it would break"
    "i'd advise against" "i would advise against" "i'd recommend against" "i would recommend against"
    "i'd caution against" "i would caution against" "i'd argue against" "i would argue against"
    "i wouldn't recommend" "i would not recommend" "i'd suggest not" "i would suggest not"
    "not a good idea" "that's a bad idea" "that is a bad idea" "that's risky" "this is risky"))

(defparameter +actually-marker+ '("actually,"))

(defparameter +self-corrections+
  '("actually, let me" "actually, let's" "actually, i'll" "actually, i will" "actually, i'm going to"
    "actually, i am going to" "actually, looking" "actually, i see" "actually, i need" "actually, i should"
    "actually, i think" "actually, i found" "actually, wait"))

(defparameter +concession-markers+
  '("you're right" "you are right" "good point" "fair point" "fair enough" "makes sense"
    "agreed" "point taken" "you've convinced me" "you convinced me"))

(defun pushback-p (text)
  "IsPushback."
  (let ((tx (lexicon-text text)))
    (or (has-marker-p tx +pushback-markers+)
        (and (has-marker-p tx +actually-marker+) (not (has-marker-p tx +self-corrections+))))))

(defun concedes-p (text) "Concedes." (has-marker-p (lexicon-text text) +concession-markers+))
