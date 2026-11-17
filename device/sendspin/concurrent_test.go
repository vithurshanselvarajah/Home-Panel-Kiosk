package sendspin

import (
	"sync"
	"testing"
	"time"
)

func TestThePlacementIsSafeUnderTheSpeaker(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := false
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			p.write(nowUs()+int64(i)*1000, []int16{1000, -1000, 2000, -2000})
		}

		started = true
		p.write(nowUs()+10_000_000, []int16{3000, -3000})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		out := make([]int16, 1536)
		for i := 0; i < 400; i++ {
			p.Render(uint64(i)*768, out)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			p.queuedMs()
			p.Ready()
			p.Held()
			p.stats()
		}
	}()

	waited := make(chan struct{})
	go func() {
		wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("the placement deadlocked")
	}
}
