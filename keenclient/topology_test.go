package keenclient

import (
	"testing"
)

func TestBackhaulBand(t *testing.T) {
	cases := map[string]string{
		"GigabitEthernet0/Vlan1": "Ethernet",
		"WifiMaster0/Backhaul0":  "2.4GHz",
		"WifiMaster1/Backhaul0":  "5GHz",
		"":                       "",
	}
	for uplink, want := range cases {
		if got := BackhaulBand(uplink); got != want {
			t.Errorf("BackhaulBand(%q) = %q, want %q", uplink, got, want)
		}
	}
}

func TestResolveBridgeParent(t *testing.T) {
	ctrlMAC := "04:5f:a6:54:9c:c9"
	names := map[string]string{
		"045FA63E0A5E": "Agent-2-P#36",
	}

	// Controller bridge: "4000." prefix + Bridge0 MAC.
	name, mac, isCtrl := ResolveBridgeParent("4000.04:5f:a6:54:9c:c9", ctrlMAC, "Controller-P#33", names)
	if !isCtrl || name != "Controller-P#33" || mac != NormalizeMAC(ctrlMAC) {
		t.Errorf("controller bridge resolved wrong: name=%q mac=%q isCtrl=%v", name, mac, isCtrl)
	}

	// Multi-hop: bridge points at another extender.
	name, _, isCtrl = ResolveBridgeParent("4000.04:5f:a6:3e:0a:5e", ctrlMAC, "Controller-P#33", names)
	if isCtrl || name != "Agent-2-P#36" {
		t.Errorf("extender bridge resolved wrong: name=%q isCtrl=%v", name, isCtrl)
	}

	// Empty bridge falls back to the controller.
	name, _, isCtrl = ResolveBridgeParent("", ctrlMAC, "Controller-P#33", names)
	if !isCtrl || name != "Controller-P#33" {
		t.Errorf("empty bridge resolved wrong: name=%q isCtrl=%v", name, isCtrl)
	}
}

func TestBackhaulConnection(t *testing.T) {
	eth := BackhaulConnection(MeshBackhaul{Uplink: "GigabitEthernet0/Vlan1", Speed: "1000", Duplex: "full"}, nil)
	if eth != "1000 Mbit/s" {
		t.Errorf("ethernet connection = %q, want %q", eth, "1000 Mbit/s")
	}

	wifi := BackhaulConnection(MeshBackhaul{Uplink: "WifiMaster1/Backhaul0"}, &MeshAssociation{TxRate: 866, RSSI: -67})
	want := "Wi-Fi 5 GHz · 866 Mbit/s (-67 dBm)"
	if wifi != want {
		t.Errorf("wifi connection = %q, want %q", wifi, want)
	}
}

func TestGroupClientsByNode(t *testing.T) {
	const ctrlCID = "ctrl-cid"
	const ag2CID = "ag2-cid"
	hosts := []HotspotHost{
		// Wireless client attached to Agent-2.
		{MAC: "88:03:e9:48:70:cb", IP: "192.168.22.121", Active: true, Link: "up",
			MWS: &HotspotMWS{CID: ag2CID, RSSI: -77, TxRate: 325, Mode: "11ac"}},
		// Second wireless client on Agent-2.
		{MAC: "aa:bb:cc:00:00:01", IP: "192.168.22.122", Active: true, Link: "up",
			MWS: &HotspotMWS{CID: ag2CID, RSSI: -60}},
		// Wired hosts: counted for the controller even with link down
		// (the web UI only checks active).
		{MAC: "d4:e8:53:74:02:ed", IP: "192.168.22.249", Active: true, Link: "down"},
		{MAC: "d4:e8:53:74:02:ee", IP: "192.168.22.250", Active: true, Link: "up"},
		// Inactive host: excluded.
		{MAC: "d4:e8:53:74:02:ef", IP: "192.168.22.251", Active: false},
		// An extender's own MAC: excluded even when active.
		{MAC: "04:5f:a6:3e:0a:5e", IP: "192.168.22.227", Active: true, Link: "up"},
	}

	groups := GroupClientsByNode(hosts, ctrlCID, []string{"04:5f:a6:3e:0a:5e"})

	if got := len(groups[ctrlCID]); got != 2 {
		t.Errorf("controller clients = %d, want 2", got)
	}
	if got := len(groups[ag2CID]); got != 2 {
		t.Errorf("agent clients = %d, want 2", got)
	}
	// Wireless client keeps its mws details.
	c := groups[ag2CID][0]
	if !c.IsWireless || c.RSSI != -77 || c.TxRate != 325 || c.WiFiMode != "11ac" {
		t.Errorf("wireless client details wrong: %+v", c)
	}
	// Wired client falls back to the controller cid.
	if groups[ctrlCID][0].IsWireless {
		t.Errorf("wired client must not be marked wireless: %+v", groups[ctrlCID][0])
	}
}

func TestParseSystemControllerStatusFix(t *testing.T) {
	// A successful, non-empty payload must report online (regression test for
	// the old bug that always left Status="offline").
	snap, err := parseSystemController([]byte(`{"hostname":"3053-Controller","uptime":"449511"}`), "192.168.22.14", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.IsOnline || snap.Status != "online" {
		t.Errorf("status = %q online = %v, want online/true", snap.Status, snap.IsOnline)
	}

	// Empty body means offline.
	snap, err = parseSystemController([]byte(""), "192.168.22.14", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.IsOnline || snap.Status != "offline" {
		t.Errorf("empty body must be offline, got status=%q online=%v", snap.Status, snap.IsOnline)
	}
}

func TestParseHotspotHosts(t *testing.T) {
	// Verified shape on KN-3811: {"host": [...]}.
	hosts, err := parseHotspotHosts([]byte(`{"host":[{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.22.1","active":true,"link":"up","mws":{"cid":"cid-1","rssi":-50,"txrate":300,"mode":"11ac"}}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 1 || hosts[0].MWS == nil || hosts[0].MWS.CID != "cid-1" {
		t.Fatalf("hosts parsed wrong: %+v", hosts)
	}

	// Empty object means no hosts.
	hosts, err = parseHotspotHosts([]byte(`{}`))
	if err != nil || hosts != nil {
		t.Errorf("empty hotspot must yield no hosts, got %v err %v", hosts, err)
	}
}

func TestParseMeshMembersSkipsNothingButFlagsDeleted(t *testing.T) {
	// The mode-less member (a device that left the mesh) must still parse.
	members, err := parseMeshMembers([]byte(`[
		{"cid":"c1","mac":"04:5f:a6:3e:0a:5e","known-host":"Agent-2-P#36","mode":"extender","backhaul":{"uplink":"GigabitEthernet0/Vlan1","root":"4000.04:5f:a6:54:9c:c9"}},
		{"cid":"c2","mac":"34:ce:00:6e:e6:f1","known-host":"Keenetic Xiaomi 3G"}
	]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	if members[1].Mode != "" || members[1].Backhaul.Root != "" {
		t.Errorf("mode-less member parsed wrong: %+v", members[1])
	}
}
