package util

import (
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"
)

func StructValueToAny(value *structpb.Value) (any, int64, error) {
	if value == nil {
		return nil, 0, nil
	}
	switch value.GetKind().(type) {
	case *structpb.Value_NullValue:
		return nil, 0, nil
	case *structpb.Value_StringValue:
		str := value.GetStringValue()
		return str, int64(len(str)), nil
	case *structpb.Value_NumberValue:
		return value.GetNumberValue(), 8, nil
	case *structpb.Value_BoolValue:
		return value.GetBoolValue(), 1, nil
	case *structpb.Value_StructValue:
		m, n, err := StructToAnyMap(value.GetStructValue())
		return m, n, err
	case *structpb.Value_ListValue:
		list := make([]any, 0, len(value.GetListValue().Values))
		total := int64(0)
		for _, item := range value.GetListValue().Values {
			v, n, err := StructValueToAny(item)
			if err != nil {
				return nil, 0, err
			}
			total += n
			list = append(list, v) // 注意这里 append 值，不是 *any
		}
		return list, total, nil
	default:
		return nil, 0, fmt.Errorf("unsupported value kind: %T", value.GetKind())
	}
}
func StructToAnyMap(s *structpb.Struct) (map[string]any, int64, error) {
	ans := make(map[string]any, len(s.GetFields()))
	totalLength := int64(0)
	for key, value := range s.GetFields() {
		if value == nil {
			ans[key] = nil
			continue
		}
		convertedValue, subLength, err := StructValueToAny(value)
		if err != nil {
			return nil, 0, err
		}
		totalLength += int64(len(key))
		totalLength += subLength
		if !IsASCIIAlphaNumDashUnderscore(key) {
			return nil, 0, fmt.Errorf("invalid key: %s", key)
		}
		ans[key] = convertedValue
	}
	return ans, totalLength, nil
}
