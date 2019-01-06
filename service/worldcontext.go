package service

import (
	"math/big"

	"github.com/icon-project/goloop/common/codec"

	"github.com/icon-project/goloop/common"
	"github.com/icon-project/goloop/module"
	"github.com/icon-project/goloop/service/eeproxy"
	"github.com/icon-project/goloop/service/scoredb"
)

const (
	VarStepPrice  = "step_price"
	VarStepCosts  = "step_costs"
	VarStepTypes  = "step_types"
	VarTreasury   = "treasury"
	VarGovernance = "governance"
	VarSystem     = "system"
)

var (
	SystemID = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
)

type worldContext struct {
	WorldState

	treasury   module.Address
	governance module.Address
	system     module.Address

	systemInfo systemStorageInfo

	blockInfo BlockInfo
	txInfo    TransactionInfo

	info map[string]interface{}

	cm ContractManager
	em eeproxy.Manager
}

func (c *worldContext) WorldVirtualState() WorldVirtualState {
	if wvs, ok := c.WorldState.(WorldVirtualState); ok {
		return wvs
	}
	return NewWorldVirtualState(c.WorldState, nil)
}

func (c *worldContext) GetFuture(lq []LockRequest) WorldContext {
	wvs := c.WorldVirtualState()
	if len(lq) == 0 {
		return c.WorldStateChanged(wvs)
	} else {
		lq2 := make([]LockRequest, len(lq)+1)
		copy(lq2, lq)
		lq2[len(lq)] = LockRequest{
			Lock: AccountReadLock,
			ID:   string(c.system.ID()),
		}
		return c.WorldStateChanged(wvs.GetFuture(lq2))
	}
}

type systemStorageInfo struct {
	updated      bool
	ass          AccountSnapshot
	stepPrice    *big.Int
	stepCosts    map[string]int64
	stepCostInfo *codec.TypedObj
}

func (c *worldContext) updateSystemInfo() {
	if !c.systemInfo.updated {
		ass := c.GetAccountSnapshot(c.system.ID())
		if c.systemInfo.ass == nil || ass.StorageChangedAfter(c.systemInfo.ass) {
			c.systemInfo.ass = ass

			as := newAccountROState(ass)

			stepPrice := scoredb.NewVarDB(as, VarStepPrice).BigInt()
			if stepPrice == nil {
				stepPrice = version2StepPrice
			}
			c.systemInfo.stepPrice = stepPrice

			stepCosts := make(map[string]int64)
			stepTypes := scoredb.NewArrayDB(as, VarStepTypes)
			stepCostDB := scoredb.NewDictDB(as, VarStepCosts, 1)
			tcount := stepTypes.Size()
			for i := 0; i < tcount; i++ {
				tname := stepTypes.Get(i).String()
				stepCosts[tname] = stepCostDB.Get(tname).Int64()
			}
			c.systemInfo.stepCosts = stepCosts
			c.systemInfo.stepCostInfo = nil
		}
		c.systemInfo.updated = true
	}
}

func (c *worldContext) StepsFor(t StepType, n int) int64 {
	c.updateSystemInfo()
	if v, ok := c.systemInfo.stepCosts[string(t)]; ok {
		return v * int64(n)
	} else {
		return 0
	}
}

func (c *worldContext) StepPrice() *big.Int {
	c.updateSystemInfo()
	return c.systemInfo.stepPrice
}

func (c *worldContext) BlockTimeStamp() int64 {
	return c.blockInfo.Timestamp
}

func (c *worldContext) BlockHeight() int64 {
	return c.blockInfo.Height
}

func (c *worldContext) GetBlockInfo(bi *BlockInfo) {
	*bi = c.blockInfo
}

func (c *worldContext) Treasury() module.Address {
	return c.treasury
}

func (c *worldContext) Governance() module.Address {
	return c.governance
}

func (c *worldContext) ContractManager() ContractManager {
	return c.cm
}

func (c *worldContext) EEManager() eeproxy.Manager {
	return c.em
}

func (c *worldContext) WorldStateChanged(ws WorldState) WorldContext {
	wc := &worldContext{
		WorldState: ws,
		treasury:   c.treasury,
		governance: c.governance,
		systemInfo: c.systemInfo,
		blockInfo:  c.blockInfo,

		cm: c.cm,
		em: c.em,
	}
	wc.systemInfo.updated = false
	return wc
}

func (c *worldContext) SetTransactionInfo(ti *TransactionInfo) {
	c.txInfo = *ti
	c.info = nil
}

func (c *worldContext) GetTransactionInfo(ti *TransactionInfo) {
	*ti = c.txInfo
}

func (c *worldContext) stepCostInfo() interface{} {
	c.updateSystemInfo()
	if c.systemInfo.stepCostInfo == nil {
		c.systemInfo.stepCostInfo = common.MustEncodeAny(c.systemInfo.stepCosts)
	}
	return c.systemInfo.stepCostInfo
}

func (c *worldContext) GetInfo() map[string]interface{} {
	if c.info == nil {
		m := make(map[string]interface{})
		m["B.height"] = c.blockInfo.Height
		m["B.timestamp"] = c.blockInfo.Timestamp
		m["T.index"] = c.txInfo.Index
		m["T.timestamp"] = c.txInfo.Timestamp
		m["T.nonce"] = c.txInfo.Nonce
		m["StepCosts"] = c.stepCostInfo()
		c.info = m
	}
	return c.info
}

func NewWorldContext(ws WorldState, ts int64, height int64, cm ContractManager,
	em eeproxy.Manager,
) WorldContext {
	var system, governance, treasury module.Address
	ass := ws.GetAccountSnapshot(SystemID)
	as := newAccountROState(ass)
	if as != nil {
		treasury = scoredb.NewVarDB(as, VarTreasury).Address()
		governance = scoredb.NewVarDB(as, VarGovernance).Address()
		system = scoredb.NewVarDB(as, VarSystem).Address()
	}
	if treasury == nil {
		treasury = common.NewAddressFromString("hx1000000000000000000000000000000000000000")
	}
	if governance == nil {
		governance = common.NewAddressFromString("cx0000000000000000000000000000000000000001")
	}
	if system == nil {
		system = common.NewAddressFromString("cx0000000000000000000000000000000000000000")
	}
	return &worldContext{
		WorldState: ws,
		treasury:   treasury,
		governance: governance,
		system:     system,
		blockInfo:  BlockInfo{Timestamp: ts, Height: height},

		cm: cm,
		em: em,
	}
}
