package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/models"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

type Server struct {
	addr              string
	listener          net.Listener
	cmdChan           chan<- models.Command
	subscribeChan     chan<- models.SubscribeReq
	unsubscribeChan   chan<- models.UnsubscribeReq
	metrics           ServerMetrics
	activeConnections atomic.Int64
	wg                sync.WaitGroup
}

// New creates a Server bound to addr, wiring it to store's command channel.
// @returns *Server: ready to accept connections via Start.
func New(addr string, cmdChan chan<- models.Command, subscribeChan chan<- models.SubscribeReq, unsubscribeChan chan<- models.UnsubscribeReq, metrics ServerMetrics) *Server {
	return &Server{
		addr:            addr,
		cmdChan:         cmdChan,
		subscribeChan:   subscribeChan,
		unsubscribeChan: unsubscribeChan,
		metrics:         metrics,
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

	s.listener = ln
	log.Printf("[server] listening on %s\n", s.addr)
	return nil
}

func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}

			log.Printf("[server] accept error: %v\n", err)
			continue
		}
		s.activeConnections.Add(1)
		s.metrics.SetActiveConnections(s.activeConnections.Load())
		s.wg.Go(func() { s.handleConnection(conn) })
	}
}

func (s *Server) ShutDown() {
	_ = s.listener.Close()
	s.wg.Wait()
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

	done := make(chan struct{})
	go func() {
		subscribeConnChecker(conn, done)
	}()

	channels := s.registerSubscription(conn, topics)
	defer s.cleanupSubscription(channels)

	merged := s.fanIn(channels)

	s.forwardMessages(conn, merged, done)
}

// registerSubscription subscribes the client to each topic and writes a RESP subscribe confirmation per topic.
// @returns map[topic]chan: the per-topic channels that will receive published messages.
func (s *Server) registerSubscription(conn net.Conn, topics []string) map[string]chan []byte {
	subscribers := make(map[string]chan []byte, len(topics))
	for _, topic := range topics {
		subscribers[topic] = make(chan []byte, 16)
	}

	done := make(chan struct{})
	s.subscribeChan <- models.SubscribeReq{
		Subscribers: subscribers,
		Done:        done,
	}
	<-done

	// Each topic gets its own complete RESP array (*3: "subscribe", topic,
	// count) — concatenated into one buffer for a single write() syscall,
	// but still N independently-decodable top-level replies, matching what
	// a client reading one confirmation per topic expects.
	var confirmations []byte
	for i, topic := range topics {
		confirmations = append(confirmations, parser.Array("subscribe", topic, strconv.Itoa(i+1))...)
	}
	s.writeToConnection(conn, confirmations)

	return subscribers
}

// cleanupSubscription unregisters every channel in channels from the store's pubsub map.
// Intended to run as a deferred call in handleSubscribe so cleanup is guaranteed on exit.
func (s *Server) cleanupSubscription(channels map[string]chan []byte) {
	done := make(chan struct{})
	s.unsubscribeChan <- models.UnsubscribeReq{
		SubscribedTopics: channels,
		Done:             done,
	}
	<-done
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
func (s *Server) forwardMessages(conn net.Conn, merged <-chan []byte, done chan struct{}) {
	for {
		select {
		case msg, ok := <-merged:
			if !ok {
				return
			}
			if err := s.writeToConnection(conn, msg); err != nil {
				return
			}
		case <-done:
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

// Just Read from the subscriber channel continuously in order to check if
// the client connection is alive or not.
func subscribeConnChecker(conn net.Conn, done chan struct{}) {
	defer close(done)
	buffer := make([]byte, 1)
	for {
		_, err := conn.Read(buffer)
		if err != nil {
			return
		}
	}
}
