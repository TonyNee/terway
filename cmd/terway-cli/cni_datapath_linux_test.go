package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Jeffail/gabs/v2"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/types"
)

func TestPrepareVLANOwnership(t *testing.T) {
	parent := &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "eni", Index: 10}}
	workload := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "cali01234567890"}}
	for _, test := range []struct {
		name                                       string
		enabled, previous, active, legacy, wantErr bool
	}{
		{"enable drained", true, false, false, true, false},
		{"enable active", true, false, true, true, true},
		{"enable active without filters", true, false, true, false, true},
		{"restart active", true, true, true, false, false},
		{"restart active with legacy filters", true, true, true, true, true},
		{"rollback active", false, true, true, false, true},
		{"rollback drained", false, true, false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()
			patches.ApplyFunc(utils.LegacyVLANFilters, func(netlink.Link) ([]netlink.Filter, error) {
				if test.legacy {
					return []netlink.Filter{&netlink.U32{}}, nil
				}
				return nil, nil
			})
			removed := 0
			patches.ApplyFunc(utils.RemoveLegacyVLANFilters, func(_ context.Context, filters []netlink.Filter) error { removed += len(filters); return nil })
			links := []netlink.Link{parent}
			if test.active {
				links = append(links, workload)
			}
			err := prepareVLANOwnership(test.enabled, test.previous, links)
			if test.wantErr {
				require.ErrorContains(t, err, "drain")
				require.Zero(t, removed)
			} else {
				require.NoError(t, err)
				if test.legacy {
					require.Equal(t, 1, removed)
				}
			}
		})
	}
	patches := gomonkey.ApplyFunc(utils.LegacyVLANFilters, func(netlink.Link) ([]netlink.Filter, error) { return nil, errors.New("list failed") })
	defer patches.Reset()
	require.ErrorContains(t, prepareVLANOwnership(true, false, []netlink.Link{parent}), "list failed")
}

func TestExclusiveNodeIgnoresCiliumDatapath(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
		t.Fatal("exclusive nodes must not inspect shared-node VLAN state")
		return nil, nil
	})
	capabilities := t.TempDir() + "/capabilities"
	store := nodecap.NewFileNodeCapabilities(capabilities)
	patches.ApplyFunc(nodecap.NewFileNodeCapabilities, func(string) *nodecap.FileNodeCapabilities { return store })
	store.Set(nodecap.NodeCapabilityExclusiveENI, string(types.ExclusiveENIOnly))
	require.NoError(t, store.Save())
	path := t.TempDir() + "/cni"
	out := `{"plugins":[{"type":"terway","eniip_virtual_type":"datapathv2","vlan_strip_type":"cilium"},{"type":"cilium-cni","terway-datapath":true}]}`
	require.NoError(t, writeCNIConfig(path, out))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	c, err := gabs.ParseJSON(data)
	require.NoError(t, err)
	require.Equal(t, "filter", c.Path("plugins").Index(0).Path("vlan_strip_type").Data())
	require.False(t, c.Path("plugins").Index(1).Exists("terway-datapath"))
	patches.ApplyFunc(mountHostBpf, func() error { return nil })
	require.NoError(t, storeRuntimeConfig(capabilities, c))
	require.NoError(t, setExclusiveMode(store, map[string]string{"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly"}, path))
	require.NoError(t, store.Load())
	require.Equal(t, False, store.Get(nodecap.NodeCapabilityCiliumDatapath))
	require.Equal(t, False, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}
