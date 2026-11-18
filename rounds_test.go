package main

import (
	"context"
	"sync"
	"testing"
)

func TestPingAllHostsFile(t *testing.T) {
	specs, _, _, err := loadHostsFile("hosts.csv")
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPinger()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var wg sync.WaitGroup
	for _, s := range specs {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			success, _, completed := p.pingRound(context.Background(), s.addr)
			t.Logf("%s %s -> %d completed=%v", s.name, s.ip, success, completed)
		}()
	}
	wg.Wait()
}
