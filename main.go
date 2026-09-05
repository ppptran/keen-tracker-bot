package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"keen-tracker-bot/keenclient"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

// MonitoredDevice stores the device list from devices.json.
type MonitoredDevice struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
}

// DeviceState tracks the online/offline state of a monitored node.
type DeviceState struct {
	MAC            string
	Name           string
	IsOnline       bool
	LastIP         string
	LastSeen       time.Time
	NetworkType    string
	Port           string
	MeshNode       string
	ConnectedCount int
}

// Tracker coordinates state, monitor loop, and Telegram notifications.
type Tracker struct {
	mu             sync.RWMutex
	states         map[string]*DeviceState
	monitoredOrder []string
	controllerName string
	controllerMAC  string
	chatID         int64
	bot            *tgbotapi.BotAPI
	keenClient     *keenclient.Client
	interval       time.Duration

	// Controller reachability tracking (P0-5): alert once after
	// failThreshold consecutive failed scans, and once on recovery.
	failCount         int
	controllerAlerted bool
	failThreshold     int
}

func normalizeMAC(mac string) string {
	mac = strings.ToUpper(mac)
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, "-", "")
	return strings.TrimSpace(mac)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "Chưa từng thấy"
	}
	loc := time.FixedZone("UTC+7", 7*60*60)
	return t.In(loc).Format("15:04:05 02/01/2006")
}

func formatMAC(mac string) string {
	if len(mac) != 12 {
		return mac
	}
	var parts []string
	for i := 0; i < 12; i += 2 {
		parts = append(parts, mac[i:i+2])
	}
	return strings.Join(parts, ":")
}

func formatNetworkType(netType string) string {
	switch netType {
	case "MESH_CONTROLLER":
		return "Controller"
	case "MESH_AGENT":
		return "Agent (WiFi)"
	case "MESH_AGENT_ETH":
		return "Agent (Ethernet)"
	case "MESH_AGENT_5G":
		return "Agent (5GHz)"
	case "MESH_AGENT_2G":
		return "Agent (2.4GHz)"
	default:
		if netType == "" {
			return "Không xác định"
		}
		return netType
	}
}

// networkTypeForNode classifies a node by its backhaul band. The Keenetic
// radio convention (WifiMaster0 = 2.4 GHz, WifiMaster1 = 5 GHz) replaces the
// old heuristic of searching for "5"/"2.4" inside the uplink string.
func networkTypeForNode(node keenclient.MeshNode) string {
	if node.IsController {
		return "MESH_CONTROLLER"
	}
	switch keenclient.BackhaulBand(node.Backhaul.Uplink) {
	case "Ethernet":
		return "MESH_AGENT_ETH"
	case "2.4GHz":
		return "MESH_AGENT_2G"
	case "5GHz":
		return "MESH_AGENT_5G"
	default:
		return "MESH_AGENT"
	}
}

// formatUptime renders seconds the compact way shown on the /map tree,
// e.g. "5d 04:53", "3h 12m", "45m".
func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "—"
	}
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %02d:%02d", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh %02dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// shortModel trims the duplicated vendor suffix the router reports,
// e.g. "KN-3811 (KN-3811)" -> "KN-3811".
func shortModel(model string) string {
	if i := strings.Index(model, " ("); i >= 0 {
		return model[:i]
	}
	return model
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️ Không tìm thấy .env, sẽ dùng biến môi trường hệ thống nếu có")
	}

	tgToken := os.Getenv("TELEGRAM_TOKEN")
	if tgToken == "" {
		log.Fatal("❌ TELEGRAM_TOKEN không được để trống")
	}

	tgChatIDStr := os.Getenv("TELEGRAM_CHAT_ID")
	if tgChatIDStr == "" {
		log.Fatal("❌ TELEGRAM_CHAT_ID không được để trống")
	}
	tgChatID, err := strconv.ParseInt(tgChatIDStr, 10, 64)
	if err != nil {
		log.Fatalf("❌ TELEGRAM_CHAT_ID không hợp lệ: %v", err)
	}

	keenIP := os.Getenv("KEENETIC_IP")
	if keenIP == "" {
		keenIP = "192.168.1.1"
	}

	keenUser := os.Getenv("KEENETIC_USERNAME")
	if keenUser == "" {
		keenUser = "admin"
	}

	keenPass := os.Getenv("KEENETIC_PASSWORD")
	if keenPass == "" {
		log.Fatal("❌ KEENETIC_PASSWORD không được để trống")
	}

	skipSSL := os.Getenv("KEENETIC_INSECURE_SKIP_VERIFY") != "false"

	intervalStr := os.Getenv("CHECK_INTERVAL")
	if intervalStr == "" {
		intervalStr = "1m"
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		interval = time.Minute
	}

	failThreshold := 3
	if v := os.Getenv("CONTROLLER_FAIL_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			failThreshold = n
		}
	}

	fileBytes, err := os.ReadFile("devices.json")
	if err != nil {
		log.Fatalf("❌ Không đọc được devices.json: %v", err)
	}

	var monitoredList []MonitoredDevice
	if err := json.Unmarshal(fileBytes, &monitoredList); err != nil {
		log.Fatalf("❌ Sai cấu trúc devices.json: %v", err)
	}

	client, err := keenclient.NewClient(keenIP, keenUser, keenPass, !skipSSL)
	if err != nil {
		log.Fatalf("❌ Không tạo được Keenetic client: %v", err)
	}

	controllerName := "Controller"
	controllerMAC := ""
	if len(monitoredList) > 0 {
		for _, dev := range monitoredList {
			if strings.EqualFold(dev.Name, "Controller") || strings.Contains(strings.ToLower(dev.Name), "controller") {
				controllerMAC = normalizeMAC(dev.MAC)
				controllerName = dev.Name
				client.SetControllerMAC(controllerMAC)
				break
			}
		}
	}

	bot, err := tgbotapi.NewBotAPI(tgToken)
	if err != nil {
		log.Fatalf("❌ Không kết nối được Telegram bot: %v", err)
	}
	bot.Debug = false

	tracker := &Tracker{
		states:         make(map[string]*DeviceState),
		monitoredOrder: make([]string, 0, len(monitoredList)),
		controllerName: controllerName,
		controllerMAC:  controllerMAC,
		chatID:         tgChatID,
		bot:            bot,
		keenClient:     client,
		interval:       interval,
		failThreshold:  failThreshold,
	}

	for _, dev := range monitoredList {
		normMAC := normalizeMAC(dev.MAC)
		if normMAC == "" {
			continue
		}
		tracker.states[normMAC] = &DeviceState{
			MAC:      normMAC,
			Name:     dev.Name,
			IsOnline: true,
		}
		tracker.monitoredOrder = append(tracker.monitoredOrder, normMAC)
	}

	tracker.checkNow(true)
	go tracker.monitorLoop()
	go tracker.handleTelegramCommands()

	bootMsg := fmt.Sprintf("✅ *Keenetic Tracker Bot đã khởi chạy thành công!*\n\n📊 *Giám sát:* %d thiết bị\n⏱ *Chu kỳ quét:* %s\n🧭 Lệnh: /status · /clients · /refresh", len(tracker.monitoredOrder), intervalStr)
	msg := tgbotapi.NewMessage(tgChatID, bootMsg)
	msg.ParseMode = "Markdown"
	if _, err := bot.Send(msg); err != nil {
		log.Printf("⚠️ Không gửi được boot notification: %v", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	shutdownMsg := "👋 *Keenetic Tracker Bot đang tạm dừng hoạt động.*"
	msgShutdown := tgbotapi.NewMessage(tgChatID, shutdownMsg)
	msgShutdown.ParseMode = "Markdown"
	if _, err := bot.Send(msgShutdown); err != nil {
		log.Printf("⚠️ Không gửi được shutdown notification: %v", err)
	}
}

func (t *Tracker) monitorLoop() {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for range ticker.C {
		_, _ = t.checkNow(false)
	}
}

// handleScanFailure is the P0-5 rule: when the controller API cannot be
// reached, count consecutive failures and alert exactly once at the threshold.
// Device states are intentionally left untouched during an outage (no mass
// offline spam); the recovery alert fires once when scans succeed again.
func (t *Tracker) handleScanFailure() {
	t.mu.Lock()
	t.failCount++
	shouldAlert := t.failCount >= t.failThreshold && !t.controllerAlerted
	if shouldAlert {
		t.controllerAlerted = true
	}
	t.mu.Unlock()

	if !shouldAlert {
		return
	}
	msg := tgbotapi.NewMessage(t.chatID, fmt.Sprintf("🔴 *Mất kết nối controller!*\n\nKhông gọi được API của `%s` trong %d chu kỳ quét liên tiếp.\n🕒 _%s_", t.keenClient.Host, t.failThreshold, formatTime(time.Now())))
	msg.ParseMode = "Markdown"
	if _, err := t.bot.Send(msg); err != nil {
		log.Printf("⚠️ Không gửi controller-down alert: %v", err)
	}
}

func (t *Tracker) handleScanSuccess() {
	t.mu.Lock()
	recovered := t.controllerAlerted
	t.failCount = 0
	t.controllerAlerted = false
	t.mu.Unlock()

	if !recovered {
		return
	}
	msg := tgbotapi.NewMessage(t.chatID, fmt.Sprintf("🟢 *Controller đã phản hồi lại!*\n\nAPI `%s` hoạt động bình thường trở lại.\n🕒 _%s_", t.keenClient.Host, formatTime(time.Now())))
	msg.ParseMode = "Markdown"
	if _, err := t.bot.Send(msg); err != nil {
		log.Printf("⚠️ Không gửi controller-recovery alert: %v", err)
	}
}

// checkNow scans the mesh, updates device states and fires online/offline
// alerts. It returns the fresh mesh so callers (e.g. /refresh) can render the
// map without a second scan.
func (t *Tracker) checkNow(isInit bool) (keenclient.WiFIMesh, error) {
	mesh, err := t.keenClient.GetWiFIMesh()
	if err != nil {
		log.Printf("⚠️ Quét mesh thất bại: %v", err)
		t.handleScanFailure()
		return mesh, err
	}
	t.handleScanSuccess()

	// Unified node list from GetWiFIMesh: index 0 is the controller, the rest
	// are extenders. Online follows the web UI rule (extender has a backhaul
	// object), so devices that left the mesh stay visible as offline.
	seen := make(map[string]keenclient.MeshNode, len(mesh.Nodes))
	for _, node := range mesh.Nodes {
		mac := normalizeMAC(node.MAC)
		if mac == "" {
			continue
		}
		seen[mac] = node
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, mac := range t.monitoredOrder {
		state := t.states[mac]
		node, ok := seen[mac]
		isOnlineNow := ok && node.IsOnline

		if isOnlineNow {
			state.LastIP = node.IP
			state.LastSeen = time.Now()
			state.NetworkType = networkTypeForNode(node)
			state.Port = node.Backhaul.PortLabel
			if node.IsController {
				state.MeshNode = "Controller"
			} else {
				state.MeshNode = node.Via
			}
			state.ConnectedCount = node.ClientCount
		} else {
			state.LastIP = ""
			state.NetworkType = ""
			state.Port = ""
			state.MeshNode = ""
			state.ConnectedCount = 0
		}

		if isInit {
			state.IsOnline = isOnlineNow
			if !isOnlineNow {
				state.ConnectedCount = 0
			}
			continue
		}

		if isOnlineNow {
			if !state.IsOnline {
				state.IsOnline = true
				notifyMsg := fmt.Sprintf("🟢 *Thiết bị trực tuyến trở lại!*\n\n📶 *%s*\n• MAC: `%s`\n• IP: `%s`\n• Kết nối: `%s`\n• Nút Mesh: `%s`\n• 📱 Client kết nối: *%d*\n🕒 Cập nhật: _%s_",
					state.Name, formatMAC(state.MAC), state.LastIP, formatNetworkType(state.NetworkType), state.MeshNode, state.ConnectedCount, formatTime(time.Now()))
				msg := tgbotapi.NewMessage(t.chatID, notifyMsg)
				msg.ParseMode = "Markdown"
				if _, err := t.bot.Send(msg); err != nil {
					log.Printf("⚠️ Không gửi online notification: %v", err)
				}
			}
		} else {
			if state.IsOnline {
				state.IsOnline = false
				state.ConnectedCount = 0
				notifyMsg := fmt.Sprintf("🚨 *Cảnh báo thiết bị ngoại tuyến!*\n\n🔴 *%s*\n• MAC: `%s`\n• IP cuối: `%s`\n• Nút Mesh cuối: `%s`\n🕒 Lần cuối thấy: _%s_",
					state.Name, formatMAC(state.MAC), state.LastIP, state.MeshNode, formatTime(state.LastSeen))
				msg := tgbotapi.NewMessage(t.chatID, notifyMsg)
				msg.ParseMode = "Markdown"
				if _, err := t.bot.Send(msg); err != nil {
					log.Printf("⚠️ Không gửi offline notification: %v", err)
				}
			} else {
				state.ConnectedCount = 0
			}
		}
	}

	return mesh, nil
}

func (t *Tracker) handleTelegramCommands() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := t.bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}
		if update.Message.Chat.ID != t.chatID {
			continue
		}
		if !update.Message.IsCommand() {
			continue
		}

		switch update.Message.Command() {
		case "status":
			go t.sendStatusMessage(update.Message.Chat.ID)
		case "refresh":
			go t.handleRefreshCommand(update.Message.Chat.ID)
		case "clients":
			go t.handleClientsCommand(update.Message.Chat.ID, strings.TrimSpace(update.Message.CommandArguments()))
		}
	}
}

func (t *Tracker) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := t.bot.Send(msg); err != nil {
		log.Printf("⚠️ Không gửi message: %v", err)
	}
}

// sendHTML sends a message with Telegram HTML parse mode.
func (t *Tracker) sendHTML(chatID int64, html string) {
	msg := tgbotapi.NewMessage(chatID, html)
	msg.ParseMode = "HTML"
	if _, err := t.bot.Send(msg); err != nil {
		log.Printf("⚠️ Không gửi message: %v", err)
	}
}

// sendStatusMessage renders the Web-UI-style mesh map (the former /map
// content) from a fresh scan. /status and /map were merged into this one
// command; only /status, /clients and /refresh remain.
func (t *Tracker) sendStatusMessage(chatID int64) {
	mesh, err := t.keenClient.GetWiFIMesh()
	if err != nil {
		t.sendText(chatID, "❌ Không quét được mesh: "+err.Error())
		return
	}
	t.sendHTML(chatID, renderMeshMap(mesh))
}

// htmlEscape makes dynamic text safe for Telegram HTML parse mode.
func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// renderMeshMap builds the node tree in the shape agreed in DANH_GIA.md
// (P1-7). Each agent takes two tree lines — name + IP on the first, clients ·
// connection · uptime on a "│"-indented second line — so nothing wraps
// mid-line on narrow phone screens. Formatted with Telegram HTML (not a code
// block); dynamic names are escaped and names containing * or _ survive HTML
// mode untouched.
func renderMeshMap(mesh keenclient.WiFIMesh) string {
	var sb strings.Builder
	sb.WriteString("<b>🗺 Mesh Wi-Fi System</b>\n")
	for i, node := range mesh.Nodes {
		if node.IsController {
			status := "🔴 Offline"
			if node.IsOnline {
				status = "🟢 Online"
			}
			sb.WriteString(fmt.Sprintf("🎛 <b>%s</b> · %s · OS %s\n", htmlEscape(node.Name), htmlEscape(shortModel(node.Model)), htmlEscape(node.Firmware)))
			sb.WriteString(fmt.Sprintf("   Uptime %s · %s · 👥 %d clients\n", formatUptime(node.Uptime), status, node.ClientCount))
			continue
		}
		prefix := "├─"
		// Detail lines hang off the "│" tree column; the final node closes the
		// tree, so its detail line is indented with spaces instead.
		detailPrefix := "│      "
		if i == len(mesh.Nodes)-1 {
			prefix = "└─"
			detailPrefix = "       "
		}
		if node.IsOnline {
			sb.WriteString(fmt.Sprintf("%s <b>%s</b>  🟢 %s\n", prefix, htmlEscape(node.Name), node.IP))
			detail := fmt.Sprintf("👥 %d", node.ClientCount)
			if node.Connection != "" {
				detail += " · " + node.Connection
			}
			detail += " · " + formatUptime(node.Uptime)
			if !node.ViaIsController && node.Via != "" {
				detail += " · ↑ " + htmlEscape(node.Via)
			}
			sb.WriteString(detailPrefix + detail + "\n")
		} else {
			sb.WriteString(fmt.Sprintf("%s <b>%s</b> 🔴 Offline\n", prefix, htmlEscape(node.Name)))
			if node.Mode == "" {
				sb.WriteString(detailPrefix + "(không tham gia mesh)\n")
			}
		}
	}
	if len(mesh.Candidates) > 0 {
		sb.WriteString("\n➕ <b>Chờ ghép mesh:</b>\n")
		for _, c := range mesh.Candidates {
			name := c.Model
			if name == "" {
				name = c.MAC
			}
			sb.WriteString(fmt.Sprintf("• %s (<code>%s</code>)\n", htmlEscape(name), htmlEscape(c.MAC)))
		}
	}
	sb.WriteString(fmt.Sprintf("\n📊 Controller 1 · Extenders %d · Clients %d\n", len(mesh.Nodes)-1, mesh.TotalClients()))
	sb.WriteString(fmt.Sprintf("🕒 <i>Cập nhật lúc: %s</i>", formatTime(time.Now())))
	return sb.String()
}

// handleClientsCommand lists the clients attached to one node (/clients <tên>).
// The list shows genuinely connected clients (wireless, or wired with link up)
// plus the total counter that matches the Nodes table.
func (t *Tracker) handleClientsCommand(chatID int64, arg string) {
	if arg == "" {
		t.sendText(chatID, "ℹ️ Dùng: /clients <tên node>\nVí dụ: /clients Agent-2 hoặc /clients controller")
		return
	}
	mesh, err := t.keenClient.GetWiFIMesh()
	if err != nil {
		t.sendText(chatID, "❌ Không quét được mesh: "+err.Error())
		return
	}
	var node *keenclient.MeshNode
	q := strings.ToLower(arg)
	for i := range mesh.Nodes {
		n := &mesh.Nodes[i]
		if strings.EqualFold(n.Name, arg) || strings.Contains(strings.ToLower(n.Name), q) {
			node = n
			break
		}
	}
	if node == nil {
		names := make([]string, 0, len(mesh.Nodes))
		for _, n := range mesh.Nodes {
			names = append(names, n.Name)
		}
		t.sendText(chatID, "❓ Không tìm thấy node \""+arg+"\".\nCác node hiện có: "+strings.Join(names, ", "))
		return
	}

	clients := mesh.ClientGroups[node.CID]
	var shown []keenclient.ClientInfo
	for _, c := range clients {
		if c.IsWireless || c.Link == "up" {
			shown = append(shown, c)
		}
	}
	sort.Slice(shown, func(i, j int) bool {
		if shown[i].IsWireless != shown[j].IsWireless {
			return shown[i].IsWireless
		}
		if shown[i].IsWireless {
			return shown[i].RSSI > shown[j].RSSI
		}
		return shown[i].IP < shown[j].IP
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📱 <b>%s</b> — tổng 👥 %d clients\n(×%d đang kết nối thực sự)\n\n", htmlEscape(node.Name), len(clients), len(shown)))
	const maxShow = 30
	if len(shown) == 0 {
		sb.WriteString("Không có client nào đang kết nối thực sự.\n")
	}
	for i, c := range shown {
		if i == maxShow {
			sb.WriteString(fmt.Sprintf("… và %d client nữa\n", len(shown)-maxShow))
			break
		}
		if c.IsWireless {
			line := fmt.Sprintf("📶 <b>%s</b> · %s", htmlEscape(c.Name), c.IP)
			if c.RSSI != 0 {
				line += fmt.Sprintf(" · %d dBm", c.RSSI)
			}
			if c.TxRate != 0 {
				line += fmt.Sprintf(" · %d Mbit/s", c.TxRate)
			}
			if c.WiFiMode != "" {
				line += " · " + c.WiFiMode
			}
			sb.WriteString(line + "\n")
		} else {
			sb.WriteString(fmt.Sprintf("🔌 <b>%s</b> · %s\n", htmlEscape(c.Name), c.IP))
		}
	}
	t.sendHTML(chatID, sb.String())
}

func (t *Tracker) handleRefreshCommand(chatID int64) {
	waitMsg := tgbotapi.NewMessage(chatID, "🔄 *Đang thực hiện quét hệ thống ngay lập tức...*")
	waitMsg.ParseMode = "Markdown"
	sentMsg, err := t.bot.Send(waitMsg)
	if err == nil {
		defer func() {
			deleteMsg := tgbotapi.NewDeleteMessage(chatID, sentMsg.MessageID)
			if _, delErr := t.bot.Request(deleteMsg); delErr != nil {
				log.Printf("⚠️ Không xoá wait message: %v", delErr)
			}
		}()
	}

	mesh, err := t.checkNow(false)
	if err != nil {
		t.sendText(chatID, "❌ Quét hệ thống thất bại: "+err.Error())
		return
	}
	t.sendHTML(chatID, renderMeshMap(mesh))
}
