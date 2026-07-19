package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/data_type/heap"
	"github.com/priyanshu-s-rana/kv_store/lru"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

// PING command returns PONG, used to check server liveness.
func (s *Store) ping(_ []string) Response {
	return Response{Value: parser.SimpleString(constants.PONG)}
}

// GET command retrieves the value of the specified key.
// @returns <key_value>:  if it exists and is not expired,
// @returns  nil: if the key does not exist or is expired.
func (s *Store) get(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Get))}
	}

	key := args[0]
	e, ok := s.data[key]
	if !ok {
		return Response{Value: parser.NullBulkString()}
	}

	if e.isExpired() {
		deleteKey(s, key)
		return Response{Value: parser.NullBulkString()}
	}

	s.lru.MoveToFront(key)
	return Response{Value: parser.BulkString(string(e.value))}
}

// SET command sets the value of the specified key.
// It supports optional modifiers:
// - NX: Set the key only if it does not already exist.
// - XX: Set the key only if it already exists.
// - EX <seconds>: Set the key with an expiry time in seconds.
// @returns OK: if the command is successful,
func (s *Store) set(args []string) Response {
	if len(args) < 2 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Set))}
	}
	value := args[1]
	e := entry{value: []byte(value)}

	resp, ok := setWithModifiers(s, args, &e)
	if !ok {
		return resp
	}

	makeRoom(s)

	return Response{Value: parser.SimpleString(constants.OK)}
}

// DEL command deletes the specified key.
// @returns 0: if the key does not exist,
// @returns 1: if the key exists and is deleted.
func (s *Store) del(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Del))}
	}

	key := args[0]
	if _, ok := s.data[key]; !ok {
		return Response{Value: parser.Integer(constants.ZERO)}
	}

	deleteKey(s, key)
	s.publish([]string{"lock-released:" + key, "released"})
	return Response{Value: parser.Integer(constants.ONE)}
}

// EXPIRE command sets a ttl on key
// @returns 0: if the key does not exist,
// @returns 1: if the ttl is set successfully.
func (s *Store) expire(args []string) Response {
	if len(args) < 2 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Expire))}
	}

	key := args[0]
	e, ok := s.data[key]
	if !ok {
		return Response{Value: parser.Integer(constants.ZERO)}
	}

	secs, err := strconv.Atoi(args[1])
	if err != nil || secs < 0 {
		return Response{Value: parser.Error(constants.INV_EXPIRY)}
	}

	expiry := time.Now().Add(time.Duration(secs) * time.Second)
	newEntry := entry{
		value:  e.value,
		expiry: expiry,
	}
	item := ttlItem{key: key, expiresAt: expiry}
	s.ttls.Push(item)

	setKey(s, key, &newEntry, &item)
	makeRoom(s)
	return Response{Value: parser.Integer(constants.ONE)}
}

// TTL command returns the remaining time to live of a key that has an expiry set.
// @returns -2: if the key does not exist,
// @returns -1: if the key exists but has no expiry, and the TTL in seconds otherwise.
func (s *Store) ttl(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.TTL))}
	}

	key := args[0]
	e, ok := s.data[key]
	if !ok {
		return Response{Value: parser.Integer(constants.TTL_KEY_NOT_EXIST)}
	}
	if e.isExpired() {
		return Response{Value: parser.Integer(constants.TTL_KEY_NOT_EXIST)}
	}
	if !e.hasExpiry() {
		return Response{Value: parser.Integer(constants.TTL_KEY_NO_EXPIRY)}
	}

	ttl := time.Until(e.expiry).Seconds()
	return Response{Value: parser.Integer(int(ttl))}
}

// SUBSCRIBE command allows clients to subscribe to a topic.
// @returns a channel to which client can listen to.
func (s *Store) Subscribe(topic string) chan []byte {
	ch := make(chan []byte, 16)

	s.mut.Lock()
	isNewTopic := len(s.pubsub[topic]) == 0
	s.pubsub[topic] = append(s.pubsub[topic], ch)
	s.mut.Unlock()

	if isNewTopic {
		s.memoryProfile.recordPubSubTopicSize(topic)
		s.pubSubStats.incActiveTopics()
	}
	s.memoryProfile.recordPubSubSubscriber()
	s.pubSubStats.incActiveSubscribers()
	return ch
}

// Unsubscribe removes ch from the subscriber list for topic.
func (s *Store) Unsubscribe(topic string, ch chan []byte) {
	s.mut.Lock()

	subs := s.pubsub[topic]
	for i, subChan := range subs {
		if subChan == ch {
			s.pubsub[topic] = append(subs[:i], subs[i+1:]...)
			break
		}
	}

	isTopicEmpty := len(s.pubsub[topic]) == 0
	s.mut.Unlock()

	if isTopicEmpty {
		s.memoryProfile.recordPubSubTopicRemove(topic)
		s.pubSubStats.decActiveTopics()
	}
	s.memoryProfile.recordPubSubSubscriberRemove()
	s.pubSubStats.decActiveSubscribers()

}

// PUBLISH command sends a message to all subscribers of the given topic.
// @returns the number of subscribers that received the message.
func (s *Store) publish(args []string) Response {
	start := time.Now()
	defer func() { s.metrics.ObservePublishDuration(time.Since(start)) }()

	if len(args) < 2 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Publish))}
	}

	topic, message := args[0], strings.Join(args[1:], " ")

	s.mut.Lock()
	subs := make([]chan []byte, len(s.pubsub[topic]))
	copy(subs, s.pubsub[topic])
	s.mut.Unlock()

	delivered := 0
	for _, subChan := range subs {
		select {
		case subChan <- []byte(message):
			delivered++
			s.metrics.IncMessagesPublished()
		default:
		}
	}

	return Response{Value: parser.Integer(delivered)}
}

// evict removes all expired keys whose TTL entries have reached the front of the heap.
func (s *Store) evict(_ []string) Response {
	start := time.Now()
	defer func() { s.metrics.ObserveTTLExpiryDuration(time.Since(start)) }()

	now := time.Now()
	for s.ttls.Len() > 0 {
		item, ok := s.ttls.Peek()
		if !ok || item.expiresAt.After(now) {
			break
		}
		if popped, ok := s.ttls.Pop(); ok {
			s.memoryProfile.recordTTLRemove(&popped)
		}
		if e, ok := s.data[item.key]; ok && e.isExpired() {
			deleteKey(s, item.key)
			s.metrics.IncExpiredKeys()
			s.publish([]string{"lock-released:" + item.key, "released"})
		}
	}
	return Response{}
}

// KEYS command returns all keys matching the given glob-style pattern.
// @returns bulk string with keys numbered and newline-separated.
func (s *Store) keys(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Keys))}
	}
	var keys []string
	keyCount := 1
	matchFn := keyMatcher(args[0])
	for key := range s.data {
		if matchFn(key) {
			keys = append(keys, fmt.Sprintf("%d) %s", keyCount, key))
			keyCount++
		}
	}

	return Response{Value: parser.BulkString(strings.Join(keys, "\n"))}
}

// FLUSHALL command deletes all keys and resets the TTL heap and LRU.
// @returns OK: always.
func (s *Store) flushAll(_ []string) Response {
	clear(s.data)
	s.ttls = heap.New[ttlItem](func(a, b ttlItem) bool {
		return a.expiresAt.Before(b.expiresAt)
	})
	s.lru = lru.New()
	s.memoryProfile.resetAll()
	return Response{Value: parser.SimpleString(constants.OK)}
}

// MEMORY STATS command returns a flat array of memory metric key-value pairs.
func (s *Store) memoryStats(_ []string) Response {
	return Response{Value: parser.BulkString(s.memoryProfile.getStats())}
}

// MGET command returns the values of all specified keys.
// Missing or expired keys show as (nil). Response is a bulk string with numbered lines,
// matching the same format as the KEYS command.
func (s *Store) mget(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Mget))}
	}
	values := mgetValues(s, args)
	entries := make([]string, len(values))
	for i, v := range values {
		if v == "" {
			entries[i] = fmt.Sprintf("%d) %s", i+1, constants.NIL_DISPLAY)
		} else {
			entries[i] = fmt.Sprintf("%d) %s", i+1, v)
		}
	}
	return Response{Value: parser.BulkString(strings.Join(entries, "\n"))}
}

// MSET command sets multiple key-value pairs atomically.
// @returns OK: always.
func (s *Store) mset(args []string) Response {
	keyCount, ok := msetResponseCheck(args)
	if !ok {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Mset))}
	}
	msetPairs(s, args, keyCount)
	makeRoom(s)
	return Response{Value: parser.SimpleString(constants.OK)}
}

// INCR command increments the integer value of key by 1. Initialises to 1 if missing.
// @returns the new integer value, or an error if the value is not an integer.
func (s *Store) incr(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Incr))}
	}
	return incrBy(s, args[0], 1)
}

// DECR command decrements the integer value of key by 1. Initialises to -1 if missing.
// @returns the new integer value, or an error if the value is not an integer.
func (s *Store) decr(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Decr))}
	}
	return incrBy(s, args[0], -1)
}

// snapshot is the event-loop handler for the internal Snapshot command. It captures
// a copy of the live (non-expired) data and hands it back on snapResp.
func (s *Store) checkpoint(_ []string) Response {
	snapshot, err := s.capture()
	if err != nil {
		return Response{Value: parser.Error(fmt.Sprintf(constants.CAPTURE_ERROR, err))}
	}

	if err := s.persistence.Checkpoint(snapshot); err != nil {
		return Response{Value: parser.Error(fmt.Sprintf(constants.CHECKPOINT_ERROR, err))}
	}

	return Response{Value: parser.SimpleString(constants.OK)}
}

func (s *Store) rebaseline(_ []string) Response {
	snapshot, err := s.capture()
	if err != nil {
		return Response{Value: parser.Error(fmt.Sprintf(constants.CAPTURE_ERROR, err))}
	}

	if err := s.persistence.Rebaseline(snapshot); err != nil {
		return Response{Value: parser.Error(constants.REBASELINE_ERROR)}
	}

	return Response{Value: parser.SimpleString(constants.OK)}
}
