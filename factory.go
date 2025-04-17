// Copyright (C) 2019-2021, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"github.com/skychains/chain/snow"
	"github.com/skychains/chain/vms"
	"github.com/skychains/spacesvm/vm"
)

var _ vms.Factory = &Factory{}

type Factory struct{}

func (f *Factory) New(*snow.Context) (interface{}, error) { return &vm.VM{}, nil }
