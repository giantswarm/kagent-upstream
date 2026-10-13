package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
)

// unknownToolCode is the JSON-RPC error MCP servers answer a call of a tool
// they do not have with.
const unknownToolCode = -32602

// toolSelection is the tools of one server Claude may list and call. Claude
// Code itself exposes every tool a server lists, so the forwarder removes the
// others from the server's tools/list results and answers their tools/call
// requests itself.
type toolSelection map[string]struct{}

func newToolSelection(tools []string) toolSelection {
	if len(tools) == 0 {
		return nil
	}
	selection := make(toolSelection, len(tools))
	for _, tool := range tools {
		selection[tool] = struct{}{}
	}
	return selection
}

func (s toolSelection) allows(tool string) bool {
	_, ok := s[tool]
	return ok
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}

// listRequestsKey carries the ids of the tools/list requests a forwarded
// request holds, whose results the response filters.
type listRequestsKey struct{}

// screen reads a request Claude sends to a server with a tool selection. It
// answers a call of an unselected tool itself and returns false; otherwise it
// restores the body and returns the request, carrying the ids of its tools/list
// requests.
func (s toolSelection) screen(response http.ResponseWriter, request *http.Request) (*http.Request, bool) {
	if request.Method != http.MethodPost {
		return request, true
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(response, "read MCP request: "+err.Error(), http.StatusRequestEntityTooLarge)
		return nil, false
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	messages, batch, err := splitMessages(body)
	if err != nil {
		// Not JSON-RPC: the server answers it as it would without the forwarder.
		return request, true
	}
	lists := make(map[string]struct{})
	for _, raw := range messages {
		var message rpcRequest
		if json.Unmarshal(raw, &message) != nil || len(message.ID) == 0 {
			continue
		}
		switch message.Method {
		case "tools/list":
			lists[compactID(message.ID)] = struct{}{}
		case "tools/call":
			if s.allows(message.Params.Name) {
				continue
			}
			if batch {
				http.Error(response, fmt.Sprintf("tool %q is not selected for this server", message.Params.Name), http.StatusForbidden)
				return nil, false
			}
			writeUnknownTool(response, message.ID, message.Params.Name)
			return nil, false
		}
	}
	if len(lists) == 0 {
		return request, true
	}
	return request.WithContext(context.WithValue(request.Context(), listRequestsKey{}, lists)), true
}

func writeUnknownTool(response http.ResponseWriter, id json.RawMessage, tool string) {
	reply, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": unknownToolCode, "message": fmt.Sprintf("Unknown tool: %s", tool)},
	})
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Content-Length", strconv.Itoa(len(reply)))
	_, _ = response.Write(reply)
}

// filterResponse removes the unselected tools from the tools/list results of
// a response, a JSON body or an event stream.
func (s toolSelection) filterResponse(maxBytes int64) func(*http.Response) error {
	return func(response *http.Response) error {
		lists, _ := response.Request.Context().Value(listRequestsKey{}).(map[string]struct{})
		if len(lists) == 0 || response.StatusCode != http.StatusOK {
			return nil
		}
		mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		switch mediaType {
		case "application/json":
			body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
			_ = response.Body.Close()
			if err != nil {
				return fmt.Errorf("read MCP response: %w", err)
			}
			if int64(len(body)) > maxBytes {
				return fmt.Errorf("MCP response exceeds %d bytes", maxBytes)
			}
			body = s.filterMessages(body, lists)
			response.Body = io.NopCloser(bytes.NewReader(body))
			response.ContentLength = int64(len(body))
			response.Header.Set("Content-Length", strconv.Itoa(len(body)))
		case "text/event-stream":
			// The filtered stream's length is known only at its end.
			response.ContentLength = -1
			response.Header.Del("Content-Length")
			response.Body = &eventStreamFilter{
				source: bufio.NewReader(response.Body), closer: response.Body, maxBytes: maxBytes,
				filter: func(data []byte) []byte { return s.filterMessages(data, lists) },
			}
		}
		return nil
	}
}

// filterMessages returns raw with the unselected tools removed from the
// results of the listed tools/list requests; anything else is returned as is.
func (s toolSelection) filterMessages(raw []byte, lists map[string]struct{}) []byte {
	messages, batch, err := splitMessages(raw)
	if err != nil {
		return raw
	}
	changed := false
	for i, message := range messages {
		if filtered, ok := s.filterListResult(message, lists); ok {
			messages[i], changed = filtered, true
		}
	}
	if !changed {
		return raw
	}
	var out []byte
	if batch {
		out, err = json.Marshal(messages)
	} else {
		out, err = json.Marshal(messages[0])
	}
	if err != nil {
		return raw
	}
	return out
}

func (s toolSelection) filterListResult(raw json.RawMessage, lists map[string]struct{}) (json.RawMessage, bool) {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil {
		return nil, false
	}
	if _, listed := lists[compactID(message["id"])]; !listed || message["result"] == nil {
		return nil, false
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(message["result"], &result) != nil {
		return nil, false
	}
	var tools []json.RawMessage
	if json.Unmarshal(result["tools"], &tools) != nil {
		return nil, false
	}
	kept := make([]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		var described struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(tool, &described) == nil && s.allows(described.Name) {
			kept = append(kept, tool)
		}
	}
	var err error
	if result["tools"], err = json.Marshal(kept); err != nil {
		return nil, false
	}
	if message["result"], err = json.Marshal(result); err != nil {
		return nil, false
	}
	filtered, err := json.Marshal(message)
	return filtered, err == nil
}

// splitMessages returns the JSON-RPC messages of a body: one object, or the
// elements of a batch.
func splitMessages(raw []byte) ([]json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) != 0 && trimmed[0] == '[' {
		var messages []json.RawMessage
		if err := json.Unmarshal(trimmed, &messages); err != nil {
			return nil, true, err
		}
		return messages, true, nil
	}
	var message json.RawMessage
	if err := json.Unmarshal(trimmed, &message); err != nil {
		return nil, false, err
	}
	return []json.RawMessage{message}, false, nil
}

func compactID(id json.RawMessage) string {
	var out bytes.Buffer
	if json.Compact(&out, id) != nil {
		return string(id)
	}
	return out.String()
}

// eventStreamFilter passes an event stream through one event at a time, the
// data of each event run through filter. An event's other fields and its
// framing are kept as the server sent them.
type eventStreamFilter struct {
	source   *bufio.Reader
	closer   io.Closer
	maxBytes int64
	filter   func([]byte) []byte
	pending  bytes.Buffer
	err      error
}

func (f *eventStreamFilter) Read(p []byte) (int, error) {
	for f.pending.Len() == 0 {
		if f.err != nil {
			return 0, f.err
		}
		f.err = f.nextEvent()
	}
	return f.pending.Read(p)
}

func (f *eventStreamFilter) Close() error { return f.closer.Close() }

// nextEvent reads the lines of one event up to and including the blank line
// that ends it, and queues the event with its data filtered.
func (f *eventStreamFilter) nextEvent() error {
	var lines [][]byte
	var data [][]byte
	var size int64
	for {
		line, err := f.source.ReadBytes('\n')
		size += int64(len(line))
		if size > f.maxBytes {
			return fmt.Errorf("MCP event exceeds %d bytes", f.maxBytes)
		}
		if len(line) != 0 {
			lines = append(lines, line)
			content := bytes.TrimRight(line, "\r\n")
			if value, ok := bytes.CutPrefix(content, []byte("data:")); ok {
				data = append(data, bytes.TrimPrefix(value, []byte(" ")))
			}
			if len(content) == 0 {
				break
			}
		}
		if err != nil {
			f.queue(lines, data)
			if errors.Is(err, io.EOF) && len(lines) != 0 {
				return nil
			}
			return err
		}
	}
	f.queue(lines, data)
	return nil
}

func (f *eventStreamFilter) queue(lines, data [][]byte) {
	if len(data) == 0 {
		for _, line := range lines {
			f.pending.Write(line)
		}
		return
	}
	joined := bytes.Join(data, []byte("\n"))
	filtered := f.filter(joined)
	if bytes.Equal(filtered, joined) {
		for _, line := range lines {
			f.pending.Write(line)
		}
		return
	}
	// The filtered data is compact JSON, one line: it replaces the event's
	// first data line, and its other data lines are dropped.
	written := false
	for _, line := range lines {
		if !bytes.HasPrefix(line, []byte("data:")) {
			f.pending.Write(line)
			continue
		}
		if !written {
			f.pending.WriteString("data: ")
			f.pending.Write(filtered)
			f.pending.WriteByte('\n')
			written = true
		}
	}
}
