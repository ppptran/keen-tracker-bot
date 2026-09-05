# ĐÁNH GIÁ KEEN-TRACKER-BOT & ĐỀ XUẤT THAY ĐỔI

> Ngày khảo sát: 2026-09-05
> Router thật: **Viettel NR3053 (Keenetic KN-3811)** — KeeneticOS **5.0.12**, IP `192.168.22.14`
> Phương pháp: đọc toàn bộ source bot + gọi trực tiếp RCI API bằng curl + đăng nhập Web UI thật, bắt request network và đọc code JS của Web UI để tái tạo đúng thuật toán nó lọc/dựng dữ liệu.

---

## 1. Hiện trạng code hiện tại

| File | Vai trò |
|---|---|
| `main.go` | Telegram bot: poll mesh mỗi `CHECK_INTERVAL`, alert online/offline cho thiết bị trong `devices.json`, lệnh `/status`, `/refresh` |
| `keenclient/client.go` | Auth x-ndw2-interactive (GET/POST `/auth`) + các hàm GET `/rci/show/*` |
| `keenclient/models.go` | Model `MeshMember`, `WiFIMesh`, `ControllerSnapshot`… + parser |
| `devices.json` | 7 thiết bị giám sát (Controller + 5 Agent + Xiaomi R3G) |
| `debug_keen.go` | Tool khảo sát Step-0 (build tag `ignore`) |
| `zzcli.md` | Ghi chú endpoint đã biết |

Luồng dữ liệu hiện tại: `GetWiFIMesh()` = `GET /rci/show/mws/member` (extenders) + `GET /rci/show/system` (controller) → lọc member trong `checkNow()` → so với `devices.json` → notify Telegram.

---

## 2. Kết quả khảo sát router thật

### 2.1 Auth — OK, không cần sửa

Flow trong `keenclient/client.go` khớp firmware thật: `GET /auth` (401 + challenge) → `SHA256(challenge + MD5(user:realm:pass))` → `POST /auth` → 200.

⚠️ Chi tiết quan trọng: **sau POST login, router XOAY giá trị session cookie** (ví dụ `OOBNPNGXGRQGQJ=OKCIOUWC…` → `OOBNPNGXGRQGQJ=NXKDGPP…`). Cookie cũ sẽ bị 401 trên mọi request RCI. Bot hiện vẫn chạy được là nhờ `cookiejar` của `net/http` tự lưu cookie mới từ response của POST — cơ chế `sessionCookie`/`cookieRoundTripper` thủ công trong `client.go` là dư thừa và có thể gây xung đột (nó chỉ inject khi header `Cookie` trống, nên jar thắng). **Nên dọn dẹp để tránh rủi ro sau này** (P2).

### 2.2 Dữ liệu mesh thực tế của mạng này

- Controller: `04:5F:A6:54:9C:C9` (MAC Bridge0), tên hiển thị Web UI **"Controller-P#33"**, model KN-3811, OS 5.0.12, uptime ~5 ngày.
- 5 extender đều nối **Ethernet backhaul** (`uplink: "GigabitEthernet0/Vlan1"`, speed 1000, `root`/`bridge` = `4000.04:5f:a6:54:9c:c9`), online đủ.
- **Xiaomi R3G (`34:ce:00:6e:e6:f1`)**: nằm trong `mws/member` nhưng **không có `mode`, không có `ip`, không có `backhaul`** → đây là node đã từng ghép nhưng hiện KHÔNG tham gia mesh. Web UI hiển thị nó là dòng **"Offline"**, KHÔNG ẩn đi.
- `mws/associations`, `mws/client`, `mws/candidate` trả về rỗng trên mạng này (backhaul thuần Ethernet, không có node chờ ghép).

### 2.3 Các endpoint đã xác thực (dùng được cho bot)

| Endpoint | Trả về thực tế | Ghi chú |
|---|---|---|
| `GET /rci/show/mws/member` | Mảng **6 member** (5 extender + 1 Xiaomi không tham gia). **KHÔNG chứa controller** | Mỗi member: `cid, mac, known-host, model, ip, mode, fw, associations, system{uptime,cpuload,memory}, backhaul{uplink,root,bridge,speed,duplex,port-label}, port[], rci.errors` |
| `GET /rci/show/identification` | `mac: 04:5f:a6:54:9c:ca`, `serial`, **`cid` của controller** | ⚠️ MAC ở đây là `…ca`, KHÁC với MAC mesh `…c9` (= MAC Bridge0). Đừng dùng MAC này để so khớp `backhaul.bridge` |
| `GET /rci/show/version` | `description: "Controller-P#33"` (**tên hiển thị của controller**), `model`, `title: "5.0.12"` (firmware) | Tên controller không nằm ở `system.hostname` |
| `GET /rci/show/system` | `hostname: "3053-Controller"`, `uptime`, CPU, RAM | Chỉ dùng cho uptime/health |
| `GET /rci/show/interface/Bridge0` | `mac: 04:5f:a6:54:9c:c9` | MAC mesh của controller — dùng để resolve "Via" |
| `GET /rci/show/ip/hotspot` | `{host: [256 entry]}` — **toàn bộ client có dây + không dây của cả mesh** | Client không dây có object `mws` (chi tiết bên dưới). GET thuần đã đủ, không bắt buộc POST |
| `POST /rci/` (batch) | Gộp nhiều `show` vào 1 request | Web UI dùng cách này; bot nên chuyển qua để giảm số round-trip |
| `POST /rci/` → `show.mws.log` | Log chuyển vùng/roam của client (timestamp, mac, ap, band, left) | Tiềm năng cho alert thông minh hơn (P2) |

**Object `mws` trong `ip/hotspot` (client không dây) — chìa khoá của bản đồ:**

```json
{
  "mac": "88:03:e9:48:70:cb", "ip": "192.168.22.121", "active": true,
  "name": "", "hostname": "", "link": "up",
  "mws": {
    "cid": "08dcdddc-41cc-11f1-931e-d9d2417b0be9",  // ← CID của node mesh mà client đang nối
    "ap": "WifiMaster1/AccessPoint0",
    "txrate": 325, "mode": "11ac", "rssi": -77, "uptime": 4253, "security": "open"
  }
}
```

- `mws.cid` khớp chính xác `cid` của từng member trong `mws/member`.
- Host **có dây** (nối vào controller) thì **không có** `mws` → đếm về phía controller.
- Host có `mws-backhaul: true` là link node↔node (không xuất hiện trên mạng này vì backhaul Ethernet).

---

## 3. Web UI dựng trang "Mesh Wi-Fi System" như thế nào (bằng chứng)

Đã bắt request + đọc code JS (`main-BRZQI6FO.js`) của Web UI. Ảnh tham chiếu: `docs/webui-mesh-nodes.png`.

### 3.1 Nguồn dữ liệu — MỘT batch POST duy nhất vào `/wifiSystem/members`

```json
[
  {"show":{"sc":{"mws":{"member":{}}}}},
  {"show":{"mws":{"member":{}}}},
  {"show":{"mws":{"status":{}}}},
  {"show":{"ip":{"hotspot":{"details":"wireless"}}}},
  {"show":{"system":{}}},
  {"show":{"sc":{"ip":{"dhcp":{"host":{}}}}}},
  {"show":{"components":{"status":{}}}},
  {"show":{"sc":{"interface":{"trait":"Ip"}}}},
  {"show":{"interface":{"details":"yes","trait":"Ip"}}},
  {"show":{"sc":{"interface":{"ipoe":{"parent":""}}}}},
  {"show":{"version":{}}},
  {"show":{"identification":{}}},
  {"show":{"sc":{"components":{"auto-update":{}}}}},
  {"show":{"mws":{"candidate":{}}}},
  {"show":{"sc":{"mws":{"backhaul":{}}}}},
  {"show":{"sc":{"mws":{"auto-ap-shutdown":{}}}}}
]
```

### 3.2 Node Controller được dựng từ 3 nguồn (không nằm trong `mws/member`)

Từ JS — `getControllerNode()`:

```js
{
  cid: identification.cid,
  mac: identification.mac,
  name: version.description,        // ← "Controller-P#33" (KHÔNG phải hostname 3053-Controller)
  isController: true,
  isOnline: true,                    // ← hard-code: controller chính là con router này
  firmware: version.title,           // "5.0.12"
  model: getModelName(version),      // "KN-3811 (KN-3811)"
  uptime: parseInt(system.uptime),   // từ show system
  ip: "", connectedVia: ""           // cố ý không hiển thị
}
```

### 3.3 Node Extender — quy tắc lọc từ `mws/member`

Từ JS — `extractMemberBaseNode()` + `getExtenderNodes()`:

```js
members.filter(m => !m.deleted).map(m => ({
  name: m["known-host"] || m.mac,
  isOnline: ("backhaul" in m),                 // ← ONLINE = CÓ object backhaul. Không có = Offline
  firmware: m.fw, model: m.model.replace("keenetic", ""),
  isUpdateAvailable: m.fw !== m["fw-available"],
  mode: m.mode, uptime: parseInt(m.system?.uptime || 0), ip: m.ip
}))
```

→ **Xiaomi R3G KHÔNG bị lọc bỏ** — nó được giữ lại và hiển thị `Offline` vì thiếu `backhaul`.

### 3.4 Đếm client theo node — `groupClientsByNode()`

```js
extenderMacs  = nodes.filter(n => !n.isController).map(n => n.mac)
controllerCid = nodes.find(n => n.isController).cid        // identification.cid

hosts.filter(h => h.active)                // chỉ host đang active
     .filter(h => !extenderMacs.includes(h.mac))   // bỏ chính các node extender
     .groupBy(h => h.mws?.cid ?? controllerCid)    // có dây/không có mws → về controller
```

**Kiểm chứng bằng dữ liệu thật:** kết quả tái tạo = Controller 244, Agent-2 = 4, Agent-4 = 1, Agent-5 = 1 → khớp từng con số Web UI hiển thị (chênh ±1 do lệch thời điểm lấy dữ liệu, bản thân UI cũng dao động 250↔252 giữa các lần load).

### 3.5 Cột "Via" (nút cha) — dùng `backhaul.bridge`, KHÔNG dùng `backhaul.root`

```js
getMemberConnectedViaData(member, members, controllerNode, controllerMac):
  bridge = member.backhaul.bridge            // "4000.04:5f:a6:54:9c:c9"
  mac = bridge.split(".")[1]                 // "04:5f:a6:54:9c:c9"
  if mac == controllerMac (MAC Bridge0):     → Via = tên + model của controller
  else: → tìm member có mac == mac đó        → Via = tên + model của member cha (multi-hop)
```

→ Hoạt động cho **cả** backhaul Ethernet lẫn Wi-Fi, và hỗ trợ mesh nhiều tầng (agent nối vào agent).

### 3.6 Cột "Connection"

- Ethernet backhaul: `backhaul.speed` + `duplex` → "1000 Mbit/s".
- Wi-Fi backhaul: nếu `backhaul` có key `ap` → hiển thị thông tin Wi-Fi (txrate/band) thay cho tốc độ có dây.

### 3.7 Tên client

Lấy trực tiếp `name` (tên đã đăng ký) hoặc `hostname` trong từng entry của `ip/hotspot` — đã kiểm chứng: 5 node extender có `name` đúng "Agent-2-P#36"…

---

## 4. Đánh giá 2 vấn đề của bot hiện tại

### 4.1 ❗ Vấn đề 1: "Controller không có trong JSON trả về" — ĐÚNG, và đây là gốc rễ

`mws/member` của firmware **không bao giờ** trả về controller (thiết kế của KeeneticOS — controller là "bên nhìn", chỉ trả về agent). Bot hiện xử lý bằng cách ghép thêm từ `/rci/show/system`, nhưng kết quả thiếu và có bug. Chạy `debug_keen.go` cho thấy JSON hiện tại:

```
controller: online=true name="3053-Controller" ip="192.168.22.14" mac="" status="offline" source="system"
```

So với Web UI (chuẩn cần đạt): `name="Controller-P#33"`, `model="KN-3811 (KN-3811)"`, `firmware="5.0.12"`, `cid`, `mac=04:5f:a6:54:9c:c9`, online chắc chắn.

Lỗi cụ thể trong code:

| # | Lỗi | Vị trí | Mức |
|---|---|---|---|
| 1.1 | Tên controller sai — lấy `hostname` ("3053-Controller") thay vì `version.description` ("Controller-P#33") | `models.go: parseSystemController()` | P0 |
| 1.2 | **Bug status**: `parseSystemController` khởi tạo `Status: "offline"` rồi chỉ ghi đè khi `Status == "" \|\| "unknown"` → `GetWiFIMesh().Controller.Status` **luôn là "offline"** dù `IsOnline=true`. Hàm `GetControllerSnapshot` "vá lại" bằng điều kiện riêng → 2 hàm trả kết quả khác nhau cho cùng dữ liệu | `models.go:262`, `client.go:683-687` | P0 |
| 1.3 | Thiếu `cid` controller → không thể nhóm client theo node như Web UI | `models.go` | P0 |
| 1.4 | MAC controller lệch nguồn: `identification.mac` = `…ca` nhưng MAC mesh (backhaul root/bridge, Bridge0) = `…c9`. devices.json đang dùng `…c9` (đúng cho việc khớp mesh) — nếu sau này lấy MAC từ `identification` để so khớp sẽ fail âm thầm | `client.go`, `main.go` | P1 |
| 1.5 | Không có model/firmware của controller trong JSON | `models.go` | P1 |

### 4.2 ❗ Vấn đề 2: "Map không giống Web UI"

Bản đồ Telegram hiện tại (thông điệp `/status`) chỉ là danh sách phẳng, thiếu gần như mọi thứ làm nên "bản đồ" của Web UI:

| Thông tin trên Web UI | Bot hiện tại | Nguyên nhân |
|---|---|---|
| Controller: tên, model, OS, uptime, **số client** (244) | Chỉ tên sai + online | Không gọi `version`/`identification`; không có `ip/hotspot` |
| Extender: Uptime | ❌ Không có | Có sẵn `system.uptime` trong member nhưng không dùng |
| Extender: **Connection** (1000 Mbit/s / Wi-Fi băng tần) | Chỉ "Agent (WiFi/Ethernet)" heuristic | Có `backhaul.speed/duplex` nhưng không hiển thị |
| Extender: **Via** (nút cha đúng, hỗ trợ multi-hop) | Dùng `root` cho wired → luôn "Controller" | `main.go: bridgeParentName()` chỉ dùng `bridge` khi uplink chứa "wifi" — sai với mesh Ethernet nhiều tầng; Web UI luôn dùng `bridge` |
| **Số client theo node đếm từ host thật** | Dùng `member.associations` (con số của router, với controller thì không có) | Chưa biết thuật toán `groupClientsByNode` |
| **Danh sách client từng node** (tên, IP, RSSI, txrate) | ❌ Không có | Chưa dùng `ip/hotspot` |
| Node offline vẫn hiện trong bảng (Xiaomi) | Bị `continue` bỏ qua trong `checkNow` (`main.go:316`) | Web UI: thiếu `backhaul` = Offline, vẫn hiển thị |
| Băng tần backhaul Wi-Fi (2.4/5GHz) | `meshNetworkTypeFromMember` dò chuỗi "5"/"2.4" trong `uplink` → **không bao giờ khớp** với format thật `WifiMaster0/Backhaul0` (0=2.4G, 1=5G) | `main.go:147-165` |
| Thiết bị mới chờ ghép (candidate) | ❌ | Chưa gọi `mws/candidate` |
| Alert offline dựa trên transition/roam | Chỉ poll so khớp | `show.mws.log` có sẵn lịch sử roam/join/leave |

Ngoài ra: `seen` map trong `checkNow()` (`main.go:311-326`) lọc theo `mode`/`root` — với firmware thật thì chỉ cần quy tắc **"có `backhaul` = online"** của Web UI là đủ và chính xác hơn.

---

## 5. Đề xuất thay đổi (theo độ ưu tiên)

> **Trạng thái triển khai (2026-09-05):** Đã làm xong **P0 (1–5)**, **P1 (6–10)** và **P2: 12, 13, 16**.
> Hoãn: **P2-11** (batch POST — tối ưu, GET hiện đủ dùng), **P2-14** (alert từ mws.log), **P2-15** (devices.json optional — giữ nguyên cơ chế cảnh báo qua devices.json).
> File thay đổi: `keenclient/models.go` (types mới, sửa bug status, thuật toán topology), `keenclient/client.go` (GetControllerNode/GetHotspotHosts/GetMeshAssociations/GetMeshCandidates, GetWiFIMesh mới, dọn session), `main.go` (checkNow trên Nodes, alert controller fail, /map, /clients), `debug_keen.go`, `.env.example` (`CONTROLLER_FAIL_THRESHOLD`), test mới: `keenclient/topology_test.go` + `TestRenderMeshMap`.
> Đã verify live với router thật: controller = "Controller-P#33" (KN-3811, OS 5.0.12, MAC Bridge0 …c9), clients từng node 4/0/1/1/0 khớp Web UI, Xiaomi hiện Offline, Via = Controller-P#33, Connection = 1000 Mbit/s.

### P0 — Sửa 2 vấn đề đang gặp + cảnh báo controller mất kết nối ✅ ĐÃ LÀM

1. ✅ **`keenclient`: thêm `GetControllerNode()`** — gọi thêm `GET /rci/show/version` + `GET /rci/show/identification` (và `GET /rci/show/interface/Bridge0` nếu cần MAC mesh) rồi build node controller đúng chuẩn Web UI: `cid`, `mac` (Bridge0), `name = version.description`, `model`, `firmware = version.title`, `isOnline: true`, `uptime = system.uptime`.
2. ✅ **Sửa bug status** trong `parseSystemController` (`models.go`): bỏ nhánh khởi tạo "offline" hoặc đổi điều kiện ghi đè; chuẩn hoá 1 nguồn sự thật — controller online ⇔ call `/rci/show/system` thành công.
3. ✅ **Gộp controller vào danh sách node**: `WiFIMesh` trả về `Nodes = [ControllerNode] + Members` (giữ nguyên `Members` để tương thích), thêm field `IsController`. Từ đây mọi chỗ (status Telegram, alert, JSON) đều nhìn 1 danh sách duy nhất — hết cảnh "controller lẻ loi ngoài JSON".
4. ✅ **Đổi quy tắc online/offline member** theo Web UI: `isOnline = có object backhaul` (thay cho bộ lọc `mode`/`root` trong `main.go:315-321`). Member thiếu backhaul (Xiaomi) → trạng thái Offline, vẫn hiện trên map thay vì bị ẩn.

5. ✅ **Cảnh báo "mất kết nối controller"** (bổ sung tự nhiên của mục 3): hiện khi không gọi được API, `checkNow()` chỉ log rồi return (`main.go:301-306`) → trạng thái đứng yên, không bao giờ có alert "controller offline". Thêm rule:
   - Đếm số chu kỳ poll thất bại liên tiếp; vượt ngưỡng N (env `CONTROLLER_FAIL_THRESHOLD`, mặc định 3) → gửi alert `🔴 Mất kết nối controller` đúng 1 lần (không lặp mỗi chu kỳ).
   - Khi poll thành công trở lại → gửi alert khôi phục `🟢 Controller đã phản hồi lại`.
   - Trong thời gian mất kết nối: giữ nguyên trạng thái cũ của các node trong `devices.json` (không đánh offline hàng loạt) để tránh spam alert; chỉ cảnh báo riêng cho controller.

### P1 — Đưa map về giống Web UI ✅ ĐÃ LÀM

6. ✅ **Thêm `GET /rci/show/ip/hotspot`** vào client (model mới `HotspotHost` gồm `mac/ip/name/hostname/active/link/mws{cid,ap,rssi,txrate,mode,uptime}/mws-backhaul`) và cài đúng thuật toán `groupClientsByNode`:
   - lọc `active`, loại các MAC trùng MAC extender, nhóm theo `mws.cid`, nhóm rơi về controller khi không có `mws`;
   - đếm số client từng node (chi tiết từng client xem qua lệnh `/clients` ở mục 13);
7. ✅ **Hiển thị bản đồ kiểu Web UI trong Telegram** (2026-09-05: `/map` đã GỘP vào `/status` theo yêu cầu — nội dung cũ của `/status` bỏ, chỉ còn 3 lệnh: `/status`, `/clients`, `/refresh`):
   ```
   🎛 Controller-P#33 · KN-3811 · OS 5.0.12
      Uptime 5d 05:06 · 🟢 Online · 👥 244 clients
      ├─ Agent-2-P#36  🟢 192.168.22.227 · 1000 Mbit/s · 5d 04:53 · 👥 4
      │  
      ├─ Agent-4-P#19  🟢 192.168.22.233 · 1000 Mbit/s · 5d 03:49 · 👥 1
      └─ Keenetic Xiaomi 3G 🔴 Offline (không tham gia mesh)
   ```
8. ✅ **Sửa `Via` cho mesh nhiều tầng**: luôn resolve `backhaul.bridge` (bỏ prefix `XXXX.`) — khớp MAC Bridge0 của controller → "Controller", ngược lại tra `known-host` của member cha. Bỏ điều kiện "chỉ khi uplink chứa wifi" hiện tại.
9. ✅ **Sửa nhận diện băng tần backhaul**: `WifiMaster0` → 2.4 GHz, `WifiMaster1` → 5 GHz (quy ước Keenetic); xoá heuristic dò "5"/"2.4" trong chuỗi uplink (`main.go:147-165`).
10. ✅ **Hiển thị Connection**: Ethernet → `speed` + "Mbit/s"; Wi-Fi backhaul → txrate/band (khi có, lấy từ `mws/associations` — hiện rỗng vì mạng này dùng Ethernet).

### P2 — Nâng cấp trải nghiệm

11. ⏸ (hoãn) **Chuyển sang batch `POST /rci/`** cho 1 chu kỳ poll (member + version + identification + system + hotspot trong 1 request) — giảm 5 HTTP call xuống 1, giảm tải cho router.
12. ✅ **Dọn session handling**: bỏ `cookieRoundTripper`/`capturedCookie`, tin vào `cookiejar` (nó đã giữ cookie sau khi router xoay); thêm 1 bước re-auth khi 401 (đã có sẵn trong `doGet` — giữ nguyên).
13. ✅ **`/clients <node>`**: xem chi tiết client của một node (tên, IP, RSSI, txrate, uptime).
14. ⏸ (hoãn) **Alert dùng `show.mws.log`**: khi client roam giữa các node hoặc node rời mesh → log sự kiện có timestamp, chính xác hơn so khớp poll.
15. ⏸ (hoãn) **`devices.json` trở thành optional**: bot có thể tự phát hiện node từ `mws/member` (controller + 5 agent); file json chỉ còn để đặt tên hiển thị/ghim thiết bị cần cảnh báo.
16. ✅ **Candidate**: hiển thị thiết bị đang chờ ghép mesh (nếu có) như mục "Chờ ghép" trên map.

---

## 6. Bảng ánh xạ: dữ liệu Web UI ← endpoint (checklist khi code lại)

| Dữ liệu cần | Endpoint / trường | Ghi chú |
|---|---|---|
| Tên controller | `show version` → `description` | KHÔNG dùng `system.hostname` |
| MAC mesh controller | `show interface Bridge0` → `mac` (`…c9`) | `identification.mac` là `…ca`, chỉ dùng cho cid/serial |
| CID controller | `show identification` → `cid` | Khớp `mws.cid` trong hotspot |
| Model/OS controller | `show version` → `model`, `title` | |
| Uptime controller | `show system` → `uptime` | Giây, dạng chuỗi |
| Danh sách extender | `show mws member` (lọc `deleted`) | Không chứa controller |
| Node online? | có `backhaul` trong member | Thiếu = Offline (giữ lại, đừng ẩn) |
| Nút cha (Via) | `backhaul.bridge` bỏ prefix `XXXX.` | Bridge0-MAC = controller; ngược lại tra member |
| Tốc độ có dây | `backhaul.speed` + `duplex` | |
| Client theo node | `show ip hotspot` → nhóm `mws.cid` (mặc định = controller cid), lọc `active`, loại MAC extender | GET thuần đã có `mws` |
| Tên client | `hotspot.name`/`hostname` | |
| Chi tiết Wi-Fi client | `hotspot.mws.{rssi,txrate,mode,uptime}` | |
| Thiết bị chờ ghép | `show mws candidate` | |
| Log roam/join/leave | `POST /rci/` → `{"show":{"mws":{"log":{}}}}` | |

---

## 7. Phụ lục — bằng chứng khảo sát

- Auth: `GET /auth` → 401 + `X-NDM-Challenge`/`X-NDM-Realm` → POST hash → **HTTP 200**; cookie bị xoay sau POST, cookie cũ → 401.
- `mws/member` trả 6 entry: 5 extender (`mode: extender`, `uplink: GigabitEthernet0/Vlan1`, `root/bridge: 4000.04:5f:a6:54:9c:c9`) + 1 entry Xiaomi **không có `mode`/`ip`/`backhaul`**.
- Tái tạo `groupClientsByNode` từ dữ liệu thật: Controller 244, Agent-2 4, Agent-4 1, Agent-5 1 — **khớp Web UI** (biên độ ±1 do thời điểm lấy dữ liệu; UI cũng dao động 250↔252).
- JS Web UI (`main-BRZQI6FO.js`): `extractMemberBaseNode` (`isOnline: has(member,"backhaul")`), `getControllerNode` (`name: version.description`, `isOnline: true`), `groupClientsByNode` (đoạn mã trích trong mục 3.4), `getMemberConnectedViaData` (dùng `bridge`, so với MAC Bridge0).
- Ảnh chụp trang **Mesh Wi-Fi System → Nodes**: `docs/webui-mesh-nodes.png`.
