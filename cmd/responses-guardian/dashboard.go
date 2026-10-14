package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui/panel.html
var panelHTML string

type Dashboard struct {
	guardian     *Guardian
	manager      *ConfigManager
	settingsFile string
	token        string
	mu           sync.Mutex
	active       AppSettings
	statusClient *http.Client
}

func NewDashboard(g *Guardian, active AppSettings, settingsFile string, manager *ConfigManager) (*Dashboard, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	return &Dashboard{guardian: g, manager: manager, settingsFile: settingsFile, active: active, token: hex.EncodeToString(key[:]), statusClient: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (d *Dashboard) localRequest(r *http.Request) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !isLoopbackHost(remote) {
		return false
	}
	d.mu.Lock()
	listen := d.active.Listen
	d.mu.Unlock()
	requestHost, requestPort, ok := panelAuthority(r.Host)
	_, listenPort, _ := panelAuthority(listen)
	if !ok || requestPort != listenPort {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		originHost, originPort, ok := panelAuthority(u.Host)
		if !ok || originHost != requestHost || originPort != requestPort {
			return false
		}
	}
	return true
}

// Browsers omit :80 in HTTP Host and Origin, including for [::1].
func panelAuthority(authority string) (host, port string, ok bool) {
	u, err := url.Parse("http://" + authority)
	if err != nil || u.Host != authority || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(authority, ":") {
		return "", "", false
	}
	host, port = strings.ToLower(u.Hostname()), u.Port()
	if !isLoopbackHost(host) {
		return "", "", false
	}
	if port == "" {
		port = "80"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", "", false
	}
	return host, strconv.Itoa(number), true
}

func (d *Dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !d.localRequest(r) {
		http.Error(w, "local control panel only", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/__responses_guardian/panel", "/__responses_guardian/panel/":
		if r.Method != http.MethodGet {
			d.methodNotAllowed(w, "GET")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+d.token+"'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		_, _ = io.WriteString(w, strings.ReplaceAll(panelHTML, "__GUARDIAN_TOKEN__", d.token))
	case "/__responses_guardian/state":
		if r.Method != http.MethodGet {
			d.methodNotAllowed(w, "GET")
			return
		}
		d.writeState(w, r)
	case "/__responses_guardian/mode":
		if r.Method != http.MethodPost {
			d.methodNotAllowed(w, "POST")
			return
		}
		if !d.allowWrite(w, r) {
			return
		}
		var payload struct {
			Mode string `json:"mode"`
		}
		if !d.readJSON(w, r, &payload) {
			return
		}
		mode, err := parseRecoveryMode(payload.Mode)
		if err != nil {
			d.writeError(w, "请选择 off、on-error 或 always 模式", 400)
			return
		}
		if d.guardian.modeFile == "" {
			d.writeError(w, "当前启动方式未配置 mode-file，无法保存模式", 409)
			return
		}
		d.mu.Lock()
		data, _ := json.Marshal(map[string]recoveryMode{"mode": mode})
		err = atomicWriteFile(d.guardian.modeFile, data, 0o600)
		if err == nil {
			d.guardian.setMode(mode)
		}
		d.mu.Unlock()
		if err != nil {
			d.writeError(w, "模式未保存，请检查文件权限", 500)
			return
		}
		d.writeJSON(w, map[string]any{"mode": mode, "pid": os.Getpid(), "message": "新请求已使用此模式；正在输出的请求继续完成。"})
	case "/__responses_guardian/settings":
		if r.Method != http.MethodPut {
			d.methodNotAllowed(w, "PUT")
			return
		}
		if !d.allowWrite(w, r) {
			return
		}
		var settings AppSettings
		if !d.readJSON(w, r, &settings) {
			return
		}
		if err := validateAppSettings(settings); err != nil {
			d.writeError(w, err.Error(), 400)
			return
		}
		d.saveSettings(w, settings)
	default:
		http.NotFound(w, r)
	}
}

func (d *Dashboard) allowWrite(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Guardian-Token")), []byte(d.token)) != 1 {
		d.writeError(w, "请从本地控制面板提交更改", 403)
		return false
	}
	return true
}

func (d *Dashboard) readJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		d.writeError(w, "提交内容过大或不完整", 400)
		return false
	}
	if err := decodeStrictJSON(data, target); err != nil {
		d.writeError(w, "提交的配置格式无效", 400)
		return false
	}
	return true
}

func (d *Dashboard) saveSettings(w http.ResponseWriter, settings AppSettings) {
	d.mu.Lock()
	defer d.mu.Unlock()
	manager := NewConfigManager(settings.CodexConfig, settings.Provider, settings.ccswitchBaseURL(), settings.guardianBaseURL())
	if err := manager.SetRetrySettings(settings.RequestRetries, settings.StreamRetries); err != nil {
		d.writeError(w, err.Error(), 400)
		return
	}
	if err := manager.Preflight(&d.active); err != nil {
		d.writeError(w, "Codex 配置不可用，原设置未改变："+err.Error(), 400)
		return
	}
	if err := saveAppSettings(d.settingsFile, settings); err != nil {
		d.writeError(w, "配置未保存，请检查文件权限", 500)
		return
	}
	restartRequired := settingsNeedRestart(d.active, settings)
	retryChanged := d.active.RequestRetries != settings.RequestRetries || d.active.StreamRetries != settings.StreamRetries
	var applyErr error
	retryConfigWritten := false
	if d.manager != nil && !restartRequired {
		applyErr = d.manager.SetRetrySettings(settings.RequestRetries, settings.StreamRetries)
		if applyErr == nil {
			_, applyErr = d.manager.EnsureGuardian()
		}
		retryConfigWritten = applyErr == nil
	}
	if !restartRequired {
		d.active.RequestRetries, d.active.StreamRetries = settings.RequestRetries, settings.StreamRetries
	}
	message := "配置已保存。"
	if restartRequired {
		message += "地址或提供商设置将在重启 Guardian 后生效。"
	}
	if retryChanged && retryConfigWritten {
		message += "重试次数已写入 Codex 配置，在重启 Codex 后生效。"
	} else if retryChanged && d.manager != nil && restartRequired {
		message += "重试次数将在 Guardian 重启后写入新的配置。"
	} else if retryChanged && d.manager == nil {
		message += "本实例未启用 Codex 配置守护，重试次数只保存到下次启动。"
	}
	if applyErr != nil {
		message += "当前 Codex 配置写入失败，配置守护将继续尝试；请检查配置路径和提供商 ID。"
	}
	d.writeJSON(w, map[string]any{"message": message, "guardian_restart_required": restartRequired, "codex_restart_required": retryChanged && d.manager != nil, "retry_config_written": retryConfigWritten})
}

func (d *Dashboard) writeState(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	active := d.active
	saved, err := readAppSettings(d.settingsFile, active)
	d.mu.Unlock()
	if err != nil {
		saved = active
	}
	upstream := "无法连接"
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(active.Upstream, "/")+"/health", nil)
	if resp, requestErr := d.statusClient.Do(req); requestErr == nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			upstream = "可连接"
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}
	d.writeJSON(w, map[string]any{
		"version": version, "pid": os.Getpid(), "mode": d.guardian.currentMode(),
		"mode_switch_supported": d.guardian.modeFile != "", "active": active, "saved": saved,
		"guardian_restart_required": settingsNeedRestart(active, saved), "config_guard": d.manager != nil,
		"upstream_status": upstream, "settings_error": err != nil,
		"stats": map[string]uint64{"forwarded": d.guardian.forwarded.Load(), "passthrough": d.guardian.passthroughRequests.Load(), "proactive": d.guardian.proactiveSanitization.Load(), "recovered": d.guardian.recoverySuccess.Load(), "failed": d.guardian.recoveryFailed.Load(), "mode_changes": d.guardian.modeChanges.Load()},
	})
}

func (d *Dashboard) writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}
func (d *Dashboard) writeError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func (d *Dashboard) methodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	d.writeError(w, "请求方式不支持", 405)
}
