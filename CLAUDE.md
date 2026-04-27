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

- **5 diagnostic rules** (`policy-name-length`, `policy-name-pattern`,
  `policy-name-template` with strict/resolve/both, `hub-forbidden-functions`,
  `lookup-default-dict`) — `lsp-server/internal/rules/`
- **Completion, hover, signature help** — layer-aware (helm/hub/managed)
  reading from ACM 2.15 catalog plus Go-builtins, sprig, helm function
  lists. `.Values.*` drilling into chart `values.yaml` with overlay
  support. `.Release` / `.Chart` / `.Files` / `.Capabilities` /
  `.Template` Helm globals served in helm context.
- **Semantic tokens** — full Go-template tokenizer for keywords,
  strings, numbers, operators, variables, properties, functions, ACM-
  distinct identifiers, exported context values. Works in helm
  expressions, hub spans (direct + double-split escaped), and
  managed spans (`{{ "{{" }} ... {{ "}}" }}`).
- **Treesitter queries** — yaml→gotmpl injection (the bracket-matching
  fix that was unreachable in the VSCode TextMate extension) plus
  highlight overlays for ACM identifiers in `helm` and `gotmpl`.
- **Highlight links** — `lua/autoshift/init.lua` registers
  `@lsp.type.<type>[.<modifier>].<lang>` → `@variable`/`@function`/etc.
  links so colorschemes that style the standard tree-sitter captures
  give us colors automatically. Re-applied on `LspAttach` and
  `ColorScheme` because Neovim's `vim.lsp.semantic_tokens` registers
  competing default-true links at attach time.

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
│       ├── rules/                 # 5 diagnostic rules
│       ├── providers/             # completion, hover, signaturehelp, semantictokens
│       └── server/server.go       # glsp wiring
├── lua/autoshift/                 # init.lua (setup), treesitter.lua (parser check)
├── plugin/autoshift.lua           # auto-load guard
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
| Change highlight links | `lua/autoshift/init.lua` `default_links` table |
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
go build -o autoshift-lsp .
go build -o smoketest ./cmd/smoketest/
./smoketest ./autoshift-lsp
```

Inside Neovim after a binary rebuild:

```vim
:AutoshiftRestart    " kill + re-attach LSP, picks up new binary
:AutoshiftStatus     " confirm PID + root_dir
:Lazy reload autoshift   " if you edited Lua
```

## Known wrinkles

- **noexec /home** — parser detection uses three probes
  (`nvim_get_runtime_file`, nvim-treesitter API, `vim.treesitter.language.add`)
  in `lua/autoshift/treesitter.lua`. Adding a fourth detection path?
  Update `parser_available` AND `diagnose()`.
- **Bracket matching** in escape patterns — solved on the Neovim side
  via treesitter injection. Same problem on the VSCode side is parked.
- **Catalog provenance** — seeded from RHACM 2.15 Governance §1.2 PDF.
  Each entry has a `source` field. ACM 2.16+ when it ships: drop a
  new `acm-2.16.json` alongside, loader auto-discovers.
- **gopls workspace warnings** when editing Go from a different repo —
  ignore them. Real check is `go build ./...` from `lsp-server/`.

## Pending / parked

- Real Neovim end-to-end testing in production usage (user just started
  using it).
- Possibly add `:AutoshiftSync` user command to push catalog changes if
  catalog editing becomes frequent.

## Don'ts

- Don't touch `~/git-projects/autoshift-plugin/` from this session.
- Don't add `Co-Authored-By: Claude` or "Generated with Claude" trailers.
- Don't commit unless explicitly asked.
- Don't rebuild the binary unless asked or the change requires it for
  the user to test.
