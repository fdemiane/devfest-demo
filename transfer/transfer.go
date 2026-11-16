package transfer

import (
	"context"

	transferv1 "metroparadox/gen/metroparadox/transfer/v1"
)

type Service struct {
	transferv1.UnimplementedTransferServiceServer
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) SayHello(_ context.Context, _ *transferv1.SayHelloRequest) (*transferv1.SayHelloResponse, error) {
	return &transferv1.SayHelloResponse{Message: "Hello Google Fest"}, nil
}
