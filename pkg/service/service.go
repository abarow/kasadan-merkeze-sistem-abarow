// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package service holds the abarow services' shared startup helpers:
// environment variables, gRPC and health check servers and the container
// health command.
package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// Env returns the environment variable; the fallback if empty.
func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// MustEnv returns a required environment variable.
func MustEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("the %s environment variable is required", key)
	}
	return v, nil
}

// Main is the service's common entry point. If the program was called with the
// "healthcheck" argument it checks /healthz at healthAddr and exits: since there is no curl
// in scratch images the container health check is done this way.
// Otherwise it runs run until SIGINT or SIGTERM comes.
func Main(name, healthAddr string, run func(ctx context.Context, log *slog.Logger) error) {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := Healthcheck(healthAddr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", name)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log); err != nil {
		log.Error("servis durdu", "err", err)
		os.Exit(1)
	}
}

// Healthcheck sends a request to the /healthz endpoint at addr; it returns nil if the
// service is healthy.
func Healthcheck(addr string) error {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: %s", resp.Status)
	}
	return nil
}

// HealthHandler returns 200 "ok" if check returns nil, otherwise 503.
func HealthHandler(check func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			if err := check(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.Write([]byte("ok"))
	})
	return mux
}

// Dial sets up a gRPC client to a service inside the register. The services are on the
// same machine and the interruptions (a container restart, a printer service
// and the like) are short: the reconnect wait does not stretch to minutes like the gRPC default,
// it is at most 2 seconds. So when the service comes back the first
// "try again" works. The connection is unencrypted; a separate setting is needed for
// connections that leave the register (store server).
//
// The name is re-resolved at every connection (passthrough): a restarted
// container gets a new IP. gRPC's own DNS resolver re-asks the name at most every 30
// seconds; the service would be looked for at the old address and would
// seem unreachable for that time.
//
// A frozen service (the process stopped, the network half cut) leaves the TCP connection
// open and answers nothing; the client waits only for the call's time limit.
// So the connection is watched with a liveness check (keepalive): every 10 seconds
// a silent connection is pinged, if no answer comes in 5 seconds the connection closes and
// the pending calls end with UNAVAILABLE. Each call must also carry its own
// time limit (see sales, gateway); this is only the second defense.
func Dial(addr string) (*grpc.ClientConn, error) {
	return dial(addr, ClientKeepalive)
}

// ClientKeepalive is Dial's connection liveness setting.
var ClientKeepalive = keepalive.ClientParameters{Time: 10 * time.Second, Timeout: 5 * time.Second, PermitWithoutStream: true}

// NewGRPCServer sets up a gRPC server for the services inside the register. It allows the
// clients' (Dial) liveness pings (the default punishes pings more frequent than 5 minutes
// by closing the connection) and itself polls a silent client.
func NewGRPCServer(opts ...grpc.ServerOption) *grpc.Server {
	base := []grpc.ServerOption{
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
	}
	return grpc.NewServer(append(base, opts...)...)
}

func dial(addr string, kp keepalive.ClientParameters) (*grpc.ClientConn, error) {
	if !strings.Contains(addr, "://") {
		addr = "passthrough:///" + addr
	}
	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kp),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 100 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: 2 * time.Second},
			MinConnectTimeout: 2 * time.Second,
		}))
}

// RemoteError turns a gRPC error from another service into a plain error,
// keeping its code: Error() returns only the message (without the "rpc error:
// code = ... desc = ..." prefix), status.FromError finds the code.
// If it is not a gRPC error it returns err as it is.
func RemoteError(err error) error {
	st, ok := status.FromError(err)
	if !ok || st.Code() == codes.OK {
		return err
	}
	return remoteError{st}
}

// RemoteErrorFrom is like RemoteError; in addition, if the service could not be reached
// at all (the connection could not be made or the time ran out) it returns "<name> yanıt vermiyor"
// instead of gRPC's technical message; the code is kept. The service's own messages
// (e.g. "paper out") stay as they are. The message may go all the way to the
// cashier's screen.
func RemoteErrorFrom(name string, err error) error {
	st, ok := status.FromError(err)
	if !ok || st.Code() == codes.OK {
		return err
	}
	if unreachable(st) {
		return remoteError{status.New(st.Code(), name+" yanıt vermiyor")}
	}
	return remoteError{st}
}

// unreachable says the error comes from the connection, not from the service itself:
// when the gRPC client cannot make a connection it produces Unavailable and a message starting with
// "connection error", when the time runs out DeadlineExceeded.
func unreachable(st *status.Status) bool {
	switch st.Code() {
	case codes.DeadlineExceeded:
		return true
	case codes.Unavailable:
		msg := st.Message()
		return strings.HasPrefix(msg, "connection error") || strings.Contains(msg, "transport:") || strings.HasPrefix(msg, "name resolver error")
	}
	return false
}

type remoteError struct{ st *status.Status }

func (e remoteError) Error() string              { return e.st.Message() }
func (e remoteError) GRPCStatus() *status.Status { return e.st }

// HTTPConfig is ServeHTTP's settings.
type HTTPConfig struct {
	Handler    http.Handler
	Addr       string
	HealthAddr string
	// Health is the health check; it can be nil.
	Health func(context.Context) error
	// If TLSCertFile and TLSKeyFile are filled the main server serves HTTPS (the health
	// server stays plain HTTP and is asked only from inside the container).
	// The certificate is read from the file at every connection: the service need not restart
	// for a renewed certificate.
	TLSCertFile, TLSKeyFile string
	// Extra are additional HTTP servers that run together with the main server (e.g. an admin
	// port reachable only from the internal network). If one stops all close.
	Extra []ExtraServer
}

// ExtraServer is an additional server ServeHTTP opens besides the main server. It serves
// HTTPS if the certificate files are filled.
type ExtraServer struct {
	Name                    string // the name shown in the log
	Addr                    string
	Handler                 http.Handler
	TLSCertFile, TLSKeyFile string
}

// TLSFromEnv reads the ABAROW_TLS_CERT and ABAROW_TLS_KEY variables; if both are
// empty HTTPS is off, if only one is filled it returns an error.
func TLSFromEnv() (cert, key string, err error) {
	cert, key = os.Getenv("ABAROW_TLS_CERT"), os.Getenv("ABAROW_TLS_KEY")
	if (cert == "") != (key == "") {
		return "", "", errors.New("ABAROW_TLS_CERT ve ABAROW_TLS_KEY birlikte verilmeli")
	}
	return cert, key, nil
}

// certReloader reads the certificate and key from the file at every handshake.
func certReloader(certFile, keyFile string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		c, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("could not read the TLS certificate: %w", err)
		}
		return &c, nil
	}
}

// tlsConfig is the TLS setting that reads the certificate from the file at every connection; the files
// are read once at startup, a corrupt file stops the service at start.
func tlsConfig(certFile, keyFile string) (*tls.Config, error) {
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return nil, fmt.Errorf("TLS certificate: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certReloader(certFile, keyFile)}, nil
}

// ServeHTTP runs the main HTTP server, the additional servers and the health server until ctx ends,
// then shuts down, allowing a short time for open requests to finish.
func ServeHTTP(ctx context.Context, log *slog.Logger, cfg HTTPConfig) error {
	type entry struct {
		srv   *http.Server
		https bool
		name  string
	}
	var entries []entry
	add := func(name, addr string, h http.Handler, cert, key string, header time.Duration) error {
		e := entry{srv: &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: header}, name: name}
		if cert != "" && key != "" {
			tc, err := tlsConfig(cert, key)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			e.srv.TLSConfig, e.https = tc, true
		}
		entries = append(entries, e)
		return nil
	}
	if err := add("http", cfg.Addr, cfg.Handler, cfg.TLSCertFile, cfg.TLSKeyFile, 10*time.Second); err != nil {
		return err
	}
	for _, x := range cfg.Extra {
		if err := add(x.Name, x.Addr, x.Handler, x.TLSCertFile, x.TLSKeyFile, 10*time.Second); err != nil {
			return err
		}
	}
	// The health server stays plain HTTP and is asked only from inside the container.
	if err := add("health", cfg.HealthAddr, HealthHandler(cfg.Health), "", "", 5*time.Second); err != nil {
		return err
	}

	errc := make(chan error, len(entries))
	for _, e := range entries {
		go func() {
			var err error
			if e.https {
				err = e.srv.ListenAndServeTLS("", "")
			} else {
				err = e.srv.ListenAndServe()
			}
			if !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s sunucusu: %w", e.name, err)
			}
		}()
	}
	log.Info("ready", "http", cfg.Addr, "tls", entries[0].https, "health", cfg.HealthAddr, "extra", len(cfg.Extra))

	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, e := range entries {
		_ = e.srv.Shutdown(shutdownCtx)
	}
	return err
}

// ServeConfig is Serve's settings.
type ServeConfig struct {
	GRPC       *grpc.Server
	GRPCAddr   string
	HealthAddr string
	// Health is the health check (e.g. a database ping); it can be nil.
	Health func(context.Context) error
}

// Serve runs the gRPC and health servers until ctx ends, then shuts down, allowing a
// short time for open requests to finish.
func Serve(ctx context.Context, log *slog.Logger, cfg ServeConfig) error {
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	health := &http.Server{Addr: cfg.HealthAddr, Handler: HealthHandler(cfg.Health), ReadHeaderTimeout: 5 * time.Second}

	errc := make(chan error, 2)
	go func() { errc <- cfg.GRPC.Serve(lis) }()
	go func() {
		if err := health.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	log.Info("ready", "grpc", cfg.GRPCAddr, "health", cfg.HealthAddr)

	select {
	case <-ctx.Done():
	case err = <-errc:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		cfg.GRPC.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-shutdownCtx.Done():
		cfg.GRPC.Stop()
	}
	_ = health.Shutdown(shutdownCtx)
	return err
}
