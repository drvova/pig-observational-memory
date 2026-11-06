;;;; session.Entry: one branch entry, with the members the ledger reads and the decoded object.

(in-package "OM.KERNEL")

(defstruct entry
  (type "" :type string)
  (id "" :type string)
  (parent-id "" :type string)
  (timestamp "" :type string)
  ;; message is the decoded message object of a "message" entry, else nil.
  message
  content
  (custom-type "" :type string)
  summary
  data
  details
  (first-kept-id "" :type string)
  raw)

(defun entry-from-map (m)
  "session.FromMap."
  (make-entry :type (str (jget m "type"))
              :id (str (jget m "id"))
              :parent-id (str (jget m "parentId"))
              :timestamp (str (jget m "timestamp"))
              :custom-type (str (jget m "customType"))
              :first-kept-id (str (jget m "firstKeptEntryId"))
              :message (let ((msg (jget m "message"))) (and (hash-table-p msg) msg))
              :content (jget m "content")
              :summary (jget m "summary")
              :data (jget m "data")
              :details (jget m "details")
              :raw m))

(defun entry-role (e)
  (let ((msg (entry-message e))) (and msg (str (jget msg "role")))))

(defun entry-to-map (e)
  "session.Entry.ToMap: the decoded object, or one built from the fields."
  (or (entry-raw e)
      (let ((m (jobject "type" (entry-type e) "id" (entry-id e))))
        (flet ((put (k v) (setf (gethash k m) v)))
          (unless (string= (entry-timestamp e) "") (put "timestamp" (entry-timestamp e)))
          (when (entry-message e) (put "message" (entry-message e)))
          (unless (string= (entry-custom-type e) "") (put "customType" (entry-custom-type e)))
          (when (entry-data e) (put "data" (entry-data e))))
        m)))
