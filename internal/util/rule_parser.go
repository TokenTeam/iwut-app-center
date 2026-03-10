package util

import (
	"fmt"
	"strconv"
	"strings"
)

type Rule struct {
	Operator     string  `bson:"operator"`      // AND OR NOT "==" "!=" ">" "<" ">=" "<="
	Rules        *[]Rule `bson:"rules"`         // 递归定义
	Field        *string `bson:"field"`         // 需要比较的字段，可能是用户属性，也可能是环境属性等
	DefaultField *string `bson:"default_field"` // 可选，默认与field相同，如果field在某些情况下不可用，可以使用default_field
	Value        *string `bson:"value"`         // 需要比较的值
}

type LogicFilterItem struct {
	Keys       map[string]any
	FilterFunc func(env map[string]string) (bool, error)
}

type MissingKeyError struct {
	Key string
}

func (e *MissingKeyError) Error() string {
	return fmt.Sprintf("missing key: %s", e.Key)
}

type RuleParser struct {
	logicFuncBuffer map[string]LogicFilterItem
	configCenter    *ConfigCenterUtil
}

var (
	LogicOperators      = map[string]any{"AND": nil, "OR": nil, "NOT": nil}
	ComparisonOperators = map[string]any{"==": nil, "!=": nil, ">": nil, "<": nil, ">=": nil, "<=": nil}
)

func NewRuleParser(configCenterUtil *ConfigCenterUtil) *RuleParser {
	return &RuleParser{
		logicFuncBuffer: make(map[string]LogicFilterItem),
		configCenter:    configCenterUtil,
	}
}

func (r *RuleParser) IsRuleLegal(rule *Rule) (bool, error) {
	if rule == nil {
		return true, nil
	}

	usableScope := r.configCenter.GetPossibleReadScopeWithoutPrefix()
	keys, err := GetExternalFieldKeyAndSimplify(rule)
	if err != nil {
		return false, err
	}
	for k := range keys {
		if _, ok := usableScope[k]; !ok {
			return false, fmt.Errorf("key %s is not in usable scope", k)
		}
	}
	return true, nil
}

func GetExternalFieldKeyAndSimplify(rule *Rule) (map[string]any, error) {
	if rule == nil {
		return nil, fmt.Errorf("rule is nil")
	}
	keys := make(map[string]any)
	if _, ok := LogicOperators[rule.Operator]; ok {
		rule.Field = nil
		rule.DefaultField = nil
		rule.Value = nil
		if rule.Rules != nil {
			for _, r := range *rule.Rules {
				subKeys, err := GetExternalFieldKeyAndSimplify(&r)
				if err != nil {
					return nil, err
				}
				for k := range subKeys {
					keys[k] = nil
				}
			}
		} else {
			return nil, fmt.Errorf("rule is invalid, missing rules for logic operation")
		}
	} else if _, ok := ComparisonOperators[rule.Operator]; ok {
		rule.Rules = nil
		if rule.Field != nil {
			if strings.HasPrefix(*rule.Field, "${") && strings.HasSuffix(*rule.Field, "}") {
				k := (*rule.Field)[2 : len(*rule.Field)-1]
				keys[k] = nil
			} else {
				return nil, fmt.Errorf("rule is invalid, field should be in the format of ${key} for comparison operation")
			}
		} else {
			return nil, fmt.Errorf("rule is invalid, missing field for comparison operation")
		}
		// 对于比较操作，value 可以为 nil，表示与 null 比较，这在某些场景下是有意义的，例如判断某个字段是否存在或是否为 null
		// 请小心对待
	} else {
		return nil, fmt.Errorf("rule is invalid, unknown operator: %s", rule.Operator)
	}
	return keys, nil
}

// GetFilterFunc return: keys, filterFunc, error
func (r *RuleParser) GetFilterFunc(rule *Rule, filterFuncId string) (map[string]any, func(map[string]string) (bool, error), error) {
	if rule == nil {
		return map[string]any{}, func(_ map[string]string) (bool, error) { return true, nil }, nil
	}
	if filterItem, ok := r.logicFuncBuffer[filterFuncId]; ok {
		return filterItem.Keys, filterItem.FilterFunc, nil
	}
	keys, filterFunc, err := convertRuleToFunc(rule)
	usableScope := r.configCenter.GetPossibleReadScopeWithoutPrefix()
	for k := range keys {
		if _, ok := usableScope[k]; !ok {
			return nil, nil, fmt.Errorf("key %s is not in usable scope", k)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	filterItem := LogicFilterItem{
		Keys:       keys,
		FilterFunc: filterFunc,
	}
	r.logicFuncBuffer[filterFuncId] = filterItem
	return keys, filterFunc, nil
}

func (r *RuleParser) GetFilterFuncId(clientId string, internalVersion int32) string {
	return fmt.Sprintf("%s_%d", clientId, internalVersion)
}
func (r *RuleParser) ExpireFilterFunc(filterFuncId string) {
	delete(r.logicFuncBuffer, filterFuncId)
}

func convertRuleToFunc(rule *Rule) (map[string]any, func(map[string]string) (bool, error), error) {
	if rule == nil {
		return nil, nil, fmt.Errorf("rule is nil")
	}
	keys := make(map[string]any)
	if _, ok := LogicOperators[rule.Operator]; ok {
		if rule.Rules == nil {
			return nil, nil, fmt.Errorf("rule is invalid, missing rules for logic operation")
		}
		if rule.Operator == "NOT" && len(*rule.Rules) != 1 {
			return nil, nil, fmt.Errorf("NOT operator must have exactly one sub-rule")
		}
		rule.Field = nil
		rule.DefaultField = nil
		rule.Value = nil
		subFunctions := make([]func(map[string]string) (bool, error), 0, len(*rule.Rules))

		for _, r := range *rule.Rules {
			subKeys, subFunc, err := convertRuleToFunc(&r)
			if err != nil {
				return nil, nil, err
			}
			for k := range subKeys {
				keys[k] = nil
			}
			subFunctions = append(subFunctions, subFunc)
		}
		f := func(env map[string]string) (bool, error) {
			if rule.Operator == "AND" {
				result := true
				for _, subFunc := range subFunctions {
					subResult, err := subFunc(env)
					if err != nil {
						return false, err
					}
					result = result && subResult
					if result == false {
						break
					}
				}
				return result, nil
			} else if rule.Operator == "OR" {
				result := false
				for _, subFunc := range subFunctions {
					subResult, err := subFunc(env)
					if err != nil {
						return false, err
					}
					result = result || subResult
					if result == true {
						break
					}
				}
				return result, nil
			} else if rule.Operator == "NOT" {
				result, err := subFunctions[0](env)
				if err != nil {
					return false, err
				}
				return !result, nil
			}
			return false, fmt.Errorf("unknown operator: %s", rule.Operator)
		}
		return keys, f, nil
	} else if _, ok := ComparisonOperators[rule.Operator]; ok {
		rule.Rules = nil
		k := ""
		if rule.Field != nil {
			if strings.HasPrefix(*rule.Field, "${") && strings.HasSuffix(*rule.Field, "}") {
				k = (*rule.Field)[2 : len(*rule.Field)-1]
				keys[k] = nil
			} else {
				return nil, nil, fmt.Errorf("rule is invalid, field should be in the format of ${key} for comparison operation")
			}
		} else {
			return nil, nil, fmt.Errorf("rule is invalid, missing field for comparison operation")
		}
		operFunc, err := getOperFunc(rule.Operator)
		if err != nil {
			return nil, nil, err
		}

		var (
			defaultReturnValue bool
			defaultErr         error
		)
		if rule.Operator == "==" || rule.Operator == "!=" {
			defaultReturnValue, defaultErr = operFunc(rule.DefaultField, rule.Value)
			f := func(env map[string]string) (bool, error) {
				if val, ok := env[k]; ok {
					return operFunc(val, rule.Value)
				}
				return defaultReturnValue, defaultErr
			}
			return keys, f, nil
		}
		ruleDefaultValue, err := strconv.ParseFloat(*rule.DefaultField, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("default field value is not a valid float: %s", *rule.DefaultField)
		}
		ruleValue, err := strconv.ParseFloat(*rule.Value, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("field value is not a valid float: %s", *rule.Value)
		}
		defaultReturnValue, defaultErr = operFunc(ruleDefaultValue, ruleValue)
		f := func(env map[string]string) (bool, error) {
			if val, ok := env[k]; ok {
				valF, err := strconv.ParseFloat(val, 64)
				if err != nil {
					return false, fmt.Errorf("field value in env is not a valid float: %s", val)
				}
				return operFunc(valF, ruleValue)
			}
			return defaultReturnValue, defaultErr
		}
		return keys, f, nil
	}
	return nil, nil, fmt.Errorf("rule is invalid, unknown operator: %s", rule.Operator)
}

func getOperFunc(oper string) (func(a, b any) (bool, error), error) {
	if oper == "==" || oper == "!=" {
		if oper == "==" {
			return func(a, b any) (bool, error) {
				return a == b, nil
			}, nil
		}
		return func(a, b any) (bool, error) {
			return a != b, nil
		}, nil
	}
	var coreCmp func(a float64, b float64) bool
	switch oper {
	case ">":
		coreCmp = func(a float64, b float64) bool { return a > b }
	case "<":
		coreCmp = func(a float64, b float64) bool { return a < b }
	case ">=":
		coreCmp = func(a float64, b float64) bool { return a >= b }
	case "<=":
		coreCmp = func(a float64, b float64) bool { return a <= b }
	default:
		return nil, fmt.Errorf("unknown operator: %s", oper)
	}
	if coreCmp != nil {
		return func(a, b any) (bool, error) {
			af, aok := a.(float64)
			bf, bok := b.(float64)
			if aok && bok {
				return coreCmp(af, bf), nil
			}
			return false, fmt.Errorf("type assertion to float64 failed")
		}, nil
	}
	return nil, fmt.Errorf("unknown error, oper: %s", oper)
}
