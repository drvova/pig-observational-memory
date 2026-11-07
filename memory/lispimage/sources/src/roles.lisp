;;;; serialize.EscapeRoleLabels (memory/serialize/roles.go, contracts/secrets.md "Role labels"):
;;;; "quoted " goes before every role-like label in tool text the level-1 request shows, so a
;;;; forged [User]: line never reads as a turn. The labels are matched over the ASCII-lowercased
;;;; text; each match's group 1 ends where the quote goes in.

(in-package "OM.BASE")

(defparameter +role-quote+ "quoted ")

(defparameter +role-labels+
  (list (compile-regex #r§(\[[ \t]*)(?:user|human|assistant|system|developer|source entry id|branch summary|tool result)\b§)
        (compile-regex #r§(\[[ \t]*)(?:bash|custom)[ \t]+[@(]§)
        (compile-regex #r§(</?\|?)(?:user|human|assistant|system|developer|im_start|im_end)\b§)
        (compile-regex #r§(?m)^([ \t]*(?:>[ \t]*)*)(?:#{1,6}[ \t]*(?:\*\*)?(?:user|human|assistant|system|developer)(?:\*\*)?[ \t]*(?::|$)|(?:\*\*)?(?:user|human|assistant|system|developer)(?:\*\*)?[ \t]*:)§)))

(defun escape-role-labels (text)
  (let ((lower (ascii-lower text)))
    (if (not (find-if (lambda (c) (find c "[<:#")) lower))
        text
        (let ((points '()))
          (dolist (re +role-labels+)
            (dolist (m (regex-find-all-index re lower))
              (pushnew (aref m 3) points)))
          (if (null points)
              text
              (with-output-to-string (o)
                (let ((last 0))
                  (dolist (p (sort points #'<))
                    (write-string text o :start last :end p)
                    (write-string +role-quote+ o)
                    (setf last p))
                  (write-string text o :start last))))))))
