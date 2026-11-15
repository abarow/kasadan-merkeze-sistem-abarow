// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRemoteErrorKeepsCodeAndCleansMessage(t *testing.T) {
	remote := status.Error(codes.FailedPrecondition, "kart ödemesi reddedildi")
	err := fmt.Errorf("ödeme alınamadı: %w", RemoteError(remote))

	if got := err.Error(); got != "ödeme alınamadı: kart ödemesi reddedildi" {
		t.Errorf("message %q", got)
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("kod %v, want FailedPrecondition", got)
	}
	plain := errors.New("düz hata")
	if RemoteError(plain) != plain {
		t.Error("a non-gRPC error changed")
	}
	if RemoteError(nil) != nil {
		t.Error("a nil error should stay nil")
	}
}

// The name is asked of the system at every connection, not of gRPC's DNS resolver; an address
// with a scheme stays as it is.
func TestDialResolvesOnEveryConnect(t *testing.T) {
	for addr, want := range map[string]string{
		"staff:9000":         "passthrough:///staff:9000",
		"dns:///staff:9000":  "dns:///staff:9000",
		"unix:///run/x.sock": "unix:///run/x.sock",
	} {
		conn, err := Dial(addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := conn.Target(); got != want {
			t.Errorf("%s → %s, want %s", addr, got, want)
		}
		conn.Close()
	}
}

func TestRemoteErrorFromHidesConnectionDetails(t *testing.T) {
	tests := []struct {
		err  error
		want string
		code codes.Code
	}{
		{status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: dial tcp 172.19.0.2:9000: i/o timeout"`), "yazıcı yanıt vermiyor", codes.Unavailable},
		{status.Error(codes.DeadlineExceeded, "context deadline exceeded"), "yazıcı yanıt vermiyor", codes.DeadlineExceeded},
		// The service's own message is kept.
		{status.Error(codes.Unavailable, "kağıt bitti"), "kağıt bitti", codes.Unavailable},
		{status.Error(codes.InvalidArgument, "3. satır 33 karakter; yazıcı 32 sütun"), "3. satır 33 karakter; yazıcı 32 sütun", codes.InvalidArgument},
	}
	for _, tt := range tests {
		err := RemoteErrorFrom("yazıcı", tt.err)
		if err.Error() != tt.want || status.Code(err) != tt.code {
			t.Errorf("%v → %q (%v); want %q (%v)", tt.err, err, status.Code(err), tt.want, tt.code)
		}
	}
	plain := errors.New("düz hata")
	if RemoteErrorFrom("yazıcı", plain) != plain || RemoteErrorFrom("yazıcı", nil) != nil {
		t.Error("a non-gRPC error changed")
	}
}

// A frozen service: the TCP connection stays open but nothing is answered. A call
// with no time limit would hang forever; the liveness check closes the connection and the
// call ends with UNAVAILABLE.
func TestDialDetectsFrozenServer(t *testing.T) {
	if testing.Short() {
		t.Skip("the gRPC client keeps the keepalive interval at least 10 s; the test takes ~11 s")
	}
	srv := NewGRPCServer()
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "test.Hang",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "Wait", Handler: func(_ any, ctx context.Context, _ func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			select {
			case <-hang:
			case <-ctx.Done():
			}
			return nil, nil
		}}},
	}, struct{}{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	proxy := newFreezableProxy(t, lis.Addr().String())
	conn, err := dial(proxy.addr(), keepalive.ClientParameters{Time: 10 * time.Second, Timeout: 500 * time.Millisecond, PermitWithoutStream: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Let the connection be made, then the server "freezes".
	if err := conn.Invoke(t.Context(), "/test.Hang/Nope", &emptypb.Empty{}, &emptypb.Empty{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("could not connect: %v", err)
	}
	proxy.freeze()

	// No time limit (context.Background): the call must still end.
	done := make(chan error, 1)
	go func() {
		done <- conn.Invoke(context.Background(), "/test.Hang/Wait", &emptypb.Empty{}, &emptypb.Empty{})
	}()
	select {
	case err := <-done:
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("error %v, want UNAVAILABLE", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a call to a frozen server did not finish in 20 seconds")
	}
}

// freezableProxy carries bytes between the client and the server; when freeze is called
// it stops carrying bytes in both directions but keeps the connections open.
type freezableProxy struct {
	lis    net.Listener
	frozen chan struct{}
	once   sync.Once
}

func newFreezableProxy(t *testing.T, target string) *freezableProxy {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &freezableProxy{lis: lis, frozen: make(chan struct{})}
	t.Cleanup(func() { lis.Close() })
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			s, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			t.Cleanup(func() { c.Close(); s.Close() })
			go p.pipe(c, s)
			go p.pipe(s, c)
		}
	}()
	return p
}

func (p *freezableProxy) addr() string { return p.lis.Addr().String() }
func (p *freezableProxy) freeze()      { p.once.Do(func() { close(p.frozen) }) }

func (p *freezableProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			select {
			case <-p.frozen:
				// frozen: the bytes are swallowed, the connection stays open
			default:
				dst.Write(buf[:n])
			}
		}
		if err != nil {
			return
		}
	}
}
