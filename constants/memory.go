package constants

const (
	// Per-Key Overhead
	ENTRY_OVERHEAD    int64 = 48 // []byte header(24) + time.Time(24)
	TTL_ITEM_OVERHEAD int64 = 40 // string header(16) + time.Time(24)
	LRU_NODE_OVERHEAD int64 = 32 // next ptr(8) + prev ptr(8) + string header(16)

	// Fixed Overhead
	LRU_OVERHEAD    int64 = 24 // head ptr(8) + tail ptr(8) + map header(8)
	HEAP_OVERHEAD   int64 = 32 // []T slice header(24) + func ptr(8)
	STORE_OVERHEAD  int64 = 48 // map(8) + chan(8) + heap ptr(8) + map(8) + sync.Mutex(8) + chan(8)
	SERVER_OVERHEAD int64 = 32 // string header(16) + chan(8) + store ptr(8)

	// Primitive Overhead (header/struct size in bytes on 64-bit)
	STRING_OVERHEAD       int64 = 16 // ptr(8) + len(8)
	INT_OVERHEAD          int64 = 8
	INT64_OVERHEAD        int64 = 8
	BYTE_SLICE_OVERHEAD   int64 = 24 // ptr(8) + len(8) + cap(8)
	BYTE_CHANNEL_OVERHEAD int64 = 8  // ptr(8)
)
