package otelhttp

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

type FileExporter struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

func NewFileExporter(path string) (*FileExporter, error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(file)
	enc.SetEscapeHTML(false)
	return &FileExporter{file: file, enc: enc}, nil
}

func (e *FileExporter) Close() error {
	if e == nil || e.file == nil {
		return nil
	}
	return e.file.Close()
}

func (e *FileExporter) WriteTraces(req *collectortracev1.ExportTraceServiceRequest) error {
	if e == nil {
		return nil
	}
	record := map[string]any{
		"signal":      "traces",
		"exported_at": time.Now().UTC().Format(time.RFC3339Nano),
		"spans":       traceSummaries(req),
	}
	return e.write(record)
}

func (e *FileExporter) WriteLogs(req *collectorlogsv1.ExportLogsServiceRequest) error {
	if e == nil {
		return nil
	}
	record := map[string]any{
		"signal":      "logs",
		"exported_at": time.Now().UTC().Format(time.RFC3339Nano),
		"records":     logSummaries(req),
	}
	return e.write(record)
}

func (e *FileExporter) write(record map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enc.Encode(record)
}

func traceSummaries(req *collectortracev1.ExportTraceServiceRequest) []map[string]any {
	var spans []map[string]any
	if req == nil {
		return spans
	}
	for _, resourceSpans := range req.GetResourceSpans() {
		resourceAttrs := attrsMap(resourceSpans.GetResource().GetAttributes())
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				spans = append(spans, spanSummary(span, resourceAttrs))
			}
		}
	}
	return spans
}

func spanSummary(span *tracev1.Span, resourceAttrs map[string]any) map[string]any {
	return map[string]any{
		"trace_id":       hex.EncodeToString(span.GetTraceId()),
		"span_id":        hex.EncodeToString(span.GetSpanId()),
		"parent_span_id": hex.EncodeToString(span.GetParentSpanId()),
		"name":           span.GetName(),
		"kind":           span.GetKind().String(),
		"status":         span.GetStatus().GetCode().String(),
		"attributes":     attrsMap(span.GetAttributes()),
		"events":         eventSummaries(span.GetEvents()),
		"resource":       resourceAttrs,
	}
}

func eventSummaries(events []*tracev1.Span_Event) []map[string]any {
	summaries := make([]map[string]any, 0, len(events))
	for _, event := range events {
		summaries = append(summaries, map[string]any{
			"name":       event.GetName(),
			"attributes": attrsMap(event.GetAttributes()),
		})
	}
	return summaries
}

func logSummaries(req *collectorlogsv1.ExportLogsServiceRequest) []map[string]any {
	var records []map[string]any
	if req == nil {
		return records
	}
	for _, resourceLogs := range req.GetResourceLogs() {
		resourceAttrs := attrsMap(resourceLogs.GetResource().GetAttributes())
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			for _, record := range scopeLogs.GetLogRecords() {
				records = append(records, logSummary(record, resourceAttrs))
			}
		}
	}
	return records
}

func logSummary(record *logsv1.LogRecord, resourceAttrs map[string]any) map[string]any {
	return map[string]any{
		"trace_id":   hex.EncodeToString(record.GetTraceId()),
		"span_id":    hex.EncodeToString(record.GetSpanId()),
		"severity":   record.GetSeverityText(),
		"body":       anyValue(record.GetBody()),
		"attributes": attrsMap(record.GetAttributes()),
		"resource":   resourceAttrs,
	}
}

func attrsMap(attrs []*commonv1.KeyValue) map[string]any {
	result := map[string]any{}
	for _, attr := range attrs {
		result[attr.GetKey()] = anyValue(attr.GetValue())
	}
	return result
}

func anyValue(value *commonv1.AnyValue) any {
	if value == nil {
		return nil
	}
	switch typed := value.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return typed.StringValue
	case *commonv1.AnyValue_IntValue:
		return typed.IntValue
	case *commonv1.AnyValue_DoubleValue:
		return typed.DoubleValue
	case *commonv1.AnyValue_BoolValue:
		return typed.BoolValue
	case *commonv1.AnyValue_BytesValue:
		return hex.EncodeToString(typed.BytesValue)
	case *commonv1.AnyValue_ArrayValue:
		values := make([]any, 0, len(typed.ArrayValue.Values))
		for _, item := range typed.ArrayValue.Values {
			values = append(values, anyValue(item))
		}
		return values
	case *commonv1.AnyValue_KvlistValue:
		return attrsMap(typed.KvlistValue.Values)
	default:
		return nil
	}
}
