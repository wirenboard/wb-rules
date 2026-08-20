package wbrules

import (
	"sync"
	"testing"
)

// The driver keeps delivering events from its own goroutine for a moment
// after the engine has stopped; PushEvent racing Close used to panic with
// "send on closed channel" (a select with a default case does not protect a
// send on a CLOSED channel). Seen as a 1-in-10 crash of the corpus test run
// under CPU load, and reachable in production during shutdown.
func TestEventBufferPushAfterCloseIsDropped(t *testing.T) {
	eb := NewEventBuffer()
	eb.PushEvent(&ControlChangeEvent{})
	eb.Close()
	eb.PushEvent(&ControlChangeEvent{}) // must not panic
	eb.Close()                          // idempotent
	if got := eb.length(); got != 1 {
		t.Fatalf("event pushed after Close must be dropped, buffer has %d", got)
	}
	if _, open := <-eb.Observe(); open {
		// one notification was queued by the first push; the channel must
		// then be closed
		if _, open := <-eb.Observe(); open {
			t.Fatal("observer channel not closed")
		}
	}
}

func TestEventBufferConcurrentPushAndClose(t *testing.T) {
	for round := 0; round < 200; round++ {
		eb := NewEventBuffer()
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for j := 0; j < 50; j++ {
					eb.PushEvent(&ControlChangeEvent{})
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			eb.Close()
		}()
		close(start)
		wg.Wait() // a panic in any goroutine fails the test
	}
}
