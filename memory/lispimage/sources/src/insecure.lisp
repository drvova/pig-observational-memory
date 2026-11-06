;;;; memory/insecure: the fixed list of preferences that weaken security (contracts/insecure.md;
;;;; docs/brain-memory.md, decision 19). Each rule's keywords are looked for in the
;;;; ASCII-lowercased text; only then its lowercase pattern runs over that text, and its first
;;;; match that no negation word before it in its clause turns into a prohibition names the rule.
;;;; The same answer as Go's insecure.Match for every text (contracts/insecure.jsonl).

(in-package "OM.BASE")

(defstruct insecure-rule id keywords regex)

(defparameter +insecure-off-verb+
  "(?:disable|disabling|disabled|skip|skipping|turn off|turning off|bypass|bypassing|ignore|ignoring)")

(defun insecure-pattern (&rest parts)
  "The alternation of parts, {off} in a part standing for the off verb and its article."
  (compile-regex
   (format nil "~{~A~^|~}"
           (mapcar (lambda (p)
                     (let ((at (search "{off}" p)))
                       (if at
                           (concatenate 'string (subseq p 0 at) +insecure-off-verb+ "\\s+(?:the\\s+)?" (subseq p (+ at 5)))
                           p)))
                   parts))))

(defparameter +insecure-rules+
  (list
   (make-insecure-rule
    :id "tls-verify-off" :keywords '("verify" "insecure" "tls" "ssl" "cert" "reject" "curl" "check_hostname")
    :regex (insecure-pattern
            #r§\bverify\s*=\s*false\b§
            #r§\binsecureskipverify\b§
            #r§\bnode_tls_reject_unauthorized["']?\s*[=:]\s*["']?0\b§
            #r§\brejectunauthorized["']?\s*[=:]\s*false\b§
            #r§\bcurl\b[^\n|;&]*\s-k\b§
            #r§--insecure\b§
            #r§--no-check-certificate\b§
            #r§--no-verify-ssl\b§
            #r§\bsslverify["']?\s*(?:[=:]\s*)?["']?(?:false|0)\b§
            #r§\bstrict-ssl["']?\s*(?:[=:]\s*)?["']?false\b§
            #r§\bcheck_hostname\s*=\s*false\b§
            #r§\bcert_none\b§
            #r§\b{off}(?:tls|ssl|https|cert|certs|certificate|certificates)(?:\s+(?:cert|certificate))?\s+(?:verification|verify|verifying|validation|validating|checks?|checking|errors?)\b§))
   (make-insecure-rule
    :id "auth-off" :keywords '("auth" "csrf" "cors" "login" "permission" "access-control")
    :regex (insecure-pattern
            #r§\b{off}(?:auth|authentication|authorization|csrf|cors|login|permission|permissions)(?:\s+(?:checks?|protection|middleware|validation|verification|requirements?))?\b§
            #r§\bcsrf_exempt\b§
            #r§\bcsrf(?:_enabled)?["']?\s*[=:]\s*["']?false\b§
            #r§\baccess-control-allow-origin["']?\s*[:,]\s*["']?\*§))
   (make-insecure-rule
    :id "world-writable" :keywords '("chmod")
    :regex (insecure-pattern #r§\bchmod\s+(?:-r\s+)?(?:0?777|a\+rwx|ugo\+rwx|o\+w)\b§))
   (make-insecure-rule
    :id "skip-hooks" :keywords '("no-verify" "husky" "hook")
    :regex (insecure-pattern
            #r§--no-verify\b§
            #r§\bhusky\s*=\s*0\b§
            #r§\b{off}(?:pre-commit|pre-push|commit|git)\s+hooks?\b§))
   (make-insecure-rule
    :id "sandbox-off" :keywords '("sandbox" "dangerously" "setenforce" "privileged")
    :regex (insecure-pattern
            #r§--no-sandbox\b§
            #r§--disable-sandbox\b§
            #r§--dangerously-[a-z-]+§
            #r§--privileged\b§
            #r§\bsetenforce\s+0\b§
            #r§\bsandbox["']?\s*[=:]\s*["']?false\b§
            #r§\b{off}sandbox(?:ing)?\b§))
   (make-insecure-rule
    :id "signature-off" :keywords '("gpg" "unauthenticated" "signature" "checksum" "integrity" "trusted-host")
    :regex (insecure-pattern
            #r§\bgpgcheck\s*=\s*0\b§
            #r§--nogpgcheck\b§
            #r§--allow-unauthenticated\b§
            #r§--trusted-host\b§
            #r§\b{off}(?:signature|checksum|integrity|gpg)\s+(?:verification|checks?|checking|validation)\b§))
   (make-insecure-rule
    :id "plaintext-secret" :keywords '("password" "secret" "key" "token" "credential")
    :regex (insecure-pattern
            #r§\b(?:store|storing|save|saving|keep|keeping|put|putting|commit|committing|hard-?code|hard-?coding)\s+(?:the\s+|my\s+|our\s+|all\s+)?(?:passwords?|secrets?|api\s*keys?|tokens?|credentials?)\s+(?:in\s+(?:plain\s*text|clear\s*text|the\s+repo(?:sitory)?|git|the\s+source|the\s+code)|unencrypted|as\s+plain\s*text)\b§
            #r§\bhard-?cod(?:e|ed|ing)\s+(?:the\s+|my\s+|our\s+)?(?:passwords?|secrets?|api\s*keys?|tokens?|credentials?)\b§))
   (make-insecure-rule
    :id "shell-injection" :keywords '("eval" "shell")
    :regex (insecure-pattern #r§\beval\s*\(§ #r§\bshell\s*=\s*true\b§))
   (make-insecure-rule
    :id "pipe-to-shell" :keywords '("curl" "wget")
    :regex (insecure-pattern #r§\b(?:curl|wget)\b[^\n|]*\|\s*(?:sudo\s+)?(?:ba|z|da)?sh\b§))
   (make-insecure-rule
    :id "weak-random" :keywords '("random" "rand" "md5" "sha1")
    :regex (insecure-pattern
            #r§\b(?:math\.random|random\.random|random\.randint|mt_rand|rand\(\))[^\n.]{0,40}?\b(?:tokens?|passwords?|secrets?|keys?|nonces?|salts?)\b§
            #r§\b(?:md5|sha1)\b[^\n.]{0,30}?\bpasswords?\b§))))

(defparameter +insecure-negation+
  (compile-regex (concatenate 'string
                              #r§\b(?:never|not|cannot|dont|avoid|avoiding|stop|stopped|forbid|forbidden|prohibit|prohibited|refuse|[a-z]+n['§
                              (string (code-char #x2019))
                              #r§]t)\b§)))

(defparameter +insecure-held-notice+ "held: weakens security" "insecure.HeldNotice.")

(defun insecure-clause-start (text end)
  "insecure.clauseStart: after the last , ; ! ? or newline before end, or the last . or : that a
space or tab follows; 0 for none."
  (loop for i from (1- end) downto 0
        do (let ((c (char text i)))
             (case c
               ((#\, #\; #\! #\? #\Newline) (return-from insecure-clause-start (1+ i)))
               ((#\. #\:) (when (and (< (1+ i) (length text)) (member (char text (1+ i)) '(#\Space #\Tab)))
                            (return-from insecure-clause-start (1+ i)))))))
  0)

(defun insecure-match (text)
  "insecure.Match: the id of the first rule with a match no negation word before it in its clause
turns into a prohibition, or nil."
  (when (and (stringp text) (plusp (length text)))
    (let ((lower (coerce (ascii-lower text) 'simple-string)))
      (dolist (rule +insecure-rules+ nil)
        (when (some (lambda (k) (search k lower)) (insecure-rule-keywords rule))
          (dolist (m (regex-find-all-index (insecure-rule-regex rule) lower))
            (unless (regex-match-p +insecure-negation+ (subseq lower (insecure-clause-start lower (aref m 0)) (aref m 0)))
              (return-from insecure-match (insecure-rule-id rule)))))))))
