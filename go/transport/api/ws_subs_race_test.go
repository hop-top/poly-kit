package api

import (
	"strconv"
	"sync"
	"testing"
)

// The read loop mutates a connection's subscriptions while the hub loop
// matches broadcasts against them. Run with -race: an unguarded map is a
// data race, and in production a fatal concurrent map access a client can
// trigger at will.
func TestSubscriptions_ConcurrentMutationAndMatch(t *testing.T) {
	var s subscriptions
	var wg sync.WaitGroup
	stop := make(chan struct{})
	started := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			p := "t." + strconv.Itoa(i%50)
			s.add(p)
			s.remove(p)
		}
	}()

	<-started
	for i := 0; i < 200000; i++ {
		s.matches("t.7")
	}
	close(stop)
	wg.Wait()
}
