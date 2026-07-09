package persistence

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

type AOFConfig struct {
	FilePath   string
	SyncPolicy string
}

type AOF struct {
	writer    *bufio.Writer
	file      *os.File
	aofConfig *AOFConfig
	mu        sync.Mutex
}

type AOFMetadata struct {
	aof        *AOF
	Generation uint64
}

func NewAOF(config *AOFConfig) (*AOF, error) {
	filePath := config.FilePath
	log.Printf("[AOF] filePath: %s", filePath)
	file, err := OpenFile(filePath, constants.OpenAOF)
	if err != nil {
		return nil, err
	}
	writer := bufio.NewWriter(file)
	aof := &AOF{
		writer:    writer,
		file:      file,
		aofConfig: config,
	}

	return aof, nil
}

func (aof *AOF) Append(name constants.CmdName, args []string, sequenceID uint64) error {
	parts := append([]string{constants.SequenceID, strconv.FormatUint(sequenceID, 10), string(name)}, args...)
	command := parser.Array(parts...)

	aof.mu.Lock()
	_, err := aof.writer.Write(command)
	aof.mu.Unlock()

	if err == nil && aof.aofConfig.SyncPolicy == constants.SyncAlways {
		return aof.fsync()
	}

	return err
}

func (aof *AOF) Initialize(gen uint64) error {
	aof.mu.Lock()
	defer aof.mu.Unlock()

	if err := aof.writer.Flush(); err != nil {
		return err
	}

	if err := aof.file.Truncate(0); err != nil {
		return err
	}

	if _, err := aof.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	return aof.writeHeader(gen)
}

func (aof *AOF) Replay(cmdChan chan<- Command, snapshotSequenceID uint64) (bool, uint64, error) {
	if _, err := aof.file.Seek(0, io.SeekStart); err != nil {
		return false, snapshotSequenceID, err
	}

	p := parser.New(aof.file)
	replayCounter := 0
	latestSquenceID := snapshotSequenceID
	for {
		cmd, err := p.ReadCommand()
		if err != nil {
			if err == io.EOF {
				break
			}
			return false, latestSquenceID, err
		}

		if string(cmd.Name) == constants.Header {
			continue
		}

		if string(cmd.Name) == constants.SequenceID {
			cmdSequenceID, err := strconv.ParseUint(cmd.Args[0], 10, 64)
			if err != nil {
				return replayCounter > 0, latestSquenceID, err
			}
			if snapshotSequenceID >= cmdSequenceID {
				continue
			}

			actualCmd := &Command{
				Name: constants.CmdName(cmd.Args[1]),
				Args: cmd.Args[2:],
			}

			if err := sendCommandToEventLoop(cmdChan, actualCmd.Name, actualCmd.Args); err != nil {
				return replayCounter > 0, latestSquenceID, err
			}

			replayCounter++
			latestSquenceID = cmdSequenceID
		}

	}

	log.Printf("[Persistence] Successfully replayed %d commands.", replayCounter)
	return replayCounter > 0, latestSquenceID, nil
}

func (aof *AOF) fsync() error {
	aof.mu.Lock()
	defer aof.mu.Unlock()
	if err := aof.writer.Flush(); err != nil {
		return err
	}
	return aof.file.Sync()
}

func (aof *AOF) fsyncLocked() error {
	if err := aof.writer.Flush(); err != nil {
		return err
	}
	return aof.file.Sync()
}

func (aof *AOF) ensureInitialize(gen uint64) error {
	info, err := aof.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return aof.writeHeader(gen)
	}
	return nil
}

func (aof *AOF) writeHeader(gen uint64) error {
	if err := writeHeader(aof.writer, gen); err != nil {
		return err
	}
	return aof.fsyncLocked()
}

func (aof *AOF) readHeader() (*AOFMetadata, error) {
	parser := parser.New(aof.file)
	header, err := parser.ReadCommand()
	aofMeta := &AOFMetadata{
		aof: aof,
	}
	if err != nil {
		if err == io.EOF {
			aofMeta.Generation = 0
			return aofMeta, nil
		}
		return nil, err
	}

	if string(header.Name) != constants.Header {
		return nil, fmt.Errorf("Doesn't contain header")
	}
	for i := 0; i < len(header.Args); i += 2 {
		switch header.Args[i] {
		case constants.Generation:
			aofMeta.Generation, err = strconv.ParseUint(header.Args[i+1], 10, 64)
			if err != nil {
				return nil, err
			}
		}
	}

	return aofMeta, nil
}
