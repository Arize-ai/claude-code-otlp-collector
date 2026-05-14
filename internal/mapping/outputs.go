package mapping

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
)

type OutputStoreConfig struct {
	TTL                 time.Duration
	AllowBodyRef        bool
	BodyRefCleanupTTL   time.Duration
	BodyRefCleanupRoots []string
	BodyRefSuffixes     []string
}

type OutputStore struct {
	mu                  sync.Mutex
	ttl                 time.Duration
	allowBodyRef        bool
	bodyRefCleanupTTL   time.Duration
	bodyRefCleanupRoots []string
	bodyRefSuffixes     []string
	bodyRefs            map[string]time.Time
	byTraceSpan         map[string]ResponseOutput
	byRequestID         map[string]ResponseOutput
	byTrace             map[string][]ResponseOutput
	inputByRequestID    map[string]RequestInput
	pendingInputs       map[string][]RequestInput
}

type ResponseOutput struct {
	TraceID   string
	SpanID    string
	RequestID string
	MessageID string
	Role      string
	Text      string
	SeenAt    time.Time
}

type RequestInput struct {
	RequestID   string
	PromptID    string
	QuerySource string
	ModelName   string
	Text        string
	SeenAt      time.Time
}

func NewOutputStore(config OutputStoreConfig) *OutputStore {
	ttl := config.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	cleanupTTL := config.BodyRefCleanupTTL
	if cleanupTTL <= 0 {
		cleanupTTL = 1 * time.Hour
	}
	bodyRefSuffixes := config.BodyRefSuffixes
	if len(bodyRefSuffixes) == 0 {
		bodyRefSuffixes = []string{".request.json", ".response.json"}
	}
	return &OutputStore{
		ttl:                 ttl,
		allowBodyRef:        config.AllowBodyRef,
		bodyRefCleanupTTL:   cleanupTTL,
		bodyRefCleanupRoots: cleanBodyRefRoots(config.BodyRefCleanupRoots),
		bodyRefSuffixes:     bodyRefSuffixes,
		bodyRefs:            map[string]time.Time{},
		byTraceSpan:         map[string]ResponseOutput{},
		byRequestID:         map[string]ResponseOutput{},
		byTrace:             map[string][]ResponseOutput{},
		inputByRequestID:    map[string]RequestInput{},
		pendingInputs:       map[string][]RequestInput{},
	}
}

func (s *OutputStore) IngestLogRequest(req *collectorlogsv1.ExportLogsServiceRequest) int {
	if s == nil || req == nil {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())

	count := 0
	for _, resourceLogs := range req.GetResourceLogs() {
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			for _, record := range scopeLogs.GetLogRecords() {
				if input, ok := s.inputFromLogRecord(record); ok {
					s.storePendingInputLocked(input)
					count++
					continue
				}
				if promptID, querySource, requestID, ok := apiRequestLink(record); ok {
					if s.linkPendingInputLocked(promptID, querySource, requestID) {
						count++
					}
					continue
				}
				if output, ok := s.outputFromLogRecord(record); ok {
					s.storeOutputLocked(output)
					count++
				}
			}
		}
	}
	return count
}

func (s *OutputStore) FindForSpan(traceID string, spanID string, requestID string, allowTraceFallback bool) (ResponseOutput, bool) {
	if s == nil {
		return ResponseOutput{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())

	if traceID != "" && spanID != "" {
		if output, ok := s.byTraceSpan[traceSpanKey(traceID, spanID)]; ok {
			return output, true
		}
	}
	if requestID != "" {
		if output, ok := s.byRequestID[requestID]; ok {
			return output, true
		}
	}
	if allowTraceFallback && traceID != "" {
		outputs := s.byTrace[traceID]
		if len(outputs) == 1 {
			return outputs[0], true
		}
	}
	return ResponseOutput{}, false
}

func (s *OutputStore) FindInputForSpan(requestID string) (RequestInput, bool) {
	if s == nil || requestID == "" {
		return RequestInput{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())

	input, ok := s.inputByRequestID[requestID]
	return input, ok
}

func (s *OutputStore) inputFromLogRecord(record *logsv1.LogRecord) (RequestInput, bool) {
	if !isAPIRequestBodyEvent(record) {
		return RequestInput{}, false
	}
	body, err := s.payloadBody(record)
	if err != nil || body == "" {
		return RequestInput{}, false
	}
	text := displayInputFromRequestBody(body)
	modelName := modelNameFromRequestBody(body)
	if text == "" {
		return RequestInput{}, false
	}
	promptID, _ := getString(record.GetAttributes(), "prompt.id")
	querySource, _ := getString(record.GetAttributes(), "query_source")
	return RequestInput{
		PromptID:    promptID,
		QuerySource: querySource,
		ModelName:   modelName,
		Text:        text,
		SeenAt:      time.Now(),
	}, true
}

func (s *OutputStore) outputFromLogRecord(record *logsv1.LogRecord) (ResponseOutput, bool) {
	if !isAPIResponseBodyEvent(record) {
		return ResponseOutput{}, false
	}
	body, err := s.payloadBody(record)
	if err != nil || body == "" {
		return ResponseOutput{}, false
	}
	parsed, err := parseAssistantOutput(body)
	if err != nil || parsed.Text == "" {
		return ResponseOutput{}, false
	}

	requestID, _ := getString(record.GetAttributes(), "request_id", "gen_ai.response.id")
	return ResponseOutput{
		TraceID:   hex.EncodeToString(record.GetTraceId()),
		SpanID:    hex.EncodeToString(record.GetSpanId()),
		RequestID: requestID,
		MessageID: parsed.MessageID,
		Role:      parsed.Role,
		Text:      parsed.Text,
		SeenAt:    time.Now(),
	}, true
}

func isAPIRequestBodyEvent(record *logsv1.LogRecord) bool {
	if record == nil {
		return false
	}
	eventName, _ := getString(record.GetAttributes(), "event.name", "event_name")
	eventName = strings.TrimPrefix(eventName, "claude_code.")
	return eventName == "api_request_body"
}

func apiRequestLink(record *logsv1.LogRecord) (promptID string, querySource string, requestID string, ok bool) {
	if record == nil {
		return "", "", "", false
	}
	eventName, _ := getString(record.GetAttributes(), "event.name", "event_name")
	eventName = strings.TrimPrefix(eventName, "claude_code.")
	if eventName != "api_request" {
		return "", "", "", false
	}
	requestID, ok = getString(record.GetAttributes(), "request_id", "gen_ai.response.id")
	if !ok || requestID == "" {
		return "", "", "", false
	}
	promptID, _ = getString(record.GetAttributes(), "prompt.id")
	querySource, _ = getString(record.GetAttributes(), "query_source")
	return promptID, querySource, requestID, true
}

func isAPIResponseBodyEvent(record *logsv1.LogRecord) bool {
	if record == nil {
		return false
	}
	eventName, _ := getString(record.GetAttributes(), "event.name", "event_name")
	eventName = strings.TrimPrefix(eventName, "claude_code.")
	return eventName == "api_response_body"
}

func (s *OutputStore) payloadBody(record *logsv1.LogRecord) (string, error) {
	if s.allowBodyRef {
		if bodyRef, ok := getString(record.GetAttributes(), "body_ref"); ok && bodyRef != "" {
			path := normalizeBodyRefPath(bodyRef)
			body, err := readBodyRefPath(path)
			if err == nil && body != "" {
				s.trackBodyRefLocked(path, time.Now())
				return body, nil
			}
		}
	}
	if body, ok := getString(record.GetAttributes(), "body"); ok && body != "" {
		return body, nil
	}
	if body, ok := anyValueString(record.GetBody()); ok && body != "" && strings.HasPrefix(strings.TrimSpace(body), "{") {
		return body, nil
	}
	bodyRef, ok := getString(record.GetAttributes(), "body_ref")
	if !ok || bodyRef == "" {
		return "", errors.New("api body event has no body or body_ref")
	}
	if !s.allowBodyRef {
		return "", errors.New("body_ref reading is disabled")
	}
	path := normalizeBodyRefPath(bodyRef)
	body, err := readBodyRefPath(path)
	if err == nil && body != "" {
		s.trackBodyRefLocked(path, time.Now())
	}
	return body, err
}

func normalizeBodyRefPath(bodyRef string) string {
	bodyRef = strings.TrimSpace(bodyRef)
	bodyRef = strings.TrimPrefix(bodyRef, "file://")
	bodyRef = strings.TrimPrefix(bodyRef, "file:")
	return filepath.Clean(bodyRef)
}

func readBodyRefPath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func cleanBodyRefRoots(roots []string) []string {
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		root = strings.TrimPrefix(root, "file://")
		root = strings.TrimPrefix(root, "file:")
		if root == "" {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(root))
	}
	return cleaned
}

func (s *OutputStore) trackBodyRefLocked(path string, seenAt time.Time) {
	if len(s.bodyRefCleanupRoots) == 0 || !s.bodyRefCleanupAllowed(path) {
		return
	}
	s.bodyRefs[path] = seenAt
}

func (s *OutputStore) bodyRefCleanupAllowed(path string) bool {
	if path == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	if !hasAllowedSuffix(cleanPath, s.bodyRefSuffixes) {
		return false
	}
	for _, root := range s.bodyRefCleanupRoots {
		if isPathWithinRoot(cleanPath, root) {
			return true
		}
	}
	return false
}

func hasAllowedSuffix(path string, suffixes []string) bool {
	for _, suffix := range suffixes {
		suffix = strings.TrimSpace(suffix)
		if suffix != "" && strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func isPathWithinRoot(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, "../"))
}

type assistantOutput struct {
	MessageID string
	Role      string
	Text      string
}

func parseAssistantOutput(body string) (assistantOutput, error) {
	var payload struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return assistantOutput{}, err
	}
	var parts []string
	for _, block := range payload.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	role := payload.Role
	if role == "" {
		role = "assistant"
	}
	return assistantOutput{
		MessageID: payload.ID,
		Role:      role,
		Text:      strings.Join(parts, "\n"),
	}, nil
}

func displayInputFromRequestBody(body string) string {
	var payload struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	for i := len(payload.Messages) - 1; i >= 0; i-- {
		message := payload.Messages[i]
		if message.Role != "user" {
			continue
		}
		text := stripSystemReminderText(messageTextContent(message.Content))
		if text != "" {
			return text
		}
	}
	for i := len(payload.Messages) - 1; i >= 0; i-- {
		message := payload.Messages[i]
		if message.Role != "user" {
			continue
		}
		text := toolResultContent(message.Content)
		if text != "" {
			return text
		}
	}
	return ""
}

func modelNameFromRequestBody(body string) string {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	return normalizeModelName(payload.Model)
}

func messageTextContent(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolResultContent(raw json.RawMessage) string {
	var blocks []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		content := strings.TrimSpace(rawJSONText(block.Content))
		if content != "" {
			parts = append(parts, "[tool_result] "+content)
		}
	}
	return strings.Join(parts, "\n")
}

func rawJSONText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, block := range blocks {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return string(raw)
}

func stripSystemReminderText(text string) string {
	const startTag = "<system-reminder>"
	const endTag = "</system-reminder>"
	for {
		start := strings.Index(text, startTag)
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], endTag)
		if end < 0 {
			break
		}
		end += start + len(endTag)
		text = text[:start] + text[end:]
	}
	return strings.TrimSpace(text)
}

func (s *OutputStore) storeOutputLocked(output ResponseOutput) {
	if output.TraceID != "" && output.SpanID != "" {
		s.byTraceSpan[traceSpanKey(output.TraceID, output.SpanID)] = output
	}
	if output.RequestID != "" {
		s.byRequestID[output.RequestID] = output
	}
	if output.TraceID != "" {
		s.byTrace[output.TraceID] = append(s.byTrace[output.TraceID], output)
	}
}

func (s *OutputStore) storePendingInputLocked(input RequestInput) {
	key := requestInputKey(input.PromptID, input.QuerySource)
	s.pendingInputs[key] = append(s.pendingInputs[key], input)
}

func (s *OutputStore) linkPendingInputLocked(promptID string, querySource string, requestID string) bool {
	key := requestInputKey(promptID, querySource)
	pending := s.pendingInputs[key]
	if len(pending) == 0 {
		return false
	}
	input := pending[0]
	if len(pending) == 1 {
		delete(s.pendingInputs, key)
	} else {
		s.pendingInputs[key] = pending[1:]
	}
	input.RequestID = requestID
	input.SeenAt = time.Now()
	s.inputByRequestID[requestID] = input
	return true
}

func requestInputKey(promptID string, querySource string) string {
	return promptID + "\x00" + querySource
}

func (s *OutputStore) pruneLocked(now time.Time) {
	cutoff := now.Add(-s.ttl)
	for key, output := range s.byTraceSpan {
		if output.SeenAt.Before(cutoff) {
			delete(s.byTraceSpan, key)
		}
	}
	for key, output := range s.byRequestID {
		if output.SeenAt.Before(cutoff) {
			delete(s.byRequestID, key)
		}
	}
	for key, input := range s.inputByRequestID {
		if input.SeenAt.Before(cutoff) {
			delete(s.inputByRequestID, key)
		}
	}
	for key, outputs := range s.byTrace {
		filtered := outputs[:0]
		for _, output := range outputs {
			if !output.SeenAt.Before(cutoff) {
				filtered = append(filtered, output)
			}
		}
		if len(filtered) == 0 {
			delete(s.byTrace, key)
		} else {
			s.byTrace[key] = filtered
		}
	}
	for key, inputs := range s.pendingInputs {
		filtered := inputs[:0]
		for _, input := range inputs {
			if !input.SeenAt.Before(cutoff) {
				filtered = append(filtered, input)
			}
		}
		if len(filtered) == 0 {
			delete(s.pendingInputs, key)
		} else {
			s.pendingInputs[key] = filtered
		}
	}
	s.pruneBodyRefsLocked(now)
}

func (s *OutputStore) pruneBodyRefsLocked(now time.Time) {
	if len(s.bodyRefs) == 0 || len(s.bodyRefCleanupRoots) == 0 {
		return
	}
	cutoff := now.Add(-s.bodyRefCleanupTTL)
	for path, seenAt := range s.bodyRefs {
		if !seenAt.Before(cutoff) {
			continue
		}
		if s.bodyRefCleanupAllowed(path) {
			_ = os.Remove(path)
		}
		delete(s.bodyRefs, path)
	}
}

func traceSpanKey(traceID string, spanID string) string {
	return traceID + "/" + spanID
}
