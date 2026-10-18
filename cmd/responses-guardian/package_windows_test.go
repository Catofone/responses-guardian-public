//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicPackagePreflight(t *testing.T) {
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell unavailable")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(repo, "scripts", "public-files.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		DesktopVersion string   `json:"desktopVersion"`
		ProxyVersion   string   `json:"proxyVersion"`
		Files          []string `json:"files"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "public source with spaces")
	for _, relative := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(fixture, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	privateNote := filepath.Join(fixture, "docs", "personal.prompt.md")
	if err := os.WriteFile(privateNote, []byte("This file must never enter the public export."), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDirectory := filepath.Join(t.TempDir(), "not-created-by-preflight")
	check := func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File",
			filepath.Join(fixture, "scripts", "package-public.ps1"), "-ValidateOnly", "-OutputDirectory", outputDirectory)
		return cmd.CombinedOutput()
	}
	output, err := check()
	if err != nil {
		t.Fatalf("valid public preflight failed: %v\n%s", err, output)
	}
	var checked struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(output, &checked); err != nil {
		t.Fatalf("invalid preflight report: %v\n%s", err, output)
	}
	for _, name := range checked.Files {
		if strings.EqualFold(filepath.Base(name), "AGENTS.md") || strings.Contains(name, "personal.prompt") || strings.Contains(name, "GITHUB.zh-CN") || strings.Contains(name, "REVIEW-") {
			t.Fatalf("personal documentation entered the public allowlist: %s", name)
		}
	}
	for _, relative := range []string{"AGENTS.md", "docs/aGeNtS.Md", "AGENTS.local.md", ".codex/notes.md", "docs/GITHUB.zh-CN.md", "docs/personal.prompt.md"} {
		t.Run("reject_"+relative, func(t *testing.T) {
			path := filepath.Join(fixture, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("Local collaboration notes."), 0o600); err != nil {
				t.Fatal(err)
			}
			modified := manifest
			modified.Files = append(append([]string(nil), manifest.Files...), relative)
			data, err := json.Marshal(modified)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(fixture, "scripts", "public-files.json")
			if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
					t.Error(err)
				}
			})
			output, err := check()
			if err == nil {
				t.Fatalf("local instructions or notes were accepted: %s", relative)
			}
			if !strings.Contains(string(output), "Private content") {
				t.Fatalf("local instructions or notes were not rejected: %v\n%s", err, output)
			}
		})
	}
	readmePath := filepath.Join(fixture, "README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, extra string }{
		{"internal_link", "\n[internal](" + "codex" + "://threads/" + strings.Repeat("1", 36) + ")\n"},
		{"local_workspace", "\nWorkspace: Q:\\demo\\workspace\n"},
		{"local_history", "\nPersonal snapshot: baseline-2000-01-01\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(readmePath, append(append([]byte(nil), readme...), []byte(tc.extra)...), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := check()
			if err == nil || !strings.Contains(string(output), "Personal notes") {
				t.Fatalf("private documentation was not rejected: %v\n%s", err, output)
			}
		})
	}
	if err := os.WriteFile(readmePath, readme, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest.Files = append(manifest.Files, "../outside.md")
	invalidManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "scripts", "public-files.json"), invalidManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err = check()
	if err == nil || !strings.Contains(string(output), "Invalid public path") {
		t.Fatalf("escaping path was not rejected: %v\n%s", err, output)
	}
	if _, err := os.Stat(outputDirectory); !os.IsNotExist(err) {
		t.Fatalf("preflight wrote release output: %v", err)
	}
}
