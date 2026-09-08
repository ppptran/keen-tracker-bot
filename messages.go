package main

// Supported bot languages, selected via BOT_LANG in .env. Anything unknown or
// empty falls back to Vietnamese, the language the bot originally spoke.
const (
	langVI = "vi"
	langEN = "en"
)

// messages holds every Telegram-facing text the bot sends. It is a struct (not
// a map) so the compiler forces both languages to define every template; the
// format-verb parity between languages is checked by TestMessageCatalogsInSync.
type messages struct {
	boot           string // ✅ boot notification: device count, interval
	shutdown       string // 👋 graceful-stop notification
	controllerDown string // 🔴 controller unreachable: host, threshold, time
	controllerUp   string // 🟢 controller recovered: host, time
	onlineNotify   string // 🟢 device online: name, MAC, IP, connection, mesh node, clients, time
	offlineNotify  string // 🚨 device offline: name, MAC, last IP, last mesh node, last seen
	scanFail       string // ❌ mesh scan failed: error
	refreshWait    string // 🔄 /refresh wait message
	refreshFail    string // ❌ /refresh scan failed: error
	meshCandidates string // ➕ /status devices waiting to join the mesh
	meshUpdated    string // 🕒 /status footer: time
	neverSeen      string // formatTime zero-time label
	netUnknown     string // formatNetworkType unknown network type
	clientsUsage   string // ℹ️ /clients called without arguments
	nodeNotFound   string // ❓ /clients unknown node: arg, node list
	clientsHeader  string // 📱 /clients header: node, total, wireless, wired, shown
	noClients      string // /clients no connected clients
	moreClients    string // /clients overflow: hidden client count
}

var catalogs = map[string]*messages{
	langVI: {
		boot:           "✅ *Keenetic Tracker Bot đã khởi chạy thành công!*\n\n📊 *Giám sát:* %d thiết bị\n⏱ *Chu kỳ quét:* %s\n🧭 Lệnh: /status · /clients · /refresh",
		shutdown:       "👋 *Keenetic Tracker Bot đang tạm dừng hoạt động.*",
		controllerDown: "🔴 *Mất kết nối controller!*\n\nKhông gọi được API của `%s` trong %d chu kỳ quét liên tiếp.\n🕒 _%s_",
		controllerUp:   "🟢 *Controller đã phản hồi lại!*\n\nAPI `%s` hoạt động bình thường trở lại.\n🕒 _%s_",
		onlineNotify:   "🟢 *Thiết bị trực tuyến trở lại!*\n\n📶 *%s*\n• MAC: `%s`\n• IP: `%s`\n• Kết nối: `%s`\n• Nút Mesh: `%s`\n• 📱 Client kết nối: *%d*\n🕒 Cập nhật: _%s_",
		offlineNotify:  "🚨 *Cảnh báo thiết bị ngoại tuyến!*\n\n🔴 *%s*\n• MAC: `%s`\n• IP cuối: `%s`\n• Nút Mesh cuối: `%s`\n🕒 Lần cuối thấy: _%s_",
		scanFail:       "❌ Không quét được mesh: %s",
		refreshWait:    "🔄 *Đang thực hiện quét hệ thống ngay lập tức...*",
		refreshFail:    "❌ Quét hệ thống thất bại: %s",
		meshCandidates: "➕ <b>Chờ ghép mesh:</b>",
		meshUpdated:    "🕒 <i>Cập nhật lúc: %s</i>",
		neverSeen:      "Chưa từng thấy",
		netUnknown:     "Không xác định",
		clientsUsage:   "ℹ️ Dùng: /clients <tên node>\nVí dụ: /clients Agent-2 hoặc /clients controller",
		nodeNotFound:   "❓ Không tìm thấy node \"%s\".\nCác node hiện có: %s",
		clientsHeader:  "📱 <b>%s</b> — tổng 👥 %d clients (%d wireless · %d wired)\n(×%d đang kết nối thực sự)\n\n",
		noClients:      "Không có client nào đang kết nối thực sự.\n",
		moreClients:    "… và %d client nữa\n",
	},
	langEN: {
		boot:           "✅ *Keenetic Tracker Bot started successfully!*\n\n📊 *Monitoring:* %d devices\n⏱ *Scan interval:* %s\n🧭 Commands: /status · /clients · /refresh",
		shutdown:       "👋 *Keenetic Tracker Bot is shutting down.*",
		controllerDown: "🔴 *Controller connection lost!*\n\nCould not reach the API of `%s` for %d consecutive scan cycles.\n🕒 _%s_",
		controllerUp:   "🟢 *Controller is responding again!*\n\nAPI `%s` is back to normal.\n🕒 _%s_",
		onlineNotify:   "🟢 *Device is back online!*\n\n📶 *%s*\n• MAC: `%s`\n• IP: `%s`\n• Connection: `%s`\n• Mesh node: `%s`\n• 📱 Connected clients: *%d*\n🕒 Updated: _%s_",
		offlineNotify:  "🚨 *Device went offline!*\n\n🔴 *%s*\n• MAC: `%s`\n• Last IP: `%s`\n• Last mesh node: `%s`\n🕒 Last seen: _%s_",
		scanFail:       "❌ Failed to scan the mesh: %s",
		refreshWait:    "🔄 *Scanning the system now...*",
		refreshFail:    "❌ System scan failed: %s",
		meshCandidates: "➕ <b>Waiting to join the mesh:</b>",
		meshUpdated:    "🕒 <i>Updated at: %s</i>",
		neverSeen:      "Never seen",
		netUnknown:     "Unknown",
		clientsUsage:   "ℹ️ Usage: /clients <node name>\nExample: /clients Agent-2 or /clients controller",
		nodeNotFound:   "❓ Node \"%s\" not found.\nAvailable nodes: %s",
		clientsHeader:  "📱 <b>%s</b> — 👥 %d clients in total (%d wireless · %d wired)\n(×%d actually connected)\n\n",
		noClients:      "No clients are actually connected.\n",
		moreClients:    "… and %d more client(s)\n",
	},
}

// msgOf returns the catalog for lang, falling back to Vietnamese.
func msgOf(lang string) *messages {
	if m, ok := catalogs[lang]; ok {
		return m
	}
	return catalogs[langVI]
}

// msg returns the tracker's language catalog for message formatting.
func (t *Tracker) msg() *messages { return msgOf(t.lang) }
