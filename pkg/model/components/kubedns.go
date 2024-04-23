/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package components

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/loader"
)

// KubeDnsOptionsBuilder adds options for kube-dns
type KubeDnsOptionsBuilder struct {
	Context *OptionsContext
}

var _ loader.OptionsBuilder = &KubeDnsOptionsBuilder{}

// BuildOptions fills in the kubedns model
func (b *KubeDnsOptionsBuilder) BuildOptions(o interface{}) error {
	clusterSpec := o.(*kops.ClusterSpec)

	if clusterSpec.KubeDNS == nil {
		clusterSpec.KubeDNS = &kops.KubeDNSConfig{}
		if clusterSpec.CloudProvider.Scaleway != nil {
			clusterSpec.KubeDNS.Tolerations = []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/control-plane",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				},
				{
					Key:      "CriticalAddonsOnly",
					Operator: corev1.TolerationOpExists,
				},
			}
		}
	}

	if clusterSpec.KubeDNS.CacheMaxSize == 0 {
		clusterSpec.KubeDNS.CacheMaxSize = 1000
	}

	if clusterSpec.KubeDNS.CacheMaxConcurrent == 0 {
		clusterSpec.KubeDNS.CacheMaxConcurrent = 150
	}

	if clusterSpec.KubeDNS.ServerIP == "" {
		ip, err := WellKnownServiceIP(&clusterSpec.Networking, 10)
		if err != nil {
			return err
		}
		clusterSpec.KubeDNS.ServerIP = ip.String()
	}

	if clusterSpec.KubeDNS.Domain == "" {
		clusterSpec.KubeDNS.Domain = clusterSpec.ClusterDNSDomain
	}

	if clusterSpec.KubeDNS.MemoryRequest == nil || clusterSpec.KubeDNS.MemoryRequest.IsZero() {
		defaultMemoryRequest := resource.MustParse("70Mi")
		clusterSpec.KubeDNS.MemoryRequest = &defaultMemoryRequest
	}

	if clusterSpec.KubeDNS.CPURequest == nil || clusterSpec.KubeDNS.CPURequest.IsZero() {
		defaultCPURequest := resource.MustParse("100m")
		clusterSpec.KubeDNS.CPURequest = &defaultCPURequest
	}

	if clusterSpec.KubeDNS.MemoryLimit == nil || clusterSpec.KubeDNS.MemoryLimit.IsZero() {
		defaultMemoryLimit := resource.MustParse("170Mi")
		clusterSpec.KubeDNS.MemoryLimit = &defaultMemoryLimit
	}

	if clusterSpec.IsIPv6Only() && clusterSpec.GetCloudProvider() == kops.CloudProviderAWS {
		if len(clusterSpec.KubeDNS.UpstreamNameservers) == 0 {
			clusterSpec.KubeDNS.UpstreamNameservers = []string{"fd00:ec2::253"}
		}
	}

	//if clusterSpec.GetCloudProvider() == kops.CloudProviderScaleway {
	//	clusterSpec.KubeDNS.UpstreamNameservers = []string{"100.64.0.10"}
	//}

	nodeLocalDNS := clusterSpec.KubeDNS.NodeLocalDNS
	if nodeLocalDNS == nil {
		nodeLocalDNS = &kops.NodeLocalDNSConfig{}
		clusterSpec.KubeDNS.NodeLocalDNS = nodeLocalDNS
	}
	if nodeLocalDNS.Enabled == nil {
		nodeLocalDNS.Enabled = fi.PtrTo(false)
	}
	if fi.ValueOf(nodeLocalDNS.Enabled) && nodeLocalDNS.LocalIP == "" {
		if clusterSpec.IsIPv6Only() {
			nodeLocalDNS.LocalIP = "fd00:90de:d95::1"
		} else {
			nodeLocalDNS.LocalIP = "169.254.20.10"
		}
	}
	if fi.ValueOf(nodeLocalDNS.Enabled) && nodeLocalDNS.ForwardToKubeDNS == nil {
		nodeLocalDNS.ForwardToKubeDNS = fi.PtrTo(false)
	}

	if nodeLocalDNS.MemoryRequest == nil || nodeLocalDNS.MemoryRequest.IsZero() {
		defaultMemoryRequest := resource.MustParse("5Mi")
		nodeLocalDNS.MemoryRequest = &defaultMemoryRequest
	}

	if nodeLocalDNS.CPURequest == nil || nodeLocalDNS.CPURequest.IsZero() {
		defaultCPURequest := resource.MustParse("25m")
		nodeLocalDNS.CPURequest = &defaultCPURequest
	}

	if nodeLocalDNS.Image == nil {
		nodeLocalDNS.Image = fi.PtrTo("registry.k8s.io/dns/k8s-dns-node-cache:1.22.20")
	}

	return nil
}
