package sdk

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

// parseMGetResponse splits the numbered bulk-string format that MGET returns
// ("1) val\n2) (nil)\n...") into a plain string slice. Missing entries become "".
func parseMGetResponse(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		_, val, found := strings.Cut(line, ") ")
		if !found {
			result[i] = line
			continue
		}
		if val == constants.NIL_DISPLAY {
			result[i] = ""
		} else {
			result[i] = val
		}
	}
	return result
}

// parseSetRequest appends any SET modifiers (EX, NX, XX) to args based on the provided options.
func parseSetRequest(args []string, options ...SetOption) []string {
	setOptions := &setOptions{}
	for _, opt := range options {
		opt(setOptions)
	}
	if setOptions.ex > 0 {
		args = append(args, constants.EX, strconv.Itoa(setOptions.ex))
	}
	if setOptions.nx {
		args = append(args, constants.NX)
	}
	if setOptions.xx {
		args = append(args, constants.XX)
	}

	return args
}

// sendCommand writes a RESP array command and reads the server's response.
func (client *KVStoreClient) sendCommand(msg ...string) (string, error) {

	_, err := client.conn.Write(parser.Array(msg...))
	if err != nil {
		return "", fmt.Errorf("[KVStore Client] error writing to server: %v", err)
	}
	resp, err := parser.ReadResponse(client.reader)
	if err != nil {
		return "", err
	}

	return resp, nil
}

// handleSubscribe opens a dedicated connection for a subscription, reads the
// server's confirmation synchronously, then forwards all subsequent messages
// into the Subscription channel until the connection closes.
func (client *KVStoreClient) handleSubscribe(msg ...string) (*Subscription, error) {
	subscriber, err := NewSubscription(client.addr)
	if err != nil {
		return nil, err
	}
	_, err = subscriber.conn.Write(parser.Array(msg...))
	if err != nil {
		subscriber.Unsubscribe()
		return nil, fmt.Errorf("[KVStore Subscription Client] error writing to server: %v", err)
	}
	first, err := parser.ReadResponse(&subscriber.reader)
	if err != nil {
		subscriber.Unsubscribe()
		return nil, err
	}

	go func() {
		defer close(subscriber.message)
		subscriber.message <- first
		for {
			msg, err := parser.ReadResponse(&subscriber.reader)
			if err != nil {
				return
			}
			subscriber.message <- msg
		}
	}()

	return subscriber, nil
}
