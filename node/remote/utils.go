package remote

import (
	"context"
	"crypto/tls"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"regexp"
	"strconv"

	"google.golang.org/grpc"
)

var (
	HTTPProtocols = regexp.MustCompile("https?://")
)

// GetHeightRequestContext adds the height to the context for querying the state at a given height
func GetHeightRequestContext(context context.Context, height int64) context.Context {
	return metadata.AppendToOutgoingContext(
		context,
		grpctypes.GRPCBlockHeightHeader,
		strconv.FormatInt(height, 10),
	)
}

// MustCreateGrpcConnection creates a new gRPC connection using the provided configuration and panics on error
func MustCreateGrpcConnection(cfg *GRPCConfig) *grpc.ClientConn {
	grpConnection, err := CreateGrpcConnection(cfg)
	if err != nil {
		panic(err)
	}
	return grpConnection
}

// CreateGrpcConnection creates a new gRPC client connection from the given configuration
func CreateGrpcConnection(cfg *GRPCConfig) (*grpc.ClientConn, error) {
	var grpcOpts []grpc.DialOption
	if cfg.Insecure {
		grpcOpts = append(grpcOpts, grpc.WithInsecure())
	} else {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	}

	callOptions := make([]grpc.CallOption, 0, 5)
	if cfg.CallOptions.MaxRecvMsgSize > 0 {
		callOptions = append(callOptions, grpc.MaxCallRecvMsgSize(cfg.CallOptions.MaxRecvMsgSize))
	}
	if cfg.CallOptions.MaxSendMsgSize > 0 {
		callOptions = append(callOptions, grpc.MaxCallSendMsgSize(cfg.CallOptions.MaxSendMsgSize))
	}

	if len(callOptions) > 0 {
		grpcOpts = append(grpcOpts, grpc.WithDefaultCallOptions(callOptions...))
	}

	address := HTTPProtocols.ReplaceAllString(cfg.Address, "")
	return grpc.Dial(address, grpcOpts...)
}
