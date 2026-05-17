package constants

const (
	ONE               = ":1\r\n"
	ZERO              = ":0\r\n"
	NIL               = "$-1\r\n"
	TTL_KEY_NO_EXPIRY = ":-1\r\n"
	TTL_KEY_NOT_EXIST = ":-2\r\n"
	OK                = "+OK\r\n"
	PONG              = "+PONG\r\n"
	INV_EXPIRY        = "-ERR invalid expiry time\r\n"
	WRONG_NUM_ARGS    = "-ERR wrong number of arguments for %s\r\n"
	UNKNOWN_CMD 	  = "-ERR unknown command %s\r\n"
)
