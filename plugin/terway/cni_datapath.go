package main

import (
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/rpc"
)

// Only member-ENI allocations delegate VLAN handling to the node extension.
func usesTrunkDatapath(conf *types.CNIConf, alloc *rpc.NetConf, ipType rpc.IPType) bool {
	return conf.CiliumDatapath() && ipType == rpc.IPType_TypeENIMultiIP && alloc.GetENIInfo().GetTrunk()
}
