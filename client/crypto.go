package client

import (
	"github.com/vitiko/hlf-sdk-go/api"
	"github.com/vitiko/hlf-sdk-go/crypto"
	"github.com/vitiko/hlf-sdk-go/crypto/ecdsa"
)

func DefaultCryptoSuite() api.CryptoSuite {
	suite, _ := crypto.GetSuite(ecdsa.Module, ecdsa.DefaultOpts)
	return suite
}
