package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/celestiaorg/celestia-node/libs/utils"
)

const xtokenFileName = "xtoken.json"

func grpcClient(lc fx.Lifecycle, cfg Config) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	if cfg.TLSEnabled {
		opts = append(opts, grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
		)
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if cfg.XTokenPath != "" {
		xToken, err := parseTokenPath(cfg.XTokenPath)
		if err != nil {
			return nil, err
		}
		authCreds := tokenAuth{token: xToken, requireTLS: cfg.TLSEnabled}
		opts = append(opts, grpc.WithPerRPCCredentials(authCreds))
	}

	endpoint := net.JoinHostPort(cfg.IP, cfg.Port)
	conn, err := NewGRPCClient(endpoint, opts...)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			conn.Connect()
			if !conn.WaitForStateChange(ctx, connectivity.Ready) {
				return errors.New("couldn't connect to core endpoint")
			}
			return nil
		},
	})
	return conn, nil
}

// tokenAuth implements the credentials.PerRPCCredentials interface
// to support token-based auth for unary and streaming grpc requests
type tokenAuth struct {
	token      string
	requireTLS bool
}

func (t tokenAuth) GetRequestMetadata(ctx context.Context, in ...string) (map[string]string, error) {
	return map[string]string{"x-token": t.token}, nil
}

func (t tokenAuth) RequireTransportSecurity() bool {
	return t.requireTLS
}

// parseTokenPath retrieves the authentication token from a JSON file at the specified path.
func parseTokenPath(xtokenPath string) (string, error) {
	xtokenPath = filepath.Join(xtokenPath, xtokenFileName)
	exist := utils.Exists(xtokenPath)
	if !exist {
		return "", os.ErrNotExist
	}

	token, err := os.ReadFile(xtokenPath)
	if err != nil {
		return "", err
	}

	auth := struct {
		Token string `json:"x-token"`
	}{}

	err = json.Unmarshal(token, &auth)
	if err != nil {
		return "", err
	}
	if auth.Token == "" {
		return "", errors.New("x-token is empty. Please setup a token or cleanup xtokenPath")
	}
	return auth.Token, nil
}
