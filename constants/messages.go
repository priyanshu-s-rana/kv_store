package constants

const (
	ONE               = ":1\r\n"
	ZERO              = ":0\r\n"
	NIL               = "$-1\r\n"
	TTL_KEY_NO_EXPIRY = ":-1\r\n"
	TTL_KEY_NOT_EXIST = ":-2\r\n"
	OK                = "+OK\r\n"
	PONG              = "+PONG\r\n"

	// Error messages
	INV_EXPIRY         = "-ERR invalid expiry time\r\n"
	WRONG_NUM_ARGS     = "-ERR wrong number of arguments for %s\r\n"
	UNKNOWN_CMD        = "-ERR unknown command %s\r\n"
	INV_ARR_LEN_PARSER = "invalid array length: %s"
	EMPTY_CMD          = "empty line command"
	INV_ARRAY_LEN      = "invalid array length: %s"
	INV_CMD_ARRAY_LEN  = "invalid command array length: %d"
	INV_STR_PARSER     = "invalid bulk string format: %s"
	INV_STR_LEN_PARSER = "invalid bulk string length: %s"

	// Log messages
	SNAPSHOT_SAVED     = "Snapshot saved successfully to: %s"
	SNAPSHOT_FAILED    = "Failed to save snapshot: %v"
	SNAPSHOT_NOT_FOUND = "No snapshot found to load at: %s"
	SNAPSHOT_LOADED    = "Snapshot loaded: %d keys successfully from: %s"
)
