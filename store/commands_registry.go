package store

import (
	"github.com/priyanshu-s-rana/kv_store/constants"
)

type CommandsMeta struct {
	Handler func(*Store, []string) Response
	IsWrite bool
}

var Registry = map[constants.CmdName]CommandsMeta{
	constants.Ping: {
		Handler: (*Store).ping,
		IsWrite: false,
	},
	constants.Get: {
		Handler: (*Store).get,
		IsWrite: false,
	},
	constants.Set: {
		Handler: (*Store).set,
		IsWrite: true,
	},
	constants.Del: {
		Handler: (*Store).del,
		IsWrite: true,
	},
	constants.Expire: {
		Handler: (*Store).expire,
		IsWrite: true,
	},
	constants.TTL: {
		Handler: (*Store).ttl,
		IsWrite: false,
	},
	constants.Publish: {
		Handler: (*Store).publish,
		IsWrite: false,
	},
	constants.Keys: {
		Handler: (*Store).keys,
		IsWrite: false,
	},
	constants.FlushAll: {
		Handler: (*Store).flushAll,
		IsWrite: true,
	},
	constants.MemoryStats: {
		Handler: (*Store).memoryStats,
		IsWrite: false,
	},
	constants.Mget: {
		Handler: (*Store).mget,
		IsWrite: false,
	},
	constants.Mset: {
		Handler: (*Store).mset,
		IsWrite: true,
	},
	constants.Incr: {
		Handler: (*Store).incr,
		IsWrite: true,
	},
	constants.Decr: {
		Handler: (*Store).decr,
		IsWrite: true,
	},
	constants.EVICT: {
		Handler: (*Store).evict,
		IsWrite: false,
	},
	constants.Checkpoint: {
		Handler: (*Store).checkpoint,
		IsWrite: false,
	},
	constants.CheckpointSuccess: {
		Handler: (*Store).checkpointSuccess,
		IsWrite: false,
	},
	constants.Rebaseline: {
		Handler: (*Store).rebaseline,
		IsWrite: false,
	},
}
