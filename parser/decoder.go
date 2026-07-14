package parser

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// ReadResponse reads one complete RESP message from reader and returns it as a
// human-readable string. Returns a non-nil error for both connection failures
// and server-side RESP error responses (type '-'), so callers can use a single
// err != nil check without inspecting the string.
func ReadResponse(reader *bufio.Reader) (string, error) {
	data, err := reader.Peek(1)
	if len(data) == 0 || err != nil {
		return "", fmt.Errorf("error reading server response: %v", err)
	}

	switch data[0] {
	case '+':
		return DecodeSimpleString(reader, data), nil
	case '-':
		msg := DecodeError(reader, data)
		return msg, fmt.Errorf("%s", msg)
	case ':':
		return DecodeInteger(reader, data), nil
	case '$':
		return DecodeBulkString(reader, data), nil
	case '*':
		return DecodeArray(reader, data), nil
	default:
		return reader.ReadString('\n')
	}
}

// DecodeArray parses a RESP array and returns its string elements.
// @returns []string: the decoded elements in order.
// @returns nil: if data is not a valid RESP array.
func DecodeArray(reader *bufio.Reader, data []byte) string {
	var p *Parser
	if reader != nil {
		p = New(reader)
	} else {
		p = New(bytes.NewReader(data))
	}

	line, err := p.readLine()
	if err != nil {
		return "(error) " + err.Error()
	}
	if len(line) == 0 || line[0] != '*' {
		return ""
	}

	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return "(error) " + err.Error()
	}
	if n <= 0 {
		return ""
	}

	parts := make([]string, n)
	for i := range n {
		parts[i], _, err = p.readBulkString()
		if err != nil {
			return "(error) " + err.Error()
		}
	}

	return strings.Join(parts, " ")
}

// decodeSingleLine is a helper function to decode RESP simple strings and errors by stripping the first character and the trailing '\r\n'.
// @returns string: the decoded string without the leading character and trailing '\r\n', or an empty string if data is too short.
func decodeSingleLine(reader *bufio.Reader, data []byte) string {
	if reader != nil {
		p := New(reader)
		line, err := p.readLine()
		if len(line) < 2 || err != nil {
			return ""
		}

		return line[1:]
	}

	if len(data) < 3 {
		return ""
	}
	return string(data[1 : len(data)-2])
}

// DecodeSimpleString parses a RESP simple string and returns its value.
// @returns string: the decoded simple string without the leading '+' and trailing '\r\n'.
func DecodeSimpleString(reader *bufio.Reader, data []byte) string {
	return decodeSingleLine(reader, data)
}

// DecodeError parses a RESP error and returns its message.
// @returns string: the decoded error message without the leading '-' and trailing '\r\n'.
func DecodeError(reader *bufio.Reader, data []byte) string {
	return decodeSingleLine(reader, data)
}

// DecodeInteger parses a RESP integer and returns its string representation.
// @returns string: the decoded integer as a string without the leading ':' and trailing '\r\n'.
func DecodeInteger(reader *bufio.Reader, data []byte) string {
	return decodeSingleLine(reader, data)
}

// DecodeBulkString parses a RESP bulk string and returns its value.
// @returns string: the decoded bulk string without the leading '$' and trailing '\r\n',
// distinguishing a genuinely empty payload ("$0", returns "") from a null
// bulk string ("$-1", returns "nil") — the two are not the same value.
func DecodeBulkString(reader *bufio.Reader, data []byte) string {
	if reader != nil {
		p := New(reader)
		value, isNull, err := p.readBulkString()
		if err != nil {
			return "(error) " + err.Error()
		}
		if isNull {
			return "nil"
		}
		return value
	}

	p := New(bytes.NewReader(data))

	value, isNull, err := p.readBulkString()
	if err != nil {
		return "nil"
	}
	if isNull {
		return "nil"
	}

	return value
}
