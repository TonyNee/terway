package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/rpc"
)

func TestUsesTrunkDatapath(t *testing.T) {
	for _, mode := range []types.VlanStripType{types.VlanStripTypeFilter, types.VlanStripTypeVlan, types.VlanStripTypeCilium} {
		for _, ipType := range []rpc.IPType{rpc.IPType_TypeVPCIP, rpc.IPType_TypeVPCENI, rpc.IPType_TypeENIMultiIP} {
			for _, trunk := range []bool{false, true} {
				conf := &types.CNIConf{VlanStripType: mode}
				alloc := &rpc.NetConf{ENIInfo: &rpc.ENIInfo{Trunk: trunk}}
				require.Equal(t, mode == types.VlanStripTypeCilium && ipType == rpc.IPType_TypeENIMultiIP && trunk, usesTrunkDatapath(conf, alloc, ipType))
			}
		}
	}
}
