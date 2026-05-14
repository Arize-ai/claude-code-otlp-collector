package otelhttp

import (
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
)

func addResourceAttributesToTraces(req *collectortracev1.ExportTraceServiceRequest, attrs map[string]string) {
	if req == nil || len(attrs) == 0 {
		return
	}
	for _, resourceSpans := range req.GetResourceSpans() {
		for key, value := range attrs {
			setResourceString(&resourceSpans.Resource.Attributes, key, value)
		}
	}
}

func addProjectAttributesFromResourceToTraces(req *collectortracev1.ExportTraceServiceRequest, attrName string) {
	if req == nil || attrName == "" {
		return
	}
	for _, resourceSpans := range req.GetResourceSpans() {
		value, ok := resourceString(resourceSpans.GetResource().GetAttributes(), attrName)
		if !ok || value == "" {
			continue
		}
		setProjectResourceStrings(&resourceSpans.Resource.Attributes, value)
	}
}

func addResourceAttributesToLogs(req *collectorlogsv1.ExportLogsServiceRequest, attrs map[string]string) {
	if req == nil || len(attrs) == 0 {
		return
	}
	for _, resourceLogs := range req.GetResourceLogs() {
		for key, value := range attrs {
			setResourceString(&resourceLogs.Resource.Attributes, key, value)
		}
	}
}

func addProjectAttributesFromResourceToLogs(req *collectorlogsv1.ExportLogsServiceRequest, attrName string) {
	if req == nil || attrName == "" {
		return
	}
	for _, resourceLogs := range req.GetResourceLogs() {
		value, ok := resourceString(resourceLogs.GetResource().GetAttributes(), attrName)
		if !ok || value == "" {
			continue
		}
		setProjectResourceStrings(&resourceLogs.Resource.Attributes, value)
	}
}

func setProjectResourceStrings(attrs *[]*commonv1.KeyValue, projectName string) {
	for _, key := range []string{"openinference.project.name", "arize.project.name", "project.name", "model_id"} {
		setResourceString(attrs, key, projectName)
	}
}

func resourceString(attrs []*commonv1.KeyValue, key string) (string, bool) {
	for _, attr := range attrs {
		if attr.GetKey() == key {
			return attr.GetValue().GetStringValue(), true
		}
	}
	return "", false
}

func setResourceString(attrs *[]*commonv1.KeyValue, key string, value string) {
	for _, attr := range *attrs {
		if attr.GetKey() == key {
			attr.Value = &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}
			return
		}
	}
	*attrs = append(*attrs, &commonv1.KeyValue{
		Key:   key,
		Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}},
	})
}
