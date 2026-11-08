// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package podman implements release.Runtime on rootless Podman and systemd --user.
// The agent runs inside a container and uses two sockets: the user's Podman API
// socket (to pull images, to read the running container) and the user D-Bus socket
// (to restart a unit, daemon-reload). The Quadlet units and the drop-ins are in a
// directory mounted as plain files.
//
// A unit's image is pinned to a digest with the drop-in
// "<unit>.container.d/zz-release.conf" next to the unit; the actual .container file is
// not touched. Only units with the "abarow-" prefix and a .container file in the
// directory are touched (an allow list enforced in code).
//
// This adapter has been tested against the Podman API (imitated with httptest); it
// has not been run on real systemd and Podman yet.
package podman

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"abarow.com/services/update/internal/channel"
)

// DropIn is the name of the drop-in file that pins the image.
const DropIn = "zz-release.conf"

// Systemd is the port that manages units (the D-Bus adapter is in systemd.go).
type Systemd interface {
	// Reload makes systemd re-read the unit files (runs the Quadlet generator).
	Reload(ctx context.Context) error
	// Restart restarts the unit and waits until the job is done.
	Restart(ctx context.Context, unit string) error
	// Active reports whether the unit is running healthy (since Notify=healthy,
	// "active" means healthy).
	Active(ctx context.Context, unit string) (bool, error)
}

// Options are the adapter's settings.
type Options struct {
	// Socket is the path of the Podman API socket; ignored if BaseURL is given (test).
	Socket  string
	BaseURL string
	// UnitsDir is the directory where the Quadlet units (~/.config/containers/systemd) are mounted.
	UnitsDir string
	Systemd  Systemd
	// Token is the abarow image pull token for the registry.
	Token func() (string, bool)
	// Mirror is the "address:port" of the tier above's image mirror. If given, images are pulled
	// not from the registry in the manifest but from this mirror, under the name
	// "<mirror>/<channel>/<image>@digest" (the mirror's authorization is given by the channel prefix). If empty the repo in the manifest is used.
	Mirror string
	// Channel is the channel prefix on the mirror (test, pilot, prod); required when Mirror is given.
	Channel string
	// Client is the HTTP client for tests.
	Client *http.Client
}

// Runtime is the Podman implementation of release.Runtime.
type Runtime struct {
	o    Options
	http *http.Client
	base string
}

var (
	unitRe  = regexp.MustCompile(`^abarow-[a-z0-9][a-z0-9-]*$`)
	imageRe = regexp.MustCompile(`(?m)^\s*Image\s*=\s*(\S+)\s*$`)
	nameRe  = regexp.MustCompile(`(?m)^\s*ContainerName\s*=\s*(\S+)\s*$`)
)

// New builds the adapter.
func New(o Options) (*Runtime, error) {
	if o.UnitsDir == "" || o.Systemd == nil || o.Token == nil {
		return nil, errors.New("podman: ayarlar eksik")
	}
	if o.Mirror != "" && o.Channel == "" {
		return nil, errors.New("podman: ayna için kanal gerekli")
	}
	r := &Runtime{o: o, http: o.Client, base: strings.TrimRight(o.BaseURL, "/")}
	if r.http == nil && o.BaseURL != "" {
		// Podman API over TCP (for tests and remote use)
		r.http = &http.Client{Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
			ResponseHeaderTimeout: headerTimeout,
		}}
	}
	if r.http == nil {
		if o.Socket == "" {
			return nil, errors.New("podman: soket yolu gerekli")
		}
		r.http = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "unix", o.Socket)
			},
			ResponseHeaderTimeout: headerTimeout,
		}}
		r.base = "http://podman"
	}
	return r, nil
}

// Limits of the calls to the Podman API: a hung API must not hold the update forever.
// The pull streams its progress, so its headers come at once; the whole pull may take long
// over a slow store link.
const (
	dialTimeout   = 5 * time.Second
	headerTimeout = time.Minute
	callTimeout   = 30 * time.Second
	pullTimeout   = 30 * time.Minute
)

// units returns the allowed units in the directory (unit name → content of the .container file).
func (r *Runtime) units() (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(r.o.UnitsDir, "abarow-*.container"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range files {
		unit := strings.TrimSuffix(filepath.Base(f), ".container")
		if !unitRe.MatchString(unit) {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		out[unit] = string(raw)
	}
	return out, nil
}

// pinned is the image the unit is pinned to by a drop-in ("" if none).
func (r *Runtime) pinned(unit string) string {
	raw, err := os.ReadFile(filepath.Join(r.o.UnitsDir, unit+".container.d", DropIn))
	if err != nil {
		return ""
	}
	if m := imageRe.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

// ref is the name the image is pulled under on this machine and pinned to the unit: the path
// on the mirror if there is one, otherwise the repo in the manifest.
func (r *Runtime) ref(img channel.Image) string {
	if r.o.Mirror == "" {
		return img.Ref()
	}
	return r.o.Mirror + "/" + r.o.Channel + "/" + path.Base(img.Repo) + "@" + img.Digest
}

// repoOf separates the repo from a "repo:tag" or "repo@sha256:..." name.
func repoOf(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

func (r *Runtime) allowed(unit string) error {
	if !unitRe.MatchString(unit) {
		return fmt.Errorf("birim adı izinli değil: %q", unit)
	}
	if _, err := os.Stat(filepath.Join(r.o.UnitsDir, unit+".container")); err != nil {
		return fmt.Errorf("%s bu makinede kurulu değil", unit)
	}
	return nil
}

// Current returns the running images of the installed units. The running container's
// digest is read from Podman; if the container is not running the pinned digest is used.
func (r *Runtime) Current(ctx context.Context) (map[string]channel.Image, error) {
	units, err := r.units()
	if err != nil {
		return nil, err
	}
	out := map[string]channel.Image{}
	for unit, body := range units {
		ref := r.pinned(unit)
		if ref == "" {
			if m := imageRe.FindStringSubmatch(body); m != nil {
				ref = m[1]
			}
		}
		if ref == "" {
			continue
		}
		name := unit // If ContainerName is not given in Quadlet it is systemd-<unit>; all our units give it.
		if m := nameRe.FindStringSubmatch(body); m != nil {
			name = m[1]
		}
		digest, err := r.containerDigest(ctx, name)
		if err != nil {
			// Not running: use the pinned digest if there is one, otherwise count it as unknown.
			digest = ""
			if i := strings.Index(ref, "@"); i >= 0 {
				digest = ref[i+1:]
			}
		}
		out[unit] = channel.Image{Unit: unit, Repo: repoOf(ref), Digest: digest}
	}
	return out, nil
}

func (r *Runtime) containerDigest(ctx context.Context, name string) (string, error) {
	var info struct {
		ImageDigest string `json:"ImageDigest"`
		State       struct {
			Running bool `json:"Running"`
		} `json:"State"`
	}
	if err := r.get(ctx, "/v5.0.0/libpod/containers/"+url.PathEscape(name)+"/json", &info); err != nil {
		return "", err
	}
	if !info.State.Running || info.ImageDigest == "" {
		return "", errors.New("container çalışmıyor")
	}
	return info.ImageDigest, nil
}

// Pull pulls the image by digest. Podman verifies the digest: if the content does not match the digest the pull fails.
func (r *Runtime) Pull(ctx context.Context, img channel.Image) error {
	if err := r.allowed(img.Unit); err != nil {
		return err
	}
	token, ok := r.o.Token()
	if !ok {
		return errors.New("imaj çekme jetonu yok ya da süresi dolmuş")
	}
	ref := r.ref(img)
	host := ref
	if i := strings.Index(host, "/"); i > 0 {
		host = host[:i]
	}
	auth, _ := json.Marshal(map[string]string{"username": "abarow", "password": token, "serveraddress": host})
	q := url.Values{"reference": {ref}, "quiet": {"false"}}
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/v5.0.0/libpod/images/pull?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString(auth))
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("podman pull %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	// The response is a line-by-line JSON stream; an error line carries "error".
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var line struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Error != "" {
			return errors.New("podman pull: " + line.Error)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// The digest of the pulled image must be the same as the expected one.
	var inspect struct {
		Digest string `json:"Digest"`
	}
	if err := r.get(ctx, "/v5.0.0/libpod/images/"+url.PathEscape(ref)+"/json", &inspect); err != nil {
		return fmt.Errorf("indirilen imaj doğrulanamadı: %w", err)
	}
	if inspect.Digest != img.Digest {
		return fmt.Errorf("indirilen imajın digest'i uymuyor: %s", inspect.Digest)
	}
	return nil
}

// Switch pins the unit to the image and restarts it.
func (r *Runtime) Switch(ctx context.Context, img channel.Image) error {
	if err := r.allowed(img.Unit); err != nil {
		return err
	}
	dir := filepath.Join(r.o.UnitsDir, img.Unit+".container.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "# ajan yazar: güncelleme imajı digest ile sabitler (abarow-update)\n[Container]\nImage=" + r.ref(img) + "\n"
	tmp := filepath.Join(dir, DropIn+".tmp")
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, DropIn)); err != nil {
		return err
	}
	if err := r.o.Systemd.Reload(ctx); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	return r.o.Systemd.Restart(ctx, img.Unit+".service")
}

// Healthy says whether the unit is running healthy.
func (r *Runtime) Healthy(ctx context.Context, unit string) (bool, error) {
	if err := r.allowed(unit); err != nil {
		return false, err
	}
	return r.o.Systemd.Active(ctx, unit+".service")
}

func (r *Runtime) get(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("podman %s: %d", path, resp.StatusCode)
	}
	return json.NewDecoder(bytes.NewReader(raw)).Decode(out)
}

// UnitNames are the names of the allowed and installed units (for diagnostics).
func (r *Runtime) UnitNames() []string {
	u, _ := r.units()
	out := make([]string, 0, len(u))
	for k := range u {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
