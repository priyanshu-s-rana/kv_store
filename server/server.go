package server

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync/atomic"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/models"
	"github.com/priyanshu-s-rana/kv_store/parser"
	"github.com/priyanshu-s-rana/kv_store/store"
)

type serverStats struct {
	activeConnections        atomic.Int64
	totalConnectionsAccepted atomic.Int64
	bytesSent                atomic.Int64
	bytesReceived            atomic.Int64
	parserErrors             atomic.Int64
	commandsReceived         atomic.Int64
	failedCommands           atomic.Int64
}

type Server struct {
	addr    string
	cmdChan chan<- models.Command
	store   *store.Store
	stats   serverStats
}

// New creates a Server bound to addr, wiring it to store's command channel.
// @returns *Server: ready to accept connections via Start.
func New(addr string, store *store.Store) *Server {
	return &Server{
		addr:    addr,
		cmdChan: store.CmdChan(),
		store:   store,
	}
}

// Start listens on s.addr and spawns a goroutine per accepted connection.
// Runs indefinitely; accept errors are logged and skipped, not fatal.
// @returns error: if the TCP listener itself cannot be created.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to start server on %s: %v", s.addr, err)
	}

	fmt.Printf("[server] listening on %s\n", s.addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[server] accept error: %v\n", err)
			continue
		}
		s.stats.activeConnections.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection serves a single client connection until it closes or errors.
// Reads inline or RESP commands in a loop, dispatching each to the store's event loop.
// SUBSCRIBE breaks out of the loop and hands control to handleSubscribe for the lifetime of the subscription.
func (s *Server) handleConnection(conn net.Conn) {
	defer func() {
		s.stats.activeConnections.Add(-1)
		conn.Close()
	}()

	remoteAddr := conn.RemoteAddr().String()
	log.Printf("[server] client connected: %s\n", remoteAddr)
	s.stats.totalConnectionsAccepted.Add(1)
	defer log.Printf("[server] client disconnected: %s\n", remoteAddr)

	countingReader := &CountingReader{
		reader: conn,
	}
	p := parser.New(countingReader)
	var lastBytesRead int64
	for {
		cmd, err := p.ReadCommand()
		current := countingReader.BytesRead()
		s.stats.bytesReceived.Add(current - lastBytesRead)
		lastBytesRead = current
		if err != nil {
			if err != io.EOF {
				s.stats.parserErrors.Add(1)
				log.Printf("[server] error reading command from %s: %v\n", remoteAddr, err)
			}
			return
		}
		s.stats.commandsReceived.Add(1)

		if cmd.Name == constants.Subscribe {
			s.handleSubscribe(conn, cmd.Args)
			return
		}

		responseChan := make(chan models.Response, 1)

		s.cmdChan <- models.Command{
			Name: cmd.Name,
			Args: cmd.Args,
			Resp: responseChan,
		}

		resp := <-responseChan
		if len(resp.Value) > 0 && resp.Value[0] == '-' {
			s.stats.failedCommands.Add(1)
		}
		s.writeToConnection(conn, resp.Value)
	}
}

// handleSubscribe registers the client on each topic and forwards published messages until the connection closes.
// Sends a RESP subscribe confirmation for each topic before entering the receive loop.
// Cleans up all subscriptions via defer when the connection drops.
func (s *Server) handleSubscribe(conn net.Conn, topics []string) {
	if len(topics) == 0 {
		s.stats.failedCommands.Add(1)
		s.writeToConnection(conn, parser.Error("SUBSCRIBE requires at least one topic"))
		return
	}

	channels := s.registerSubscription(conn, topics)
	defer s.cleanupSubscription(channels)

	merged := s.fanIn(channels)

	s.forwardMessages(conn, merged)
}

// registerSubscription subscribes the client to each topic and writes a RESP subscribe confirmation per topic.
// @returns map[topic]chan: the per-topic channels that will receive published messages.
func (s *Server) registerSubscription(conn net.Conn, topics []string) map[string]chan []byte {
	channels := make(map[string]chan []byte, len(topics))
	for _, topic := range topics {
		ch := s.store.Subscribe(topic)
		channels[topic] = ch

		s.writeToConnection(conn, parser.Array("subscribe", topic, "1"))
	}

	return channels
}

// cleanupSubscription unregisters every channel in channels from the store's pubsub map.
// Intended to run as a deferred call in handleSubscribe so cleanup is guaranteed on exit.
func (s *Server) cleanupSubscription(channels map[string]chan []byte) {
	for topic, ch := range channels {
		s.store.Unsubscribe(topic, ch)
	}
}

// fanIn merges multiple per-topic subscription channels into a single receive channel.
// Spawns one goroutine per topic; each goroutine wraps incoming payloads as RESP message arrays.
// @returns <-chan []byte: unified stream of encoded RESP messages ready to write to the client.
func (s *Server) fanIn(channels map[string]chan []byte) <-chan []byte {
	merged := make(chan []byte, 32)
	for topic, ch := range channels {
		go func() {
			for msg := range ch {
				merged <- parser.Array("message", topic, string(msg))
			}
		}()
	}

	return merged
}

// forwardMessages drains merged and writes each message to conn.
// Returns as soon as a write fails, signalling the caller to tear down the subscription.
func (s *Server) forwardMessages(conn net.Conn, merged <-chan []byte) {
	for msg := range merged {
		if err := s.writeToConnection(conn, msg); err != nil {
			return
		}
	}
}

func (s *Server) writeToConnection(conn net.Conn, msg []byte) error {
	bytesWritten, err := conn.Write(msg)
	if err != nil {
		log.Printf("[server] error writing to the connection at: %s", conn.RemoteAddr().String())
		return err
	}
	s.stats.bytesSent.Add(int64(bytesWritten))
	return nil
}
