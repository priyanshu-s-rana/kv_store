package parser

import (
	"fmt"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

func Integer(n int) []byte {
	return fmt.Appendf(nil, ":%d\r\n", n)
}

func BulkString(s string) []byte {
	return fmt.Appendf(nil, "$%d\r\n%s\r\n", len(s), s)
}

func Array(parts ...string) []byte {
	resp := fmt.Appendf(nil, "*%d\r\n", len(parts))
	for _, part := range parts {
		resp = append(resp, BulkString(part)...)
	}
	return resp
}

func Error(msg string) []byte {
	return fmt.Appendf(nil, "-ERR %s\r\n", msg)
}

func SimpleString(msg string) []byte {
	return fmt.Appendf(nil, "+%s\r\n", msg)
}

func NullBulkString() []byte {
	return []byte(constants.NIL)
}

func NullArray() []byte {
	return []byte(constants.NIL_ARRAY)
}
