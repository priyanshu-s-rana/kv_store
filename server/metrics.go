package server

import (
	"io"
	"sync/atomic"
)

// ServerStats is a plain snapshot of server metrics returned by GetStats.
type ServerStats struct {
	ActiveConnections        int64
	TotalConnectionsAccepted int64
	BytesSent                int64
	BytesReceived            int64
	ParserErrors             int64
	CommandsReceived         int64
	FailedCommands           int64
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

func (s *Server) GetStats() ServerStats {
	return ServerStats{
		ActiveConnections:        s.stats.activeConnections.Load(),
		TotalConnectionsAccepted: s.stats.totalConnectionsAccepted.Load(),
		BytesSent:                s.stats.bytesSent.Load(),
		BytesReceived:            s.stats.bytesReceived.Load(),
		ParserErrors:             s.stats.parserErrors.Load(),
		CommandsReceived:         s.stats.commandsReceived.Load(),
		FailedCommands:           s.stats.failedCommands.Load(),
	}
}
