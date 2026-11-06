;;;; The Lisp side of the append-only files' crash test (memory/contracts/lispappend_test.go):
;;;;   sbcl --script lisp/append-parity.lisp append <store dir> <neutral store> <model> <tag>
;;;;   sbcl --script lisp/append-parity.lisp read <store dir> <neutral store> <model>
;;;; append writes one line, tagged <tag>, to each append-only file the image shares with the Go
;;;; engine, through the image's own writers: the Go layer's manifest (a level-1 proposal
;;;; generation), the model's usage ledger, the curated usage, the floor record and the neutral
;;;; store; a file the test tore keeps its fragment as one line nothing decodes. read prints what
;;;; the image reads from each file, one line each, in the shape the Go side prints its own reads.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM.LAYER")

(defun parity-append (store neutral model tag)
  (let ((*now* (lambda () (cons (* 1790000000 1000000000) 0))))
    (with-store-lock (store)
      (om.golayer::append-propose store model :session tag)
      (record-model-locked store model (model-event-line :model model :kind "detect" :outcome tag :tokens 5 :engine "v2")))
    (append-usage store "m" "helped" :quote tag)
    (om.server::record-floor store (format nil "{\"requestedBy\":~A,\"reason\":\"r\",\"time\":\"2026-09-21T00:00:00Z\",\"op\":\"lower\"}"
                                           (om::json-string tag)))
    (om.server::append-line neutral (om::stored-line "" (om::jobject "type" "custom" "id" tag)))))

(defun parity-read (store neutral model)
  (format t "manifest:~{ ~A~}~%"
          (loop for g in (go-generations store model) for n from 1
                collect (format nil "~D:~A:~A" n (om::jget g "op") (om::str (om::jget g "session")))))
  (format t "ledger:~{ ~A~}~%" (loop for e in (model-events store model)
                                     collect (om::str (om::jget e "outcome"))))
  (format t "usage: helped=~D~%" (count-if (lambda (e) (and (equal (om::jget e "name") "m") (equal (om::jget e "event") "helped")))
                                           (read-model-events (join-path store "usage.jsonl"))))
  (format t "floor:~{ ~A~}~%" (loop for e in (read-model-events (join-path store "harness" "floor.jsonl"))
                                    collect (om::str (om::jget e "requestedBy"))))
  (format t "neutral:~{ ~A~}~%" (loop for st in (om::store-load neutral)
                                      collect (om::str (om::jget (om::stored-entry st) "id")))))

(let ((args (member-if (lambda (a) (member a '("append" "read") :test #'string=)) sb-ext:*posix-argv*)))
  (destructuring-bind (step store neutral model &optional tag) args
    (if (string= step "append")
        (parity-append store neutral model tag)
        (parity-read store neutral model))))
