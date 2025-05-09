package vm

import (
	_ "embed"
	"encoding/json"
	"testing"

	luxdVersion "github.com/skychains/chain/version"
	spacesVersion "github.com/skychains/spacesvm/version"
	"github.com/stretchr/testify/assert"
)

type rpcChainCompatibility struct {
	RPCChainVMProtocolVersion map[string]uint `json:"rpcChainVMProtocolVersion"`
}

//go:embed compatibility.json
var rpcChainVMProtocolCompatibilityBytes []byte

func TestCompatibility(t *testing.T) {
	var compat rpcChainCompatibility
	err := json.Unmarshal(rpcChainVMProtocolCompatibilityBytes, &compat)
	assert.NoError(t, err)

	currentSpacesRPC, valueInJSON := compat.RPCChainVMProtocolVersion[spacesVersion.Version.String()]
	assert.True(t, valueInJSON)

	assert.Equal(t, luxdVersion.RPCChainVMProtocol, currentSpacesRPC)
}
