package models

import (
	"github.com/priyanshu-s-rana/kv_store/constants"
)

type Command struct {
	Name constants.CmdName
	Args []string
	Resp chan Response
}

type Response struct {
	Value []string
	Err   error
}
