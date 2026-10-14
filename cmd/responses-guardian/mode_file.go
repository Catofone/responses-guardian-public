package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"time"
)

const modePollInterval = 200 * time.Millisecond

// Only this small, local preference file is read. It contains no credentials
// and changing it does not require a writable HTTP administration endpoint.
func readModeFile(path string) (recoveryMode, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", err
	}
	if len(data) > 4096 {
		return "", errors.New("mode file exceeds 4096 bytes")
	}
	var preference struct {
		Mode string `json:"mode"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&preference); err != nil {
		return "", errors.New("mode file must contain a JSON object with a mode")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", errors.New("mode file must contain exactly one JSON object")
	}
	return parseRecoveryMode(preference.Mode)
}

func initialMode(fallback recoveryMode, path string) (recoveryMode, error) {
	if path == "" {
		return fallback, nil
	}
	mode, err := readModeFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	return mode, err
}

func (g *Guardian) watchModeFile(ctx context.Context) {
	ticker := time.NewTicker(modePollInterval)
	defer ticker.Stop()
	lastError := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mode, err := readModeFile(g.modeFile)
			if err != nil {
				// An interrupted edit or invalid preference must not change the
				// running mode. A subsequent valid write automatically recovers.
				if err.Error() != lastError {
					log.Printf("mode file could not be loaded; keeping current mode")
					lastError = err.Error()
				}
				continue
			}
			lastError = ""
			g.setMode(mode)
		}
	}
}
