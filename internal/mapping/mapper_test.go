package mapping

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestMapLLMSpan(t *testing.T) {
	span := &tracev1.Span{
		Name: "claude_code.llm_request",
		Attributes: testAttrs(map[string]any{
			"model":                 "claude-sonnet-4-6[1m]",
			"input_tokens":          int64(100),
			"cache_read_tokens":     int64(30),
			"cache_creation_tokens": int64(20),
			"output_tokens":         int64(50),
			"request_id":            "req_123",
		}),
	}
	req := traceRequest(span)

	MapTraceRequest(req, nil)

	assertStringAttr(t, span.GetAttributes(), "openinference.span.kind", "LLM")
	assertStringAttr(t, span.GetAttributes(), "llm.system", "anthropic")
	assertStringAttr(t, span.GetAttributes(), "llm.model_name", "claude-sonnet-4-6")
	assertStringAttr(t, span.GetAttributes(), "model", "claude-sonnet-4-6")
	assertStringAttr(t, span.GetAttributes(), "gen_ai.request.model", "claude-sonnet-4-6")
	assertIntAttr(t, span.GetAttributes(), "llm.token_count.prompt", 150)
	assertIntAttr(t, span.GetAttributes(), "llm.token_count.completion", 50)
	assertIntAttr(t, span.GetAttributes(), "llm.token_count.total", 200)
	assertStringAttr(t, span.GetAttributes(), "llm.request_id", "req_123")
	assertStatus(t, span, tracev1.Status_STATUS_CODE_OK, "")
}

func TestMapLLMStopReasonAsInvocationParameters(t *testing.T) {
	span := &tracev1.Span{
		Name: "claude_code.llm_request",
		Attributes: testAttrs(map[string]any{
			"stop_reason": "end_turn",
		}),
	}

	MapTraceRequest(traceRequest(span), nil)

	assertStringAttr(t, span.GetAttributes(), "llm.invocation_parameters", `{"stop_reason":"end_turn"}`)
	if _, ok := getString(span.GetAttributes(), "llm.invocation_parameters.stop_reason"); ok {
		t.Fatal("unexpected nested invocation parameter attr")
	}
}

func TestMapToolSpanWithOutputEvent(t *testing.T) {
	span := &tracev1.Span{
		Name: "claude_code.tool",
		Attributes: testAttrs(map[string]any{
			"tool_name":  "Bash",
			"file_path":  "/repo/src/foo.go",
			"tool_input": `{"command":"go test ./..."}`,
		}),
		Events: []*tracev1.Span_Event{{
			Name: "tool.output",
			Attributes: testAttrs(map[string]any{
				"output": "FAIL ./internal/mapping",
			}),
		}},
	}
	req := traceRequest(span)

	MapTraceRequest(req, nil)

	if got := span.GetName(); got != "Bash" {
		t.Fatalf("span name = %q, want Bash", got)
	}
	assertStringAttr(t, span.GetAttributes(), "openinference.span.kind", "TOOL")
	assertStringAttr(t, span.GetAttributes(), "tool.name", "Bash")
	assertStringAttr(t, span.GetAttributes(), "tool.file_path", "/repo/src/foo.go")
	assertStringAttr(t, span.GetAttributes(), "tool.command", "go test ./...")
	assertStringAttr(t, span.GetAttributes(), "input.value", `{"command":"go test ./..."}`)
	assertStringAttr(t, span.GetAttributes(), "output.value", "FAIL ./internal/mapping")
	assertStatus(t, span, tracev1.Status_STATUS_CODE_OK, "")
}

func TestMapToolStructuredFieldsFromInput(t *testing.T) {
	span := &tracev1.Span{
		Name: "claude_code.tool",
		Attributes: testAttrs(map[string]any{
			"tool_name":  "WebFetch",
			"tool_input": `{"url":"https://example.com","query":"otel mapping","description":"fetch docs","truncated":true}`,
		}),
	}
	req := traceRequest(span)

	MapTraceRequest(req, nil)

	if got := span.GetName(); got != "WebFetch" {
		t.Fatalf("span name = %q, want WebFetch", got)
	}
	assertStringAttr(t, span.GetAttributes(), "tool.url", "https://example.com")
	assertStringAttr(t, span.GetAttributes(), "tool.query", "otel mapping")
	assertStringAttr(t, span.GetAttributes(), "tool.description", "fetch docs")
	assertStringAttr(t, span.GetAttributes(), "tool.truncated", "true")
}

func TestMapToolChildSpansAsChain(t *testing.T) {
	blocked := &tracev1.Span{
		Name:       "claude_code.tool.blocked_on_user",
		Attributes: testAttrs(map[string]any{"decision": "accept"}),
	}
	execution := &tracev1.Span{
		Name:       "claude_code.tool.execution",
		Attributes: testAttrs(map[string]any{"success": true}),
	}
	req := traceRequest(blocked, execution)

	MapTraceRequest(req, nil)

	assertStringAttr(t, blocked.GetAttributes(), "openinference.span.kind", "CHAIN")
	assertStringAttr(t, blocked.GetAttributes(), "input.value", "accept")
	assertStringAttr(t, blocked.GetAttributes(), "input.mime_type", "text/plain")
	assertStringAttr(t, execution.GetAttributes(), "openinference.span.kind", "CHAIN")
	assertStringAttr(t, execution.GetAttributes(), "input.value", "true")
	assertStringAttr(t, execution.GetAttributes(), "input.mime_type", "text/plain")
	assertStatus(t, blocked, tracev1.Status_STATUS_CODE_OK, "")
	assertStatus(t, execution, tracev1.Status_STATUS_CODE_OK, "")
}

func TestMapInteractionSpan(t *testing.T) {
	span := &tracev1.Span{
		Name: "claude_code.interaction",
		Attributes: testAttrs(map[string]any{
			"user_prompt":          "Find failing tests and fix one",
			"interaction.sequence": int64(3),
		}),
	}
	req := traceRequest(span)

	MapTraceRequest(req, nil)

	assertStringAttr(t, span.GetAttributes(), "openinference.span.kind", "AGENT")
	assertStringAttr(t, span.GetAttributes(), "input.value", "Find failing tests and fix one")
	assertIntAttr(t, span.GetAttributes(), "trace.number", 3)
	assertStatus(t, span, tracev1.Status_STATUS_CODE_OK, "")
}

func TestMapClaudeStatusError(t *testing.T) {
	span := &tracev1.Span{
		Name: "claude_code.tool.execution",
		Attributes: testAttrs(map[string]any{
			"success": false,
			"error":   "permission denied",
		}),
	}

	MapTraceRequest(traceRequest(span), nil)

	assertStringAttr(t, span.GetAttributes(), "input.value", "false")
	assertStringAttr(t, span.GetAttributes(), "input.mime_type", "text/plain")
	assertStatus(t, span, tracev1.Status_STATUS_CODE_ERROR, "permission denied")
}

func TestMapClaudeStatusOnUnclassifiedClaudeSpan(t *testing.T) {
	span := &tracev1.Span{Name: "claude_code.hook"}

	MapTraceRequest(traceRequest(span), nil)

	assertStatus(t, span, tracev1.Status_STATUS_CODE_OK, "")
}

func TestLogResponseBodyEnrichesLLMOutput(t *testing.T) {
	traceID := []byte("1234567890123456")
	spanID := []byte("12345678")
	store := NewOutputStore(OutputStoreConfig{})
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					TraceId: traceID,
					SpanId:  spanID,
					Attributes: testAttrs(map[string]any{
						"event.name": "api_response_body",
						"body":       `{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"Done."}]}`,
					}),
				}},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 1 {
		t.Fatalf("stored outputs = %d, want 1", got)
	}

	span := &tracev1.Span{
		Name:       "claude_code.llm_request",
		TraceId:    traceID,
		SpanId:     spanID,
		Attributes: testAttrs(map[string]any{"model": "claude-sonnet-4-6"}),
	}
	req := traceRequest(span)

	MapTraceRequest(req, store)

	assertStringAttr(t, span.GetAttributes(), "output.value", "Done.")
	assertStringAttr(t, span.GetAttributes(), "llm.output_messages.0.message.role", "assistant")
	assertStringAttr(t, span.GetAttributes(), "llm.output_messages.0.message.content", "Done.")
	if _, ok := getString(span.GetAttributes(), "llm.output_messages.0.message.id"); ok {
		t.Fatal("unexpected output message id attr")
	}
}

func TestLogRequestBodyEnrichesLLMInputAndModel(t *testing.T) {
	store := NewOutputStore(OutputStoreConfig{})
	requestBody := `{"model":"claude-haiku-4-5-20251001[1m]","messages":[{"role":"user","content":"version 20"},{"role":"assistant","content":"version 20"},{"role":"user","content":[{"type":"text","text":"<system-reminder>hidden</system-reminder>"},{"type":"text","text":"tell me about the database"}]}]}`
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{
					{
						Attributes: testAttrs(map[string]any{
							"event.name":   "api_request_body",
							"prompt.id":    "prompt_1",
							"query_source": "repl_main_thread",
							"body":         requestBody,
						}),
					},
					{
						Attributes: testAttrs(map[string]any{
							"event.name":   "api_request",
							"prompt.id":    "prompt_1",
							"query_source": "repl_main_thread",
							"request_id":   "req_1",
						}),
					},
				},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 2 {
		t.Fatalf("stored log items = %d, want 2", got)
	}

	span := &tracev1.Span{
		Name: "claude_code.llm_request",
		Attributes: testAttrs(map[string]any{
			"request_id":  "req_1",
			"input.value": "[SUGGESTION MODE: stale input]",
		}),
	}
	req := traceRequest(span)

	MapTraceRequest(req, store)

	assertStringAttr(t, span.GetAttributes(), "input.value", "tell me about the database")
	assertStringAttr(t, span.GetAttributes(), "input.mime_type", "text/plain")
	assertStringAttr(t, span.GetAttributes(), "llm.model_name", "claude-haiku-4-5-20251001")
	assertStringAttr(t, span.GetAttributes(), "model", "claude-haiku-4-5-20251001")
	assertStringAttr(t, span.GetAttributes(), "gen_ai.request.model", "claude-haiku-4-5-20251001")
}

func TestLogRequestBodyLinksByPromptAndQuerySourceOrder(t *testing.T) {
	store := NewOutputStore(OutputStoreConfig{})
	firstBody := `{"messages":[{"role":"user","content":"first"}]}`
	secondBody := `{"messages":[{"role":"user","content":"second"}]}`
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{
					{Attributes: testAttrs(map[string]any{
						"event.name":   "api_request_body",
						"prompt.id":    "prompt_1",
						"query_source": "agent:builtin:Explore",
						"body":         firstBody,
					})},
					{Attributes: testAttrs(map[string]any{
						"event.name":   "api_request_body",
						"prompt.id":    "prompt_1",
						"query_source": "agent:builtin:Explore",
						"body":         secondBody,
					})},
					{Attributes: testAttrs(map[string]any{
						"event.name":   "api_request",
						"prompt.id":    "prompt_1",
						"query_source": "agent:builtin:Explore",
						"request_id":   "req_1",
					})},
					{Attributes: testAttrs(map[string]any{
						"event.name":   "api_request",
						"prompt.id":    "prompt_1",
						"query_source": "agent:builtin:Explore",
						"request_id":   "req_2",
					})},
				},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 4 {
		t.Fatalf("stored log items = %d, want 4", got)
	}

	first, ok := store.FindInputForSpan("req_1")
	if !ok {
		t.Fatal("missing first request input")
	}
	second, ok := store.FindInputForSpan("req_2")
	if !ok {
		t.Fatal("missing second request input")
	}
	if first.Text != "first" {
		t.Fatalf("first text = %q, want %q", first.Text, "first")
	}
	if second.Text != "second" {
		t.Fatalf("second text = %q, want %q", second.Text, "second")
	}
}

func TestLogResponseBodyReadsBodyRef(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "req_1.response.json")
	if err := os.WriteFile(bodyPath, []byte(`{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"Done from file."}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	traceID := []byte("1234567890123456")
	spanID := []byte("12345678")
	store := NewOutputStore(OutputStoreConfig{AllowBodyRef: true, BodyRefCleanupRoots: []string{dir}})
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					TraceId: traceID,
					SpanId:  spanID,
					Attributes: testAttrs(map[string]any{
						"event.name": "api_response_body",
						"body_ref":   "file:" + bodyPath,
					}),
				}},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 1 {
		t.Fatalf("stored outputs = %d, want 1", got)
	}

	span := &tracev1.Span{
		Name:    "claude_code.llm_request",
		TraceId: traceID,
		SpanId:  spanID,
	}
	req := traceRequest(span)

	MapTraceRequest(req, store)

	assertStringAttr(t, span.GetAttributes(), "output.value", "Done from file.")
}

func TestLogRequestBodyReadsBodyRef(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "req_1.request.json")
	requestBody := `{"messages":[{"role":"user","content":"from file"}]}`
	if err := os.WriteFile(bodyPath, []byte(requestBody), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewOutputStore(OutputStoreConfig{AllowBodyRef: true, BodyRefCleanupRoots: []string{dir}})
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{
					{Attributes: testAttrs(map[string]any{
						"event.name":   "api_request_body",
						"prompt.id":    "prompt_1",
						"query_source": "repl_main_thread",
						"body_ref":     "file:" + bodyPath,
					})},
					{Attributes: testAttrs(map[string]any{
						"event.name":   "api_request",
						"prompt.id":    "prompt_1",
						"query_source": "repl_main_thread",
						"request_id":   "req_1",
					})},
				},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 2 {
		t.Fatalf("stored log items = %d, want 2", got)
	}

	span := &tracev1.Span{
		Name:       "claude_code.llm_request",
		Attributes: testAttrs(map[string]any{"request_id": "req_1"}),
	}
	MapTraceRequest(traceRequest(span), store)

	assertStringAttr(t, span.GetAttributes(), "input.value", "from file")
}

func TestBodyRefCleanupDeletesAllowedTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "req_123.response.json")
	if err := os.WriteFile(bodyPath, []byte(`{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"Done."}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewOutputStore(OutputStoreConfig{
		AllowBodyRef:        true,
		BodyRefCleanupRoots: []string{dir},
		BodyRefCleanupTTL:   time.Nanosecond,
	})
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					Attributes: testAttrs(map[string]any{
						"event.name": "api_response_body",
						"body_ref":   "file:" + bodyPath,
					}),
				}},
			}},
		}},
	}

	if got := store.IngestLogRequest(logs); got != 1 {
		t.Fatalf("stored outputs = %d, want 1", got)
	}
	store.pruneLocked(time.Now().Add(time.Second))

	if _, err := os.Stat(bodyPath); !os.IsNotExist(err) {
		t.Fatalf("body ref file still exists or stat failed with non-not-exist error: %v", err)
	}
}

func TestBodyRefCleanupKeepsDisallowedFiles(t *testing.T) {
	allowedDir := t.TempDir()
	otherDir := t.TempDir()
	wrongSuffixPath := filepath.Join(allowedDir, "req_123.json")
	outsidePath := filepath.Join(otherDir, "req_456.response.json")
	for _, path := range []string{wrongSuffixPath, outsidePath} {
		if err := os.WriteFile(path, []byte(`{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"Done."}]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	store := NewOutputStore(OutputStoreConfig{
		AllowBodyRef:        true,
		BodyRefCleanupRoots: []string{allowedDir},
		BodyRefCleanupTTL:   time.Nanosecond,
	})
	store.trackBodyRefLocked(wrongSuffixPath, time.Now().Add(-time.Second))
	store.trackBodyRefLocked(outsidePath, time.Now().Add(-time.Second))
	store.pruneLocked(time.Now())

	for _, path := range []string{wrongSuffixPath, outsidePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to remain, stat error: %v", path, err)
		}
	}
}

func TestBodyRefRefusesReadOutsideAllowedRoot(t *testing.T) {
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "req_evil.response.json")
	if err := os.WriteFile(outsidePath, []byte(`{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"leaked"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewOutputStore(OutputStoreConfig{
		AllowBodyRef:        true,
		BodyRefCleanupRoots: []string{allowedDir},
	})
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					Attributes: testAttrs(map[string]any{
						"event.name": "api_response_body",
						"body_ref":   "file:" + outsidePath,
					}),
				}},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 0 {
		t.Fatalf("stored outputs = %d, want 0 (read outside allowed root must be refused)", got)
	}
}

func TestBodyRefRefusesSymlinkEscape(t *testing.T) {
	allowedDir := t.TempDir()
	secretDir := t.TempDir()
	secretPath := filepath.Join(secretDir, "secret.response.json")
	if err := os.WriteFile(secretPath, []byte(`{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"secret"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(allowedDir, "req_link.response.json")
	if err := os.Symlink(secretPath, symlinkPath); err != nil {
		t.Skipf("symlink unsupported on this filesystem: %v", err)
	}

	store := NewOutputStore(OutputStoreConfig{
		AllowBodyRef:        true,
		BodyRefCleanupRoots: []string{allowedDir},
	})
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					Attributes: testAttrs(map[string]any{
						"event.name": "api_response_body",
						"body_ref":   "file:" + symlinkPath,
					}),
				}},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 0 {
		t.Fatalf("stored outputs = %d, want 0 (symlink escape must be refused)", got)
	}
}

func TestBodyRefRefusesReadWithoutAllowedRoots(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "req_1.response.json")
	if err := os.WriteFile(bodyPath, []byte(`{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewOutputStore(OutputStoreConfig{AllowBodyRef: true})
	logs := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					Attributes: testAttrs(map[string]any{
						"event.name": "api_response_body",
						"body_ref":   "file:" + bodyPath,
					}),
				}},
			}},
		}},
	}
	if got := store.IngestLogRequest(logs); got != 0 {
		t.Fatalf("stored outputs = %d, want 0 (no allow-list roots configured)", got)
	}
}

func traceRequest(spans ...*tracev1.Span) *collectortracev1.ExportTraceServiceRequest {
	return &collectortracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{{
			ScopeSpans: []*tracev1.ScopeSpans{{
				Spans: spans,
			}},
		}},
	}
}

func assertStringAttr(t *testing.T, attrs []*commonv1.KeyValue, key string, want string) {
	t.Helper()
	got, ok := getString(attrs, key)
	if !ok {
		t.Fatalf("missing attr %s", key)
	}
	if got != want {
		t.Fatalf("attr %s = %q, want %q", key, got, want)
	}
}

func assertIntAttr(t *testing.T, attrs []*commonv1.KeyValue, key string, want int64) {
	t.Helper()
	got, ok := getInt(attrs, key)
	if !ok {
		t.Fatalf("missing attr %s", key)
	}
	if got != want {
		t.Fatalf("attr %s = %d, want %d", key, got, want)
	}
}

func assertStatus(t *testing.T, span *tracev1.Span, want tracev1.Status_StatusCode, wantMessage string) {
	t.Helper()
	if span.GetStatus().GetCode() != want {
		t.Fatalf("status = %s, want %s", span.GetStatus().GetCode(), want)
	}
	if span.GetStatus().GetMessage() != wantMessage {
		t.Fatalf("status message = %q, want %q", span.GetStatus().GetMessage(), wantMessage)
	}
}
