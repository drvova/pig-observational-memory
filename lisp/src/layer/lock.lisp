;;;; Last of W4's files: OM.TOOL is locked once its API is defined, so model code can neither
;;;; redefine nor rebind its functions (an flet of om.tool:notes is a package-lock violation).

(in-package "OM.LAYER")

(sb-ext:lock-package "OM.TOOL")
