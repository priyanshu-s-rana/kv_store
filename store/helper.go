package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

func set_with_modifiers(s *Store, args []string, e *entry) (Response, bool) {
	n := len(args)
	key := args[0]
	for i := 2; i < n; i++ {
		// Do not set the key if it already exists when NX is specified.
		if args[i] == constants.NX {
			if _, exists := s.data[key]; exists {
				return Response{Value: parser.NullBulkString()}, false
			}
		}
		// Do not set the key if it does not exist when XX is specified.
		if args[i] == constants.XX {
			if _, exists := s.data[key]; !exists {
				return Response{Value: parser.NullBulkString()}, false
			}
		}
		// Set the key with an expiry time in seconds when EX is specified.
		if args[i] == constants.EX {
			if i+1 >= n {
				return Response{Value: parser.Error(constants.INV_EXPIRY)}, false
			}
			secs, err := strconv.Atoi(args[i+1])
			if err != nil || secs < 0 {
				return Response{Value: parser.Error(constants.INV_EXPIRY)}, false
			}
			e.expiry = time.Now().Add(time.Duration(secs) * time.Second)
			s.ttls.Push(ttlItem{key: key, expiresAt: e.expiry})
		}
	}

	return Response{Value: parser.SimpleString(constants.OK)}, true
}

func keyMatcher(pattern string) func(string) bool {
	prefix := pattern[0] == '*'
	suffix := pattern[len(pattern)-1] == '*'
	switch {
	case prefix && suffix:
		if len(pattern) == 1 {
			return func(key string) bool { return true }
		}
		mid := pattern[1 : len(pattern)-1]
		return func(key string) bool { return strings.Contains(key, mid) }
	case prefix:
		return func(key string) bool { return strings.HasSuffix(key, pattern[1:]) }
	case suffix:
		return func(key string) bool { return strings.HasPrefix(key, pattern[:len(pattern)-1]) }
	default:
		return func(key string) bool { return key == pattern }
	}
}

func (s *Store) _default(cmd Command) Response {
	return Response{Value: parser.Error(fmt.Sprintf(constants.UNKNOWN_CMD, cmd.Name))}
}
