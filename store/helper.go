package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

// set_with_modifiers applies NX, XX, and EX modifiers to the entry before it is stored.
// @returns Response, false: if a modifier condition blocks the write,
// @returns OK, true: if all modifiers passed.
func setWithModifiers(s *Store, args []string, e *entry) (Response, bool) {
	n := len(args)
	key := args[0]
	var item *ttlItem
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
			t := ttlItem{key: key, expiresAt: e.expiry}
			s.ttls.Push(t)
			item = &t
		}
	}

	setKey(s, key, e, item)
	return Response{Value: parser.SimpleString(constants.OK)}, true
}

// keyMatcher returns a function that reports whether a key matches the glob-style pattern.
// Supports leading *, trailing *, both (* contains *), and exact match.
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

// _default returns an unknown command error for any unrecognised command name.
func (s *Store) _default(cmd Command) Response {
	return Response{Value: parser.Error(fmt.Sprintf(constants.UNKNOWN_CMD, cmd.Name))}
}

// deleteKey removes key from data, LRU, and memory profile atomically.
// The LRU node is fetched before removal so memory accounting is correct.
func deleteKey(s *Store, key string) {
	if value, ok := s.data[key]; ok {
		node := s.lru.GetNode(key) // get before Remove clears lruIndex
		s.lru.Remove(key)
		s.memoryProfile.recordDataRemove(key, value)
		s.memoryProfile.recordLRURemove(node)
		delete(s.data, key)
	}
}

// setKey writes e into the store and updates the memory profile and LRU.
// If the key is new, full key+value+LRU overhead is charged; if it already
// exists, only the value-size diff is charged.
// item is the TTL entry to charge; nil is safe and means no TTL overhead.
func setKey(s *Store, key string, e *entry, item *ttlItem) {
	_, isNew := s.data[key]
	isNew = !isNew
	if isNew {
		s.memoryProfile.recordDataSize(key, e)
	} else {
		s.memoryProfile.updateValueSize(s.data[key].value, e.value)
	}
	s.memoryProfile.recordTTLSize(item)
	s.lru.MoveToFront(key)
	if isNew {
		s.memoryProfile.recordLRUSize(s.lru.GetNode(key))
	}
	s.data[key] = e
}

// makeRoom evicts least-recently-used keys until the store is within maxBytes.
// Uses PeekBack so deleteKey can fetch the LRU node before removal and keep
// memory accounting accurate.
func makeRoom(s *Store) {
	for s.memoryProfile.isOverLimit() {
		key, ok := s.lru.PeekBack()
		if !ok {
			break
		}
		deleteKey(s, key)
	}
}

// incrBy atomically adjusts the integer value of key by delta.
// If the key is missing or expired it is initialised to delta.
// @returns Response with the new integer value, or an error if the value is not an integer.
func incrBy(s *Store, key string, delta int) Response {
	valueEntry, ok := s.data[key]
	if !ok || valueEntry.isExpired() {
		if ok {
			deleteKey(s, key)
		}
		e := entry{value: []byte(strconv.Itoa(delta))}
		setKey(s, key, &e, nil)
		makeRoom(s)
		return Response{Value: parser.Integer(delta)}
	}
	value, err := strconv.Atoi(string(valueEntry.value))
	if err != nil {
		return Response{Value: parser.Error(constants.NOT_INTEGER)}
	}
	value += delta
	e := entry{value: []byte(strconv.Itoa(value)), expiry: valueEntry.expiry}
	setKey(s, key, &e, nil)
	makeRoom(s)
	return Response{Value: parser.Integer(value)}
}

// @returns (keyCount, valid )
func msetResponseCheck(args []string) (keyCount int, valid bool) {
	if len(args) < 2 {
		return 0, false
	}
	return len(args) / 2, len(args)%2 == 0
}

// mgetValues looks up each key in keys and returns a string slice of their values.
// Missing or expired keys are replaced with "" (empty string, encoded as a null bulk string by the caller); expired keys are lazily deleted.
func mgetValues(s *Store, keys []string) []string {
	result := make([]string, len(keys))
	for i, key := range keys {
		e, ok := s.data[key]
		if !ok || e.isExpired() {
			if ok {
				deleteKey(s, key)
			}
			result[i] = ""
			continue
		}
		s.lru.MoveToFront(key)
		result[i] = string(e.value)
	}
	return result
}

// msetPairs writes keyCount key-value pairs from args (interleaved as k,v,k,v,...) into the store.
func msetPairs(s *Store, args []string, keyCount int) {
	for i := range keyCount {
		e := entry{value: []byte(args[i*2+1])}
		setKey(s, args[i*2], &e, nil)
	}
}
