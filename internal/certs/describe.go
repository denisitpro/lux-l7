package certs

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"time"
)

// maxSANs caps how many subject alternative names a log line lists; a wildcard
// bundle can carry hundreds and one line must stay short.
const maxSANs = 10

// Info is the public description of a loaded leaf certificate, for logs. It never
// holds key material, only attributes anyone can read off the certificate.
type Info struct {
	KeyType   string // rsa, ecdsa, ed25519, or other
	KeyBits   int    // rsa only
	Curve     string // ecdsa only
	Subject   string // subject CN, may be empty
	SANs      []string
	SANsTotal int // len(SANs) before the cap
	NotAfter  time.Time
}

// Describe reads the public attributes of leaf.
func Describe(leaf *x509.Certificate) Info {
	info := Info{KeyType: "other", Subject: leaf.Subject.CommonName, NotAfter: leaf.NotAfter.UTC()}
	switch pub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		info.KeyType, info.KeyBits = "rsa", pub.N.BitLen()
	case *ecdsa.PublicKey:
		info.KeyType, info.Curve = "ecdsa", pub.Curve.Params().Name
	case ed25519.PublicKey:
		info.KeyType = "ed25519"
	}
	sans := append([]string(nil), leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		sans = append(sans, ip.String())
	}
	info.SANsTotal = len(sans)
	if len(sans) > maxSANs {
		sans = sans[:maxSANs]
	}
	info.SANs = sans
	return info
}

// Attrs are the slog attributes for the certificate log lines.
func (i Info) Attrs() []any {
	attrs := []any{"key_type", i.KeyType}
	switch i.KeyType {
	case "rsa":
		attrs = append(attrs, "key_bits", i.KeyBits)
	case "ecdsa":
		attrs = append(attrs, "curve", i.Curve)
	}
	attrs = append(attrs, "subject_cn", i.Subject, "sans", i.SANs)
	if i.SANsTotal > len(i.SANs) {
		attrs = append(attrs, "sans_total", i.SANsTotal)
	}
	return append(attrs, "not_after", i.NotAfter.Format(time.RFC3339))
}

// describePair describes the leaf of a loaded pair; ok is false when the leaf cannot be parsed.
func describePair(cert *tls.Certificate) (Info, bool) {
	leaf := cert.Leaf
	if leaf == nil {
		if len(cert.Certificate) == 0 {
			return Info{}, false
		}
		var err error
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return Info{}, false
		}
	}
	return Describe(leaf), true
}
