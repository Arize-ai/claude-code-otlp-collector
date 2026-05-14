package mapping

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

const (
	SpanKindAgent = "AGENT"
	SpanKindChain = "CHAIN"
	SpanKindLLM   = "LLM"
	SpanKindTool  = "TOOL"
)

// MapTraceRequest adds OpenInference-compatible aliases to Claude Code native
// telemetry while preserving Claude's original span topology and attributes.
func MapTraceRequest(req *collectortracev1.ExportTraceServiceRequest, outputs *OutputStore) {
	if req == nil {
		return
	}

	llmCountsByTrace := countLLMSpansByTrace(req)
	for _, resourceSpans := range req.GetResourceSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				spanType := claudeSpanType(span)
				if strings.HasPrefix(spanType, "claude_code.") {
					mapClaudeStatus(span)
				}
				switch spanType {
				case "claude_code.interaction":
					mapInteraction(span)
				case "claude_code.llm_request":
					traceID := hex.EncodeToString(span.GetTraceId())
					spanID := hex.EncodeToString(span.GetSpanId())
					mapLLM(span, outputs, llmCountsByTrace[traceID] == 1, traceID, spanID)
				case "claude_code.tool":
					mapTool(span)
				case "claude_code.tool.blocked_on_user", "claude_code.tool.execution":
					mapChain(span)
				}
			}
		}
	}
}

func countLLMSpansByTrace(req *collectortracev1.ExportTraceServiceRequest) map[string]int {
	counts := map[string]int{}
	for _, resourceSpans := range req.GetResourceSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				if claudeSpanType(span) == "claude_code.llm_request" {
					counts[hex.EncodeToString(span.GetTraceId())]++
				}
			}
		}
	}
	return counts
}

func claudeSpanType(span *tracev1.Span) string {
	if span == nil {
		return ""
	}
	if span.GetName() != "" {
		return span.GetName()
	}
	if spanType, ok := getString(span.GetAttributes(), "span.type"); ok {
		return spanType
	}
	return ""
}

func interactionInput(span *tracev1.Span) (string, bool) {
	prompt, ok := getString(span.GetAttributes(), "user_prompt", "input.value")
	if !ok || prompt == "" || prompt == "<REDACTED>" {
		return "", false
	}
	return prompt, true
}

func mapInteraction(span *tracev1.Span) {
	setString(&span.Attributes, "openinference.span.kind", SpanKindAgent)
	if sequence, ok := getInt(span.GetAttributes(), "interaction.sequence"); ok {
		setInt(&span.Attributes, "trace.number", sequence)
	}
	if prompt, ok := interactionInput(span); ok {
		setString(&span.Attributes, "input.value", prompt)
	}
}

func mapLLM(span *tracev1.Span, outputs *OutputStore, allowTraceFallback bool, traceID string, spanID string) {
	setString(&span.Attributes, "openinference.span.kind", SpanKindLLM)
	setString(&span.Attributes, "llm.system", firstString(span.GetAttributes(), "gen_ai.system", "anthropic"))

	if model, ok := getString(span.GetAttributes(), "model", "gen_ai.request.model", "llm.model_name"); ok && model != "" {
		normalized := normalizeModelName(model)
		setString(&span.Attributes, "llm.model_name", normalized)
		setString(&span.Attributes, "model", normalized)
		setString(&span.Attributes, "gen_ai.request.model", normalized)
	}

	inputTokens, _ := getInt(span.GetAttributes(), "input_tokens")
	cacheReadTokens, _ := getInt(span.GetAttributes(), "cache_read_tokens")
	cacheCreationTokens, _ := getInt(span.GetAttributes(), "cache_creation_tokens")
	outputTokens, _ := getInt(span.GetAttributes(), "output_tokens")
	promptTokens := inputTokens + cacheReadTokens + cacheCreationTokens

	if promptTokens > 0 {
		setInt(&span.Attributes, "llm.token_count.prompt", promptTokens)
	}
	if outputTokens > 0 {
		setInt(&span.Attributes, "llm.token_count.completion", outputTokens)
	}
	if promptTokens+outputTokens > 0 {
		setInt(&span.Attributes, "llm.token_count.total", promptTokens+outputTokens)
	}

	requestID, _ := getString(span.GetAttributes(), "request_id", "gen_ai.response.id")
	setStringIfNonEmpty(&span.Attributes, "llm.request_id", requestID)
	if stopReason, ok := getString(span.GetAttributes(), "stop_reason"); ok {
		setString(&span.Attributes, "llm.invocation_parameters", invocationParametersJSON(stopReason))
	}

	if outputs == nil {
		return
	}
	if input, ok := outputs.FindInputForSpan(requestID); ok {
		if model, hasModel := getString(span.GetAttributes(), "llm.model_name"); !hasModel || model == "" {
			normalized := normalizeModelName(input.ModelName)
			setStringIfNonEmpty(&span.Attributes, "llm.model_name", normalized)
			setStringIfNonEmpty(&span.Attributes, "model", normalized)
			setStringIfNonEmpty(&span.Attributes, "gen_ai.request.model", normalized)
		}
		attachLLMInput(span, input)
	}
	if output, ok := outputs.FindForSpan(traceID, spanID, requestID, allowTraceFallback); ok {
		attachLLMOutput(span, output)
	}
}

func mapTool(span *tracev1.Span) {
	setString(&span.Attributes, "openinference.span.kind", SpanKindTool)
	if toolName, ok := getString(span.GetAttributes(), "tool_name"); ok && toolName != "" {
		setString(&span.Attributes, "tool.name", toolName)
		span.Name = toolName
	}
	if filePath, ok := getString(span.GetAttributes(), "file_path"); ok {
		setString(&span.Attributes, "tool.file_path", filePath)
	}
	if command, ok := getString(span.GetAttributes(), "full_command"); ok {
		setString(&span.Attributes, "tool.command", command)
	}
	mapToolStructuredFields(span)
	if subagentType, ok := getString(span.GetAttributes(), "subagent_type"); ok {
		setString(&span.Attributes, "agent.name", subagentType)
	}

	if input, ok := getString(span.GetAttributes(), "tool_input", "tool_parameters"); ok {
		setString(&span.Attributes, "input.value", input)
	}

	inputFromEvent, outputFromEvent := toolEventBodies(span)
	if _, hasInput := getString(span.GetAttributes(), "input.value"); !hasInput {
		setStringIfNonEmpty(&span.Attributes, "input.value", inputFromEvent)
	}
	setStringIfNonEmpty(&span.Attributes, "output.value", outputFromEvent)
}

func mapChain(span *tracev1.Span) {
	setString(&span.Attributes, "openinference.span.kind", SpanKindChain)
	if claudeSpanType(span) == "claude_code.tool.blocked_on_user" {
		if decision, ok := getString(span.GetAttributes(), "decision"); ok {
			setStringIfNonEmpty(&span.Attributes, "input.value", decision)
			setString(&span.Attributes, "input.mime_type", "text/plain")
		}
	}
	if claudeSpanType(span) == "claude_code.tool.execution" {
		if success, ok := getBool(span.GetAttributes(), "success"); ok {
			setString(&span.Attributes, "input.value", strconv.FormatBool(success))
			setString(&span.Attributes, "input.mime_type", "text/plain")
		}
	}
}

func mapClaudeStatus(span *tracev1.Span) {
	if span == nil {
		return
	}
	if span.GetStatus().GetCode() != tracev1.Status_STATUS_CODE_UNSET {
		return
	}

	if success, ok := getBool(span.GetAttributes(), "success"); ok && !success {
		message, _ := getString(span.GetAttributes(), "error", "error.message", "exception.message")
		setSpanStatus(span, tracev1.Status_STATUS_CODE_ERROR, message)
		return
	}
	if message, ok := getString(span.GetAttributes(), "error", "error.message", "exception.message"); ok && message != "" {
		setSpanStatus(span, tracev1.Status_STATUS_CODE_ERROR, message)
		return
	}

	setSpanStatus(span, tracev1.Status_STATUS_CODE_OK, "")
}

func setSpanStatus(span *tracev1.Span, code tracev1.Status_StatusCode, message string) {
	if span.Status == nil {
		span.Status = &tracev1.Status{}
	}
	span.Status.Code = code
	span.Status.Message = message
}

func normalizeModelName(model string) string {
	model = strings.TrimSpace(model)
	if idx := strings.Index(model, "["); idx >= 0 {
		model = strings.TrimSpace(model[:idx])
	}
	return model
}

func invocationParametersJSON(stopReason string) string {
	encoded, err := json.Marshal(map[string]string{"stop_reason": stopReason})
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func attachLLMOutput(span *tracev1.Span, output ResponseOutput) {
	setStringIfNonEmpty(&span.Attributes, "output.value", output.Text)
	setStringIfNonEmpty(&span.Attributes, "llm.output_messages.0.message.role", output.Role)
	setStringIfNonEmpty(&span.Attributes, "llm.output_messages.0.message.content", output.Text)
}

func attachLLMInput(span *tracev1.Span, input RequestInput) {
	setStringIfNonEmpty(&span.Attributes, "input.value", input.Text)
	setString(&span.Attributes, "input.mime_type", "text/plain")
}

func firstString(attrs []*commonv1.KeyValue, key string, fallback string) string {
	if value, ok := getString(attrs, key); ok && value != "" {
		return value
	}
	return fallback
}

func toolEventBodies(span *tracev1.Span) (input string, output string) {
	for _, event := range span.GetEvents() {
		if event.GetName() != "tool.output" {
			continue
		}
		if input == "" {
			input, _ = getString(event.GetAttributes(), "input", "tool_input", "tool.input", "arguments")
		}
		if output == "" {
			output, _ = getString(event.GetAttributes(), "output", "tool_output", "tool.output", "tool_result", "result", "content", "body")
		}
	}
	return input, output
}

func mapToolStructuredFields(span *tracev1.Span) {
	for _, decoded := range decodedToolPayloads(span.GetAttributes()) {
		setFirstJSONField(&span.Attributes, decoded, "tool.command", "full_command", "bash_command", "command")
		setFirstJSONField(&span.Attributes, decoded, "tool.file_path", "file_path", "path")
		setFirstJSONField(&span.Attributes, decoded, "tool.url", "url")
		setFirstJSONField(&span.Attributes, decoded, "tool.query", "query", "pattern", "regex")
		setFirstJSONField(&span.Attributes, decoded, "tool.description", "description")
		setFirstJSONField(&span.Attributes, decoded, "tool.truncated", "truncated", "is_truncated")
	}
}

func decodedToolPayloads(attrs []*commonv1.KeyValue) []map[string]any {
	var decoded []map[string]any
	for _, key := range []string{"tool_parameters", "tool_input"} {
		raw, ok := getString(attrs, key)
		if !ok || raw == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			decoded = append(decoded, payload)
		}
	}
	return decoded
}

func setFirstJSONField(attrs *[]*commonv1.KeyValue, payload map[string]any, target string, keys ...string) {
	if _, exists := getString(*attrs, target); exists {
		return
	}
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			setStringIfNonEmpty(attrs, target, typed)
			return
		case bool:
			setString(attrs, target, strconv.FormatBool(typed))
			return
		case float64:
			setString(attrs, target, strconv.FormatFloat(typed, 'f', -1, 64))
			return
		}
	}
}
