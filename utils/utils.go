package utils

import (
	"log"
	"strconv"
	"strings"
	"time"
)

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

func AbsoluteExpiry(secs int) int64 {
	return AbsoluteTimeNow() + int64(secs)*1000
}

func AbsoluteTimeNow() int64 {
	return time.Now().UnixMilli()
}

func AbsoluteTimeInSeconds(absoluteTime int64) int64 {
	return absoluteTime / 1000
}

func SimilarStrings(a string, b string) bool {
	return strings.EqualFold(a, b)
}

func AbsoluteExpiryInString(secs int) string {
	return strconv.FormatInt(AbsoluteExpiry(secs), 10)
}

func ConvertToAbsoluteExpiry(secs string) (string, error) {
	secsInt, err := strconv.Atoi(secs)
	if err != nil {
		return "", err
	}
	return AbsoluteExpiryInString(secsInt), nil
}
