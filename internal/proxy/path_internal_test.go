package proxy

import "testing"

func TestRewriteLocationDefaultPorts(t *testing.T) {
	https := target{scheme: "https", host: "api.example.com"}
	http := target{scheme: "http", host: "api.example.com:8080"}
	for _, c := range []struct {
		t        target
		in, want string
	}{
		{https, "https://api.example.com:443/x", "/h/api.example.com/x"},
		{https, "https://API.example.com./x", "/h/api.example.com/x"},
		{https, "https://api.example.com:8443/x", "https://api.example.com:8443/x"},
		{https, "http://api.example.com/x", "http://api.example.com/x"},
		{https, "https://user@api.example.com/x", "https://user@api.example.com/x"},
		{http, "http://api.example.com:8080/x", "/h/api.example.com:8080/x"},
		{http, "http://api.example.com/x", "http://api.example.com/x"},
		{https, "//evil.example.net/x", "//evil.example.net/x"},
	} {
		if got := rewriteLocation(c.in, c.t); got != c.want {
			t.Errorf("%s: %q -> %q, want %q", c.t.host, c.in, got, c.want)
		}
	}
}
