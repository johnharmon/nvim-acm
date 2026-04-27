// Smoke test: spawns acm-ls, exercises diagnostics + completion +
// hover + signature help against canned fixtures, and reports pass/fail.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const oversizedNameDoc = `apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: this-is-a-policy-name-that-is-way-too-long-to-fit-in-the-default-limit-of-sixty-three-chars
  namespace: default
spec: {}
`

const templatedNameDoc = `apiVersion: policy.open-cluster-management.io/v1
kind: Placement
metadata:
  name: placement-{{ $team }}-something
spec: {}
`

const hubForbiddenDoc = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata:
  name: demo
spec:
  object-templates-raw: |
    {{hub trimPrefix "foo-" .ManagedClusterName hub}}
`

const lookupNoDefaultDoc = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata:
  name: demo
spec:
  object-templates-raw: |
    {{hub (lookup "v1" "Secret" "ns" "name").data.token hub}}
`

// For completion / hover / signatureHelp probes — a small file with a hub block.
const hubFixture = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata:
  name: hub-fixture
spec:
  object-templates-raw: |
    {{hub fromSecret "ns" "name" "key" hub}}
`

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

type publishParams struct {
	URI         string `json:"uri"`
	Diagnostics []struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	} `json:"diagnostics"`
}

type runner struct {
	mu      sync.Mutex
	stdin   io.Writer
	idSeq   int
	pending map[int]chan *rpcMessage
	notes   chan *rpcMessage
}

func main() {
	binary := "./acm-ls"
	if len(os.Args) > 1 && (strings.HasPrefix(os.Args[1], "./") || strings.HasPrefix(os.Args[1], "/")) {
		binary = os.Args[1]
	}
	stdin, stdout, stop := startServer(binary)
	defer stop()

	r := &runner{
		stdin:   stdin,
		pending: map[int]chan *rpcMessage{},
		notes:   make(chan *rpcMessage, 64),
	}
	go r.readLoop(bufio.NewReader(stdout))

	// initialize
	resp, err := r.request("initialize", map[string]any{
		"processId":             os.Getpid(),
		"rootUri":               nil,
		"capabilities":          map[string]any{},
		"initializationOptions": defaultSettings(),
	}, 2*time.Second)
	if err != nil || resp == nil {
		fail("initialize failed: %v", err)
	}
	r.notify("initialized", map[string]any{})

	// Open all fixtures.
	cases := []struct {
		uri        string
		text       string
		expectCode string
	}{
		{"file:///tmp/length.yaml", oversizedNameDoc, "policy-name-length"},
		{"file:///tmp/template.yaml", templatedNameDoc, "policy-name-template"},
		{"file:///tmp/hubfn.yaml", hubForbiddenDoc, "hub-forbidden-functions"},
		{"file:///tmp/lookup.yaml", lookupNoDefaultDoc, "lookup-default-dict"},
	}
	gotDiag := map[string]map[string]bool{}
	for _, c := range cases {
		gotDiag[c.expectCode] = map[string]bool{}
		r.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri": c.uri, "languageId": "yaml", "version": 1, "text": c.text,
			},
		})
	}
	r.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri": "file:///tmp/hub-fixture.yaml", "languageId": "yaml", "version": 1, "text": hubFixture,
		},
	})

	collectUntil(r.notes, 1500*time.Millisecond, func(msg *rpcMessage) bool {
		if msg.Method != "textDocument/publishDiagnostics" {
			return false
		}
		var pp publishParams
		if json.Unmarshal(msg.Params, &pp) != nil {
			return false
		}
		for _, d := range pp.Diagnostics {
			code := strings.Trim(string(d.Code), `"`)
			if _, ok := gotDiag[code]; ok {
				gotDiag[code][pp.URI] = true
			}
		}
		return false
	})

	// Diagnostics report.
	failed := 0
	fmt.Println("Diagnostics:")
	for _, c := range cases {
		mark := "ok  "
		if !gotDiag[c.expectCode][c.uri] {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("  %s  %-30s expected %s\n", mark, c.uri, c.expectCode)
	}

	// Completion: cursor inside the hub fixture, after `from`.
	// Line 6 (0-indexed) is `    {{hub fromSecret ...`
	// "    {{hub from" — character 14 sits after `from`.
	completionResp, err := r.request("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/hub-fixture.yaml"},
		"position":     map[string]any{"line": 6, "character": 14},
	}, 2*time.Second)
	fmt.Println("\nCompletion:")
	if err != nil || completionResp == nil {
		fmt.Println("  FAIL  no response")
		failed++
	} else {
		ok, count := containsCompletion(completionResp.Result, "fromSecret")
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("  %s  fromSecret in completion list (got %d items)\n", mark, count)
	}

	// Hover on `fromSecret` (cursor at `from|Secret`).
	hoverResp, err := r.request("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/hub-fixture.yaml"},
		"position":     map[string]any{"line": 6, "character": 16},
	}, 2*time.Second)
	fmt.Println("\nHover:")
	if err != nil || hoverResp == nil || len(hoverResp.Result) == 0 || string(hoverResp.Result) == "null" {
		fmt.Println("  FAIL  no hover")
		failed++
	} else {
		mark := "ok  "
		if !strings.Contains(string(hoverResp.Result), "fromSecret") {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("  %s  hover mentions fromSecret\n", mark)
	}

	// Signature help: cursor after fromSecret + space (still on line 6).
	// `    {{hub fromSecret ` — character 21 puts cursor right after the space.
	sigResp, err := r.request("textDocument/signatureHelp", map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/hub-fixture.yaml"},
		"position":     map[string]any{"line": 6, "character": 21},
	}, 2*time.Second)
	fmt.Println("\nSignature help:")
	if err != nil || sigResp == nil || len(sigResp.Result) == 0 || string(sigResp.Result) == "null" {
		fmt.Println("  FAIL  no signature help")
		failed++
	} else {
		mark := "ok  "
		if !strings.Contains(string(sigResp.Result), "fromSecret") {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("  %s  signatures mention fromSecret\n", mark)
	}

	// Semantic tokens: full doc.
	semResp, err := r.request("textDocument/semanticTokens/full", map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/hub-fixture.yaml"},
	}, 2*time.Second)
	fmt.Println("\nSemantic tokens:")
	if err != nil || semResp == nil || len(semResp.Result) == 0 || string(semResp.Result) == "null" {
		fmt.Println("  FAIL  no semantic tokens")
		failed++
	} else {
		var st struct {
			Data []uint32 `json:"data"`
		}
		if json.Unmarshal(semResp.Result, &st) != nil || len(st.Data) == 0 {
			fmt.Println("  FAIL  no token data")
			failed++
		} else {
			tokenCount := len(st.Data) / 5
			mark := "ok  "
			if tokenCount < 6 {
				mark = "FAIL"
				failed++
			}
			fmt.Printf("  %s  emitted %d tokens (%d uint32s)\n", mark, tokenCount, len(st.Data))
		}
	}

	fmt.Println()
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d case(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("All cases passed.")
}

func defaultSettings() map[string]any {
	return map[string]any{
		"acm": map[string]any{
			"enabled": true,
			"acm":     map[string]any{"version": "2.15"},
			"rules": map[string]any{
				"policy-name-length":      map[string]any{"enabled": true, "maxLength": 63},
				"policy-name-template":    map[string]any{"enabled": true, "mode": "strict"},
				"hub-forbidden-functions": map[string]any{"enabled": true},
				"lookup-default-dict":     map[string]any{"enabled": true},
			},
		},
	}
}

func containsCompletion(raw json.RawMessage, name string) (bool, int) {
	// CompletionList has "items"; or it may be a plain array.
	var list struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err == nil && list.Items != nil {
		for _, it := range list.Items {
			if it.Label == name {
				return true, len(list.Items)
			}
		}
		return false, len(list.Items)
	}
	var arr []struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, it := range arr {
			if it.Label == name {
				return true, len(arr)
			}
		}
		return false, len(arr)
	}
	return false, 0
}

func collectUntil(ch <-chan *rpcMessage, dur time.Duration, fn func(*rpcMessage) bool) {
	timer := time.NewTimer(dur)
	defer timer.Stop()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if fn(msg) {
				return
			}
		case <-timer.C:
			return
		}
	}
}

func (r *runner) request(method string, params any, timeout time.Duration) (*rpcMessage, error) {
	r.mu.Lock()
	r.idSeq++
	id := r.idSeq
	ch := make(chan *rpcMessage, 1)
	r.pending[id] = ch
	r.mu.Unlock()

	writeFrame(r.stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-ch:
		return msg, nil
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for response to %s", method)
	}
}

func (r *runner) notify(method string, params any) {
	writeFrame(r.stdin, map[string]any{
		"jsonrpc": "2.0", "method": method, "params": params,
	})
}

func (r *runner) readLoop(reader *bufio.Reader) {
	for {
		msg, err := readMessage(reader)
		if err != nil {
			return
		}
		if msg.ID != nil {
			r.mu.Lock()
			ch, ok := r.pending[*msg.ID]
			if ok {
				delete(r.pending, *msg.ID)
			}
			r.mu.Unlock()
			if ok {
				ch <- msg
			}
			continue
		}
		select {
		case r.notes <- msg:
		default:
		}
	}
}

func startServer(binary string) (io.WriteCloser, io.ReadCloser, func()) {
	cmd := exec.Command(binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	return stdin, stdout, func() { _ = cmd.Process.Kill() }
}

func writeFrame(w io.Writer, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	io.WriteString(w, fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data)))
	w.Write(data)
}

func readMessage(r *bufio.Reader) (*rpcMessage, error) {
	headers := map[string]string{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	var length int
	fmt.Sscanf(headers["Content-Length"], "%d", &length)
	if length <= 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
