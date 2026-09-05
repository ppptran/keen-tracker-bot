// Package keenclient implements a Go client for the Keenetic Router API.
//
// It follows the MD5 Challenge-Response authentication flow described in the
// Keenetic Web API and mirrors the structure of zte-tracker-bot's zteclient so
// the upper Tracker layer can be reused. Both the auth flow and the /rci/show/*
// endpoint shapes MUST be validated against a real router (Step 0) via the
// debug_keen tool before relying on the parsed structs.
package keenclient

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultScheme is used when KEENETIC_IP has no explicit scheme.
	// Keenetic serves its web UI over HTTP by default, so we prefer http
	// and fall back to https during auto-detection.
	DefaultScheme = "http"
	// DefaultUser is the fallback login (typically "admin").
	DefaultUser = "admin"
	// authPath is the Keenetic authentication endpoint.
	authPath = "/auth"
	// Header constants for the challenge/response flow.
	headerChallenge     = "X-NDM-Challenge"
	headerRealm         = "X-NDM-Realm"
	headerResponse      = "X-NDM-Response"
	headerContentType   = "application/json; charset=utf-8"
	// probeTimeout is how long we wait on the scheme-probe request.
	probeTimeout = 4 * time.Second
)

// responseRegex matches the key=value tokens embedded in a Www-Authenticate
// header, e.g.
//
//	x-ndw2-interactive realm="Keenetic KN-3811" challenge="ANKXVI..." session_id="..." session_cookie="..."
var responseRegex = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)

// ErrorResponse is a minimal binder for Keenetic error payloads.
type ErrorResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// Client is the Go client for the Keenetic Router API.
//
// Session handling relies entirely on the HTTP client's cookie jar: the
// router issues the session cookie on GET /auth and ROTATES it on a successful
// POST /auth (the pre-auth value then gets 401 on every RCI call). The jar
// picks up each Set-Cookie automatically, so no manual cookie plumbing is
// needed — doGet's 401-retry re-authenticates when the session expires.
type Client struct {
	Host          string
	Username      string
	Password      string
	BaseURL       string
	VerifySSL     bool
	HTTPClient    *http.Client
	ControllerMAC string

	mu            sync.Mutex
	authenticated bool
}

// NewClient creates a new Keenetic Router API client.
func NewClient(host, username, password string, verifySSL bool) (*Client, error) {
	parsedURL, _, err := resolveScheme(host, verifySSL)
	if err != nil {
		return nil, err
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !verifySSL,
		},
	}

	httpClient := &http.Client{
		Jar:       jar,
		Transport: tr,
		Timeout:   15 * time.Second,
	}

	if username == "" {
		username = DefaultUser
	}

	return &Client{
		Host:       parsedURL.Host,
		Username:   username,
		Password:   password,
		BaseURL:    fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host),
		VerifySSL:  verifySSL,
		HTTPClient: httpClient,
	}, nil
}

// resolveScheme determines the base URL for the router.
//   - If host already carries a scheme (http:// or https://), it is used as-is.
//   - Otherwise it probes http first, then https, and picks the one that responds.
//   - If neither responds, it falls back to the DefaultScheme with a warning.
func resolveScheme(host string, verifySSL bool) (*url.URL, string, error) {
	hasScheme := strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://")

	if hasScheme {
		parsed, err := url.Parse(host)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse host URL: %w", err)
		}
		return parsed, parsed.Scheme, nil
	}

	// Probe both schemes. Use a short-lived transport so a hung TLS handshake
	// or a plain-HTTP rejection does not block for the full 15s.
	probeClient := &http.Client{
		Timeout: probeTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !verifySSL},
		},
	}

	candidates := []string{"http", "https"}
	for _, scheme := range candidates {
		candidate := fmt.Sprintf("%s://%s", scheme, host)
		resp, err := probeClient.Get(candidate + authPath)
		if err != nil {
			continue
		}
		resp.Body.Close()
		// Any reachable candidate is good; prefer http as Keenetic defaults to it.
		parsed, perr := url.Parse(candidate)
		if perr != nil {
			continue
		}
		return parsed, scheme, nil
	}

	// Neither scheme responded. Fall back to the default and let the caller
	// surface the original connect error later.
	parsed, err := url.Parse(fmt.Sprintf("%s://%s", DefaultScheme, host))
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse host URL: %w", err)
	}
	return parsed, parsed.Scheme, nil
}

// ---------------------------------------------------------------------------
// Authentication flow (x-ndw2-interactive challenge-response)
//
// This matches the real protocol used by modern Keenetic firmwares (KN-3811,
// Hero, etc.). yeucau.md's double-MD5 description is WRONG for this firmware
// family; the authoritative flow (validated against many HA integrations and
// the gokeenapi Go client) is:
//
//	1. GET  /auth            -> 401 with:
//	        Www-Authenticate: x-ndw2-interactive realm="..." challenge="..." session_id="..." session_cookie="..."
//	        X-Ndm-Challenge, X-Ndm-Realm, Set-Cookie headers
//	2. Compute the response hash:
//	        ha1      = MD5( password )                    [or MD5(user:realm:pass)]
//	        response = SHA256( challenge + ha1 )
//	3. POST /auth (JSON { "login": user, "password": response })
//	        and send the session_cookie back (as Cookie header / set by jar).
//	4. On 200/204 the session cookie is stored and used for all later /rci/* calls.
//
// The GET /auth 401 is NORMAL — it is how the router hands out the challenge.
// ---------------------------------------------------------------------------

// md5Hex returns the MD5 digest of s as a lowercase hex string.
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// sha256Hex returns the SHA-256 digest of s as a lowercase hex string.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ndw2Response computes the x-ndw2-interactive response hash.
//
// variant "user" uses MD5(username:realm:password) as ha1 (cataseven /
// HA-Keenetic-device_tracker style). variant "pass" uses MD5(password) alone
// (malinovsku / akinin / keenetic-grafana-monitoring style). Both then compute
// SHA256(challenge + ha1). We try both so a single firmware never blocks us.
func ndw2Response(variant, username, realm, password, challenge string) string {
	var ha1 string
	switch variant {
	case "user":
		ha1 = md5Hex(username + ":" + realm + ":" + password)
	default:
		ha1 = md5Hex(password)
	}
	return sha256Hex(challenge + ha1)
}

// parseWwwAuthenticate extracts key="value" tokens from a Www-Authenticate
// header value (e.g. the realm, challenge, session_id and session_cookie).
func parseWwwAuthenticate(value string) map[string]string {
	out := make(map[string]string)
	for _, m := range responseRegex.FindAllStringSubmatch(value, -1) {
		if len(m) == 3 {
			out[strings.ToLower(m[1])] = m[2]
		}
	}
	return out
}

// getChallenge performs GET /auth and returns the challenge-realm pair, the
// HTTP status and the raw body.
//
// Returns status==200 when the router already recognises the session (i.e. the
// cookie was echoed back and the request is already authenticated) — in that
// case no challenge header is present and the caller may treat the client as
// authenticated. The session cookie itself is managed by the cookie jar.
func (c *Client) getChallenge() (challenge, realm, rawBody string, status int, err error) {
	reqURL := c.BaseURL + authPath
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", "", "", 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", "", "", 0, err
	}
	defer resp.Body.Close()

	// Preferred: parse the Www-Authenticate header (the modern source of truth).
	wwwAuth := resp.Header.Get("Www-Authenticate")
	tokens := parseWwwAuthenticate(wwwAuth)
	challenge = firstNonEmpty(tokens["challenge"], resp.Header.Get(headerChallenge))
	realm = firstNonEmpty(tokens["realm"], resp.Header.Get(headerRealm))

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", 0, err
	}
	return challenge, realm, string(body), resp.StatusCode, nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Authenticate logs in using the x-ndw2-interactive challenge-response flow.
func (c *Client) Authenticate() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Step 1: GET /auth for challenge + realm (the jar stores the issued
	// session cookie).
	challenge, realm, rawBody, status, err := c.getChallenge()
	if err != nil {
		return false, fmt.Errorf("step 1 (get challenge) failed: %w", err)
	}

	// A 200 on GET /auth means the router already recognises our session cookie
	// (e.g. from a previous Authenticate that set it). Bail out early.
	if status >= 200 && status < 300 {
		c.authenticated = true
		return true, nil
	}

	if challenge == "" {
		return false, fmt.Errorf("step 1 (get challenge) failed: empty X-NDM-Challenge (HTTP %d body: %s)", status, truncate(rawBody, 200))
	}

	// Step 2: try both known hash variants. The modern cataseven flow uses
	// MD5(user:realm:pass); the older integrations use MD5(pass) alone. Both
	// then SHA256(challenge + ha1). We attempt them in order. The POST response
	// rotates the session cookie; the jar picks up the new value automatically.
	variants := []string{"user", "pass"}
	var lastErr error
	for _, variant := range variants {
		responseHash := ndw2Response(variant, c.Username, realm, c.Password, challenge)
		ok, err := c.postAuth(responseHash)
		if err == nil && ok {
			c.authenticated = true
			return true, nil
		}
		lastErr = err
	}

	return false, fmt.Errorf("authentication failed for all hash variants: %w", lastErr)
}

// postAuth performs the POST /auth step with the given response hash. It
// returns true when the router accepts the login (HTTP 200/204).
func (c *Client) postAuth(responseHash string) (bool, error) {
	// Step 3: POST /auth with the response hash as JSON body, and (on many
	// firmwares) also as the X-Ndm-Response header. The session cookie from
	// step 1 is sent by the cookie jar.
	payload := map[string]string{
		"login":    c.Username,
		"password": responseHash,
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := c.BaseURL + authPath
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", headerContentType)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set(headerResponse, responseHash)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("step 3 (login POST) failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, fmt.Errorf("authentication failed: HTTP %d (body: %s)", resp.StatusCode, truncate(string(respBody), 300))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("authentication failed: unexpected HTTP %d (body: %s)", resp.StatusCode, truncate(string(respBody), 300))
	}

	// Detect explicit error flags some firmwares embed in a JSON body.
	var er struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(respBody, &er) == nil && er.Error != "" {
		return false, fmt.Errorf("authentication failed: router error: %s", er.Error)
	}

	return true, nil
}

// ProbeAuth performs the full challenge/response exchange and prints every
// intermediate value (status, headers, body, computed hashes). It is meant for
// Step-0 debugging against a real router so the exact protocol is confirmed.
// It does NOT mutate the client's authenticated state.
func (c *Client) ProbeAuth() error {
	// Step 1: GET /auth
	reqURL := c.BaseURL + authPath
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("build GET /auth: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET /auth: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	c.probePrintf("GET %s -> HTTP %d", reqURL, resp.StatusCode)
	c.probePrintf("  Headers:")
	for name, vals := range resp.Header {
		for _, v := range vals {
			c.probePrintf("    %s: %s", name, v)
		}
	}
	c.probePrintf("  Body: %q", truncate(string(body), 500))

	wwwAuth := resp.Header.Get("Www-Authenticate")
	tokens := parseWwwAuthenticate(wwwAuth)
	challenge := firstNonEmpty(tokens["challenge"], resp.Header.Get(headerChallenge))
	realm := firstNonEmpty(tokens["realm"], resp.Header.Get(headerRealm))
	sessionCookie := tokens["session_cookie"]
	if ck := resp.Header.Get("Set-Cookie"); ck != "" {
		cookieKV := strings.Split(ck, ";")[0]
		if strings.Contains(cookieKV, "=") {
			sessionCookie = cookieKV
		}
	}

	c.probePrintf("  Realm            = %q", realm)
	c.probePrintf("  Challenge        = %q", challenge)
	c.probePrintf("  SessionCookie    = %q", sessionCookie)
	c.probePrintf("  Www-Authenticate = %q", wwwAuth)
	c.probePrintf("  Www tokens       = %v", tokens)

	if challenge == "" {
		return fmt.Errorf("no challenge in GET /auth response")
	}

	// Step 2: compute both variant hashes so we can see which the router wants.
	for _, variant := range []string{"user", "pass"} {
		responseHash := ndw2Response(variant, c.Username, realm, c.Password, challenge)
		c.probePrintf("  variant=%-4s response = SHA256(challenge + %s) = %s", variant, ha1Label(variant), responseHash)
	}

	// Step 3: POST /auth with the "user" variant (documented modern flow).
	responseHash := ndw2Response("user", c.Username, realm, c.Password, challenge)
	payload := map[string]string{"login": c.Username, "password": responseHash}
	payloadBytes, _ := json.Marshal(payload)
	req2, err := http.NewRequest("POST", reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return fmt.Errorf("build POST /auth: %w", err)
	}
	req2.Header.Set("Content-Type", headerContentType)
	req2.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req2.Header.Set(headerResponse, responseHash)

	resp2, err := c.HTTPClient.Do(req2)
	if err != nil {
		return fmt.Errorf("POST /auth: %w", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	c.probePrintf("POST %s -> HTTP %d", reqURL, resp2.StatusCode)
	c.probePrintf("  Request headers (X-Ndm-Response=%s):", responseHash)
	c.probePrintf("  Headers:")
	for name, vals := range resp2.Header {
		for _, v := range vals {
			c.probePrintf("    %s: %s", name, v)
		}
	}
	c.probePrintf("  Body: %q", truncate(string(body2), 500))
	c.probePrintf("  POST status: %d => success=%s", resp2.StatusCode, stringifyBool(resp2.StatusCode >= 200 && resp2.StatusCode < 300))

	// Print which cookies the server tried to set.
	for _, ck := range resp2.Cookies() {
		c.probePrintf("  Set-Cookie: %s=%s", ck.Name, ck.Value)
	}

	return nil
}

// ha1Label returns a human-readable description of the ha1 variant for debug.
func ha1Label(variant string) string {
	if variant == "user" {
		return "MD5(user:realm:pass)"
	}
	return "MD5(pass)"
}

// stringifyBool renders a bool as yes/no for readable probe output.
func stringifyBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// probePrintf prints a line to stdout. Kept as a method hook so tests can swap.
func (c *Client) probePrintf(format string, a ...interface{}) {
	fmt.Printf(format+"\n", a...)
}

// IsAuthenticated reports whether the last Authenticate call succeeded.
func (c *Client) IsAuthenticated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authenticated
}

// Logout clears the session cookie server-side.
func (c *Client) Logout() {
	c.mu.Lock()
	defer c.mu.Unlock()

	reqURL := c.BaseURL + authPath
	req, err := http.NewRequest("DELETE", reqURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	if resp, err := c.HTTPClient.Do(req); err == nil {
		resp.Body.Close()
	}
	c.authenticated = false
}

// ---------------------------------------------------------------------------
// RCI show endpoints
// ---------------------------------------------------------------------------

// doGet performs an authenticated GET and returns the raw body. If the router
// returns 401, it re-authenticates once and retries the request (resilient
// session handling as described in yeucau.md Step 4).
func (c *Client) doGet(path string) ([]byte, error) {
	body, status, err := c.doGetRaw(path)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		// Session expired -> re-auth and retry once.
		if ok, authErr := c.Authenticate(); authErr == nil && ok {
			body, status, err = c.doGetRaw(path)
			if err != nil {
				return nil, err
			}
			if status == http.StatusUnauthorized {
				return nil, fmt.Errorf("re-authenticated but %s still returns 401", path)
			}
		} else {
			return nil, fmt.Errorf("%s returned 401 and re-auth failed: %w", path, authErr)
		}
	}
	return body, nil
}

// doGetRaw performs a single authenticated GET without re-auth handling.
func (c *Client) doGetRaw(path string) ([]byte, int, error) {
	// Ensure we have a session cookie. If not, authenticate first.
	c.mu.Lock()
	needAuth := !c.authenticated
	c.mu.Unlock()
	if needAuth {
		if ok, err := c.Authenticate(); err != nil || !ok {
			return nil, 0, fmt.Errorf("not authenticated: %w", err)
		}
	}

	reqURL := c.BaseURL + path
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// SetControllerMAC stores the controller MAC when it is known from config.
func (c *Client) SetControllerMAC(mac string) {
	c.ControllerMAC = strings.TrimSpace(mac)
}

// GetControllerSnapshot builds a synthetic controller record. Preferred source
// is the Web-UI-style controller node (proper name/model/mesh MAC from version
// + identification + Bridge0); when those endpoints fail it degrades to the
// /rci/show/system reachability probe. The Keenetic firmware does not return a
// controller row in /rci/show/mws/member, so the controller always comes from
// these endpoints, never from the member list.
func (c *Client) GetControllerSnapshot() (ControllerSnapshot, error) {
	if node, err := c.GetControllerNode(); err == nil {
		return ControllerSnapshot{
			Name:     node.Name,
			IP:       c.Host,
			MAC:      node.MAC,
			Status:   "online",
			IsOnline: true,
			Source:   "version+identification+bridge0+system",
			LastSeen: time.Now(),
		}, nil
	}

	snap := ControllerSnapshot{
		Name:     "Controller",
		IP:       c.Host,
		MAC:      c.ControllerMAC,
		Status:   "offline",
		Source:   "system",
		IsOnline: false,
		LastSeen: time.Now(),
	}

	if c.Host == "" {
		snap.IP = "unknown"
	}

	if ok, err := c.Authenticate(); err != nil || !ok {
		snap.Status = "offline"
		snap.IsOnline = false
		if err != nil {
			return snap, err
		}
		return snap, fmt.Errorf("controller auth failed")
	}

	body, err := c.doGet("/rci/show/system")
	if err != nil {
		snap.Status = "offline"
		snap.IsOnline = false
		return snap, err
	}

	parsed, err := parseSystemController(body, c.Host, c.ControllerMAC)
	if err != nil {
		snap.Status = "offline"
		snap.IsOnline = false
		return snap, err
	}

	if parsed.IP != "" {
		snap.IP = parsed.IP
	}
	if parsed.MAC != "" {
		snap.MAC = parsed.MAC
	}
	if parsed.Name != "" {
		snap.Name = parsed.Name
	}
	if strings.TrimSpace(string(body)) != "" && strings.TrimSpace(string(body)) != "null" {
		snap.IsOnline = true
		snap.Status = parsed.Status
		if snap.Status == "" || snap.Status == "unknown" || snap.Status == "offline" {
			snap.Status = "online"
		}
	} else {
		snap.IsOnline = false
		snap.Status = "offline"
	}
	snap.LastSeen = time.Now()
	return snap, nil
}

// GetMeshMembers queries GET /rci/show/mws/member.
func (c *Client) GetMeshMembers() ([]MeshMember, error) {
	body, err := c.doGet("/rci/show/mws/member")
	if err != nil {
		return nil, err
	}
	return parseMeshMembers(body)
}

// GetMeshStatus is kept for compatibility but is not part of the active bot
// contract. The firmware exposes too little useful data there to justify using it.
func (c *Client) GetMeshStatus() (MeshStatus, error) {
	body, err := c.doGet("/rci/show/mws/status")
	if err != nil {
		return MeshStatus{}, err
	}
	return parseMeshStatus(body)
}

// GetMeshClients is kept for compatibility but is intentionally not used by the
// tracker because the router does not provide reliable mesh client data here.
func (c *Client) GetMeshClients() ([]MeshClient, error) {
	body, err := c.doGet("/rci/show/mws/client")
	if err != nil {
		return nil, err
	}
	return parseMeshClients(body)
}

// GetSystemController reads the controller metadata available from the router itself.
func (c *Client) GetSystemController() (ControllerSnapshot, error) {
	body, err := c.doGet("/rci/show/system")
	if err != nil {
		return ControllerSnapshot{}, err
	}
	return parseSystemController(body, c.Host, c.ControllerMAC)
}

// GetControllerNode builds the web-UI controller node. The controller is NOT
// part of /rci/show/mws/member — the web UI assembles it from three sources:
//
//   - /rci/show/version           -> name (description), model, firmware (title)
//   - /rci/show/identification    -> cid (the mesh identity used by hotspot mws.cid)
//   - /rci/show/interface/Bridge0 -> the mesh MAC (identification.mac belongs to
//     a different port and will NOT match backhaul root/bridge values)
//   - /rci/show/system            -> uptime
//
// A successful call proves the controller is reachable, so IsOnline is true.
func (c *Client) GetControllerNode() (MeshNode, error) {
	node := MeshNode{IsController: true, IsOnline: true, Name: "Controller"}

	body, err := c.doGet("/rci/show/version")
	if err != nil {
		return node, err
	}
	var v struct {
		Description string `json:"description"`
		Model       string `json:"model"`
		Title       string `json:"title"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return node, fmt.Errorf("parse /rci/show/version failed: %w", err)
	}
	if v.Description != "" {
		node.Name = v.Description
	}
	node.Model = v.Model
	node.Firmware = v.Title

	if body, err := c.doGet("/rci/show/identification"); err == nil {
		var id struct {
			CID string `json:"cid"`
			MAC string `json:"mac"`
		}
		if json.Unmarshal(body, &id) == nil {
			node.CID = id.CID
			node.MAC = id.MAC // fallback only; replaced by Bridge0 below
		}
	}
	if body, err := c.doGet("/rci/show/interface/Bridge0"); err == nil {
		var br struct {
			MAC string `json:"mac"`
		}
		if json.Unmarshal(body, &br) == nil && br.MAC != "" {
			node.MAC = br.MAC
		}
	}
	if body, err := c.doGet("/rci/show/system"); err == nil {
		var sys struct {
			Uptime string `json:"uptime"`
		}
		if json.Unmarshal(body, &sys) == nil {
			node.Uptime = parseUptimeSeconds(sys.Uptime)
		}
	}
	return node, nil
}

// GetHotspotHosts queries GET /rci/show/ip/hotspot: every wired and wireless
// client of the whole mesh. Wireless entries carry the nested "mws" object
// that links a client to its mesh node (mws.cid).
func (c *Client) GetHotspotHosts() ([]HotspotHost, error) {
	body, err := c.doGet("/rci/show/ip/hotspot")
	if err != nil {
		return nil, err
	}
	return parseHotspotHosts(body)
}

// GetMeshAssociations queries GET /rci/show/mws/associations (Wi-Fi backhaul
// links between nodes). Empty on Ethernet-backed meshes.
func (c *Client) GetMeshAssociations() ([]MeshAssociation, error) {
	body, err := c.doGet("/rci/show/mws/associations")
	if err != nil {
		return nil, err
	}
	return parseMeshAssociations(body)
}

// GetMeshCandidates queries GET /rci/show/mws/candidate: devices discovered as
// ready to join the mesh but not acquired yet.
func (c *Client) GetMeshCandidates() ([]MeshCandidate, error) {
	body, err := c.doGet("/rci/show/mws/candidate")
	if err != nil {
		return nil, err
	}
	return parseMeshCandidates(body)
}

// GetWiFIMesh performs one full mesh scan and assembles the Web-UI-style node
// list: index 0 is the controller (version + identification + Bridge0 + system,
// exactly like the web UI), the rest are the /rci/show/mws/member extenders
// with backhaul, Via and per-node client counts resolved. Only the member
// fetch is fatal; the remaining sources degrade gracefully so the map still
// renders when a single endpoint misbehaves.
func (c *Client) GetWiFIMesh() (WiFIMesh, error) {
	var m WiFIMesh
	m.FetchedAt = time.Now()

	members, err := c.GetMeshMembers()
	if err != nil {
		return m, fmt.Errorf("mws/member: %w", err)
	}
	m.Members = make([]MeshMember, 0, len(members))
	for _, mm := range members {
		if mm.Deleted {
			continue
		}
		m.Members = append(m.Members, mm)
	}

	ctrlNode, ctrlErr := c.GetControllerNode()
	if ctrlErr != nil {
		// Legacy fallback: the /rci/show/system snapshot still proves
		// reachability when version/identification are unavailable.
		snap, sysErr := c.GetSystemController()
		if sysErr != nil {
			snap = ControllerSnapshot{
				Name: "Controller", IP: c.Host, MAC: c.ControllerMAC,
				Status: "online", IsOnline: true, Source: "system", LastSeen: time.Now(),
			}
		}
		m.Controller = snap
		ctrlNode = MeshNode{
			Name: snap.Name, MAC: snap.MAC,
			IsController: true, IsOnline: snap.IsOnline,
		}
	} else {
		m.Controller = ControllerSnapshot{
			Name:     ctrlNode.Name,
			IP:       c.Host,
			MAC:      ctrlNode.MAC,
			Status:   "online",
			IsOnline: true,
			Source:   "version+identification+bridge0+system",
			LastSeen: time.Now(),
		}
	}
	if ctrlNode.CID == "" {
		ctrlNode.CID = "controller" // grouping fallback key
	}

	// Clients per node (web UI groupClientsByNode algorithm).
	var hosts []HotspotHost
	if hs, err := c.GetHotspotHosts(); err == nil {
		hosts = hs
	}

	extMACs := make([]string, 0, len(m.Members))
	nameByMAC := make(map[string]string, len(m.Members))
	for _, mm := range m.Members {
		if n := NormalizeMAC(mm.MAC); n != "" {
			extMACs = append(extMACs, mm.MAC)
			nameByMAC[n] = firstNonEmpty(mm.KnownHost, mm.MAC)
		}
	}
	groups := GroupClientsByNode(hosts, ctrlNode.CID, extMACs)
	m.ClientGroups = groups

	// Wi-Fi backhaul associations, best effort (empty on Ethernet meshes).
	assocByMAC := make(map[string]*MeshAssociation)
	if assocs, err := c.GetMeshAssociations(); err == nil {
		for i := range assocs {
			if n := NormalizeMAC(assocs[i].MAC); n != "" {
				a := assocs[i]
				assocByMAC[n] = &a
			}
		}
	}

	ctrlNode.ClientCount = len(groups[ctrlNode.CID])
	nodes := make([]MeshNode, 0, len(m.Members)+1)
	nodes = append(nodes, ctrlNode)
	for _, mm := range m.Members {
		nodes = append(nodes, buildExtenderNode(mm, ctrlNode, nameByMAC, groups, assocByMAC))
	}
	m.Nodes = nodes

	// Devices waiting to join the mesh, best effort.
	if cands, err := c.GetMeshCandidates(); err == nil {
		m.Candidates = cands
	}
	return m, nil
}

// buildExtenderNode converts one mws/member row into a web-UI-style node:
// online iff it has a backhaul object (members without one — e.g. previously
// acquired devices that left the mesh — stay visible as Offline, like the web
// UI), and the parent resolved from backhaul.bridge so multi-hop topologies
// render correctly for wired and wireless backhaul alike.
func buildExtenderNode(mm MeshMember, ctrlNode MeshNode, nameByMAC map[string]string, groups map[string][]ClientInfo, assocByMAC map[string]*MeshAssociation) MeshNode {
	hasBH := mm.Backhaul.Root != "" || mm.Backhaul.Uplink != "" || mm.Backhaul.Bridge != ""
	node := MeshNode{
		CID:               mm.CID,
		MAC:               mm.MAC,
		Name:              firstNonEmpty(mm.KnownHost, mm.MAC),
		Model:             mm.Model,
		Firmware:          mm.FW,
		IP:                mm.IP,
		Mode:              mm.Mode,
		IsOnline:          hasBH,
		HasBackhaul:       hasBH,
		Uptime:            parseUptimeSeconds(mm.System.Uptime),
		Backhaul:          mm.Backhaul,
		InternetAvailable: mm.InternetAvailable,
		IsUpdateAvailable: mm.FW != "" && mm.FWAvailable != "" && mm.FW != mm.FWAvailable,
	}
	node.ClientCount = len(groups[mm.CID])
	if hasBH {
		node.Via, node.ViaMAC, node.ViaIsController = ResolveBridgeParent(mm.Backhaul.Bridge, ctrlNode.MAC, ctrlNode.Name, nameByMAC)
		node.Connection = BackhaulConnection(mm.Backhaul, assocByMAC[NormalizeMAC(mm.MAC)])
	}
	return node
}

// GetRawRCI returns the raw body of any RCI path (without parsing).
// Used by debug_keen to dump the exact JSON shape for Step-0 verification.
func (c *Client) GetRawRCI(path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.doGet(path)
}

// truncate clips a string to n bytes for readable error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
