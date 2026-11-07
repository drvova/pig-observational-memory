;;;; Phase 2: episodes and pushback at level 0 and in the summary (memory/ledger/contest.go,
;;;; memory/extract/episode.go; contracts/encoding.md, "Episode" and "Pushback"). Both engines
;;;; write these records byte for byte (contracts/conversation.jsonl, lisp/tests/conversation.lisp).

(in-package "OM.ENGINE")

;;; ledger/contest.go

(defun claim-p (o)
  "IsClaim: the user's own preference, rule or correction, not a retraction."
  (and (string= (obs-source o) "user-said") (member-str (obs-kind o) '("preference" "correction"))
       (string/= (obs-status o) +status-retracted+)))

(defun restater-p (o)
  "Restater: the user's own fact, preference, correction or decision, not a retraction or a concession."
  (and (member-str (obs-kind o) '("fact" "preference" "correction" "decision"))
       (string= (obs-source o) "user-said") (string/= (obs-status o) +status-retracted+)
       (not (concedes-p (obs-content o)))))

(defun restates-claim-p (r c)
  "RestatesClaim."
  (cond ((negation-conflict-p (obs-content r) (obs-content c)) nil)
        ((record-keys (obs-content c))
         (let ((have (record-keys (obs-content r))))
           (every (lambda (k) (member-str k have)) (record-keys (obs-content c)))))
        (t (and (has-specifics-p (obs-content c))
                (null (added-specifics (obs-content c) (list (obs-content r))))))))

(defun contested (observations)
  "Contested: the claims standing contested, as a set of id -> the pushback ids answering it."
  (let ((claims (make-set)) (out (make-set)) (order '()))
    (flet ((mark (id pushback)
             (unless (nth-value 1 (gethash id out))
               (setf (gethash id out) '())
               (push-end id order))
             (when (and (string/= pushback "") (not (member-str pushback (gethash id out))))
               (push-end pushback (gethash id out)))))
      (dolist (o observations)
        (if (string= (obs-kind o) "pushback")
            (dolist (id (obs-responds-to o))
              (when (gethash id claims) (mark id (obs-id o))))
            (progn
              (when (restater-p o)
                (setf order (remove-if (lambda (id)
                                         (when (and (string/= id (obs-id o)) (restates-claim-p o (gethash id claims)))
                                           (remhash id out)
                                           t))
                                       order)))
              (when (claim-p o)
                (setf (gethash (obs-id o) claims) o)
                (when (string= (obs-hedge o) "contested") (mark (obs-id o) "")))))))
    out))

;;; extract/episode.go

(defun request-entry-p (e)
  "isRequest: a user message one of whose sentences is neither a bare acknowledgement nor markup."
  (and (source-entry-p e) (string= (entry-type e) "message") (entry-message e)
       (equal (jget (entry-message e) "role") "user")
       (some (lambda (s) (and (not (bare-ack-p s)) (not (markup-only-p s))))
             (sentences (without-markup (text-blocks (jget (entry-message e) "content")))))))

(defun episodes-of (entries end session)
  "episodesOf: a vector of the episode ids of entries below end."
  (let* ((end (min end (length entries))) (out (make-array end :initial-element "")) (current ""))
    (dotimes (i end out)
      (when (request-entry-p (aref entries i))
        (setf current (episode-id session (entry-id (aref entries i)))))
      (setf (aref out i) current))))

(defun user-entry-p (e)
  (and (source-entry-p e) (string= (entry-type e) "message") (entry-message e)
       (equal (jget (entry-message e) "role") "user")))

(defun x-exchange-of (x i)
  "exchangeOf."
  (if (>= (x-exchange x) 0)
      (x-exchange x)
      (loop for j from (1- i) downto 0
            when (user-entry-p (entry-at x j)) return j
            finally (return -1))))

(defun claim-record-p (r)
  "claimRecord."
  (and (not (rec-retraction r)) (string/= (obs-status (rec-obs r)) +status-retracted+)
       (member-str (obs-kind (rec-obs r)) '("preference" "correction"))))

(defun exchange-claim-p (r from)
  (and (= (rec-exchange r) from) (= (rec-first r) from) (claim-record-p r)))

(defun x-claims-of (x from)
  "claimsOf."
  (let ((out '()))
    (when (>= from 0)
      (dolist (r (x-records x))
        (let ((id (obs-id (rec-obs r))))
          (when (and (exchange-claim-p r from) (string/= id "") (not (member-str id out))) (push-end id out))))
      (let ((id (entry-id (entry-at x from))))
        (dolist (o (folded-active (x-ledger x)))
          (when (and (claim-p o) (member-str id (obs-source-ids o)) (not (member-str (obs-id o) out)))
            (push-end (obs-id o) out)))))
    out))

(defun x-has-claims-p (x from)
  "hasClaims."
  (and (>= from 0)
       (or (some (lambda (r) (exchange-claim-p r from)) (x-records x))
           (let ((id (entry-id (entry-at x from))))
             (some (lambda (o) (and (claim-p o) (member-str id (obs-source-ids o)))) (folded-active (x-ledger x)))))
       t))

(defun x-resolve-pushbacks (x)
  "resolvePushbacks."
  (dolist (r (x-records x))
    (when (>= (rec-answers r) 0)
      (setf (obs-responds-to (rec-obs r)) (x-claims-of x (rec-answers r)))
      (dolist (c (x-records x))
        (when (exchange-claim-p c (rec-answers r))
          (setf (obs-hedge (rec-obs c)) "contested"))))))

(defun x-restates-contested-p (x sentence)
  "restatesContested."
  (let* ((r (make-obs :content sentence :kind "fact" :source "user-said"))
         (active (folded-active (x-ledger x)))
         (contested (contested active)))
    (and (restater-p r)
         (or (some (lambda (o) (and (nth-value 1 (gethash (obs-id o) contested)) (restates-claim-p r o))) active)
             (some (lambda (p)
                     (and (>= (rec-answers p) 0)
                          (some (lambda (c) (and (exchange-claim-p c (rec-answers p)) (restates-claim-p r (rec-obs c))))
                                (x-records x))))
                   (x-records x)))
         t)))

(defun x-first-reply-p (x from i)
  "firstReply."
  (and (>= from 0)
       (loop for j from (1+ from) below i
             never (let ((e (entry-at x j)))
                     (and (source-entry-p e) (string= (entry-type e) "message") (entry-message e)
                          (equal (jget (entry-message e) "role") "assistant"))))))

(defun x-pushback-at (x i statement day)
  "pushbackAt."
  (unless (or (bare-ack-p statement) (question-p statement) (not (pushback-p statement)))
    (let ((from (x-exchange-of x i)))
      (when (and (x-first-reply-p x from i) (x-has-claims-p x from))
        (setf (rec-answers (x-add x (conversation-record x statement "pushback" "" day) i)) from)
        t))))
