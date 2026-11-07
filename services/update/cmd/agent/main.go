// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command agent is the composition root of the update agent of a unit (register, store server,
// headquarters): it generates the device identity, registers with the registration server of the
// tier above, and after approval gets the image pull token and renews it. The unit's
// screen reads the status and the approval code from the local API (GET /api/update/status).
//
// Settings (environment variables):
//
//	ABAROW_UPDATE_SERVER_URL      the registration server's device API address (https://...), required
//	ABAROW_UPDATE_CA_FILE         the CA that signed the server certificate (PEM); the system roots if absent
//	ABAROW_UPDATE_TIER            register, store or headquarters, required
//	ABAROW_UPDATE_LEVEL           update level: test, pilot, prod (default prod)
//	ABAROW_UPDATE_NAME            the name shown on the screen
//	ABAROW_STORE_ID, ABAROW_REGISTER_ID   store and register id
//	ABAROW_UPDATE_DIR             directory of the identity and the token (default /data)
//	ABAROW_UPDATE_HOST_ROOT       the root where the host's files are mounted for the hardware fingerprint (default /host)
//	ABAROW_UPDATE_STATUS_ADDR     local status API address (default :8080)
//	ABAROW_UPDATE_ALLOW_INSECURE  if "1" plain HTTP is accepted (testing only)
//	ABAROW_UPDATE_RELAY_ADDR      store server only: the TLS proxy address for the registers (e.g. :8443); ABAROW_TLS_CERT and ABAROW_TLS_KEY are required
//
// New release flow (optional; opens if ABAROW_UPDATE_TRUST_FILE is given):
//
//	ABAROW_UPDATE_TRUST_FILE      trusted channel keys (JSON: {"build":[...],"pilot":[...],"prod":[...]}, base64url public keys)
//	ABAROW_UPDATE_RUNTIME         podman (default) or dryrun (does not touch real containers; testing only)
//	ABAROW_UPDATE_PODMAN_SOCKET   Podman API socket (default /run/podman/podman.sock)
//	ABAROW_UPDATE_DBUS_SOCKET     user D-Bus socket (default /run/dbus/bus)
//	ABAROW_UPDATE_UNITS_DIR       directory of the Quadlet units (default /units)
//	ABAROW_UPDATE_OWN_UNIT        the agent's own unit (changed last; default abarow-update)
//	ABAROW_UPDATE_POLL_INTERVAL   channel polling interval (default 10m)
//	ABAROW_UPDATE_HEALTH_TIMEOUT  how long to wait for the new image to come up healthy; rolled back if exceeded (default 3m)
//	ABAROW_UPDATE_REPORT_INTERVAL how often the running version state is reported to the tier above again (default 5m; immediately on change)
//	ABAROW_UPDATE_PREDOWNLOAD     if "0" pre-download is off (default on)
//	ABAROW_UPDATE_GATE_URL        the safe moment question (gateway at the register); if empty every moment is safe
//	ABAROW_UPDATE_APPROVE_SECRET  secret key shared with the screen service (only it can make approval requests)
//	ABAROW_UPDATE_POLICY_DIR      Podman configuration directory (~/.config/containers on the host); if given the agent writes the image signature check (policy.json) here; the trust file needs "cosign" keys and ABAROW_UPDATE_REGISTRY
//	ABAROW_UPDATE_REGISTRY        the mirror images are pulled from (address:port; its own mirror or the tier above's mirror); if empty the registry in the manifest
//	ABAROW_UPDATE_MIRROR_DIR      directory shared with the local image mirror (Zot): the credentials file is written when the token is renewed
//	ABAROW_UPDATE_MIRROR_UPSTREAM address:port of the tier above's mirror (the key of the credentials file)
//	ABAROW_UPDATE_DRYRUN_UNITS    dry-run runtime: unit=repo@sha256:... pairs; ABAROW_UPDATE_DRYRUN_BAD: unhealthy digests
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"abarow.com/pkg/service"
	"abarow.com/services/update/internal/adapters/in/httpapi"
	"abarow.com/services/update/internal/adapters/in/httprelay"
	"abarow.com/services/update/internal/adapters/out/dryrun"
	"abarow.com/services/update/internal/adapters/out/httpclient"
	"abarow.com/services/update/internal/adapters/out/httpgate"
	"abarow.com/services/update/internal/adapters/out/podman"
	"abarow.com/services/update/internal/agent"
	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/feed"
	"abarow.com/services/update/internal/policy"
	"abarow.com/services/update/internal/release"
	"abarow.com/services/update/internal/token"
)

func main() {
	healthAddr := service.Env("ABAROW_UPDATE_HEALTH_ADDR", ":8081")
	service.Main("update-agent", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		url := os.Getenv("ABAROW_UPDATE_SERVER_URL")
		if url == "" {
			return errors.New("ABAROW_UPDATE_SERVER_URL is required: the registration server's device API address (https://...)")
		}
		var ca []byte
		if f := os.Getenv("ABAROW_UPDATE_CA_FILE"); f != "" {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			ca = b
		}
		opts := httpclient.Options{BaseURL: url, CAPEM: ca, AllowInsecure: os.Getenv("ABAROW_UPDATE_ALLOW_INSECURE") == "1"}
		client, err := httpclient.New(opts)
		if err != nil {
			return err
		}
		tier := token.Tier(os.Getenv("ABAROW_UPDATE_TIER"))
		switch tier {
		case token.Register, token.Store, token.Headquarters:
		default:
			return errors.New("ABAROW_UPDATE_TIER must be register, store or headquarters")
		}
		ag, err := agent.New(agent.Config{
			Dir:       service.Env("ABAROW_UPDATE_DIR", "/data"),
			Root:      os.DirFS(service.Env("ABAROW_UPDATE_HOST_ROOT", "/host")),
			Tier:      tier,
			Level:     token.Level(service.Env("ABAROW_UPDATE_LEVEL", string(token.Prod))),
			Store:     os.Getenv("ABAROW_STORE_ID"),
			Register:  os.Getenv("ABAROW_REGISTER_ID"),
			Name:      os.Getenv("ABAROW_UPDATE_NAME"),
			Transport: client,
			Log:       log,
			OnToken:   mirrorFeed(),
		})
		if err != nil {
			return err
		}
		// New release flow: the signed channel manifest is polled, announced, applied with approval.
		var agentOpts []httpapi.AgentOption
		if trustFile := os.Getenv("ABAROW_UPDATE_TRUST_FILE"); trustFile != "" {
			updater, err := newUpdater(log, ag, client, trustFile)
			if err != nil {
				return err
			}
			// The running version state is reported to the tier above (shown on the headquarters screen); immediately when the state changes.
			every, err := time.ParseDuration(service.Env("ABAROW_UPDATE_REPORT_INTERVAL", "5m"))
			if err != nil || every < time.Second {
				return errors.New("ABAROW_UPDATE_REPORT_INTERVAL is not a valid duration")
			}
			ag.SetReporter(updater.Report, every)
			interval, err := time.ParseDuration(service.Env("ABAROW_UPDATE_POLL_INTERVAL", "10m"))
			if err != nil || interval < time.Second {
				return errors.New("ABAROW_UPDATE_POLL_INTERVAL is not a valid duration")
			}
			go updater.Run(ctx, interval)
			agentOpts = append(agentOpts, httpapi.WithUpdates(updater, os.Getenv("ABAROW_UPDATE_APPROVE_SECRET")))
		}

		go ag.Run(ctx)

		// The store server offers a proxy so that the registers can reach headquarters.
		var extra []service.ExtraServer
		if addr := os.Getenv("ABAROW_UPDATE_RELAY_ADDR"); addr != "" {
			if tier != token.Store {
				return errors.New("ABAROW_UPDATE_RELAY_ADDR is used only at the store tier")
			}
			cert, key, err := service.TLSFromEnv()
			if err != nil {
				return err
			}
			if cert == "" && !opts.AllowInsecure {
				return errors.New("the proxy does not open without TLS: give ABAROW_TLS_CERT and ABAROW_TLS_KEY")
			}
			tr, base, err := httpclient.NewTransport(opts)
			if err != nil {
				return err
			}
			relay, err := httprelay.New(httprelay.Options{Upstream: base, Transport: tr})
			if err != nil {
				return err
			}
			extra = append(extra, service.ExtraServer{Name: "vekil", Addr: addr, Handler: relay, TLSCertFile: cert, TLSKeyFile: key})
		}
		st := ag.Status()
		log.Info("update agent", "device", st.Device, "tier", st.Tier, "level", st.Level, "status", st.State)

		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.NewAgent(ag, agentOpts...),
			Addr:       service.Env("ABAROW_UPDATE_STATUS_ADDR", ":8080"),
			HealthAddr: healthAddr,
			Extra:      extra,
			// A copied disk disables the agent; the container looks unhealthy.
			Health: func(context.Context) error {
				if ag.Status().State == agent.StateIdentityMismatch {
					return errors.New("the device identity does not match this machine")
				}
				return nil
			},
		})
	})
}

// newUpdater builds the updater of the new release flow from the environment settings.
func newUpdater(log *slog.Logger, ag *agent.Agent, source release.Source, trustFile string) (*release.Updater, error) {
	level := token.Level(service.Env("ABAROW_UPDATE_LEVEL", string(token.Prod)))
	polDir := os.Getenv("ABAROW_UPDATE_POLICY_DIR")
	mirror := os.Getenv("ABAROW_UPDATE_REGISTRY")
	if polDir != "" && mirror == "" {
		return nil, errors.New("ABAROW_UPDATE_REGISTRY (the mirror address) is required for ABAROW_UPDATE_POLICY_DIR")
	}
	// load reads the trust file as of the moment now: keys outside their validity window drop out.
	// The image signature check (policy.json) is produced from the same keys; if the level has no
	// valid cosign key left every pull is rejected (it does not fall back to an unsigned pull).
	var lastWarn time.Time
	load := func(now time.Time) (channel.Trust, error) {
		raw, err := os.ReadFile(trustFile)
		if err != nil {
			return nil, err
		}
		trust, err := channel.ParseTrustAt(raw, now)
		if err != nil {
			return nil, err
		}
		if infos, err := channel.Inspect(raw, now); err == nil && now.Sub(lastWarn) >= 24*time.Hour {
			for _, k := range infos {
				if k.State == channel.KeyValid && !k.NotAfter.IsZero() && k.NotAfter.Sub(now) <= 14*24*time.Hour {
					lastWarn = now
					log.Warn("the trust key is about to expire", "kind", k.Kind, "role", k.Role, "key", k.ID, "not_after", k.NotAfter)
				}
			}
		}
		if polDir == "" {
			return trust, nil
		}
		keys, err := policy.ParseKeys(raw, now)
		if err != nil {
			return nil, err
		}
		files, err := policy.Generate(level, mirror, keys)
		if errors.Is(err, policy.ErrNoKey) {
			log.Error("the level has no valid cosign key; image pulls are locked", "level", level, "err", err)
			files, err = policy.Locked(mirror)
		}
		if err != nil {
			return nil, err
		}
		changed, err := policy.Install(polDir, files)
		if err != nil {
			return nil, fmt.Errorf("could not write the image signature policy: %w", err)
		}
		if changed {
			log.Info("image signature policy updated", "level", level, "registry", mirror)
		}
		return trust, nil
	}
	// At startup the trust file must be flawless; if an error comes later the keys in hand are kept.
	trust, err := load(time.Now())
	if err != nil {
		return nil, err
	}
	if polDir != "" {
		log.Info("image signature check is on", "level", level, "registry", mirror)
	}
	dir := service.Env("ABAROW_UPDATE_DIR", "/data")
	var rt release.Runtime
	switch kind := service.Env("ABAROW_UPDATE_RUNTIME", "podman"); kind {
	case "podman":
		sd, err := podman.ConnectDBus(service.Env("ABAROW_UPDATE_DBUS_SOCKET", "/run/dbus/bus"))
		if err != nil {
			return nil, fmt.Errorf("could not connect to D-Bus: %w", err)
		}
		rt, err = podman.New(podman.Options{
			Socket:   service.Env("ABAROW_UPDATE_PODMAN_SOCKET", "/run/podman/podman.sock"),
			UnitsDir: service.Env("ABAROW_UPDATE_UNITS_DIR", "/units"),
			Systemd:  sd,
			Token:    ag.Token,
			Mirror:   os.Getenv("ABAROW_UPDATE_REGISTRY"),
			Channel:  channel.ChannelOf(token.Level(service.Env("ABAROW_UPDATE_LEVEL", string(token.Prod)))),
		})
		if err != nil {
			return nil, err
		}
	case "dryrun":
		d, err := dryrun.New(filepath.Join(dir, "test.json"), os.Getenv("ABAROW_UPDATE_DRYRUN_UNITS"), os.Getenv("ABAROW_UPDATE_DRYRUN_BAD"))
		if err != nil {
			return nil, err
		}
		if v := os.Getenv("ABAROW_UPDATE_DRYRUN_PULL"); v != "" {
			if d.PullDelay, err = time.ParseDuration(v); err != nil {
				return nil, errors.New("ABAROW_UPDATE_DRYRUN_PULL is not a valid duration")
			}
		}
		log.Warn("update runtime: dry run (real containers are not touched)")
		rt = d
	default:
		return nil, fmt.Errorf("ABAROW_UPDATE_RUNTIME %q is invalid (podman or dryrun)", kind)
	}
	healthTimeout, err := time.ParseDuration(service.Env("ABAROW_UPDATE_HEALTH_TIMEOUT", "3m"))
	if err != nil || healthTimeout < time.Second {
		return nil, errors.New("ABAROW_UPDATE_HEALTH_TIMEOUT is not a valid duration")
	}
	cfg := release.Config{
		HealthTimeout: healthTimeout,
		Dir:           dir, Level: token.Level(service.Env("ABAROW_UPDATE_LEVEL", string(token.Prod))), Trust: trust, Refresh: load,
		Source: source, Runtime: rt, Predownload: os.Getenv("ABAROW_UPDATE_PREDOWNLOAD") != "0",
		OwnUnit: service.Env("ABAROW_UPDATE_OWN_UNIT", "abarow-update"), Log: log, OnChange: ag.Nudge,
		// Without a token (before the registration is approved) no image can be pulled; no polling is done either.
		Ready: func() bool { return ag.Status().State == agent.StateApproved },
	}
	if url := os.Getenv("ABAROW_UPDATE_GATE_URL"); url != "" {
		cfg.Gate = httpgate.Gate{URL: url, Secret: os.Getenv("ABAROW_UPDATE_APPROVE_SECRET")}
	}
	return release.New(cfg)
}

// mirrorFeed writes the token to the shared directory so that the local mirror can
// pull from the tier above; it returns nil if no mirror is installed.
func mirrorFeed() func(string, time.Time) error {
	dir, upstream := os.Getenv("ABAROW_UPDATE_MIRROR_DIR"), os.Getenv("ABAROW_UPDATE_MIRROR_UPSTREAM")
	if dir == "" || upstream == "" {
		return nil
	}
	return feed.Writer{Dir: dir, Upstream: upstream}.OnToken
}
