package persistence

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

func OpenFile(filePath string, flag int) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(filePath), constants.DirPerm); err != nil {
		return nil, err
	}
	return os.OpenFile(filePath, flag, constants.FilePerm)
}

func writeHeader(writer *bufio.Writer, gen uint64) error {
	header := parser.Array(constants.Header, constants.Generation, strconv.FormatUint(gen, 10))
	_, err := writer.Write(header)
	if err != nil {
		return err
	}
	return nil
}

func sendCommandToEventLoop(cmdChan chan<- Command, name constants.CmdName, args []string) error {
	responseChan := make(chan Response, 1)

	cmdChan <- Command{
		Name:    name,
		Args:    args,
		Resp:    responseChan,
		SkipAof: true,
	}
	resp := <-responseChan
	if err := resp.IsError(); err != nil {
		return fmt.Errorf("Error occured after sending command to eventLoop: %v", err)
	}

	return nil
}

func (p *Persistence) saveSnapshot(data map[string]SnapshotEntry, sealedGen uint64, lastSequenceID uint64) error {
	if err := p.snapshot.SaveToDisk(data, sealedGen, lastSequenceID); err != nil {
		log.Printf(constants.SNAPSHOT_FAILED, err)
		return err
	}
	log.Printf(constants.SNAPSHOT_SAVED, p.snapshot.snapshotConfig.FilePath)
	return nil
}

func (p *Persistence) checkpointSuccess() error {
	return sendCommandToEventLoop(p.cmdChan, constants.CheckpointSuccess, nil)
}

func (p *Persistence) triggerFinalCheckpoint() {
	if err := sendCommandToEventLoop(p.cmdChan, constants.Checkpoint, nil); err != nil {
		log.Printf("[Persistence] Failed to Checkpoint the Data: %v.", err)
	}
}

// StartSnapshotting saves the store to disk at every interval until ctx is cancelled.
// The caller is responsible for cancelling ctx to stop the goroutine and release the ticker.
func (p *Persistence) startSnapshotting() {
	p.wg.Go(func() {
		ticker := time.NewTicker(p.snapshot.snapshotConfig.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-ticker.C:
				if !p.checkpointState.InProgress.Load() {
					if err := sendCommandToEventLoop(p.cmdChan, constants.Checkpoint, nil); err != nil {
						log.Printf("[Persistance] Scheduled checkpoint failed to trigger: %v", err)
					}
				}
			}
		}
	})
}

func (p *Persistence) markCheckpointFailed() {
	p.checkpointState.LastSucceeded.Store(false)
	p.checkpointState.InProgress.Store(false)
}

func (p *Persistence) markCheckpointSuccess() {
	p.checkpointState.LastSucceeded.Store(true)
	p.checkpointState.InProgress.Store(false)
}
