package store

import (
	"sync"
	"testing"
)

// ---- REGRESSION TEST FOR A NOW-FIXED RACE ----
//
// Previously (see git history): server.go called store.Subscribe/Unsubscribe
// directly from each connection's own goroutine, bypassing the single-
// threaded event loop entirely — the one exception to "cmdChan is the only
// consistency boundary." Both methods mutated *MemoryProfile's plain int64
// fields with no lock, so two clients subscribing/unsubscribing concurrently
// raced. Confirmed independently at the time via the pre-existing
// sdk/sdk_pubsub_test.go TestSDKPubSubConcurrentSubscribersDistinctTopics.
//
// Subscribe/Unsubscribe are now routed through subscribeChan/unsubscribeChan
// into Store.eventLoop (store.go), exactly like every other command — so
// this test now asserts the race is gone: many goroutines submitting
// SubscribeReq/UnsubscribeReq concurrently must never trip -race, because
// the event loop serializes all of them one at a time.
func TestSubscribeUnsubscribeConcurrentRace(t *testing.T) {
	cmdChan := make(chan Command)
	subscribeChan := make(chan SubscribeReq)
	unsubscribeChan := make(chan UnsubscribeReq)
	st := New(0, cmdChan, subscribeChan, unsubscribeChan, fakePersistence{}, newSpyStoreMetrics())
	st.Start()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			topic := "topic-a"
			if i%2 == 0 {
				topic = "topic-b"
			}

			ch := make(chan []byte, 16)
			subDone := make(chan struct{})
			subscribeChan <- SubscribeReq{
				Subscribers: map[string]chan []byte{topic: ch},
				Done:        subDone,
			}
			<-subDone

			unsubDone := make(chan struct{})
			unsubscribeChan <- UnsubscribeReq{
				SubscribedTopics: map[string]chan []byte{topic: ch},
				Done:             unsubDone,
			}
			<-unsubDone
		}(i)
	}
	wg.Wait()
}
