# Trunk VLAN handling with the Cilium datapath plugin

This opt-in mode delegates trunk VLAN pop/push to the built-in Terway datapath
plugin in the policy image. Terway continues to allocate ENIs/IPs and configure
veth devices, addresses, gateways and routes. Cilium owns endpoint VLAN bindings
and their BPF maps. No additional container or datapath plugin service is needed.

The extension is enabled per node, and replaces VLAN actions only for **trunk
member allocations using datapathv2 + veth**. Enable it on nodes already using
datapathv2; do not change ordinary Pods' datapath to enable this extension.
Ordinary shared-IP allocations keep the existing CNI, routing, Service, NAT and
QoS paths. Exclusive ENI nodes keep their existing ENI-only configuration.
Netkit and trunk allocations using multiple interfaces, RDMA or VF devices are
outside this first stage. Use a policy image containing
the built-in Terway datapath patches and the corresponding Terway image from
this change. Updating only the policy image does not activate DP.

For each DP trunk allocation, the primary Terway CNI sets the host veth alias
to `terway-trunk-datapath`. The matching Cilium chainer recognizes that alias
in its existing peer-link lookup, queries the Terway backend, and publishes
the validated VLAN/parent-MAC binding. Ordinary veths are not marked and require
no additional backend RPC or VLAN binding during creation, restoration or
deletion. Packet hooks may still run for ordinary traffic on a shared parent
NIC; unmatched traffic continues without VLAN pop/push. Both CNI binaries must
contain this contract. CNI results and workload routes remain unchanged.

## Configuration

Set these fields on the Terway entry in `10-terway.conf` (or the primary entry
in `10-terway.conflist`) in the configuration used by the test nodes:

```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway",
  "eniip_virtual_type": "datapathv2",
  "vlan_strip_type": "cilium",
  "network_policy_provider": "ebpf"
}
```

Keep the existing trunk/IPAM, MTU, host stack CIDR and policy settings required
by your deployment. `vlan_strip_type` does not enable trunk resource allocation.
For a Helm-managed test deployment the equivalent values are:

```yaml
terway:
  daemonMode: ENIMultiIP
  enableDatapathV2: true
  vlanStripType: cilium
```

Initialization automatically sets `"terway-datapath": true` on `cilium-cni`,
records `cilium_datapath = true` in node capabilities, and starts the policy
agent with `--enable-terway-datapath=true`. This also works with an explicitly
configured Cilium chainer. Do not set the agent flag through `cilium_args`;
the generated configuration owns it. TCX is not required for this veth stage.
The Cilium extension requires native, unencrypted routing and rejects VLAN
bypass, Egress Gateway and XDP acceleration.

Omitting the option, or setting it to `filter`, retains legacy VLAN TC handling.
The existing `vlan` option continues to select VLAN devices.

## Switch a test node

1. Use an isolated test node configuration. Cordon and drain the node before
   changing modes. Remove all Terway workload interfaces, including those of
   non-host-network DaemonSet pods; `drain --ignore-daemonsets` alone may leave
   such interfaces behind. Existing trunk endpoints must be recreated to acquire DP
   metadata.
2. Apply the DP configuration and restart Terway on that drained node with the
   new image. Initialization checks for remaining `cali*` workload veths while
   holding the CNI lock. It refuses a mode change if any remain.
3. On a drained node, initialization removes the legacy Terway U32 ingress VLAN
   pop and IPv4/IPv6 egress VLAN push filters from physical NICs. It matches their
   priorities, selectors and actions, preserving clsact, Cilium BPF programs,
   QoS rules and unrelated filters. A normal DP restart with active pods is
   allowed if no legacy filters need cleaning.
4. Confirm that the policy agent starts successfully, then uncordon the node
   and recreate test workloads. CNI ADD refuses a parent NIC with remaining
   legacy VLAN filters instead of allowing mixed VLAN handling.

Rollback also requires a drained node: switch to `filter`, restart Terway, then
recreate workloads. Terway reinstalls the legacy rules during CNI ADD; Cilium
manages deletion of DP endpoint state. Do not hot-switch existing endpoints.

## Functional validation

Check all three layers before evaluating traffic:

* The generated host CNI conflist contains `vlan_strip_type: cilium` on Terway
  and `terway-datapath: true` on Cilium; the agent command line enables DP.
* The trunk NIC has no legacy U32 VLAN pop/push actions in `tc -d filter show
  dev <trunk-interface> ingress` or `egress`. Cilium TC BPF filters can still
  exist. Do not delete the entire clsact qdisc.
* Only DP trunk veths have the `terway-trunk-datapath` alias.
* Only trunk endpoints have `terway-vlan-id` / `terway-parent-mac` properties
  and `cilium_twvlan` bindings; ordinary endpoints have no new DP metadata.
* The agent has populated `cilium_twvlan` bindings. Generate trunk workload
  traffic and inspect the pinned per-CPU `cilium_twstats` map: index 0 counts
  ingress VLAN pop and index 1 counts egress VLAN push. Sum values across CPUs;
  counters 2 through 5 report invalid packets, VLAN operation failures or
  inconsistent bindings. Verify that normal traffic increments pop/push without
  increasing these failure counters.

Run the existing Terway regression suite on actual trunk nodes, covering
same-node/cross-node traffic, pod-to-node traffic, Service access, NetworkPolicy,
ordinary ENI coexistence, pod deletion/IP reuse and Terway/policy restarts.
Exercise IPv4 and IPv6 where deployed. Successful traffic plus DP bindings and
counter increments, with legacy VLAN TC rules absent, demonstrates that DP is
handling VLANs. Unit and namespace tests alone do not establish this result.
