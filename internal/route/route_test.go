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

func TestHTTPSSuffixHostWithoutHostHeaderAccepted(t *testing.T) {
	rs := mustParse(t, `[{"host":".example.com","path_prefix":"/","upstream":"https://origin.example.net:443"}]`)
	if rs[0].OutHost != "origin.example.net:443" || rs[0].ServerName != "origin.example.net" {
		t.Fatalf("out=%q sni=%q", rs[0].OutHost, rs[0].ServerName)
	}
	rs = mustParse(t, `[{"host":".example.com","path_prefix":"/","upstream":"http://origin.example.net:80"}]`)
	if rs[0].OutHost != "origin.example.net:80" || rs[0].ServerName != "" {
		t.Fatalf("out=%q sni=%q", rs[0].OutHost, rs[0].ServerName)
	}
}

func TestServerNameResolution(t *testing.T) {
	rs := mustParse(t, `[
		{"host":"example.com","path_prefix":"/","upstream":"https://origin.example.net"},
		{"host":"example.com","path_prefix":"/p","upstream":"https://origin.example.net:8443"},
		{"host":"example.com","path_prefix":"/a","upstream":"https://127.0.0.1:443","host_header":"a.example.com:8443"},
		{"host":"example.com","path_prefix":"/h","upstream":"https://127.0.0.1:443","host_header":"example.com"},
		{"host":"example.com","path_prefix":"/b","upstream":"https://origin.example.net:443","tls_server_name":"sni.example.com"}
	]`)
	want := []struct{ out, sni string }{
		{"origin.example.net", "origin.example.net"},
		{"origin.example.net:8443", "origin.example.net"},
		{"a.example.com:8443", "a.example.com"},
		{"example.com", "example.com"},
		{"origin.example.net:443", "sni.example.com"},
	}
	for i, w := range want {
		if rs[i].OutHost != w.out || rs[i].ServerName != w.sni {
			t.Fatalf("%d: out=%q sni=%q, want %+v", i, rs[i].OutHost, rs[i].ServerName, w)
		}
	}
}
