package callercredential

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/credproviderpb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const testInjector = "spiffe://cluster.local/ns/ate-system/sa/atenet-egress"

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// bundle issues a leaf and returns it as one PEM file, chain then key.
func (ca testCA) bundle(t *testing.T, dnsName, uri string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	if dnsName != "" {
		template.DNSNames = []string{dnsName}
	}
	if uri != "" {
		parsed, err := url.Parse(uri)
		require.NoError(t, err)
		template.URIs = []*url.URL{parsed}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)
}

func writeFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

func TestListenerAcceptsOnlyTheEgressGateway(t *testing.T) {
	serverCA, clientCA := newTestCA(t), newTestCA(t)
	address := freeAddress(t)
	listener, err := NewListener(ListenerConfig{
		Address:          address,
		ServerCredBundle: writeFile(t, "server.pem", serverCA.bundle(t, "kagent-controller.kagent.svc", "")),
		ClientCAFile:     writeFile(t, "client-ca.pem", clientCA.pem),
		InjectorSPIFFEID: testInjector,
	}, NewServer(NewTurns(), forwardingAuthenticator{}, &fakeBroker{}))
	require.NoError(t, err)
	go func() { _ = listener.Start(t.Context()) }()

	call := func(clientBundle []byte) error {
		certificate, err := tls.X509KeyPair(clientBundle, clientBundle)
		require.NoError(t, err)
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(serverCA.pem)
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{certificate}, RootCAs: roots, ServerName: "kagent-controller.kagent.svc", MinVersion: tls.VersionTLS13,
		})))
		require.NoError(t, err)
		defer func() { _ = connection.Close() }()
		_, err = credproviderpb.NewCredentialProviderClient(connection).FetchSecret(t.Context(), &credproviderpb.FetchSecretRequest{Uri: bearerURI, ActorSpiffeId: testActorID})
		return err
	}
	require.Eventually(t, func() bool {
		return status.Code(call(clientCA.bundle(t, "", testInjector))) == codes.NotFound
	}, 5*time.Second, 50*time.Millisecond, "the egress gateway reaches the provider, which knows no turn")
	require.Equal(t, codes.Unavailable, status.Code(call(clientCA.bundle(t, "", "spiffe://cluster.local/ns/team-a/sa/default"))), "another workload of the same CA is refused at the handshake")
	require.Equal(t, codes.Unavailable, status.Code(call(newTestCA(t).bundle(t, "", testInjector))), "the gateway's identity from another CA is refused")
}

func TestNewListenerRefusesAnIncompleteConfig(t *testing.T) {
	_, err := NewListener(ListenerConfig{Address: ":0", ClientCAFile: "ca.pem", InjectorSPIFFEID: testInjector}, nil)
	require.Error(t, err)
	_, err = NewListener(ListenerConfig{Address: ":0", ServerCredBundle: "b.pem", ClientCAFile: "ca.pem", InjectorSPIFFEID: "atenet-egress"}, nil)
	require.Error(t, err)
}
