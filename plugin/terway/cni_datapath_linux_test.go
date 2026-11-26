package main

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/stretchr/testify/require"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/rpc"
)

func TestParseCiliumDatapathSetup(t *testing.T) {
	patches := gomonkey.ApplyFunc(link.GetDeviceNumber, func(string) (int32, error) { return 10, nil })
	defer patches.Reset()
	for _, trunk := range []bool{false, true} {
		conf := &types.CNIConf{ENIIPVirtualType: "datapathv2", VlanStripType: types.VlanStripTypeCilium}
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{IPv4: "10.96.0.0/12"},
				PodIP:       &rpc.IPSet{IPv4: "192.0.2.10"},
				PodCIDR:     &rpc.IPSet{IPv4: "192.0.2.0/24"},
				GatewayIP:   &rpc.IPSet{IPv4: "192.0.2.1"},
			},
			ENIInfo: &rpc.ENIInfo{MAC: "02:00:00:00:00:01", Trunk: trunk, GatewayIP: &rpc.IPSet{IPv4: "192.0.2.1"}},
		}
		if trunk {
			alloc.ENIInfo.Vid = 100
		}
		cfg, err := parseSetupConf(context.Background(), &skel.CmdArgs{IfName: "eth0"}, alloc, conf, rpc.IPType_TypeENIMultiIP)
		require.NoError(t, err)
		require.Equal(t, trunk, cfg.CiliumDatapath)
		require.Equal(t, trunk, cfg.StripVlan)
		if trunk {
			require.Equal(t, types.PolicyRoute, cfg.DP)
		} else {
			baseline := *conf
			baseline.VlanStripType = types.VlanStripTypeFilter
			previous, err := parseSetupConf(context.Background(), &skel.CmdArgs{IfName: "eth0"}, alloc, &baseline, rpc.IPType_TypeENIMultiIP)
			require.NoError(t, err)
			require.Equal(t, previous, cfg)
		}
		require.Equal(t, int(alloc.ENIInfo.Vid), cfg.Vid)
		_, err = parseSetupConf(context.Background(), &skel.CmdArgs{IfName: "eth0"}, alloc, conf, rpc.IPType_TypeVPCENI)
		require.NoError(t, err) // Exclusive allocations keep their original setup path.
		alloc.ENIInfo.ERDMA = true
		_, err = parseSetupConf(context.Background(), &skel.CmdArgs{IfName: "eth0"}, alloc, conf, rpc.IPType_TypeENIMultiIP)
		if trunk {
			require.ErrorContains(t, err, "RDMA or VF")
		} else {
			require.NoError(t, err)
		}
	}
}

func TestOrdinarySetupUnchangedByTrunkDatapath(t *testing.T) {
	patches := gomonkey.ApplyFunc(link.GetDeviceNumber, func(string) (int32, error) { return 10, nil })
	defer patches.Reset()
	for _, mode := range []string{"veth", "ipvlan", "datapathv2"} {
		for _, ipType := range []rpc.IPType{rpc.IPType_TypeENIMultiIP, rpc.IPType_TypeVPCENI} {
			alloc := &rpc.NetConf{
				IfName: "eth1",
				BasicInfo: &rpc.BasicInfo{
					ServiceCIDR: &rpc.IPSet{IPv4: "10.96.0.0/12"}, PodIP: &rpc.IPSet{IPv4: "192.0.2.10"},
					PodCIDR: &rpc.IPSet{IPv4: "192.0.2.0/24"}, GatewayIP: &rpc.IPSet{IPv4: "192.0.2.1"},
				},
				ENIInfo: &rpc.ENIInfo{MAC: "02:00:00:00:00:01", GatewayIP: &rpc.IPSet{IPv4: "192.0.2.1"}},
			}
			for _, rdma := range []bool{false, true} {
				alloc.ENIInfo.ERDMA = rdma
				conf := &types.CNIConf{ENIIPVirtualType: mode, VlanStripType: types.VlanStripTypeFilter}
				args := &skel.CmdArgs{IfName: "eth0"}
				before, err := parseSetupConf(context.Background(), args, alloc, conf, ipType)
				require.NoError(t, err)
				conf.VlanStripType = types.VlanStripTypeCilium
				after, err := parseSetupConf(context.Background(), args, alloc, conf, ipType)
				require.NoError(t, err)
				require.Equal(t, before, after)
			}
		}
	}
}
