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
  Default-on with severity `error`. Four state-machine passes:
  - Helm-level `{{`/`}}` (with stack-style pairing — the original
    greedy "next `}}` after this `{{`" approach silently stole
    closers from later balanced expressions to satisfy earlier
    unclosed `{{`, hiding imbalance during live editing).
  - Direct hub markers `{{hub`/`hub}}`, with string-range filtering
    via `context.FindHelmStringRanges` so the inner `{{hub` of an
    escape-form pattern doesn't false-match.
  - Hub-escape pair `{{ "{{hub" }}` / `{{ "hub}}" }}`.
  - Managed-escape pair `{{ "{{" }}` / `{{ "}}" }}`.
- Settings: `rules.unclosed-delimiters.enabled`, `.severity`.
- Multi-line `{{-` / `-}}` works because the close-finder doesn't
  care about newlines. Tested across direct, escape-form, partial-
  typing of escape patterns, and stray-closer cases.

---

### Full Go-template syntax errors

**Status:** done (helm-level only — no managed-rendered body validation)
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
- Implemented in `lsp-server/internal/rules/templateSyntax.go`.
  Per-`object-templates-raw:` block-scalar parse via
  `text/template/parse`. Default-on, severity warning.
- Stub FuncMap registers every helm/hub/managed/sprig/Go-builtin
  function name from the resolved catalog plus `hub` itself. The
  direct-form hub expression `{{hub fn args hub}}` then parses
  cleanly — `hub` reads as a no-op function call with the trailing
  `hub` as an identifier argument. Escape forms parse as ordinary
  helm actions with string-literal arguments. Mixing direct-hub +
  managed-escape + helm `{{ if }}` on a single line works because
  the parser is layer-agnostic.
- Block-scalar discovery uses regex-based line scanning: locate
  `^(\s*)object-templates-raw:\s*\|[+-]?\d*\s*$`, then walk forward
  while `indent > keyIndent` (blank lines included). Original
  indentation is preserved when handing the body to the parser so
  parse-error line numbers map directly back to document lines.
- Position mapping: parser errors are formatted
  `template: NAME:LINE: MSG`; line is body-relative. Diagnostic
  document line = `block.contentLine + (parserLine - 1)`, clamped
  to the last content line so EOF-style errors (where the parser
  reports one line past the body) still land on a visible line.
- Caveats: doesn't validate variable paths (`.Values.x` is
  opaque); doesn't validate function arity (stubs accept anything);
  doesn't validate the managed-rendered template body — that needs
  pre-render and is explicitly skipped.
- Phase A and Phase B (below) extend this to multi-layer
  validation via render-chain.

---

### Phase A — layered syntax check across helm / hub / managed (render-chain)

**Status:** done (A.1–A.4 landed; default off pending Phase B's typed-stub robustness for missing-keys)
**Difficulty:** medium

**Approach:** Three-stage parse + `Execute` chain. Each stage uses
the previous stage's rendered output as input.

1. **Helm stage:** parse the document with standard `{{` / `}}`
   delims and the helm/sprig/Go-builtin FuncMap, then `Execute`
   against a values context built from the merged `values.yaml`
   tree (existing `internal/values/` machinery — chart values
   plus overlays). Stub functions return shape-preserving
   placeholders: `hub` returns `{{hub <args> hub}}` so direct
   form survives stage 1; string-typed catalog functions return
   sentinel strings; functions whose return type would matter to
   downstream layers (`fromConfigMap` etc.) return placeholder
   values matching the declared catalog return type.
2. **Hub stage:** parse the stage-1 output with custom delims
   `{{hub` / `hub}}` and the hub FuncMap. Execute against a
   stub hub-context (with `.ManagedClusterName` etc. defaulted).
   Stage-1 output's escape-form patterns have already been
   collapsed by helm rendering into direct hub form, so the
   custom-delim parser sees them naturally. Hub stubs render
   to sentinel values for stage 3.
3. **Managed stage:** parse stage-2 output with standard
   `{{` / `}}` delims and managed FuncMap. Surface any parse
   errors as managed-layer diagnostics. No execution needed
   unless we add a stage 4 (none planned).

**Values-file integration (required, not optional):**
The helm-stage `Execute` data context must be populated from
the chart's merged values tree so accessing `.Values.someKey`
during render returns a meaningful value rather than panicking
on a missing field. Plumbing:
- Use existing `valuesCache` (already in `server.Server`).
- For each document, resolve the chart values + any user-
  configured overlay files (existing `acm.values.overlayFiles`
  setting; later, the session-scoped values-file chain TODO).
- Build a `map[string]any` data context with `.Values` set to
  the merged tree, `.Release`/`.Chart`/`.Files`/`.Capabilities`/
  `.Template` populated with sentinel maps from `helm.json`
  `contextValues`.
- Wrap unknown-key accesses in a fallback that returns a
  type-safe zero value (empty string / empty map / nil) so
  Execute never panics on a missing field.

**Position mapping across stages:**
`Execute` writes to an `io.Writer` without telling you which
parse-tree node produced which bytes. To translate a stage-3
parse error back to a position in the *original* document, each
stage must produce a source map: `outputByteRange → inputByteRange`.
We can implement this by replacing the standard `text/template`
Execute path with a custom walker that emits both bytes and
source-map entries as it traverses each `parse.Tree`. Required
for diagnostic positioning; ~2-3 days of careful work on its
own.

**Open questions:**
- `lookup` (Kubernetes `lookup` from `lookupClusterClaim` etc.) —
  return placeholder dict to keep render going. Don't actually
  hit a cluster.
- Helm's `range`/`with` over `.Values.list` — if the list is
  empty (default), the inner block doesn't execute, so its
  syntax errors are missed. Consider injecting a single-element
  default list at strategic points.
- Should we render the top-level chart manifests (incl. `Chart.yaml`
  reading) or just stage 1 against the buffer text alone? Recommend
  buffer-only for v1; full chart-aware mode later.
- Per-rule severity — recommend `template-syntax.hub` and
  `.managed` settings keys, defaulted to `warning`, so users
  can disable individual layers if false-positives bite.

**Notes:**
- Phase A is the "no-crash render of all three layers" baseline.
  Sets up infrastructure (data context, stubs, Execute path,
  source maps) that Phase B builds on.
- Doesn't catch type mismatches across layers; that's Phase B.
- **A.1 (landed):** `lsp-server/internal/rules/templateRender.go`
  exposes `renderHelmStage(body, valuesRoot, resolved)` which
  parses + Executes a block-scalar body against a stub data
  context built from chart values + helm context catalog. All
  catalog functions plus `hub` registered as no-op stubs.
  Escape forms (`{{ "{{hub" }}` etc.) collapse to direct form
  (`{{hub`) via natural string emission. Values cache is plumbed
  through `templateSyntax.cache` ready for use. Tests cover the
  collapse, sentinel data context, and values-tree conversion.
  Not yet wired into the rule's diagnostic output — that's A.2.
- **A.2 (landed):** stage 2 hub-parse on stage 1's rendered output
  via `parse.Parse(name, rendered, "{{hub", "hub}}", hubFuncs)` —
  custom multi-character delims work as-is in the stdlib parser.
  Hub FuncMap excludes helm-only and managed-only functions so
  cross-layer misuse surfaces as a "function not defined" parse
  error. Gated behind `rules.template-syntax.layered = true`
  (default off). Execute errors are silently swallowed and stage
  2 is skipped for those blocks — necessary because chained
  missing-key access (`.Values.foo.bar.baz`) panics text/template
  with nil-pointer evaluation, not gracefully recoverable until
  Phase B's typed stubs land. Position mapping is line-based
  approximation: stage 2 line N → block content line N. Works
  cleanly for escape patterns (single-line collapse); imprecise
  for multi-line `{{ if }} … {{ end }}` blocks (A.4 fixes).
- **A.3 (landed):** stage 3 managed-parse on stage 2's rendered
  output. `renderHubStage` (new) parses with `{{hub`/`hub}}` delims
  via `text/template`'s `.Delims()` API and Executes against a
  hub-side data context built from `HubExportedValues`. The output
  has hub markers replaced with stub return values; stage 3 parses
  it with standard `{{`/`}}` delims and a managed-only FuncMap
  (helm-only and hub-only functions excluded so cross-layer misuse
  surfaces). Diagnostic prefix: `managed-template parse error: …`.
  Tests cover broken managed-escape (caught), balanced managed-
  escape (no diagnostic), and a full three-layer stack with
  helm `{{ if }}{{ end }}`, hub-escape, and managed-escape on
  separate lines (no diagnostic).
- **A.4 (landed):** position mapping via substring match. When stage
  2 or stage 3 reports a parse error at line N of its rendered input,
  `mapErrLineToDocument` extracts that rendered line's text and walks
  the original block-scalar body looking for a line whose trimmed
  content contains (or is contained by) it. Match → use the original
  body's line index. No match → fall back to arithmetic
  `block.contentLine + (parserLine - 1)` (which is correct when
  helm/hub stages preserved newlines 1:1 — the case for escape
  patterns). Diagnostic message also includes the rendered line text
  (`… | rendered: <text>`) so users can verify position even when the
  match heuristic misses. Full byte-level source maps through
  text/template's Execute would require re-implementing Execute as
  a tree walker (the parser's Position info is per-AST-node, but
  Execute writes to an io.Writer without reporting which node
  produced which bytes); deferred unless A.4's heuristic proves
  insufficient in practice.

The chained-missing-keys problem (`.Values.foo.bar.baz` where
`.Values.foo` is unset → nil-pointer evaluation panic from text/
template) is real and currently the reason Execute errors are NOT
surfaced as diagnostics. `missingkey=zero` only handles map keys,
not nested traversal of nil. Phase B's typed stubs + recursive
defaulting data-context type fix this.

---

### Phase B — type-flow validation across template layers

**Status:** in-progress (B.1 + B.2 landed: missing-keys robustness + typed stubs; B.3–B.4 pending)
**Difficulty:** high

**Approach:** Promote stub functions from "return interface{}" to
typed signatures derived from the catalog's `params` and `returns`
declarations. Use `reflect.MakeFunc` to construct callable stubs
matching each declared signature. When `Execute` runs them with
mismatched-typed arguments, Go's template runtime surfaces typed
errors that we capture as diagnostics. Catches mistakes like
"`fromConfigMap` returns string, you indexed it as a dict on the
managed side".

**Pieces:**
1. Catalog → Go reflect.Type bridge. Map JSON-typed declarations
   (`"string"`, `"dict"`, `"list"`, `"int"`, `"bool"`, etc.) to
   actual Go types. Extend the catalog's type vocabulary if needed
   (currently mostly strings).
2. `reflect.MakeFunc` factory to build catalog-typed stubs.
3. Variable type inference for `.Values.*` accesses — propagate
   constraints backward from each call site. Without this, all
   `.Values` accesses are `interface{}` and the type-mismatch
   detection won't fire on user-data flows.
4. Cross-stage type continuity. Stage 1's rendered output is
   text; stage 2 sees it as text. To detect "stage 1 emitted a
   dict-shaped JSON, stage 2 wanted parseable hub syntax", encode
   per-function "rendered-output shape" rules (`fromConfigMap` →
   text-string, `lookup` → dict-or-nil, `toJson` → JSON string).

**Open questions:**
- Catalog vocabulary — current type strings are documentation-
  oriented (e.g. "string"). Phase B needs them machine-parseable.
  Either extend the existing `type` field semantics or add a
  parallel `goType` field.
- Which subset of variable type inference is worth the
  complexity — recommend only literal-derivable constraints
  (`{{ index .Values.x "k" }}` → `.Values.x` must be map-shaped).
- Severity — type errors should be `error`-severity by default
  since they'll genuinely fail at policy-eval time.

**Notes:**
- Estimated 1-2 weeks on top of Phase A's foundation.
- Significant ongoing maintenance: every catalog function needs a
  signature, every new ACM version's catalog needs type review.
- The user's motivating example: "you default to a dict here, but
  the next layer expects a string so this causes issues" is exactly
  what this phase is for.
- **B.1 (landed):** chained-missing-keys robustness. Both
  `renderHelmStage` and `renderHubStage` now walk the parsed
  template tree via `collectAccessPaths`, gather every field-
  access chain (`.Values.foo.bar.baz`, `.Release.Name`, etc.),
  and pre-populate the data context with intermediate
  `map[string]any` placeholders (and `""` sentinel leaves). Paths
  are sorted longest-first so prefix relationships resolve
  correctly — when both `.Values.x` and `.Values.x.y` are
  referenced, the longer wins, leaving `x` as a navigable map.
  Existing chart-derived values are never overwritten.
  Unblocks `template-syntax.layered = true` for typical templates
  that walk into unset values without surfacing nil-pointer
  Execute panics. Tests cover the deep-nesting case, the prefix-
  conflict case, and the chart-values-preserved case.
- **B.2 (landed):** typed stubs from catalog signatures via
  `reflect.MakeFunc`. `makeTypedStub` reads a catalog function's
  `Params` and `Returns.Type` fields, maps each to a Go
  `reflect.Type` (string → `string`, dict/map → `map[string]any`,
  list/array → `[]any`, etc.), and constructs a function value
  with the matching signature using `reflect.MakeFunc`. The stub
  returns the zero value of its declared return type. Callers
  with wrong arity or incompatible literal types surface as
  `Execute` errors which become Phase B diagnostics. Functions
  with no declared types fall back to the untyped variadic
  variant — preferable to refusing the function entirely when
  catalog metadata is incomplete. Gated behind
  `rules.template-syntax.typedStubs = true` (default off) so
  default behavior matches Phase A. `buildHelmTypedFuncs` /
  `buildHubTypedFuncs` build the per-layer FuncMaps. Variadic
  catalog params translate to Go-variadic at the trailing
  position; mid-position `optional` falls back to untyped (Go
  has no concept of optional non-trailing params).
- **B.3:** variable type inference for `.Values.*` accesses.
- **B.4:** cross-stage type continuity (rendered-output shape rules).

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

## Bugs

### Settings prefix mismatch — user rule overrides don't reach rules

**Status:** done
**Difficulty:** low

**Symptom:** The Lua client `setup{ settings = { acm = { rules = {…} } } }`
wraps rule overrides under `acm.rules.<id>.*`, and ships those as
`initializationOptions`. The server stores the entire init-options map
verbatim into `s.settings`. Each rule then reads via
`Get(ctx.Settings, "rules.<rule-id>.*", default)` — no `acm.` prefix —
so the lookups always miss and the default wins. The user can change
their `init.lua` config to whatever they want and nothing changes.

The smoketest in `lsp-server/cmd/smoketest/main.go` mirrors the same
shape (`"acm": {…rules…}`), so it's also exercising defaults rather
than the user-supplied values. Tests pass because defaults match.

**Fix options:**
1. Strip the `acm` wrapper on the server side in `initialize` and
   `didChangeConfiguration` — when init-options is `{ acm: X }` (and
   only an `acm` key), set `s.settings = X`. Cleanest, doesn't
   require touching every rule.
2. Rewrite rule paths to read `acm.rules.<id>.*`. More mechanical
   change, easier to spot in code review.

Recommend (1).

**Open questions:**
- Should we keep the wrapper present so VSCode-extension shape
  parity is preserved? VSCode users send `acm.*` via
  `workspace/configuration` too — so the wrapper is consistent
  across both clients. Strip it server-side, leave the client shape
  alone.

**Notes:**
- Fixed by adding `normalizeSettings` in
  `internal/server/server.go`, called from both `initialize` and
  `didChangeConfiguration`. When init-options is `{ acm: <map> }`,
  the inner map is stored as `s.settings`; otherwise the raw map
  is used. Backward-compatible with any future flat-shape client.
- Tests cover wrapped, flat, missing-acm, and acm-not-a-map shapes
  (`internal/server/server_test.go`).
- Rule overrides like `rules.policy-name-length.maxLength = 120`
  set via the Lua client now actually take effect.

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
   `rules.<rule-id>.*` paths. They auto-flow from the user's
   `init.lua` `settings.acm.rules.<rule-id>` tree —
   `normalizeSettings` in `internal/server/server.go` strips the
   outer `acm` wrapper so rule paths don't need the prefix.
3. Register in `internal/server/server.go`'s rule list.
4. Add a unit test if the logic is non-trivial; the existing rules
   don't all have tests, but ones that touch `values/` or `context/`
   should.
5. Run `go test ./...` and the smoketest before opening a PR.

---

## Done — landed enhancements

For reference, things that aren't really "TODOs" but shipped as
part of recent work:

- **Escape-pair pass extensions** — `unclosed-delimiters` now
  pairs `{{ "{{hub" }}` / `{{ "hub}}" }}` (hub-escape) and
  `{{ "{{" }}` / `{{ "}}" }}` (managed-escape) in addition to the
  helm-level `{{`/`}}` and direct hub markers.
- **State-machine pairing** for helm `{{`/`}}` — replaced the
  greedy-pair scanner that hid imbalance during live editing.
- **`defaultLibrary` modifier on ACM-side keywords** — the `hub`
  identifier and the inner `{{`/`}}` runs of escape patterns now
  emit with the modifier set, so colorschemes can target
  `@lsp.typemod.keyword.defaultLibrary.<lang>` distinctly from
  go-template control keywords.
- **Highlight-link group naming fix** — Neovim emits
  `@lsp.typemod.<type>.<modifier>.<lang>`, not
  `@lsp.type.<type>.<modifier>.<lang>`. Default-link table updated
  to the correct shape; previous entries for `function.defaultLibrary`
  and `property.readonly` had been dead links.
- **`highlights` config option** — per-group overrides accepted in
  `setup{}` so users can recolor any LSP semantic-token group
  without forking the link table.
- **String state across embedded helm in body** — hub/managed-span
  body tokenization now skips helm-expression byte ranges as
  transparent (rather than splitting the body into gaps), so a
  string `"{{ $x }}"` inside the body emits as one tString
  covering the whole literal. Previously `.rendered-config` inside
  `".rendered-config"` mis-classified as a tProperty because the
  surrounding string got split by an embedded helm expression.
- **`appendStringInnerDelims` skip-aware** — when a string's inner
  `{{`/`}}` is the start/end of a real helm expression, no
  `keyword.defaultLibrary` token is emitted (the helm expression
  owns those bytes). Escape-form `"{{hub-"` etc. still tag
  correctly because their inner `{{` is a literal byte inside the
  surrounding helm expression's string, not a separate helm
  expression.
- **Rename autoshift → acm-ls** — Lua module, plugin file, user
  commands (`AcmRestart`/`Stop`/`Status`), binary name, Go module
  path, env var, settings root key, diagnostic source, vim global,
  treesitter query header comments. Commit `064757c`.
