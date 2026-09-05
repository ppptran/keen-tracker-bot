# Keen Tracker Bot

Bot Telegram giám sát hệ thống **Mesh Wi-Fi của router Keenetic** (khảo sát và triển khai trên **Viettel NR3053 / Keenetic KN-3811**, KeeneticOS 5.x).

- `/status` — quét mesh và gửi bản đồ dạng cây: Controller → các Agent (IP, số client, tốc độ backhaul, uptime, băng tần Wi-Fi), node offline vẫn hiển thị.
- `/clients <tên node>` — liệt kê client đang kết nối thật sự vào một node (tên, IP, RSSI, tốc độ Wi-Fi).
- `/refresh` — quét ngay và gửi lại bản đồ.

Ngoài lệnh, bot tự cảnh báo khi thiết bị trong `devices.json` online/offline và khi mất kết nối với controller.

## Giao diện trên Telegram

Khi bot khởi chạy:

```
✅ Keenetic Tracker Bot đã khởi chạy thành công!

📊 Giám sát: 7 thiết bị
⏱ Chu kỳ quét: 1m
🧭 Lệnh: /status · /clients · /refresh
```

Khi gõ `/status` — bản đồ mesh dựng đúng kiểu trang *Mesh Wi-Fi System* của Web UI (node offline vẫn hiển thị, `↑` chỉ node cha khi mesh nhiều tầng):

```
🎛 Controller · KN-3811 · OS 5.0.12
   Uptime 5d 05:06 · 🟢 Online · 👥 24 clients
├─ Agent-2  🟢 192.168.1.227
│      👥 4 · 1000 Mbit/s · 5d 04:53
├─ Agent-3  🟢 192.168.1.231
│      👥 0 · 1000 Mbit/s · 5d 04:32
├─ Agent-4  🟢 192.168.1.233
│      👥 1 · 1000 Mbit/s · 5d 03:49
├─ Agent-5  🟢 192.168.1.236
│      👥 1 · 1000 Mbit/s · 5d 03:53
├─ Agent-6  🟢 192.168.1.237
│      👥 0 · 1000 Mbit/s · 5d 03:36
└─ Agent-7 🔴 Offline
       (không tham gia mesh)

📊 Controller 1 · Extenders 6 · Clients 25
🕒 Cập nhật lúc: 16:43:49 05/09/2026
```

> ⚠️ **Kiến trúc CPU:** router KN-3811 là **aarch64 (ARM 64-bit)** — file chạy trên router phải là `keen-tracker-bot-linux-arm64` (đã build sẵn trong repo). Bản `-amd64` chỉ dùng cho máy tính x86.

---

## 1. Chuẩn bị router: SSH + Entware (bắt buộc)

Bot chạy **ngay trên router**, nên router phải có Entware và bật SSH. Tất cả làm trên Web UI của router (`http://192.168.1.1`).

### 1a. Cài system components

Vào **Management → System Settings** → mục *KeeneticOS Update and Component Options* → **Show components**, đảm bảo đã cài 2 thành phần:

- **SSH server**
- **OPKG package system**

(Nếu thiếu, tick chọn → Apply, router sẽ tải về và cài — cần Internet.)

### 1b. Bật SSH cho user

Vào **Management → Users and Access**:

- **User Accounts**: có sẵn user `admin` (có mật khẩu). Có thể dùng luôn hoặc **Create user** riêng.
- Kéo xuống mục **Administrative Services → Inbound Management Access**: tick **via SSH** (port mặc định 22).

### 1c. Cài Entware (bắt buộc — không có thì không thoát được `(config)>`)

Vào **Management → OPKG** (trang *OPKG Package Manager*):

1. **Drive**: chọn **Internal storage** → **Save** (KN-3811 có sẵn lựa chọn này; muốn dùng USB thì chọn ổ USB — nhớ cắm sẵn).
2. **User Access**: tick user cần quyền (mặc định `admin` đã được tick).

⚠️ Chọn Drive xong **chưa có Entware** — phải nạp installer `aarch64-installer.tar.gz` (router KN-3811 là aarch64) từ `bin.entware.net`. Có 2 cách:

**Cách A — router tự tải (khuyên dùng):** SSH vào router, tại prompt `(config)>` chạy:

```
(config)> opkg disk storage:/ https://bin.entware.net/aarch64-k3.10/installer/aarch64-installer.tar.gz
(config)> system configuration save
(config)> system reboot
```

**Cách B — tải file về máy rồi upload (nếu router không tải được từ Internet):**

1. Tải file: `https://bin.entware.net/aarch64-k3.10/installer/aarch64-installer.tar.gz`
2. Web UI → **Management → Applications** → mục *USB Devices* → **Internal storage** → tạo thư mục `install` → upload file vừa tải vào đó.
3. SSH vào router: `(config)> opkg disk storage:/` → `system configuration save` → `system reboot`.

Sau khi reboot, router giải nén installer và cài Entware (~1–2 phút, log hiện `"Entware" installed!`). Kiểm tra:

```
(config)> exec sh
```

→ nhảy vào **BusyBox shell** (`/ #`) là thành công — đây chính là chỗ thoát được `(config)>`.

> Tuỳ chọn bảo mật: trong shell gõ `passwd root` để đổi mật khẩu root của Entware (mặc định `root`/`keenetic`).

> Entware là môi trường Debian-like chạy bên trong KeeneticOS. Nhờ nó ta mới có shell thật trên router (`exec sh`) và thư mục `/opt` bền vững qua lần khởi động lại.

---

## 2. SSH vào router và vào shell Entware

SSH vào router sẽ rơi vào **CLI của Keenetic** (không phải shell Linux — lệnh `uname`, `ls`… sẽ báo `no such command`). Cần Entware để vào shell:

```bash
ssh admin@192.168.1.1        # nhập mật khẩu
```

Tại prompt `(config)>` của Keenetic CLI, gõ:

```
exec sh
```

→ vào **shell Entware** (BusyBox). Từ đây mọi lệnh Linux hoạt động. (`exit` để thoát ngược lại CLI.)

---

## 3. Tạo thư mục `keenetic-bot` và copy đúng 3 file

Bot cần đúng **3 file** nằm cùng thư mục (ví dụ `/opt/keenetic-bot`):

| File | Lấy từ |
|---|---|
| `keen-tracker-bot-linux-arm64` | binary đã build (repo) |
| `.env` | từ `.env.example` |
| `devices.json` | từ `devices.json.example` |

**Cách chuyển file đơn giản nhất — router kéo từ máy tính:** đứng ở thư mục repo trên máy tính, mở HTTP server tạm:

```bash
cd keen-tracker-bot
python3 -m http.server 8000
```

Rồi trên shell Entware của router:

```sh
mkdir -p /opt/keenetic-bot && cd /opt/keenetic-bot

# thay 192.168.22.XXX bằng IP máy tính của bạn
wget http://192.168.1.XXX:8000/keen-tracker-bot-linux-arm64
wget http://192.168.1.XXX:8000/.env.example -O .env
wget http://192.168.1.XXX:8000/devices.json.example -O devices.json

chmod +x keen-tracker-bot-linux-arm64
```

*(Cách khác: `opkg install openssh-sftp-server` rồi dùng WinSCP/scp kéo file qua.)*

---

## 4. Sửa `.env`

```sh
vi .env        # hoặc: opkg install nano && nano .env
```

```ini
# Telegram
TELEGRAM_TOKEN=123456:ABC...      # token từ @BotFather
TELEGRAM_CHAT_ID=........       # chat ID nhận cảnh báo

# Router — bot chạy ngay trên router nên dùng chính IP nó
KEENETIC_IP=192.168.1.1
KEENETIC_USERNAME=admin
KEENETIC_PASSWORD=...

KEENETIC_INSECURE_SKIP_VERIFY=true
CHECK_INTERVAL=1m                 # chu kỳ quét

# Số chu kỳ quét hỏng liên tiếp trước khi cảnh báo "mất kết nối controller"
CONTROLLER_FAIL_THRESHOLD=3
```

> Bot chạy trên chính router nên `KEENETIC_IP` có thể thử `127.0.0.1`; nếu không được thì dùng IP LAN (`192.168.1.1`).

---

## 5. Sửa `devices.json`

File này **chỉ dùng để cảnh báo** online/offline (danh sách MAC cần theo dõi) và đặt tên riêng. Tên node/controller hiển thị trên `/status` bot tự lấy từ router.

```json
[
  { "mac": "00:00:00:00:00:00", "name": "Controller" },
  { "mac": "00:00:00:00:00:00", "name": "Agent-1" }
]
```

> **Lưu ý MAC của Controller:** phải là **MAC Bridge0** (MAC mesh, xem trên Web UI **My Networks and Wi-Fi → Home segment**, hoặc chính là phần MAC trong `backhaul.root/bridge`). Nó có thể khác 1 ký tự cuối với MAC trong trang nhận diện router. MAC sai → không cảnh báo được controller.
> Node chưa có trong file vẫn hiện trên `/status`, nhưng sẽ **không có cảnh báo** online/offline.

---

## 6. Tự chạy mỗi lần router khởi động

Entware **không dùng systemd** (không có `systemctl` trên router) — tương đương của nó là script init trong `/opt/etc/init.d/`, tự chạy lúc boot và vẫn có `start / stop / restart / status`.

Tạo file `/opt/etc/init.d/S99keenetic-bot`:

```sh
cat > /opt/etc/init.d/S99keenetic-bot << 'EOF'
#!/bin/sh
DIR=/opt/keenetic-bot
BIN=$DIR/keen-tracker-bot-linux-arm64
PIDFILE=$DIR/bot.pid
LOGFILE=$DIR/bot.log

start() {
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "keenetic-bot already running (pid $(cat "$PIDFILE"))"
        return 0
    fi
    cd "$DIR" || return 1        # bot đọc .env/devices.json theo thư mục hiện tại
    nohup "$BIN" >> "$LOGFILE" 2>&1 &
    echo $! > "$PIDFILE"
    echo "keenetic-bot started (pid $(cat "$PIDFILE"))"
}

stop() {
    if [ -f "$PIDFILE" ] && kill "$(cat "$PIDFILE")" 2>/dev/null; then
        rm -f "$PIDFILE"
        echo "keenetic-bot stopped"
    else
        rm -f "$PIDFILE"
        echo "keenetic-bot not running"
    fi
}

status() {
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "running (pid $(cat "$PIDFILE"))"
    else
        echo "stopped"
    fi
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; sleep 1; start ;;
    status)  status ;;
    *)       echo "Usage: $0 {start|stop|restart|status}" ;;
esac
EOF
chmod +x /opt/etc/init.d/S99keenetic-bot
```

Script tên `S99*` sẽ được Entware **tự chạy khi router khởi động** (kể cả khi mất điện). Các lệnh điều khiển:

```sh
/opt/etc/init.d/S99keenetic-bot start      # chạy
/opt/etc/init.d/S99keenetic-bot stop       # dừng
/opt/etc/init.d/S99keenetic-bot restart    # chạy lại
/opt/etc/init.d/S99keenetic-bot status     # xem trạng thái
tail -f /opt/keenetic-bot/bot.log          # xem log
```

---

## 7. Kiểm tra lần đầu

```sh
/opt/etc/init.d/S99keenetic-bot start
tail -f /opt/keenetic-bot/bot.log
```

Thấy dòng boot (không có `❌`) và nhận được tin **"Keenetic Tracker Bot đã khởi chạy thành công!"** trên Telegram là xong. Thử `/status`, `/clients Agent-2`, `/refresh`.

---

## Cập nhật bot về sau

```sh
/opt/etc/init.d/S99keenetic-bot stop
# thay file binary mới vào /opt/keenetic-bot/ (wget/scp như bước 3)
/opt/etc/init.d/S99keenetic-bot start
```

## Build từ nguồn (máy tính cần cài Go)

```bash
# cho router (aarch64) — file này dùng để deploy
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o keen-tracker-bot-linux-arm64 .

# cho máy tính x86-64 (chạy thử/debug)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o keen-tracker-bot-linux-amd64 .
```

## License / Giấy phép

Phát hành theo **GNU General Public License v3.0** — xem toàn bộ nội dung trong file [LICENSE](LICENSE).

- **EN:** This program is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 3. Anyone may use, install, study, modify and share it — including for commercial purposes — as long as redistributed modified versions remain GPL-3.0 licensed. Copyright (c) 2026 detran.
- **VI:** Đây là phần mềm tự do: bất kỳ ai cũng có thể tải, cài, sử dụng, nghiên cứu, sửa đổi và chia sẻ lại — kể cả cho mục đích thương mại (bán phần mềm, bán dịch vụ cài đặt) — với điều kiện bản phái sinh khi phân phối phải tiếp tục phát hành theo GPL-3.0 (mở nguồn). Bản dịch tiếng Việt không chính thức của GPL xem tại [gnu.org/licenses/gpl-3.0.vi.html](https://www.gnu.org/licenses/gpl-3.0.vi.html).
