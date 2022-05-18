package chaincode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/common"
	fabricPeer "github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/msp"
	"github.com/hyperledger/fabric/protoutil"
	"github.com/pkg/errors"

	"github.com/vitiko/hlf-sdk-go/api"
	"github.com/vitiko/hlf-sdk-go/client/chaincode/txwaiter"
	"github.com/vitiko/hlf-sdk-go/client/tx"
)

type invokeBuilder struct {
	ccCore        *Core
	fn            string
	args          [][]byte
	transientArgs api.TransArgs
	doOptions     []api.DoOption
	err           *errArgMap
}

var _ api.ChaincodeInvokeBuilder = (*invokeBuilder)(nil)

var (
	ErrOrdererNotDefined     = errors.New(`orderer not defined`)
	ErrNotEnoughEndorsements = errors.New(`not enough endorsements`)
)

func NewInvokeBuilder(ccCore *Core, fn string) api.ChaincodeInvokeBuilder {
	return &invokeBuilder{
		ccCore: ccCore,
		fn:     fn,
		err:    newErrArgMap(),
	}
}

// WithIdentity instructs invoke builder to use given identity for signing transaction proposals
func (b *invokeBuilder) WithIdentity(identity msp.SigningIdentity) api.ChaincodeInvokeBuilder {
	b.doOptions = append(b.doOptions, api.WithIdentity(identity))
	return b
}

func (b *invokeBuilder) ArgBytes(args [][]byte) api.ChaincodeInvokeBuilder {
	b.args = args
	return b
}

func (b *invokeBuilder) Transient(args api.TransArgs) api.ChaincodeInvokeBuilder {
	b.transientArgs = args
	return b
}

func (b *invokeBuilder) ArgJSON(in ...interface{}) api.ChaincodeInvokeBuilder {
	argBytes := make([][]byte, 0)
	for _, arg := range in {
		if data, err := json.Marshal(arg); err != nil {
			b.err.Add(arg, err)
		} else {
			argBytes = append(argBytes, data)
		}
	}
	return b.ArgBytes(argBytes)
}

func (b *invokeBuilder) ArgString(args ...string) api.ChaincodeInvokeBuilder {
	return b.ArgBytes(tx.StringArgsBytes(args...))
}

func (b *invokeBuilder) Do(ctx context.Context, options ...api.DoOption) (*fabricPeer.Response, string, error) {
	err := b.err.Err()
	if err != nil {
		return nil, ``, err
	}

	if b.ccCore.orderer == nil {
		return nil, ``, ErrOrdererNotDefined
	}

	// set default options
	doOpts := &api.DoOptions{
		Identity:        b.ccCore.identity,
		Pool:            b.ccCore.peerPool,
		EndorsingMspIDs: b.ccCore.endorsingMSPs,
	}
	doOpts.TxWaiter, err = txwaiter.Self(doOpts)
	if err != nil {
		return nil, "", nil
	}

	// apply options
	for _, applyOpt := range append(b.doOptions, options...) {
		if err = applyOpt(doOpts); err != nil {
			return nil, ``, fmt.Errorf("apply options: %s", err)
		}
	}

	proposal, txID, err := tx.Endorsement{
		Channel:      b.ccCore.channelName,
		Chaincode:    b.ccCore.name,
		Args:         tx.FnArgs(b.fn, b.args...),
		Signer:       doOpts.Identity,
		TransientMap: b.transientArgs,
	}.SignedProposal()

	if err != nil {
		return nil, ``, fmt.Errorf("create proposal: %w", err)
	}

	peerResponses, err := b.ccCore.peerPool.EndorseOnMSPs(ctx, doOpts.EndorsingMspIDs, proposal)
	if err != nil {
		return nil, txID, fmt.Errorf("send proposal: %w", err)
	}

	if len(peerResponses) == 0 || len(peerResponses) != len(doOpts.EndorsingMspIDs) {
		return nil, ``, fmt.Errorf(`endorsements received num=%d, required=%d: %w`,
			len(peerResponses), len(doOpts.EndorsingMspIDs), ErrNotEnoughEndorsements)
	}

	envelope, err := CreateEnvelope(proposal, peerResponses, doOpts.Identity)
	if err != nil {
		return nil, txID, fmt.Errorf("create signed transaction: %w", err)
	}

	_, err = b.ccCore.orderer.Broadcast(ctx, envelope)
	if err != nil {
		return nil, txID, fmt.Errorf("broadcast transaction: %w", err)
	}

	if err = doOpts.TxWaiter.Wait(ctx, b.ccCore.channelName, txID); err != nil {
		return nil, txID, err
	}

	return peerResponses[0].Response, txID, nil
}

func CreateEnvelope(
	proposal *fabricPeer.SignedProposal, peerResponses []*fabricPeer.ProposalResponse, identity msp.SigningIdentity) (
	*common.Envelope, error) {

	prop := new(fabricPeer.Proposal)
	if err := proto.Unmarshal(proposal.ProposalBytes, prop); err != nil {
		return nil, fmt.Errorf("unmarshal proposal: %w", err)
	}

	return protoutil.CreateSignedTx(prop, identity, peerResponses...)
}

// TruncatableString a string that might be shortened to a specified length.
type TruncatableString struct {
	// The shortened string. For example, if the original string was 500 bytes long and
	// the limit of the string was 128 bytes, then this value contains the first 128
	// bytes of the 500-byte string. Note that truncation always happens on a
	// character boundary, to ensure that a truncated string is still valid UTF-8.
	// Because it may contain multi-byte characters, the size of the truncated string
	// may be less than the truncation limit.
	Value string

	// The number of bytes removed from the original string. If this
	// value is 0, then the string was not shortened.
	TruncatedByteCount int
}

func (t TruncatableString) String() string {
	if t.TruncatedByteCount == 0 {
		return t.Value
	}

	return fmt.Sprintf("%s(%d)", t.Value, t.TruncatedByteCount)
}

func makeTruncatableString(str string, size int) TruncatableString {
	if len(str) <= size {
		return TruncatableString{
			Value:              str,
			TruncatedByteCount: 0,
		}
	}

	return TruncatableString{
		Value:              str[0:size] + `...`,
		TruncatedByteCount: len(str[size:]),
	}
}

func newErrArgMap() *errArgMap {
	return &errArgMap{
		container: make(map[TruncatableString]error),
	}
}

type errArgMap struct {
	// slice of part of arg...
	container map[TruncatableString]error
}

func (e *errArgMap) Add(arg interface{}, err error) {
	e.container[makeTruncatableString(fmt.Sprintf("%#v", arg), 50)] = err
}

func (e *errArgMap) Err() error {
	if len(e.container) == 0 {
		return nil
	}

	buff := bytes.NewBuffer(nil)
	for key, err := range e.container {
		buff.WriteString(errors.Wrap(err, key.String()).Error() + "\n")
	}
	return errors.New(buff.String())
}
