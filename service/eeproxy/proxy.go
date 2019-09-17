package eeproxy

import (
	"math/big"
	"sync"

	"github.com/icon-project/goloop/common/log"

	"github.com/gofrs/uuid"

	"github.com/icon-project/goloop/common"
	"github.com/icon-project/goloop/common/codec"
	"github.com/icon-project/goloop/common/errors"
	"github.com/icon-project/goloop/common/ipc"
	"github.com/icon-project/goloop/module"
	"github.com/icon-project/goloop/service/scoreapi"
)

type Message uint

const (
	msgVERSION    = 0
	msgINVOKE     = 1
	msgRESULT     = 2
	msgGETVALUE   = 3
	msgSETVALUE   = 4
	msgCALL       = 5
	msgEVENT      = 6
	msgGETINFO    = 7
	msgGETBALANCE = 8
	msgGETAPI     = 9
	msgLOG        = 10
	msgCLOSE      = 11
)

const (
	ModuleName = "eeproxy"
)

type CallContext interface {
	GetValue(key []byte) ([]byte, error)
	SetValue(key, value []byte) error
	DeleteValue(key []byte) error
	GetInfo() *codec.TypedObj
	GetBalance(addr module.Address) *big.Int
	OnEvent(addr module.Address, indexed, data [][]byte)
	OnResult(status uint16, steps *big.Int, result *codec.TypedObj)
	OnCall(from, to module.Address, value, limit *big.Int, method string, params *codec.TypedObj)
	OnAPI(status uint16, obj *scoreapi.Info)
}

type Proxy interface {
	Invoke(ctx CallContext, code string, isQuery bool, from, to module.Address, value, limit *big.Int, method string, params *codec.TypedObj) error
	SendResult(ctx CallContext, status uint16, steps *big.Int, result *codec.TypedObj) error
	GetAPI(ctx CallContext, code string) error
	Release()
	Kill() error
}

type proxyManager interface {
	onReady(t string, p *proxy) error
	kill(u string) error
}

type callFrame struct {
	addr module.Address
	ctx  CallContext

	prev *callFrame
}

type proxy struct {
	lock     sync.Mutex
	reserved bool
	mgr      proxyManager

	conn ipc.Connection

	version   uint16
	uid       string
	scoreType string

	log log.Logger

	frame *callFrame

	next  *proxy
	pprev **proxy
}

type versionMessage struct {
	Version uint16 `codec:"version"`
	UID     string
	Type    string
}

type invokeMessage struct {
	Code   string `codec:"code"`
	IsQry  bool
	From   *common.Address `codec:"from"`
	To     common.Address  `codec:"to"`
	Value  common.HexInt   `codec:"value"`
	Limit  common.HexInt   `codec:"limit"`
	Method string          `codec:"method"`
	Params *codec.TypedObj `codec:"params"`
}

type getValueMessage struct {
	Success bool
	Value   []byte
}

type setValueMessage struct {
	Key      []byte `codec:"key"`
	IsDelete bool
	Value    []byte `codec:"value"`
}

type callMessage struct {
	To     common.Address
	Value  common.HexInt
	Limit  common.HexInt
	Method string
	Params *codec.TypedObj
}

type eventMessage struct {
	Indexed [][]byte
	Data    [][]byte
}

type getAPIMessage struct {
	Status uint16
	Info   *scoreapi.Info
}

type logMessage struct {
	Level   log.Level
	Message string
}

func (p *proxy) Invoke(ctx CallContext, code string, isQuery bool, from, to module.Address, value, limit *big.Int, method string, params *codec.TypedObj) error {
	var m invokeMessage
	m.Code = code
	m.IsQry = isQuery
	if from != nil {
		m.From = common.NewAddress(from.Bytes())
	}
	m.To.SetBytes(to.Bytes())
	m.Value.Set(value)
	m.Limit.Set(limit)
	m.Method = method
	m.Params = params

	p.log.Tracef("Proxy[%p].Invoke code=%s query=%v from=%v to=%v value=%v limit=%v method=%s\n", p, code, isQuery, from, to, value, limit, method)

	p.lock.Lock()
	defer p.lock.Unlock()
	p.frame = &callFrame{
		addr: to,
		ctx:  ctx,
		prev: p.frame,
	}
	return p.conn.Send(msgINVOKE, &m)
}

func (p *proxy) GetAPI(ctx CallContext, code string) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.frame = &callFrame{
		addr: nil,
		ctx:  ctx,
		prev: p.frame,
	}
	return p.conn.Send(msgGETAPI, code)
}

type resultMessage struct {
	Status   uint16
	StepUsed common.HexInt
	Result   *codec.TypedObj
}

func (p *proxy) reserve() bool {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.reserved {
		return false
	}
	p.reserved = true
	return true
}

func (p *proxy) Release() {
	p.lock.Lock()
	if !p.reserved {
		p.lock.Unlock()
		return
	}
	p.reserved = false
	if p.frame == nil {
		p.lock.Unlock()
		if err := p.mgr.onReady(p.scoreType, p); err != nil {
			p.close()
		}
		return
	}
	p.lock.Unlock()
}

func (p *proxy) SendResult(ctx CallContext, status uint16, steps *big.Int, result *codec.TypedObj) error {
	p.log.Tracef("Proxy[%p].SendResult status=%s steps=%v", p, module.Status(status), steps)
	var m resultMessage
	m.Status = status
	m.StepUsed.Set(steps)
	if result == nil {
		result = codec.Nil
	}
	m.Result = result
	return p.conn.Send(msgRESULT, &m)
}

func (p *proxy) popFrame() *callFrame {
	p.lock.Lock()
	defer p.lock.Unlock()

	frame := p.frame
	p.frame = frame.prev

	return frame
}

func (p *proxy) tryToBeReady() error {
	p.lock.Lock()
	if p.frame == nil && !p.reserved {
		p.lock.Unlock()
		return p.mgr.onReady(p.scoreType, p)
	} else {
		p.lock.Unlock()
	}
	return nil
}

func (p *proxy) HandleMessage(c ipc.Connection, msg uint, data []byte) error {
	switch msg {
	case msgRESULT:
		var m resultMessage
		if _, err := codec.MP.UnmarshalFromBytes(data, &m); err != nil {
			return err
		}
		p.log.Tracef("Proxy[%p].OnResult status=%s steps=%v", p, module.Status(m.Status), &m.StepUsed.Int)

		frame := p.popFrame()

		frame.ctx.OnResult(m.Status, &m.StepUsed.Int, m.Result)

		return p.tryToBeReady()
	case msgGETVALUE:
		var key []byte
		if _, err := codec.MP.UnmarshalFromBytes(data, &key); err != nil {
			return err
		}
		var m getValueMessage
		if value, err := p.frame.ctx.GetValue(key); err != nil {
			p.log.Tracef("Proxy[%p].GetValue key=<%x> err=%+v", p, key, err)
			return err
		} else {
			if value != nil {
				m.Success = true
				m.Value = value
			} else {
				m.Success = false
				m.Value = nil
			}
			p.log.Tracef("Proxy[%p].GetValue key=<%x> value=<%x>", p, key, value)
		}
		return p.conn.Send(msgGETVALUE, &m)

	case msgSETVALUE:
		var m setValueMessage
		if _, err := codec.MP.UnmarshalFromBytes(data, &m); err != nil {
			return err
		}
		if m.IsDelete {
			p.log.Tracef("Proxy[%p].Delete key=<%x>", p, m.Key)
			return p.frame.ctx.DeleteValue(m.Key)
		} else {
			p.log.Tracef("Proxy[%p].SetValue key=<%x> value=<%x>", p, m.Key, m.Value)
			return p.frame.ctx.SetValue(m.Key, m.Value)
		}

	case msgCALL:
		var m callMessage
		if _, err := codec.MP.UnmarshalFromBytes(data, &m); err != nil {
			return err
		}
		p.log.Tracef("Proxy[%p].OnCall from=%v to=%v value=%v steplimit=%v method=%s\n",
			p, p.frame.addr, &m.To, &m.Value.Int, &m.Limit.Int, m.Method)
		p.frame.ctx.OnCall(p.frame.addr,
			&m.To, &m.Value.Int, &m.Limit.Int, m.Method, m.Params)
		return nil

	case msgEVENT:
		var m eventMessage
		if _, err := codec.MP.UnmarshalFromBytes(data, &m); err != nil {
			return err
		}
		p.log.Tracef("Proxy[%p].OnEvent from=%v indexed=%v data=%v",
			p, p.frame.addr, m.Indexed, m.Data)
		p.frame.ctx.OnEvent(p.frame.addr, m.Indexed, m.Data)
		return nil

	case msgGETINFO:
		p.log.Tracef("Proxy[%p].GetInfo()", p)
		v := p.frame.ctx.GetInfo()
		eo, err := common.EncodeAny(v)
		if err != nil {
			return err
		}
		return p.conn.Send(msgGETINFO, eo)

	case msgGETBALANCE:
		var addr common.Address
		if _, err := codec.MP.UnmarshalFromBytes(data, &addr); err != nil {
			return err
		}
		var balance common.HexInt
		balance.Set(p.frame.ctx.GetBalance(&addr))
		p.log.Tracef("Proxy[%p].GetBalance(%s) -> %s",
			p, &addr, &balance)
		return p.conn.Send(msgGETBALANCE, &balance)

	case msgGETAPI:
		var m getAPIMessage
		if _, err := codec.MP.UnmarshalFromBytes(data, &m); err != nil {
			return err
		}
		p.log.Tracef("Proxy[%p].OnAPI status=%s, info=%s",
			p, module.Status(m.Status), m.Info)

		frame := p.popFrame()
		frame.ctx.OnAPI(m.Status, m.Info)
		return p.tryToBeReady()

	case msgLOG:
		var m logMessage
		if _, err := codec.MP.UnmarshalFromBytes(data, &m); err != nil {
			return err
		}

		p.lock.Lock()
		defer p.lock.Unlock()

		if p.frame != nil && p.frame.addr != nil {
			p.log.Log(m.Level, p.scoreType, "|",
				common.StrLeft(10, p.frame.addr.String()), "|", m.Message)
		} else {
			p.log.Log(m.Level, p.scoreType, "|", m.Message)
		}
		return nil
	default:
		p.log.Warnf("UnknownMessage(%d)", msg)
		return errors.ErrIllegalArgument
	}
}

func (p *proxy) close() error {
	p.conn.Send(msgCLOSE, nil)
	return p.conn.Close()
}

func (p *proxy) Kill() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.mgr.kill(p.uid)
}

func (p *proxy) detach() bool {
	if p.pprev == nil {
		return false
	}
	*p.pprev = p.next
	if p.next != nil {
		p.next.pprev = p.pprev
	}
	p.pprev = nil
	p.next = nil
	return true
}

func (p *proxy) attachTo(r **proxy) {
	p.next = *r
	if p.next != nil {
		p.next.pprev = &p.next
	}
	p.pprev = r
	*r = p
}

func newProxy(m proxyManager, c ipc.Connection, l log.Logger, t string, v uint16, uid string) (*proxy, error) {
	p := &proxy{
		mgr:  m,
		conn: c,
		log:  l,

		scoreType: t,
		version:   v,
		uid:       uid,
	}
	c.SetHandler(msgRESULT, p)
	c.SetHandler(msgGETVALUE, p)
	c.SetHandler(msgSETVALUE, p)
	c.SetHandler(msgCALL, p)
	c.SetHandler(msgEVENT, p)
	c.SetHandler(msgGETINFO, p)
	c.SetHandler(msgGETBALANCE, p)
	c.SetHandler(msgGETAPI, p)
	c.SetHandler(msgLOG, p)

	if err := m.onReady(t, p); err != nil {
		return nil, err
	}
	p.log = p.log.WithFields(log.Fields{
		log.FieldKeyEID: p.uid,
	})
	return p, nil
}

func newUID() string {
	return uuid.Must(uuid.NewV4()).String()
}
