// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// selfSigned writes self-signed certificate and key files for 127.0.0.1.
func selfSigned(t *testing.T, dir string) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// When TLS is given the main server speaks only HTTPS; the request looks like TLS
// (cookies become Secure), the health server stays plain HTTP.
func TestServeHTTPTLS(t *testing.T) {
	certFile, keyFile, pool := selfSigned(t, t.TempDir())
	addr, healthAddr := freeAddr(t), freeAddr(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			http.Error(w, "TLS yok", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "merhaba")
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeHTTP(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPConfig{
			Handler: handler, Addr: addr, HealthAddr: healthAddr,
			TLSCertFile: certFile, TLSKeyFile: keyFile,
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}
	var body []byte
	for range 50 {
		resp, err := client.Get("https://" + addr + "/")
		if err == nil {
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(body) != "merhaba" {
		t.Fatalf("HTTPS response %q", body)
	}

	// Plain HTTP is not accepted on the main port.
	plain, err := http.Get("http://" + addr + "/")
	if err == nil {
		defer plain.Body.Close()
		if plain.StatusCode == http.StatusOK {
			t.Error("a plain HTTP request got 200")
		}
	}
	// The health check is plain HTTP.
	if err := Healthcheck(healthAddr); err != nil {
		t.Errorf("health check: %v", err)
	}
}

// A corrupt or missing certificate stops the service at start.
func TestServeHTTPBadTLSFiles(t *testing.T) {
	err := ServeHTTP(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPConfig{
		Handler: http.NotFoundHandler(), Addr: "127.0.0.1:0", HealthAddr: "127.0.0.1:0",
		TLSCertFile: filepath.Join(t.TempDir(), "yok.pem"), TLSKeyFile: filepath.Join(t.TempDir(), "yok.key"),
	})
	if err == nil {
		t.Fatal("a missing certificate file did not fail")
	}
}

func TestTLSFromEnv(t *testing.T) {
	t.Setenv("ABAROW_TLS_CERT", "")
	t.Setenv("ABAROW_TLS_KEY", "")
	if c, k, err := TLSFromEnv(); err != nil || c != "" || k != "" {
		t.Errorf("empty environment: %q %q %v", c, k, err)
	}
	t.Setenv("ABAROW_TLS_CERT", "/a.pem")
	if _, _, err := TLSFromEnv(); err == nil {
		t.Error("an error was expected when only a certificate is given")
	}
	t.Setenv("ABAROW_TLS_KEY", "/a.key")
	if c, k, err := TLSFromEnv(); err != nil || c != "/a.pem" || k != "/a.key" {
		t.Errorf("both filled: %q %q %v", c, k, err)
	}
}

// The additional servers open together with the main server; one can be plain, one HTTPS.
func TestServeHTTPExtraServers(t *testing.T) {
	certFile, keyFile, pool := selfSigned(t, t.TempDir())
	main, plain, secure, health := freeAddr(t), freeAddr(t), freeAddr(t), freeAddr(t)
	say := func(s string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, s) })
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeHTTP(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPConfig{
			Handler: say("ana"), Addr: main, HealthAddr: health,
			Extra: []ExtraServer{
				{Name: "admin", Addr: plain, Handler: say("admin")},
				{Name: "vekil", Addr: secure, Handler: say("vekil"), TLSCertFile: certFile, TLSKeyFile: keyFile},
			},
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	get := func(url string) string {
		for range 50 {
			resp, err := client.Get(url)
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return string(b)
			}
			time.Sleep(50 * time.Millisecond)
		}
		return ""
	}
	for url, want := range map[string]string{"http://" + main + "/": "ana", "http://" + plain + "/": "admin", "https://" + secure + "/": "vekil"} {
		if got := get(url); got != want {
			t.Errorf("%s: %q, want %q", url, got, want)
		}
	}
}

// The additional server's corrupt certificate stops the service at start.
func TestServeHTTPExtraBadTLS(t *testing.T) {
	err := ServeHTTP(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPConfig{
		Handler: http.NotFoundHandler(), Addr: "127.0.0.1:0", HealthAddr: "127.0.0.1:0",
		Extra: []ExtraServer{{Name: "vekil", Addr: "127.0.0.1:0", Handler: http.NotFoundHandler(), TLSCertFile: "/yok.pem", TLSKeyFile: "/yok.key"}},
	})
	if err == nil {
		t.Fatal("a corrupt extra certificate did not fail")
	}
}
