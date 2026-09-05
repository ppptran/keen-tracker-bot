package keenclient

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Data models for the Keenetic RCI (Router Control Interface) endpoints.
//
// Field names below were verified against a real KN-3811 (KeeneticOS 5.0.12):
//   - /rci/show/mws/member returns a JSON array of mesh extenders.
//   - /rci/show/system returns a small controller snapshot (router/controller info).
//   - /rci/show/mws/status and /rci/show/mws/client are not relied upon because
//     the firmware does not expose meaningful data there for this bot's use case.
// ---------------------------------------------------------------------------

// MeshMember represents an entry from GET /rci/show/mws/member.
type MeshMember struct {
	CID                string            `json:"cid"`
	Model              string            `json:"model"`
	MAC                string            `json:"mac"`
	KnownHost          string            `json:"known-host"` // display name of the node
	IP                 string            `json:"ip"`
	Mode               string            `json:"mode"`    // "extender", "controller", ...
	HWType             string            `json:"hw_type"` // "router"
	HWID               string            `json:"hw_id"`
	License            string            `json:"license"`
	InternetAvailable  bool              `json:"internet-available"`
	FW                 string            `json:"fw"`
	FWAvailable        string            `json:"fw-available"`
	FWRelease          string            `json:"fw-release"`
	FWReleaseAvailable string            `json:"fw-release-available"`
	FWUpdateSandbox    string            `json:"fw-update-sandbox"`
	Region             string            `json:"region"`
	Associations       int               `json:"associations"` // number of attached clients
	FWChecking         bool              `json:"fw-checking"`
	Deleted            bool              `json:"deleted"`
	Capabilities       MeshCapabilities  `json:"capabilities"`
	System             MeshSystem        `json:"system"`
	Backhaul           MeshBackhaul      `json:"backhaul"`
	Port               []MeshPort        `json:"port"`
	RCI                MeshRCI           `json:"rci"`
}

// MeshCapabilities mirrors capabilities.capabilities object.
type MeshCapabilities struct {
	Controller bool `json:"controller"`
	Cloud      bool `json:"cloud"`
	// ...other optional flags are ignored.
}

// MeshSystem mirrors the system object (CPU/memory/uptime).
type MeshSystem struct {
	CPULoad int    `json:"cpuload"`
	Memory  string `json:"memory"` // "used/total" e.g. "187896/524288"
	Uptime  string `json:"uptime"` // seconds as a string
}

// MeshBackhaul describes how the node connects back to the controller.
type MeshBackhaul struct {
	Uplink    string `json:"uplink"`
	Root      string `json:"root"`   // "4000.<mac>" of the controller
	Bridge    string `json:"bridge"`
	Cost      int    `json:"cost"`
	Speed     string `json:"speed"`
	Duplex    string `json:"duplex"`
	PortLabel string `json:"port-label"`
}

// MeshPort describes a physical port on the node.
type MeshPort struct {
	Label      string `json:"label"`
	Appearance string `json:"appearance"`
	Link       string `json:"link"`   // "up" / "down"
	Speed      string `json:"speed"`
	Duplex     string `json:"duplex"`
}

// MeshRCI mirrors the rci object (remote controller interface status).
type MeshRCI struct {
	Errors int `json:"errors"`
}

// MeshClient represents an entry from GET /rci/show/mws/client.
// Keenetic only populates this for wireless mesh clients; on an Ethernet-backed
// mesh the endpoint returns an empty list.
type MeshClient struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	KnownHost string `json:"known-host"`
	Parent   string `json:"parent"`
	// ...optional fields.
}

// MeshStatus is intentionally kept for backward compatibility, but the active
// bot contract no longer depends on /rci/show/mws/status.
type MeshStatus struct {
	AutoUpdate bool `json:"auto-update"`
	Controller struct {
		UpdatePending bool `json:"update-pending"`
	} `json:"controller"`
}

// SystemControllerInfo is the minimal controller payload returned by
// /rci/show/system.
type SystemControllerInfo struct {
	Name    string `json:"name"`
	IP      string `json:"ip"`
	MAC     string `json:"mac"`
	Status  string `json:"status"`
	Online  bool   `json:"online"`
	Version string `json:"version"`
}

// Device is the Tracker-facing device model. It mirrors zteclient.Device so the
// zte-tracker-bot Tracker/state-machine can be reused nearly unchanged. Mesh
// members (extenders) map to MESH_AGENT* rows; the root controller maps to
// MESH_CONTROLLER.
type Device struct {
	MACAddress  string `json:"mac_address"`
	IPAddress   string `json:"ip_address"`
	HostName    string `json:"host_name"`
	NetworkType string `json:"network_type"` // LAN, WLAN, MESH_CONTROLLER, MESH_AGENT*
	Active      bool   `json:"active"`
	Port        string `json:"port"`
	MeshNode    string `json:"mesh_node"`  // display name of parent mesh node
	ParentID    string `json:"parent_id"`  // topology instID of parent
	ParentMAC   string `json:"parent_mac"` // MAC of parent mesh node
	ClientCount int    `json:"client_count"`
	ClientCountKnown bool `json:"client_count_known"`
	// Uptime is the node uptime in seconds (for display in the Tracker).
	Uptime int64 `json:"uptime"`
}

// InventoryStats summarizes host counts from the last GetDevices call.
// Mirrors zteclient.InventoryStats.
type InventoryStats struct {
	LANHosts    int // clients attached to the mesh (associations)
	WLANHosts   int // wireless clients
	MeshNodes   int // mesh agents + controller
	TotalUnique int // unique MACs across the mesh
}

// ControllerSnapshot is a synthetic record for the router itself. Keenetic does
// not always return a Controller row in /rci/show/mws/member, so the bot tracks
// the controller using the router reachability and status endpoint instead.
type ControllerSnapshot struct {
	Name     string    `json:"name"`
	IP       string    `json:"ip"`
	MAC      string    `json:"mac,omitempty"`
	IsOnline bool      `json:"is_online"`
	Status   string    `json:"status,omitempty"`
	LastSeen time.Time `json:"last_seen,omitempty"`
	Source   string    `json:"source,omitempty"`
}

// WiFIMesh aggregates one mesh scan. Nodes is the Web-UI-style unified list
// ([controller, ...extenders]) that the map is rendered from; Members keeps
// the raw /rci/show/mws/member rows for compatibility.
type WiFIMesh struct {
	Controller   ControllerSnapshot      `json:"controller"`
	Nodes        []MeshNode              `json:"nodes"`
	Members      []MeshMember            `json:"members"`
	Candidates   []MeshCandidate         `json:"candidates,omitempty"`
	ClientGroups map[string][]ClientInfo `json:"-"` // cid -> attached clients (for /clients)
	Clients      []MeshClient            `json:"clients"` // legacy, always empty
	Status       MeshStatus              `json:"status"`  // legacy, always empty
	FetchedAt    time.Time               `json:"fetched_at"`
}

// TotalClients sums the per-node client counts (the web UI "Clients" counter).
func (m WiFIMesh) TotalClients() int {
	n := 0
	for _, node := range m.Nodes {
		n += node.ClientCount
	}
	return n
}

// MeshNode is one row of the web UI "Mesh Wi-Fi System → Nodes" table.
// Index 0 is always the controller, the rest are extenders in mws/member order.
type MeshNode struct {
	CID               string       `json:"cid"`
	MAC               string       `json:"mac"`
	Name              string       `json:"name"`
	Model             string       `json:"model,omitempty"`
	Firmware          string       `json:"firmware,omitempty"`
	IP                string       `json:"ip,omitempty"`
	Mode              string       `json:"mode,omitempty"`
	IsController      bool         `json:"is_controller"`
	IsOnline          bool         `json:"is_online"`
	HasBackhaul       bool         `json:"has_backhaul"`
	Uptime            int64        `json:"uptime"` // seconds
	Backhaul          MeshBackhaul `json:"backhaul,omitempty"`
	Via               string       `json:"via,omitempty"`      // resolved parent display name
	ViaMAC            string       `json:"via_mac,omitempty"`  // resolved parent MAC
	ViaIsController   bool         `json:"via_is_controller,omitempty"`
	Connection        string       `json:"connection,omitempty"` // "1000 Mbit/s" / "Wi-Fi 5 GHz · 866 Mbit/s (-67 dBm)"
	ClientCount       int          `json:"client_count"`
	InternetAvailable bool         `json:"internet_available,omitempty"`
	IsUpdateAvailable bool         `json:"is_update_available,omitempty"`
}

// HotspotHost is one entry of GET /rci/show/ip/hotspot. Wireless clients carry
// a nested "mws" object whose "cid" points at the mesh node they attach to;
// wired hosts have no "mws" at all. "mws-backhaul" marks node-to-node Wi-Fi
// links, which the web UI excludes from client counts.
type HotspotHost struct {
	MAC         string `json:"mac"`
	IP          string `json:"ip"`
	Name        string `json:"name"`
	Hostname    string `json:"hostname"`
	Active      bool   `json:"active"`
	Link        string `json:"link"`
	Registered  bool   `json:"registered"`
	LastSeen    int64  `json:"last-seen"`
	MWSBackhaul bool   `json:"mws-backhaul"`
	Interface   struct {
		ID string `json:"id"`
	} `json:"interface"`
	MWS *HotspotMWS `json:"mws,omitempty"`
}

// HotspotMWS holds the wireless association details of a hotspot host.
type HotspotMWS struct {
	CID    string `json:"cid"`
	AP     string `json:"ap"`
	TxRate int    `json:"txrate"`
	Mode   string `json:"mode"`
	RSSI   int    `json:"rssi"`
	Uptime int64  `json:"uptime"`
}

// ClientInfo is one attached client, grouped per node for /clients.
type ClientInfo struct {
	MAC        string `json:"mac"`
	IP         string `json:"ip,omitempty"`
	Name       string `json:"name,omitempty"`
	NodeCID    string `json:"node_cid,omitempty"`
	IsWireless bool   `json:"is_wireless"`
	Link       string `json:"link,omitempty"`
	RSSI       int    `json:"rssi,omitempty"`   // dBm, negative; 0 when wired
	TxRate     int    `json:"txrate,omitempty"` // Mbit/s
	WiFiMode   string `json:"wifi_mode,omitempty"`
	Uptime     int64  `json:"uptime,omitempty"`
}

// MeshAssociation is one Wi-Fi backhaul link from /rci/show/mws/associations.
// Empty on Ethernet-backed meshes.
type MeshAssociation struct {
	MAC    string `json:"mac"`
	AP     string `json:"ap"`
	TxRate int    `json:"txrate"`
	RxRate int    `json:"rxrate"`
	RSSI   int    `json:"rssi"`
	Uptime int64  `json:"uptime"`
}

// MeshCandidate is a device seen by /rci/show/mws/candidate: discovered and
// ready to join the mesh, but not acquired yet.
type MeshCandidate struct {
	CID   string `json:"cid"`
	MAC   string `json:"mac"`
	Model string `json:"model"`
	State string `json:"state"`
}

// ---------------------------------------------------------------------------
// Parser helpers. Keenetic RCI returns JSON for /rci/show/* in modern NDMS.
// Some older NDMS versions can wrap replies in XML when the client sends
// Accept: application/xml. We keep a resilient parser that tolerates both.
// ---------------------------------------------------------------------------

// parseMeshMembers parses the /rci/show/mws/member response as a JSON array.
func parseMeshMembers(data []byte) ([]MeshMember, error) {
	var members []MeshMember
	if tr := strings.TrimSpace(string(data)); tr == "" || tr == "null" {
		return members, nil
	}
	if err := json.Unmarshal(data, &members); err != nil {
		return members, fmt.Errorf("parse mws/member failed: %w", err)
	}
	return members, nil
}

// parseMeshClients parses the /rci/show/mws/client response as a JSON array.
// On an Ethernet-backed mesh this endpoint returns an empty array; it can also
// return an empty body, so tolerate both instead of erroring.
func parseMeshClients(data []byte) ([]MeshClient, error) {
	var clients []MeshClient
	if tr := strings.TrimSpace(string(data)); tr == "" || tr == "null" {
		return clients, nil
	}
	if err := json.Unmarshal(data, &clients); err != nil {
		return clients, fmt.Errorf("parse mws/client failed: %w", err)
	}
	return clients, nil
}

// parseMeshStatus parses the /rci/show/mws/status response as a JSON object.
func parseMeshStatus(data []byte) (MeshStatus, error) {
	var st MeshStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parse mws/status failed: %w", err)
	}
	return st, nil
}

// parseHotspotHosts parses /rci/show/ip/hotspot: {"host": [...]} (verified on
// KN-3811 / KeeneticOS 5.0), tolerating a bare array and empty bodies.
func parseHotspotHosts(data []byte) ([]HotspotHost, error) {
	tr := strings.TrimSpace(string(data))
	if tr == "" || tr == "null" || tr == "{}" {
		return nil, nil
	}
	var wrapped struct {
		Host []HotspotHost `json:"host"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Host != nil {
		return wrapped.Host, nil
	}
	var list []HotspotHost
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse ip/hotspot failed: %w", err)
	}
	return list, nil
}

// parseMeshAssociations parses /rci/show/mws/associations, tolerating the
// shapes {"associations": [...]}, {"station": [...]} and a bare array.
func parseMeshAssociations(data []byte) ([]MeshAssociation, error) {
	tr := strings.TrimSpace(string(data))
	if tr == "" || tr == "null" || tr == "{}" {
		return nil, nil
	}
	var wrapped map[string][]MeshAssociation
	if err := json.Unmarshal(data, &wrapped); err == nil {
		for _, key := range []string{"associations", "station"} {
			if list, ok := wrapped[key]; ok && list != nil {
				return list, nil
			}
		}
	}
	var list []MeshAssociation
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse mws/associations failed: %w", err)
	}
	return list, nil
}

// parseMeshCandidates parses /rci/show/mws/candidate: {} when empty, otherwise
// a bare array or wrapped under a "candidate" key depending on firmware.
func parseMeshCandidates(data []byte) ([]MeshCandidate, error) {
	tr := strings.TrimSpace(string(data))
	if tr == "" || tr == "null" || tr == "{}" {
		return nil, nil
	}
	var wrapped struct {
		Candidate []MeshCandidate `json:"candidate"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Candidate != nil {
		return wrapped.Candidate, nil
	}
	var list []MeshCandidate
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse mws/candidate failed: %w", err)
	}
	return list, nil
}

// parseSystemController extracts the controller block from /rci/show/system.
// On actual Keenetic firmware, the response is a plain system object such as:
// {"hostname":"3053-Controller","cpuload":4,...} without a nested "controller" key.
// A successful, non-empty payload means the controller is online; empty/null
// bodies mean offline. Status therefore mirrors reachability in ONE place so
// GetWiFIMesh and GetControllerSnapshot can never disagree again.
func parseSystemController(data []byte, fallbackHost, fallbackMAC string) (ControllerSnapshot, error) {
	snap := ControllerSnapshot{
		Name:     "Controller",
		IP:       fallbackHost,
		MAC:      fallbackMAC,
		Status:   "offline",
		Source:   "system",
		LastSeen: time.Now(),
	}
	tr := strings.TrimSpace(string(data))
	if tr == "" || tr == "null" {
		snap.Status = "offline"
		snap.IsOnline = false
		return snap, nil
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return snap, fmt.Errorf("parse /rci/show/system failed: %w", err)
	}

	if v, ok := obj["hostname"].(string); ok && v != "" {
		snap.Name = v
	}
	if v, ok := obj["controller"].(map[string]interface{}); ok {
		if name, ok := v["name"].(string); ok && name != "" {
			snap.Name = name
		}
		if ip, ok := v["ip"].(string); ok && ip != "" {
			snap.IP = ip
		}
		if mac, ok := v["mac"].(string); ok && mac != "" {
			snap.MAC = mac
		}
		if status, ok := v["status"].(string); ok && status != "" {
			snap.Status = status
		}
		if online, ok := v["online"].(bool); ok {
			snap.IsOnline = online
		}
	}
	if snap.IP == "" {
		snap.IP = fallbackHost
	}
	if snap.MAC == "" {
		snap.MAC = fallbackMAC
	}
	if snap.Name == "" {
		snap.Name = "Controller"
	}
	if snap.Status == "" || snap.Status == "unknown" || snap.Status == "offline" {
		snap.Status = "online"
	}
	snap.IsOnline = true
	snap.LastSeen = time.Now()
	return snap, nil
}

// xmlUnmarshal is exposed for future XML-fallback support.
func xmlUnmarshal(data []byte, v interface{}) error {
	return xml.Unmarshal(data, v)
}

// NormalizeMAC strips separators and uppercases a MAC. Matches the zte-tracker-bot
// convention so the Tracker state machine can be reused unchanged.
func NormalizeMAC(mac string) string {
	mac = strings.ToUpper(strings.TrimSpace(mac))
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, "-", "")
	return mac
}

// wireToNetworkType maps a Keenetic backhaul-uplink identifier to an internal
// network-type code used by the Tracker. Backhaul `uplink` values look like
// "GigabitEthernet0/Vlan1" (wired) or "WiFi0/0", "WiFi5/0" (wireless), or may
// be a bare band identifier. We map to the same codes as zte-tracker-bot so the
// Tracker's isMeshNetworkType() keeps working.
func wireToNetworkType(uplink string) string {
	u := strings.ToLower(strings.TrimSpace(uplink))
	switch {
	case u == "":
		return "MESH_AGENT"
	case strings.Contains(u, "ethernet"), strings.Contains(u, "el") || strings.Contains(u, "vlan"),
		strings.Contains(u, "eth"), strings.HasPrefix(u, "gi"):
		return "MESH_AGENT_ETH"
	case strings.Contains(u, "5"), strings.Contains(u, "wifi5"), strings.HasPrefix(u, "wi"):
		return "MESH_AGENT_5G"
	default:
		return "MESH_AGENT"
	}
}

// isMeshNodeType mirrors zte-tracker-bot's isMeshNetworkType so the Tracker
// can reuse the same classification logic.
func isMeshNodeType(netType string) bool {
	switch netType {
	case "MESH_CONTROLLER", "MESH_AGENT", "MESH_AGENT_ETH", "MESH_AGENT_5G", "MESH_AGENT_2G":
		return true
	default:
		return false
	}
}

// BackhaulBand classifies a backhaul uplink interface name using the Keenetic
// radio convention: WifiMaster0 = 2.4 GHz, WifiMaster1 = 5 GHz. Wired uplinks
// look like "GigabitEthernet0/Vlan1". This replaces the old heuristic of
// searching for "5"/"2.4" inside the uplink string, which never matched the
// real "WifiMaster0/Backhaul0" format.
func BackhaulBand(uplink string) string {
	u := strings.ToLower(strings.TrimSpace(uplink))
	switch {
	case u == "":
		return ""
	case strings.Contains(u, "wifimaster0"):
		return "2.4GHz"
	case strings.Contains(u, "wifimaster1"):
		return "5GHz"
	case strings.Contains(u, "ethernet"), strings.Contains(u, "vlan"), strings.HasPrefix(u, "gi"):
		return "Ethernet"
	default:
		if strings.Contains(u, "wifi") {
			return "Wi-Fi"
		}
		return ""
	}
}

// ResolveBridgeParent resolves a backhaul bridge ("4000.<mac>") to the parent
// node exactly like the web UI: strip the "<code>." prefix, then match the
// controller's Bridge0 MAC first and any mesh member MAC second. This works
// for both wired and wireless backhaul and supports multi-hop meshes, unlike
// the old root-only lookup.
func ResolveBridgeParent(bridge, controllerMAC, controllerName string, nameByMAC map[string]string) (name, mac string, isController bool) {
	b := strings.TrimSpace(bridge)
	if b == "" {
		return controllerName, NormalizeMAC(controllerMAC), true
	}
	if i := strings.Index(b, "."); i >= 0 && i+1 < len(b) {
		b = b[i+1:]
	}
	parentMAC := NormalizeMAC(b)
	if parentMAC == "" {
		return controllerName, NormalizeMAC(controllerMAC), true
	}
	if parentMAC == NormalizeMAC(controllerMAC) {
		return controllerName, parentMAC, true
	}
	if name, ok := nameByMAC[parentMAC]; ok && name != "" {
		return name, parentMAC, false
	}
	return b, parentMAC, false
}

// BackhaulConnection renders the web UI "Connection" column: wired links show
// the link speed, wireless backhaul shows band + rate (+RSSI when known from
// /rci/show/mws/associations).
func BackhaulConnection(b MeshBackhaul, assoc *MeshAssociation) string {
	band := BackhaulBand(b.Uplink)
	rate := strings.TrimSpace(b.Speed)
	if assoc != nil && assoc.TxRate > 0 {
		rate = strconv.Itoa(assoc.TxRate)
	}
	switch band {
	case "2.4GHz", "5GHz", "Wi-Fi":
		label := "Wi-Fi"
		switch band {
		case "2.4GHz":
			label = "Wi-Fi 2.4 GHz"
		case "5GHz":
			label = "Wi-Fi 5 GHz"
		}
		if rate != "" {
			label += " · " + rate + " Mbit/s"
		}
		if assoc != nil && assoc.RSSI != 0 {
			label += fmt.Sprintf(" (%d dBm)", assoc.RSSI)
		}
		return label
	default:
		if rate != "" {
			return rate + " Mbit/s"
		}
		if band == "Ethernet" {
			return "Ethernet"
		}
		return ""
	}
}

func parseUptimeSeconds(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

// GroupClientsByNode replicates the Keenetic web UI algorithm exactly:
//   - keep hosts with active == true,
//   - exclude the mesh node MACs themselves,
//   - group by host.mws.cid, falling back to the controller cid (wired hosts
//     and controller-attached clients have no mws object).
//
// Note the web UI counts active wired hosts even when link == "down"; the
// per-node counters intentionally match that behaviour.
func GroupClientsByNode(hosts []HotspotHost, controllerCID string, extenderMACs []string) map[string][]ClientInfo {
	extSet := make(map[string]bool, len(extenderMACs))
	for _, mac := range extenderMACs {
		if n := NormalizeMAC(mac); n != "" {
			extSet[n] = true
		}
	}
	out := make(map[string][]ClientInfo)
	for _, h := range hosts {
		if !h.Active {
			continue
		}
		if extSet[NormalizeMAC(h.MAC)] {
			continue
		}
		cid := controllerCID
		ci := ClientInfo{
			MAC:  h.MAC,
			IP:   h.IP,
			Link: h.Link,
			Name: h.Name,
		}
		if ci.Name == "" {
			ci.Name = h.Hostname
		}
		if h.MWS != nil && h.MWS.CID != "" {
			cid = h.MWS.CID
			ci.IsWireless = true
			ci.NodeCID = h.MWS.CID
			ci.RSSI = h.MWS.RSSI
			ci.TxRate = h.MWS.TxRate
			ci.WiFiMode = h.MWS.Mode
			ci.Uptime = h.MWS.Uptime
		}
		if ci.Name == "" {
			ci.Name = ci.MAC
		}
		out[cid] = append(out[cid], ci)
	}
	return out
}
