package store

import (
	"fmt"
	"strconv"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

func set_with_modifiers(s *Store, args []string, e *entry) (Response, bool) {
	n := len(args)
	key := args[0]
	for i := 2; i < n; i++ {
		// Do not set the key if it already exists when NX is specified.
		if args[i] == constants.NX {
			if _, exists := s.data[key]; exists {
				return Response{Value: []byte(constants.NIL)}, false
			}
		}
		// Do not set the key if it does not exist when XX is specified.
		if args[i] == constants.XX {
			if _, exists := s.data[key]; !exists {
				return Response{Value: []byte(constants.NIL)}, false
			}
		}
		// Set the key with an expiry time in seconds when EX is specified.
		if args[i] == constants.EX {
			if i+1 >= n {
				return Response{Value: []byte(constants.INV_EXPIRY)}, false
			}
			secs, err := strconv.Atoi(args[i+1])
			if err != nil || secs < 0 {
				return Response{Value: []byte(constants.INV_EXPIRY)}, false
			}
			e.expiry = time.Now().Add(time.Duration(secs) * time.Second)
			s.ttls.Push(ttlItem{key: key, expiresAt: e.expiry})
		}
	}

	return Response{Value: []byte(constants.OK)}, true
}

func (s *Store) _default(cmd Command) Response {
	return Response{Value: fmt.Appendf(nil, constants.UNKNOWN_CMD, cmd.Name)}
}
