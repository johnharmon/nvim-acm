package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/acm-ls/lsp-server/internal/server"
	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
)

func main() {
	commonlog.Configure(1, nil)

	catalogsDir := os.Getenv("ACM_CATALOGS_DIR")
	if catalogsDir == "" {
		exe, err := os.Executable()
		if err == nil {
			catalogsDir = filepath.Join(filepath.Dir(exe), "catalogs")
		}
	}
	s := server.New(server.Config{CatalogsDir: catalogsDir})
	if err := s.RunStdio(); err != nil {
		fmt.Fprintln(os.Stderr, "acm-ls:", err)
		os.Exit(1)
	}
}
