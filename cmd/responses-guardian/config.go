package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ConfigManager struct {
	path           string
	provider       string
	ccswitch       string
	guardian       string
	mu             sync.Mutex
	lastError      string
	requestRetries int
	streamRetries  int
	exactRetries   bool
}

const (
	minimumRequestRetries = 20
	minimumStreamRetries  = 20
)

func NewConfigManager(path, provider, ccswitch, guardian string) *ConfigManager {
	return &ConfigManager{
		path:           path,
		provider:       provider,
		ccswitch:       strings.TrimRight(ccswitch, "/"),
		guardian:       strings.TrimRight(guardian, "/"),
		requestRetries: minimumRequestRetries,
		streamRetries:  minimumStreamRetries,
	}
}

func (m *ConfigManager) SetRetrySettings(request, stream int) error {
	if request < 0 || request > 100 || stream < 0 || stream > 100 {
		return errors.New("retry values must be between 0 and 100")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestRetries, m.streamRetries, m.exactRetries = request, stream, true
	return nil
}

func (m *ConfigManager) EnsureGuardian() (bool, error) {
	change, err := m.rewriteConfig(m.ccswitch, m.guardian, true, true, nil)
	return change != nil && change.changed, err
}

func (m *ConfigManager) RestoreCCSwitch() (bool, error) {
	change, err := m.rewriteConfig(m.guardian, m.ccswitch, false, false, nil)
	return change != nil && change.changed, err
}

// Startup is the only write allowed to migrate a previous instance's route.
// The watcher continues to accept only this instance's configured endpoints.
func (m *ConfigManager) BeginStartup(previous *AppSettings) (*configChange, error) {
	return m.rewriteConfig(m.ccswitch, m.guardian, true, true, previous)
}

type configChange struct {
	path     string
	provider string
	before   []byte
	after    []byte
	mode     os.FileMode
	changed  bool
}

func (c *configChange) Rollback() error {
	if c == nil || !c.changed {
		return nil
	}
	err := atomicWriteFileChecked(c.path, c.before, c.mode, c.after, true)
	if !errors.Is(err, errFileChanged) {
		return err
	}
	// A concurrent edit may still contain our now-dead route. Undo that route
	// alone, preserving the editor's credentials, retry policy and other fields.
	beforeURL, err := providerBaseURL(c.before, c.provider)
	if err != nil {
		return err
	}
	afterURL, err := providerBaseURL(c.after, c.provider)
	if err != nil {
		return err
	}
	if beforeURL == afterURL {
		return errFileChanged
	}
	current, info, err := readConfigFile(c.path)
	if err != nil {
		return err
	}
	updated, changed, err := rewriteProviderBaseURL(current, c.provider, afterURL, beforeURL)
	if err != nil {
		return errFileChanged
	}
	if changed {
		if err := atomicWriteFileChecked(c.path, updated, info.Mode().Perm(), current, true); err != nil {
			return err
		}
	}
	log.Printf("startup rollback restored routing while preserving concurrent config edits")
	return nil
}

func providerBaseURL(data []byte, provider string) (string, error) {
	inTarget := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			section, ok := parseModelProviderSection(strings.TrimSpace(line))
			inTarget = ok && section == provider
			continue
		}
		if inTarget {
			if _, value, _, ok := parseQuotedAssignment(line, "base_url"); ok {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("base_url not found in model provider section %q", provider)
}

func (m *ConfigManager) previousRoutes(previous *AppSettings) []string {
	if previous == nil || validateAppSettings(*previous) != nil || previous.Provider != m.provider {
		return nil
	}
	currentPath, oldPath := filepath.Clean(m.path), filepath.Clean(previous.CodexConfig)
	samePath := currentPath == oldPath
	if runtime.GOOS == "windows" {
		samePath = strings.EqualFold(currentPath, oldPath)
	}
	if !samePath {
		return nil
	}
	return []string{previous.ccswitchBaseURL(), previous.guardianBaseURL()}
}

func readConfigFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("Codex config must be a regular file")
	}
	data, err := os.ReadFile(path)
	return data, info, err
}

func (m *ConfigManager) prepareConfig(data []byte, from, to string, ensureRetries bool, previous *AppSettings) ([]byte, bool, error) {
	updated, changed, err := rewriteProviderBaseURL(data, m.provider, from, to, m.previousRoutes(previous)...)
	if err != nil {
		return nil, false, err
	}
	if ensureRetries {
		var retryChanged bool
		updated, retryChanged, err = setProviderRetrySettings(updated, m.provider, m.requestRetries, m.streamRetries, m.exactRetries)
		changed = changed || retryChanged
	}
	return updated, changed, err
}

// Validate both the chosen provider and the permissions needed for later
// atomic replacements without changing config.toml or creating its backup.
func (m *ConfigManager) Preflight(previous *AppSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, info, err := readConfigFile(m.path)
	if err != nil {
		return err
	}
	if _, _, err := m.prepareConfig(data, m.ccswitch, m.guardian, true, previous); err != nil {
		return err
	}
	if info.Mode().Perm()&0o222 == 0 {
		return errors.New("Codex config is read-only")
	}
	file, err := os.OpenFile(m.path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(m.path), ".guardian-preflight-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	return temp.Close()
}

func (m *ConfigManager) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := m.EnsureGuardian()
			if err != nil {
				message := err.Error()
				if message != m.lastError {
					log.Printf("Codex config watcher: %v", err)
					m.lastError = message
				}
				continue
			}
			m.lastError = ""
			if changed {
				log.Printf("restored Guardian routing and configured Codex retry settings")
			}
		}
	}
}

func (m *ConfigManager) rewriteConfig(from, to string, createBackup, ensureRetries bool, previous *AppSettings) (*configChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, info, err := readConfigFile(m.path)
	if err != nil {
		return nil, err
	}
	updated, changed, err := m.prepareConfig(data, from, to, ensureRetries, previous)
	if err != nil {
		return nil, err
	}
	change := &configChange{path: m.path, provider: m.provider, before: data, after: updated, mode: info.Mode().Perm(), changed: changed}
	if !changed {
		return change, nil
	}

	if createBackup {
		if err := createBackupOnce(m.path, data); err != nil {
			return nil, err
		}
	}
	if err := atomicWriteFileChecked(m.path, updated, info.Mode().Perm(), data, true); err != nil {
		return nil, err
	}
	return change, nil
}

func rewriteProviderBaseURL(data []byte, provider, from, to string, previousRoutes ...string) ([]byte, bool, error) {
	lines := strings.Split(string(data), "\n")
	inTarget := false
	foundSection := false
	foundBaseURL := false
	changed := false

	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section, ok := parseModelProviderSection(trimmed)
			inTarget = ok && section == provider
			if inTarget {
				foundSection = true
			}
			continue
		}
		if !inTarget {
			continue
		}

		prefix, current, suffix, ok := parseQuotedAssignment(line, "base_url")
		if !ok {
			continue
		}
		foundBaseURL = true
		current = strings.TrimRight(current, "/")
		if current == strings.TrimRight(to, "/") {
			return data, false, nil
		}
		accepted := current == strings.TrimRight(from, "/")
		for _, route := range previousRoutes {
			accepted = accepted || current == strings.TrimRight(route, "/")
		}
		if !accepted {
			return nil, false, fmt.Errorf(
				"provider %q base_url is %q, expected %q or %q; refusing to overwrite",
				provider,
				current,
				from,
				to,
			)
		}
		lineEnding := ""
		if strings.HasSuffix(rawLine, "\r") {
			lineEnding = "\r"
		}
		lines[i] = prefix + to + suffix + lineEnding
		changed = true
		break
	}

	if !foundSection {
		return nil, false, fmt.Errorf("model provider section %q not found", provider)
	}
	if !foundBaseURL {
		return nil, false, fmt.Errorf("base_url not found in model provider section %q", provider)
	}
	if !changed {
		return data, false, nil
	}
	return []byte(strings.Join(lines, "\n")), true, nil
}

// ensureProviderRetrySettings keeps the retry policy in the live Codex file
// even when CCSwitch rebuilds the provider section from its saved template.
// Existing values above the minimum are respected; small/default values are
// raised so a provider switch cannot silently restore Codex's low defaults.
func ensureProviderRetrySettings(data []byte, provider string) ([]byte, bool, error) {
	return setProviderRetrySettings(data, provider, minimumRequestRetries, minimumStreamRetries, false)
}

func setProviderRetrySettings(data []byte, provider string, requestRetries, streamRetries int, exact bool) ([]byte, bool, error) {
	lines := strings.Split(string(data), "\n")
	inTarget := false
	sectionFound := false
	sectionEnd := len(lines)
	baseURLIndex := -1
	requestIndex := -1
	streamIndex := -1
	requestValue := -1
	streamValue := -1
	crlf := strings.Contains(string(data), "\r\n")
	lineEnding := ""
	if crlf {
		lineEnding = "\r"
	}

	for i, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if inTarget {
				sectionEnd = i
				inTarget = false
			}
			section, ok := parseModelProviderSection(trimmed)
			if ok && section == provider {
				inTarget = true
				sectionFound = true
			}
			continue
		}
		if !inTarget {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "base_url") {
			baseURLIndex = i
		}
		for _, field := range []struct {
			name  string
			index *int
			value *int
			min   int
		}{
			{name: "request_max_retries", index: &requestIndex, value: &requestValue, min: requestRetries},
			{name: "stream_max_retries", index: &streamIndex, value: &streamValue, min: streamRetries},
		} {
			if strings.HasPrefix(strings.TrimSpace(line), field.name) {
				prefix, value, suffix, ok := parseIntegerAssignment(line, field.name)
				if !ok {
					return nil, false, fmt.Errorf("invalid %s assignment in provider %q", field.name, provider)
				}
				parsed := 0
				if _, scanErr := fmt.Sscanf(value, "%d", &parsed); scanErr != nil || parsed < 0 {
					return nil, false, fmt.Errorf("invalid %s value in provider %q", field.name, provider)
				}
				*field.index = i
				*field.value = parsed
				if parsed < field.min || (exact && parsed != field.min) {
					lines[i] = prefix + fmt.Sprintf("%d", field.min) + suffix + lineEnding
				}
			}
		}
	}
	if inTarget {
		sectionEnd = len(lines)
	}
	if !sectionFound {
		return nil, false, fmt.Errorf("model provider section %q not found", provider)
	}
	changed := false
	if requestIndex >= 0 && (requestValue < requestRetries || (exact && requestValue != requestRetries)) {
		changed = true
	}
	if streamIndex >= 0 && (streamValue < streamRetries || (exact && streamValue != streamRetries)) {
		changed = true
	}
	insertAt := baseURLIndex + 1
	if insertAt <= 0 || insertAt > sectionEnd {
		insertAt = sectionEnd
	}
	insertions := make([]string, 0, 2)
	if requestIndex < 0 {
		insertions = append(insertions, fmt.Sprintf("request_max_retries = %d%s", requestRetries, lineEnding))
	}
	if streamIndex < 0 {
		insertions = append(insertions, fmt.Sprintf("stream_max_retries = %d%s", streamRetries, lineEnding))
	}
	if len(insertions) > 0 {
		changed = true
		lines = insertLines(lines, insertAt, insertions)
	}
	if !changed {
		return data, false, nil
	}
	return []byte(strings.Join(lines, "\n")), true, nil
}

func parseIntegerAssignment(line, key string) (prefix, value, suffix string, ok bool) {
	pattern := regexp.MustCompile(`^(\s*` + regexp.QuoteMeta(key) + `\s*=\s*)([0-9]+)(\s*(?:#.*)?)$`)
	match := pattern.FindStringSubmatch(line)
	if match == nil {
		return "", "", "", false
	}
	return match[1], match[2], match[3], true
}

func insertLines(lines []string, at int, insertions []string) []string {
	if at < 0 {
		at = 0
	}
	if at > len(lines) {
		at = len(lines)
	}
	result := make([]string, 0, len(lines)+len(insertions))
	result = append(result, lines[:at]...)
	result = append(result, insertions...)
	result = append(result, lines[at:]...)
	return result
}

var providerSectionPattern = regexp.MustCompile(`^\[\s*model_providers\.(?:"([^"]+)"|'([^']+)'|([A-Za-z0-9_-]+))\s*\](?:\s*#.*)?$`)

func parseModelProviderSection(line string) (string, bool) {
	match := providerSectionPattern.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	for _, value := range match[1:] {
		if value != "" {
			return value, true
		}
	}
	return "", false
}

func parseQuotedAssignment(line, key string) (prefix, value, suffix string, ok bool) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^(\s*` + regexp.QuoteMeta(key) + `\s*=\s*")([^"]*)("\s*(?:#.*)?)$`),
		regexp.MustCompile(`^(\s*` + regexp.QuoteMeta(key) + `\s*=\s*')([^']*)('\s*(?:#.*)?)$`),
	}
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(line)
		if match != nil {
			return match[1], match[2], match[3], true
		}
	}
	return "", "", "", false
}

func createBackupOnce(path string, data []byte) error {
	backupPath := path + ".responses-guardian.bak"
	f, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("create config backup: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write config backup: %w", err)
	}
	return nil
}

func backupPathFor(configPath string) string {
	return filepath.Clean(configPath) + ".responses-guardian.bak"
}
