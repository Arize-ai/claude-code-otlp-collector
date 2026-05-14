package mapping

import (
	"encoding/json"
	"fmt"
	"strconv"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
)

func getString(attrs []*commonv1.KeyValue, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := getAttr(attrs, key); ok {
			return anyValueString(value)
		}
	}
	return "", false
}

func getInt(attrs []*commonv1.KeyValue, keys ...string) (int64, bool) {
	for _, key := range keys {
		value, ok := getAttr(attrs, key)
		if !ok || value == nil {
			continue
		}
		switch typed := value.Value.(type) {
		case *commonv1.AnyValue_IntValue:
			return typed.IntValue, true
		case *commonv1.AnyValue_DoubleValue:
			return int64(typed.DoubleValue), true
		case *commonv1.AnyValue_StringValue:
			parsed, err := strconv.ParseInt(typed.StringValue, 10, 64)
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func getBool(attrs []*commonv1.KeyValue, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := getAttr(attrs, key)
		if !ok || value == nil {
			continue
		}
		switch typed := value.Value.(type) {
		case *commonv1.AnyValue_BoolValue:
			return typed.BoolValue, true
		case *commonv1.AnyValue_StringValue:
			parsed, err := strconv.ParseBool(typed.StringValue)
			if err == nil {
				return parsed, true
			}
		}
	}
	return false, false
}

func getAttr(attrs []*commonv1.KeyValue, key string) (*commonv1.AnyValue, bool) {
	for _, kv := range attrs {
		if kv.GetKey() == key {
			return kv.GetValue(), true
		}
	}
	return nil, false
}

func setString(attrs *[]*commonv1.KeyValue, key string, value string) {
	setAttr(attrs, key, stringValue(value))
}

func setStringIfNonEmpty(attrs *[]*commonv1.KeyValue, key string, value string) {
	if value != "" {
		setString(attrs, key, value)
	}
}

func setInt(attrs *[]*commonv1.KeyValue, key string, value int64) {
	setAttr(attrs, key, &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: value}})
}

func setAttr(attrs *[]*commonv1.KeyValue, key string, value *commonv1.AnyValue) {
	for _, kv := range *attrs {
		if kv.GetKey() == key {
			kv.Value = value
			return
		}
	}
	*attrs = append(*attrs, &commonv1.KeyValue{Key: key, Value: value})
}

func stringValue(value string) *commonv1.AnyValue {
	return &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}
}

func anyValueString(value *commonv1.AnyValue) (string, bool) {
	if value == nil {
		return "", false
	}
	switch typed := value.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return typed.StringValue, true
	case *commonv1.AnyValue_IntValue:
		return strconv.FormatInt(typed.IntValue, 10), true
	case *commonv1.AnyValue_DoubleValue:
		return strconv.FormatFloat(typed.DoubleValue, 'f', -1, 64), true
	case *commonv1.AnyValue_BoolValue:
		return strconv.FormatBool(typed.BoolValue), true
	case *commonv1.AnyValue_BytesValue:
		return string(typed.BytesValue), true
	case *commonv1.AnyValue_ArrayValue, *commonv1.AnyValue_KvlistValue:
		encoded, err := json.Marshal(valueToInterface(value))
		if err != nil {
			return fmt.Sprintf("%v", typed), true
		}
		return string(encoded), true
	default:
		return "", false
	}
}

func valueToInterface(value *commonv1.AnyValue) any {
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
		return string(typed.BytesValue)
	case *commonv1.AnyValue_ArrayValue:
		result := make([]any, 0, len(typed.ArrayValue.Values))
		for _, item := range typed.ArrayValue.Values {
			result = append(result, valueToInterface(item))
		}
		return result
	case *commonv1.AnyValue_KvlistValue:
		result := map[string]any{}
		for _, item := range typed.KvlistValue.Values {
			result[item.GetKey()] = valueToInterface(item.GetValue())
		}
		return result
	default:
		return nil
	}
}
