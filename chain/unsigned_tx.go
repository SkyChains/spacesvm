// Copyright (C) 2019-2021, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"github.com/skychains/chain/database"
	"github.com/skychains/chain/ids"
	"github.com/ethereum/go-ethereum/common"

	"github.com/skychains/spacesvm/tdata"
)

type TransactionContext struct {
	Genesis   *Genesis
	Database  database.Database
	BlockTime uint64
	TxID      ids.ID
	Sender    common.Address
}

type UnsignedTransaction interface {
	Copy() UnsignedTransaction
	GetBlockID() ids.ID
	GetMagic() uint64
	GetPrice() uint64
	SetBlockID(ids.ID)
	SetMagic(uint64)
	SetPrice(uint64)
	FeeUnits(*Genesis) uint64  // number of units to mine tx
	LoadUnits(*Genesis) uint64 // units that should impact fee rate

	ExecuteBase(*Genesis) error
	Execute(*TransactionContext) error
	TypedData() *tdata.TypedData
	Activity() *Activity
}
