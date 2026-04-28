# TODOS

Pending capability ideas — primarily new diagnostic rules, plus a few
adjacent items. Update **Status** when a decision is made; capture the
*why* under **Notes** so future-you doesn't re-litigate it.

Statuses: `proposed` | `decided` | `in-progress` | `done` | `skipped`.

Difficulty is a rough size estimate against the existing codebase, not a
calendar estimate:
- **low** — fits an afternoon, uses existing infrastructure
- **medium** — a day or two, may add a small new piece
- **high** — multi-day, new infrastructure or external dependencies

## Diagnostics

### Unknown function calls in templates

**Status:** done (default off — opt-in via settings)
**Difficulty:** low

**Approach:** For each call site found by walking hub / managed / helm
spans, check the function identifier against the loaded catalog
(`acm-*.json` hub/managed funcs, `helm.json`, `go-builtins.json`,
sprig set). Emit a diagnostic on the function-name range when no
catalog entry matches.

**Open questions:**
- Severity — warn or error? Default warn, configurable.
- Sprig coverage in the catalog is a subset; reporting unknowns on
  *real* sprig functions would be a regression. Either expand
  catalog coverage first or suppress when a name matches the wider
  sprig allowlist (vendor `Masterminds/sprig`'s function-name list).
- Should user-defined `define` templates count as "known"?
  Probably yes — scan for `{{ define "name" }}` in the workspace
  and add to the per-call resolver.

**Notes:**
- Implemented in `lsp-server/internal/rules/unknownFunction.go`.
  Wired through a `CatalogResolver` interface so unit tests can fake
  the catalog without touching disk.
- Defaults to **disabled** because of the sprig-subset false-positive
  risk. Settings: `rules.unknown-function.enabled` (bool),
  `rules.unknown-function.severity`, and
  `rules.unknown-function.allowedFunctions` (string list to extend
  the known set).
- Followups still open: vendoring the full sprig name list, scanning
  workspace `{{ define "x" }}` to count user templates as known.

---

### `.Values.X` paths that don't exist

**Status:** proposed
**Difficulty:** low–medium

**Approach:** A new rule that walks `.Values.<path>` references in
helm-context spans and resolves them through the existing
`internal/values/` machinery (chart `values.yaml` + overlays). When the
path doesn't exist in any merged source, emit a diagnostic on the
identifier range.

**Open questions:**
- Severity — warn (overlay may supply at deploy time) or error?
- How to handle dynamic / conditional access (`.Values.foo | default …`)?
  Suppress when the parent path resolves and the leaf is gated by a
  default fallback.
- Workspace-relative overlays vs CI-time overlays — current settings
  cover workspace-relative; CI overlays produce false positives. Maybe
  a setting `values.allowMissing = ["someKey"]` or a comment marker.

**Notes:**

---

### Unclosed `{{` / mismatched `hub` pair

**Status:** done
**Difficulty:** low

**Approach:** Pure-string scan for `{{` openers without a matching
`}}` closer in the same line/scalar, and `{{hub` openers without a
balancing `hub}}`. Emit on the dangling delimiter range.

**Open questions:**
- Multi-line statements — gotmpl allows `{{-` / `-}}` across lines;
  scope properly.
- Is this redundant once full-syntax (`text/template/parse`) lands?
  Yes, but lighter-weight, no parser dep, runs on partially-typed code
  cleanly. Probably worth keeping as the first-line check.

**Notes:**
- Implemented in `lsp-server/internal/rules/unclosedDelimiters.go`.
  Default-on with severity `error`. Two passes: balanced `{{`/`}}`
  scan, then a greedy pair-up of `{{hub` / `hub}}` markers via
  `context.FindHelmStringRanges` to skip matches inside string
  literals (so escape-form `{{ "{{hub" }}` doesn't appear orphaned).
- Settings: `rules.unclosed-delimiters.enabled`, `.severity`.
- Multi-line `{{-` / `-}}` works because the close-finder doesn't
  care about newlines. Tested across direct, escape-form, and stray-
  closer cases.

---

### Full Go-template syntax errors

**Status:** proposed
**Difficulty:** medium

**Approach:** Wrap stdlib `text/template/parse` (the parser helm itself
uses). For each block-scalar / hub-span content, feed it through
`parse.Parse(name, text, "{{", "}}", funcs...)`; map any returned
parse error's reported position back to an LSP range via byte-offset
lookup against the original document.

**Tradeoffs:**
- Catches mistakes the simpler scans can't — wrong control-flow
  nesting (`{{ if }} ... {{ end }} ... {{ end }}`), bad pipeline
  syntax, malformed actions.
- Requires registering all helm/sprig/ACM functions as no-op stubs
  in the FuncMap so the parser doesn't reject legitimate calls as
  "function X not defined". Catalog already enumerates these — wire
  it through.

**Open questions:**
- Run on whole chart or per-block-scalar? Per-scalar is simpler and
  matches our existing per-doc parsing model.
- Pre-render with values? No — that's full helm-eval (see below).
- Where does `{{hub … hub}}` fit? It's not standard Go-template
  syntax, so we'd have to either rewrite hub markers to a canonical
  form before feeding the parser, or skip hub-span content for this
  rule and let the simpler unknown-function/unclosed checks cover it.

**Notes:**

---

### Argument arity / type checking for catalog functions

**Status:** proposed
**Difficulty:** medium

**Approach:** For each catalog-known function call, parse the argument
list (small recursive-descent over the inner span), and validate
count + literal-type compatibility against the catalog entry's
`params` array. Emit on the call-arg range.

**Open questions:**
- Variadic / optional params — the catalog format may need extending.
- Type inference for non-literal args (chained `.Values.x` etc.) is
  out of scope; literal-only is enough for v1.
- Where to draw the line vs full type checking — recommend stop at
  arity + obvious literal mismatches.

**Notes:**

---

### Cross-document references

**Status:** proposed
**Difficulty:** medium–high

**Approach:** Detect references like `PlacementBinding.placementRef.name`
pointing to a `Placement` / `PlacementRule` and verify the target
exists somewhere in the workspace. Emit on the `.name` value range.

**Open questions:**
- Requires a workspace-wide index of resource kind+name+namespace.
  Adds new server state, file-watching, and `didChangeWorkspaceFolders`
  handling.
- Scope of references to check — start with the binding/placement
  pair; bindingSubjects, namespaceSelectors etc. later.
- Where does the index live? Probably alongside `valuesCache` in
  the Server struct, populated on `didOpen` and refreshed on
  `didChange` for tracked files.

**Notes:**

---

### CRD schema validation

**Status:** proposed
**Difficulty:** medium

**Approach:** Ship the OpenAPI / JSON-Schema definitions for ACM CRDs
(`Policy`, `PlacementBinding`, `Placement`, `PlacementRule`,
`ConfigurationPolicy`, `OperatorPolicy`) alongside the existing
JSON catalogs. Per-doc parse → resolve `kind` → run schema check →
emit diagnostics on offending paths.

**Open questions:**
- Source of truth for the schemas — extract from
  `stolostron/governance-policy-propagator` and similar repos, or
  generate from a live cluster's CRDs?
- Schema drift maintenance — same versioning story as the ACM
  catalog. `crd-2.15.json`, `crd-2.16.json`, …
- Library: `xeipuuv/gojsonschema` is the obvious pick; `kubeval`-
  style is heavier than we need.

**Notes:**

---

### Full helm-render evaluation

**Status:** proposed (lean toward `skipped`)
**Difficulty:** high

**Approach:** Actually render the chart through helm and report any
runtime template errors, plus surface evaluated diagnostics that
only show up against real values.

**Why probably skip:**
- Re-implementing helm's render path is enormous (subcharts,
  dependencies, full sprig, post-renderers, lookup, …).
- Shelling out to `helm template` per keystroke is slow and adds an
  external runtime dependency.
- Better surfaced as a *command* (`:AcmRenderCheck` shells out and
  publishes resulting diagnostics) than as an editor-time rule.

**Notes:**

---

## Adjacent ideas

These aren't diagnostics but came up in passing — listed so they don't
get lost.

### Session-scoped values-file chain via user commands

**Status:** proposed
**Difficulty:** low–medium

**Approach:** Mirror `helm template -f a.yaml -f b.yaml -f c.yaml`
semantics — an ordered list of values files, deep-merged left-to-right
(later files override earlier; nested maps merge recursively, scalars
replace). User commands manage the chain at runtime; the chain is
pushed to the server via `workspace/didChangeConfiguration` and
flows into `internal/values/` resolution alongside the existing
chart `values.yaml`.

Proposed commands:
- `:AcmValuesAdd <path>` — append a file to the chain
- `:AcmValuesPrepend <path>` — prepend (lower precedence than current)
- `:AcmValuesRemove <path>` — drop by path
- `:AcmValuesSet <path> [<path>...]` — replace the whole chain
- `:AcmValuesClear` — empty the chain
- `:AcmValuesList` — print current chain in evaluation order

**Open questions:**
- Path resolution — relative to cwd, the chart root, or the current
  buffer? Recommend chart root (matches helm's `-f` semantics).
- Persistence — session-only, or write to a config file
  (e.g. `.acm-values.json` next to `Chart.yaml`)? Start session-only;
  add persistence later if requested.
- Merge semantics — confirm we replicate helm's exact behavior for
  nil overrides (helm treats `key: null` as "keep parent"; some
  users expect "delete"). Test against helm output before locking in.
- Existing `acm.values.overlayFiles` setting — the user-command path
  should *replace* (not append to) the static-config list when
  active, with `:AcmValuesList` reflecting the effective set.
- Diagnostics interaction — the `.Values.X` path-miss rule should
  consult the same merged tree, not just chart values, so adding an
  overlay clears spurious diagnostics in real time.

**Notes:**

---

### `:AcmSync` user command

**Status:** parked (also noted in `CLAUDE.md`)
**Idea:** Push catalog changes to other consumers if catalog editing
becomes frequent. No concrete user pull yet — wait until it does.

### Code actions / quick fixes

**Status:** proposed
**Difficulty:** medium per fix; low to wire infrastructure
**Idea:** For diagnostics that have an obvious fix (truncate a long
policy name, swap a hub-forbidden function for the documented
alternative, fill in a missing `hub`/`hub}}` closer), expose a
`textDocument/codeAction`. Wire the capability once, then add per-rule
quick fixes incrementally.

**Notes:** Wait until at least one rule has an obvious mechanical
fix worth surfacing before adding the infrastructure.

---

## How to add a rule (recipe)

When picking one of these up, the existing flow is:

1. Create `lsp-server/internal/rules/<name>.go` modeled after the
   existing rules. Implement `ID() string` and `Run(ctx Context) []protocol.Diagnostic`.
2. Read settings via `Get` / `GetInt` / `GetStringSlice` against
   `rules.<rule-id>.*` paths — they auto-flow from the user's
   `init.lua` `settings.acm.rules.<rule-id>` tree without further
   wiring.
3. Register in `internal/server/server.go`'s rule list.
4. Add a unit test if the logic is non-trivial; the existing rules
   don't all have tests, but ones that touch `values/` or `context/`
   should.
5. Run `go test ./...` and the smoketest before opening a PR.
