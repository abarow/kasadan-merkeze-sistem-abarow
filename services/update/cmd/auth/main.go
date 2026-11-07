// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command auth is the composition root of the authorization service of the registry
// mirror (Zot). It gives a short-lived token to the client pulling images (Podman,
// Docker, skopeo) with the Docker token protocol: the client presents the abarow token as the
// password, the service verifies the token offline with the publisher root and gives
// pull permission to the repositories by the token's update level.
//
// The tokens are signed with the service's ES256 key; Zot verifies them with the service's
// certificate (mirror.crt). The key and the certificate are generated in the directory at
// first start; Zot's container mounts the same directory read-only.
//
// Settings (environment variables):
//
//	ABAROW_UPDATE_ROOT_PUB       the publisher's root public key (base64url), required
//	ABAROW_UPDATE_REGISTRY_DIR   directory of mirror.key and mirror.crt (default /data)
//	ABAROW_UPDATE_REGISTRY_SERVICE  the bearer.service name in Zot's configuration (default abarow-zot)
//	ABAROW_UPDATE_AUTH_ADDR      token endpoint address (default :8083)
//	ABAROW_UPDATE_CI_SHA256      SHA-256 digest (hex) of the write token of the publisher's CI; if given the user ABAROW_UPDATE_CI_USER (default ci) can write to the test/, pilot/, prod/ paths with this token
//	ABAROW_TLS_CERT, ABAROW_TLS_KEY  the endpoint's TLS certificate (required; ABAROW_UPDATE_ALLOW_INSECURE=1 for testing)
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"abarow.com/pkg/service"
	"abarow.com/services/update/internal/mirror"
)

func main() {
	healthAddr := service.Env("ABAROW_UPDATE_HEALTH_ADDR", ":8081")
	service.Main("update-auth", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		pub, err := base64.RawURLEncoding.DecodeString(os.Getenv("ABAROW_UPDATE_ROOT_PUB"))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return errors.New("ABAROW_UPDATE_ROOT_PUB is required: the publisher's root public key (base64url; server generate-key prints it)")
		}
		key, err := loadOrCreateKey(service.Env("ABAROW_UPDATE_REGISTRY_DIR", "/data"))
		if err != nil {
			return err
		}
		var writer *mirror.Writer
		if h := os.Getenv("ABAROW_UPDATE_CI_SHA256"); h != "" {
			sum, err := hex.DecodeString(h)
			if err != nil || len(sum) != sha256.Size {
				return errors.New("ABAROW_UPDATE_CI_SHA256 must be 64 hex characters")
			}
			writer = &mirror.Writer{User: service.Env("ABAROW_UPDATE_CI_USER", "ci")}
			copy(writer.SHA256[:], sum)
		}
		svc, err := mirror.New(mirror.Config{Root: pub, Key: key, Service: service.Env("ABAROW_UPDATE_REGISTRY_SERVICE", "abarow-zot"), Writer: writer})
		if err != nil {
			return err
		}
		cert, tlsKey, err := service.TLSFromEnv()
		if err != nil {
			return err
		}
		if cert == "" && os.Getenv("ABAROW_UPDATE_ALLOW_INSECURE") != "1" {
			return errors.New("the token endpoint does not open without TLS: give ABAROW_TLS_CERT and ABAROW_TLS_KEY")
		}
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:     svc.Handler(),
			Addr:        service.Env("ABAROW_UPDATE_AUTH_ADDR", ":8083"),
			HealthAddr:  service.Env("ABAROW_UPDATE_HEALTH_ADDR", ":8081"),
			TLSCertFile: cert,
			TLSKeyFile:  tlsKey,
		})
	})
}

// loadOrCreateKey loads the mirror.key and mirror.crt files in the directory; if neither
// exists it generates and writes them. If only one exists it fails (half an install).
func loadOrCreateKey(dir string) (*mirror.Key, error) {
	keyFile, certFile := filepath.Join(dir, "mirror.key"), filepath.Join(dir, "mirror.crt")
	keyPEM, kerr := os.ReadFile(keyFile)
	certPEM, cerr := os.ReadFile(certFile)
	switch {
	case kerr == nil && cerr == nil:
		return mirror.ParseKey(keyPEM, certPEM)
	case errors.Is(kerr, fs.ErrNotExist) && errors.Is(cerr, fs.ErrNotExist):
		k, keyPEM, err := mirror.GenerateKey(time.Now())
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		// The certificate is public information (Zot's container reads it); only this service reads the key.
		if err := os.WriteFile(certFile, k.CertPEM, 0o644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
			return nil, err
		}
		return k, nil
	default:
		return nil, errors.New("mirror.key and mirror.crt must exist together (half an install)")
	}
}
