package drivers

import (
	"fmt"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return &s }

// intProp extracts an int property with a default.
func intProp(props map[string]any, key string, def int) int {
	v, ok := props[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return def
	}
}

// boolProp extracts a bool property with a default.
func boolProp(props map[string]any, key string, def bool) bool {
	v, ok := props[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

// stringSliceProp extracts a string slice from a properties map.
func stringSliceProp(props map[string]any, key string) []string {
	v, ok := props[key]
	if !ok {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		var result []string
		for _, item := range s {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}

// diffOutputs compares desired config against current outputs, returning field changes.
func diffOutputs(desired map[string]any, current map[string]any) []interfaces.FieldChange {
	var changes []interfaces.FieldChange
	for k, desiredVal := range desired {
		currentVal, exists := current[k]
		if !exists || fmt.Sprintf("%v", currentVal) != fmt.Sprintf("%v", desiredVal) {
			changes = append(changes, interfaces.FieldChange{
				Path: k,
				Old:  currentVal,
				New:  desiredVal,
			})
		}
	}
	return changes
}

// diffSlice returns elements in a that are not in b.
func diffSlice(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	var diff []string
	for _, v := range a {
		if _, ok := bSet[v]; !ok {
			diff = append(diff, v)
		}
	}
	return diff
}
