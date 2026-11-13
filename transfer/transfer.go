package transfer

import (
	"context"
	"fmt"
)

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) TransferMoney(_ context.Context, _ TransferRequest) (*TransferResponse, error) {
	fmt.Println("Money transfered")
	return &TransferResponse{}, nil
}
