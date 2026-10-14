package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type AppSettings struct {
	Listen         string `json:"listen"`
	Upstream       string `json:"upstream"`
	Provider       string `json:"provider"`
	CodexConfig    string `json:"codex_config"`
	RequestRetries int    `json:"request_max_retries"`
	StreamRetries  int    `json:"stream_max_retries"`
}

func defaultAppSettings() AppSettings {
	return AppSettings{
		Listen: "127.0.0.1:15722", Upstream: "http://127.0.0.1:15721",
		Provider: "custom", CodexConfig: defaultCodexConfigPath(),
		RequestRetries: 20, StreamRetries: 20,
	}
}

func (s AppSettings) guardianBaseURL() string { return "http://" + s.Listen + "/v1" }
func (s AppSettings) ccswitchBaseURL() string { return strings.TrimRight(s.Upstream, "/") + "/v1" }

var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validateAppSettings(s AppSettings) error {
	host, port, err := net.SplitHostPort(s.Listen)
	if err != nil || !isLoopbackHost(host) || port == "0" {
		return errors.New("本地监听地址必须是回环地址，端口为 1–65535")
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return errors.New("本地代理端口必须在 1–65535 之间")
	}
	u, err := url.Parse(s.Upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !isLoopbackHost(u.Hostname()) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("CCSwitch 地址必须为本地 http(s) 地址，不含凭据、查询参数或片段")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("CCSwitch 地址仅填写本地代理地址与端口，不包含 /v1 或其他路径")
	}
	if u.Port() != "" {
		if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
			return errors.New("CCSwitch 端口必须在 1–65535 之间")
		}
	}
	listenURL, _ := url.Parse("http://" + s.Listen)
	if isSameLocalEndpoint(listenURL, u) {
		return errors.New("Guardian 与 CCSwitch 端口不能相同，否则会形成转发循环")
	}
	if !providerNamePattern.MatchString(s.Provider) {
		return errors.New("Codex 提供商 ID 仅支持 1–64 个字母、数字、下划线或短横线")
	}
	if !filepath.IsAbs(s.CodexConfig) || filepath.Ext(s.CodexConfig) != ".toml" {
		return errors.New("Codex 配置路径必须为绝对 .toml 路径")
	}
	if s.RequestRetries < 0 || s.RequestRetries > 100 || s.StreamRetries < 0 || s.StreamRetries > 100 {
		return errors.New("请求与流重试次数必须在 0–100 之间")
	}
	return nil
}

func isSameLocalEndpoint(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return isLoopbackHost(a.Hostname()) && isLoopbackHost(b.Hostname()) && port(a) == port(b)
}

func readAppSettings(path string, defaults AppSettings) (AppSettings, error) {
	if path == "" {
		return defaults, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return AppSettings{}, err
	}
	settings := defaults
	if err := decodeStrictJSON(data, &settings); err != nil {
		return AppSettings{}, fmt.Errorf("read settings: %w", err)
	}
	if err := validateAppSettings(settings); err != nil {
		return AppSettings{}, err
	}
	return settings, nil
}

func saveAppSettings(path string, settings AppSettings) error {
	if err := validateAppSettings(settings); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o600)
}

func decodeStrictJSON(data []byte, into any) error {
	if len(data) > 16<<10 {
		return errors.New("JSON too large")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return errors.New("expected a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return errors.New("invalid JSON or unsupported field")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func settingsNeedRestart(a, b AppSettings) bool {
	return a.Listen != b.Listen || a.Upstream != b.Upstream || a.Provider != b.Provider || a.CodexConfig != b.CodexConfig
}

type InstanceInfo struct {
	PID      int         `json:"pid"`
	Settings AppSettings `json:"settings"`
}

func readInstance(path string) (*InstanceInfo, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var instance InstanceInfo
	if err := decodeStrictJSON(data, &instance); err != nil {
		return nil, errors.New("invalid instance settings")
	}
	if err := validateAppSettings(instance.Settings); err != nil {
		return nil, fmt.Errorf("invalid instance settings: %w", err)
	}
	return &instance, nil
}

func saveInstance(path string, settings AppSettings) error {
	if path == "" {
		return nil
	}
	data, err := json.Marshal(InstanceInfo{PID: os.Getpid(), Settings: settings})
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o600)
}
