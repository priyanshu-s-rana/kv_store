package models

import (
	"github.com/priyanshu-s-rana/kv_store/constants"
)

// Command represents a client request dispatched to the store's event loop.
type Command struct {
	Name constants.CmdName
	Args []string
	Resp chan Response
}

// NewCommand creates a Command with a buffered response channel ready to receive one reply.
func NewCommand(name constants.CmdName, args []string) Command {
	return Command{
		Name: name,
		Args: args,
		Resp: make(chan Response, 1),
	}
}

// Response carries the RESP-encoded result and any transport-level error back to the caller.
type Response struct {
	Value []byte
}
