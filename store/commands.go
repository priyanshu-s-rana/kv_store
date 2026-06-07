package store

import (
	"fmt"
	"strconv"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

func (s *Store) ping() Response {
	return Response{Value: parser.SimpleString(constants.PONG)}
}

// GET command retrieves the value of the specified key.
// @returns <key_value>:  if it exists and is not expired,
// @returns  nil: if the key does not exist or is expired.
func (s *Store) get(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, "GET"))}
	}

	key := args[0]
	e, ok := s.data[key]
	if !ok {
		return Response{Value: parser.NullBulkString()}
	}

	if e.isExpired() {
		delete(s.data, key)
		return Response{Value: parser.NullBulkString()}
	}

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
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, "SET"))}
	}
	key, value := args[0], args[1]
	e := entry{value: []byte(value)}

	resp, ok := set_with_modifiers(s, args, &e)
	if !ok {
		return resp
	}

	s.data[key] = &e
	return Response{Value: parser.SimpleString(constants.OK)}
}

// DEL command deletes the specified key.
// @returns 0: if the key does not exist,
// @returns 1: if the key exists and is deleted.
func (s *Store) del(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, "DEL"))}
	}

	key := args[0]
	_, ok := s.data[key]
	if !ok {
		return Response{Value: parser.Integer(constants.ZERO)}
	}

	delete(s.data, key)
	s.publish([]string{"lock-released:" + key, "released"})
	return Response{Value: parser.Integer(constants.ONE)}
}

// EXPIRE command sets a ttl on key
// @returns 0: if the key does not exist,
// @returns 1: if the ttl is set successfully.
func (s *Store) expire(args []string) Response {
	if len(args) < 2 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, "EXPIRE"))}
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

	e.expiry = time.Now().Add(time.Duration(secs) * time.Second)
	s.ttls.Push(ttlItem{key: key, expiresAt: e.expiry})

	return Response{Value: parser.Integer(constants.ONE)}
}

// TTL command returns the remaining time to live of a key that has an expiry set.
// @returns -2: if the key does not exist,
// @returns -1: if the key exists but has no expiry, and the TTL in seconds otherwise.
func (s *Store) ttl(args []string) Response {
	if len(args) < 1 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, "TTL"))}
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
	s.pubsub[topic] = append(s.pubsub[topic], ch)
	s.mut.Unlock()

	return ch
}

func (s *Store) Unsubscribe(topic string, ch chan []byte) {
	s.mut.Lock()

	subs := s.pubsub[topic]
	for i, sub_chan := range subs {
		if sub_chan == ch {
			s.pubsub[topic] = append(subs[:i], subs[i+1:]...)
			break
		}
	}

	s.mut.Unlock()
}

func (s *Store) publish(args []string) Response {
	if len(args) < 2 {
		return Response{Value: parser.Error(fmt.Sprintf(constants.WRONG_NUM_ARGS, "PUBLISH"))}
	}

	topic, message := args[0], args[1]

	s.mut.Lock()
	subs := make([]chan []byte, len(s.pubsub[topic]))
	copy(subs, s.pubsub[topic])
	s.mut.Unlock()

	delivered := 0
	for _, sub_chan := range subs {
		select {
		case sub_chan <- []byte(message):
			delivered++
		default:
		}
	}

	return Response{Value: parser.Integer(delivered)}
}

func (s *Store) evict() {
	now := time.Now()
	for s.ttls.Len() > 0 {
		item, ok := s.ttls.Peek()
		if !ok || item.expiresAt.After(now) {
			break
		}
		s.ttls.Pop()
		if e, ok := s.data[item.key]; ok && e.isExpired() {
			delete(s.data, item.key)
			s.publish([]string{"lock-released:" + item.key, "released"})
		}
	}
}

func (s *Store) snapshot() {
	snapshotData := make(map[string]snapshotEntry, len(s.data))
	for key, e := range s.data {
		snapshotData[key] = snapshotEntry{
			Value:  e.value,
			Expiry: e.expiry,
		}
	}

	select {
	case s.snapResp <- SnapshotResponse{data: snapshotData, err: nil}:
	default:
	}
}
