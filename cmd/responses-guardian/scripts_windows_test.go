//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Uses a disposable executable, config and ports. Never invokes the scripts
// from a real Guardian installation or reads the user's Codex config.
func TestPowerShell51GuardianLifecycle(t *testing.T) {
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell 5.1 unavailable")
	}
	root := filepath.Join(t.TempDir(), "Guardian fixture with spaces")
	scripts := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scripts, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".ps1") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "..", "scripts", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scripts, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(root, "responses-guardian.exe")
	build := exec.Command("go", "build", "-o", exe, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	runPS := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, shell, append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass"}, args...)...)
		// A background process may inherit its parent's pipe handles on Windows.
		// A file keeps command completion independent of those inherited handles.
		output, err := os.CreateTemp(root, ".powershell-test-*.log")
		if err != nil {
			return nil, err
		}
		defer os.Remove(output.Name())
		defer output.Close()
		command.Stdout, command.Stderr = output, output
		err = command.Run()
		if _, seekErr := output.Seek(0, io.SeekStart); seekErr != nil {
			return nil, seekErr
		}
		data, readErr := io.ReadAll(output)
		if readErr != nil {
			return nil, readErr
		}
		return data, err
	}
	runScript := func(name string, args ...string) ([]byte, error) {
		return runPS(append([]string{"-File", filepath.Join(scripts, name)}, args...)...)
	}
	mustScript := func(name string, args ...string) {
		t.Helper()
		output, err := runScript(name, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, output)
		}
	}
	psQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	prelude := ". " + psQuote(filepath.Join(scripts, "guardian-common.ps1")) + "; $guardianRoot=" + psQuote(root) + "; "
	launcherCommand := `function Start-Process {
    [CmdletBinding()]
    param([string]$FilePath, [string]$WorkingDirectory, [string[]]$ArgumentList,
          [string]$WindowStyle, [string]$RedirectStandardOutput,
          [string]$RedirectStandardError, [switch]$PassThru)
    if ($FilePath -match '^http://') {
        $page = Invoke-WebRequest -UseBasicParsing -Uri $FilePath -TimeoutSec 3
        if ($page.StatusCode -ne 200) { throw 'Fixture panel was unavailable' }
        Write-Host ('FIXTURE_PANEL_OPENED ' + $FilePath)
    } else {
        Write-Host 'FIXTURE_PROXY_STARTED'
        Microsoft.PowerShell.Management\Start-Process @PSBoundParameters
    }
}; & ` + psQuote(filepath.Join(scripts, "launch-guardian.ps1"))
	mustLauncher := func(expectStart bool) {
		t.Helper()
		output, err := runPS("-Command", launcherCommand)
		if err != nil || !bytes.Contains(output, []byte("FIXTURE_PANEL_OPENED")) {
			t.Fatalf("launcher: %v\n%s", err, output)
		}
		if bytes.Contains(output, []byte("FIXTURE_PROXY_STARTED")) != expectStart {
			t.Fatalf("launcher started the proxy unexpectedly: %s", output)
		}
	}
	output, err := runPS("-Command", prelude+`if ($PSVersionTable.PSVersion.Major -ne 5) { throw 'Expected Windows PowerShell 5.1' }; Get-ChildItem -LiteralPath (Join-Path $guardianRoot 'scripts') -Filter *.ps1 | ForEach-Object { $guardianTokens=$null; $guardianErrors=$null; [System.Management.Automation.Language.Parser]::ParseFile($_.FullName,[ref]$guardianTokens,[ref]$guardianErrors) | Out-Null; if ($guardianErrors) { throw $guardianErrors } }`)
	if err != nil {
		t.Fatalf("PowerShell 5.1 syntax: %v\n%s", err, output)
	}
	newUpstream := func() *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				_, _ = io.WriteString(w, `{"status":"healthy"}`)
			} else {
				_, _ = io.WriteString(w, `{"fixture_forwarded":true}`)
			}
		}))
		t.Cleanup(server.Close)
		return server
	}
	reserveIPv6 := func() string {
		t.Helper()
		listener, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Fatalf("IPv6 loopback fixture: %v", err)
		}
		address := listener.Addr().String()
		_ = listener.Close()
		return address
	}
	settings := defaultAppSettings()
	settings.Listen, settings.Upstream = reserveIPv6(), newUpstream().URL
	settings.CodexConfig = filepath.Join(root, "config.toml")
	initial := []byte("[model_providers.custom]\nbase_url = '" + settings.ccswitchBaseURL() + "'\n")
	if err := os.WriteFile(settings.CodexConfig, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	settingsFile := filepath.Join(root, "runtime", "settings.json")
	if err := saveAppSettings(settingsFile, settings); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(settings.Listen)
	// An unrelated IPv4 listener on the same port must neither block the IPv6
	// start nor be mistaken for the Guardian process during mode switches.
	ipv4, err := net.Listen("tcp4", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ipv4.Close() })
	t.Cleanup(func() {
		output, err := runScript("stop-ccswitch.ps1", "-Force")
		if err != nil {
			t.Errorf("fixture cleanup: %v\n%s", err, output)
		}
	})
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	readHealth := func(listen string) struct {
		PID  int    `json:"pid"`
		Mode string `json:"mode"`
	} {
		t.Helper()
		resp, err := client.Get("http://" + listen + "/__responses_guardian/health")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var health struct {
			PID  int    `json:"pid"`
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
			t.Fatal(err)
		}
		return health
	}
	mustLauncher(true)
	first := readHealth(settings.Listen)
	if first.Mode != "off" {
		t.Fatal("first launcher start did not default to off")
	}
	mustScript("set-mode.ps1", "always", "-Port", port)
	if health := readHealth(settings.Listen); health.Mode != "always" || health.PID != first.PID {
		t.Fatal("IPv6 live switch restarted the process or did not apply")
	}
	mustLauncher(false)
	if health := readHealth(settings.Listen); health.Mode != "always" || health.PID != first.PID {
		t.Fatal("opening the panel changed the active process or mode")
	}
	output, err = runPS("-Command", `function Start-Process { param([string]$FilePath); throw 'FIXTURE_BROWSER_FAILURE' }; & `+psQuote(filepath.Join(scripts, "launch-guardian.ps1")))
	if err == nil || !bytes.Contains(output, []byte("FIXTURE_BROWSER_FAILURE")) {
		t.Fatalf("browser failure was not returned: %v\n%s", err, output)
	}
	if health := readHealth(settings.Listen); health.Mode != "always" || health.PID != first.PID {
		t.Fatal("browser failure changed the running proxy")
	}
	mustScript("stop-ccswitch.ps1", "-Force")
	mustLauncher(true)
	inherited := readHealth(settings.Listen)
	if inherited.Mode != "always" || inherited.PID == first.PID {
		t.Fatal("launcher did not inherit the last mode after shutdown")
	}
	first = inherited
	mustScript("activate-always.ps1") // Already live; must not try to update.
	mustScript("start-ccswitch.ps1", "-Mode", "off")
	if health := readHealth(settings.Listen); health.Mode != "off" || health.PID != first.PID {
		t.Fatal("repeated IPv6 start did not reuse the active process")
	}
	mustScript("status.ps1")
	next := settings
	next.Listen, next.Upstream = reserveIPv6(), newUpstream().URL
	if err := saveAppSettings(settingsFile, next); err != nil {
		t.Fatal(err)
	}
	mustScript("stop-ccswitch.ps1", "-Force")
	data, _ := os.ReadFile(settings.CodexConfig)
	if !bytes.Contains(data, []byte(settings.ccswitchBaseURL())) {
		t.Fatal("stopping the old instance used pending settings")
	}
	mustScript("start-ccswitch.ps1", "-Mode", "off")
	if health := readHealth(next.Listen); health.Mode != "off" || health.PID == first.PID {
		t.Fatal("new ports were not activated")
	}
	resp, err := client.Post("http://"+next.Listen+"/v1/responses", "application/json", strings.NewReader(`{"input":"fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	forwarded, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Contains(forwarded, []byte(`"fixture_forwarded":true`)) {
		t.Fatal("new upstream was not reached")
	}
	mustScript("stop-ccswitch.ps1", "-Force")
	data, _ = os.ReadFile(settings.CodexConfig)
	if !bytes.Contains(data, []byte(next.ccswitchBaseURL())) {
		t.Fatal("new instance did not restore the new CCSwitch port")
	}
	// Reproduce the original fatal failure with a previous read-only instance
	// and another pending listener. The script must not run a stale restore.
	failed := next
	failed.Listen = reserveIPv6()
	if err := saveAppSettings(settingsFile, failed); err != nil {
		t.Fatal(err)
	}
	instanceFile := filepath.Join(root, "runtime", "instance.json")
	oldInstance, _ := os.ReadFile(instanceFile)
	if err := os.Chmod(instanceFile, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(instanceFile, 0o600) })
	output, err = runScript("start-ccswitch.ps1", "-Mode", "off")
	if err == nil {
		t.Fatalf("read-only instance startup unexpectedly succeeded: %s", output)
	}
	after, _ := os.ReadFile(settings.CodexConfig)
	instanceAfter, _ := os.ReadFile(instanceFile)
	if !bytes.Equal(data, after) || !bytes.Equal(oldInstance, instanceAfter) {
		t.Fatal("failed startup or stale restore changed the original config/instance")
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "responses-guardian.pid")); !os.IsNotExist(err) {
		t.Fatal("failed startup script left its PID file")
	}
	if err := os.Chmod(instanceFile, 0o600); err != nil {
		t.Fatal(err)
	}
	mustScript("start-ccswitch.ps1", "-Mode", "off")
	health := readHealth(failed.Listen)
	if health.Mode != "off" {
		t.Fatal("retry after startup failure did not recover")
	}
	mustScript("stop-ccswitch.ps1", "-Force")
	modeFile := filepath.Join(root, "runtime", "mode.json")
	invalidMode := []byte(`{"mode":"invalid"}`)
	if err := os.WriteFile(modeFile, invalidMode, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err = runPS("-Command", launcherCommand)
	if err == nil || !bytes.Contains(output, []byte("saved mode is invalid")) || bytes.Contains(output, []byte("FIXTURE_PANEL_OPENED")) {
		t.Fatalf("invalid saved mode was not reported: %v\n%s", err, output)
	}
	modeAfter, _ := os.ReadFile(modeFile)
	if !bytes.Equal(modeAfter, invalidMode) {
		t.Fatal("launcher silently overwrote an invalid preference")
	}
	t.Logf("PowerShell 5.1: IPv6 lifecycle, same-port IPv4 isolation, port migration, forwarding and startup rollback passed (fixture PID %s)", strconv.Itoa(health.PID))
}
