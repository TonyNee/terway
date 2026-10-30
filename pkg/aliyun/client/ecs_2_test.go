package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/tea"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
)

func TestECSService_DescribeNetworkInterface2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs20140526.Client).DescribeNetworkInterfaces,
		func(client *ecs20140526.Client, request *ecs20140526.DescribeNetworkInterfacesRequest) (*ecs20140526.DescribeNetworkInterfacesResponse, error) {
			return nil, &tea.SDKError{Code: ptr.To("InternalError")}
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.DescribeNetworkInterface2(ctx)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_AssignPrivateIPAddress2_Error_Retry(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	count := 0
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			count++
			if count < 2 {
				return nil, sdkErr.NewServerError(403, "{\"Code\": \"Throttling\"}", "request-id")
			}
			return &ecs.AssignPrivateIpAddressesResponse{
				RequestId: "request-id",
				AssignedPrivateIpAddressesSet: ecs.AssignedPrivateIpAddressesSet{
					PrivateIpSet: ecs.PrivateIpSetInAssignPrivateIpAddresses{
						PrivateIpAddress: []string{"10.0.0.1"},
					},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(ctx, &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPCount:            1,
		},
		Backoff: &wait.Backoff{Steps: 2, Duration: time.Millisecond},
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 2, count)
	assert.Equal(t, "10.0.0.1", result[0].IPAddress)
}

// Test AssignPrivateIPAddress2 when only IPv4 prefix is returned (no regular IPs)
// This tests the dd77ec94 commit logic for prefix-only allocation
func TestECSService_AssignPrivateIPAddress2_OnlyPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			// Verify request uses Ipv4PrefixCount
			val, _ := request.Ipv4PrefixCount.GetValue()
			assert.Equal(t, 1, val)

			return &ecs.AssignPrivateIpAddressesResponse{
				RequestId: "request-id",
				AssignedPrivateIpAddressesSet: ecs.AssignedPrivateIpAddressesSet{
					PrivateIpSet: ecs.PrivateIpSetInAssignPrivateIpAddresses{
						PrivateIpAddress: []string{}, // Empty when only prefix is allocated
					},
					Ipv4PrefixSet: ecs.Ipv4PrefixSetInAssignPrivateIpAddresses{
						Ipv4Prefixes: []string{"10.244.126.96/28"},
					},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(ctx, &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPv4PrefixCount:    1,
		},
		Backoff: &wait.Backoff{Steps: 1, Duration: time.Millisecond},
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 1)
	assert.Equal(t, Prefix("10.244.126.96/28"), result[0].Prefix)
	assert.Empty(t, result[0].IPAddress)
}

// Test AssignPrivateIPAddress2 with both IPs and prefixes in response
// Note: ECS API doesn't allow simultaneous allocation, but code should handle combined response
func TestECSService_AssignPrivateIPAddress2_BothIPAndPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			return &ecs.AssignPrivateIpAddressesResponse{
				RequestId: "request-id",
				AssignedPrivateIpAddressesSet: ecs.AssignedPrivateIpAddressesSet{
					PrivateIpSet: ecs.PrivateIpSetInAssignPrivateIpAddresses{
						PrivateIpAddress: []string{"10.0.0.1"},
					},
					Ipv4PrefixSet: ecs.Ipv4PrefixSetInAssignPrivateIpAddresses{
						Ipv4Prefixes: []string{"10.244.126.96/28"},
					},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(ctx, &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPCount:            1,
		},
		Backoff: &wait.Backoff{Steps: 1, Duration: time.Millisecond},
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 2)
	// First result should be IP
	assert.Equal(t, "10.0.0.1", result[0].IPAddress)
	assert.Empty(t, result[0].Prefix)
	// Second result should be prefix
	assert.Equal(t, Prefix("10.244.126.96/28"), result[1].Prefix)
	assert.Empty(t, result[1].IPAddress)
}

// Test AssignPrivateIPAddress2 with empty response (edge case)
func TestECSService_AssignPrivateIPAddress2_EmptyResponse(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			return &ecs.AssignPrivateIpAddressesResponse{
				RequestId: "request-id",
				AssignedPrivateIpAddressesSet: ecs.AssignedPrivateIpAddressesSet{
					PrivateIpSet: ecs.PrivateIpSetInAssignPrivateIpAddresses{
						PrivateIpAddress: []string{},
					},
					Ipv4PrefixSet: ecs.Ipv4PrefixSetInAssignPrivateIpAddresses{
						Ipv4Prefixes: []string{},
					},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(ctx, &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPCount:            1,
		},
		Backoff: &wait.Backoff{Steps: 1, Duration: time.Millisecond},
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

// Test AssignIpv6Addresses2 with only IPv6 prefix (no regular IPv6 addresses)
func TestECSService_AssignIpv6Addresses2_OnlyPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignIpv6Addresses,
		func(client *ecs.Client, request *ecs.AssignIpv6AddressesRequest) (*ecs.AssignIpv6AddressesResponse, error) {
			// Verify request uses Ipv6PrefixCount
			val, _ := request.Ipv6PrefixCount.GetValue()
			assert.Equal(t, 1, val)

			return &ecs.AssignIpv6AddressesResponse{
				RequestId: "request-id",
				Ipv6Sets: ecs.Ipv6SetsInAssignIpv6Addresses{
					Ipv6Address: []string{}, // Empty when only prefix is allocated
				},
				Ipv6PrefixSets: ecs.Ipv6PrefixSetsInAssignIpv6Addresses{
					Ipv6Prefix: []string{"2408:4002:10c0:8200::/80"},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignIpv6Addresses2(ctx, &AssignIPv6AddressesOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPv6PrefixCount:    1,
		},
		Backoff: &wait.Backoff{Steps: 1, Duration: time.Millisecond},
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 1)
	assert.Equal(t, Prefix("2408:4002:10c0:8200::/80"), result[0].Prefix)
	assert.Empty(t, result[0].IPAddress)
}

// Test AssignIpv6Addresses2 with both IPv6 addresses and prefixes
func TestECSService_AssignIpv6Addresses2_BothIPAndPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignIpv6Addresses,
		func(client *ecs.Client, request *ecs.AssignIpv6AddressesRequest) (*ecs.AssignIpv6AddressesResponse, error) {
			return &ecs.AssignIpv6AddressesResponse{
				RequestId: "request-id",
				Ipv6Sets: ecs.Ipv6SetsInAssignIpv6Addresses{
					Ipv6Address: []string{"2408:4002:10c0:8200::1"},
				},
				Ipv6PrefixSets: ecs.Ipv6PrefixSetsInAssignIpv6Addresses{
					Ipv6Prefix: []string{"2408:4002:10c0:8200::/80"},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignIpv6Addresses2(ctx, &AssignIPv6AddressesOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPv6Count:          1,
		},
		Backoff: &wait.Backoff{Steps: 1, Duration: time.Millisecond},
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 2)
	// First result should be IP
	assert.Equal(t, "2408:4002:10c0:8200::1", result[0].IPAddress)
	assert.Empty(t, result[0].Prefix)
	// Second result should be prefix
	assert.Equal(t, Prefix("2408:4002:10c0:8200::/80"), result[1].Prefix)
	assert.Empty(t, result[1].IPAddress)
}

func TestECSService_AssignPrivateIPAddress2_FatalError(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			return nil, sdkErr.NewServerError(400, "{\"Code\": \"InvalidParameter\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(ctx, &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPCount:            1,
		},
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.ErrorCodeIs(err, "InvalidParameter"))
}

func TestECSService_UnAssignPrivateIPAddresses2_OnlyPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.UnassignPrivateIpAddressesRequest) (*ecs.UnassignPrivateIpAddressesResponse, error) {
			assert.NotNil(t, request.Ipv4Prefix)
			assert.Equal(t, []string{"10.0.0.0/28"}, *request.Ipv4Prefix)
			assert.Nil(t, request.PrivateIpAddress)
			return &ecs.UnassignPrivateIpAddressesResponse{RequestId: "req-1"}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses2(ctx, "eni-1", []IPSet{{Prefix: "10.0.0.0/28"}})
	assert.NoError(t, err)
}

func TestECSService_UnAssignPrivateIPAddresses2_MixedIPAndPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses2(ctx, "eni-1", []IPSet{
		{IPAddress: "10.0.0.1"},
		{Prefix: "10.0.0.16/28"},
	})
	assert.ErrorIs(t, err, ErrInvalidArgs)
}

func TestECSService_UnAssignPrivateIPAddresses2_EmptyInput(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	err := ecsService.UnAssignPrivateIPAddresses2(context.Background(), "eni-1", []IPSet{})
	assert.NoError(t, err, "empty input should return nil immediately")
}

func TestECSService_UnAssignPrivateIPAddresses2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.UnassignPrivateIpAddressesRequest) (*ecs.UnassignPrivateIpAddressesResponse, error) {
			return nil, sdkErr.NewServerError(500, "{\"Code\": \"InternalError\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses2(ctx, "eni-1", []IPSet{{IPAddress: "10.0.0.1"}})
	assert.Error(t, err)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_UnAssignPrivateIPAddresses2_NotFound(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.UnassignPrivateIpAddressesRequest) (*ecs.UnassignPrivateIpAddressesResponse, error) {
			return nil, sdkErr.NewServerError(404, fmt.Sprintf("{\"Code\": \"%s\"}", apiErr.ErrInvalidENINotFound), "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses2(ctx, "eni-1", []IPSet{{IPAddress: "10.0.0.1"}})
	assert.NoError(t, err)
}

func TestECSService_AssignIpv6Addresses2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignIpv6Addresses,
		func(client *ecs.Client, request *ecs.AssignIpv6AddressesRequest) (*ecs.AssignIpv6AddressesResponse, error) {
			return nil, sdkErr.NewServerError(400, "{\"Code\": \"InvalidParameter\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignIpv6Addresses2(ctx, &AssignIPv6AddressesOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPv6Count:          1,
		},
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.ErrorCodeIs(err, "InvalidParameter"))
}

func TestECSService_UnAssignIpv6Addresses2_OnlyPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignIpv6Addresses,
		func(client *ecs.Client, request *ecs.UnassignIpv6AddressesRequest) (*ecs.UnassignIpv6AddressesResponse, error) {
			assert.NotNil(t, request.Ipv6Prefix)
			assert.Equal(t, []string{"2408:4005:3af:6d03:be7a::/80"}, *request.Ipv6Prefix)
			assert.Nil(t, request.Ipv6Address)
			return &ecs.UnassignIpv6AddressesResponse{RequestId: "req-1"}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses2(ctx, "eni-1", []IPSet{{Prefix: "2408:4005:3af:6d03:be7a::/80"}})
	assert.NoError(t, err)
}

func TestECSService_UnAssignIpv6Addresses2_MixedIPAndPrefix(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses2(ctx, "eni-1", []IPSet{
		{IPAddress: "2001:db8::1"},
		{Prefix: "2408:4005:3af:6d03:be7a::/80"},
	})
	assert.ErrorIs(t, err, ErrInvalidArgs)
}

func TestECSService_UnAssignIpv6Addresses2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignIpv6Addresses,
		func(client *ecs.Client, request *ecs.UnassignIpv6AddressesRequest) (*ecs.UnassignIpv6AddressesResponse, error) {
			return nil, sdkErr.NewServerError(500, "{\"Code\": \"InternalError\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses2(ctx, "eni-1", []IPSet{{IPAddress: "2001:db8::1"}})
	assert.Error(t, err)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_UnAssignIpv6Addresses2_NotFound(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignIpv6Addresses,
		func(client *ecs.Client, request *ecs.UnassignIpv6AddressesRequest) (*ecs.UnassignIpv6AddressesResponse, error) {
			return nil, sdkErr.NewServerError(404, fmt.Sprintf("{\"Code\": \"%s\"}", apiErr.ErrInvalidENINotFound), "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses2(ctx, "eni-1", []IPSet{{IPAddress: "2001:db8::1"}})
	assert.NoError(t, err)
}

func TestECSService_DetachNetworkInterface2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DetachNetworkInterface,
		func(client *ecs.Client, request *ecs.DetachNetworkInterfaceRequest) (*ecs.DetachNetworkInterfaceResponse, error) {
			return nil, sdkErr.NewServerError(500, "{\"Code\": \"InternalError\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.DetachNetworkInterface2(ctx, &DetachNetworkInterfaceOptions{
		NetworkInterfaceID: ptrTo("eni-1"),
		InstanceID:         ptrTo("i-1"),
	})
	assert.Error(t, err)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_DetachNetworkInterface2_NotFound(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DetachNetworkInterface,
		func(client *ecs.Client, request *ecs.DetachNetworkInterfaceRequest) (*ecs.DetachNetworkInterfaceResponse, error) {
			return nil, sdkErr.NewServerError(404, fmt.Sprintf("{\"Code\": \"%s\"}", apiErr.ErrInvalidENINotFound), "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.DetachNetworkInterface2(ctx, &DetachNetworkInterfaceOptions{
		NetworkInterfaceID: ptrTo("eni-1"),
		InstanceID:         ptrTo("i-1"),
	})
	assert.NoError(t, err)
}

func ptrTo[T any](v T) *T {
	return &v
}

func TestECSService_DescribeNetworkInterface_LocalTagsPagination(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, mode := range []string{"pages", "empty", "error"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, mode), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !assert.NoError(t, r.ParseForm()) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.Equal(t, APIDescribeNetworkInterfaces, r.Header.Get("x-acs-action"))
					assert.Equal(t, "cn-hangzhou", r.Form.Get("RegionId"))
					assert.Equal(t, "vpc-test", r.Form.Get("VpcId"))
					assert.Equal(t, "eni-test", r.Form.Get("NetworkInterfaceId.1"))
					assert.Equal(t, "i-test", r.Form.Get("InstanceId"))
					assert.Equal(t, "Secondary", r.Form.Get("Type"))
					assert.Equal(t, "InUse", r.Form.Get("Status"))
					assert.Equal(t, fmt.Sprint(maxSinglePageSize), r.Form.Get("MaxResults"))
					assert.NotContains(t, r.Form, "Tag.1.Key")
					assert.NotContains(t, r.Form, "Tag.1.Value")
					call := calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if mode == "error" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = fmt.Fprintf(w, `{"Code":%q,"Message":"request rejected","RequestId":"request-test"}`, apiErr.ErrForbidden)
						return
					}
					if mode == "empty" {
						_, _ = fmt.Fprint(w, `{}`)
						return
					}
					resp := &ecs20140526.DescribeNetworkInterfacesResponse{Body: &ecs20140526.DescribeNetworkInterfacesResponseBody{NetworkInterfaceSets: &ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSets{}}}
					switch call {
					case 1, 2:
						if call == 1 {
							assert.Empty(t, r.Form.Get("NextToken"))
						} else {
							assert.Equal(t, "page-2", r.Form.Get("NextToken"))
						}
						resp.Body.NetworkInterfaceSets.NetworkInterfaceSet = make([]*ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSet, maxSinglePageSize)
						for i := range resp.Body.NetworkInterfaceSets.NetworkInterfaceSet {
							resp.Body.NetworkInterfaceSets.NetworkInterfaceSet[i] = &ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSet{}
						}
						resp.Body.NextToken = ptr.To(fmt.Sprintf("page-%d", call+1))
					case 3:
						assert.Equal(t, "page-3", r.Form.Get("NextToken"))
						// A short final page can still carry a token; do not query it.
						resp.Body.NextToken = ptr.To("unused-token")
						resp.Body.NetworkInterfaceSets.NetworkInterfaceSet = []*ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSet{
							{NetworkInterfaceId: ptr.To("eni-first"), Tags: &ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetTags{Tag: []*ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetTagsTag{{TagKey: ptr.To("app"), TagValue: ptr.To("terway")}, {TagKey: ptr.To("cluster"), TagValue: ptr.To("test")}}}},
							{NetworkInterfaceId: ptr.To("eni-partial"), Tags: &ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetTags{Tag: []*ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetTagsTag{{TagKey: ptr.To("app"), TagValue: ptr.To("terway")}}}},
							{NetworkInterfaceId: ptr.To("eni-second"), Tags: &ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetTags{Tag: []*ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetTagsTag{{TagKey: ptr.To("app"), TagValue: ptr.To("terway")}, {TagKey: ptr.To("cluster"), TagValue: ptr.To("test")}}}},
						}
						resp.Body.NetworkInterfaceSets.NetworkInterfaceSet[0].Ipv6Sets = &ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetIpv6Sets{
							Ipv6Set: []*ecs20140526.DescribeNetworkInterfacesResponseBodyNetworkInterfaceSetsNetworkInterfaceSetIpv6SetsIpv6Set{
								{Ipv6Address: ptr.To("fd00::2"), Primary: ptr.To(false)},
								{Ipv6Address: ptr.To("fd00::1"), Primary: ptr.To(true)},
							},
						}
					default:
						t.Error("unexpected additional page")
					}
					assert.NoError(t, json.NewEncoder(w).Encode(resp.Body))
				}))
				defer server.Close()
				native, err := ecs20140526.NewClient(&openapi.Config{
					Endpoint: ptr.To(strings.TrimPrefix(server.URL, "http://")), Protocol: ptr.To("http"),
					AccessKeyId: ptr.To("test"), AccessKeySecret: ptr.To("test"),
				})
				require.NoError(t, err)
				service := createTestECSServiceForAPI()
				service.ClientSet = &mockClientSetForAPI{ecsV2Client: native}
				tags := map[string]string{"app": "terway", "cluster": "test"}
				var result []*NetworkInterface
				if v2 {
					result, err = service.DescribeNetworkInterface2(context.Background(), &DescribeNetworkInterfaceOptions{
						VPCID: ptrTo("vpc-test"), NetworkInterfaceIDs: ptrTo([]string{"eni-test"}),
						InstanceID: ptrTo("i-test"), InstanceType: ptrTo("Secondary"), Status: ptrTo("InUse"), Tags: &tags,
					})
				} else {
					result, err = service.DescribeNetworkInterface(context.Background(), "vpc-test", []string{"eni-test"}, "i-test", "Secondary", "InUse", tags)
				}
				if mode == "error" {
					assert.Nil(t, result)
					assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrForbidden))
					assert.Equal(t, "request-test", apiErr.ErrRequestID(err))
					assert.EqualValues(t, 1, calls.Load())
					return
				}
				require.NoError(t, err)
				if mode == "empty" {
					assert.Empty(t, result)
					assert.EqualValues(t, 1, calls.Load())
					return
				}
				assert.EqualValues(t, 3, calls.Load())
				require.Len(t, result, 2)
				assert.Equal(t, "eni-first", result[0].NetworkInterfaceID)
				assert.Equal(t, "eni-second", result[1].NetworkInterfaceID)
				assert.Equal(t, "app", result[0].Tags[0].TagKey)
				assert.Equal(t, "app", result[1].Tags[0].TagKey)
				assert.Equal(t, []IPSet{{IPAddress: "fd00::2"}, {IPAddress: "fd00::1", Primary: true}}, result[0].IPv6Set)
				assert.Empty(t, result[0].PrivateIPAddress)
				assert.Empty(t, result[0].PrivateIPSets)
			})
		}
	}
}
