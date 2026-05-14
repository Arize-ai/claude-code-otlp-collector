package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/arize-ai/claude-code-otlp-collector/internal/mapping"
	"github.com/arize-ai/claude-code-otlp-collector/internal/otelhttp"
)

func main() {
	listen := flag.String("listen", envString("CLAUDE_COLLECTOR_LISTEN", ":4318"), "OTLP/HTTP listen address")
	exporterEndpoint := flag.String("exporter-endpoint", envString("CLAUDE_COLLECTOR_EXPORTER_ENDPOINT", "http://localhost:4319"), "downstream OTLP/HTTP endpoint base URL")
	exporterHeaders := flag.String("exporter-headers", envString("CLAUDE_COLLECTOR_EXPORTER_HEADERS", ""), "comma-separated downstream OTLP headers, for example space_id=...,api_key=...")
	exportFile := flag.String("export-file", envString("CLAUDE_COLLECTOR_EXPORT_FILE", ""), "optional local JSONL file to append mapped traces/logs")
	forwardLogs := flag.Bool("forward-logs", envBool("CLAUDE_COLLECTOR_FORWARD_LOGS", true), "forward logs to the downstream endpoint")
	projectName := flag.String("project-name", envString("CLAUDE_COLLECTOR_PROJECT_NAME", ""), "project name to add as OpenInference/Arize resource attributes")
	projectFromResourceAttribute := flag.String("project-from-resource-attribute", envString("CLAUDE_COLLECTOR_PROJECT_FROM_RESOURCE_ATTRIBUTE", ""), "resource attribute whose value should be copied to OpenInference/Arize project attributes")
	resourceAttributes := flag.String("resource-attributes", envString("CLAUDE_COLLECTOR_RESOURCE_ATTRIBUTES", ""), "comma-separated resource attributes to add before export")
	serviceNameFilter := flag.String("service-name-filter", envString("CLAUDE_COLLECTOR_SERVICE_NAME_FILTER", "claude-code"), "only forward/export telemetry from this service.name; set to empty or all to disable")
	traceDelay := flag.Duration("trace-delay", envDuration("CLAUDE_COLLECTOR_TRACE_DELAY", 500*time.Millisecond), "delay trace forwarding briefly so log-derived LLM outputs can arrive")
	outputTTL := flag.Duration("output-ttl", envDuration("CLAUDE_COLLECTOR_OUTPUT_TTL", 5*time.Minute), "how long to retain log-derived LLM outputs for trace enrichment")
	allowBodyRef := flag.Bool("allow-body-ref", envBool("CLAUDE_COLLECTOR_ALLOW_BODY_REF", false), "allow reading OTEL_LOG_RAW_API_BODIES=file:<dir> body_ref files from local disk")
	bodyRefCleanupRoots := flag.String("body-ref-cleanup-roots", envString("CLAUDE_COLLECTOR_BODY_REF_CLEANUP_ROOTS", ""), "comma-separated directories where read body_ref files may be cleaned up")
	bodyRefCleanupTTL := flag.Duration("body-ref-cleanup-ttl", envDuration("CLAUDE_COLLECTOR_BODY_REF_CLEANUP_TTL", time.Hour), "how long to keep tracked body_ref files before cleanup")
	bodyRefSuffixes := flag.String("body-ref-cleanup-suffixes", envString("CLAUDE_COLLECTOR_BODY_REF_CLEANUP_SUFFIXES", ".request.json,.response.json"), "comma-separated file suffixes eligible for body_ref cleanup")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bodyRefRoots := parseList(*bodyRefCleanupRoots)
	if *allowBodyRef && len(bodyRefRoots) == 0 {
		logger.Warn("allow-body-ref is enabled but body-ref-cleanup-roots is empty; body_ref reads will be refused",
			"flag", "--body-ref-cleanup-roots",
			"env", "CLAUDE_COLLECTOR_BODY_REF_CLEANUP_ROOTS")
	}

	fileExporter, err := otelhttp.NewFileExporter(*exportFile)
	if err != nil {
		logger.Error("failed to open export file", "path", *exportFile, "error", err)
		os.Exit(1)
	}
	if fileExporter != nil {
		defer fileExporter.Close()
	}

	server := otelhttp.Server{
		ListenAddress:    *listen,
		ExporterEndpoint: *exporterEndpoint,
		ExporterHeaders:  parseHeaders(*exporterHeaders),
		ForwardLogs:      *forwardLogs,
		ResourceAttributes: resourceAttributesWithProject(
			parseHeaders(*resourceAttributes),
			*projectName,
		),
		ProjectFromResourceAttribute: *projectFromResourceAttribute,
		ServiceNameFilter:            *serviceNameFilter,
		TraceDelay:                   *traceDelay,
		FileExporter:                 fileExporter,
		OutputStore: mapping.NewOutputStore(mapping.OutputStoreConfig{
			TTL:                 *outputTTL,
			AllowBodyRef:        *allowBodyRef,
			BodyRefCleanupTTL:   *bodyRefCleanupTTL,
			BodyRefCleanupRoots: bodyRefRoots,
			BodyRefSuffixes:     parseList(*bodyRefSuffixes),
		}),
		Logger: logger,
	}

	if err := server.Run(ctx); err != nil {
		logger.Error("collector stopped", "error", err)
		os.Exit(1)
	}
}

func parseHeaders(raw string) map[string]string {
	headers := map[string]string{}
	for _, part := range parseList(raw) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		headers[key] = strings.TrimSpace(value)
	}
	return headers
}

func parseList(raw string) []string {
	var parts []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

func resourceAttributesWithProject(attrs map[string]string, projectName string) map[string]string {
	if projectName == "" {
		return attrs
	}
	if attrs == nil {
		attrs = map[string]string{}
	}
	for _, key := range []string{"openinference.project.name", "arize.project.name", "project.name", "model_id"} {
		if _, exists := attrs[key]; !exists {
			attrs[key] = projectName
		}
	}
	return attrs
}

func envString(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
