// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command server is the composition root of the update registration server: it takes the
// units' registration requests, waits for an authorized person's approval and gives approved
// units an image pull token. It runs in two places:
//
//   - At headquarters: approves store server and register registrations. It signs the tokens
//     with headquarters' signing key and the headquarters certificate given by the publisher
//     (both are in the headquarters agent's directory: signing.key and certificate.jwt).
//   - At the publisher: approves headquarters registrations, gives headquarters a certificate.
//     It signs the tokens with the publisher root key.
//
// Two separate APIs are served: the device API (open to the network, TLS) and the admin API (an
// internal port that only the screen service reaches; it takes the person from headers).
//
//	server generate-key <file>              generates the publisher root key, writes the public key
//	server verify-token <public-key> <token-file>   verifies the token with the root public key, prints its content
//	server sign-channel|countersign-channel|verify-channel|generate-policy|check-trust ...      signs, promotes and verifies the channel manifest (channel.go)
//
// Settings (environment variables):
//
//	ABAROW_DATABASE_URL            PostgreSQL address, required
//	ABAROW_UPDATE_ALLOWED          tiers to approve: "store,register" (default) or "headquarters"
//	ABAROW_UPDATE_ROOT_KEY_FILE    publisher root key (only on the server that approves the headquarters tier)
//	ABAROW_UPDATE_IDENTITY_DIR     headquarters agent's directory: signing.key, certificate.jwt (default /data)
//	ABAROW_UPDATE_CHANNEL_SOURCE   at the publisher: the address channel manifests are fetched from (https://.../<channel>.json; the promotion workflow's release page); if given ABAROW_UPDATE_TRUST_FILE is required
//	ABAROW_UPDATE_TRUST_FILE       trusted channel keys (JSON); the manifests fetched are verified with them
//	ABAROW_UPDATE_CHANNEL_SYNC_INTERVAL  polling interval of the source (default 5m)
//	ABAROW_UPDATE_CHANNEL_DIR      at the publisher: directory of the signed channel manifests (test.json, pilot.json, prod.json); at headquarters <identity dir>/channel
//	ABAROW_UPDATE_TOKEN_TTL        token lifetime (default 168h)
//	ABAROW_UPDATE_CERT_TTL         headquarters certificate lifetime (default 2160h = 90 days)
//	ABAROW_UPDATE_DEVICE_ADDR      device API address (default :8443)
//	ABAROW_UPDATE_ADMIN_ADDR       admin API address (default :8082)
//	ABAROW_TLS_CERT, ABAROW_TLS_KEY  the device API's TLS certificate (required; ABAROW_UPDATE_ALLOW_INSECURE=1 for testing)
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"abarow.com/pkg/service"
	"abarow.com/services/update/internal/adapters/in/httpapi"
	"abarow.com/services/update/internal/adapters/out/postgres"
	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/channelsync"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "generate-key" {
		if err := generateKey(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "server:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 3 && os.Args[1] == "verify-token" {
		if err := verifyToken(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, "server:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && isChannelCommand(os.Args[1]) {
		if err := channelCommand(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "server:", err)
			os.Exit(1)
		}
		return
	}
	healthAddr := service.Env("ABAROW_UPDATE_HEALTH_ADDR", ":8081")
	service.Main("update-server", healthAddr, run)
}

// generateKey writes the publisher root key to a file (only the owner can read it) and prints the public key.
func generateKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; it is not overwritten", path)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv.Seed())), 0o600); err != nil {
		return err
	}
	fmt.Println("genel anahtar (ajanlara ve aynalara sabitlenir):", base64.RawURLEncoding.EncodeToString(pub))
	return nil
}

// verifyToken verifies the token file with the publisher's public key and prints its content.
// The file is either the agent's token.json or just the token itself.
func verifyToken(pubB64, file string) error {
	pub, err := base64.RawURLEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("the public key is invalid")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	tok := strings.TrimSpace(string(raw))
	if strings.HasPrefix(tok, "{") {
		var s struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		tok = s.Token
	}
	c, err := token.Verify(ed25519.PublicKey(pub), tok, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("valid: device=%s tier=%s level=%s store=%s register=%s auth=%v issuer=%s expires=%s\n",
		c.Device, c.Tier, c.Level, c.Store, c.Register, c.Scopes, c.Issuer, c.Expiry.Format(time.RFC3339))
	return nil
}

func readSeed(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s is corrupt: a 32-byte seed (hex) was expected", path)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func duration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s is not a valid duration: %q", key, v)
	}
	return d, nil
}

// fileIssuer signs the tokens at headquarters with the key in the headquarters agent's directory;
// it reads the certificate from the file every time (the server does not restart when the agent renews it).
type fileIssuer struct{ dir string }

func (f fileIssuer) load() (registration.SigningIssuer, error) {
	key, err := readSeed(filepath.Join(f.dir, "signing.key"))
	if err != nil {
		return registration.SigningIssuer{}, err
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, "certificate.jwt"))
	if err != nil {
		return registration.SigningIssuer{}, errors.New("headquarters has not got a certificate from the publisher yet (the headquarters registration may not be approved)")
	}
	cert := strings.TrimSpace(string(raw))
	id, err := token.CertSubject(cert)
	if err != nil {
		return registration.SigningIssuer{}, err
	}
	return registration.SigningIssuer{Key: key, ID: id, Chain: cert}, nil
}

func (f fileIssuer) Issue(c token.Claims, now time.Time) (string, error) {
	i, err := f.load()
	if err != nil {
		return "", err
	}
	return i.Issue(c, now)
}

func (f fileIssuer) Certify(in token.CertInput) (string, error) {
	return "", errors.New("only the publisher root can give the certificate")
}

func run(ctx context.Context, log *slog.Logger) error {
	dsn := os.Getenv("ABAROW_DATABASE_URL")
	if dsn == "" {
		return errors.New("ABAROW_DATABASE_URL is required: the PostgreSQL address (postgres://user:password@server/database)")
	}
	var allowed []token.Tier
	for _, t := range strings.Split(service.Env("ABAROW_UPDATE_ALLOWED", "store,register"), ",") {
		switch tier := token.Tier(strings.TrimSpace(t)); tier {
		case token.Register, token.Store, token.Headquarters:
			allowed = append(allowed, tier)
		default:
			return fmt.Errorf("ABAROW_UPDATE_ALLOWED invalid tier: %q", t)
		}
	}
	ttl, err := duration("ABAROW_UPDATE_TOKEN_TTL", 168*time.Hour)
	if err != nil {
		return err
	}
	certTTL, err := duration("ABAROW_UPDATE_CERT_TTL", 90*24*time.Hour)
	if err != nil {
		return err
	}

	// Who signs the token: the root key at the publisher, the agent's key and certificate at headquarters.
	var issuer registration.Issuer
	channelDir := os.Getenv("ABAROW_UPDATE_CHANNEL_DIR")
	if rootFile := os.Getenv("ABAROW_UPDATE_ROOT_KEY_FILE"); rootFile != "" {
		root, err := readSeed(rootFile)
		if err != nil {
			return err
		}
		issuer = registration.SigningIssuer{Key: root, ID: token.PublisherIssuer}
		log.Info("publisher mode: tokens are signed with the root key")
	} else {
		dir := service.Env("ABAROW_UPDATE_IDENTITY_DIR", "/data")
		if channelDir == "" {
			channelDir = filepath.Join(dir, "channel") // manifests verified by the headquarters agent
		}
		fi := fileIssuer{dir: dir}
		if _, err := readSeed(filepath.Join(dir, "signing.key")); err != nil {
			return fmt.Errorf("could not read headquarters' signing key (the headquarters agent must start first): %w", err)
		}
		issuer = fi
		log.Info("headquarters mode: tokens are signed with the headquarters key and the publisher certificate")
	}

	tlsCert, tlsKey, err := service.TLSFromEnv()
	if err != nil {
		return err
	}
	if tlsCert == "" && os.Getenv("ABAROW_UPDATE_ALLOW_INSECURE") != "1" {
		return errors.New("the device API does not open without TLS: give ABAROW_TLS_CERT and ABAROW_TLS_KEY")
	}

	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	svc, err := registration.New(registration.Config{Store: store, Issuer: issuer, Allowed: allowed, TTL: ttl, CertTTL: certTTL})
	if err != nil {
		return err
	}
	var deviceOpts []httpapi.DeviceOption
	if channelDir != "" {
		deviceOpts = append(deviceOpts, httpapi.WithChannels(channelDir))
	}
	// At the publisher the channel directory is fed with the manifests uploaded by the promotion workflow; each manifest
	// is verified with the trusted keys, the source is not trusted.
	if source := os.Getenv("ABAROW_UPDATE_CHANNEL_SOURCE"); source != "" {
		if os.Getenv("ABAROW_UPDATE_ROOT_KEY_FILE") == "" || channelDir == "" {
			return errors.New("ABAROW_UPDATE_CHANNEL_SOURCE is used only in publisher mode (ABAROW_UPDATE_ROOT_KEY_FILE and ABAROW_UPDATE_CHANNEL_DIR)")
		}
		raw, err := os.ReadFile(os.Getenv("ABAROW_UPDATE_TRUST_FILE"))
		if err != nil {
			return fmt.Errorf("could not read ABAROW_UPDATE_TRUST_FILE: %w", err)
		}
		trust, err := channel.ParseTrust(raw)
		if err != nil {
			return err
		}
		interval, err := duration("ABAROW_UPDATE_CHANNEL_SYNC_INTERVAL", 5*time.Minute)
		if err != nil {
			return err
		}
		syncer := channelsync.Syncer{BaseURL: source, Dir: channelDir, Trust: trust, TrustFn: func(now time.Time) (channel.Trust, error) {
			raw, err := os.ReadFile(os.Getenv("ABAROW_UPDATE_TRUST_FILE"))
			if err != nil {
				return nil, err
			}
			return channel.ParseTrustAt(raw, now)
		}, AllowInsecure: os.Getenv("ABAROW_UPDATE_ALLOW_INSECURE") == "1", Log: log}
		go syncer.Run(ctx, interval)
	}

	// The admin API is on a separate, internal port; it cannot be on the same mux as the device API
	// (only the screen service may put the header).
	return service.ServeHTTP(ctx, log, service.HTTPConfig{
		Handler:     httpapi.NewDevice(svc, deviceOpts...),
		Addr:        service.Env("ABAROW_UPDATE_DEVICE_ADDR", ":8443"),
		HealthAddr:  service.Env("ABAROW_UPDATE_HEALTH_ADDR", ":8081"),
		Health:      store.Ping,
		TLSCertFile: tlsCert,
		TLSKeyFile:  tlsKey,
		Extra:       []service.ExtraServer{{Name: "admin", Addr: service.Env("ABAROW_UPDATE_ADMIN_ADDR", ":8082"), Handler: httpapi.NewAdmin(svc)}},
	})
}
