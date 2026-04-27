; AutoShift treesitter highlight overlay for gotmpl content.
; Layered on top of the gotmpl parser's built-in highlights. Adds
; ACM-specific identifiers (fromSecret, protect, skipObject, etc.) and
; exported context values (.ManagedClusterName, .Object, etc.) as
; @function.builtin / @variable.builtin so themes pick them out from
; ordinary helm/sprig calls.
;
; Node names assume the upstream tree-sitter-go-template grammar shape.
; If your grammar uses different node types, adjust accordingly.

; ACM-distinct hub-side functions.
((identifier) @function.builtin
 (#any-of? @function.builtin
   "fromSecret"
   "fromConfigMap"
   "fromClusterClaim"
   "lookupClusterClaim"
   "copySecretData"
   "copyConfigMapData"
   "autoindent"
   "base64enc"
   "base64dec"
   "protect"
   "toLiteral"
   "getNodesWithExactRoles"
   "hasNodesWithExactRoles"))

; Managed-only.
((identifier) @function.builtin
 (#eq? @function.builtin "skipObject"))

; ACM exported context values appear as `.foo` field accesses.
((field) @variable.builtin
 (#any-of? @variable.builtin
   "ManagedClusterName"
   "ManagedClusterLabels"
   "PolicyMetadata"
   "ObjectName"
   "ObjectNamespace"
   "Object"))
