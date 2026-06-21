package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
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

	conn, serverReader := mustConnect(addr)
	defer conn.Close()

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          fmt.Sprintf("%s> ", addr),
		InterruptPrompt: "^C",
	})
	if err != nil {
		log.Fatalf("[cli] readline init: %v", err)
	}
	defer rl.Close()

	for {
		command, err := rl.Readline()
		if isInterupted(err, rl) {
			return
		}

		trimmed := strings.TrimSpace(command)
		if trimmed == "" {
			continue
		}
		if isExitCmd(trimmed) {
			fmt.Fprintln(rl.Stdout(), "Bye!")
			return
		}

		fields := strings.Fields(trimmed)

		if strings.ToUpper(fields[0]) == "SUBSCRIBE" {
			enterSubscribeMode(fields, rl, addr)
			continue
		}

		conn.Write(parser.Array(fields...))

		resp, err := readServerResponse(serverReader)
		if err != nil {
			if resp != "" {
				fmt.Fprintln(rl.Stdout(), resp) // server-side RESP error
			} else {
				fmt.Fprintln(rl.Stdout(), "Error:", err) // connection error
			}
			continue
		}

		fmt.Fprintln(rl.Stdout(), resp)
	}
}

// isInterupted reports whether a readline error means the session should end.
func isInterupted(err error, rl *readline.Instance) bool {
	if err != nil {
		fmt.Fprintln(rl.Stdout(), "\nBye!")
		return true
	}
	return false
}

// isExitCmd reports whether the user typed an explicit exit command.
func isExitCmd(cmd string) bool {
	return cmd == "exit" || cmd == "quit"
}

// enterSubscribeMode opens a dedicated connection for the subscription so the
// caller's main connection is never touched. It sends the SUBSCRIBE command,
// reads the first response (confirmation or error), and if successful starts a
// goroutine to print push messages until Ctrl+C closes the dedicated connection.
func enterSubscribeMode(fields []string, rl *readline.Instance, addr string) {
	subConn, subReader := mustConnect(addr)
	defer subConn.Close()

	subConn.Write(parser.Array(fields...))

	resp, err := readServerResponse(subReader)
	fmt.Fprintln(rl.Stdout(), resp)
	if err != nil {
		return
	}

	rl.SetPrompt("=> ")
	go readLoop(subReader, rl.Stdout())

	for {
		_, err := rl.Readline()
		if err == readline.ErrInterrupt || err == io.EOF {
			break
		}
	}

	fmt.Fprintln(rl.Stdout(), "unsubscribed")
	rl.SetPrompt(fmt.Sprintf("%s> ", addr))
}

// mustConnect dials addr and returns the connection with a fresh buffered reader.
// Calls log.Fatalf on failure since the CLI cannot function without a server connection.
func mustConnect(addr string) (net.Conn, *bufio.Reader) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("[cli] connect to %s failed: %v", addr, err)
	}
	return conn, bufio.NewReader(conn)
}

// readLoop drains reader and writes each decoded RESP message to out until an error occurs.
// Intended to run as a goroutine during subscribe mode; exits when the connection closes.
func readLoop(reader *bufio.Reader, out io.Writer) {
	for {
		msg, err := readServerResponse(reader)
		if err != nil {
			return
		}
		fmt.Fprintln(out, msg)
	}
}

// readServerResponse reads one RESP message from serverReader and returns it as a string.
func readServerResponse(serverReader *bufio.Reader) (string, error) {
	return parser.ReadResponse(serverReader)
}
