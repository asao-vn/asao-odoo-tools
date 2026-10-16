// Package mcpserver is the Model Context Protocol server behind `asao mcp serve`: newline-delimited
// JSON-RPC 2.0 over the process's standard streams.
//
// It owns no business logic and no authority. Its tools are the agentsurface projection of the
// governed command tree, and a tool call is executed by handing the equivalent command line to the
// same executable path a person's terminal uses (internal/dispatch). Authentication, authorization
// and audit therefore happen exactly where they happen for a typed command: in the privileged
// runtime, as the person whose credential this machine holds.
//
// What the server adds is the person in the loop for CONFIRM tools: before relaying such a call it
// asks the PERSON through the host (MCP elicitation). A host that cannot ask gets a refusal carrying
// the exact command line, which the person can read and run themselves.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// LatestProtocolVersion is the newest MCP revision this server speaks.
const LatestProtocolVersion = "2025-06-18"

// supportedProtocolVersions are the revisions this server can answer in. The differences that
// matter here: 2025-03-26 added tool annotations, 2025-06-18 added structured tool output and
// elicitation.
var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// maxMessageBytes bounds one inbound message. A tool call carries flags, not files.
const maxMessageBytes = 4 << 20

// maxConcurrentCalls bounds how many tool calls run at once.
const maxConcurrentCalls = 4

// confirmationTimeout is how long the server waits for the person to answer a confirmation.
const confirmationTimeout = 10 * time.Minute

// Executor runs one command line (without the program name) and returns what the CLI wrote to
// stdout and stderr, and its exit code.
type Executor func(ctx context.Context, argv []string) (stdout, diagnostics []byte, exitCode int)

// Options configure one server.
type Options struct {
	Tools        []agentsurface.Tool
	Execute      Executor
	Version      string
	Instructions string
	// Mode is ModeTools (one MCP tool per command) or ModeCatalog (four tools that find, describe and
	// run the same commands). Empty means ModeTools.
	Mode string
	// Log receives the server's own diagnostics (never the protocol). Nil discards them.
	Log io.Writer
	// Name is the server's name and the prefix of its catalog tools (<Name>_find, ...). Empty means
	// "asao". Program is the executable a tool's command line names (empty: "asao"), and Product the
	// name shown to the person (empty: "ASAO"). odoo-cli serves the same protocol under its own names.
	Name    string
	Program string
	Product string
	// ConfirmationNote ends every confirmation the person is asked for: what still holds once they
	// say yes. Empty: ASAO's own check of the person's permissions. odoo-cli checks nobody's
	// permissions, and says what its calls run with instead.
	ConfirmationNote string
	// HostConfirms says the host itself asks the person before every call to a tool that is not
	// read-only (a hosted connector such as ChatGPT's), so a CONFIRM tool runs on that consent rather
	// than on an elicitation this server would send. Only HandleMessage's hosts set it.
	HostConfirms bool
	// Filter, when set, rewrites every command's result document before it leaves for the agent:
	// how much of the data the agent sees (odoo-cli's --data-access, DEC-2026-0178). It receives the
	// tool and the document, and returns the document the agent gets.
	Filter func(tool agentsurface.Tool, envelope map[string]interface{}) map[string]interface{}
	// WithholdDiagnostics leaves the command's diagnostics out of what the agent gets: they can quote
	// what the command read.
	WithholdDiagnostics bool
}

func (s *server) name() string {
	if s.options.Name == "" {
		return "asao"
	}
	return s.options.Name
}

func (s *server) program() string {
	if s.options.Program == "" {
		return "asao"
	}
	return s.options.Program
}

func (s *server) product() string {
	if s.options.Product == "" {
		return "ASAO"
	}
	return s.options.Product
}

// Serve speaks MCP on in/out until in closes or ctx ends. It returns nil on a clean end of input.
func Serve(ctx context.Context, in io.Reader, out io.Writer, options Options) error {
	if options.Execute == nil {
		return errors.New("mcpserver: no executor")
	}
	if options.Log == nil {
		options.Log = io.Discard
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := newServer(options, out)

	reader := bufio.NewReaderSize(in, 64*1024)
	var calls sync.WaitGroup
	var result error
	for {
		line, err := readLine(reader)
		if len(bytes.TrimSpace(line)) > 0 {
			s.handle(ctx, line, &calls)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				result = err
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	// The host has gone: nobody can receive an answer or confirm anything any more. In-flight calls
	// are cancelled exactly as an interrupt cancels a typed command, then awaited.
	cancel()
	calls.Wait()
	return result
}

func newServer(options Options, out io.Writer) *server {
	s := &server{
		options:  options,
		out:      out,
		tools:    map[string]agentsurface.Tool{},
		inflight: map[string]context.CancelFunc{},
		pending:  map[string]chan rpcMessage{},
		slots:    make(chan struct{}, maxConcurrentCalls),
	}
	for _, tool := range options.Tools {
		s.tools[tool.Name] = tool
		s.order = append(s.order, tool.Name)
	}
	return s
}

// HandleMessage answers ONE JSON-RPC message for a stateless transport: the Streamable HTTP
// transport in its JSON-response form, where every POST carries one message and the answer is the
// response body. protocolVersion is the MCP-Protocol-Version header the host sent with it (empty
// before initialize, or from an older host: then 2025-03-26, as the specification says).
//
// Nothing persists between messages, so nothing can be asked of the person mid-call: a stateless
// transport never elicits. A tool that needs confirmation runs only when options.HostConfirms says
// the HOST asks the person before every call that is not read-only; otherwise it is refused with the
// command line, as on a stdio host that cannot ask.
//
// The reply is nil for a notification or a response, which the transport answers with 202.
func HandleMessage(ctx context.Context, message []byte, protocolVersion string, options Options) []byte {
	if options.Log == nil {
		options.Log = io.Discard
	}
	var out bytes.Buffer
	s := newServer(options, &out)
	s.protocolVersion = "2025-03-26"
	for _, supported := range supportedProtocolVersions {
		if protocolVersion == supported {
			s.protocolVersion = supported
		}
	}
	if len(message) > maxMessageBytes {
		message = []byte(`{"jsonrpc":"2.0","method":"\u0000oversized"}`)
	}
	var calls sync.WaitGroup
	s.handle(ctx, message, &calls)
	calls.Wait()
	return bytes.TrimSpace(out.Bytes())
}

type server struct {
	options Options
	out     io.Writer
	writeMu sync.Mutex

	tools map[string]agentsurface.Tool
	order []string

	stateMu         sync.Mutex
	protocolVersion string
	canElicit       bool
	inflight        map[string]context.CancelFunc
	pending         map[string]chan rpcMessage

	nextID atomic.Int64
	slots  chan struct{}
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := reader.ReadLine()
		line = append(line, chunk...)
		if len(line) > maxMessageBytes {
			// Drain the rest of the oversized line so the next message starts cleanly.
			for isPrefix && err == nil {
				_, isPrefix, err = reader.ReadLine()
			}
			return []byte(`{"jsonrpc":"2.0","method":"\u0000oversized"}`), err
		}
		if err != nil || !isPrefix {
			return line, err
		}
	}
}

func (s *server) handle(ctx context.Context, line []byte, calls *sync.WaitGroup) {
	var message rpcMessage
	if err := json.Unmarshal(line, &message); err != nil {
		s.reply(nil, nil, &rpcError{Code: codeParseError, Message: "message is not a JSON-RPC 2.0 object"})
		return
	}
	if message.Method == "\u0000oversized" {
		s.reply(nil, nil, &rpcError{Code: codeInvalidRequest, Message: fmt.Sprintf("message exceeds %d bytes", maxMessageBytes)})
		return
	}
	if message.Method == "" {
		// A response to a request this server sent (an elicitation).
		if len(message.ID) > 0 {
			s.stateMu.Lock()
			waiter, ok := s.pending[string(message.ID)]
			delete(s.pending, string(message.ID))
			s.stateMu.Unlock()
			if ok {
				waiter <- message
			}
		}
		return
	}
	isNotification := len(message.ID) == 0 || string(message.ID) == "null"
	switch message.Method {
	case "initialize":
		if !isNotification {
			s.reply(message.ID, s.initialize(message.Params), nil)
		}
	case "ping":
		if !isNotification {
			s.reply(message.ID, map[string]interface{}{}, nil)
		}
	case "notifications/initialized":
	case "notifications/cancelled":
		var params struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			s.stateMu.Lock()
			if cancel, ok := s.inflight[string(params.RequestID)]; ok {
				cancel()
			}
			s.stateMu.Unlock()
		}
	case "tools/list":
		if !isNotification {
			s.reply(message.ID, s.listTools(), nil)
		}
	case "tools/call":
		if isNotification {
			return
		}
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		decoder := json.NewDecoder(bytes.NewReader(message.Params))
		decoder.UseNumber()
		if err := decoder.Decode(&params); err != nil {
			s.reply(message.ID, nil, &rpcError{Code: codeInvalidParams, Message: "tools/call params must be {name, arguments}"})
			return
		}
		catalog := s.options.Mode == ModeCatalog
		tool, ok := s.tools[params.Name]
		if catalog {
			ok = s.isCatalogTool(params.Name)
		}
		if !ok {
			s.reply(message.ID, nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool %q", params.Name)})
			return
		}
		callCtx, cancel := context.WithCancel(ctx)
		key := string(message.ID)
		s.stateMu.Lock()
		s.inflight[key] = cancel
		s.stateMu.Unlock()
		calls.Add(1)
		go func(id json.RawMessage) {
			defer calls.Done()
			defer func() {
				s.stateMu.Lock()
				delete(s.inflight, key)
				s.stateMu.Unlock()
				cancel()
			}()
			select {
			case s.slots <- struct{}{}:
				defer func() { <-s.slots }()
			case <-callCtx.Done():
				return
			}
			var result map[string]interface{}
			if catalog {
				result = s.callCatalog(callCtx, params.Name, normalize(params.Arguments))
			} else {
				result = s.callTool(callCtx, tool, normalize(params.Arguments))
			}
			if callCtx.Err() != nil && ctx.Err() == nil {
				// The client cancelled this request; the protocol says not to answer it.
				return
			}
			s.reply(id, result, nil)
		}(message.ID)
	default:
		if !isNotification {
			s.reply(message.ID, nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method %q is not supported", message.Method)})
		}
	}
}

func (s *server) initialize(raw json.RawMessage) map[string]interface{} {
	var params struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	_ = json.Unmarshal(raw, &params)
	version := LatestProtocolVersion
	for _, supported := range supportedProtocolVersions {
		if params.ProtocolVersion == supported {
			version = supported
		}
	}
	_, elicitation := params.Capabilities["elicitation"]
	s.stateMu.Lock()
	s.protocolVersion = version
	s.canElicit = elicitation && version >= "2025-06-18"
	s.stateMu.Unlock()
	fmt.Fprintf(s.options.Log, "%s mcp: %s %s connected (protocol %s, confirmation %s), %d commands offered as %s\n",
		s.program(), orUnknown(params.ClientInfo.Name), params.ClientInfo.Version, version, map[bool]string{true: "by elicitation", false: "unavailable"}[s.canElicit], len(s.tools), s.modeName())
	return map[string]interface{}{
		"protocolVersion": version,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{"listChanged": false},
		},
		"serverInfo": map[string]interface{}{
			"name":    s.name(),
			"title":   s.product(),
			"version": s.options.Version,
		},
		"instructions": s.options.Instructions,
	}
}

func (s *server) modeName() string {
	if s.options.Mode == ModeCatalog {
		names := s.catalog()
		return fmt.Sprintf("a catalog (%s, %s, %s, %s)", names.find, names.describe, names.read, names.write)
	}
	return "tools"
}

func orUnknown(name string) string {
	if strings.TrimSpace(name) == "" {
		return "unknown client"
	}
	return name
}

func (s *server) listTools() map[string]interface{} {
	s.stateMu.Lock()
	version := s.protocolVersion
	s.stateMu.Unlock()
	if s.options.Mode == ModeCatalog {
		tools := s.catalogTools()
		for _, entry := range tools {
			if version < "2025-03-26" {
				delete(entry, "annotations")
			}
			if version < "2025-06-18" {
				delete(entry, "title")
			}
		}
		return map[string]interface{}{"tools": tools}
	}
	tools := make([]map[string]interface{}, 0, len(s.order))
	for _, name := range s.order {
		tool := s.tools[name]
		entry := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		}
		if version >= "2025-03-26" {
			entry["annotations"] = tool.Annotations
		}
		if version >= "2025-06-18" {
			entry["title"] = tool.Title
		}
		tools = append(tools, entry)
	}
	return map[string]interface{}{"tools": tools}
}

// normalize turns json.Number values (decoded with UseNumber so large integers survive) into the
// shapes agentsurface.Tool.Argv expects, recursively.
func normalize(arguments map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(arguments))
	for key, value := range arguments {
		out[key] = normalizeValue(value)
	}
	return out
}

func normalizeValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return normalize(typed)
	case []interface{}:
		items := make([]interface{}, len(typed))
		for index, item := range typed {
			items[index] = normalizeValue(item)
		}
		return items
	default:
		return value
	}
}

func (s *server) callTool(ctx context.Context, tool agentsurface.Tool, arguments map[string]interface{}) map[string]interface{} {
	argv, argErr := tool.Argv(arguments)
	if argErr != nil {
		return s.errorResult(tool, argErr, "")
	}
	commandLine := tool.CommandLine(arguments)
	if tool.NeedsConfirmation(arguments) && !s.options.HostConfirms {
		if refusal := s.confirm(ctx, tool, arguments, commandLine); refusal != nil {
			return s.errorResult(tool, refusal, commandLine)
		}
	}
	stdout, diagnostics, exitCode := s.options.Execute(ctx, argv)
	return s.commandResult(tool, stdout, diagnostics, exitCode, commandLine)
}

// confirm asks the person, through the host, whether to run this call. A nil return means yes.
func (s *server) confirm(ctx context.Context, tool agentsurface.Tool, arguments map[string]interface{}, commandLine string) *clicore.Error {
	s.stateMu.Lock()
	canElicit := s.canElicit
	s.stateMu.Unlock()
	if !canElicit {
		return clicore.NewError(clicore.CategoryDenied, "HUMAN_CONFIRMATION_REQUIRED",
			"this call needs the person's own confirmation, and this agent host cannot ask for it").
			WithContextValue("commandLine", commandLine).
			WithRemediation("Show the person the command line; they can review it and run it themselves in a terminal. Hosts that support MCP elicitation ask the person directly.")
	}
	message := s.confirmationMessage(tool, arguments, commandLine)
	id := fmt.Sprintf("\"%s-confirm-%d\"", s.name(), s.nextID.Add(1))
	waiter := make(chan rpcMessage, 1)
	s.stateMu.Lock()
	s.pending[id] = waiter
	s.stateMu.Unlock()
	defer func() {
		s.stateMu.Lock()
		delete(s.pending, id)
		s.stateMu.Unlock()
	}()
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"method":  "elicitation/create",
		"params": map[string]interface{}{
			"message": message,
			"requestedSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"confirm": map[string]interface{}{
						"type":        "boolean",
						"title":       "Run this command",
						"description": "Tick to let " + s.product() + " run it as you. Leave it unticked, decline or cancel to stop.",
					},
				},
				"required": []string{"confirm"},
			},
		},
	}
	if err := s.write(request); err != nil {
		return clicore.NewError(clicore.CategoryUnavailable, "HUMAN_CONFIRMATION_UNAVAILABLE", "the confirmation request could not be sent")
	}
	timer := time.NewTimer(confirmationTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return clicore.NewError(clicore.CategoryDenied, "HUMAN_CONFIRMATION_CANCELLED", "the call was cancelled before the person answered")
	case <-timer.C:
		return clicore.NewError(clicore.CategoryDenied, "HUMAN_CONFIRMATION_TIMEOUT", "the person did not answer the confirmation in time").
			WithContextValue("commandLine", commandLine)
	case response := <-waiter:
		if response.Error != nil {
			return clicore.NewError(clicore.CategoryDenied, "HUMAN_CONFIRMATION_UNAVAILABLE", "the agent host could not ask the person: "+response.Error.Message).
				WithContextValue("commandLine", commandLine).
				WithRemediation("Show the person the command line; they can run it themselves.")
		}
		var answer struct {
			Action  string                 `json:"action"`
			Content map[string]interface{} `json:"content"`
		}
		_ = json.Unmarshal(response.Result, &answer)
		if answer.Action == "accept" && answer.Content["confirm"] == true {
			return nil
		}
		return clicore.NewError(clicore.CategoryDenied, "HUMAN_CONFIRMATION_DECLINED", "the person did not confirm this call; nothing was run").
			WithContextValue("action", answer.Action)
	}
}

func (s *server) confirmationMessage(tool agentsurface.Tool, arguments map[string]interface{}, commandLine string) string {
	var b strings.Builder
	b.WriteString("Your AI agent wants " + s.product() + " to run this as you:\n\n")
	b.WriteString(commandLine)
	b.WriteString("\n\n")
	b.WriteString(firstLine(tool.Description))
	if reason, ok := arguments["reason"].(string); ok && strings.TrimSpace(reason) != "" {
		b.WriteString("\nReason given: ")
		b.WriteString(strings.TrimSpace(reason))
	}
	if confirmImpact, _ := arguments[agentsurface.ConfirmImpactFlag].(bool); confirmImpact {
		b.WriteString("\nConfirming also accepts the reported impact on dependent records.")
	}
	note := s.options.ConfirmationNote
	if note == "" {
		note = "ASAO still checks your own permissions before anything runs."
	}
	b.WriteString("\n\n" + note)
	return b.String()
}

func firstLine(text string) string {
	if index := strings.Index(text, "\n"); index >= 0 {
		return text[:index]
	}
	return text
}

// commandResult wraps the CLI's own result document as the tool result. The document is passed on
// verbatim: it already carries a stable code, category and remediation for every refusal.
func (s *server) commandResult(tool agentsurface.Tool, stdout, diagnostics []byte, exitCode int, commandLine string) map[string]interface{} {
	trimmed := bytes.TrimSpace(stdout)
	var envelope map[string]interface{}
	if err := json.Unmarshal(trimmed, &envelope); err != nil || envelope == nil {
		problem := clicore.NewError(clicore.CategoryInternal, "TOOL_OUTPUT_NOT_JSON", "the command did not return a result document").
			WithContextValue("exitCode", exitCode)
		return s.errorResult(tool, problem, commandLine)
	}
	isError := exitCode != clicore.ExitOK || envelope["status"] == "ERROR"
	if s.options.Filter != nil {
		envelope = s.options.Filter(tool, envelope)
	}
	compact, _ := json.Marshal(envelope)
	content := []map[string]interface{}{{"type": "text", "text": string(compact)}}
	if notes := strings.TrimSpace(string(diagnostics)); notes != "" && !s.options.WithholdDiagnostics {
		content = append(content, map[string]interface{}{"type": "text", "text": "CLI diagnostics:\n" + notes})
	}
	content = append(content, map[string]interface{}{"type": "text", "text": "Ran: " + commandLine})
	result := map[string]interface{}{"content": content, "isError": isError}
	s.stateMu.Lock()
	structured := s.protocolVersion >= "2025-06-18"
	s.stateMu.Unlock()
	if structured {
		result["structuredContent"] = envelope
	}
	return result
}

// errorResult reports a refusal the server itself made, in the CLI's own error document shape, so
// an agent branches on one format whichever layer refused.
func (s *server) errorResult(tool agentsurface.Tool, problem *clicore.Error, commandLine string) map[string]interface{} {
	envelope := clicore.Envelope{
		APIVersion: clicore.APIVersion,
		Kind:       clicore.KindError,
		Command:    tool.Command,
		Status:     "ERROR",
		Error:      problem,
	}
	var buffer bytes.Buffer
	_ = clicore.EmitJSON(&buffer, envelope)
	var document map[string]interface{}
	_ = json.Unmarshal(buffer.Bytes(), &document)
	compact, _ := json.Marshal(document)
	content := []map[string]interface{}{{"type": "text", "text": string(compact)}}
	if commandLine != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": "Not run: " + commandLine})
	}
	result := map[string]interface{}{"content": content, "isError": true}
	s.stateMu.Lock()
	structured := s.protocolVersion >= "2025-06-18"
	s.stateMu.Unlock()
	if structured {
		result["structuredContent"] = document
	}
	return result
}

func (s *server) reply(id json.RawMessage, result interface{}, problem *rpcError) {
	message := map[string]interface{}{"jsonrpc": "2.0"}
	if len(id) > 0 {
		message["id"] = id
	} else {
		message["id"] = nil
	}
	if problem != nil {
		message["error"] = problem
	} else {
		message["result"] = result
	}
	if err := s.write(message); err != nil {
		fmt.Fprintf(s.options.Log, "%s mcp: could not write a reply: %v\n", s.program(), err)
	}
}

func (s *server) write(message interface{}) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.out.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if flusher, ok := s.out.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}
