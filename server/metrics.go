package server

import (
	"io"
	"sync/atomic"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// ServerMetrics is the domain-level metrics contract for the Server subsystem.
// Implementations translate these events/state changes into an observability
// backend (e.g. Prometheus); the Server package has no knowledge of that backend.
type ServerMetrics interface {
	IncConnectionsAccepted()
	IncConnectionsClosed()
	SetActiveConnections(count int64)

	IncBytesSent(bytes int64)
	IncBytesReceived(bytes int64)

	IncParserErrors()

	IncCommandsReceived(cmd constants.CmdName)
	IncFailedCommands(cmd constants.CmdName)

	// Time from handing a command to the Store until its response is received.
	// This is the full round trip as seen by the network layer, including Store
	// execution; it is a superset of the Store's own ObserveCommandDuration.
	ObserveCommandDuration(cmd constants.CmdName, duration time.Duration)

	ObserveResponseWriteDuration(duration time.Duration)
}

type CountingReader struct {
	reader    io.Reader
	bytesRead atomic.Int64
}

func (cntRdr *CountingReader) Read(buff []byte) (int, error) {
	n, err := cntRdr.reader.Read(buff)
	cntRdr.bytesRead.Add(int64(n))
	return n, err
}

func (cntRdr *CountingReader) BytesRead() int64 {
	return cntRdr.bytesRead.Load()
}
