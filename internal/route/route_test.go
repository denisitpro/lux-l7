package route_test

import (
	"strings"
	"testing"

	"github.com/denisitpro/lux-l7/internal/route"
)

func TestMatchHost(t *testing.T) {
	cases := []struct {
		req, pattern string
		want         bool
	}{
		{"example.com", "example.com", true},
		{"EXAMPLE.COM.", "example.com", true},
		{"example.com:443", "example.com", true},
		{"a.example.com", ".example.com", true},
		{"example.com", ".example.com", true},
		{"notexample.com", ".example.com", false},
		{"evil.example.com.evil", ".example.com", false},
	}
	for _, c := range cases {
		if got := route.MatchHost(c.req, c.pattern); got != c.want {
			t.Fatalf("MatchHost(%q,%q)=%v want %v", c.req, c.pattern, got, c.want)
		}
	}
}

func TestPathMatchesSegmentBoundary(t *testing.T) {
	if !route.PathMatches("/location1", "/location1") {
		t.Fatal("exact prefix")
	}
	if !route.PathMatches("/location1", "/location1/") {
		t.Fatal("with slash")
	}
	if !route.PathMatches("/location1", "/location1/x") {
		t.Fatal("with suffix")
	}
	if route.PathMatches("/location1", "/location10") {
		t.Fatal("/location10 must not match /location1")
	}
	if !route.PathMatches("/", "/anything") {
		t.Fatal("/ catch-all")
	}
}

func TestMatchFirstWins(t *testing.T) {
	routes := mustParse(t, `[
		{"host":"example.com","path_prefix":"/a","upstream":"http://127.0.0.1:1"},
		{"host":"example.com","path_prefix":"/","upstream":"http://127.0.0.1:2"}
	]`)
	if route.Match(routes, "example.com", "/a/x") != 0 {
		t.Fatal("expected first route")
	}
	if route.Match(routes, "example.com", "/b") != 1 {
		t.Fatal("expected second route")
	}
}

func TestRewritePrefix(t *testing.T) {
	cases := []struct {
		path, prefix, rewrite, want string
	}{
		{"/location1/x", "/location1", "/", "/x"},
		{"/location1/x", "/location1", "/api", "/api/x"},
		{"/location1", "/location1", "/", "/"},
		{"/location1/x?y=1", "/location1", "/", "/x"},
	}
	for _, c := range cases {
		path := strings.SplitN(c.path, "?", 2)[0]
		got := route.RewritePath(path, c.prefix, c.rewrite)
		if got != c.want {
			t.Fatalf("RewritePath(%q)=%q want %q", c.path, got, c.want)
		}
	}
	if route.RewritePath("/foo", "/location1", "") != "/foo" {
		t.Fatal("empty rewrite leaves path")
	}
}

func TestParseJSONValidation(t *testing.T) {
	_, err := route.ParseJSON("")
	if err == nil {
		t.Fatal("empty ROUTES")
	}
	_, err = route.ParseJSON("[]")
	if err == nil {
		t.Fatal("empty array")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"ftp://x"}]`)
	if err == nil || !strings.Contains(err.Error(), "ROUTES[0]") {
		t.Fatalf("bad scheme: %v", err)
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"https://x/path"}]`)
	if err == nil {
		t.Fatal("path in upstream")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"https://x?q=1"}]`)
	if err == nil {
		t.Fatal("query in upstream")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"https://u:p@x"}]`)
	if err == nil {
		t.Fatal("userinfo")
	}
	_, err = route.ParseJSON(`[{"path_prefix":"/","upstream":"http://127.0.0.1"}]`)
	if err == nil {
		t.Fatal("missing host")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","upstream":"http://127.0.0.1"}]`)
	if err == nil {
		t.Fatal("missing path_prefix")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"x","upstream":"http://127.0.0.1"}]`)
	if err == nil {
		t.Fatal("path_prefix must start with /")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"/"}]`)
	if err == nil {
		t.Fatal("missing upstream")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"http://127.0.0.1","extra":1}]`)
	if err == nil {
		t.Fatal("unknown field")
	}
	_, err = route.ParseJSON(`[{"host":"example.com","path_prefix":"/","upstream":"http://127.0.0.1","tls_server_name":"x"}]`)
	if err == nil {
		t.Fatal("tls_server_name on http")
	}
}

func mustParse(t *testing.T, raw string) []route.Route {
	r, err := route.ParseJSON(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}
