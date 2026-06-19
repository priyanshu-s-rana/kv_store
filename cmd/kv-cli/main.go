package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/chzyer/readline"
	"github.com/priyanshu-s-rana/kv_store/config"
	"github.com/priyanshu-s-rana/kv_store/parser"
	"github.com/priyanshu-s-rana/kv_store/utils"
)

func main() {
	hostFlag := flag.String("h", "", "server host, overrides config.yaml")
	portFlag := flag.String("p", "", "server port, overrides config.yaml")
	flag.Parse()

	config.SetConfig()

	host := utils.ResolveStringFallbacks(*hostFlag, config.CONFIG.Server.Host, "localhost")
	port := utils.ResolveStringFallbacks(*portFlag, config.CONFIG.Server.Port, "5040")
	addr := host + ":" + port

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("[cli] error connecting to server at: %s, err: %v", addr, err)
	}
	defer conn.Close()

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          fmt.Sprintf("%s> ", addr),
		InterruptPrompt: "^C",
	})
	if err != nil {
		log.Fatalf("[cli] readline init: %v", err)
	}
	defer rl.Close()

	serverReader := bufio.NewReader(conn)
	for {
		command, err := rl.Readline()
		if err != nil {
			fmt.Println("\nBye!")
			return
		}

		trimmed := strings.TrimSpace(command)
		if trimmed == "" {
			continue
		}
		if trimmed == "exit" || trimmed == "quit" {
			fmt.Println("Bye!")
			return
		}

		conn.Write(parser.Array(strings.Fields(trimmed)...))

		serverResponse, err := readServerResponse(serverReader)
		if err != nil {
			fmt.Println("Error: " + err.Error())
			continue
		}
		fmt.Println(serverResponse)
	}
}

// readServerResponse reads one RESP message from serverReader and returns it as a string.
func readServerResponse(serverReader *bufio.Reader) (string, error) {
	data, err := serverReader.Peek(1)
	if len(data) == 0 || err != nil {
		return "", fmt.Errorf("[cli] error reading server response: %v", err)
	}

	switch data[0] {
	case '+':
		return parser.DecodeSimpleString(serverReader, data), nil
	case '-':
		return parser.DecodeError(serverReader, data), nil
	case ':':
		return parser.DecodeInteger(serverReader, data), nil
	case '$':
		return parser.DecodeBulkString(serverReader, data), nil
	case '*':
		return parser.DecodeArray(serverReader, data), nil
	default:
		return serverReader.ReadString('\n')
	}
}
