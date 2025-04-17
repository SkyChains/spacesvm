// Copyright (C) 2019-2021, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"github.com/skychains/chain/ids"
	"github.com/skychains/chain/utils/set"
)

type Mempool interface {
	Len() int
	Prune(set.Set[ids.ID])
	PopMax() (*Transaction, uint64)
	Add(*Transaction) bool
	NewTxs(uint64) []*Transaction
}
