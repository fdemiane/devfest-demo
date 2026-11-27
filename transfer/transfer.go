package transfer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	transferv1 "metroparadox/gen/metroparadox/transfer/v1"
)

const (
	nanosPerUnit          = 1_000_000_000
	maxIdempotencyEntries = 100_000
)

var supportedCurrencies = map[string]struct{}{
	"USD": {},
	"LBP": {},
}

type idempotencyRecord struct {
	sourceAccount      string
	destinationAccount string
	transferID         string
	amount             *money.Money
	response           *transferv1.Transfer
}

type inFlightTransfer struct {
	done   chan struct{}
	record idempotencyRecord
	err    error
}

type Service struct {
	transferv1.UnimplementedTransferServiceServer
	store           Store
	mu              sync.Mutex
	idempotency     map[string]idempotencyRecord
	idempotencyKeys []string
	inFlight        map[string]*inFlightTransfer
}

func NewService(store Store) *Service {
	return &Service{
		store:       store,
		idempotency: make(map[string]idempotencyRecord),
		inFlight:    make(map[string]*inFlightTransfer),
	}
}

func (s *Service) SayHello(_ context.Context, _ *transferv1.SayHelloRequest) (*transferv1.SayHelloResponse, error) {
	return &transferv1.SayHelloResponse{Message: "Hello Google Fest"}, nil
}

func (s *Service) CreateTransfer(ctx context.Context, req *transferv1.CreateTransferRequest) (*transferv1.Transfer, error) {
	if ctx == nil {
		return nil, status.Error(codes.InvalidArgument, "context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if s.store == nil {
		return nil, status.Error(codes.Internal, "store is not configured")
	}
	if err := validateCreateTransferRequest(req); err != nil {
		return nil, err
	}

	requestID := req.GetRequestId()
	s.mu.Lock()
	s.ensureMapsLocked()
	if rec, ok := s.idempotency[requestID]; ok {
		s.mu.Unlock()
		return resolveIdempotentHit(rec, req)
	}
	if call, ok := s.inFlight[requestID]; ok {
		s.mu.Unlock()
		select {
		case <-call.done:
			if call.err != nil {
				return nil, call.err
			}
			return resolveIdempotentHit(call.record, req)
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	call := &inFlightTransfer{done: make(chan struct{})}
	s.inFlight[requestID] = call
	s.mu.Unlock()

	resp, rec, err := s.executeAndBuildTransfer(ctx, req)

	s.mu.Lock()
	delete(s.inFlight, requestID)
	if err == nil {
		s.recordIdempotencyLocked(requestID, rec)
		call.record = rec
	} else {
		call.err = err
	}
	close(call.done)
	s.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) ensureMapsLocked() {
	if s.idempotency == nil {
		s.idempotency = make(map[string]idempotencyRecord)
	}
	if s.inFlight == nil {
		s.inFlight = make(map[string]*inFlightTransfer)
	}
}

func (s *Service) recordIdempotencyLocked(requestID string, rec idempotencyRecord) {
	if _, exists := s.idempotency[requestID]; !exists {
		if len(s.idempotencyKeys) >= maxIdempotencyEntries {
			evict := s.idempotencyKeys[0]
			s.idempotencyKeys = s.idempotencyKeys[1:]
			delete(s.idempotency, evict)
		}
		s.idempotencyKeys = append(s.idempotencyKeys, requestID)
	}
	s.idempotency[requestID] = rec
}

func (s *Service) executeAndBuildTransfer(ctx context.Context, req *transferv1.CreateTransferRequest) (*transferv1.Transfer, idempotencyRecord, error) {
	transferID := req.GetTransferId()
	if transferID == "" {
		var err error
		transferID, err = newUUID()
		if err != nil {
			return nil, idempotencyRecord{}, status.Error(codes.Internal, "failed to generate transfer id")
		}
	}

	t := req.GetTransfer()
	amount := cloneMoney(t.GetAmount())
	storeReq := TransferRequest{
		SourceAccount:      t.GetSourceAccount(),
		DestinationAccount: t.GetDestinationAccount(),
		Amount:             amount,
	}

	_, err := s.store.ExecuteTransfer(ctx, storeReq, func(src, dst Account) (Account, Account, *TransferResponse, error) {
		newSrc, newDst, err := applyTransfer(src, dst, amount)
		if err != nil {
			return Account{}, Account{}, nil, err
		}
		return newSrc, newDst, &TransferResponse{TransferID: transferID}, nil
	})
	if err != nil {
		return nil, idempotencyRecord{}, mapStoreError(err)
	}

	resp := &transferv1.Transfer{
		Name:               "transfers/" + transferID,
		SourceAccount:      t.GetSourceAccount(),
		DestinationAccount: t.GetDestinationAccount(),
		Amount:             amount,
		CreateTime:         timestamppb.Now(),
		State:              transferv1.Transfer_COMPLETED,
	}

	rec := idempotencyRecord{
		sourceAccount:      t.GetSourceAccount(),
		destinationAccount: t.GetDestinationAccount(),
		transferID:         req.GetTransferId(),
		amount:             cloneMoney(amount),
		response:           cloneTransfer(resp),
	}
	return resp, rec, nil
}

func resolveIdempotentHit(rec idempotencyRecord, req *transferv1.CreateTransferRequest) (*transferv1.Transfer, error) {
	if !matchesIdempotencyRecord(rec, req) {
		return nil, status.Error(codes.InvalidArgument, "request_id reused with different transfer parameters")
	}
	return cloneTransfer(rec.response), nil
}

func validateCreateTransferRequest(req *transferv1.CreateTransferRequest) error {
	if req == nil || req.GetTransfer() == nil {
		return status.Error(codes.InvalidArgument, "transfer is required")
	}
	if req.GetRequestId() == "" {
		return status.Error(codes.InvalidArgument, "request_id is required")
	}
	if strings.Contains(req.GetTransferId(), "/") {
		return status.Error(codes.InvalidArgument, "transfer_id must not contain '/'")
	}

	t := req.GetTransfer()
	if t.GetSourceAccount() == "" {
		return status.Error(codes.InvalidArgument, "source_account is required")
	}
	if t.GetDestinationAccount() == "" {
		return status.Error(codes.InvalidArgument, "destination_account is required")
	}
	if t.GetSourceAccount() == t.GetDestinationAccount() {
		return status.Error(codes.InvalidArgument, "source_account and destination_account must differ")
	}
	return validatePositiveMoney(t.GetAmount())
}

func validatePositiveMoney(m *money.Money) error {
	if m == nil {
		return status.Error(codes.InvalidArgument, "amount is required")
	}
	if _, ok := supportedCurrencies[m.GetCurrencyCode()]; !ok {
		return status.Errorf(codes.InvalidArgument, "unsupported currency %q", m.GetCurrencyCode())
	}
	if err := validateMoneyFormat(m); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if m.GetUnits() < 0 || (m.GetUnits() == 0 && m.GetNanos() <= 0) {
		return status.Error(codes.InvalidArgument, "amount must be greater than zero")
	}
	return nil
}

func validateMoneyFormat(m *money.Money) error {
	if m == nil {
		return errors.New("money is nil")
	}
	nanos := m.GetNanos()
	if nanos <= -nanosPerUnit || nanos >= nanosPerUnit {
		return errors.New("money nanos out of range")
	}
	if (m.GetUnits() > 0 && nanos < 0) || (m.GetUnits() < 0 && nanos > 0) {
		return errors.New("money units and nanos must have the same sign")
	}
	return nil
}

func applyTransfer(src, dst Account, amount *money.Money) (Account, Account, error) {
	if src.Balance == nil || dst.Balance == nil {
		return Account{}, Account{}, fmt.Errorf("%w: account balance is missing", ErrInvalidArgument)
	}
	if err := validateMoneyFormat(src.Balance); err != nil {
		return Account{}, Account{}, fmt.Errorf("%w: invalid source balance: %v", ErrInvalidArgument, err)
	}
	if err := validateMoneyFormat(dst.Balance); err != nil {
		return Account{}, Account{}, fmt.Errorf("%w: invalid destination balance: %v", ErrInvalidArgument, err)
	}

	currency := amount.GetCurrencyCode()
	if src.Balance.GetCurrencyCode() != dst.Balance.GetCurrencyCode() {
		return Account{}, Account{}, fmt.Errorf("%w: source and destination currencies must match", ErrInvalidArgument)
	}
	if src.Balance.GetCurrencyCode() != currency {
		return Account{}, Account{}, fmt.Errorf("%w: transfer currency %s does not match account currency %s", ErrInvalidArgument, currency, src.Balance.GetCurrencyCode())
	}

	srcNanos := moneyToNanos(src.Balance)
	dstNanos := moneyToNanos(dst.Balance)
	amtNanos := moneyToNanos(amount)

	if amtNanos.Sign() <= 0 {
		return Account{}, Account{}, fmt.Errorf("%w: amount must be positive", ErrInvalidArgument)
	}
	if srcNanos.Sign() < 0 || dstNanos.Sign() < 0 {
		return Account{}, Account{}, fmt.Errorf("%w: account balance cannot be negative", ErrInvalidArgument)
	}
	if srcNanos.Cmp(amtNanos) < 0 {
		return Account{}, Account{}, ErrInsufficientFunds
	}

	newSrcNanos := new(big.Int).Sub(srcNanos, amtNanos)
	newDstNanos := new(big.Int).Add(dstNanos, amtNanos)

	newSrcBalance, err := nanosToMoney(currency, newSrcNanos)
	if err != nil {
		return Account{}, Account{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	newDstBalance, err := nanosToMoney(currency, newDstNanos)
	if err != nil {
		return Account{}, Account{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	src.Balance = newSrcBalance
	dst.Balance = newDstBalance
	return src, dst, nil
}

func moneyToNanos(m *money.Money) *big.Int {
	units := big.NewInt(m.GetUnits())
	units.Mul(units, big.NewInt(nanosPerUnit))
	return units.Add(units, big.NewInt(int64(m.GetNanos())))
}

func nanosToMoney(currency string, total *big.Int) (*money.Money, error) {
	divisor := big.NewInt(nanosPerUnit)
	units, rem := new(big.Int).QuoRem(total, divisor, new(big.Int))
	if !units.IsInt64() {
		return nil, errors.New("money units overflow int64")
	}
	return &money.Money{
		CurrencyCode: currency,
		Units:        units.Int64(),
		Nanos:        int32(rem.Int64()),
	}, nil
}

func matchesIdempotencyRecord(rec idempotencyRecord, req *transferv1.CreateTransferRequest) bool {
	t := req.GetTransfer()
	return rec.sourceAccount == t.GetSourceAccount() &&
		rec.destinationAccount == t.GetDestinationAccount() &&
		rec.transferID == req.GetTransferId() &&
		proto.Equal(rec.amount, t.GetAmount())
}

func mapStoreError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}

func cloneMoney(m *money.Money) *money.Money {
	if m == nil {
		return nil
	}
	cloned, ok := proto.Clone(m).(*money.Money)
	if !ok {
		return nil
	}
	return cloned
}

func cloneTransfer(t *transferv1.Transfer) *transferv1.Transfer {
	if t == nil {
		return nil
	}
	cloned, ok := proto.Clone(t).(*transferv1.Transfer)
	if !ok {
		return nil
	}
	return cloned
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
