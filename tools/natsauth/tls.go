// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Leaf TLS files (under <out>/tls). ca.pem is public and is given to the
// registers; ca.key does not leave the store server.
const (
	caCertFile     = "ca.pem"
	caKeyFile      = "ca.key"
	serverCertFile = "server.pem"
	serverKeyFile  = "server.key"
)

const (
	caValidity     = 20 * 365 * 24 * time.Hour
	serverValidity = 2 * 365 * 24 * time.Hour
	// renewBefore: the server certificate is renewed when this much time is left.
	renewBefore = 60 * 24 * time.Hour
)

// ensureLeafTLS prepares the TLS files for the store server's leaf connections: a
// certificate authority (CA) specific to the store and the server certificate it
// signed.
//
// The CA is produced once and kept; the registers get the CA certificate at install
// and verify the store server with it. If the CA changes it must be redistributed to
// all the registers. The server certificate is regenerated when the addresses the registers connect to
// (hosts) change or when it is about to expire;
// the registers are not affected by this.
func ensureLeafTLS(dir string, hosts []string, storeID string, now time.Time) error {
	if len(hosts) == 0 {
		return errors.New("-leaf-hosts is required for a store server: the name or IP the registers will connect to (e.g. store,192.168.1.10)")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ca, caKey, err := ensureCA(dir, storeID, now)
	if err != nil {
		return fmt.Errorf("CA: %w", err)
	}
	if current, err := readCert(filepath.Join(dir, serverCertFile)); err == nil &&
		current.CheckSignatureFrom(ca) == nil && now.Add(renewBefore).Before(current.NotAfter) && sameHosts(current, hosts) &&
		slices.Contains(current.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		if _, err := os.Stat(filepath.Join(dir, serverKeyFile)); err == nil {
			return nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "abarow operasyon " + storeID},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		// Client use too: at headquarters on three machines the same certificate
		// is shown when connecting to the other machines (cluster routes).
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	// Key first, then certificate: if interrupted the certificate stays old and
	// is regenerated at the next run.
	if err := writeKey(filepath.Join(dir, serverKeyFile), key, 0o444); err != nil {
		return err
	}
	return writePEM(filepath.Join(dir, serverCertFile), "CERTIFICATE", der, 0o444)
}

// ensureCA reads the CA from the file; produces it if missing.
func ensureCA(dir, storeID string, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, keyPath := filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile)
	cert, certErr := readCert(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		key, err := parseKey(keyPEM)
		if err != nil {
			return nil, nil, fmt.Errorf("%s bozuk: %w", keyPath, err)
		}
		if !key.PublicKey.Equal(cert.PublicKey) {
			return nil, nil, fmt.Errorf("%s and %s do not match", certPath, keyPath)
		}
		return cert, key, nil
	case errors.Is(certErr, fs.ErrNotExist) && errors.Is(keyErr, fs.ErrNotExist):
	default:
		// If one exists and the other does not, a new CA is not produced: the CA at the registers
		// would become invalid.
		return nil, nil, fmt.Errorf("the CA files are missing or corrupt (%v; %v); the CA distributed to the registers must be kept", certErr, keyErr)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "abarow store CA" + storeID},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	// Only natsauth (root) reads the CA's private key; NATS does not need it.
	if err := writeKey(keyPath, key, 0o400); err != nil {
		return nil, nil, err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o444); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// sameHosts says the certificate is exactly for these addresses.
func sameHosts(cert *x509.Certificate, hosts []string) bool {
	var have []string
	have = append(have, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		have = append(have, ip.String())
	}
	want := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			h = ip.String()
		}
		want = append(want, h)
	}
	slices.Sort(have)
	slices.Sort(want)
	return slices.Equal(have, slices.Compact(want))
}

// copyLeafCA verifies the store server's CA certificate at the register and copies it to the
// output folder.
func copyLeafCA(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("could not read the store server CA certificate: %w", err)
	}
	cert, err := parseCert(data)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if !cert.IsCA {
		return fmt.Errorf("%s is not a CA certificate (the store server's ca/%s should be given)", src, caCertFile)
	}
	if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(dst, data, 0o444)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err)
	}
	return n
}

func readCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCert(data)
}

func parseCert(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || !strings.HasSuffix(block.Type, "PRIVATE KEY") {
		return nil, errors.New("not a PEM key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	return ec, nil
}

func writeKey(path string, key *ecdsa.PrivateKey, mode fs.FileMode) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(path, "PRIVATE KEY", der, mode)
}

// writePEM rewrites the file; since a read-only file cannot be overwritten it
// is deleted first.
func writePEM(path, typ string, der []byte, mode fs.FileMode) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode)
}
