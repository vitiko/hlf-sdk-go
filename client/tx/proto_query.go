package tx

import (
	"context"
	"fmt"
	"reflect"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/peer"

	"github.com/vitiko/hlf-sdk-go/api"
)

type ProtoQuerier struct {
	Querier   api.Querier
	Channel   string
	Chaincode string
}

func NewProtoQuerier(querier api.Querier, channel, chaincode string) *ProtoQuerier {
	return &ProtoQuerier{
		Querier:   querier,
		Channel:   channel,
		Chaincode: chaincode,
	}
}

func (c *ProtoQuerier) Query(ctx context.Context, args ...interface{}) (*peer.Response, error) {
	argsBytes, err := ArgsBytes(args...)
	if err != nil {
		return nil, err
	}
	return c.Querier.Query(ctx, c.Channel, c.Chaincode, argsBytes, nil, nil)
}

func (c *ProtoQuerier) QueryBytes(ctx context.Context, args ...[]byte) (*peer.Response, error) {
	return c.Querier.Query(ctx, c.Channel, c.Chaincode, args, nil, nil)
}

func (c *ProtoQuerier) QueryProto(ctx context.Context, args []interface{}, target proto.Message) (proto.Message, error) {
	return QueryProto(ctx, c.Querier, c.Channel, c.Chaincode, args, target)
}

func (c *ProtoQuerier) QueryStringsProto(ctx context.Context, args []string, target proto.Message) (proto.Message, error) {
	return QueryStringsProto(ctx, c.Querier, c.Channel, c.Chaincode, args, target)
}

func (c *ProtoQuerier) QueryBytesProto(ctx context.Context, args [][]byte, target proto.Message) (proto.Message, error) {
	return QueryBytesProto(ctx, c.Querier, c.Channel, c.Chaincode, args, target)
}

func QueryProto(ctx context.Context, querier api.Querier, channel, chaincode string, args []interface{}, target proto.Message) (proto.Message, error) {
	argsBytes, err := ArgsBytes(args...)
	if err != nil {
		return nil, err
	}

	return QueryBytesProto(ctx, querier, channel, chaincode, argsBytes, target)
}

func QueryStringsProto(ctx context.Context, querier api.Querier, channel, chaincode string, args []string, target proto.Message) (proto.Message, error) {
	return QueryBytesProto(ctx, querier, channel, chaincode, StringArgsBytes(args...), target)
}

func QueryBytesProto(ctx context.Context, querier api.Querier, channel, chaincode string, args [][]byte, target proto.Message) (proto.Message, error) {
	res, err := querier.Query(
		ctx, channel, chaincode, args, nil, nil)

	if err != nil {
		return nil, err
	}

	resProto := proto.Clone(target)

	if err = proto.Unmarshal(res.Payload, resProto); err != nil {
		return nil, fmt.Errorf(`unmarshal result to %s: %w`, reflect.TypeOf(target), err)
	}

	return resProto, nil
}

type ProtoInvoker struct {
	*ProtoQuerier

	Invoker   api.Invoker
	Channel   string
	Chaincode string
}

func NewProtoInvoker(invoker api.Invoker, channel, chaincode string) *ProtoInvoker {
	return &ProtoInvoker{
		ProtoQuerier: NewProtoQuerier(invoker, channel, chaincode),
		Invoker:      invoker,
		Channel:      channel,
		Chaincode:    chaincode,
	}
}
