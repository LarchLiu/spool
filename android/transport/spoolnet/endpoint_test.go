package spoolnet

import "testing"

func TestEndpointValidation(t *testing.T) {
	for _, address := range []string{
		"http://spool.example.ts.net", "https://localhost", "https://127.0.0.1", "https://[::1]",
		"https://user:password@spool.example.ts.net", "https://spool.example.ts.net/chat",
		"https://spool.example.ts.net?token=secret", "https://spool.example.ts.net?",
		"https://spool.example.ts.net#", "https://spool.example.ts.net:0", "https://spool.example.ts.net:65536",
		"https://spool..example.ts.net", "https://spool.example.ts.net\\@elsewhere.test",
	} {
		if _, _, err := parseEndpoint(address); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
	for _, address := range []string{"https://spool.example.ts.net", " https://SPOOL.example.ts.net:443/ "} {
		origin, target, err := parseEndpoint(address)
		if err != nil || origin.String() != "https://spool.example.ts.net" || target != "spool.example.ts.net:443" {
			t.Errorf("parse %q: %v %q %v", address, origin, target, err)
		}
	}
	_, target, err := parseEndpoint("https://spool.example.ts.net:8443")
	if err != nil || target != "spool.example.ts.net:8443" {
		t.Fatalf("custom HTTPS port: %q %v", target, err)
	}
}

func TestInterfaceSnapshot(t *testing.T) {
	t.Cleanup(func() { UpdateInterfaces("[]") })
	if err := UpdateInterfaces(`[{"name":"wlan0","index":3,"mtu":1500,"addresses":["192.168.1.42/24","2001:db8::42/64"]}]`); err != nil {
		t.Fatal(err)
	}
	list, err := androidInterfaces()
	if err != nil || len(list) != 1 || list[0].Name != "wlan0" {
		t.Fatalf("unexpected interfaces %v %v", list, err)
	}
	addresses, err := list[0].Addrs()
	if err != nil || len(addresses) != 2 || addresses[0].String() != "192.168.1.42/24" {
		t.Fatalf("lost interface host address: %v %v", addresses, err)
	}
	if err := UpdateInterfaces(`[{"name":"bad","index":1,"mtu":1500,"addresses":["invalid"]}]`); err == nil {
		t.Fatal("accepted malformed address")
	}
	list, _ = androidInterfaces()
	if len(list) != 1 || list[0].Name != "wlan0" {
		t.Fatal("failed snapshot overwrote valid state")
	}
}
