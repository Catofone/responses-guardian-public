//go:build windows

package main

import (
	"bytes"
	"context"
	"debug/pe"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWindowsDesktopManagerUI(t *testing.T) {
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell unavailable")
	}
	compiler := filepath.Join(os.Getenv("WINDIR"), "Microsoft.NET", "Framework64", "v4.0.30319", "csc.exe")
	if _, err := os.Stat(compiler); err != nil {
		t.Skip(".NET Framework C# compiler unavailable")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "Guardian desktop 测试 with spaces & !")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".ps1") {
			data, err := os.ReadFile(filepath.Join(repo, "scripts", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "scripts", entry.Name()), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	run := func(name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		output, err := os.CreateTemp(root, ".desktop-test-*.log")
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
	must := func(name string, args ...string) {
		t.Helper()
		if output, err := run(name, args...); err != nil {
			t.Fatalf("fixture command: %v\n%s", err, output)
		}
	}
	must("go", "build", "-o", filepath.Join(root, "responses-guardian.exe"), ".")
	ps := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}
	must(shell, append(ps, filepath.Join(repo, "scripts", "build-desktop.ps1"), "-OutputPath", filepath.Join(root, "Guardian.exe"))...)
	image, err := pe.Open(filepath.Join(root, "Guardian.exe"))
	if err != nil {
		t.Fatal(err)
	}
	header, ok := image.OptionalHeader.(*pe.OptionalHeader64)
	_ = image.Close()
	if !ok || header.Subsystem != 2 {
		t.Fatal("desktop executable is not a native Windows GUI application")
	}
	must(shell, append(ps, filepath.Join(repo, "scripts", "build-desktop-tests.ps1"), "-DesktopExecutable", filepath.Join(root, "Guardian.exe"), "-OutputPath", filepath.Join(root, "GuardianApp.Tests.exe"))...)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"status":"healthy"}`) }))
	defer upstream.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	settings := defaultAppSettings()
	settings.Listen, settings.Upstream = listener.Addr().String(), upstream.URL
	_ = listener.Close()
	settings.CodexConfig = filepath.Join(root, "config.toml")
	if err := os.WriteFile(settings.CodexConfig, []byte("[model_providers.custom]\nbase_url = '"+settings.ccswitchBaseURL()+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveAppSettings(filepath.Join(root, "runtime", "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if output, err := run(shell, append(ps, filepath.Join(root, "scripts", "stop-ccswitch.ps1"), "-Force")...); err != nil {
			t.Errorf("fixture shutdown: %v\n%s", err, output)
		}
	})
	captures := os.Getenv("GUARDIAN_DESKTOP_CAPTURE_DIR")
	if captures == "" {
		captures = root
	}
	if err := os.MkdirAll(captures, 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := run(filepath.Join(root, "GuardianApp.Tests.exe"), root, captures)
	if err != nil || !bytes.Contains(output, []byte("DESKTOP_UI_TESTS_PASSED")) {
		if logs, readErr := os.ReadFile(filepath.Join(root, "runtime", "responses-guardian.log")); readErr == nil {
			t.Logf("fixture proxy log: %s", logs)
		}
		t.Fatalf("desktop UI failed: %v\n%s", err, output)
	}
	t.Log("native window: startup, mode buttons, settings validation, close/reopen, protected stop and persistent errors passed")
}
