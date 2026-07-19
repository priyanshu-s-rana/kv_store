package parser

import (
	"fmt"
	"strconv"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/utils"
)

// Integer encodes n as a RESP integer (":n\r\n").
func Integer(n int) []byte {
	return fmt.Appendf(nil, ":%d\r\n", n)
}

// BulkString encodes s as a RESP bulk string ("$len\r\ns\r\n").
func BulkString(s string) []byte {
	resp := make([]byte, 0, utils.EstimateRespStringBufferSize(s))

	// Header
	resp = append(resp, '$')
	resp = strconv.AppendInt(resp, int64(len(s)), 10)
	resp = append(resp, '\r', '\n')

	// Body
	resp = append(resp, s...)
	resp = append(resp, '\r', '\n')

	return resp
}

func appendBulkString(buf []byte, s string) []byte {
	// Header
	buf = append(buf, '$')
	buf = strconv.AppendInt(buf, int64(len(s)), 10)
	buf = append(buf, '\r', '\n')

	// Body
	buf = append(buf, s...)
	buf = append(buf, '\r', '\n')

	return buf
}

// Array encodes parts as a RESP array of bulk strings.
func Array(parts ...string) []byte {
	resp := make([]byte, 0, utils.EstimateRespArrayBufferSize(parts))

	// Header
	resp = append(resp, '*')
	resp = strconv.AppendInt(resp, int64(len(parts)), 10)
	resp = append(resp, '\r', '\n')

	// Body
	for _, part := range parts {
		resp = appendBulkString(resp, part)
	}
	return resp
}

func appendBulkUint(buf []byte, val uint64) []byte {
	// Header
	buf = append(buf, '$')
	buf = strconv.AppendInt(buf, int64(utils.Digits(val)), 10)
	buf = append(buf, '\r', '\n')

	// Body
	buf = strconv.AppendUint(buf, val, 10)
	buf = append(buf, '\r', '\n')

	return buf
}

func AOFArray(sequenceID uint64, command constants.CmdName, args []string) []byte {
	resp := make([]byte, 0, utils.EstimateRespAOFBufferSize(sequenceID, command, args))

	// Header
	resp = append(resp, '*')
	resp = strconv.AppendInt(resp, int64(3+len(args)), 10)
	resp = append(resp, '\r', '\n')

	// Body
	resp = appendBulkString(resp, constants.SequenceID)
	resp = appendBulkUint(resp, sequenceID)
	resp = appendBulkString(resp, string(command))
	for _, arg := range args {
		resp = appendBulkString(resp, arg)
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
