package store

import (
	"github.com/priyanshu-s-rana/kv_store/constants"
)

type commandsMeta struct {
	handler func(*Store, []string) Response
	isWrite bool
}

var registry = map[constants.CmdName]commandsMeta{
	constants.Ping: {
		handler: (*Store).ping,
		isWrite: false,
	},
	constants.Get: {
		handler: (*Store).get,
		isWrite: false,
	},
	constants.Set: {
		handler: (*Store).set,
		isWrite: true,
	},
	constants.Del: {
		handler: (*Store).del,
		isWrite: true,
	},
	constants.PExpireAt: {
		handler: (*Store).pexpireAt,
		isWrite: true,
	},
	constants.TTL: {
		handler: (*Store).ttl,
		isWrite: false,
	},
	constants.Publish: {
		handler: (*Store).publish,
		isWrite: false,
	},
	constants.Keys: {
		handler: (*Store).keys,
		isWrite: false,
	},
	constants.FlushAll: {
		handler: (*Store).flushAll,
		isWrite: true,
	},
	constants.MemoryStats: {
		handler: (*Store).memoryStats,
		isWrite: false,
	},
	constants.Mget: {
		handler: (*Store).mget,
		isWrite: false,
	},
	constants.Mset: {
		handler: (*Store).mset,
		isWrite: true,
	},
	constants.Incr: {
		handler: (*Store).incr,
		isWrite: true,
	},
	constants.Decr: {
		handler: (*Store).decr,
		isWrite: true,
	},
	constants.EVICT: {
		handler: (*Store).evict,
		isWrite: false,
	},
	constants.Checkpoint: {
		handler: (*Store).checkpoint,
		isWrite: false,
	},
	constants.Rebaseline: {
		handler: (*Store).rebaseline,
		isWrite: false,
	},
}
