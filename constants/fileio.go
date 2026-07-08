package constants

import "os"

const (
	DirPerm      os.FileMode = 0o755
	FilePerm     os.FileMode = 0o644
	OpenAOF                  = os.O_CREATE | os.O_RDWR | os.O_APPEND
	OpenSnapshot             = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
)
