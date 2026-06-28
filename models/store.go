package models

import (
	"errors"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// Command represents a client request dispatched to the store's event loop.
type Command struct {
	Name    constants.CmdName
	Args    []string
	Resp    chan Response
	SkipAof bool
}

// Response carries the RESP-encoded result and any transport-level error back to the caller.
type Response struct {
	Value []byte
}

func (resp *Response) IsError() error {
	if len(resp.Value) > 0 && resp.Value[0] == '-' {
		return errors.New(string(resp.Value))
	}
	return nil
}
