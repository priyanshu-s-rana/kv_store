package parser

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

type Command struct {
	Name constants.CmdName
	Args []string
}

type Parser struct {
	reader *bufio.Reader
}

// New creates a Parser that reads commands from r.
// @returns *Parser: wraps r in a bufio.Reader internally.
func New(r io.Reader) *Parser {
	return &Parser{
		reader: bufio.NewReader(r),
	}
}

// ReadCommand parses the next command from the stream.
// Auto-detects the wire format from the first byte:
// - '*' starts a RESP array of bulk strings.
// - Anything else is treated as an inline whitespace-separated line.
// @returns Command: the parsed command with uppercased Name and raw Args.
// @returns io.EOF: when the stream is exhausted cleanly.
// @returns error: on malformed input, bad length, or truncated bulk string.
func (p *Parser) ReadCommand() (Command, error) {
	var line string
	var err error
	var cmd Command

	for {
		line, err = p.readLine()
		if err != nil {
			return cmd, err
		}
		if strings.TrimSpace(line) != "" {
			break
		}
	}

	if len(line) == 0 || line[0] != '*' {
		return p.parseLine(line)
	}

	arrLength, err := strconv.Atoi(line[1:])
	if err != nil {
		return cmd, fmt.Errorf(constants.INV_ARRAY_LEN, line)
	}

	if arrLength <= 0 {
		return cmd, fmt.Errorf(constants.INV_CMD_ARRAY_LEN, arrLength)
	}

	parts := make([]string, 0, arrLength)
	for range arrLength {
		cmdString, _, err := p.readBulkString()
		if err != nil {
			return cmd, err
		}
		parts = append(parts, cmdString)
	}

	if len(parts) == 0 {
		return cmd, fmt.Errorf(constants.EMPTY_CMD)
	}

	cmd.Name = constants.CmdName(strings.ToUpper(parts[0]))
	cmd.Args = parts[1:]
	return cmd, nil
}

// readLine reads one '\n'-terminated line from the buffered reader.
// @returns string: line content with trailing "\r\n" or "\n" stripped.
// @returns io.EOF: if the stream closes before a newline is seen.
func (p *Parser) readLine() (string, error) {
	line, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// parseLine parses an inline-format command from a single line.
// Splits on whitespace (collapsing runs) and uppercases the verb.
// @returns Command: Name is parts[0] uppercased, Args is the rest.
// @returns error: if the line contains no tokens.
func (p *Parser) parseLine(line string) (Command, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return Command{}, fmt.Errorf(constants.EMPTY_CMD)
	}

	return Command{
		Name: constants.CmdName(strings.ToUpper(parts[0])),
		Args: parts[1:],
	}, nil
}

// readBulkString reads one RESP bulk string: a "$<len>\r\n" header,
// then exactly <len> bytes, then a trailing "\r\n".
// Payload is read with io.ReadFull so it is binary-safe (CRLF and NUL
// bytes inside the payload are preserved).
// @returns string: the payload bytes as a string.
// @returns isNull: true only for the null bulk string "$-1" — distinct from
// a valid, merely-empty payload ("$0"), which returns ("", false, nil).
// @returns error: on malformed header or truncated payload.
func (p *Parser) readBulkString() (value string, isNull bool, err error) {
	line, err := p.readLine()
	if err != nil {
		return "", false, err
	}

	if len(line) == 0 || line[0] != '$' {
		return "", false, fmt.Errorf(constants.INV_STR_PARSER, line)
	}

	// Null bulk string
	if line == "$-1" {
		return "", true, nil
	}

	length, err := strconv.Atoi(line[1:])
	if err != nil {
		return "", false, fmt.Errorf(constants.INV_STR_PARSER, line)
	}

	buf := make([]byte, length+2) // +2 for \r\n
	_, err = io.ReadFull(p.reader, buf)
	if err != nil {
		return "", false, err
	}

	return string(buf[:length]), false, nil
}
