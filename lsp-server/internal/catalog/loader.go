package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

type Loader struct {
	catalogsDir  string
	helmCatalog  *HelmCatalog
	goBuiltins   *HelmCatalog
	acmCatalogs  map[string]*AcmCatalog
}

func NewLoader(catalogsDir string) *Loader {
	return &Loader{catalogsDir: catalogsDir, acmCatalogs: map[string]*AcmCatalog{}}
}

func (l *Loader) Load() error {
	helm, err := readJSON[HelmCatalog](filepath.Join(l.catalogsDir, "helm.json"))
	if err == nil {
		l.helmCatalog = helm
	}
	goBuiltins, err := readJSON[HelmCatalog](filepath.Join(l.catalogsDir, "go-builtins.json"))
	if err == nil {
		l.goBuiltins = goBuiltins
	}

	entries, err := os.ReadDir(l.catalogsDir)
	if err != nil {
		return fmt.Errorf("read catalogs dir: %w", err)
	}
	re := regexp.MustCompile(`^acm-(.+)\.json$`)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !re.MatchString(e.Name()) {
			continue
		}
		c, err := readJSON[AcmCatalog](filepath.Join(l.catalogsDir, e.Name()))
		if err != nil {
			continue
		}
		l.acmCatalogs[c.AcmVersion] = c
	}
	return nil
}

func (l *Loader) AvailableAcmVersions() []string {
	versions := make([]string, 0, len(l.acmCatalogs))
	for v := range l.acmCatalogs {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return versions
}

func (l *Loader) Resolve(version string, extras UserExtras) Resolved {
	acm, ok := l.acmCatalogs[version]
	if !ok {
		acm = l.fallback(version)
	}

	helmFuncs := []TemplateFunction{}
	helmCtxValues := []ExportedValue{}
	if l.helmCatalog != nil {
		helmFuncs = l.helmCatalog.Functions
		helmCtxValues = l.helmCatalog.ContextValues
	}
	goBuiltins := []TemplateFunction{}
	if l.goBuiltins != nil {
		goBuiltins = l.goBuiltins.Functions
	}

	return Resolved{
		AcmVersion:            acm.AcmVersion,
		HelmFunctions:         dedupeFuncs(append(helmFuncs, extras.HelmFunctions...)),
		HelmContextValues:     dedupeValues(helmCtxValues),
		HubFunctions:          dedupeFuncs(append(acm.HubFunctions, extras.HubFunctions...)),
		ManagedFunctions:      dedupeFuncs(append(acm.ManagedFunctions, extras.ManagedFunctions...)),
		SprigFunctions:        dedupeFuncs(append(acm.SprigFunctions, extras.SprigFunctions...)),
		GoBuiltins:            goBuiltins,
		HubExportedValues:     dedupeValues(append(acm.HubExportedValues, extras.HubExportedValues...)),
		ManagedExportedValues: dedupeValues(append(acm.ManagedExportedValues, extras.ManagedExportedValues...)),
	}
}

func (l *Loader) fallback(requested string) *AcmCatalog {
	versions := l.AvailableAcmVersions()
	if len(versions) == 0 {
		return &AcmCatalog{AcmVersion: requested}
	}
	return l.acmCatalogs[versions[len(versions)-1]]
}

func readJSON[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return &out, nil
}

func dedupeFuncs(items []TemplateFunction) []TemplateFunction {
	seen := map[string]int{}
	out := make([]TemplateFunction, 0, len(items))
	for _, item := range items {
		if idx, ok := seen[item.Name]; ok {
			out[idx] = item
		} else {
			seen[item.Name] = len(out)
			out = append(out, item)
		}
	}
	return out
}

func dedupeValues(items []ExportedValue) []ExportedValue {
	seen := map[string]int{}
	out := make([]ExportedValue, 0, len(items))
	for _, item := range items {
		if idx, ok := seen[item.Name]; ok {
			out[idx] = item
		} else {
			seen[item.Name] = len(out)
			out = append(out, item)
		}
	}
	return out
}
