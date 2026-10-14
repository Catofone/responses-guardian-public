package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func requestContentEncoding(header http.Header) string {
	values := header.Values("Content-Encoding")
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, ",")
}

func decodeRequestBody(body []byte, contentEncoding string, maxBytes int64) ([]byte, bool, error) {
	codings := splitContentCodings(contentEncoding)
	if len(codings) == 0 {
		return body, false, nil
	}

	decoded := body
	for i := len(codings) - 1; i >= 0; i-- {
		var err error
		decoded, err = decodeSingleContentEncoding(codings[i], decoded, maxBytes)
		if err != nil {
			return nil, false, err
		}
	}
	return decoded, true, nil
}

func splitContentCodings(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" && part != "identity" {
			result = append(result, part)
		}
	}
	return result
}

func decodeSingleContentEncoding(coding string, body []byte, maxBytes int64) ([]byte, error) {
	switch coding {
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return readDecodedWithLimit(reader, maxBytes)
	case "zstd", "zst":
		reader, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderMaxMemory(uint64(maxBytes)))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return readDecodedWithLimit(reader, maxBytes)
	case "deflate":
		reader, err := zlib.NewReader(bytes.NewReader(body))
		if err == nil {
			decoded, readErr := readDecodedWithLimit(reader, maxBytes)
			_ = reader.Close()
			if readErr == nil {
				return decoded, nil
			}
		}
		raw := flate.NewReader(bytes.NewReader(body))
		defer raw.Close()
		return readDecodedWithLimit(raw, maxBytes)
	default:
		return nil, fmt.Errorf("unsupported content-encoding %q", coding)
	}
}

func readDecodedWithLimit(reader io.Reader, maxBytes int64) ([]byte, error) {
	decoded, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > maxBytes {
		return nil, fmt.Errorf("decompressed request exceeds %d bytes", maxBytes)
	}
	return decoded, nil
}
