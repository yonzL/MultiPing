package main

import "testing"

func TestHostsFileAddresses(t *testing.T) {
	specs, _, _, err := loadHostsFile("../resource/hosts.csv")
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range specs {
		t.Logf("%d %s %s %#x", i, s.name, s.ip, s.addr)
	}
	if specs[0].addr != ipv4ToNetworkUint32([4]byte{127, 0, 0, 1}) {
		t.Fatalf("localhost addr = %#x", specs[0].addr)
	}
}
