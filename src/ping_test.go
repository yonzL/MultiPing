package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestLoadHostsFileUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.csv")
	content := "hostName,hostIP\n" +
		"Local,127.0.0.1\n" +
		"Bad,999.1.1.1\n" +
		"OnlyName\n" +
		",127.0.0.3\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	specs, warnings, skipped, err := loadHostsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d, want 2", len(specs))
	}
	if specs[0].name != "Local" || specs[0].ip != "127.0.0.1" {
		t.Fatalf("unexpected first spec: %+v", specs[0])
	}
	if specs[1].name != "127.0.0.3" {
		t.Fatalf("unexpected second spec: %+v", specs[1])
	}
	if len(warnings) != 2 {
		t.Fatalf("len(warnings) = %d, want 2: %v", len(warnings), warnings)
	}
	if skipped != 2 {
		t.Fatalf("skipped = %d, want 2", skipped)
	}
}

func TestLoadHostsFileGBK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts_gbk.csv")
	utf8Content := "hostName,hostIP\n核心交换机,192.168.1.1\n"
	gbk, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(utf8Content))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, gbk, 0o644); err != nil {
		t.Fatal(err)
	}
	specs, _, _, err := loadHostsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].name != "核心交换机" {
		t.Fatalf("unexpected specs: %+v", specs)
	}
}

func TestIPv4ToNetworkUint32(t *testing.T) {
	got := ipv4ToNetworkUint32([4]byte{192, 168, 0, 1})
	want := uint32(192) | uint32(168)<<8 | uint32(0)<<16 | uint32(1)<<24
	if got != want {
		t.Fatalf("ipv4ToNetworkUint32 = %#x, want %#x", got, want)
	}
}

func TestPingLocalhost(t *testing.T) {
	p, err := newPinger()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	dest := ipv4ToNetworkUint32([4]byte{127, 0, 0, 1})
	if _, ok := p.pingOnce(dest); !ok {
		t.Fatal("ping 127.0.0.1 failed")
	}
	success, avg, completed := p.pingRound(context.Background(), dest)
	if !completed || success != 3 {
		t.Fatalf("pingRound = success %d, completed %v", success, completed)
	}
	if avg < 0 {
		t.Fatalf("avg latency = %v, want >= 0", avg)
	}
}
