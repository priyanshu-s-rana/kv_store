package constants

type CmdName string

const (
	Ping              CmdName = "PING"
	Get               CmdName = "GET"
	Set               CmdName = "SET"
	Mset              CmdName = "MSET"
	Mget              CmdName = "MGET"
	Del               CmdName = "DEL"
	TTL               CmdName = "TTL"
	Expire            CmdName = "EXPIRE"
	Incr              CmdName = "INCR"
	Decr              CmdName = "DECR"
	EVICT             CmdName = "_EVICT"
	Subscribe         CmdName = "SUBSCRIBE"
	Publish           CmdName = "PUBLISH"
	Snapshot          CmdName = "_SNAPSHOT"
	FlushAll          CmdName = "FLUSHALL"
	Keys              CmdName = "KEYS"
	MemoryStats       CmdName = "MEMORYSTATS"
	Checkpoint        CmdName = "CHECKPOINT"
	CheckpointSuccess CmdName = "CHECKPOINT_SUCCESS"
	Rebaseline        CmdName = "REBASELINE"
)
