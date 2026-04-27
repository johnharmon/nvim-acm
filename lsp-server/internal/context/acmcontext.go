package context

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/acm-ls/lsp-server/internal/parsedoc"
)

// ACMKinds is the set of Kubernetes kinds that signal an ACM-policy file.
var ACMKinds = map[string]bool{
	"Policy":              true,
	"PlacementBinding":    true,
	"PlacementRule":       true,
	"Placement":           true,
	"ConfigurationPolicy": true,
	"OperatorPolicy":      true,
	"PolicySet":           true,
}

var hubTemplatePattern = regexp.MustCompile(`\{\{-?\s*"\{\{hub|\{\{-?\s*hub\b`)

// IsAcmContextFromDocs reports whether any of the parsed docs declares an ACM kind.
func IsAcmContextFromDocs(docs []parsedoc.ParsedDoc) bool {
	for _, d := range docs {
		if ACMKinds[d.Kind] {
			return true
		}
	}
	return false
}

// IsAcmContextForFile is the broader heuristic: in addition to the per-file
// signal, scan sibling files in the same directory for ACM kinds or hub-template
// syntax so that a file in an ACM chart doesn't escape detection just because
// it doesn't itself contain a Policy.
//
// uri is the LSP document URI (`file://...`); docsHadAcm short-circuits when
// the per-file check already fired.
func IsAcmContextForFile(uri string, docsHadAcm bool) bool {
	if docsHadAcm {
		return true
	}
	filePath, ok := uriToPath(uri)
	if !ok {
		return false
	}
	dir := filepath.Dir(filePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	base := filepath.Base(filePath)
	for _, e := range entries {
		if e.IsDir() || e.Name() == base {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		if siblingHasAcmSignal(filepath.Join(dir, e.Name())) {
			return true
		}
	}
	return false
}

func siblingHasAcmSignal(filePath string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	if hubTemplatePattern.Match(data) {
		return true
	}
	docs := parsedoc.ParseAll(string(data))
	for _, d := range docs {
		if ACMKinds[d.Kind] {
			return true
		}
	}
	return false
}

func uriToPath(rawURI string) (string, bool) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return "", false
	}
	if u.Scheme != "file" {
		return "", false
	}
	return u.Path, true
}
