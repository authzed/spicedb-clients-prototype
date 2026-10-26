package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/test/bufconn"
	"math/big"
	"net"
	"testing"
	"time"
)

type tlsSchemaServer struct {
	v1.UnimplementedSchemaServiceServer
	t         *testing.T
	handshake bool
}

func (s *tlsSchemaServer) ReadSchema(ctx context.Context, _ *v1.ReadSchemaRequest) (*v1.ReadSchemaResponse, error) {
	p, ok := peer.FromContext(ctx)
	require.True(s.t, ok)
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	require.True(s.t, ok)
	require.True(s.t, info.State.HandshakeComplete)
	md, _ := metadata.FromIncomingContext(ctx)
	require.Equal(s.t, []string{"Bearer tls-token"}, md.Get("authorization"))
	s.handshake = true
	return &v1.ReadSchemaResponse{SchemaText: "definition user {}"}, nil
}

// A real TLS handshake over an in-memory listener avoids network-dependent constructor-only assertions.
func TestCustomTrustCompletesRealTLSHandshake(t *testing.T) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, e)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "spicedb.test"}, DNSNames: []string{"spicedb.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, e)
	cert, e := x509.ParseCertificate(der)
	require.NoError(t, e)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})))
	stub := &tlsSchemaServer{t: t}
	v1.RegisterSchemaServiceServer(server, stub)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	c, e := NewWithOpts("passthrough:///spicedb.test", "tls-token", WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, ServerName: "spicedb.test", MinVersion: tls.VersionTLS12}))))
	require.NoError(t, e)
	t.Cleanup(func() { _ = c.Close() })
	result, e := c.ReadSchema(context.Background(), ReadSchemaRequest{})
	require.NoError(t, e)
	require.Equal(t, "definition user {}", result.Schema)
	require.True(t, stub.handshake)
}
