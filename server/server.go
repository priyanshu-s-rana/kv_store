package server

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/models"
	"github.com/priyanshu-s-rana/kv_store/parser"
	"github.com/priyanshu-s-rana/kv_store/store"
)

type Server struct {
	addr              string
	cmdChan           chan<- models.Command
	store             *store.Store
	metrics           ServerMetrics
	activeConnections atomic.Int64
}

// New creates a Server bound to addr, wiring it to store's command channel.
// @returns *Server: ready to accept connections via Start.
func New(addr string, cmdChan chan<- models.Command, store *store.Store, metrics ServerMetrics) *Server {
	return &Server{
		addr:    addr,
		cmdChan: cmdChan,
		store:   store,
		metrics: metrics,
	}
}

// Start listens on s.addr and spawns a goroutine per accepted connection.
// Runs indefinitely; accept errors are logged and skipped, not fatal.
// @returns error: if the TCP listener itself cannot be created.
func (s *Server) Start(config models.CFG) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to start server on %s: %v", s.addr, err)
	}

	if config.Debug.Pprof.Enable {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)

		go func() {
			addr := config.Debug.Pprof.Host + ":" + config.Debug.Pprof.Port

			log.Printf("pprof listening on http://%s/debug/pprof/", addr)

			if err := http.ListenAndServe(addr, nil); err != nil {
				log.Printf("pprof server stopped: %v", err)
			}
		}()
	}

	log.Printf("[server] listening on %s\n", s.addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[server] accept error: %v\n", err)
			continue
		}
		s.activeConnections.Add(1)
		s.metrics.SetActiveConnections(s.activeConnections.Load())
		go s.handleConnection(conn)
	}
}

// handleConnection serves a single client connection until it closes or errors.
// Reads inline or RESP commands in a loop, dispatching each to the store's event loop.
// SUBSCRIBE breaks out of the loop and hands control to handleSubscribe for the lifetime of the subscription.
func (s *Server) handleConnection(conn net.Conn) {
	defer func() {
		s.activeConnections.Add(-1)
		s.metrics.SetActiveConnections(s.activeConnections.Load())
		s.metrics.IncConnectionsClosed()
		conn.Close()
	}()

	remoteAddr := conn.RemoteAddr().String()
	log.Printf("[server] client connected: %s\n", remoteAddr)
	s.metrics.IncConnectionsAccepted()
	defer log.Printf("[server] client disconnected: %s\n", remoteAddr)

	countingReader := &CountingReader{
		reader: conn,
	}
	p := parser.New(countingReader)
	var lastBytesRead int64
	for {
		cmd, err := p.ReadCommand()
		current := countingReader.BytesRead()
		s.metrics.IncBytesReceived(current - lastBytesRead)
		lastBytesRead = current
		if err != nil {
			if err != io.EOF {
				s.metrics.IncParserErrors()
				log.Printf("[server] error reading command from %s: %v\n", remoteAddr, err)
			}
			return
		}
		s.metrics.IncCommandsReceived(cmd.Name)

		if cmd.Name == constants.Subscribe {
			s.handleSubscribe(conn, cmd.Args)
			return
		}

		responseChan := make(chan models.Response, 1)

		start := time.Now()
		s.cmdChan <- models.Command{
			Name: cmd.Name,
			Args: cmd.Args,
			Resp: responseChan,
		}

		resp := <-responseChan
		s.metrics.ObserveCommandDuration(cmd.Name, time.Since(start))
		if len(resp.Value) > 0 && resp.Value[0] == '-' {
			s.metrics.IncFailedCommands(cmd.Name)
		}
		s.writeToConnection(conn, resp.Value)
	}
}

// handleSubscribe registers the client on each topic and forwards published messages until the connection closes.
// Sends a RESP subscribe confirmation for each topic before entering the receive loop.
// Cleans up all subscriptions via defer when the connection drops.
func (s *Server) handleSubscribe(conn net.Conn, topics []string) {
	if len(topics) == 0 {
		s.metrics.IncFailedCommands(constants.Subscribe)
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
	start := time.Now()
	bytesWritten, err := conn.Write(msg)
	s.metrics.ObserveResponseWriteDuration(time.Since(start))
	if err != nil {
		log.Printf("[server] error writing to the connection at: %s", conn.RemoteAddr().String())
		return err
	}
	s.metrics.IncBytesSent(int64(bytesWritten))
	return nil
}
