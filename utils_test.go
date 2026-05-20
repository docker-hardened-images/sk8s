package sk8s

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func tlsMockSecret(certName string, caCrt, tlsCret, tlsKey []byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: certName,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"ca.crt":  caCrt,
			"tls.crt": tlsCret,
			"tls.key": tlsKey,
		},
	}
}

// PEM material for tlsMockSecret tests only (CN=sk8s.testfixture, ephemeral).
var (
	fixtureCACertPEM = []byte(`-----BEGIN CERTIFICATE-----
MIIDFzCCAf+gAwIBAgIUaoRV9NgoJd7Ize4T+IV8PjaKUqEwDQYJKoZIhvcNAQEL
BQAwGzEZMBcGA1UEAwwQc2s4cy50ZXN0Zml4dHVyZTAeFw0yNjA1MTUxMTEwMjZa
Fw0yNjA1MTYxMTEwMjZaMBsxGTAXBgNVBAMMEHNrOHMudGVzdGZpeHR1cmUwggEi
MA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCtbB+0TdwSOVQ9Vr2OyP0KO6pl
1tsamtSJHjTNmeq5tZqu0ykSIY6oZvhrLg3Ik68sbkGCjPAIByRY+yE1NS6MxWRD
yB4HRLxyckRSB8pifzUblDg+RsOLKf1dod2JsnQfPtCDIUoiGr1sEubbo9j4c9e7
Ht8KiDLCnvzY+oE2osecBf2wAeKcMGV3gruhDGfim3iNd3+80nzsc77NWEYyWmFh
VXQnWyY95DAnOgo6zR/6bysXYPTmYbpTD3EY3RCXRGhBfOGmJ/4NvSMX3OGJyDHz
OZHeTIINHNQtZfaxSrsoWX1vFzyHmzJy6SvKRaPsYSOVfuIHN3N9O7helqOpAgMB
AAGjUzBRMB0GA1UdDgQWBBSgRLaxZb8niqGEQ5vSzlQZffomAzAfBgNVHSMEGDAW
gBSgRLaxZb8niqGEQ5vSzlQZffomAzAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3
DQEBCwUAA4IBAQB+7CamYGEZG8UgJjA5Yp1VJf/iQRz6CwiNBQbNhpjUwrUJz3Xg
MyPAaDVhfHLM5JoHTDlSPxC08pZTeXBW26fIvPPX1YaBquCEJDCSsGbpvdLFkEYO
ilTXepSJ9cNe0StvLVFRXUe4G2LYLIHFk9Dzxdu1CpPN7me4QbtQbbZ2cbD/zVLX
ci2TbQKsGBNnTNEb6oVgYglUXMd5fkYTsdt4l6HriU5HwdH32vjIEnp7BORDmv6T
eQNMrSYWjQgRet2LXy7ByYlfYmjHzcY4KiXbkbE+L2ABVIUQhVN/W3NxuRIIEG/+
Hcs3tP3xwG+X2pGt/bYbkbn+x/kcPrHCSDXN
-----END CERTIFICATE-----`)
	fixtureTLSPrivateKeyPEM = []byte(`-----BEGIN PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCtbB+0TdwSOVQ9
Vr2OyP0KO6pl1tsamtSJHjTNmeq5tZqu0ykSIY6oZvhrLg3Ik68sbkGCjPAIByRY
+yE1NS6MxWRDyB4HRLxyckRSB8pifzUblDg+RsOLKf1dod2JsnQfPtCDIUoiGr1s
Eubbo9j4c9e7Ht8KiDLCnvzY+oE2osecBf2wAeKcMGV3gruhDGfim3iNd3+80nzs
c77NWEYyWmFhVXQnWyY95DAnOgo6zR/6bysXYPTmYbpTD3EY3RCXRGhBfOGmJ/4N
vSMX3OGJyDHzOZHeTIINHNQtZfaxSrsoWX1vFzyHmzJy6SvKRaPsYSOVfuIHN3N9
O7helqOpAgMBAAECggEAE99Cr8jHOcBfforgvEqcMk69ex94am9JAPBY6SFkxASD
FdrlByqYvAPWngOAOVZw+YSl1ZWcULMuz1JxjvUJ4UAiOeElzbvq4ytkWEkDwC8m
8QLWQg6eTCVS3uaUKfnstALg5lHLeqZ5Q7fTw+Hd1DSECTFjqgOK24HX4+4qnc2v
iwBW1G36hsonWQwPRzI9Hsf6MTdXSeSyVjRpRRXyG/zC9FkN0E6BG/GIyn+dtaEO
H1fRGSyLXgISzO9h5G10H6t147uA7yQlaxxk4CrxKfkH4KHRQy5LG6pXbslJEwqH
bcmTbqG9MXfD7llWs3yg/6FyOIyIUORR2PkxkjQPPQKBgQDUVn1+/y9drVo6ANF4
47OdGqcVXRa6XgearAk7Zpc4dHRriLc8J6JWZCET6MoE+LfJ5RapcbhmVeKBT9aW
j6Z4xG19TX1rEisDWk4zErbpKzX49dUnog+uYiT3pP6jdACfmoNMdF6tKIBry7Wx
7GfTNAXzoYktIVF3pWtp0luPTQKBgQDRFR0ex9bF+5I79LSkN4wABNfDGbDNpVt2
6s0zrQEVT9RJg5DOx4YbfMeK3JRnSjAAmaFW8UElfeUYgiei1R4h580oZFm1TAPY
oPJKopg72f4K8nFqPm83tBa3mf6gH+SIKvylD/yVBfI41sNbBaVHeApmwn8bZdRv
0idM7GzvzQKBgFxH4m6I9MrfhfDjXiYNv4eth6PPOwtvxhpAXhrEsT/FzLrXRdsM
1o55Ia8HYpTaivVhbIHjfGJtPO06B2aTs6OUqojkXndkA/GHE6k6nuei8efq3uJE
mlANM0e1Gz1qMsMqYZmekW7rxTQT6jkTJuQxHc0ODRHiAwfeiloJI+WZAoGAHEJ4
TyK/mr7oAwaOK+v+FjqRVyNvzDvfYvFVjviBPvotPUp1Fh3NuIVjCxfJTzStzEb3
kaLGJWUgw/FDnjSj//0us5jsrx55HpySYxga72wFdEFUpwGNUsAamfJMgiQNZYI5
562DfDjzhk8w1Gqs7j4BWeZL+84Fqp+DBFioWLkCgYBAf+ir8g4Nta+x2AOIkepw
NV+fzDDoU/elTwMvJCzOcGzy0GdwRwjViFTxLuCESkm/+dBPTYNCE1HlLB7vCDYq
f0eKjrGeXNOUt6dyQFGKv5jki4ZEEODdx2njP5XGMjBJd0K89Oz/SaELwEJKJUqJ
su3QrCoO4pvhXIae283/zw==
-----END PRIVATE KEY-----`)
)

func TestTLS_fixturePEMparsesAndMatchesSecretShape(t *testing.T) {
	t.Parallel()
	tlsCRT := fixtureCACertPEM
	caCRT := fixtureCACertPEM // self-signed fixture: chain of one

	for _, tt := range []struct {
		name string
		pem  []byte
	}{
		{"ca", caCRT},
		{"tls.crt", tlsCRT},
	} {
		block, _ := pem.Decode(tt.pem)
		if block == nil || block.Type != "CERTIFICATE" {
			t.Fatalf("%s: expected PEM CERTIFICATE block", tt.name)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			t.Fatalf("%s: parse certificate: %v", tt.name, err)
		}
	}
	keyBlock, _ := pem.Decode(fixtureTLSPrivateKeyPEM)
	if keyBlock == nil {
		t.Fatal("tls.key: nil PEM block")
	}
	if !strings.Contains(keyBlock.Type, "PRIVATE") {
		t.Fatalf("tls.key: want private key PEM, got type %q", keyBlock.Type)
	}

	sec := tlsMockSecret("fixture-tls", caCRT, tlsCRT, fixtureTLSPrivateKeyPEM)
	if sec.Type != corev1.SecretTypeTLS {
		t.Fatalf("type: got %s want %s", sec.Type, corev1.SecretTypeTLS)
	}
	for _, k := range []string{"ca.crt", "tls.crt", "tls.key"} {
		if len(sec.Data[k]) == 0 {
			t.Fatalf("Data[%s] empty", k)
		}
	}
}

func TestTLSMockSecret_create_viaFakeClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cli := fake.NewClientset()
	ns := "test-ns"

	sec := tlsMockSecret("ingress-tls", fixtureCACertPEM, fixtureCACertPEM, fixtureTLSPrivateKeyPEM)
	if _, err := cli.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := cli.CoreV1().Secrets(ns).Create(ctx, sec, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if got.Name != "ingress-tls" {
		t.Fatalf("name: got %q", got.Name)
	}
	if got.Type != corev1.SecretTypeTLS {
		t.Fatalf("type: got %s", got.Type)
	}
	if !bytes.Equal(got.Data["tls.crt"], fixtureCACertPEM) || !bytes.Equal(got.Data["tls.key"], fixtureTLSPrivateKeyPEM) {
		t.Fatal("persisted PEM data mismatch")
	}
}
