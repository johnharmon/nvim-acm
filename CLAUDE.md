# nvim-acm — context for future Claude sessions

This repo is a Neovim plugin (Lua client + Go LSP server) for ACM
(Advanced Cluster Management) Helm policy templates — the same domain
as Red Hat's `stolostron/go-template-utils`. Sibling project at
`~/git-projects/autoshift-plugin` is a VSCode extension covering the
same domain; they share catalog data conceptually but are versioned
independently. **Do not edit autoshift-plugin from this session unless
the user asks.**

The user's primary editor is Neovim. They use lazy.nvim for plugin
management and run on a system with `/home` mounted noexec
(treesitter parsers live on a separate filesystem under a custom
`parser_install_dir`). They prefer terse caveman-mode communication —
fragments OK, drop pleasantries; code/commits/security write normal.
Commit messages: descriptive multi-paragraph, no Claude attribution,
no `Co-Authored-By` trailer.

## What's already done

- **7 diagnostic rules** — see `lsp-server/internal/rules/`:
  - `policy-name-length`, `policy-name-pattern`,
    `policy-name-template` (strict/resolve/both), `hub-forbidden-functions`,
    `lookup-default-dict` — name / forbidden-function / lookup checks.
  - `unclosed-delimiters` (default on, error) — state-machine pairing
    of helm `{{`/`}}` plus orphan detection across direct hub
    (`{{hub`/`hub}}`), hub-escape (`{{ "{{hub" }}` / `{{ "hub}}" }}`)
    and managed-escape (`{{ "{{" }}` / `{{ "}}" }}`) markers.
  - `unknown-function` (default off, warning) — flags identifiers
    not in the union of helm/hub/managed/sprig/Go-builtins; opt-in
    because catalog sprig coverage is a subset.
- **Completion, hover, signature help** — layer-aware (helm/hub/managed)
  reading from ACM 2.15 catalog plus Go-builtins, sprig, helm function
  lists. `.Values.*` drilling into chart `values.yaml` with overlay
  support. `.Release` / `.Chart` / `.Files` / `.Capabilities` /
  `.Template` Helm globals served in helm context.
- **Semantic tokens** — full Go-template tokenizer for keywords,
  strings, numbers, operators, variables, properties, functions, ACM-
  distinct identifiers, exported context values. Handles three layers
  of nesting cleanly:
  - Helm-level `{{ ... }}` expressions, including string literals that
    contain rendered helm expressions (the helm expression's bytes
    skip-through the surrounding string scan, so `"{{ $x }}"` emits
    one tString covering the whole literal with the helm expression's
    own classification overlaid).
  - Hub spans (direct `{{hub … hub}}` body + escape-form body
    between `{{ "{{hub" }}` and `{{ "hub}}" }}`).
  - Managed spans (`{{ "{{" }} ... {{ "}}" }}`).
  - `hub` and the inner `{{`/`}}` of escape forms are tagged with the
    `defaultLibrary` modifier, surfaced as
    `@lsp.typemod.keyword.defaultLibrary.<lang>` so colorschemes can
    distinguish ACM-side markers from go-template control keywords.
- **Treesitter queries** — yaml→gotmpl injection (the bracket-matching
  fix that was unreachable in the VSCode TextMate extension) plus
  highlight overlays for ACM identifiers in `helm` and `gotmpl`.
- **Highlight links** — `lua/acm-ls/init.lua` registers
  `@lsp.type.<type>.<lang>` and `@lsp.typemod.<type>.<modifier>.<lang>`
  links pointing at standard tree-sitter captures so colorschemes give
  us sensible colors out of the box. Re-applied on `LspAttach` and
  `ColorScheme` because Neovim's `vim.lsp.semantic_tokens` registers
  competing default-true links at attach time. Users override per-group
  via `setup{ highlights = { … } }`.

## Layout

```
nvim-acm/
├── lsp-server/                    # Go LSP, single binary
│   ├── main.go, go.mod
│   ├── catalogs/                  # acm-2.15.json, helm.json, go-builtins.json
│   ├── cmd/smoketest/             # JSON-RPC integration test
│   └── internal/
│       ├── catalog/               # types + JSON loader
│       ├── parsedoc/              # YAML kind/name + LSP range
│       ├── context/               # detector.go, hubspans.go, acmcontext.go
│       ├── values/                # chartvalues, compose, templaterender, pathparser
│       ├── rules/                 # 7 diagnostic rules
│       ├── providers/             # completion, hover, signaturehelp, semantictokens
│       └── server/server.go       # glsp wiring
├── lua/acm-ls/                    # init.lua (setup), treesitter.lua (parser check)
├── plugin/acm-ls.lua              # auto-load guard
├── queries/{yaml,gotmpl,helm}/    # treesitter overlays
└── scripts/install.sh             # build + symlink
```

## Common change recipes

| Change | Where |
|---|---|
| Add ACM hub/managed function or exported value | `lsp-server/catalogs/acm-2.15.json` |
| Add Helm chart global (`.Release.X`, `.Chart.X`) | `lsp-server/catalogs/helm.json` `contextValues` |
| Add helm/sprig/Go-builtin function | corresponding JSON in `lsp-server/catalogs/` |
| Add a diagnostic rule | `lsp-server/internal/rules/<name>.go` + register in `internal/server/server.go` |
| Tweak layer detection | `internal/context/detector.go` — has comprehensive test in `detector_test.go` |
| Tweak hub/managed span finder | `internal/context/hubspans.go` |
| Adjust semantic token classification | `internal/providers/semantictokens.go` |
| Change highlight links | `lua/acm-ls/init.lua` `default_links` table |
| Treesitter query | `queries/<lang>/<kind>.scm` |

## Build / test / iterate

```fish
# build (after editing Go code)
scripts/install.sh --build-only
# Or full reinstall:
scripts/install.sh

# unit tests
cd lsp-server && go test ./...

# integration smoketest (spawns binary, runs all 5 LSP capabilities)
cd lsp-server
go build -o acm-ls .
go build -o smoketest ./cmd/smoketest/
./smoketest ./acm-ls
```

Inside Neovim after a binary rebuild:

```vim
:AcmRestart    " kill + re-attach LSP, picks up new binary
:AcmStatus     " confirm PID + root_dir
:Lazy reload acm-ls   " if you edited Lua
```

## Known wrinkles

- **noexec /home** — parser detection uses three probes
  (`nvim_get_runtime_file`, nvim-treesitter API, `vim.treesitter.language.add`)
  in `lua/acm-ls/treesitter.lua`. Adding a fourth detection path?
  Update `parser_available` AND `diagnose()`.
- **Bracket matching** in escape patterns — solved on the Neovim side
  via treesitter injection. Same problem on the VSCode side is parked.
- **Catalog provenance** — seeded from RHACM 2.15 Governance §1.2 PDF.
  Each entry has a `source` field. ACM 2.16+ when it ships: drop a
  new `acm-2.16.json` alongside, loader auto-discovers.
- **gopls workspace warnings** when editing Go from a different repo —
  ignore them. Real check is `go build ./...` from `lsp-server/`.
- **Settings normalization** — both the Lua client and the VSCode
  extension wrap their settings under an outer `acm.*` namespace.
  `normalizeSettings` in `internal/server/server.go` strips that
  wrapper on `initialize` / `didChangeConfiguration` so rule code
  can read paths like `rules.<id>.*` and `acm.version` directly.
  Don't undo that strip — the wrapper is the client-side shape, the
  unwrapped form is the rule-side contract.

## Pending / parked

- Real Neovim end-to-end testing in production usage (user just started
  using it).
- Possibly add `:AcmSync` user command to push catalog changes if
  catalog editing becomes frequent.

## Don'ts

- Don't touch `~/git-projects/autoshift-plugin/` from this session.
- Don't add `Co-Authored-By: Claude` or "Generated with Claude" trailers.
- Don't commit unless explicitly asked.
- Don't rebuild the binary unless asked or the change requires it for
  the user to test.
