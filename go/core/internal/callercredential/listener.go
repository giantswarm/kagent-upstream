package callercredential

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/agent-substrate/substrate/pkg/proto/credproviderpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// ListenerConfig is the mutual TLS the provider serves with: its own
// credential bundle (certificate chain and key in one PEM file), the CA the
// egress gateway's client certificate chains to, and the gateway's SPIFFE ID,
// the only client accepted. Both files are read again on every handshake, so
// rotated pod certificates take effect without a restart.
type ListenerConfig struct {
	Address          string
	ServerCredBundle string
	ClientCAFile     string
	InjectorSPIFFEID string
}

// Listener serves Server on the configured address until its context ends. It
// runs on every replica: a turn is known to the replica that dispatched it.
type Listener struct {
	config ListenerConfig
	server *Server
}

// NewListener validates config and returns the runnable listener.
func NewListener(config ListenerConfig, server *Server) (*Listener, error) {
	if config.ServerCredBundle == "" || config.ClientCAFile == "" {
		return nil, fmt.Errorf("caller credential provider needs a server credential bundle and a client CA file")
	}
	id, err := url.Parse(config.InjectorSPIFFEID)
	if err != nil || id.Scheme != "spiffe" || id.Host == "" || id.Path == "" || id.User != nil || id.RawQuery != "" || id.Fragment != "" {
		return nil, fmt.Errorf("caller credential provider injector identity %q must be a SPIFFE ID", config.InjectorSPIFFEID)
	}
	return &Listener{config: config, server: server}, nil
}

// NeedLeaderElection is false: every replica answers for the turns it runs.
func (l *Listener) NeedLeaderElection() bool { return false }

// Start serves until ctx ends.
func (l *Listener) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", l.config.Address)
	if err != nil {
		return fmt.Errorf("listen for caller credential requests: %w", err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(l.tlsConfig())))
	credproviderpb.RegisterCredentialProviderServer(server, l.server)
	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()
	if err := server.Serve(listener); err != nil {
		return fmt.Errorf("serve caller credential requests: %w", err)
	}
	return nil
}

func (l *Listener) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			certificate, err := tls.LoadX509KeyPair(l.config.ServerCredBundle, l.config.ServerCredBundle)
			if err != nil {
				return nil, fmt.Errorf("load caller credential provider certificate: %w", err)
			}
			pem, err := os.ReadFile(l.config.ClientCAFile)
			if err != nil {
				return nil, fmt.Errorf("read caller credential provider client CA: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("client CA file %q holds no certificate", l.config.ClientCAFile)
			}
			return &tls.Config{
				MinVersion:       tls.VersionTLS13,
				Certificates:     []tls.Certificate{certificate},
				ClientAuth:       tls.RequireAndVerifyClientCert,
				ClientCAs:        pool,
				NextProtos:       []string{"h2"},
				VerifyConnection: verifyInjector(l.config.InjectorSPIFFEID),
			}, nil
		},
	}
}

func verifyInjector(expected string) func(tls.ConnectionState) error {
	return func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return fmt.Errorf("client certificate is required")
		}
		for _, uri := range state.PeerCertificates[0].URIs {
			if uri.String() == expected {
				return nil
			}
		}
		return fmt.Errorf("client certificate does not carry the egress gateway's identity %q", expected)
	}
}
