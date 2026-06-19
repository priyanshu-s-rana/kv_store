package parser

import (
	"fmt"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// Integer encodes n as a RESP integer (":n\r\n").
func Integer(n int) []byte {
	return fmt.Appendf(nil, ":%d\r\n", n)
}

// BulkString encodes s as a RESP bulk string ("$len\r\ns\r\n").
func BulkString(s string) []byte {
	return fmt.Appendf(nil, "$%d\r\n%s\r\n", len(s), s)
}

// Array encodes parts as a RESP array of bulk strings.
func Array(parts ...string) []byte {
	resp := fmt.Appendf(nil, "*%d\r\n", len(parts))
	for _, part := range parts {
		resp = append(resp, BulkString(part)...)
	}
	return resp
}

// Error encodes msg as a RESP error ("-ERR msg\r\n").
func Error(msg string) []byte {
	return fmt.Appendf(nil, "-ERR %s\r\n", msg)
}

// SimpleString encodes msg as a RESP simple string ("+msg\r\n").
func SimpleString(msg string) []byte {
	return fmt.Appendf(nil, "+%s\r\n", msg)
}

// NullBulkString returns the RESP null bulk string ("$-1\r\n"), used to represent nil.
func NullBulkString() []byte {
	return []byte(constants.NIL)
}

// NullArray returns the RESP null array ("*-1\r\n").
func NullArray() []byte {
	return []byte(constants.NIL_ARRAY)
}
