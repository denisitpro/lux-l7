package listenlog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
)

func TestUp(t *testing.T) {
	tests := []struct {
		name     string
		listener string
		addr     string
		mode     string
		tls      bool
		extra    []any
		want     map[string]any
	}{
		{"main header", "main", "127.0.0.1:8080", "header", false, []any{"h2c", false},
			map[string]any{"listener": "main", "addr": "127.0.0.1:8080", "mode": "header", "tls": false, "h2c": false}},
		{"main route", "main", ":8080", "route", true, []any{"routes", 3},
			map[string]any{"listener": "main", "addr": ":8080", "mode": "route", "tls": true, "routes": float64(3)}},
		{"tunnel", "tunnel", ":443", "tunnel", true, nil,
			map[string]any{"listener": "tunnel", "addr": ":443", "mode": "tunnel", "tls": true}},
		{"metrics", "metrics", "127.0.0.1:9090", "metrics", false, nil,
			map[string]any{"listener": "metrics", "addr": "127.0.0.1:9090", "mode": "metrics", "tls": false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			Up(slog.New(slog.NewJSONHandler(&buf, nil)), tt.listener, tt.addr, tt.mode, tt.tls, tt.extra...)
			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got["msg"] != "listener up" || got["level"] != "INFO" {
				t.Fatalf("msg/level = %v/%v", got["msg"], got["level"])
			}
			delete(got, "msg")
			delete(got, "level")
			delete(got, "time")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
