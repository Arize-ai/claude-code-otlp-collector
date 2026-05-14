package mapping

import commonv1 "go.opentelemetry.io/proto/otlp/common/v1"

func testAttrs(values map[string]any) []*commonv1.KeyValue {
	attrs := make([]*commonv1.KeyValue, 0, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			attrs = append(attrs, &commonv1.KeyValue{Key: key, Value: stringValue(typed)})
		case int:
			attrs = append(attrs, &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: int64(typed)}}})
		case int64:
			attrs = append(attrs, &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: typed}}})
		case bool:
			attrs = append(attrs, &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: typed}}})
		}
	}
	return attrs
}
