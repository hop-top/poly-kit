package wizard

// String returns the string result for key, or "" if missing/wrong type.
func String(results map[string]any, key string) string {
	v, ok := results[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// Bool returns the bool result for key, or false if missing/wrong type.
func Bool(results map[string]any, key string) bool {
	v, ok := results[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		return false
	}
	return b
}

// Strings returns the []string result for key, or nil if missing/wrong type.
func Strings(results map[string]any, key string) []string {
	v, ok := results[key]
	if !ok {
		return nil
	}
	ss, ok := v.([]string)
	if !ok {
		return nil
	}
	return ss
}

// Choice is an alias for String, used with Select steps.
func Choice(results map[string]any, key string) string {
	return String(results, key)
}
