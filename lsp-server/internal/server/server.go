package server

import (
	"path/filepath"
	"sync"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/parsedoc"
	"github.com/acm-ls/lsp-server/internal/providers"
	"github.com/acm-ls/lsp-server/internal/rules"
	"github.com/acm-ls/lsp-server/internal/values"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
)

const Name = "acm-ls"
const Version = "0.1.0"

// Server is the acm-ls LSP server.
type Server struct {
	handler   *protocol.Handler
	rpcServer *server.Server

	mu          sync.Mutex
	documents   map[protocol.DocumentUri]string
	settings    rules.Settings
	rules       []rules.Rule
	valuesCache *values.Cache
	loader      *catalog.Loader
	catalogsDir string
}

// Config customizes a Server.
type Config struct {
	CatalogsDir string
}

// New constructs a Server with the given config. CatalogsDir defaults to
// `<binary-dir>/catalogs` when empty.
func New(cfg Config) *Server {
	cache := values.NewCache()
	catalogsDir := cfg.CatalogsDir
	if catalogsDir == "" {
		catalogsDir = filepath.Join("catalogs")
	}
	loader := catalog.NewLoader(catalogsDir)
	if err := loader.Load(); err != nil {
		// Non-fatal; rules and providers degrade gracefully.
	}
	s := &Server{
		documents:   map[protocol.DocumentUri]string{},
		settings:    rules.Settings{},
		valuesCache: cache,
		loader:      loader,
		catalogsDir: catalogsDir,
		rules: []rules.Rule{
			rules.PolicyNameLength,
			rules.PolicyNamePattern,
			rules.NewPolicyNameTemplate(cache),
			rules.HubForbiddenFunctions,
			rules.LookupDefaultDict,
			rules.UnclosedDelimiters,
			rules.NewUnknownFunction(loader),
		},
	}
	h := &protocol.Handler{
		Initialize:                      s.initialize,
		Initialized:                     s.initialized,
		Shutdown:                        s.shutdown,
		SetTrace:                        s.setTrace,
		TextDocumentDidOpen:             s.didOpen,
		TextDocumentDidChange:           s.didChange,
		TextDocumentDidClose:            s.didClose,
		WorkspaceDidChangeConfiguration: s.didChangeConfiguration,
		TextDocumentCompletion:          s.completion,
		TextDocumentHover:               s.hover,
		TextDocumentSignatureHelp:       s.signatureHelp,
		TextDocumentSemanticTokensFull:  s.semanticTokensFull,
	}
	s.handler = h
	s.rpcServer = server.NewServer(h, Name, false)
	return s
}

func (s *Server) RunStdio() error {
	return s.rpcServer.RunStdio()
}

func (s *Server) initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	if params.InitializationOptions != nil {
		if m, ok := params.InitializationOptions.(map[string]any); ok {
			s.mu.Lock()
			s.settings = rules.Settings(m)
			s.mu.Unlock()
		}
	}
	syncKind := protocol.TextDocumentSyncKindFull
	completionTrigger := []string{" ", ".", "$"}
	signatureTrigger := []string{" ", "(", "\""}
	semanticTokensFull := true
	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: &syncKind,
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: completionTrigger,
			},
			HoverProvider: true,
			SignatureHelpProvider: &protocol.SignatureHelpOptions{
				TriggerCharacters: signatureTrigger,
			},
			SemanticTokensProvider: protocol.SemanticTokensOptions{
				Legend: providers.Legend(),
				Full:   &semanticTokensFull,
			},
		},
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    Name,
			Version: stringPtr(Version),
		},
	}, nil
}

func (s *Server) initialized(ctx *glsp.Context, _ *protocol.InitializedParams) error { return nil }
func (s *Server) shutdown(ctx *glsp.Context) error                                   { return nil }
func (s *Server) setTrace(ctx *glsp.Context, _ *protocol.SetTraceParams) error       { return nil }

func (s *Server) didOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.mu.Lock()
	s.documents[params.TextDocument.URI] = params.TextDocument.Text
	s.mu.Unlock()
	s.publishDiagnostics(ctx, params.TextDocument.URI)
	return nil
}

func (s *Server) didChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	s.mu.Lock()
	for _, change := range params.ContentChanges {
		if full, ok := change.(protocol.TextDocumentContentChangeEventWhole); ok {
			s.documents[params.TextDocument.URI] = full.Text
			continue
		}
		if structured, ok := change.(protocol.TextDocumentContentChangeEvent); ok {
			s.documents[params.TextDocument.URI] = structured.Text
		}
	}
	s.mu.Unlock()
	s.publishDiagnostics(ctx, params.TextDocument.URI)
	return nil
}

func (s *Server) didClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	s.mu.Lock()
	delete(s.documents, params.TextDocument.URI)
	s.mu.Unlock()
	ctx.Notify("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{
		URI:         params.TextDocument.URI,
		Diagnostics: []protocol.Diagnostic{},
	})
	return nil
}

func (s *Server) didChangeConfiguration(ctx *glsp.Context, params *protocol.DidChangeConfigurationParams) error {
	if params.Settings != nil {
		if m, ok := params.Settings.(map[string]any); ok {
			s.mu.Lock()
			s.settings = rules.Settings(m)
			s.mu.Unlock()
			for uri := range s.documents {
				s.publishDiagnostics(ctx, uri)
			}
		}
	}
	return nil
}

func (s *Server) completion(ctx *glsp.Context, params *protocol.CompletionParams) (any, error) {
	text, fpath, settings := s.docState(params.TextDocument.URI)
	resolved := s.resolveCatalog(settings)
	items := providers.Provide(providers.CompletionInput{
		URI:         string(params.TextDocument.URI),
		FilePath:    fpath,
		Text:        text,
		Position:    params.Position,
		Catalog:     resolved,
		ValuesCache: s.valuesCache,
	})
	return items, nil
}

func (s *Server) hover(ctx *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	text, fpath, settings := s.docState(params.TextDocument.URI)
	resolved := s.resolveCatalog(settings)
	return providers.Hover(providers.HoverInput{
		URI:         string(params.TextDocument.URI),
		FilePath:    fpath,
		Text:        text,
		Position:    params.Position,
		Catalog:     resolved,
		ValuesCache: s.valuesCache,
	}), nil
}

func (s *Server) semanticTokensFull(ctx *glsp.Context, params *protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	text, _, settings := s.docState(params.TextDocument.URI)
	resolved := s.resolveCatalog(settings)
	return providers.SemanticTokens(providers.SemanticTokensInput{
		Text:    text,
		Catalog: resolved,
	}), nil
}

func (s *Server) signatureHelp(ctx *glsp.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	text, _, settings := s.docState(params.TextDocument.URI)
	resolved := s.resolveCatalog(settings)
	return providers.SignatureHelp(providers.SignatureHelpInput{
		Text:     text,
		Position: params.Position,
		Catalog:  resolved,
	}), nil
}

func (s *Server) docState(uri protocol.DocumentUri) (string, string, rules.Settings) {
	s.mu.Lock()
	text := s.documents[uri]
	settings := s.settings
	s.mu.Unlock()
	fpath := values.URIToPath(string(uri))
	return text, fpath, settings
}

func (s *Server) resolveCatalog(settings rules.Settings) catalog.Resolved {
	version := rules.Get(settings, "acm.version", "2.15")
	return s.loader.Resolve(version, catalog.UserExtras{})
}

func (s *Server) publishDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri) {
	s.mu.Lock()
	text := s.documents[uri]
	settings := s.settings
	s.mu.Unlock()

	docs := parsedoc.ParseAll(text)
	rctx := rules.Context{
		URI:      string(uri),
		FilePath: values.URIToPath(string(uri)),
		Text:     text,
		Docs:     docs,
		Settings: settings,
	}

	diags := []protocol.Diagnostic{}
	for _, r := range s.rules {
		diags = append(diags, r.Run(rctx)...)
	}
	ctx.Notify("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diags,
	})
}

func stringPtr(s string) *string { return &s }
