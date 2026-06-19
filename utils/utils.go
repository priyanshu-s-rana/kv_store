package utils

func ResolveStringFallbacks(parts ...string) string {
	for _, value := range parts {
		if value != "" && value != "None" {
			return value
		}
	}
	return ""
}
