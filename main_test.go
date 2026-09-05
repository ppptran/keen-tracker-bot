package main

import (
	"strings"
	"testing"

	"keen-tracker-bot/keenclient"
)

func TestRenderMeshMap(t *testing.T) {
	mesh := keenclient.WiFIMesh{
		Nodes: []keenclient.MeshNode{
			{IsController: true, Name: "Controller-P#33", Model: "KN-3811 (KN-3811)", Firmware: "5.0.12", IsOnline: true, Uptime: 5 * 86400, ClientCount: 244},
			{IsController: false, Name: "Agent-2-P#36", IP: "192.168.22.227", IsOnline: true, HasBackhaul: true,
				Backhaul: keenclient.MeshBackhaul{Uplink: "GigabitEthernet0/Vlan1", Speed: "1000"}, Uptime: 5*86400 + 53*60, ClientCount: 4,
				Via: "Controller-P#33", ViaIsController: true, Connection: "1000 Mbit/s"},
			{IsController: false, Name: "Agent-4-P#19", IP: "192.168.22.233", IsOnline: true, HasBackhaul: true,
				Backhaul: keenclient.MeshBackhaul{Uplink: "GigabitEthernet0/Vlan1", Speed: "1000"}, Uptime: 5*86400 + 49*60, ClientCount: 1,
				Via: "Controller-P#33", ViaIsController: true, Connection: "1000 Mbit/s"},
			{IsController: false, Name: "Keenetic Xiaomi 3G 262***022", IsOnline: false},
		},
	}
	got := renderMeshMap(mesh)

	wantBlocks := []string{
		"🎛 <b>Controller-P#33</b> · KN-3811 · OS 5.0.12\n   Uptime 5d 00:00 · 🟢 Online · 👥 244 clients\n",
		// Agent lines are split: name+IP, then clients · connection · uptime on
		// a "│"-indented detail line so nothing wraps mid-line on phones.
		"├─ <b>Agent-2-P#36</b>  🟢 192.168.22.227\n│      👥 4 · 1000 Mbit/s · 5d 00:53\n",
		"├─ <b>Agent-4-P#19</b>  🟢 192.168.22.233\n│      👥 1 · 1000 Mbit/s · 5d 00:49\n",
		"└─ <b>Keenetic Xiaomi 3G 262***022</b> 🔴 Offline\n       (không tham gia mesh)\n",
	}
	for _, want := range wantBlocks {
		if !strings.Contains(got, want) {
			t.Errorf("map output missing block %q\nGot:\n%s", want, got)
		}
	}
	// The detail lines carry the tree connector; there must be no leftover
	// standalone "│" line between entries.
	if strings.Contains(got, "│  \n") {
		t.Errorf("unexpected standalone connector line\nGot:\n%s", got)
	}
}
