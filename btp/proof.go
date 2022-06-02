/*
 * Copyright 2022 ICON Foundation
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package btp

import (
	"github.com/icon-project/goloop/btp/ntm"
	"github.com/icon-project/goloop/common/errors"
	"github.com/icon-project/goloop/module"
)

type proofContextMap struct {
	pcMap map[int64]module.BTPProofContext
}

func (m *proofContextMap) ProofContextFor(ntid int64) (module.BTPProofContext, error) {
	pc, ok := m.pcMap[ntid]
	if !ok {
		return nil, errors.Errorf("not found ntid=%d", ntid)
	}
	return pc, nil
}

func (m *proofContextMap) Update(btpSection module.BTPSection) module.BTPProofContextMap {
	res := m
	for _, nts := range btpSection.NetworkTypeSections() {
		if nts.(*networkTypeSectionByBuilder).nextProofContextChanged() {
			if res == m {
				res = &proofContextMap{
					pcMap: make(map[int64]module.BTPProofContext),
				}
				for k, v := range m.pcMap {
					res.pcMap[k] = v
				}
			}
			res.pcMap[nts.NetworkTypeID()] = nts.NextProofContext()
		}
	}
	return res
}

func (m *proofContextMap) Verify(
	srcUID []byte,
	height int64,
	round int32,
	bd module.BTPDigest,
	ntsdProves [][]byte,
) error {
	if len(bd.NetworkTypeDigests()) != len(ntsdProves) {
		return errors.Errorf("invalid len networkTypeLen=%d provesLen=%d height=%d round=%d", len(bd.NetworkTypeDigests()), len(ntsdProves), height, round)
	}
	for i, ntd := range bd.NetworkTypeDigests() {
		ntid := ntd.NetworkTypeID()
		pc, ok := m.pcMap[ntid]
		if !ok {
			return errors.InvalidStateError.Errorf(
				"no ProofContext in PCM index=%d ntid=%d height=%d round=%d",
				i, ntid, height, round,
			)
		}
		d := pc.NewDecision(srcUID, ntid, height, round, ntd.NetworkTypeSectionHash())
		proof, err := pc.NewProofFromBytes(ntsdProves[i])
		if err != nil {
			return errors.Wrapf(
				err, "new proof fail index=%d ntid=%d height=%d round=%d",
				i, ntid, height, round,
			)
		}
		err = pc.Verify(d.Hash(), proof)
		if err != nil {
			return errors.Wrapf(
				err, "verify fail index=%d ntid=%d height=%d round=%d",
				i, ntid, height, round,
			)
		}
	}
	return nil
}

func NewProofContextsMap(view StateView) (module.BTPProofContextMap, error) {
	res := &proofContextMap{
		pcMap: make(map[int64]module.BTPProofContext),
	}
	ntidSlice, err := view.GetNetworkTypeIDs()
	if err != nil {
		return nil, err
	}
	for _, ntid := range ntidSlice {
		nt, err := view.GetNetworkTypeView(ntid)
		if err != nil {
			return nil, err
		}
		mod := ntm.ForUID(nt.UID())
		pc, err := mod.NewProofContextFromBytes(nt.NextProofContext())
		if err != nil {
			return nil, err
		}
		res.pcMap[ntid] = pc
	}
	return res, nil
}

var ZeroProofContextMap = &proofContextMap{
	pcMap: make(map[int64]module.BTPProofContext),
}
