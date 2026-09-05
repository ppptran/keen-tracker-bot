//go:build ignore

// debug_keen is a Step-0 survey tool. It validates the Keenetic auth flow and
// dumps the raw RCI responses so we can confirm:
//
//	1. The MD5 Challenge-Response flow works (GET /auth challenge + realm).
//	2. GET /rci/show/mws/member returns the full mesh member list.
//	3. GET /rci/show/system returns the minimal controller snapshot.
//	4. We intentionally do not rely on /rci/show/mws/status and /rci/show/mws/client
//	   because the firmware does not provide useful data there for this bot.
//
// Usage against a real router:
//
//	cp .env.example .env   # fill KEENETIC_* vars
//	sed '/^\/\/go:build ignore/d' debug_keen.go > /tmp/debug_keen_run.go
//	go run /tmp/debug_keen_run.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/joho/godotenv"

	"keen-tracker-bot/keenclient"
)

func main() {
	godotenv.Load()

	host := getenv("KEENETIC_IP", "192.168.1.1")
	user := getenv("KEENETIC_USERNAME", "admin")
	pass := getenv("KEENETIC_PASSWORD", "")
	if pass == "" {
		fmt.Println("❌ KEENETIC_PASSWORD is empty.")
		os.Exit(1)
	}
	skipSSL := os.Getenv("KEENETIC_INSECURE_SKIP_VERIFY") != "false"

	client, err := keenclient.NewClient(host, user, pass, !skipSSL)
	if err != nil {
		fmt.Println("❌ NewClient failed:", err)
		os.Exit(1)
	}

	fmt.Println("=== BƯỚC 0: KHẢO SÁT KEENETIC RCI API ===")
	fmt.Printf("Router: %s\n", host)

	// ---- 1. Test authentication flow ----
	fmt.Println("\n--- 1. Probe MD5 Challenge-Response auth (full detail) ---")
	if err := client.ProbeAuth(); err != nil {
		fmt.Printf("❌ AUTH FAILED: %v\n", err)
		fmt.Println("   Xem gợi ý sửa header trong keenclient/client.go (Auth flow).")
		os.Exit(1)
	}

	// Actually authenticate so the RCI endpoints below run with a valid session.
	if _, err := client.Authenticate(); err != nil {
		fmt.Printf("⚠️ Authenticate after probe returned: %v\n", err)
	}

	// ---- 2. Dump raw RCI responses (exact JSON shape) ----
	endpoints := []struct {
		name string
		path string
	}{
		{"mws/member", "/rci/show/mws/member"},
		{"system", "/rci/show/system"},
	}

	for _, ep := range endpoints {
		fmt.Printf("\n--- 2.%d RAW %s ---\n", 1, ep.name)
		raw, err := client.GetRawRCI(ep.path)
		if err != nil {
			fmt.Printf("❌ GET %s failed: %v\n", ep.path, err)
			continue
		}
		fmt.Println(string(raw))
	}

	// ---- 2b. Try batch /rci/ POST (multi-query) after auth ----
	fmt.Println("\n--- 2b. BATCH /rci/ probe ---")
	batchBody := map[string]interface{}{
		"requests": []map[string]string{
			{"path": "/rci/show/mws/member"},
			{"path": "/rci/show/system"},
		},
	}
	batchJSON, _ := json.Marshal(batchBody)
	batchReq, err := http.NewRequest("POST", client.BaseURL+"/rci/", bytes.NewReader(batchJSON))
	if err != nil {
		fmt.Printf("❌ Build POST /rci/ failed: %v\n", err)
	} else {
		batchReq.Header.Set("Content-Type", "application/json")
		batchReq.Header.Set("User-Agent", "Mozilla/5.0")
		batchReq.Header.Set("Accept", "application/json")
		resp, err := client.HTTPClient.Do(batchReq)
		if err != nil {
			fmt.Printf("❌ POST /rci/ failed: %v\n", err)
		} else {
			defer resp.Body.Close()
			bodyBytes, _ := io.ReadAll(resp.Body)
			fmt.Printf("POST /rci/ status = %d\n", resp.StatusCode)
			fmt.Printf("POST /rci/ body = %s\n", truncateForDebug(string(bodyBytes)))
		}
	}

	// ---- 3. Try parsing into the structs ----
	fmt.Println("\n--- 3. Parse into structs ---")
	mesh, err := client.GetWiFIMesh()
	if err != nil {
		fmt.Printf("❌ GetWiFIMesh failed: %v\n", err)
	} else {
		if len(mesh.Members) == 0 {
			fmt.Println("⚠️ 0 members parsed (kiểm tra cấu trúc JSON, có thể cần custom parser)")
		}
		fmt.Printf("  controller: online=%v name=%q ip=%q mac=%q status=%q source=%q\n",
			mesh.Controller.IsOnline, mesh.Controller.Name, mesh.Controller.IP, mesh.Controller.MAC, mesh.Controller.Status, mesh.Controller.Source)
		for _, mm := range mesh.Members {
			fmt.Printf("  MEMBER %s | %-16s | name=%-20s | ip=%-15s | mode=%-10s | backhaul=%-24s | assoc=%d | uptime=%s\n",
				keenclient.NormalizeMAC(mm.MAC), mm.Model, mm.KnownHost, mm.IP, mm.Mode, mm.Backhaul.Uplink, mm.Associations, mm.System.Uptime)
		}
	}

	fmt.Println("\n--- 3b. Controller snapshot from /rci/show/system ---")
	controller, err := client.GetControllerSnapshot()
	if err != nil {
		fmt.Printf("❌ GetControllerSnapshot failed: %v\n", err)
	} else {
		fmt.Printf("  controller: online=%v name=%q ip=%q mac=%q status=%q source=%q\n",
			controller.IsOnline, controller.Name, controller.IP, controller.MAC, controller.Status, controller.Source)
	}

	fmt.Println("\n--- 3c. Web-UI-style nodes (GetWiFIMesh.Nodes) ---")
	fmt.Printf("  controller node: name=%q model=%q fw=%q cid=%q mac=%q\n", mesh.Nodes[0].Name, mesh.Nodes[0].Model, mesh.Nodes[0].Firmware, mesh.Nodes[0].CID, mesh.Nodes[0].MAC)
	for _, n := range mesh.Nodes[1:] {
		fmt.Printf("  NODE %-22s | online=%-5v | ip=%-15s | via=%-18s | conn=%-14s | uptime=%d | clients=%d | fw=%s\n",
			n.Name, n.IsOnline, n.IP, n.Via, n.Connection, n.Uptime, n.ClientCount, n.Firmware)
	}
	fmt.Printf("  TOTAL clients = %d\n", mesh.TotalClients())
	if len(mesh.Candidates) > 0 {
		fmt.Println("  Candidates (chờ ghép mesh):")
		for _, c := range mesh.Candidates {
			fmt.Printf("    - %s (%s) state=%s\n", c.Model, c.MAC, c.State)
		}
	}

	// ---- 4. Pretty-print a parsed member/client example ----
	fmt.Println("\n--- 4. Struct mapping (member/client) ---")
	b, _ := json.MarshalIndent(mesh, "", "  ")
	fmt.Println(string(b))

	client.Logout()
	fmt.Println("\n=== HOÀN TẤT KHẢO SÁT ===")
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func truncateForDebug(s string) string {
	if len(s) <= 1000 {
		return s
	}
	return s[:1000] + "..."
}
