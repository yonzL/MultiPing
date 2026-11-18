package main

import (
	"context"
	"sync"
	"testing"
)

func TestPingConcurrentSharedHandle(t *testing.T) {
	p, err := newPinger()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	dest := ipv4ToNetworkUint32([4]byte{127, 0, 0, 1})
	var wg sync.WaitGroup
	var mu sync.Mutex
	fails := 0
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			success, _, completed := p.pingRound(context.Background(), dest)
			if !completed || success != 3 {
				mu.Lock()
				fails++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf("fails=%d/24", fails)
	if fails > 0 {
		t.Fatalf("%d of 24 concurrent rounds failed", fails)
	}
}
