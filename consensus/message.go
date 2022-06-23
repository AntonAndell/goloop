package consensus

import (
	"fmt"
	"io"
	"time"

	"github.com/icon-project/goloop/common"
	"github.com/icon-project/goloop/common/codec"
	"github.com/icon-project/goloop/common/errors"
	"github.com/icon-project/goloop/module"
)

var msgCodec = codec.BC

const (
	ProtoProposal module.ProtocolInfo = iota << 8
	ProtoBlockPart
	ProtoVote
	ProtoRoundState
	ProtoVoteList
)

type protocolConstructor struct {
	proto       module.ProtocolInfo
	constructor func() Message
}

var protocolConstructors = [...]protocolConstructor{
	{ProtoProposal, func() Message { return NewProposalMessage() }},
	{ProtoBlockPart, func() Message { return newBlockPartMessage() }},
	{ProtoVote, func() Message { return newVoteMessage() }},
	{ProtoRoundState, func() Message { return newRoundStateMessage() }},
	{ProtoVoteList, func() Message { return newVoteListMessage() }},
}

func UnmarshalMessage(sp uint16, bs []byte) (Message, error) {
	for _, pc := range protocolConstructors {
		if sp == uint16(pc.proto) {
			msg := pc.constructor()
			if _, err := msgCodec.UnmarshalFromBytes(bs, msg); err != nil {
				return nil, err
			}
			return msg, nil
		}
	}
	return nil, errors.New("Unknown protocol")
}

type Message interface {
	Verify() error
	subprotocol() uint16
}

type _HR struct {
	Height int64
	Round  int32
}

func (hr *_HR) height() int64 {
	return hr.Height
}

func (hr *_HR) round() int32 {
	return hr.Round
}

func (hr *_HR) verify() error {
	if hr.Height <= 0 {
		return errors.Errorf("bad height %v", hr.Height)
	}
	if hr.Round < 0 {
		return errors.Errorf("bad round %v", hr.Round)
	}
	return nil
}

type proposal struct {
	_HR
	BlockPartSetID *PartSetID
	POLRound       int32
}

func (p *proposal) bytes() []byte {
	bs, err := msgCodec.MarshalToBytes(p)
	if err != nil {
		panic(err)
	}
	return bs
}

type ProposalMessage struct {
	signedBase
	proposal
}

func NewProposalMessage() *ProposalMessage {
	msg := &ProposalMessage{}
	msg.signedBase._byteser = msg
	return msg
}

func (msg *ProposalMessage) Verify() error {
	if err := msg._HR.verify(); err != nil {
		return err
	}
	if msg.BlockPartSetID.Count() <= 0 || msg.POLRound < -1 || msg.POLRound >= msg.Round {
		return errors.New("bad field value")
	}
	return msg.signedBase.verify()
}

func (msg *ProposalMessage) subprotocol() uint16 {
	return uint16(ProtoProposal)
}

func (msg *ProposalMessage) String() string {
	return fmt.Sprintf("ProposalMessage{H:%d R:%d BPSID:%v Addr:%v}", msg.Height, msg.Round, msg.BlockPartSetID, common.HexPre(msg.address().ID()))
}

type BlockPartMessage struct {
	// V1 Fields
	// for debugging
	Height int64
	Index  uint16

	BlockPart []byte

	// V2 Fields
	Nonce int32
}

func newBlockPartMessage() *BlockPartMessage {
	return &BlockPartMessage{}
}

func (msg *BlockPartMessage) Verify() error {
	if msg.Height <= 0 {
		return errors.Errorf("bad height %v", msg.Height)
	}
	return nil
}

func (msg *BlockPartMessage) subprotocol() uint16 {
	return uint16(ProtoBlockPart)
}

func (msg *BlockPartMessage) String() string {
	return fmt.Sprintf("BlockPartMessage{H:%d,I:%d}", msg.Height, msg.Index)
}

type blockVoteByteser struct {
	msg *voteMessage
}

func (v *blockVoteByteser) bytes() []byte {
	bv := struct {
		blockVoteBase
		Timestamp int64
	}{
		v.msg.blockVoteBase,
		v.msg.Timestamp,
	}
	return msgCodec.MustMarshalToBytes(&bv)
}

type voteMessage struct {
	signedBase
	voteBase
	Timestamp      int64
	NTSDProofParts [][]byte
}

func newVoteMessage() *voteMessage {
	msg := &voteMessage{}
	msg.signedBase._byteser = &blockVoteByteser{
		msg: msg,
	}
	return msg
}

func NewVoteMessage(
	w module.Wallet,
	voteType VoteType, height int64, round int32, id []byte,
	partSetID *PartSetID, ts int64,
	ntsHashEntries []module.NTSHashEntryFormat,
	ntsdProofParts [][]byte,
) *voteMessage {
	vm := newVoteMessage()
	vm.Height = height
	vm.Round = round
	vm.Type = voteType
	vm.BlockID = id
	vm.BlockPartSetID = partSetID
	vm.Timestamp = ts
	_ = vm.sign(w)
	for _, ntsHashEntry := range ntsHashEntries {
		vm.NTSVoteBases = append(vm.NTSVoteBases, ntsVoteBase(ntsHashEntry))
	}
	vm.NTSDProofParts = ntsdProofParts
	return vm
}

func NewPrecommitMessage(
	w module.Wallet,
	height int64, round int32, id []byte, partSetID *PartSetID, ts int64,
) *voteMessage {
	return NewVoteMessage(
		w, VoteTypePrecommit, height, round, id, partSetID, ts, nil, nil,
	)
}

func (msg *voteMessage) EqualExceptSigs(msg2 *voteMessage) bool {
	return msg.voteBase.Equal(&msg2.voteBase) && msg.Timestamp == msg2.Timestamp
}

func (msg *voteMessage) Verify() error {
	if err := msg._HR.verify(); err != nil {
		return err
	}
	if msg.Type < VoteTypePrevote || msg.Type > numberOfVoteTypes {
		return errors.New("bad field value")
	}
	if msg.Type == VoteTypePrevote && len(msg.NTSVoteBases) > 0 {
		return errors.Errorf(
			"prevote with NTSVotes len=%d", len(msg.NTSVoteBases),
		)
	}
	if len(msg.NTSVoteBases) != len(msg.NTSDProofParts) {
		return errors.Errorf("NTS loop len mismatch NTSVoteBasesLen=%d NTSDProofPartsLen=%d", len(msg.NTSVoteBases), len(msg.NTSDProofParts))
	}
	verifyProofCount := msg.Type == VoteTypePrecommit && msg.BlockPartSetID != nil
	if verifyProofCount && int(msg.BlockPartSetID.AppData()) != len(msg.NTSDProofParts) {
		return errors.Errorf("NTS loop len mismatch appData=%d NTSDProofPartsLen=%d", msg.BlockPartSetID.AppData(), len(msg.NTSDProofParts))
	}
	return msg.signedBase.verify()
}

func (msg *voteMessage) VerifyNTSDProofParts(
	pcm module.BTPProofContextMap,
	srcUID []byte,
	expValIndex int,
) error {
	for i, nvb := range msg.NTSVoteBases {
		pc, err := pcm.ProofContextFor(nvb.NetworkTypeID)
		if err != nil {
			return err
		}
		ntsd := pc.NewDecision(
			srcUID, nvb.NetworkTypeID,
			msg.Height, msg.Round, nvb.NetworkTypeSectionHash,
		)
		pp, err := pc.NewProofPartFromBytes(msg.NTSDProofParts[i])
		if err != nil {
			return err
		}
		idx, err := pc.VerifyPart(ntsd.Hash(), pp)
		if err != nil {
			return err
		}
		if expValIndex != idx {
			return errors.Errorf("invalid validator index exp=%d actual=%d ntid=%d", expValIndex, idx, nvb.NetworkTypeID)
		}
	}
	return nil
}

func (msg *voteMessage) subprotocol() uint16 {
	return uint16(ProtoVote)
}

func (msg *voteMessage) String() string {
	return fmt.Sprintf("VoteMessage{%s,H:%d,R:%d,BlockID:%v,Addr:%v}", msg.Type, msg.Height, msg.Round, common.HexPre(msg.BlockID), common.HexPre(msg.address().ID()))
}

func (msg *voteMessage) RLPEncodeSelf(e codec.Encoder) error {
	e2, err := e.EncodeList()
	if err != nil {
		return err
	}
	err = e2.EncodeMulti(
		&msg.Signature,
		&msg.Height,
		&msg.Round,
		&msg.Type,
		&msg.BlockID,
		&msg.BlockPartSetID,
		&msg.Timestamp,
	)
	if err != nil {
		return err
	}
	if len(msg.NTSVoteBases) == 0 {
		return nil
	}
	e3, err := e2.EncodeList()
	if err != nil {
		return err
	}
	for i, ntsVote := range msg.NTSVoteBases {
		err = e3.EncodeMulti(
			&ntsVote.NetworkTypeID,
			&ntsVote.NetworkTypeSectionHash,
			msg.NTSDProofParts[i],
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (msg *voteMessage) RLPDecodeSelf(d codec.Decoder) error {
	d2, err := d.DecodeList()
	if err != nil {
		return err
	}
	var ntsVotes []struct {
		ntsVoteBase
		NTSDProofPart []byte
	}
	cnt, err := d2.DecodeMulti(
		&msg.Signature,
		&msg.Height,
		&msg.Round,
		&msg.Type,
		&msg.BlockID,
		&msg.BlockPartSetID,
		&msg.Timestamp,
		&ntsVotes,
	)
	if cnt == 7 && err == io.EOF {
		msg.NTSVoteBases = nil
		msg.NTSDProofParts = nil
		return nil
	}
	msg.NTSVoteBases = make([]ntsVoteBase, 0, len(ntsVotes))
	msg.NTSDProofParts = make([][]byte, 0, len(ntsVotes))
	for _, ntsVote := range ntsVotes {
		msg.NTSVoteBases = append(msg.NTSVoteBases, ntsVote.ntsVoteBase)
		msg.NTSDProofParts = append(msg.NTSDProofParts, ntsVote.NTSDProofPart)
	}
	return err
}

type peerRoundState struct {
	_HR
	PrevotesMask   *bitArray
	PrecommitsMask *bitArray
	BlockPartsMask *bitArray
	Sync           bool
}

func (prs peerRoundState) String() string {
	return fmt.Sprintf("PeerRoundState{H:%v R:%v PV:%v PC:%v BP:%v Sync:%t}", prs.Height, prs.Round, prs.PrevotesMask, prs.PrecommitsMask, prs.BlockPartsMask, prs.Sync)
}

type RoundStateMessage struct {
	peerRoundState
	Timestamp int64
	// TODO: add LastMaskType, LastIndex
}

func (msg RoundStateMessage) String() string {
	return fmt.Sprintf("PeerRoundStateMessage{H:%v R:%v PV:%v PC:%v BP:%v Sync:%t}", msg.Height, msg.Round, msg.PrevotesMask, msg.PrecommitsMask, msg.BlockPartsMask, msg.Sync)
}

func newRoundStateMessage() *RoundStateMessage {
	return &RoundStateMessage{
		Timestamp: time.Now().UnixNano(),
	}
}

func (msg *RoundStateMessage) Verify() error {
	if err := msg.peerRoundState._HR.verify(); err != nil {
		return err
	}
	return nil
}

func (msg *RoundStateMessage) subprotocol() uint16 {
	return uint16(ProtoRoundState)
}

type voteListMessage struct {
	VoteList *voteList
}

func newVoteListMessage() *voteListMessage {
	return &voteListMessage{}
}

func (msg *voteListMessage) Verify() error {
	if msg.VoteList == nil {
		return errors.Errorf("nil VoteList")
	}
	return nil
}

func (msg voteListMessage) String() string {
	return fmt.Sprintf("VoteListMessage%+v", msg.VoteList)
}

func (msg *voteListMessage) subprotocol() uint16 {
	return uint16(ProtoVoteList)
}
