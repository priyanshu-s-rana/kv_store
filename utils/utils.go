package utils

import (
	"log"

	"github.com/priyanshu-s-rana/kv_store/constants"
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

func Digits(n uint64) int {
	d := 1
	for n >= 10 {
		n /= 10
		d++
	}
	return d
}

func EstimateRespArrayBufferSize(parts []string) int {
	size := 1 + Digits(uint64(len(parts))) + 2
	for _, part := range parts {
		size += EstimateRespStringBufferSize(part)
	}

	return size
}

func EstimateRespStringBufferSize(s string) int {
	return 1 + Digits(uint64(len(s))) + 2 + len(s) + 2
}

// $6\r\n123456\r\n
func EstimateRespUintBufferSize(n uint64) int {
	digits := Digits(n)
	return 1 + Digits(uint64(digits)) + 2 + digits + 2
}

func EstimateRespAOFBufferSize(sequenceID uint64, name constants.CmdName, args []string) int {
	// AOF Command --> str(SequnceID) + SequenceID + str(CmdName) + Args
	size := 1 + Digits(uint64(len(args)+3)) + 2
	size += EstimateRespStringBufferSize(constants.SequenceID)
	size += EstimateRespUintBufferSize(sequenceID)
	size += EstimateRespStringBufferSize(string(name))
	for _, arg := range args {
		size += EstimateRespStringBufferSize(arg)
	}

	return size
}
