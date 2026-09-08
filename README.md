# Keen Tracker Bot

<div align="center">

**English** | [🇻🇳 Tiếng Việt](README_VN.md)

</div>

A Telegram bot that monitors a **Keenetic router's Mesh Wi-Fi system** (designed and deployed on **Viettel NR3053 / Keenetic KN-3811**, KeeneticOS 5.x).

- `/status` — scans the mesh and sends a tree-style map: Controller → Agents (IP, client count, backhaul speed, uptime, Wi-Fi band); offline nodes are still shown.
- `/clients <node name>` — lists the clients actually connected to a given node (name, IP, RSSI, Wi-Fi speed).
- `/refresh` — runs a scan immediately and resends the map.

Besides the commands, the bot automatically alerts you when devices listed in `devices.json` go online/offline and when the connection to the controller is lost.

## Telegram Interface

When the bot starts:

```
✅ Keenetic Tracker Bot started successfully!

📊 Monitoring: 7 devices
⏱ Scan interval: 1m
🧭 Commands: /status · /clients · /refresh
```

Typing `/status` — the mesh map is laid out like the Web UI's *Mesh Wi-Fi System* page (offline nodes are still shown; `↑` marks the parent node in a multi-tier mesh):

```
🎛 Controller · KN-3811 · OS 5.0.12
   Uptime 5d 05:06 · 🟢 Online · 👥 2 direct wireless clients
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
       (not joined to the mesh)

📊 Controller 1 · Extenders 6 · Wireless 11 · Wired 241
🕒 Updated at: 16:43:49 05/09/2026
```

> ⚠️ **CPU architecture:** the KN-3811 router is **aarch64 (ARM 64-bit)** — the binary that runs on the router must be `keen-tracker-bot-linux-arm64` (pre-built in this repo). The `-amd64` build is only for x86 computers.

---

## 1. Prepare the router: SSH + Entware (required)

The bot runs **directly on the router**, so the router needs Entware installed and SSH enabled. Everything is done in the router's Web UI (`http://192.168.1.1`).

### 1a. Install system components

Go to **Management → System Settings** → the *KeeneticOS Update and Component Options* section → **Show components**, and make sure these two components are installed:

- **SSH server**
- **OPKG package system**

(If they're missing, tick them → Apply; the router will download and install them — Internet required.)

### 1b. Enable SSH for a user

Go to **Management → Users and Access**:

- **User Accounts**: an `admin` user (with a password) exists by default. You can use it as is or **Create user** separately.
- Scroll down to **Administrative Services → Inbound Management Access**: tick **via SSH** (default port 22).

### 1c. Install Entware (required — without it you can't escape the `(config)>` CLI)

Go to **Management → OPKG** (the *OPKG Package Manager* page):

1. **Drive**: select **Internal storage** → **Save** (the KN-3811 offers this option out of the box; to use USB instead, select the USB drive — make sure it's plugged in).
2. **User Access**: tick the user that needs access (`admin` is ticked by default).

⚠️ Selecting the Drive does **not** install Entware yet — you must load the `aarch64-installer.tar.gz` installer (the KN-3811 is aarch64) from `bin.entware.net`. Two ways to do it:

**Option A — let the router download it (recommended):** SSH into the router and run at the `(config)>` prompt:

```
(config)> opkg disk storage:/ https://bin.entware.net/aarch64-k3.10/installer/aarch64-installer.tar.gz
(config)> system configuration save
(config)> system reboot
```

**Option B — download the file to your computer and upload it (if the router can't download from the Internet):**

1. Download the file: `https://bin.entware.net/aarch64-k3.10/installer/aarch64-installer.tar.gz`
2. Web UI → **Management → Applications** → the *USB Devices* section → **Internal storage** → create an `install` folder → upload the downloaded file there.
3. SSH into the router: `(config)> opkg disk storage:/` → `system configuration save` → `system reboot`.

After the reboot, the router unpacks the installer and installs Entware (~1–2 minutes; the log shows `"Entware" installed!`). To verify:

```
(config)> exec sh
```

→ landing in the **BusyBox shell** (`/ #`) means success — this is exactly how you escape the `(config)>` CLI.

> Optional security step: in the shell, run `passwd root` to change the Entware root password (default is `root`/`keenetic`).

> Entware is a Debian-like environment that runs inside KeeneticOS. Thanks to it you get a real shell on the router (`exec sh`) and a persistent `/opt` directory that survives reboots.

---

## 2. SSH into the router and enter the Entware shell

SSH-ing into the router drops you into the **Keenetic CLI** (not a Linux shell — commands like `uname`, `ls`, etc. return `no such command`). You need Entware to get to a shell:

```bash
ssh admin@192.168.1.1        # enter your password
```

At the Keenetic CLI `(config)>` prompt, type:

```
exec sh
```

→ you're in the **Entware shell** (BusyBox). From here, all Linux commands work. (`exit` goes back to the CLI.)

---

## 3. Create the `keenetic-bot` folder and copy exactly 3 files

The bot needs exactly **3 files** in the same folder (e.g. `/opt/keenetic-bot`):

| File | Taken from |
|---|---|
| `keen-tracker-bot-linux-arm64` | pre-built binary (Release) |
| `.env` | from `.env.example` |
| `devices.json` | from `devices.json.example` |

**Simplest file transfer — the router pulls from your computer:** from the repo folder on your computer, start a temporary HTTP server:

```bash
cd keen-tracker-bot
python3 -m http.server 8000
```

Then, in the router's Entware shell:

```sh
mkdir -p /opt/keenetic-bot && cd /opt/keenetic-bot

# replace 192.168.1.XXX with your computer's IP
wget http://192.168.1.XXX:8000/keen-tracker-bot-linux-arm64
wget http://192.168.1.XXX:8000/.env.example -O .env
wget http://192.168.1.XXX:8000/devices.json.example -O devices.json

chmod +x keen-tracker-bot-linux-arm64
```

*(Alternative: `opkg install openssh-sftp-server`, then pull the files over with WinSCP/scp.)*

---

## 4. Edit `.env`

```sh
vi .env        # or: opkg install nano && nano .env
```

```ini
# Telegram
TELEGRAM_TOKEN=123456:ABC...      # token from @BotFather
TELEGRAM_CHAT_ID=........       # chat ID that receives alerts

# Bot language: vi (Vietnamese) or en (English); default vi
BOT_LANG=vi

# Router — the bot runs on the router itself, so use its own IP
KEENETIC_IP=192.168.1.1
KEENETIC_USERNAME=admin
KEENETIC_PASSWORD=...

KEENETIC_INSECURE_SKIP_VERIFY=true
CHECK_INTERVAL=1m                 # scan interval

# Number of consecutive failed scans before the "controller connection lost" alert
CONTROLLER_FAIL_THRESHOLD=3
```

> Since the bot runs on the router itself, you can try `127.0.0.1` for `KEENETIC_IP`; if that doesn't work, use the LAN IP (`192.168.1.1`).

---

## 5. Edit `devices.json`

This file is **only used for alerting** when devices go online/offline (the list of MAC addresses to watch) and for custom names. The node/controller names shown in `/status` are fetched from the router by the bot itself.

```json
[
  { "mac": "00:00:00:00:00:00", "name": "Controller" },
  { "mac": "00:00:00:00:00:00", "name": "Agent-1" }
]
```

> **Note on the Controller's MAC:** it must be the **Bridge0 MAC** (the mesh MAC — see the Web UI under **My Networks and Wi-Fi → Home segment**, or the MAC portion of `backhaul.root/bridge`). It may differ from the MAC shown on the router's identification page in the last character. A wrong MAC → no controller alerts.
> Nodes not listed in this file still appear in `/status`, but they get **no** online/offline **alerts**.

---

## 6. Run automatically on every router boot

Entware does **not** use systemd (there's no `systemctl` on the router) — its equivalent is an init script in `/opt/etc/init.d/`, which runs automatically at boot and still supports `start / stop / restart / status`.

Create the file `/opt/etc/init.d/S99keenetic-bot`:

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
    cd "$DIR" || return 1        # the bot reads .env/devices.json from the current directory
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

A script named `S99*` is **run automatically by Entware when the router boots** (including after a power outage). Control commands:

```sh
/opt/etc/init.d/S99keenetic-bot start      # start
/opt/etc/init.d/S99keenetic-bot stop       # stop
/opt/etc/init.d/S99keenetic-bot restart    # restart
/opt/etc/init.d/S99keenetic-bot status     # check status
tail -f /opt/keenetic-bot/bot.log          # view logs
```

---

## 7. First-run check

```sh
/opt/etc/init.d/S99keenetic-bot start
tail -f /opt/keenetic-bot/bot.log
```

Once you see the boot lines (no `❌`) and receive the **"Keenetic Tracker Bot started successfully!"** message on Telegram, you're done. Try `/status`, `/clients Agent-2`, `/refresh`.

---

## Updating the bot later

```sh
/opt/etc/init.d/S99keenetic-bot stop
# drop the new binary into /opt/keenetic-bot/ (wget/scp as in step 3)
/opt/etc/init.d/S99keenetic-bot start
```

## Build from source (requires Go on your computer)

```bash
# for the router (aarch64) — this is the file to deploy
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o keen-tracker-bot-linux-arm64 .

# for an x86-64 computer (testing/debugging)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o keen-tracker-bot-linux-amd64 .
```

## License

Released under the **GNU General Public License v3.0** — see the full text in the [LICENSE](LICENSE) file.

- **EN:** This program is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 3. Anyone may use, install, study, modify and share it — including for commercial purposes — as long as redistributed modified versions remain GPL-3.0 licensed. Copyright (c) 2026 detran.
- An unofficial Vietnamese translation of the GPL is available at [gnu.org/licenses/gpl-3.0.vi.html](https://www.gnu.org/licenses/gpl-3.0.vi.html).
