package rules

import (
	"bytes"
	"strconv"
	"strings"
	"text/template"

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
	var buf bytes.Buffer
	data := buildHelmDataContext(valuesRoot, resolved)
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
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, dataCtx); err != nil {
		return buf.String(), nil, err
	}
	return buf.String(), nil, nil
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
