package grpc

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"time"

	grpcretry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/pkg/errors"
	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"

	"github.com/vitiko/hlf-sdk-go/api/config"
	"github.com/vitiko/hlf-sdk-go/opencensus/hlf"
)

var (
	DefaultGRPCRetryConfig = &config.GRPCRetryConfig{
		Max:     10,
		Timeout: config.Duration{Duration: 10 * time.Second},
	}

	DefaultGRPCKeepAliveConfig = &config.GRPCKeepAliveConfig{
		Time:    60,
		Timeout: 5,
	}
)

const (
	maxRecvMsgSize = 100 * 1024 * 1024
	maxSendMsgSize = 100 * 1024 * 1024
)

type Opts struct {
	TLSCertHash []byte
	Dial        []grpc.DialOption
}

// OptionsFromConfig - adds tracing, TLS certs and connection limits
func OptionsFromConfig(c config.ConnectionConfig, logger *zap.Logger) (*Opts, error) {

	// TODO: move to config or variable options

	opts := &Opts{
		Dial: []grpc.DialOption{
			grpc.WithStatsHandler(hlf.Wrap(&ocgrpc.ClientHandler{
				StartOptions: trace.StartOptions{
					Sampler:  trace.AlwaysSample(),
					SpanKind: trace.SpanKindClient,
				},
			})),
		},
	}

	if c.Tls.Enabled {
		var (
			err    error
			tlsCfg tls.Config
		)

		tlsCfg.InsecureSkipVerify = c.Tls.SkipVerify
		// if custom CA certificate is presented, use it
		if c.Tls.CACertPath != `` {
			caCert, err := ioutil.ReadFile(c.Tls.CACertPath)
			if err != nil {
				return nil, errors.Wrap(err, `failed to read CA certificate`)
			}
			certPool := x509.NewCertPool()
			if ok := certPool.AppendCertsFromPEM(caCert); !ok {
				return nil, errors.New(`failed to append CA certificate to chain`)
			}
			tlsCfg.RootCAs = certPool
		} else {
			// otherwise, we use system certificates
			if tlsCfg.RootCAs, err = x509.SystemCertPool(); err != nil {
				return nil, fmt.Errorf(`get system cert pool: %w`, err)
			}
		}
		if c.Tls.CertPath != `` {
			// use mutual tls if certificate and pk is presented
			if c.Tls.KeyPath != `` {
				cert, err := tls.LoadX509KeyPair(c.Tls.CertPath, c.Tls.KeyPath)
				if err != nil {
					return nil, fmt.Errorf(`TLS client certificate: %w`, err)
				}
				tlsCfg.Certificates = append(tlsCfg.Certificates, cert)

				if len(cert.Certificate) > 0 {
					opts.TLSCertHash = TLSCertHash(cert.Certificate[0])
				}
			}
		}

		cred := credentials.NewTLS(&tlsCfg)
		opts.Dial = append(opts.Dial, grpc.WithTransportCredentials(cred))
	} else {
		opts.Dial = append(opts.Dial, grpc.WithInsecure())
	}

	// Set default keep alive
	if c.GRPC.KeepAlive == nil {
		c.GRPC.KeepAlive = DefaultGRPCKeepAliveConfig
	}
	opts.Dial = append(opts.Dial, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                time.Duration(c.GRPC.KeepAlive.Time) * time.Second,
		Timeout:             time.Duration(c.GRPC.KeepAlive.Timeout) * time.Second,
		PermitWithoutStream: true,
	}))

	var retryConfig *config.GRPCRetryConfig
	if c.GRPC.Retry != nil {
		retryConfig = c.GRPC.Retry
	} else if c.Timeout.String() != `` {
		retryConfig = DefaultGRPCRetryConfig
		retryConfig.Timeout = c.Timeout
	} else {
		retryConfig = DefaultGRPCRetryConfig
	}

	opts.Dial = append(opts.Dial,
		grpc.WithUnaryInterceptor(
			grpcretry.UnaryClientInterceptor(
				grpcretry.WithMax(retryConfig.Max),
				grpcretry.WithBackoff(grpcretry.BackoffLinear(retryConfig.Timeout.Duration)),
			),
		),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxRecvMsgSize),
			grpc.MaxCallSendMsgSize(maxSendMsgSize),
		),
		grpc.WithBlock(),
	)

	fields := []zap.Field{
		zap.String(`host`, c.Host),
		zap.Bool(`tls`, c.Tls.Enabled),
		zap.Reflect(`keep alive`, c.GRPC.KeepAlive),
		zap.Reflect(`retry`, retryConfig),
	}
	if c.Tls.Enabled {
		fields = append(fields, zap.Reflect(`tls`, c.Tls))
	}

	logger.Debug(`grpc options`, fields...)

	return opts, nil
}

func TLSCertHash(cert []byte) []byte {
	hash := sha256.Sum256(cert)
	return hash[:]
}

// ConnectionFromConfigs - initializes grpc connection with pool of addresses with round-robin client balancer
func ConnectionFromConfigs(ctx context.Context, logger *zap.Logger, conf ...config.ConnectionConfig) (*grpc.ClientConn, error) {
	if len(conf) == 0 {
		return nil, errors.New(`no GRPC options provided`)
	}
	// use options from first config
	opts, err := OptionsFromConfig(conf[0], logger)
	if err != nil {
		return nil, errors.Wrap(err, `failed to get GRPC options`)
	}
	// name is necessary for grpc balancer and address verification in tls certs
	dnsResolverName, _, err := net.SplitHostPort(conf[0].Host)
	if err != nil {
		return nil, fmt.Errorf("cant fetch domain name from %v", conf[0].Host)
	}

	addr := make([]resolver.Address, len(conf))
	var hosts []string

	for i, cc := range conf {
		addr[i] = resolver.Address{Addr: cc.Host}
		hosts = append(hosts, cc.Host)
	}

	r, _ := manual.GenerateAndRegisterManualResolver()
	r.InitialState(resolver.State{Addresses: addr})

	opts.Dial = append(opts.Dial, grpc.WithBalancerName(roundrobin.Name))

	logger.Debug(`grpc dial to orderer`, zap.Strings(`hosts`, hosts))

	ctxConn, cancel := context.WithTimeout(ctx, time.Second*2)
	defer cancel()

	conn, err := grpc.DialContext(ctxConn, fmt.Sprintf("%s:///%s", r.Scheme(), dnsResolverName), opts.Dial...)
	if err != nil {
		return nil, errors.Wrap(err, `grpc dial`)
	}

	return conn, nil
}
