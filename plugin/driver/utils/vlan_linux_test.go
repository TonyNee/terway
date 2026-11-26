package utils

import (
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/AliyunContainerService/terway/pkg/tc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func TestLegacyVLANFilterMatch(t *testing.T) {
	makePush := func() *netlink.U32 {
		action := netlink.NewVlanKeyAction()
		action.Action, action.Vid = netlink.TCA_VLAN_KEY_PUSH, 100
		action.Attrs().Action = netlink.TC_ACT_PIPE
		filter := &netlink.U32{FilterAttrs: netlink.FilterAttrs{LinkIndex: 10, Parent: netlink.HANDLE_MIN_EGRESS, Protocol: unix.ETH_P_IP, Priority: 50001}, Actions: []netlink.Action{action}}
		tc.MatchSrc(filter, &net.IPNet{IP: net.ParseIP("192.0.2.1"), Mask: net.CIDRMask(32, 32)})
		return filter
	}
	for _, test := range []struct {
		name   string
		mutate func(*netlink.U32)
		match  bool
	}{
		{"owned", func(*netlink.U32) {}, true},
		{"other priority", func(f *netlink.U32) { f.Priority++ }, false},
		{"nil selector", func(f *netlink.U32) { f.Sel = nil }, false},
		{"different source mask", func(f *netlink.U32) { f.Sel.Keys[0].Mask = 0xffffff00 }, false},
		{"different source offset", func(f *netlink.U32) { f.Sel.Keys[0].Off = 16 }, false},
		{"more actions", func(f *netlink.U32) { f.Actions = append(f.Actions, netlink.NewVlanKeyAction()) }, false},
		{"QoS class", func(f *netlink.U32) { f.ClassId = 1 }, false},
		{"different verdict", func(f *netlink.U32) { f.Actions[0].Attrs().Action = netlink.TC_ACT_OK }, false},
		{"IPv6 old priority", func(f *netlink.U32) {
			f.Protocol = unix.ETH_P_IPV6
			f.Sel = nil
			tc.MatchSrc(f, &net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(128, 128)})
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := makePush()
			test.mutate(f)
			require.Equal(t, test.match, isLegacyVLANFilter(f, 10, netlink.HANDLE_MIN_EGRESS))
		})
	}
	require.False(t, isLegacyVLANFilter(&netlink.BpfFilter{}, 10, netlink.HANDLE_MIN_EGRESS))
}

// Exercise the kernel's returned U32 representation, rather than only synthetic
// structs, including IPv6 and the ingress RECLASSIFY action.
func TestLegacyVLANCleanup(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	netNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer netNS.Close()
	defer testutils.UnmountNS(netNS)
	err = netNS.Do(func(ns.NetNS) error {
		require.NoError(t, netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "eni"}}))
		link, err := netlink.LinkByName("eni")
		require.NoError(t, err)
		ctx := context.Background()
		require.NoError(t, EnsureVlanUntagger(ctx, link))
		require.NoError(t, EnsureVlanTag(ctx, link, &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{IP: net.ParseIP("192.0.2.1"), Mask: net.CIDRMask(24, 32)},
			IPv6: &net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
		}, 100))
		// This unrelated rule shares clsact and must survive cleanup.
		other := &netlink.U32{FilterAttrs: netlink.FilterAttrs{LinkIndex: link.Attrs().Index, Parent: netlink.HANDLE_MIN_EGRESS, Protocol: unix.ETH_P_IP, Priority: 40000}}
		tc.MatchSrc(other, &net.IPNet{IP: net.ParseIP("192.0.2.2"), Mask: net.CIDRMask(32, 32)})
		require.NoError(t, netlink.FilterAdd(other))
		filters, err := LegacyVLANFilters(link)
		require.NoError(t, err)
		require.Len(t, filters, 3)
		require.NoError(t, RemoveLegacyVLANFilters(ctx, filters))
		filters, err = LegacyVLANFilters(link)
		require.NoError(t, err)
		require.Empty(t, filters)
		require.NoError(t, RemoveLegacyVLANFilters(ctx, filters))
		remaining, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		found := false
		for _, filter := range remaining {
			if filter.Attrs().Priority == 40000 {
				found = true
			}
		}
		require.True(t, found)
		qdiscs, err := netlink.QdiscList(link)
		require.NoError(t, err)
		found = false
		for _, qdisc := range qdiscs {
			if qdisc.Type() == "clsact" {
				found = true
			}
		}
		require.True(t, found)
		return nil
	})
	require.NoError(t, err)
}
