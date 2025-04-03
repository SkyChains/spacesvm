// Copyright (C) 2019-2021, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"github.com/SkyChains/chain/ids"
	"github.com/SkyChains/chain/utils/set"
)

type Mempool interface {
	Len() int
	Prune(set.Set[ids.ID])
	PopMax() (*Transaction, uint64)
	Add(*Transaction) bool
	NewTxs(uint64) []*Transaction
}
