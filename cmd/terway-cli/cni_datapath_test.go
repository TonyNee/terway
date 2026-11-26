package main

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/Jeffail/gabs/v2"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
)

func TestCiliumDatapathConfig(t *testing.T) {
	primary := []byte(`{"type":"terway","eniip_virtual_type":"datapathv2","vlan_strip_type":"cilium"}`)
	for _, explicit := range []bool{false, true} {
		configs := [][]byte{primary}
		if explicit {
			configs = append(configs, []byte(`{"type":"cilium-cni","log-file":"custom.log"}`))
		}
		out, err := mergeConfigList(configs, &feature{EBPF: true})
		require.NoError(t, err)
		c, err := gabs.ParseJSON([]byte(out))
		require.NoError(t, err)
		require.Len(t, c.Path("plugins").Children(), 2)
		assert.Equal(t, true, c.Path("plugins").Index(1).Path("terway-datapath").Data())
		patches := gomonkey.ApplyFunc(mountHostBpf, func() error { return nil })
		path := t.TempDir() + "/capabilities"
		err = storeRuntimeConfig(path, c)
		patches.Reset()
		require.NoError(t, err)
		store := nodecap.NewFileNodeCapabilities(path)
		require.NoError(t, store.Load())
		assert.Equal(t, True, store.Get(nodecap.NodeCapabilityCiliumDatapath))
		assert.Equal(t, True, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
		assert.Equal(t, dataPathV2, store.Get(nodecap.NodeCapabilityDataPath))
		// Removing the option must clear the persisted agent setting.
		_, err = c.Path("plugins").Index(0).Set("filter", "vlan_strip_type")
		require.NoError(t, err)
		patches = gomonkey.ApplyFunc(mountHostBpf, func() error { return nil })
		err = storeRuntimeConfig(path, c)
		patches.Reset()
		require.NoError(t, err)
		require.NoError(t, store.Load())
		assert.Equal(t, False, store.Get(nodecap.NodeCapabilityCiliumDatapath))
	}
}

func TestCiliumDatapathInvalidConfig(t *testing.T) {
	for _, config := range []string{
		`{"type":"terway","vlan_strip_type":"cilium"}`,
		`{"type":"terway","eniip_virtual_type":"ipvlan","vlan_strip_type":"cilium"}`,
		`{"type":"terway","eniip_virtual_type":"datapathv2","vlan_strip_type":"cilium","disable_host_peer":true}`,
	} {
		_, err := mergeConfigList([][]byte{[]byte(config)}, &feature{EBPF: true})
		require.ErrorContains(t, err, "requires eBPF")
	}
	primary := []byte(`{"type":"terway","eniip_virtual_type":"datapathv2","vlan_strip_type":"cilium"}`)
	_, err := mergeConfigList([][]byte{primary}, &feature{})
	require.Error(t, err)
	_, err = mergeConfigList([][]byte{primary, []byte(`{"type":"cilium-cni","terway-datapath":false}`)}, &feature{EBPF: true})
	require.ErrorContains(t, err, "must match")
	_, err = mergeConfigList([][]byte{[]byte(`{"type":"terway","eniip_virtual_type":"datapathv2"}`), []byte(`{"type":"cilium-cni","terway-datapath":true}`)}, &feature{EBPF: true})
	require.ErrorContains(t, err, "must match")
}

func TestCiliumDatapathPolicyConfig(t *testing.T) {
	patches := gomonkey.ApplyFunc(getAllConfig, func(string) (*TerwayConfig, error) { return &TerwayConfig{}, nil })
	defer patches.Reset()
	path := t.TempDir() + "/capabilities"
	store := nodecap.NewFileNodeCapabilities(path)
	store.Set(nodecap.NodeCapabilityDataPath, dataPathV2)
	store.Set(nodecap.NodeCapabilityHasCiliumChainer, True)
	store.Set(nodecap.NodeCapabilityCiliumDatapath, True)
	require.NoError(t, store.Save())
	cfg, err := getPolicyConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.CiliumDatapath)
	store.Set(nodecap.NodeCapabilityHasCiliumChainer, False)
	require.NoError(t, store.Save())
	_, err = getPolicyConfig(path)
	require.ErrorContains(t, err, "requires datapathv2")
}

func TestCiliumDatapathAgentArguments(t *testing.T) {
	var extraArgs, captured []string
	patches := gomonkey.ApplyFunc(parsePolicyConfig, func() ([]string, error) { return extraArgs, nil })
	defer patches.Reset()
	patches.ApplyFunc(exec.LookPath, func(string) (string, error) { return "/usr/bin/cilium-agent", nil })
	patches.ApplyFunc(syscall.Exec, func(_ string, args, _ []string) error { captured = args; return nil })
	for _, enabled := range []bool{false, true} {
		captured = nil
		err := runCilium(&PolicyConfig{HasCiliumChainer: true, Datapath: dataPathV2, CiliumDatapath: enabled})
		require.NoError(t, err)
		assert.Equal(t, enabled, containsArg(captured, "--enable-terway-datapath=true"))
		assert.Equal(t, !enabled, containsArg(captured, "--enable-terway-datapath=false"))
		assert.Contains(t, captured, "--datapath-mode=veth")
		assert.Contains(t, captured, "--enable-tcx=false")
	}
	extraArgs = []string{"--enable-terway-datapath=false"}
	err := runCilium(&PolicyConfig{HasCiliumChainer: true, Datapath: dataPathV2, CiliumDatapath: true})
	require.ErrorContains(t, err, "vlan_strip_type")
	extraArgs = []string{"--datapath-mode=netkit"}
	err = runCilium(&PolicyConfig{HasCiliumChainer: true, Datapath: dataPathV2, CiliumDatapath: true})
	require.ErrorContains(t, err, "managed by Terway")
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
