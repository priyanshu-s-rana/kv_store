package sdk

import (
	"bufio"
	"fmt"
	"net"
)

type Subscription struct {
	conn    net.Conn
	addr    string
	reader  *bufio.Reader
	message chan string
}

// NewSubscription dials address and returns a Subscription with a buffered message channel.
func NewSubscription(address string) (*Subscription, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("[KVStore Subscription Client] error forming connection with server: %v", err)
	}
	return &Subscription{
		conn:    conn,
		addr:    address,
		reader:  bufio.NewReader(conn),
		message: make(chan string, 16),
	}, nil
}

// Unsubscribe closes the server connection, which signals the server to clean up
// the subscription and causes the background goroutine to exit.
func (subscriber *Subscription) Unsubscribe() {
	subscriber.conn.Close()
}

// Message returns a receive-only channel of published messages. The channel is
// closed when the server connection ends, so a range loop over it terminates naturally.
func (subscriber *Subscription) Message() <-chan string {
	return subscriber.message
}

type setOptions struct {
	nx bool
	xx bool
	ex int
}

// SetOption is a functional option for the Set command.
type SetOption func(*setOptions)

// WithNX sets the key only if it does not already exist.
func WithNX() SetOption {
	return func(opt *setOptions) { opt.nx = true }
}

// WithXX sets the key only if it already exists.
func WithXX() SetOption {
	return func(opt *setOptions) { opt.xx = true }
}

// WithEX sets the key to expire after seconds.
func WithEX(seconds int) SetOption {
	return func(opt *setOptions) { opt.ex = seconds }
}
