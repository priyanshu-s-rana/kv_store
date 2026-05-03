package constants

type CmdName string

const (
	CmdPing      CmdName = "PING"
	CmdGet       CmdName = "GET"
	CmdSet       CmdName = "SET"
	CmdDel       CmdName = "DEL"
	CmdExpire    CmdName = "EXPIRE"
	CmdSubscribe CmdName = "SUBSCRIBE"
	CmdPublish   CmdName = "PUBLISH"
)
