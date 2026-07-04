package benchmarks

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/subserver"
)

func BenchmarkExtractJSONConfigs(b *testing.B) {
	b.ReportAllocs()
	input := json.RawMessage(`[{"type":"vless","address":"1.2.3.4","port":443,"uuid":"abc"}]`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = subserver.ExtractJSONConfigs(input)
	}
}

func BenchmarkDetectFormat(b *testing.B) {
	b.ReportAllocs()
	jsonBody := []byte(`{"type":"vless","address":"1.2.3.4"}`)
	b.Run("JSON", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = subserver.DetectFormat(jsonBody)
		}
	})
	b.Run("Base64", func(b *testing.B) {
		plain := bytes.Repeat([]byte("a"), 96)
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(plain)))
		base64.StdEncoding.Encode(encoded, plain)
		for i := 0; i < b.N; i++ {
			_ = subserver.DetectFormat(encoded)
		}
	})
	b.Run("Plain", func(b *testing.B) {
		plainBody := bytes.Repeat([]byte("vless://abc@1.2.3.4:443#test\n"), 8)
		for i := 0; i < b.N; i++ {
			_ = subserver.DetectFormat(plainBody)
		}
	})
}

func BenchmarkFilterHeaders(b *testing.B) {
	b.ReportAllocs()
	b.Run("Realistic", func(b *testing.B) {
		headers := make(map[string][]string, 20)
		for i := 0; i < 20; i++ {
			headers[strings.Repeat("k", 8+i)] = []string{strings.Repeat("v", 32)}
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = subserver.FilterHeaders(headers)
		}
	})
}

func BenchmarkParseUserInfoValue(b *testing.B) {
	b.ReportAllocs()
	header := "upload=12345; download=67890; total=100000; expire=1750000000"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = subserver.ParseUserInfoValue(map[string]string{"subscription-userinfo": header}, "download")
	}
}

func BenchmarkResponseHeaders(b *testing.B) {
	b.ReportAllocs()
	src := map[string]string{"profile-title": "X", "routing-rule": "Y"}
	ct := "application/json; charset=utf-8"
	ui := "upload=0; download=0; total=0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = subserver.ResponseHeaders(src, ct, ui)
	}
}
