//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDoubleClickLauncherKeepsFailureUntilAcknowledged(t *testing.T) {
	batch, err := os.ReadFile(filepath.Join("..", "..", "启动Guardian.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			// Exercise quoting with spaces, shell metacharacters and Unicode.
			root := filepath.Join(t.TempDir(), "Guardian 测试 & ! with spaces")
			scripts := filepath.Join(root, "scripts")
			if err := os.MkdirAll(scripts, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "启动Guardian.cmd"), batch, 0o600); err != nil {
				t.Fatal(err)
			}
			stub := "[Console]::WriteLine('FIXTURE_LAUNCH_OK'); exit 0\r\n"
			if failure {
				stub = "[Console]::Error.WriteLine('FIXTURE_LAUNCH_ERROR'); exit 7\r\n"
			}
			if err := os.WriteFile(filepath.Join(scripts, "launch-guardian.ps1"), []byte(stub), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "cmd.exe", "/D", "/C", "启动Guardian.cmd")
			command.Dir = root
			outputPath := filepath.Join(root, "launcher-test.log")
			output, err := os.Create(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			command.Stdout, command.Stderr = output, output
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			if failure {
				for {
					data, _ := os.ReadFile(outputPath)
					if bytes.Contains(data, []byte("Review the error above")) {
						if !bytes.Contains(data, []byte("FIXTURE_LAUNCH_ERROR")) {
							t.Fatal("launcher discarded the original error")
						}
						break
					}
					select {
					case err := <-done:
						t.Fatalf("failure window closed before acknowledgement: %v\n%s", err, data)
					case <-ctx.Done():
						t.Fatalf("launcher did not show its failure: %s", data)
					case <-time.After(50 * time.Millisecond):
					}
				}
				// Allow time to observe the paused process before supplying a key.
				select {
				case err := <-done:
					t.Fatalf("failure window did not stay open: %v", err)
				case <-time.After(200 * time.Millisecond):
				}
				if _, err := io.WriteString(input, "\r\n"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				data, _ := os.ReadFile(outputPath)
				if failure {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
						t.Fatalf("launcher lost the failure exit code: %v\n%s", err, data)
					}
				} else if err != nil || !bytes.Contains(data, []byte("FIXTURE_LAUNCH_OK")) || bytes.Contains(data, []byte("Review the error above")) {
					t.Fatalf("successful launcher did not close normally: %v\n%s", err, data)
				}
			case <-ctx.Done():
				t.Fatal("launcher did not close when expected")
			}
		})
	}
}
