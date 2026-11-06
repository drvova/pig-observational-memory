;;;; om.server: the image's socket, sessions, events and lifecycle (docs/lisp-core.md, "The
;;;; image"; docs/lisp-port-image-client.md). It reads the engine through the OM package's
;;;; internals (om::...), all of them in engine.lisp, so moving OM to the design's package map
;;;; touches one file here.

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-bsd-sockets)
  (require :sb-posix)
  (require :sb-concurrency))

(defpackage "OM.SERVER"
  (:use "COMMON-LISP")
  (:export "MAIN" "START-SERVER" "STOP-SERVER" "WAIT-SERVER" "SERVER-SOCKET-PATH"
           "DEFINE-METHOD" "DEFINE-MCP-METHOD" "DEFINE-NOT-PORTED" "NOT-PORTED" "RPC-FAIL"
           "*METHODS*" "*MCP-METHODS*" "HANDLE-LINE" "IMAGE-LOCKED"))
