package constants

type CmdName string

const (
	Ping      CmdName = "PING"
	Get       CmdName = "GET"
	Set       CmdName = "SET"
	Del       CmdName = "DEL"
	TTL       CmdName = "TTL"
	Expire    CmdName = "EXPIRE"
	EVICT     CmdName = "_EVICT"
	Subscribe CmdName = "SUBSCRIBE"
	Publish   CmdName = "PUBLISH"
)
