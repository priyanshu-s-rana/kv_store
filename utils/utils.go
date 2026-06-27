package utils

import "log"

func ResolveStringFallbacks(parts ...string) string {
	for _, value := range parts {
		if value != "" && value != "None" {
			return value
		}
	}
	return ""
}

func ResolveEnv(parts ...string) string {
	for _, value := range parts {
		if value == "dev" || value == "prod" {
			return value
		}
	}
	log.Printf("[config] unrecognized or missing env, falling back to dev")
	return "dev"
}
