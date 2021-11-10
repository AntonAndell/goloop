package iiss

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/icon-project/goloop/common"
	"github.com/icon-project/goloop/common/db"
	"github.com/icon-project/goloop/icon/ictest"
	"github.com/icon-project/goloop/icon/iiss/icutils"
	"github.com/icon-project/goloop/module"
	"github.com/icon-project/goloop/service/state"
)

func newWorldContext() state.WorldContext {
	dbase := db.NewMapDB()
	plt := ictest.NewPlatform()
	ws := state.NewWorldState(dbase, nil, nil, nil)
	return state.NewWorldContext(ws, nil, nil, plt)
}

func TestWorldContextImpl_GetBalance(t *testing.T) {
	address := common.MustNewAddressFromString("hx1")
	wc := newWorldContext()
	iwc := NewWorldContext(wc)

	initBalance := icutils.ToLoop(100)
	as := wc.GetAccountState(address.ID())
	as.SetBalance(initBalance)
	assert.Zero(t, as.GetBalance().Cmp(initBalance))
	assert.Zero(t, iwc.GetBalance(address).Cmp(initBalance))
}

func TestWorldContextImpl_Deposit(t *testing.T) {
	var err error
	address := common.MustNewAddressFromString("hx1")
	wc := newWorldContext()
	iwc := NewWorldContext(wc)

	balance := iwc.GetBalance(address)
	assert.NotNil(t, balance)
	assert.Zero(t, balance.Int64())

	var sum int64
	for i := int64(0); i < 10; i++ {
		amount := big.NewInt(i)
		err = iwc.Deposit(address, amount)
		assert.NoError(t, err)

		sum += i
		balance = iwc.GetBalance(address)
		assert.Equal(t, sum, balance.Int64())
	}

	err = iwc.Deposit(address, big.NewInt(-100))
	assert.Error(t, err)

	err = iwc.Deposit(address, big.NewInt(0))
	assert.NoError(t, err)
	assert.Equal(t, sum, balance.Int64())
}

func TestWorldContextImpl_Withdraw(t *testing.T) {
	var err error
	address := common.MustNewAddressFromString("hx1")
	wc := newWorldContext()
	iwc := NewWorldContext(wc)

	balance := iwc.GetBalance(address)
	assert.NotNil(t, balance)
	assert.Zero(t, balance.Int64())

	expectedBalance := int64(50)
	err = iwc.Deposit(address, big.NewInt(expectedBalance))
	assert.NoError(t, err)

	// Subtract 100 from 50
	err = iwc.Withdraw(address, big.NewInt(100))
	assert.Error(t, err)

	for i := 0; i < 5; i++ {
		err = iwc.Withdraw(address, big.NewInt(10))
		assert.NoError(t, err)

		expectedBalance -= 10
		balance = iwc.GetBalance(address)
		assert.Equal(t, expectedBalance, balance.Int64())
	}
	assert.Zero(t, balance.Sign())

	// Negative amount is not allowed
	err = iwc.Withdraw(address, big.NewInt(-100))
	assert.Error(t, err)

	// Subtract 100 from 0
	err = iwc.Withdraw(address, big.NewInt(100))
	assert.Error(t, err)
}

func TestWorldContextImpl_Transfer(t *testing.T) {
	var err error
	from := common.MustNewAddressFromString("hx1")
	to := common.MustNewAddressFromString("hx2")
	wc := newWorldContext()
	iwc := NewWorldContext(wc)

	initBalance := int64(100)
	err = iwc.Deposit(from, big.NewInt(initBalance))
	assert.NoError(t, err)
	err = iwc.Deposit(to, big.NewInt(initBalance))
	assert.NoError(t, err)

	// transfer 30 from "from" to "to"
	// from: 100 - 30 = 70
	// to: 100 + 30 = 130
	err = iwc.Transfer(from, to, big.NewInt(30))
	assert.NoError(t, err)
	assert.Zero(t, big.NewInt(70).Cmp(iwc.GetBalance(from)))
	assert.Zero(t, big.NewInt(130).Cmp(iwc.GetBalance(to)))
}

func TestWorldContextImpl_TotalSupply(t *testing.T) {
	var err error
	wc := newWorldContext()
	iwc := NewWorldContext(wc)

	ts := iwc.GetTotalSupply()
	assert.NotNil(t, ts)
	assert.Zero(t, ts.Sign())

	sum := new(big.Int)
	amount := icutils.ToLoop(100)
	for i := 0; i < 10; i++ {
		ts, err = iwc.AddTotalSupply(amount)
		assert.NoError(t, err)
		sum.Add(sum, amount)
		assert.Zero(t, ts.Cmp(sum))
	}
	assert.Zero(t, ts.Cmp(iwc.GetTotalSupply()))
}

func TestWorldContextImpl_SetScoreOwner_SanityCheck(t *testing.T) {
	var err error
	from := common.MustNewAddressFromString("hx1")
	score := common.MustNewAddressFromString("cx1")
	owner := common.MustNewAddressFromString("hx2")

	wc := NewWorldContext(newWorldContext())

	// Case: from is nil
	err = wc.SetScoreOwner(nil, score, owner)
	assert.Error(t, err)

	invalidScores := []module.Address{
		nil, common.MustNewAddressFromString("hx3"),
	}
	for _, invalidScore := range invalidScores {
		err = wc.SetScoreOwner(from, invalidScore, owner)
		assert.Error(t, err)
	}

	invalidOwners := []module.Address{nil}
	for _, invalidOwner := range invalidOwners {
		err = wc.SetScoreOwner(from, score, invalidOwner)
		assert.Error(t, err)
	}

	err = wc.SetScoreOwner(from, score, owner)
	assert.Error(t, err)
}
