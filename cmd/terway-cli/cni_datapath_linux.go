package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Jeffail/gabs/v2"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	driverTypes "github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/types"
)

// writeCNIConfig switches VLAN ownership only on a drained node. The same lock
// as CNI ADD/DEL prevents a new veth from appearing during inspection/cleanup.
func writeCNIConfig(path, out string) error {
	c, err := gabs.ParseJSON([]byte(out))
	if err != nil {
		return err
	}
	enabled := false
	for _, plugin := range c.Path("plugins").Children() {
		if plugin.Path("type").Data() == pluginTypeTerway {
			enabled = plugin.Path("vlan_strip_type").Data() == driverTypes.VlanStripTypeCilium
		}
	}
	store := nodecap.NewFileNodeCapabilities(nodeCapabilitiesFile)
	if err := store.Load(); err != nil {
		return err
	}
	previous := store.Get(nodecap.NodeCapabilityCiliumDatapath) == True
	if !enabled && !previous {
		return os.WriteFile(path, []byte(out), 0644)
	}
	if store.Get(nodecap.NodeCapabilityExclusiveENI) == string(types.ExclusiveENIOnly) {
		// nodeconfig retains its existing ENI-only rewrite. Ignore the shared
		// node's VLAN opt-in on exclusive nodes before inspecting TC state.
		for _, plugin := range c.Path("plugins").Children() {
			if plugin.Path("type").Data() == pluginTypeTerway && enabled {
				_, _ = plugin.Set(driverTypes.VlanStripTypeFilter, "vlan_strip_type")
			}
			if plugin.Path("type").Data() == pluginTypeCilium {
				_ = plugin.Delete("terway-datapath")
			}
		}
		return os.WriteFile(path, []byte(c.StringIndent("", "  ")), 0644)
	}
	if enabled && !previous && store.Get(nodecap.NodeCapabilityDataPath) == dataPathIPvlan {
		return fmt.Errorf("migrate the node from ipvlan to veth before enabling Cilium datapath")
	}
	lock, err := utils.GrabFileLock("/var/run/eni/terway_cni.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	if err := prepareVLANOwnership(enabled, previous, links); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), 0644)
}

func prepareVLANOwnership(enabled, previous bool, links []netlink.Link) error {
	var filters []netlink.Filter
	var workloads []string
	for _, link := range links {
		if link.Type() == "veth" && strings.HasPrefix(link.Attrs().Name, "cali") {
			workloads = append(workloads, link.Attrs().Name)
		}
		if enabled && link.Type() == "device" {
			legacy, err := utils.LegacyVLANFilters(link)
			if err != nil {
				return err
			}
			filters = append(filters, legacy...)
		}
	}
	if len(workloads) > 0 && (enabled != previous || len(filters) > 0) {
		return fmt.Errorf("drain all Terway workload pods before switching VLAN handling or cleaning legacy VLAN TC filters (remaining veths: %s)", strings.Join(workloads, ", "))
	}
	return utils.RemoveLegacyVLANFilters(context.Background(), filters)
}
