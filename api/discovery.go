package api

import (
	"context"

	"github.com/vitiko/hlf-sdk-go/api/config"
)

type DiscoveryProvider interface {
	Chaincode(ctx context.Context, channelName string, ccName string) (ChaincodeDiscoverer, error)
	Channel(ctx context.Context, channelName string) (ChannelDiscoverer, error)
	LocalPeers(ctx context.Context) (LocalPeersDiscoverer, error)
}

// ChaincodeDiscoverer - looking for info about network, channel, chaincode in local configs or gossip
type ChaincodeDiscoverer interface {
	Endorsers() []*HostEndpoint
	ChaincodeName() string
	ChaincodeVersion() string

	ChannelDiscoverer
}

// ChannelDiscoverer - info about orderers in channel
type ChannelDiscoverer interface {
	Orderers() []*HostEndpoint
	ChannelName() string
}

// LocalPeersDiscoverer discover local peers without providing info about channel, chaincode
type LocalPeersDiscoverer interface {
	Peers() []*HostEndpoint
}

type HostEndpoint struct {
	MspID string
	// each host could have own tls settings
	HostAddresses []*HostAddress
}

type HostAddress struct {
	Address     string
	TLSSettings config.TlsConfig
}
