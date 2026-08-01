package constants

const (
	// MaxBulkLen bounds a single RESP bulk string's declared length so a
	// malicious or corrupted length can never trigger an allocation before
	// the payload itself is even read. 512MB matches Redis's
	// proto-max-bulk-len default.
	MaxBulkLen = 512 * 1024 * 1024

	// MaxArrayLen bounds a RESP array's declared element count for the
	// same reason.
	MaxArrayLen = 1024 * 1024

	// MaxLineLen bounds a single RESP header/inline-command line so an
	// attacker who never sends '\r\n' can't force unbounded buffer growth
	// in readLine. 64KB matches Redis's proto-inline-max-size default.
	MaxLineLen = 64 * 1024
)
