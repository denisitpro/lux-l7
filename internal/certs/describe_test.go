package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"testing"
	"time"
)

func leafFor(t *testing.T, pub, priv any, cn string, dns []string, ips []net.IP) *x509.Certificate {
	t.Helper()
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC),
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv.(crypto.Signer))
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDescribeKeyTypes(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPub, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name string
		leaf *x509.Certificate
		want Info
	}{
		{"rsa", leafFor(t, &rsaKey.PublicKey, rsaKey, "rsa.example.com", []string{"rsa.example.com"}, nil),
			Info{KeyType: "rsa", KeyBits: 2048, Subject: "rsa.example.com", SANs: []string{"rsa.example.com"}, SANsTotal: 1, NotAfter: notAfter}},
		{"ecdsa p256", leafFor(t, &ecKey.PublicKey, ecKey, "ec.example.com", []string{"ec.example.com", "*.example.com"}, []net.IP{net.ParseIP("127.0.0.1")}),
			Info{KeyType: "ecdsa", Curve: "P-256", Subject: "ec.example.com", SANs: []string{"ec.example.com", "*.example.com", "127.0.0.1"}, SANsTotal: 3, NotAfter: notAfter}},
		{"ed25519", leafFor(t, edPub, edKey, "", nil, nil),
			Info{KeyType: "ed25519", NotAfter: notAfter}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Describe(tt.leaf)
			if got.SANs == nil {
				got.SANs = tt.want.SANs // nil vs empty carries no meaning
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDescribeCapsSANs(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var dns []string
	for i := range 25 {
		dns = append(dns, fmt.Sprintf("h%d.example.com", i))
	}
	info := Describe(leafFor(t, &key.PublicKey, key, "x", dns, nil))
	if len(info.SANs) != maxSANs || info.SANsTotal != 25 {
		t.Fatalf("SANs %d total %d, want %d and 25", len(info.SANs), info.SANsTotal, maxSANs)
	}
	attrs := info.Attrs()
	found := false
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i] == "sans_total" && attrs[i+1] == 25 {
			found = true
		}
	}
	if !found {
		t.Fatalf("sans_total missing in %v", attrs)
	}
}

func TestAttrsPerKeyType(t *testing.T) {
	keys := func(a []any) (ks []string) {
		for i := 0; i < len(a); i += 2 {
			ks = append(ks, a[i].(string))
		}
		return
	}
	cases := []struct {
		info Info
		want []string
	}{
		{Info{KeyType: "rsa", KeyBits: 2048}, []string{"key_type", "key_bits", "subject_cn", "sans", "not_after"}},
		{Info{KeyType: "ecdsa", Curve: "P-256"}, []string{"key_type", "curve", "subject_cn", "sans", "not_after"}},
		{Info{KeyType: "ed25519"}, []string{"key_type", "subject_cn", "sans", "not_after"}},
	}
	for _, c := range cases {
		if got := keys(c.info.Attrs()); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: keys %v, want %v", c.info.KeyType, got, c.want)
		}
	}
}
