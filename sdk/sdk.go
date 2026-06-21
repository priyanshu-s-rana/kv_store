package sdk

import (
	"bufio"
	"fmt"
	"net"
	"strconv"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

type KVStoreClient struct {
	conn   net.Conn
	addr   string
	reader *bufio.Reader
}

// NewClient dials address and returns a connected KVStoreClient.
func NewClient(address string) (*KVStoreClient, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("[KVStore Client] error connecting to server: %v", err)
	}
	return &KVStoreClient{
		conn:   conn,
		addr:   address,
		reader: bufio.NewReader(conn),
	}, nil
}

// Close closes the underlying TCP connection.
func (client *KVStoreClient) Close() error {
	if client.conn == nil {
		return nil
	}
	return client.conn.Close()
}

// Ping sends a PING and expects PONG back, useful for health-checking the connection.
func (client *KVStoreClient) Ping() (string, error) {
	return client.sendCommand(string(constants.Ping))
}

// Get retrieves the value for key, returning "nil" if the key does not exist.
func (client *KVStoreClient) Get(key string) (string, error) {
	return client.sendCommand(string(constants.Get), key)
}

// Set stores value under key and returns "OK" on success.
// Accepts optional modifiers: WithEX(secs), WithNX (set if not exists), WithXX (set if exists).
// NX and XX return "nil" when their condition is not met instead of returning an error.
func (client *KVStoreClient) Set(key string, value string, options ...SetOption) (string, error) {
	args := []string{string(constants.Set), key, value}
	return client.sendCommand(buildSetArgs(args, options...)...)
}

// Del removes key and returns 1 if deleted, 0 if it did not exist.
func (client *KVStoreClient) Del(key string) (string, error) {
	return client.sendCommand(string(constants.Del), key)
}

// Expire sets a TTL of secs seconds on key.
func (client *KVStoreClient) Expire(key string, secs int) (string, error) {
	return client.sendCommand(string(constants.Expire), key, strconv.Itoa(secs))
}

// TTL returns the remaining TTL in seconds, -1 if no expiry, -2 if the key does not exist.
func (client *KVStoreClient) TTL(key string) (string, error) {
	return client.sendCommand(string(constants.TTL), key)
}

// Incr atomically increments the integer stored at key by 1, creating it at 0 first if absent.
func (client *KVStoreClient) Incr(key string) (string, error) {
	return client.sendCommand(string(constants.Incr), key)
}

// Decr atomically decrements the integer stored at key by 1, creating it at 0 first if absent.
func (client *KVStoreClient) Decr(key string) (string, error) {
	return client.sendCommand(string(constants.Decr), key)
}

// MSet sets multiple key-value pairs atomically; pairs must alternate key, value.
func (client *KVStoreClient) MSet(pairs ...string) (string, error) {
	return client.sendCommand(append([]string{string(constants.Mset)}, pairs...)...)
}

// MGet returns the values for the given keys in order; missing or expired keys are empty strings.
func (client *KVStoreClient) MGet(keys ...string) ([]string, error) {
	resp, err := client.sendCommand(append([]string{string(constants.Mget)}, keys...)...)
	if err != nil {
		return nil, err
	}
	return parseMGetResponse(resp), nil
}

// Keys returns all keys matching pattern (e.g. "user:*").
func (client *KVStoreClient) Keys(pattern string) (string, error) {
	return client.sendCommand(string(constants.Keys), pattern)
}

// Publish sends message to all subscribers of topic and returns the subscriber count.
func (client *KVStoreClient) Publish(topic string, message string) (string, error) {
	return client.sendCommand(string(constants.Publish), topic, message)
}

// FlushAll deletes all keys from the store.
func (client *KVStoreClient) FlushAll() (string, error) {
	return client.sendCommand(string(constants.FlushAll))
}

// MemoryStats returns a newline-separated breakdown of memory usage across the store.
func (client *KVStoreClient) MemoryStats() (string, error) {
	return client.sendCommand(string(constants.MemoryStats))
}

// Subscribe registers the client on the given topics and returns a Subscription whose
// Message channel receives published messages until Unsubscribe is called.
func (client *KVStoreClient) Subscribe(topics ...string) (*Subscription, error) {
	return client.handleSubscribe(append([]string{string(constants.Subscribe)}, topics...)...)
}
