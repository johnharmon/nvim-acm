; acm-ls treesitter injection: parse Go-template syntax inside YAML
; block_scalar bodies under ACM-specific keys.
;
; Why this matters: YAML's grammar otherwise consumes block_scalar content
; as a single opaque token. VSCode's TextMate equivalent of this never
; reached inside block scalars, which broke bracket matching in escape
; patterns. Treesitter's injection model parses the inner content with a
; second grammar, so brackets, strings, and identifiers all get correct
; AST nodes and bracket-matching follows the actual structure.
;
; Requires: tree-sitter-go-template (or a compatible "gotmpl" parser)
; installed alongside tree-sitter-yaml. Install via :TSInstall gotmpl
; on Neovim 0.10+.

; object-templates-raw: |
;   {{- range ... }}
;   ... template body ...
;   {{- end }}
((block_mapping_pair
  key: (flow_node) @_key
  value: (block_node (block_scalar) @injection.content))
 (#any-of? @_key "object-templates-raw" "object-templates")
 (#set! injection.language "gotmpl"))

; Single-line Helm expressions inside quoted YAML scalars also benefit
; from gotmpl injection — values like:
;   replicas: '{{ .Values.replicas }}'
((block_mapping_pair
  value: (flow_node (double_quote_scalar) @injection.content))
 (#match? @injection.content "\\{\\{")
 (#set! injection.language "gotmpl"))

((block_mapping_pair
  value: (flow_node (single_quote_scalar) @injection.content))
 (#match? @injection.content "\\{\\{")
 (#set! injection.language "gotmpl"))
