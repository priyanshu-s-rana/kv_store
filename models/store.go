package models

import (
	"github.com/priyanshu-s-rana/kv_store/constants"
)

type Command struct {
	Name constants.CmdName
	Args []string
	Resp chan Response
}

func NewCommand(name constants.CmdName, args []string) Command {
	return Command{
		Name: name,
		Args: args,
		Resp: make(chan Response, 1),
	}
}

type Response struct {
	Value []byte
	Err   error
}
