package service

import (
	"errors"
	"math/big"

	"github.com/icon-project/goloop/module"
	"github.com/icon-project/goloop/service/eeproxy"
)

const (
	GIGA = 1000 * 1000 * 1000
	TERA = 1000 * GIGA
	PETA = 1000 * TERA
	EXA  = 1000 * PETA
)

var (
	ErrNotEnoughBalance   = errors.New("NotEnoughBalance")
	ErrTimeOut            = errors.New("TimeOut")
	ErrInvalidFeeValue    = errors.New("InvalidFeeValue")
	ErrNotEnoughStep      = errors.New("NotEnoughStep")
	ErrContractIsRequired = errors.New("ContractIsRequired")
	ErrInvalidHashValue   = errors.New("InvalidHashValue")
)

type StepType int

const (
	StepTypeDefault StepType = iota
	StepTypeInput
)

type WorldContext interface {
	WorldState
	StepPrice() *big.Int
	TimeStamp() int64
	BlockHeight() int64
	Treasury() module.Address
	ContractManager() ContractManager
	EEManager() eeproxy.Manager
	WorldStateChanged(ws WorldState) WorldContext
	WorldVirtualState() WorldVirtualState
	GetFuture(lq []LockRequest) WorldContext
	StepsFor(t StepType, n int) int64
}

type Transaction interface {
	module.Transaction
	PreValidate(wc WorldContext, update bool) error
	GetHandler(cm ContractManager) (TransactionHandler, error)
	Timestamp() int64
}

type Receipt interface {
	module.Receipt
	AddLog(addr module.Address, indexed, data [][]byte)
	SetCumulativeStepUsed(cumulativeUsed *big.Int)
	SetResult(status module.Status, used, price *big.Int, addr module.Address)
}
