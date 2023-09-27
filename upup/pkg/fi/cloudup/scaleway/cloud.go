/*
Copyright 2022 The Kubernetes Authors.

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

package scaleway

import (
	"fmt"
	"os"
	"strings"

	domain "github.com/scaleway/scaleway-sdk-go/api/domain/v2beta1"
	iam "github.com/scaleway/scaleway-sdk-go/api/iam/v1alpha1"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	ipam "github.com/scaleway/scaleway-sdk-go/api/ipam/v1alpha1"
	"github.com/scaleway/scaleway-sdk-go/api/lb/v1"
	"github.com/scaleway/scaleway-sdk-go/api/marketplace/v2"
	"github.com/scaleway/scaleway-sdk-go/api/vpc/v2"
	"github.com/scaleway/scaleway-sdk-go/api/vpcgw/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	kopsv "k8s.io/kops"
	"k8s.io/kops/dnsprovider/pkg/dnsprovider"
	dns "k8s.io/kops/dnsprovider/pkg/dnsprovider/providers/scaleway"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/cloudinstances"
	"k8s.io/kops/upup/pkg/fi"
)

const (
	TagClusterName           = "noprefix=kops.k8s.io/cluster"
	TagInstanceGroup         = "noprefix=kops.k8s.io/instance-group"
	TagNameEtcdClusterPrefix = "noprefix=kops.k8s.io/etcd"
	TagNeedsUpdate           = "noprefix=kops.k8s.io/needs-update"
	TagNameRolePrefix        = "noprefix=kops.k8s.io/role"
	TagRoleControlPlane      = "ControlPlane"
	TagRoleWorker            = "Node"
	KopsUserAgentPrefix      = "kubernetes-kops/"
)

// ScwCloud exposes all the interfaces required to operate on Scaleway resources
type ScwCloud interface {
	fi.Cloud

	ClusterName(tags []string) string
	DNS() (dnsprovider.Interface, error)
	ProviderID() kops.CloudProviderID
	Region() string
	Zone() string

	DomainService() *domain.API
	GatewayService() *vpcgw.API
	IamService() *iam.API
	InstanceService() *instance.API
	IPAMService() *ipam.API
	LBService() *lb.ZonedAPI
	MarketplaceService() *marketplace.API
	VPCService() *vpc.API

	DeleteGroup(group *cloudinstances.CloudInstanceGroup) error
	DeleteInstance(i *cloudinstances.CloudInstance) error
	DeregisterInstance(instance *cloudinstances.CloudInstance) error
	DetachInstance(instance *cloudinstances.CloudInstance) error
	FindClusterStatus(cluster *kops.Cluster) (*kops.ClusterStatus, error)
	FindVPCInfo(id string) (*fi.VPCInfo, error)
	GetApiIngressStatus(cluster *kops.Cluster) ([]fi.ApiIngressStatus, error)
	GetCloudGroups(cluster *kops.Cluster, instancegroups []*kops.InstanceGroup, warnUnmatched bool, nodes []v1.Node) (map[string]*cloudinstances.CloudInstanceGroup, error)

	GetClusterDNSRecords(clusterName string) ([]*domain.Record, error)
	GetClusterDHCPConfigs() ([]*vpcgw.DHCP, error)
	GetClusterGatewayNetworks(clusterName string) ([]*vpcgw.GatewayNetwork, error)
	GetClusterGateways(clusterName string) ([]*vpcgw.Gateway, error)
	GetClusterLoadBalancers(clusterName string) ([]*lb.LB, error)
	GetClusterPrivateNetworks(clusterName string) ([]*vpc.PrivateNetwork, error)
	GetClusterServers(clusterName string, instanceGroupName *string) ([]*instance.Server, error)
	GetClusterSSHKeys(clusterName string) ([]*iam.SSHKey, error)
	GetClusterVolumes(clusterName string) ([]*instance.Volume, error)
	GetClusterVPCs(clusterName string) ([]*vpc.VPC, error)
	GetServerPrivateIP(serverName string, zone scw.Zone) (string, error)

	DeleteDNSRecord(record *domain.Record, clusterName string) error
	DeleteDHCPConfig(dhcpConfig *vpcgw.DHCP) error
	DeleteGateway(gateway *vpcgw.Gateway) error
	DeleteGatewayNetwork(gatewayNetwork *vpcgw.GatewayNetwork) error
	DeleteLoadBalancer(loadBalancer *lb.LB) error
	DeletePrivateNetwork(privateNetwork *vpc.PrivateNetwork) error
	DeleteServer(server *instance.Server) error
	DeleteSSHKey(sshkey *iam.SSHKey) error
	DeleteVolume(volume *instance.Volume) error
	DeleteVPC(vpc *vpc.VPC) error
}

// static compile time check to validate ScwCloud's fi.Cloud Interface.
var _ fi.Cloud = &scwCloudImplementation{}

// scwCloudImplementation holds the scw.Client object to interact with Scaleway resources.
type scwCloudImplementation struct {
	client *scw.Client
	region scw.Region
	zone   scw.Zone
	dns    dnsprovider.Interface
	tags   map[string]string

	domainAPI      *domain.API
	gatewayAPI     *vpcgw.API
	iamAPI         *iam.API
	instanceAPI    *instance.API
	ipamAPI        *ipam.API
	lbAPI          *lb.ZonedAPI
	marketplaceAPI *marketplace.API
	vpcAPI         *vpc.API
}

// NewScwCloud returns a Cloud with a Scaleway Client using the env vars SCW_PROFILE or
// SCW_ACCESS_KEY, SCW_SECRET_KEY and SCW_DEFAULT_PROJECT_ID
func NewScwCloud(tags map[string]string) (ScwCloud, error) {
	//displayEnv()
	var scwClient *scw.Client
	var region scw.Region
	var zone scw.Zone
	var err error

	if profileName := os.Getenv("SCW_PROFILE"); profileName == "REDACTED" {
		// If the profile is REDACTED, we're running integration tests so no need for authentication
		scwClient, err = scw.NewClient(scw.WithoutAuth())
		if err != nil {
			return nil, err
		}
	} else {
		profile, err := CreateValidScalewayProfile()
		if err != nil {
			return nil, err
		}
		scwClient, err = scw.NewClient(
			scw.WithProfile(profile),
			scw.WithUserAgent(KopsUserAgentPrefix+kopsv.Version),
		)
		if err != nil {
			return nil, fmt.Errorf("creating client for Scaleway Cloud: %w", err)
		}
		region = scw.Region(fi.ValueOf(profile.DefaultRegion))
		zone = scw.Zone(fi.ValueOf(profile.DefaultZone))
	}

	if tags != nil {
		region, err = scw.ParseRegion(tags["region"])
		if err != nil {
			return nil, err
		}
		zone, err = scw.ParseZone(tags["zone"])
		if err != nil {
			return nil, err
		}
	}

	return &scwCloudImplementation{
		client:         scwClient,
		region:         region,
		zone:           zone,
		dns:            dns.NewProvider(domain.NewAPI(scwClient)),
		tags:           tags,
		domainAPI:      domain.NewAPI(scwClient),
		gatewayAPI:     vpcgw.NewAPI(scwClient),
		iamAPI:         iam.NewAPI(scwClient),
		instanceAPI:    instance.NewAPI(scwClient),
		ipamAPI:        ipam.NewAPI(scwClient),
		lbAPI:          lb.NewZonedAPI(scwClient),
		marketplaceAPI: marketplace.NewAPI(scwClient),
		vpcAPI:         vpc.NewAPI(scwClient),
	}, nil
}

func (s *scwCloudImplementation) ClusterName(tags []string) string {
	return ClusterNameFromTags(tags)
}

func (s *scwCloudImplementation) DNS() (dnsprovider.Interface, error) {
	provider, err := dnsprovider.GetDnsProvider(dns.ProviderName, nil)
	if err != nil {
		return nil, fmt.Errorf("building DNS provider: %w", err)
	}
	return provider, nil
}

func (s *scwCloudImplementation) ProviderID() kops.CloudProviderID {
	return kops.CloudProviderScaleway
}

func (s *scwCloudImplementation) Region() string {
	return string(s.region)
}

func (s *scwCloudImplementation) Zone() string {
	return string(s.zone)
}

func (s *scwCloudImplementation) DomainService() *domain.API {
	return s.domainAPI
}

func (s *scwCloudImplementation) GatewayService() *vpcgw.API {
	return s.gatewayAPI
}

func (s *scwCloudImplementation) IamService() *iam.API {
	return s.iamAPI
}

func (s *scwCloudImplementation) InstanceService() *instance.API {
	return s.instanceAPI
}

func (s *scwCloudImplementation) IPAMService() *ipam.API {
	return s.ipamAPI
}

func (s *scwCloudImplementation) LBService() *lb.ZonedAPI {
	return s.lbAPI
}

func (s *scwCloudImplementation) MarketplaceService() *marketplace.API {
	return s.marketplaceAPI
}

func (s *scwCloudImplementation) VPCService() *vpc.API {
	return s.vpcAPI
}

// DeleteGroup deletes the cloud resources that make up a CloudInstanceGroup, including the instances.
func (s *scwCloudImplementation) DeleteGroup(group *cloudinstances.CloudInstanceGroup) error {
	toDelete := append(group.NeedUpdate, group.Ready...)
	for _, cloudInstance := range toDelete {
		err := s.DeleteInstance(cloudInstance)
		if err != nil {
			return fmt.Errorf("deleting group %q: %w", group.HumanName, err)
		}
	}
	return nil
}

func (s *scwCloudImplementation) DeleteInstance(i *cloudinstances.CloudInstance) error {
	server, err := s.instanceAPI.GetServer(&instance.GetServerRequest{
		Zone:     s.zone,
		ServerID: i.ID,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(4).Infof("deleting cloud instance %s of group %s: instance was already deleted", i.ID, i.CloudInstanceGroup.HumanName)
			return nil
		}
		return fmt.Errorf("deleting cloud instance %s of group %s: %w", i.ID, i.CloudInstanceGroup.HumanName, err)
	}

	err = s.DeleteServer(server.Server)
	if err != nil {
		return fmt.Errorf("deleting cloud instance %s of group %s: %w", i.ID, i.CloudInstanceGroup.HumanName, err)
	}

	return nil
}

func (s *scwCloudImplementation) DeregisterInstance(i *cloudinstances.CloudInstance) error {
	server, err := s.instanceAPI.GetServer(&instance.GetServerRequest{
		Zone:     s.zone,
		ServerID: i.ID,
	})
	if err != nil {
		return fmt.Errorf("deregistering cloud instance %s of group %q: %w", i.ID, i.CloudInstanceGroup.HumanName, err)
	}

	// We remove the instance's IP from load-balancers
	lbs, err := s.GetClusterLoadBalancers(s.ClusterName(server.Server.Tags))
	if err != nil {
		return fmt.Errorf("deregistering cloud instance %s of group %q: %w", i.ID, i.CloudInstanceGroup.HumanName, err)
	}
	for _, loadBalancer := range lbs {
		backEnds, err := s.lbAPI.ListBackends(&lb.ZonedAPIListBackendsRequest{
			Zone: s.zone,
			LBID: loadBalancer.ID,
		}, scw.WithAllPages())
		if err != nil {
			return fmt.Errorf("deregistering cloud instance %s of group %q: listing load-balancer's back-ends for instance creation: %w", i.ID, i.CloudInstanceGroup.HumanName, err)
		}
		for _, backEnd := range backEnds.Backends {
			for _, serverIP := range backEnd.Pool {
				// TODO(Mia-Cross): replace PrivateIP by IPAM
				if serverIP == fi.ValueOf(server.Server.PrivateIP) {
					_, err := s.lbAPI.RemoveBackendServers(&lb.ZonedAPIRemoveBackendServersRequest{
						Zone:      s.zone,
						BackendID: backEnd.ID,
						ServerIP:  []string{serverIP},
					})
					if err != nil {
						return fmt.Errorf("deregistering cloud instance %s of group %q: removing IP from lb: %w", i.ID, i.CloudInstanceGroup.HumanName, err)
					}
				}
			}
		}
	}

	return nil
}

func (s *scwCloudImplementation) DetachInstance(i *cloudinstances.CloudInstance) error {
	klog.V(8).Infof("Scaleway DetachInstance is not implemented yet")
	return fmt.Errorf("DetachInstance is not implemented yet for Scaleway")
}

// FindClusterStatus was used before etcd-manager to check the etcd cluster status and prevent unsupported changes.
func (s *scwCloudImplementation) FindClusterStatus(cluster *kops.Cluster) (*kops.ClusterStatus, error) {
	klog.V(8).Info("Scaleway FindClusterStatus is not implemented")
	return nil, nil
}

// FindVPCInfo is not implemented yet, it's only here to satisfy the fi.Cloud interface
func (s *scwCloudImplementation) FindVPCInfo(id string) (*fi.VPCInfo, error) {
	klog.V(8).Info("Scaleway clusters don't have a VPC yet so FindVPCInfo is not implemented")
	return nil, fmt.Errorf("FindVPCInfo is not implemented yet for Scaleway")
}

func (s *scwCloudImplementation) GetApiIngressStatus(cluster *kops.Cluster) ([]fi.ApiIngressStatus, error) {
	var ingresses []fi.ApiIngressStatus
	name := "api." + cluster.Name

	responseLoadBalancers, err := s.lbAPI.ListLBs(&lb.ZonedAPIListLBsRequest{
		Zone: s.zone,
		Name: &name,
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("finding load-balancers: %w", err)
	}
	if len(responseLoadBalancers.LBs) == 0 {
		klog.V(8).Infof("Could not find any load-balancers for cluster %s", cluster.Name)
		return nil, nil
	}
	if len(responseLoadBalancers.LBs) > 1 {
		klog.V(4).Infof("More than 1 load-balancer with the name %s was found", name)
	}

	for _, loadBalancer := range responseLoadBalancers.LBs {
		for _, lbIP := range loadBalancer.IP {
			ingresses = append(ingresses, fi.ApiIngressStatus{IP: lbIP.IPAddress})
		}
	}

	return ingresses, nil
}

func (s *scwCloudImplementation) GetCloudGroups(cluster *kops.Cluster, instancegroups []*kops.InstanceGroup, warnUnmatched bool, nodes []v1.Node) (map[string]*cloudinstances.CloudInstanceGroup, error) {
	groups := make(map[string]*cloudinstances.CloudInstanceGroup)

	nodeMap := cloudinstances.GetNodeMap(nodes, cluster)

	serverGroups, err := findServerGroups(s, cluster.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to find server groups: %w", err)
	}

	for _, ig := range instancegroups {
		serverGroup, ok := serverGroups[ig.Name]
		if !ok {
			if warnUnmatched {
				klog.Warningf("Server group %q has no corresponding instance group", ig.Name)
			}
			continue
		}

		groups[ig.Name], err = buildCloudGroup(s, ig, serverGroup, nodeMap)
		if err != nil {
			return nil, fmt.Errorf("failed to build cloud group for instance group %q: %w", ig.Name, err)
		}
	}

	return groups, nil
}

func findServerGroups(s *scwCloudImplementation, clusterName string) (map[string][]*instance.Server, error) {
	servers, err := s.GetClusterServers(clusterName, nil)
	if err != nil {
		return nil, err
	}

	serverGroups := make(map[string][]*instance.Server)
	for _, server := range servers {
		igName := InstanceGroupNameFromTags(server.Tags)
		serverGroups[igName] = append(serverGroups[igName], server)
	}

	return serverGroups, nil
}

func buildCloudGroup(s *scwCloudImplementation, ig *kops.InstanceGroup, sg []*instance.Server, nodeMap map[string]*v1.Node) (*cloudinstances.CloudInstanceGroup, error) {
	cloudInstanceGroup := &cloudinstances.CloudInstanceGroup{
		HumanName:     ig.Name,
		InstanceGroup: ig,
		Raw:           sg,
		MinSize:       int(fi.ValueOf(ig.Spec.MinSize)),
		TargetSize:    int(fi.ValueOf(ig.Spec.MinSize)),
		MaxSize:       int(fi.ValueOf(ig.Spec.MaxSize)),
	}

	for _, server := range sg {
		status := cloudinstances.CloudInstanceStatusUpToDate
		for _, tag := range server.Tags {
			if tag == TagNeedsUpdate {
				status = cloudinstances.CloudInstanceStatusNeedsUpdate
			}
		}
		cloudInstance, err := cloudInstanceGroup.NewCloudInstance(server.ID, status, nodeMap[server.ID])
		if err != nil {
			return nil, fmt.Errorf("failed to create cloud instance for server %s(%s): %w", server.Name, server.ID, err)
		}
		cloudInstance.State = cloudinstances.State(server.State)
		cloudInstance.MachineType = server.CommercialType
		cloudInstance.Roles = append(cloudInstance.Roles, InstanceRoleFromTags(server.Tags))
		ip, err := s.GetServerPrivateIP(server.Name, server.Zone)
		if err != nil {
			return nil, fmt.Errorf("getting server private IP: %w", err)
		}
		cloudInstance.PrivateIP = ip
	}

	return cloudInstanceGroup, nil
}

func (s *scwCloudImplementation) GetClusterDNSRecords(clusterName string) ([]*domain.Record, error) {
	names := strings.SplitN(clusterName, ".", 2)
	clusterNameShort := names[0]
	domainName := names[1]

	records, err := s.domainAPI.ListDNSZoneRecords(&domain.ListDNSZoneRecordsRequest{
		DNSZone: domainName,
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("listing cluster DNS records: %w", err)
	}

	clusterDNSRecords := []*domain.Record(nil)
	for _, record := range records.Records {
		if strings.HasSuffix(record.Name, clusterNameShort) {
			clusterDNSRecords = append(clusterDNSRecords, record)
		}
	}
	return clusterDNSRecords, nil
}

func (s *scwCloudImplementation) GetClusterDHCPConfigs() ([]*vpcgw.DHCP, error) {
	dhcpConfigs, err := s.gatewayAPI.ListDHCPs(&vpcgw.ListDHCPsRequest{
		Zone: s.zone,
		//Address:        nil,
		//HasAddress:     nil,
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list DHCP configs: %w", err)
	}
	return dhcpConfigs.Dhcps, nil
}

func (s *scwCloudImplementation) GetClusterGatewayNetworks(privateNetworkID string) ([]*vpcgw.GatewayNetwork, error) {
	gwNetworks, err := s.gatewayAPI.ListGatewayNetworks(&vpcgw.ListGatewayNetworksRequest{
		Zone:             s.zone,
		PrivateNetworkID: scw.StringPtr(privateNetworkID),
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway networks: %w", err)
	}
	return gwNetworks.GatewayNetworks, nil
}

func (s *scwCloudImplementation) GetClusterGateways(clusterName string) ([]*vpcgw.Gateway, error) {
	gws, err := s.gatewayAPI.ListGateways(&vpcgw.ListGatewaysRequest{
		Zone: s.zone,
		Tags: []string{TagClusterName + "=" + clusterName},
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway: %w", err)
	}
	return gws.Gateways, nil
}

func (s *scwCloudImplementation) GetClusterLoadBalancers(clusterName string) ([]*lb.LB, error) {
	loadBalancerName := "api." + clusterName
	lbs, err := s.lbAPI.ListLBs(&lb.ZonedAPIListLBsRequest{
		Zone: s.zone,
		Name: &loadBalancerName,
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("listing cluster load-balancers: %w", err)
	}
	return lbs.LBs, nil
}

func (s *scwCloudImplementation) GetClusterServers(clusterName string, instanceGroupName *string) ([]*instance.Server, error) {
	tags := []string{TagClusterName + "=" + clusterName}
	if instanceGroupName != nil {
		tags = append(tags, fmt.Sprintf("%s=%s", TagInstanceGroup, *instanceGroupName))
	}
	request := &instance.ListServersRequest{
		Zone: s.zone,
		Name: instanceGroupName,
		Tags: tags,
	}
	servers, err := s.instanceAPI.ListServers(request, scw.WithAllPages())
	if err != nil {
		if instanceGroupName != nil {
			return nil, fmt.Errorf("failed to list cluster servers named %q: %w", *instanceGroupName, err)
		}
		return nil, fmt.Errorf("failed to list cluster servers: %w", err)
	}
	return servers.Servers, nil
}

func (s *scwCloudImplementation) GetClusterSSHKeys(clusterName string) ([]*iam.SSHKey, error) {
	clusterSSHKeys := []*iam.SSHKey(nil)
	allSSHKeys, err := s.iamAPI.ListSSHKeys(&iam.ListSSHKeysRequest{}, scw.WithAllPages())
	for _, sshkey := range allSSHKeys.SSHKeys {
		if strings.HasPrefix(sshkey.Name, fmt.Sprintf("kubernetes.%s-", clusterName)) {
			clusterSSHKeys = append(clusterSSHKeys, sshkey)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster ssh keys: %w", err)
	}
	return clusterSSHKeys, nil
}

func (s *scwCloudImplementation) GetClusterVolumes(clusterName string) ([]*instance.Volume, error) {
	volumes, err := s.instanceAPI.ListVolumes(&instance.ListVolumesRequest{
		Zone: s.zone,
		Tags: []string{TagClusterName + "=" + clusterName},
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster volumes: %w", err)
	}
	return volumes.Volumes, nil
}

func (s *scwCloudImplementation) GetClusterVPCs(clusterName string) ([]*vpc.VPC, error) {
	vpcs, err := s.vpcAPI.ListVPCs(&vpc.ListVPCsRequest{
		Region: s.region,
		Tags:   []string{TagClusterName + "=" + clusterName},
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster VPCs: %w", err)
	}
	return vpcs.Vpcs, nil
}

func (s *scwCloudImplementation) GetClusterPrivateNetworks(clusterName string) ([]*vpc.PrivateNetwork, error) {
	pns, err := s.vpcAPI.ListPrivateNetworks(&vpc.ListPrivateNetworksRequest{
		Region: s.region,
		Tags:   []string{TagClusterName + "=" + clusterName},
	}, scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster private networks: %w", err)
	}
	return pns.PrivateNetworks, nil
}

func (s *scwCloudImplementation) GetServerPrivateIP(serverName string, zone scw.Zone) (string, error) {
	region, err := zone.Region()
	if err != nil {
		return "", fmt.Errorf("converting zone %s to region: %w", zone, err)
	}
	//resourceType := "instance_server"
	ips, err := s.ipamAPI.ListIPs(&ipam.ListIPsRequest{
		Region:       region,
		IsIPv6:       fi.PtrTo(false),
		ResourceName: fi.PtrTo(serverName),
		//ProjectID:        nil,
		//OrganizationID:   nil,
		//Zonal:            nil,
		//ZonalNat:         nil,
		//Regional: fi.PtrTo(false),
		//PrivateNetworkID: fi.PtrTo("fbd5db06-cc23-40b2-b117-5c05af970545"),
		//SubnetID:         nil,
		//Attached:         nil,
		//ResourceID: fi.PtrTo("4de70e77-af3c-4680-8a8e-0bf4c5b1f3e2"),
		//ResourceType: ipam.ResourceType(resourceType),
		//MacAddress:       nil,
		//Tags:             nil,
		//ResourceIDs:      nil,
	}, scw.WithAllPages())
	if err != nil {
		return "", fmt.Errorf("listing IPs for server %q: %w", serverName, err)
		//return "", fmt.Errorf("listing IPs for %s %s: %w", resourceType, serverID, err)
	}
	if ips.TotalCount < 1 {
		return "", fmt.Errorf("could not find IP for server %q", serverName)
		//return "", fmt.Errorf("could not find IP for %s %s", resourceType, serverID)
	}

	ipNet := ips.IPs[0].Address
	ip := ipNet.String()
	if ipNet.Mask != nil {
		ip = ipNet.IP.String()
	}

	if ips.TotalCount > 1 {
		klog.Infof("Found more than 1 IP for server %q, using %s", serverName, ip)
		//klog.Infof("Found more than 1 IP for %s %s, using %s", resourceType, serverID, ips.IPs[0].Address.String())
	}
	return ip, nil
}

func (s *scwCloudImplementation) DeleteDNSRecord(record *domain.Record, clusterName string) error {
	domainName := strings.SplitN(clusterName, ".", 2)[1]
	recordDeleteRequest := &domain.UpdateDNSZoneRecordsRequest{
		DNSZone: domainName,
		Changes: []*domain.RecordChange{
			{
				Delete: &domain.RecordChangeDelete{
					ID: scw.StringPtr(record.ID),
				},
			},
		},
	}
	_, err := s.domainAPI.UpdateDNSZoneRecords(recordDeleteRequest)
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("DNS record %q (%s) was already deleted", record.Name, record.ID)
			return nil
		}
		return fmt.Errorf("failed to delete record %s: %w", record.Name, err)
	}
	return nil
}

func (s *scwCloudImplementation) DeleteDHCPConfig(dhcpConfig *vpcgw.DHCP) error {
	err := s.gatewayAPI.DeleteDHCP(&vpcgw.DeleteDHCPRequest{
		Zone:   s.zone,
		DHCPID: dhcpConfig.ID,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("DHCP config %s was already deleted", dhcpConfig.ID)
			return nil
		}
		return fmt.Errorf("failed to delete DHCP config: %w", err)
	}
	return nil
}

func (s *scwCloudImplementation) DeleteGateway(gateway *vpcgw.Gateway) error {
	// We detach the IP of the gateway
	_, err := s.gatewayAPI.WaitForGateway(&vpcgw.WaitForGatewayRequest{
		GatewayID: gateway.ID,
		Zone:      s.zone,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("Gateway %q (%s) was already deleted", gateway.Name, gateway.ID)
			return nil
		}
		return fmt.Errorf("waiting for gateway: %w", err)
	}

	_, err = s.gatewayAPI.UpdateIP(&vpcgw.UpdateIPRequest{
		Zone:      s.zone,
		IPID:      gateway.IP.ID,
		GatewayID: scw.StringPtr(""),
	})
	if err != nil {
		return fmt.Errorf("failed to detach gateway IP: %w", err)
	}

	// We delete the IP of the gateway
	_, err = s.gatewayAPI.WaitForGateway(&vpcgw.WaitForGatewayRequest{
		GatewayID: gateway.ID,
		Zone:      s.zone,
	})
	if err != nil {
		return fmt.Errorf("waiting for gateway: %w", err)
	}

	err = s.gatewayAPI.DeleteIP(&vpcgw.DeleteIPRequest{
		Zone: s.zone,
		IPID: gateway.IP.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to delete gateway IP: %w", err)
	}

	// We delete the gateway once it's in a stable state
	_, err = s.gatewayAPI.WaitForGateway(&vpcgw.WaitForGatewayRequest{
		GatewayID: gateway.ID,
		Zone:      s.zone,
	})
	if err != nil {
		return fmt.Errorf("waiting for gateway: %w", err)
	}
	err = s.gatewayAPI.DeleteGateway(&vpcgw.DeleteGatewayRequest{
		Zone:        s.zone,
		GatewayID:   gateway.ID,
		CleanupDHCP: true,
	})
	if err != nil {
		return fmt.Errorf("failed to delete gateway %s: %w", gateway.ID, err)
	}

	return nil
}

func (s *scwCloudImplementation) DeleteGatewayNetwork(gatewayNetwork *vpcgw.GatewayNetwork) error {
	// We look for gateway connexions to private networks and detach them before deleting the gateway
	//connexions, err := s.GetClusterGatewayNetworks(gatewayN.ID)
	//if err != nil {
	//	if is404Error(err) {
	//		klog.V(8).Infof("Gateway %q (%s) was already deleted", gateway.Name, gateway.ID)
	//		return nil
	//	}
	//	return fmt.Errorf("listing gateway networks: %w", err)
	//}
	//for _, connexion := range connexions {
	err := s.gatewayAPI.DeleteGatewayNetwork(&vpcgw.DeleteGatewayNetworkRequest{
		Zone:             s.zone,
		GatewayNetworkID: gatewayNetwork.ID,
		CleanupDHCP:      true,
	})
	if err != nil {
		return fmt.Errorf("failed to delete gateway network %s from private network: %w", gatewayNetwork.ID, err)
	}
	return nil
}

func (s *scwCloudImplementation) DeleteLoadBalancer(loadBalancer *lb.LB) error {
	ipsToRelease := loadBalancer.IP

	// We delete the load-balancer once it's in a stable state
	_, err := s.lbAPI.WaitForLb(&lb.ZonedAPIWaitForLBRequest{
		LBID: loadBalancer.ID,
		Zone: s.zone,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("Load-balancer %q (%s) was already deleted", loadBalancer.Name, loadBalancer.ID)
			return nil
		}
		return fmt.Errorf("waiting for load-balancer: %w", err)
	}
	err = s.lbAPI.DeleteLB(&lb.ZonedAPIDeleteLBRequest{
		Zone: s.zone,
		LBID: loadBalancer.ID,
	})
	if err != nil {
		return fmt.Errorf("deleting load-balancer %s: %w", loadBalancer.ID, err)
	}

	// We wait for the load-balancer to be deleted, then we detach its IPs
	_, err = s.lbAPI.WaitForLb(&lb.ZonedAPIWaitForLBRequest{
		LBID: loadBalancer.ID,
		Zone: s.zone,
	})
	if !is404Error(err) {
		return fmt.Errorf("waiting for load-balancer %s after deletion: %w", loadBalancer.ID, err)
	}
	for _, ip := range ipsToRelease {
		err := s.lbAPI.ReleaseIP(&lb.ZonedAPIReleaseIPRequest{
			Zone: s.zone,
			IPID: ip.ID,
		})
		if err != nil {
			return fmt.Errorf("deleting load-balancer IP: %w", err)
		}
	}
	return nil
}

func (s *scwCloudImplementation) DeletePrivateNetwork(privateNetwork *vpc.PrivateNetwork) error {
	err := s.vpcAPI.DeletePrivateNetwork(&vpc.DeletePrivateNetworkRequest{
		PrivateNetworkID: privateNetwork.ID,
		Region:           s.region,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("Private network %q (%s) was already deleted", privateNetwork.Name, privateNetwork.ID)
			return nil
		}
		return fmt.Errorf("failed to delete private network %s: %w", privateNetwork.ID, err)
	}
	return nil
}

func (s *scwCloudImplementation) DeleteServer(server *instance.Server) error {
	srv, err := s.instanceAPI.GetServer(&instance.GetServerRequest{
		Zone:     s.zone,
		ServerID: server.ID,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(4).Infof("delete server %s: instance %q was already deleted", server.ID, server.Name)
			return nil
		}
		return err
	}

	// We detach the etcd volumes
	for _, volume := range srv.Server.Volumes {
		volumeResponse, err := s.instanceAPI.GetVolume(&instance.GetVolumeRequest{
			Zone:     s.zone,
			VolumeID: volume.ID,
		})
		if err != nil {
			return fmt.Errorf("delete server %s: getting infos for volume %s", server.ID, volume.ID)
		}
		for _, tag := range volumeResponse.Volume.Tags {
			if strings.HasPrefix(tag, TagNameEtcdClusterPrefix) {
				_, err = s.instanceAPI.DetachVolume(&instance.DetachVolumeRequest{
					Zone:     s.zone,
					VolumeID: volume.ID,
				})
				if err != nil {
					return fmt.Errorf("delete server %s: detaching volume %s", server.ID, volume.ID)
				}
			}
		}
	}

	// We detach the private network
	if len(srv.Server.PrivateNics) > 0 {
		err = s.instanceAPI.DeletePrivateNIC(&instance.DeletePrivateNICRequest{
			Zone:         s.zone,
			ServerID:     server.ID,
			PrivateNicID: srv.Server.PrivateNics[0].ID,
		})
		if err != nil {
			return fmt.Errorf("delete server %s: detaching private network: %w", server.ID, err)
		}
		//return err
	}

	// We detach the etcd volumes
	for _, volume := range srv.Server.Volumes {
		volumeResponse, err := s.instanceAPI.GetVolume(&instance.GetVolumeRequest{
			Zone:     s.zone,
			VolumeID: volume.ID,
		})
		if err != nil {
			return fmt.Errorf("delete server %s: getting infos for volume %s", server.ID, volume.ID)
		}
		for _, tag := range volumeResponse.Volume.Tags {
			if strings.HasPrefix(tag, TagNameEtcdClusterPrefix) {
				_, err = s.instanceAPI.DetachVolume(&instance.DetachVolumeRequest{
					Zone:     s.zone,
					VolumeID: volume.ID,
				})
				if err != nil {
					return fmt.Errorf("delete server %s: detaching volume %s", server.ID, volume.ID)
				}
			}
		}
	}

	// We terminate the server. This stops and deletes the machine immediately
	_, err = s.instanceAPI.ServerAction(&instance.ServerActionRequest{
		Zone:     s.zone,
		ServerID: server.ID,
		Action:   instance.ServerActionTerminate,
	})
	if err != nil && !is404Error(err) {
		return fmt.Errorf("delete server %s: terminating instance: %w", server.ID, err)
	}

	_, err = s.instanceAPI.WaitForServer(&instance.WaitForServerRequest{
		ServerID: server.ID,
		Zone:     s.zone,
	})
	if err != nil && !is404Error(err) {
		return fmt.Errorf("delete server %s: waiting for instance after termination: %w", server.ID, err)
	}

	return nil
}

func (s *scwCloudImplementation) DeleteSSHKey(sshkey *iam.SSHKey) error {
	err := s.iamAPI.DeleteSSHKey(&iam.DeleteSSHKeyRequest{
		SSHKeyID: sshkey.ID,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("SSH key %q (%s) was already deleted", sshkey.Name, sshkey.ID)
			return nil
		}
		return fmt.Errorf("failed to delete ssh key %s: %w", sshkey.ID, err)
	}
	return nil
}

func (s *scwCloudImplementation) DeleteVolume(volume *instance.Volume) error {
	err := s.instanceAPI.DeleteVolume(&instance.DeleteVolumeRequest{
		VolumeID: volume.ID,
		Zone:     s.zone,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("Volume %q (%s) was already deleted", volume.Name, volume.ID)
			return nil
		}
		return fmt.Errorf("failed to delete volume %s: %w", volume.ID, err)
	}

	_, err = s.instanceAPI.WaitForVolume(&instance.WaitForVolumeRequest{
		VolumeID: volume.ID,
		Zone:     s.zone,
	})
	if !is404Error(err) {
		return fmt.Errorf("delete volume %s: waiting for volume after deletion: %w", volume.ID, err)
	}

	return nil
}

func (s *scwCloudImplementation) DeleteVPC(v *vpc.VPC) error {
	err := s.vpcAPI.DeleteVPC(&vpc.DeleteVPCRequest{
		Region: s.region,
		VpcID:  v.ID,
	})
	if err != nil {
		if is404Error(err) {
			klog.V(8).Infof("VPC %q (%s) was already deleted", v.Name, v.ID)
			return nil
		}
		return fmt.Errorf("failed to delete VPC %s: %w", v.ID, err)
	}
	return nil
}
