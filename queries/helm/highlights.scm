; acm-ls treesitter highlight overlay for helm files.
; The helm tree-sitter grammar already handles Helm/Go-template syntax
; at the chart level. This overlay surfaces ACM-specific identifiers and
; exported context values that the helm grammar treats as ordinary names.
;
; Inside object-templates-raw blocks the YAML injection (queries/yaml/
; injections.scm) hands content off to the gotmpl parser, where the
; gotmpl/highlights.scm queries handle ACM identifiers. This file covers
; ACM patterns that appear at the chart level (fromClusterClaim used in
; chart values, etc.) and the keyword scoping for hub markers.

; ACM-distinct hub functions when used inside `{{hub ... hub}}` directly.
((function_call
  function: (identifier) @function.builtin)
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
   "hasNodesWithExactRoles"
   "skipObject"))

; ACM exported context values, e.g. {{ .ManagedClusterName }}.
((selector_expression
  field: (field_identifier) @variable.builtin)
 (#any-of? @variable.builtin
   "ManagedClusterName"
   "ManagedClusterLabels"
   "PolicyMetadata"
   "ObjectName"
   "ObjectNamespace"
   "Object"))

; The literal word `hub` in {{hub ... hub}} markers — keyword color.
; The helm grammar may emit this as identifier or its own node; both
; alternatives are listed so at least one matches.
((identifier) @keyword
 (#eq? @keyword "hub"))
