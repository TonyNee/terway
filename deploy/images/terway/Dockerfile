ARG TERWAY_POLICY_IMAGE=crpi-m73cyoqkgaj4368u.cn-hangzhou.personal.cr.aliyuncs.com/default483925/terway:policy-9e11743b@sha256:2d15164ec9279efc0fbf18985a87d06908f0bc7366e34377be3bc458e330acaf
ARG UBUNTU_IMAGE=registry.cn-hangzhou.aliyuncs.com/acs/ubuntu:24.04-update
ARG CILIUM_LLVM_IMAGE=quay.io/cilium/cilium-llvm:19.1.7-1788264345-cbe387d@sha256:9dbf0536ebf99f08bb3989faa8d85aef7345552f322bd745898a6116eef11a6d
ARG CILIUM_BPFTOOL_IMAGE=quay.io/cilium/cilium-bpftool:7.8.1-1792661649-b17dbf1@sha256:98d03da2a4fad5510ac5903b022b45a09040697fb5f80e50784b204e521317ea
ARG CILIUM_IPTABLES_IMAGE=quay.io/cilium/iptables:1.8.8-1-1792661649-b17dbf1@sha256:16f0f887d83969450d07690995b51c19e8b39fb39d7127565b120d936997818f

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} AS policy-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_LLVM_IMAGE} AS llvm-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_BPFTOOL_IMAGE} AS bpftool-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPTABLES_IMAGE} AS iptables-dist

FROM --platform=$BUILDPLATFORM golang:1.26.8 AS builder
ARG GOPROXY
ARG TARGETOS
ARG TARGETARCH
ENV GOPROXY=$GOPROXY
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY --parents go.mod go.sum .git cmd daemon deploy deviceplugin pkg plugin rpc tests types ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    export CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH && \
    go build -tags default_build \
    -ldflags \
    "-s -w -X \"github.com/AliyunContainerService/terway/pkg/version.gitCommit=`git rev-parse HEAD`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.buildDate=`date -u +'%Y-%m-%dT%H:%M:%SZ'`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.gitVersion=`git describe --tags --match='v*' --abbrev=14`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/aliyun/credential.kubernetesAlicloudIdentity=terwayd/`git rev-parse --short HEAD 2>/dev/null`\"" -o terwayd ./cmd/terway && \
    go build -tags default_build -ldflags "-s -w" -o terway ./plugin/terway && \
    go build -tags default_build -ldflags "-s -w" -o terway-cli ./cmd/terway-cli

FROM --platform=$TARGETPLATFORM ${UBUNTU_IMAGE}
RUN apt-get update && apt-get install -y iproute2 kmod libelf1 libmnl0 iptables nftables kmod curl ipset bash ethtool bridge-utils socat grep findutils jq conntrack iputils-ping && \
    apt-get purge --auto-remove && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN --mount=type=bind,from=iptables-dist,source=/iptables,target=/iptables \
    dpkg -i /iptables/*\.deb
RUN --mount=type=bind,source=/hack/iptables-wrapper-installer.sh,target=/iptables-wrapper-installer.sh \
    /iptables-wrapper-installer.sh --no-sanity-check

COPY --link --from=llvm-dist /usr/local/bin/clang /usr/local/bin/llc /usr/bin/
COPY --link --from=bpftool-dist /usr/local /usr/local
COPY --link --from=policy-dist /bin/calico-felix /usr/local/bin/calico-felix

COPY --link ../../../policy/policyinit.sh ./../../policy/uninstall_policy.sh ../../../hack/init.sh /usr/bin/
COPY --link --from=policy-dist \
    --exclude=usr/bin/cilium-operator-generic \
    --exclude=usr/bin/cilium-bugtool \
    --exclude=usr/bin/cilium-health* \
    /tmp/install/ /
COPY --link --from=builder /go/src/github.com/AliyunContainerService/terway/terwayd /go/src/github.com/AliyunContainerService/terway/terway /go/src/github.com/AliyunContainerService/terway/terway-cli /usr/bin/
ENTRYPOINT ["/usr/bin/terwayd"]
