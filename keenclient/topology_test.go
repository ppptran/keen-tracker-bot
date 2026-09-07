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

func TestCountWirelessClients(t *testing.T) {
	// The controller bucket mixes wired hosts (no mws.cid) with wireless
	// clients attached to the controller's own radios; only the latter count.
	clients := []ClientInfo{
		{MAC: "aa:bb:cc:00:00:01", IsWireless: true},
		{MAC: "aa:bb:cc:00:00:02"},
		{MAC: "aa:bb:cc:00:00:03", IsWireless: true},
	}
	if got := CountWirelessClients(clients); got != 2 {
		t.Errorf("CountWirelessClients = %d, want 2", got)
	}
	if got := CountWirelessClients(nil); got != 0 {
		t.Errorf("CountWirelessClients(nil) = %d, want 0", got)
	}
}

func TestParseHotspotBatch(t *testing.T) {
	// Verified shape on KN-3811 (KeeneticOS 5.0.12): a client on the
	// controller's own radios has top-level ap/rssi/txrate and NO mws; a client
	// on a mesh agent nests its association under mws.
	data := []byte(`[{"show":{"ip":{"hotspot":{"host":[
		{"mac":"dc:b4:ca:19:d0:b9","ip":"192.168.22.171","active":true,"link":"up",
		 "ap":"WifiMaster1/AccessPoint0","rssi":-61,"txrate":433,"mode":"11ac","ssid":"PhongTro"},
		{"mac":"98:c8:b8:fc:32:07","ip":"192.168.22.240","active":true,"link":"up",
		 "mws":{"cid":"ag2-cid","rssi":-67,"txrate":1,"mode":"11b"}}
	]}}}}]`)
	hosts, err := parseHotspotBatch(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %d, want 2", len(hosts))
	}
	direct := hosts[0]
	if !direct.IsWireless() || direct.MWS != nil || direct.RSSI != -61 || direct.TxRate != 433 || direct.AP != "WifiMaster1/AccessPoint0" {
		t.Errorf("direct controller client parsed wrong: %+v", direct)
	}
	viaAgent := hosts[1]
	if !viaAgent.IsWireless() || viaAgent.MWS == nil || viaAgent.MWS.CID != "ag2-cid" {
		t.Errorf("agent client parsed wrong: %+v", viaAgent)
	}

	// Empty batch shapes must yield no hosts, not an error.
	for _, empty := range []string{``, `null`, `[]`, `{}`} {
		hosts, err := parseHotspotBatch([]byte(empty))
		if err != nil || hosts != nil {
			t.Errorf("parseHotspotBatch(%q) = %v, %v; want nil, nil", empty, hosts, err)
		}
	}
}

func TestGroupClientsByNodeDirectWireless(t *testing.T) {
	// A wireless client attached directly to the controller has no mws object;
	// it must land in the controller bucket AND be marked IsWireless, or the
	// controller's wireless count would always be 0.
	hosts := []HotspotHost{
		{MAC: "dc:b4:ca:19:d0:b9", IP: "192.168.22.171", Active: true, Link: "up",
			AP: "WifiMaster1/AccessPoint0", RSSI: -61, TxRate: 433, Mode: "11ac"},
		{MAC: "c4:e1:a1:3f:6c:77", IP: "192.168.22.44", Active: true, Link: "up",
			AP: "WifiMaster0/AccessPoint0", RSSI: -80, TxRate: 87, Mode: "11n"},
		{MAC: "d4:e8:53:74:02:ed", IP: "192.168.22.249", Active: true, Link: "down"}, // wired
	}
	groups := GroupClientsByNode(hosts, "ctrl-cid", nil)
	ctrl := groups["ctrl-cid"]
	if len(ctrl) != 3 {
		t.Fatalf("controller group = %d clients, want 3", len(ctrl))
	}
	if got := CountWirelessClients(ctrl); got != 2 {
		t.Errorf("controller wireless clients = %d, want 2", got)
	}
	// The wired host must not be marked wireless.
	for _, c := range ctrl {
		if c.MAC == "d4:e8:53:74:02:ed" && c.IsWireless {
			t.Errorf("wired host must not be marked wireless: %+v", c)
		}
	}
	// Direct wireless keeps its top-level association details.
	direct := ctrl[0]
	if !direct.IsWireless || direct.RSSI != -61 || direct.TxRate != 433 || direct.WiFiMode != "11ac" {
		t.Errorf("direct wireless details wrong: %+v", direct)
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
