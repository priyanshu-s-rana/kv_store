package constants

const (
	ONE               = 1
	ZERO              = 0
	NIL               = "$-1\r\n"
	NIL_ARRAY         = "*-1\r\n"
	NIL_DISPLAY       = "(nil)"
	TTL_KEY_NO_EXPIRY = -1
	TTL_KEY_NOT_EXIST = -2
	OK                = "OK"
	PONG              = "PONG"

	// Error messages
	INV_EXPIRY         = "invalid expiry time"
	WRONG_NUM_ARGS     = "wrong number of arguments for %s"
	UNKNOWN_CMD        = "unknown command %s"
	INV_ARR_LEN_PARSER = "invalid array length: %s"
	EMPTY_CMD          = "empty line command"
	INV_ARRAY_LEN      = "invalid array length: %s"
	INV_CMD_ARRAY_LEN  = "invalid command array length: %d"
	INV_STR_PARSER     = "invalid bulk string format: %s"
	INV_STR_LEN_PARSER = "invalid bulk string length: %s"
	NOT_INTEGER        = "value is not integer or out of range"

	// Log messages
	SNAPSHOT_SAVED     = "Snapshot saved successfully to: %s"
	SNAPSHOT_FAILED    = "Failed to save snapshot: %v"
	SNAPSHOT_NOT_FOUND = "No snapshot found to load at: %s"
	SNAPSHOT_LOADED    = "Snapshot loaded: %d keys successfully from: %s"
)
