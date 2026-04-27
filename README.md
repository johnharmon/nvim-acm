# nvim-acm

LSP-based diagnostics, completion, hover, signature help, and semantic
highlighting for ACM (Advanced Cluster Management) Helm policy
templates, plus treesitter injections for accurate Go-template
tokenization inside YAML block scalars.

Sibling project to the VSCode extension at `autoshift-plugin` — both
read the same JSON catalogs at the data level. Diverged here so the
Neovim plugin can ship and version independently.

## What it gives you

| Feature | LSP method | Implementation |
|---|---|---|
| Diagnostics (5 rules) | `textDocument/publishDiagnostics` | `lsp-server/internal/rules/` |
| Completion | `textDocument/completion` | `lsp-server/internal/providers/completion.go` |
| Hover | `textDocument/hover` | `lsp-server/internal/providers/hover.go` |
| Signature help | `textDocument/signatureHelp` | `lsp-server/internal/providers/signaturehelp.go` |
| Semantic tokens | `textDocument/semanticTokens/full` | `lsp-server/internal/providers/semantictokens.go` |
| Token-level highlighting inside YAML block scalars | treesitter injection | `plugin/queries/yaml/injections.scm` |
| ACM-identifier highlighting | treesitter overlay | `plugin/queries/{helm,gotmpl}/highlights.scm` |

The five diagnostic rules: `policy-name-length`, `policy-name-pattern`,
`policy-name-template` (strict/resolve/both modes),
`hub-forbidden-functions`, `lookup-default-dict`.

## Install

The repo ships a script that builds the LSP and symlinks the Lua
plugin into Neovim's native pack path:

```fish
# from the repo root
scripts/install.sh
```

Then in `~/.config/nvim/init.lua`:

```lua
require("acm-ls").setup({
  cmd = { "/absolute/path/to/nvim-acm/lsp-server/acm-ls" },
})
```

For richer highlighting inside YAML block scalars, install the gotmpl
treesitter parser:

```vim
:TSInstall yaml gotmpl
```

Open a `.yaml` or helm-detected policy file. `:LspInfo` should list
`acm-ls` attached. `:Inspect` (Neovim 0.10+) on a hub function
shows the semantic token classification.

## Architecture

```
nvim-acm/                          # standard nvim plugin layout
├── lsp-server/                    # Go binary, single-file deploy
│   ├── main.go                    # entry — runs server over stdio
│   ├── catalogs/                  # JSON: ACM funcs + helm + go-builtins
│   ├── cmd/smoketest/             # JSON-RPC client that exercises the LSP
│   └── internal/
│       ├── catalog/               # JSON loader, types
│       ├── parsedoc/              # YAML kind/name + LSP range conversion
│       ├── context/               # layer detection (helm/hub/managed),
│       │                          #   hub-span finder, ACM-context check
│       ├── values/                # values.yaml parser, overlays merge,
│       │                          #   .Values.* path parser, mini renderer
│       ├── rules/                 # 5 diagnostic rule implementations
│       ├── providers/             # completion, hover, signature, sem-tokens
│       └── server/                # glsp wiring + stateful per-document handlers
├── lua/acm-ls/
│   ├── init.lua                   # setup() registers vim.lsp.start on FileType
│   └── treesitter.lua             # parser-availability check
├── plugin/acm-ls.lua              # auto-load guard
├── queries/                       # treesitter overlays
│   ├── yaml/injections.scm        # inject gotmpl into block_scalar content
│   ├── gotmpl/highlights.scm      # ACM-distinct funcs/values
│   └── helm/highlights.scm        # same overlay for helm filetype
└── scripts/install.sh             # build + symlink
```

### Request flow

1. **Client opens a file** → Neovim's LSP client sends
   `textDocument/didOpen`. The Lua plugin `init.lua` wires this up via
   `vim.lsp.start` on `FileType yaml,helm` autocmd.
2. **Server receives didOpen** → `internal/server/server.go` stores
   the text and calls `publishDiagnostics`, which runs every rule from
   `internal/rules/` against parsed YAML docs.
3. **Cursor request** (completion / hover / signature help) →
   `internal/providers/<x>.go` is called. It reads the resolved
   catalog (loaded once at startup), runs the layer detector to know
   if the cursor is in helm / hub / managed context, and returns
   layer-appropriate items.
4. **Semantic tokens** → on `textDocument/semanticTokens/full` the
   server walks every `{{ ... }}` expression and hub-template span,
   classifying each token (operator, keyword, function, string, …)
   into LSP delta-encoded data.
5. **Treesitter highlight overlays** run independently of the LSP —
   they're query-only files in `plugin/queries/` that nvim-treesitter
   merges with the underlying yaml/helm/gotmpl grammars.

### What the LSP server reads at startup

- **`<binary-dir>/catalogs/`** (or `$ACM_CATALOGS_DIR` if set):
  - `acm-<version>.json` — hub funcs, managed funcs, sprig subset, exported values
  - `helm.json` — helm-layer functions
  - `go-builtins.json` — Go template builtins (index, len, eq, …)
- **Per-document at runtime**: chart-local `values.yaml` (cached),
  plus any overlay files configured via
  `acm.values.overlayFiles`.

The catalog files in `lsp-server/catalogs/` are the source of truth
for this repo. The sibling VSCode extension (`autoshift-plugin`) keeps
its own copy and is updated separately.

## Configuration

`setup()` in `init.lua` accepts:

| Key | Default | Notes |
|---|---|---|
| `cmd` | `{ "acm-ls" }` | Path or argv to launch the binary. |
| `filetypes` | `{ "yaml", "helm" }` | Filetypes to attach the LSP to. |
| `root_markers` | `{ "Chart.yaml", ".git", "policies" }` | Walked upward; first match wins. |
| `semantic_tokens` | `true` | Calls `vim.lsp.semantic_tokens.start` on attach (Neovim 0.10+). |
| `warn_missing_parsers` | `true` | Notify if `gotmpl`/`yaml` treesitter parsers aren't installed. |
| `settings` | see `init.lua` | Forwarded to the server via `initializationOptions`. |

The `settings.acm.*` tree mirrors the VSCode extension's
`package.json` configuration schema, including all rule knobs:

```lua
settings = {
  acm = {
    enabled = true,
    acm = { version = "2.15" },
    rules = {
      ["policy-name-length"]      = { enabled = true, severity = "warning", maxLength = 63 },
      ["policy-name-pattern"]     = { enabled = false, pattern = "^policy-" },
      ["policy-name-template"]    = { enabled = true, mode = "strict" },  -- or "resolve"/"both"
      ["hub-forbidden-functions"] = { enabled = true, severity = "error" },
      ["lookup-default-dict"]     = { enabled = true, severity = "warning" },
    },
    values = {
      overlayFiles = {},  -- workspace-relative paths layered on top of chart values
    },
  },
}
```

## Extending the catalog

Three top-level kinds you can add to:

### Adding an ACM function

Append to the appropriate array in `lsp-server/catalogs/acm-<version>.json`.

```jsonc
"hubFunctions": [
  ...,
  {
    "name": "newFunc",
    "signature": "func newFunc(ns string, name string) (value string, err error)",
    "params": [
      { "name": "ns",   "type": "string", "description": "Namespace." },
      { "name": "name", "type": "string" }
    ],
    "returns": { "type": "string", "description": "What it returns." },
    "description": "One-paragraph summary that shows in hover and completion docs.",
    "examples": ["{{hub newFunc \"ns\" \"name\" hub}}"],
    "since": "2.15",
    "source": "RHACM 2.15 Governance §1.2.X.Y"
  }
]
```

`managedFunctions`, `sprigFunctions` use the same shape. For Helm
chart-layer functions edit `lsp-server/catalogs/helm.json`. For Go
template builtins edit `lsp-server/catalogs/go-builtins.json`.

### Adding an exported context value

Append to `hubExportedValues` (cluster-scope context) or
`managedExportedValues` (object-scope, only inside `objectDefinition`):

```jsonc
"hubExportedValues": [
  ...,
  {
    "name": ".SomeNewField",
    "type": "string",
    "description": "What this resolves to at template-evaluation time.",
    "source": "RHACM 2.15 Governance §1.2.1 Table 1.4"
  }
]
```

The leading `.` is part of the name. The completion provider strips
it for filtering and insert text so users type `.S` and the item
matches.

### Reload after editing

```fish
# from repo root
scripts/install.sh --build-only

# inside Neovim
:AcmRestart
```

## Adding a new diagnostic rule

Rules live under `lsp-server/internal/rules/`. Create
`myrule.go` modeled after the existing rules:

```go
package rules

import (
    "github.com/acm-ls/lsp-server/internal/parsedoc"
    protocol "github.com/tliron/glsp/protocol_3_16"
)

type myRule struct{}

func (myRule) ID() string { return "my-rule" }

func (myRule) Run(ctx Context) []protocol.Diagnostic {
    if !Get(ctx.Settings, "rules.my-rule.enabled", true) {
        return nil
    }
    severity := Severity(Get(ctx.Settings, "rules.my-rule.severity", string(SeverityWarning)))

    out := []protocol.Diagnostic{}
    for _, d := range ctx.Docs {
        // ... your logic ...
        sev := severity.ToLSP()
        code := protocol.IntegerOrString{Value: "my-rule"}
        source := "acm"
        out = append(out, protocol.Diagnostic{
            Range:    parsedoc.RangeFromNode(d.NameNode),
            Severity: &sev,
            Code:     &code,
            Source:   &source,
            Message:  "your message here",
        })
    }
    return out
}

var MyRule Rule = myRule{}
```

Then register it in `internal/server/server.go`'s rule list:

```go
rules: []rules.Rule{
    rules.PolicyNameLength,
    // ...
    rules.MyRule,
},
```

`Get`, `GetInt`, `GetStringSlice` in `rules/types.go` provide
dotted-path access to the user's settings tree forwarded via
`initializationOptions`. Any setting you read (`rules.my-rule.enabled`,
`rules.my-rule.severity`, etc.) becomes user-configurable in their
`init.lua` automatically — no separate registration step.

For rules that need to scan hub-template content, the helpers in
`internal/context/hubspans.go` give you span ranges. For rules that
need values resolution (like the resolve mode of
`policy-name-template`), inject `values.Cache` via a constructor —
see `NewPolicyNameTemplate` for the pattern.

## Testing

### Unit tests

```fish
cd lsp-server
go test ./...
```

Currently exercised:
- `context/`: layer detector + hub-span finder
- `values/`: path parser, template renderer, compose
- `providers/`: semantic-token vocabulary + emission

### LSP integration smoke test

```fish
cd lsp-server
go build -o acm-ls .
go build -o smoketest ./cmd/smoketest/
./smoketest ./acm-ls
```

The smoketest spawns the binary, sends `initialize` +
`textDocument/didOpen` + cursor requests against canned fixtures, and
reports pass/fail per LSP capability. Useful for verifying changes
without launching Neovim.

### Real Neovim end-to-end

After running `scripts/install-nvim.sh`, open a policy YAML in nvim:

```vim
:LspInfo                         " confirm acm-ls is attached
:lua vim.lsp.buf.hover()         " on a hub function
:lua vim.lsp.buf.completion()    " or trigger via your completion plugin
:Inspect                         " (Neovim 0.10+) shows semantic-token info
:LspLog                          " stderr from the server (parse errors, etc.)
```

## Treesitter injection — why it matters

YAML's grammar consumes `object-templates-raw: |` block-scalar
content as a single opaque token. The TextMate grammar in the VSCode
extension couldn't reach inside that span, which broke bracket
matching for escape patterns like
`{{ "{{hub-" }} ... {{ "hub}}" }}` (VSCode's pair counter saw 3 `{{`
and 3 `}}` in the line and flagged the outer ones as unmatched).

Treesitter's injection query in `plugin/queries/yaml/injections.scm`
tells the parser to re-tokenize the inner block-scalar content using
the gotmpl grammar. Bracket pairing then follows the AST: the inner
`{{` of `"{{hub-"` is a string-content character (not a bracket),
and the outer `{{`/`}}` pair correctly. No client-side workarounds
needed.

## Catalog provenance

The catalog JSON files in `lsp-server/catalogs/` were seeded from the
**Red Hat Advanced Cluster Management for Kubernetes 2.15 Governance**
documentation, specifically §1.2 Template Processing. Each entry
carries a `source` field referring back to the section that defines
it. When ACM 2.16 ships, drop a new `acm-2.16.json` file alongside —
the loader auto-discovers any `acm-*.json` and lets users select
their version via `acm.acm.version`.

A long-term goal: a CI step that regenerates these catalogs from a
fresh PDF + a checkout of `stolostron/go-template-utils`. For now,
they're hand-curated.
