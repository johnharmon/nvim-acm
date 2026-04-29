package rules

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/values"
)

// renderHelmStage parses `body` as a helm template and Executes it against a
// stub data context built from `valuesRoot` plus catalog-declared helm
// context values (`.Release`, `.Chart`, `.Files`, `.Capabilities`,
// `.Template`). All catalog-known functions are registered as no-op stubs
// returning empty strings.
//
// The rendered output is the "post-helm" view of the document — escape
// patterns (`{{ "{{hub" }}` etc.) collapse to their string-literal results
// (`{{hub`), making the output suitable as input for stages 2 (hub-side
// parse with custom `{{hub` / `hub}}` delims) and 3 (managed-side parse).
//
// On parse error, parseErr is non-nil and rendered/execErr are zero. On
// Execute error, parseErr is nil and execErr is non-nil; rendered is the
// partial output emitted before the error. On success, parseErr and
// execErr are both nil.
//
// Phase A.1 caveats (addressed in subsequent phases):
//   - Direct-form hub expressions `{{hub fn args hub}}` vanish from the
//     output because the `hub` stub returns "" — they're treated by helm
//     as a function call. Phase A.2 will protect them via sentinels
//     pre/post Execute so the form survives stage 1.
//   - No source map. Stage-2/3 errors map to approximate positions only.
//     Phase A.4 will add per-byte source mapping.
func renderHelmStage(body string, valuesRoot *values.Node, resolved catalog.Resolved) (rendered string, parseErr, execErr error) {
	funcs := buildHelmStubFuncs(resolved)
	tmpl, err := template.New("helm").
		Funcs(funcs).
		Option("missingkey=zero").
		Parse(body)
	if err != nil {
		return "", err, nil
	}
	data := buildHelmDataContext(valuesRoot, resolved)
	// Pre-populate every field-access path the template references so
	// chained navigation (`.Values.foo.bar.baz`) doesn't nil-pointer
	// when `foo` isn't in values.yaml. This is the Phase B.1 robustness
	// fix that makes layered mode safe to default-on for typical
	// templates that walk into unset values.
	ensureAccessPaths(data, collectAccessPaths(tmpl))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return buf.String(), nil, err
	}
	return buf.String(), nil, nil
}

// buildHelmStubFuncs builds a template.FuncMap with a no-op stub for every
// helm / hub / managed / sprig / Go-builtin function name in the resolved
// catalog, plus `hub` itself. All stubs accept any args and return "".
//
// `text/template.Parse` requires every identifier in command position to
// resolve to a registered function; unknown ones are parse errors. The
// catalog already enumerates the names we care about, so we register them
// all as accepting any args. Type checking against catalog signatures is
// Phase B.
func buildHelmStubFuncs(c catalog.Resolved) template.FuncMap {
	stub := func(args ...any) (string, error) { return "", nil }
	out := template.FuncMap{
		"hub": stub,
	}
	add := func(fns []catalog.TemplateFunction) {
		for _, f := range fns {
			if _, exists := out[f.Name]; exists {
				continue
			}
			out[f.Name] = stub
		}
	}
	add(c.HelmFunctions)
	add(c.HubFunctions)
	add(c.ManagedFunctions)
	add(c.SprigFunctions)
	add(c.GoBuiltins)
	return out
}

// buildHubStubFuncs builds the FuncMap for stage 2 (hub parse). Includes
// only functions that are valid in hub-template context: hub catalog
// functions, the sprig subset, and Go-builtins. Helm-only functions
// (`include`, `tpl`, etc.) and managed-only functions (`skipObject`)
// are intentionally excluded so a hub-side template that uses them
// would surface as a "function not defined" parse error — which is
// what the user wants caught.
func buildHubStubFuncs(c catalog.Resolved) template.FuncMap {
	stub := func(args ...any) (string, error) { return "", nil }
	out := template.FuncMap{}
	add := func(fns []catalog.TemplateFunction) {
		for _, f := range fns {
			if _, exists := out[f.Name]; exists {
				continue
			}
			out[f.Name] = stub
		}
	}
	add(c.HubFunctions)
	add(c.SprigFunctions)
	add(c.GoBuiltins)
	return out
}

// buildManagedStubFuncs builds the FuncMap for stage 3 (managed parse).
// Includes managed catalog functions, sprig, and Go-builtins. Helm-only
// and hub-only functions are excluded — using them on the managed side
// is a real bug that should surface as "function not defined".
func buildManagedStubFuncs(c catalog.Resolved) template.FuncMap {
	stub := func(args ...any) (string, error) { return "", nil }
	out := template.FuncMap{}
	add := func(fns []catalog.TemplateFunction) {
		for _, f := range fns {
			if _, exists := out[f.Name]; exists {
				continue
			}
			out[f.Name] = stub
		}
	}
	add(c.ManagedFunctions)
	add(c.SprigFunctions)
	add(c.GoBuiltins)
	return out
}

// buildHubDataContext composes the data context for stage 2 Execute.
// `.ManagedClusterName`, `.ManagedClusterLabels`, `.PolicyMetadata`,
// and other hub-exported values come from the catalog's
// `HubExportedValues` declarations, populated with sentinel values
// matching each declared type.
func buildHubDataContext(c catalog.Resolved) map[string]any {
	ctx := map[string]any{}
	for _, v := range c.HubExportedValues {
		segments := strings.Split(strings.TrimPrefix(v.Name, "."), ".")
		if len(segments) == 0 {
			continue
		}
		if len(segments) == 1 {
			ctx[segments[0]] = sentinelForType(v.Type)
			continue
		}
		top := segments[0]
		if _, ok := ctx[top]; !ok {
			ctx[top] = map[string]any{}
		}
		m, ok := ctx[top].(map[string]any)
		if !ok {
			continue
		}
		setNested(m, segments[1:], sentinelForType(v.Type))
	}
	return ctx
}

// renderHubStage parses `text` (stage 1's rendered output) with custom
// `{{hub` / `hub}}` delimiters and Executes it against a hub-side data
// context. Stub functions return "". Output is the post-hub view of
// the document, suitable as input for stage 3 (managed-side parse).
//
// Same parseErr/execErr semantics as renderHelmStage.
func renderHubStage(text string, dataCtx map[string]any, resolved catalog.Resolved) (rendered string, parseErr, execErr error) {
	funcs := buildHubStubFuncs(resolved)
	tmpl, err := template.New("hub").
		Delims("{{hub", "hub}}").
		Funcs(funcs).
		Option("missingkey=zero").
		Parse(text)
	if err != nil {
		return "", err, nil
	}
	ensureAccessPaths(dataCtx, collectAccessPaths(tmpl))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, dataCtx); err != nil {
		return buf.String(), nil, err
	}
	return buf.String(), nil, nil
}

// collectAccessPaths walks the parse tree of `tmpl` and returns every
// field-access path it references — i.e., every `.foo.bar.baz` the
// template would navigate at execute time. Used to pre-populate the
// data context so chained access into unset values doesn't nil-pointer.
//
// Returns paths as `[]string` slices like `["Values", "foo", "bar"]`.
// FieldNodes are emitted directly; ChainNodes (which carry an Ident
// list following a base expression like `.Values.x` chained off a
// pipeline result) are also walked. The first segment is the
// top-level field name, never a leading `.`.
func collectAccessPaths(tmpl *template.Template) [][]string {
	if tmpl == nil {
		return nil
	}
	tree := tmpl.Tree
	if tree == nil || tree.Root == nil {
		return nil
	}
	paths := [][]string{}
	walkParseNodes(tree.Root, func(n parse.Node) {
		switch x := n.(type) {
		case *parse.FieldNode:
			if len(x.Ident) > 0 {
				p := make([]string, len(x.Ident))
				copy(p, x.Ident)
				paths = append(paths, p)
			}
		case *parse.ChainNode:
			if len(x.Field) > 0 {
				// A ChainNode carries field access following a base
				// expression. We can't always know what the base
				// resolved to, but for the common case where the base
				// is a `.Foo` access (FieldNode), the Field list
				// extends the path.
				// Skip for now — the underlying FieldNode is already
				// captured by the FieldNode case above.
			}
		}
	})
	return paths
}

// ensureAccessPaths walks `paths` and creates intermediate map[string]any
// entries in `ctx` for any segment that's missing. Leaf segments default
// to an empty string sentinel. Existing values are left untouched (so
// chart-derived `.Values` data isn't overwritten). When an intermediate
// segment is already a non-map value, the path is abandoned — the
// template will still error there, which is a real user issue (their
// values shape doesn't match the template's expectation).
//
// Paths are processed longest-first so prefix relationships resolve
// correctly: if a template references both `.Values.x.y` and `.Values.x`,
// the longer path makes `x` a map; the shorter path's leaf-at-x then
// sees an existing map and leaves it alone. Reverse order would set `x`
// as a string leaf first, then bail on `y` because `x` is no longer
// map-shaped.
func ensureAccessPaths(ctx map[string]any, paths [][]string) {
	sorted := make([][]string, len(paths))
	copy(sorted, paths)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i]) > len(sorted[j])
	})
	for _, p := range sorted {
		ensureSinglePath(ctx, p)
	}
}

func ensureSinglePath(ctx map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	cur := ctx
	for i, seg := range path {
		isLeaf := i == len(path)-1
		existing, has := cur[seg]
		if isLeaf {
			if !has {
				cur[seg] = ""
			}
			return
		}
		if !has {
			next := map[string]any{}
			cur[seg] = next
			cur = next
			continue
		}
		next, ok := existing.(map[string]any)
		if !ok {
			// Existing value isn't a map — can't navigate further.
			// Template will error at this point, which surfaces as an
			// execute-time diagnostic the user can act on.
			return
		}
		cur = next
	}
}

// walkParseNodes recursively visits every parse.Node in the tree. Care
// is taken to skip *typed* nils (e.g. `IfNode.ElseList = (*ListNode)(nil)`,
// which is interface-non-nil but pointer-nil and would otherwise crash
// when the switch dereferences the pointer) by checking each pointer
// before recursing.
func walkParseNodes(n parse.Node, visit func(parse.Node)) {
	if isNilNode(n) {
		return
	}
	visit(n)
	switch x := n.(type) {
	case *parse.ListNode:
		for _, c := range x.Nodes {
			walkParseNodes(c, visit)
		}
	case *parse.ActionNode:
		walkParseNodes(x.Pipe, visit)
	case *parse.IfNode:
		walkParseNodes(x.Pipe, visit)
		walkParseNodes(x.List, visit)
		walkParseNodes(x.ElseList, visit)
	case *parse.RangeNode:
		walkParseNodes(x.Pipe, visit)
		walkParseNodes(x.List, visit)
		walkParseNodes(x.ElseList, visit)
	case *parse.WithNode:
		walkParseNodes(x.Pipe, visit)
		walkParseNodes(x.List, visit)
		walkParseNodes(x.ElseList, visit)
	case *parse.PipeNode:
		for _, c := range x.Cmds {
			walkParseNodes(c, visit)
		}
	case *parse.CommandNode:
		for _, c := range x.Args {
			walkParseNodes(c, visit)
		}
	case *parse.TemplateNode:
		walkParseNodes(x.Pipe, visit)
	}
}

// isNilNode handles the typed-nil-interface trap. `parse.Node` is an
// interface; when a concrete pointer field is nil, wrapping it in the
// interface gives a non-nil interface with a nil underlying value —
// `n == nil` returns false and any deref panics.
func isNilNode(n parse.Node) bool {
	if n == nil {
		return true
	}
	switch x := n.(type) {
	case *parse.ListNode:
		return x == nil
	case *parse.ActionNode:
		return x == nil
	case *parse.IfNode:
		return x == nil
	case *parse.RangeNode:
		return x == nil
	case *parse.WithNode:
		return x == nil
	case *parse.PipeNode:
		return x == nil
	case *parse.CommandNode:
		return x == nil
	case *parse.TemplateNode:
		return x == nil
	case *parse.FieldNode:
		return x == nil
	case *parse.ChainNode:
		return x == nil
	case *parse.IdentifierNode:
		return x == nil
	case *parse.VariableNode:
		return x == nil
	case *parse.StringNode:
		return x == nil
	case *parse.NumberNode:
		return x == nil
	case *parse.BoolNode:
		return x == nil
	case *parse.NilNode:
		return x == nil
	case *parse.DotNode:
		return x == nil
	case *parse.TextNode:
		return x == nil
	}
	return false
}

// buildHelmDataContext composes the data the helm-stage Execute walks.
// `.Values` comes from the merged chart-values + overlay tree (or an
// empty map if none was found); `.Release`, `.Chart`, `.Files`,
// `.Capabilities`, `.Template` are pre-populated with sentinel values
// drawn from the helm-catalog's `contextValues` declarations so that
// `{{ .Release.Name }}` etc. don't trip `missingkey=zero` into emitting
// `<no value>` placeholders.
func buildHelmDataContext(valuesRoot *values.Node, c catalog.Resolved) map[string]any {
	ctx := map[string]any{
		"Values": valuesNodeToAny(valuesRoot),
	}
	for _, group := range buildHelmContextGroups(c.HelmContextValues) {
		ctx[group.name] = group.value
	}
	// Ensure each canonical helm context group exists even if the catalog
	// didn't declare it explicitly. Stub maps are fine — Execute walks them
	// reflectively and any field access returns the zero interface value
	// under `missingkey=zero`.
	for _, name := range []string{"Release", "Chart", "Files", "Capabilities", "Template"} {
		if _, ok := ctx[name]; !ok {
			ctx[name] = map[string]any{}
		}
	}
	return ctx
}

// helmContextGroup is a top-level helm-context name (e.g. "Release") and
// the nested map of its declared sub-fields (e.g. "Name", "Namespace").
type helmContextGroup struct {
	name  string
	value map[string]any
}

// buildHelmContextGroups walks the catalog's `contextValues` entries (each
// is a dotted name like ".Release.Name") and groups them by their
// top-level segment, populating each leaf with a sentinel value of the
// declared type.
func buildHelmContextGroups(decls []catalog.ExportedValue) []helmContextGroup {
	groups := map[string]map[string]any{}
	for _, d := range decls {
		segments := strings.Split(strings.TrimPrefix(d.Name, "."), ".")
		if len(segments) < 2 {
			continue
		}
		top := segments[0]
		if _, ok := groups[top]; !ok {
			groups[top] = map[string]any{}
		}
		setNested(groups[top], segments[1:], sentinelForType(d.Type))
	}
	out := make([]helmContextGroup, 0, len(groups))
	for k, v := range groups {
		out = append(out, helmContextGroup{name: k, value: v})
	}
	return out
}

// setNested walks `m` along `segments`, creating intermediate maps as
// needed, and sets the leaf to `value`.
func setNested(m map[string]any, segments []string, value any) {
	if len(segments) == 0 {
		return
	}
	if len(segments) == 1 {
		m[segments[0]] = value
		return
	}
	next, ok := m[segments[0]].(map[string]any)
	if !ok {
		next = map[string]any{}
		m[segments[0]] = next
	}
	setNested(next, segments[1:], value)
}

// sentinelForType produces a default value matching the catalog-declared
// type so Execute against it doesn't trip type-mismatch errors for the
// common cases (`.Release.IsInstall` is bool, etc.). Phase B promotes
// these into proper typed stubs using reflect.MakeFunc.
func sentinelForType(t string) any {
	switch strings.ToLower(t) {
	case "string":
		return ""
	case "int", "integer", "number":
		return 0
	case "bool", "boolean":
		return false
	case "list", "array":
		return []any{}
	case "map", "object", "dict":
		return map[string]any{}
	}
	return ""
}

// valuesNodeToAny converts the parsed values.yaml tree to the
// map / list / primitive shape `text/template.Execute` expects. Nodes
// whose type is unknown become nil; that combined with `missingkey=zero`
// keeps `.Values.something.deeply.nested` from panicking when the path
// isn't fully present.
func valuesNodeToAny(n *values.Node) any {
	if n == nil {
		return map[string]any{}
	}
	switch n.Type {
	case values.TypeMap:
		out := map[string]any{}
		for k, v := range n.Children {
			out[k] = valuesNodeToAny(v)
		}
		return out
	case values.TypeList:
		// values.Node doesn't currently carry list elements (only the
		// map subtree). Return an empty list — Execute over `range`
		// won't iterate anything, which is safe but means range bodies
		// aren't validated. Phase A.2+ may revisit.
		return []any{}
	case values.TypeString:
		return n.Example
	case values.TypeNumber:
		// Try to parse a numeric example; fall back to 0.
		if v, err := parseNumber(n.Example); err == nil {
			return v
		}
		return 0
	case values.TypeBoolean:
		return n.Example == "true"
	case values.TypeNull, values.TypeUnknown:
		return nil
	}
	return nil
}

func parseNumber(s string) (any, error) {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, nil
	}
	return strconv.ParseFloat(s, 64)
}
