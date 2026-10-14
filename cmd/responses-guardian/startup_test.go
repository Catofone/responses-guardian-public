package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func startupFixture(t *testing.T) (AppSettings, []byte) {
	t.Helper()
	settings := defaultAppSettings()
	settings.CodexConfig = filepath.Join(t.TempDir(), "config.toml")
	// These addresses are only written into isolated files, never contacted.
	settings.Listen, settings.Upstream = "127.0.0.1:17722", "http://127.0.0.1:17721"
	initial := []byte("model_provider = \"custom\"\r\n[model_providers.custom]\r\nbase_url = '" + settings.ccswitchBaseURL() + "' # CCSwitch\r\nrequest_max_retries = 5\r\nexperimental_bearer_token = 'fixture-secret'\r\n")
	if err := os.WriteFile(settings.CodexConfig, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	return settings, initial
}

func TestStartupRollbackRestoresExactSnapshot(t *testing.T) {
	settings, initial := startupFixture(t)
	manager := NewConfigManager(settings.CodexConfig, settings.Provider, settings.ccswitchBaseURL(), settings.guardianBaseURL())
	change, err := manager.BeginStartup(nil)
	if err != nil || !change.changed {
		t.Fatalf("startup write: %v", err)
	}
	if err := change.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings.CodexConfig)
	if !bytes.Equal(data, initial) {
		t.Fatal("failed startup changed routing, retries, credentials or formatting")
	}
}

func TestStartupRollbackPreservesConcurrentEdit(t *testing.T) {
	settings, _ := startupFixture(t)
	manager := NewConfigManager(settings.CodexConfig, settings.Provider, settings.ccswitchBaseURL(), settings.guardianBaseURL())
	change, err := manager.BeginStartup(nil)
	if err != nil {
		t.Fatal(err)
	}
	concurrent := bytes.ReplaceAll(change.after, []byte("fixture-secret"), []byte("new-user-credential"))
	concurrent = append(concurrent, []byte("# user edit after startup write\n")...)
	if err := os.WriteFile(settings.CodexConfig, concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := change.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings.CodexConfig)
	expected := bytes.Replace(concurrent, []byte(settings.guardianBaseURL()), []byte(settings.ccswitchBaseURL()), 1)
	if !bytes.Equal(data, expected) {
		t.Fatal("rollback left a dead route or overwrote concurrent credentials/comments")
	}
}

func TestStartupRollbackRefusesConcurrentRouteChange(t *testing.T) {
	settings, _ := startupFixture(t)
	manager := NewConfigManager(settings.CodexConfig, settings.Provider, settings.ccswitchBaseURL(), settings.guardianBaseURL())
	change, err := manager.BeginStartup(nil)
	if err != nil {
		t.Fatal(err)
	}
	concurrent := bytes.Replace(change.after, []byte(settings.guardianBaseURL()), []byte("http://127.0.0.1:17730/v1"), 1)
	if err := os.WriteFile(settings.CodexConfig, concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := change.Rollback(); !errors.Is(err, errFileChanged) {
		t.Fatalf("expected routing conflict, got %v", err)
	}
	data, _ := os.ReadFile(settings.CodexConfig)
	if !bytes.Equal(data, concurrent) {
		t.Fatal("rollback overwrote a concurrent route choice")
	}
}

func TestStartupInstanceWriteFailureRollsBackNewPort(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows read-only destination replacement regression")
	}
	settings, initial := startupFixture(t)
	instanceFile := filepath.Join(filepath.Dir(settings.CodexConfig), "instance.json")
	if err := saveInstance(instanceFile, settings); err != nil {
		t.Fatal(err)
	}
	oldInstance, _ := os.ReadFile(instanceFile)
	if err := os.Chmod(instanceFile, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(instanceFile, 0o600) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	_ = listener.Close()
	settingsFile := filepath.Join(filepath.Dir(settings.CodexConfig), "settings.json")
	pidFile := filepath.Join(filepath.Dir(settings.CodexConfig), "guardian.pid")
	err = run([]string{"--manage-config", "--listen", listen, "--upstream", settings.Upstream,
		"--codex-config", settings.CodexConfig, "--settings-file", settingsFile,
		"--instance-file", instanceFile, "--pid-file", pidFile})
	if err == nil || !strings.Contains(err.Error(), "save active settings") {
		t.Fatalf("expected instance write error, got %v", err)
	}
	data, _ := os.ReadFile(settings.CodexConfig)
	if !bytes.Equal(data, initial) {
		t.Fatal("Codex was left pointing to a failed startup's port")
	}
	data, _ = os.ReadFile(instanceFile)
	if !bytes.Equal(data, oldInstance) {
		t.Fatal("failed startup replaced the previous instance")
	}
	if _, err := os.Stat(pidFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed startup left a PID file")
	}
	listener, err = net.Listen("tcp", listen)
	if err != nil {
		t.Fatalf("failed startup left its listener open: %v", err)
	}
	_ = listener.Close()
}

func TestStartupMigratesPreviousPortsOnlyOnce(t *testing.T) {
	for _, oldRoute := range []string{"ccswitch", "guardian"} {
		t.Run(oldRoute, func(t *testing.T) {
			old, _ := startupFixture(t)
			route := old.ccswitchBaseURL()
			if oldRoute == "guardian" {
				route = old.guardianBaseURL()
			}
			initial := []byte("[model_providers.custom]\nbase_url = \"" + route + "\"\n")
			if err := os.WriteFile(old.CodexConfig, initial, 0o600); err != nil {
				t.Fatal(err)
			}
			next := old
			next.Listen, next.Upstream = "127.0.0.1:17724", "http://127.0.0.1:17723"
			manager := NewConfigManager(next.CodexConfig, next.Provider, next.ccswitchBaseURL(), next.guardianBaseURL())
			if _, err := manager.EnsureGuardian(); err == nil {
				t.Fatal("old route accepted without a previous instance")
			}
			if err := manager.Preflight(&old); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.BeginStartup(&old); err != nil {
				t.Fatalf("saved port change cannot start: %v", err)
			}
			if _, err := manager.RestoreCCSwitch(); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(next.CodexConfig)
			if !bytes.Contains(data, []byte(next.ccswitchBaseURL())) {
				t.Fatal("shutdown did not restore the new upstream")
			}
			if err := os.WriteFile(next.CodexConfig, initial, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.EnsureGuardian(); err == nil {
				t.Fatal("watcher kept trusting the previous route after startup")
			}
		})
	}
}

func TestStartupMigrationRefusesUnrelatedInstance(t *testing.T) {
	for _, mutate := range []func(*AppSettings){
		func(s *AppSettings) { s.Provider = "another-provider" },
		func(s *AppSettings) { s.CodexConfig = filepath.Join(t.TempDir(), "other.toml") },
		func(s *AppSettings) { s.Upstream = "https://external.example" },
	} {
		old, initial := startupFixture(t)
		next := old
		next.Listen, next.Upstream = "127.0.0.1:17724", "http://127.0.0.1:17723"
		mutate(&old)
		manager := NewConfigManager(next.CodexConfig, next.Provider, next.ccswitchBaseURL(), next.guardianBaseURL())
		if _, err := manager.BeginStartup(&old); err == nil {
			t.Fatal("unrelated or invalid instance authorized a route change")
		}
		data, _ := os.ReadFile(next.CodexConfig)
		if !bytes.Equal(data, initial) {
			t.Fatal("refused migration changed config")
		}
	}
}

func TestRestoreIgnoresUnusablePendingSettings(t *testing.T) {
	settings, _ := startupFixture(t)
	manager := NewConfigManager(settings.CodexConfig, settings.Provider, settings.ccswitchBaseURL(), settings.guardianBaseURL())
	if _, err := manager.EnsureGuardian(); err != nil {
		t.Fatal(err)
	}
	instanceFile := filepath.Join(filepath.Dir(settings.CodexConfig), "instance.json")
	if err := saveInstance(instanceFile, settings); err != nil {
		t.Fatal(err)
	}
	pendingFile := filepath.Join(filepath.Dir(settings.CodexConfig), "pending.json")
	if err := os.WriteFile(pendingFile, []byte("invalid pending JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--restore-config", "--instance-file", instanceFile, "--settings-file", pendingFile}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings.CodexConfig)
	if !bytes.Contains(data, []byte(settings.ccswitchBaseURL())) {
		t.Fatal("restore used pending settings rather than the active instance")
	}
}
