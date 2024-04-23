package scalewaytasks

import (
	"fmt"

	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/cloudup/scaleway"
)

// +kops:fitask
type PrivateNIC struct {
	//ID   *string
	Name *string
	Zone *string
	Tags []string

	ForAPIServer bool
	Count        int

	Lifecycle      fi.Lifecycle
	Instance       *Instance
	PrivateNetwork *PrivateNetwork
}

var _ fi.CloudupTask = &PrivateNIC{}
var _ fi.CompareWithID = &PrivateNIC{}
var _ fi.CloudupHasDependencies = &PrivateNIC{}

//var _ fi.HasAddress = &PrivateNIC{}

func (p *PrivateNIC) CompareWithID() *string {
	return p.Name
}

func (p *PrivateNIC) GetDependencies(tasks map[string]fi.CloudupTask) []fi.CloudupTask {
	var deps []fi.CloudupTask
	for _, task := range tasks {
		if _, ok := task.(*Instance); ok {
			deps = append(deps, task)
		}
		if _, ok := task.(*PrivateNetwork); ok {
			deps = append(deps, task)
		}
	}
	return deps
}

/*
	func (p *PrivateNIC) IsForAPIServer() bool {
		return p.ForAPIServer
	}

	func (p *PrivateNIC) FindAddresses(context *fi.CloudupContext) ([]string, error) {
		cloud := context.T.Cloud.(scaleway.ScwCloud)
		region, err := scw.Zone(fi.ValueOf(p.Zone)).Region()
		if err != nil {
			return nil, fmt.Errorf("finding private NIC's region: %w", err)
		}

		servers, err := cloud.GetClusterServers(scaleway.ClusterNameFromTags(p.Tags), p.Name)
		if err != nil {
			return nil, err
		}

		var pnicIPs []string

		for _, server := range servers {

			pNICs, err := cloud.InstanceService().ListPrivateNICs(&instance.ListPrivateNICsRequest{
				Zone:     scw.Zone(cloud.Zone()),
				Tags:     p.Tags,
				ServerID: server.ID,
			}, scw.WithContext(context.Context()), scw.WithAllPages())
			if err != nil {
				return nil, fmt.Errorf("listing private NICs for instance %q: %w", fi.ValueOf(p.Name), err)
			}

			for _, pNIC := range pNICs.PrivateNics {

				ips, err := cloud.IPAMService().ListIPs(&ipam.ListIPsRequest{
					Region:           region,
					PrivateNetworkID: p.PrivateNetwork.ID,
					ResourceID:       &pNIC.ID,
				}, scw.WithContext(context.Context()), scw.WithAllPages())
				if err != nil {
					return nil, fmt.Errorf("listing private NIC's IPs: %w", err)
				}

				for _, ip := range ips.IPs {
					pnicIPs = append(pnicIPs, ip.Address.IP.String())
				}
			}
		}
		return pnicIPs, nil
	}
*/
func (p *PrivateNIC) Find(context *fi.CloudupContext) (*PrivateNIC, error) {
	cloud := context.T.Cloud.(scaleway.ScwCloud)
	servers, err := cloud.GetClusterServers(scaleway.ClusterNameFromTags(p.Tags), p.Name)
	if err != nil {
		return nil, err
	}

	var privateNICsFound []*instance.PrivateNIC
	for _, server := range servers {
		pNICs, err := cloud.InstanceService().ListPrivateNICs(&instance.ListPrivateNICsRequest{
			Zone:     scw.Zone(cloud.Zone()),
			Tags:     p.Tags,
			ServerID: server.ID,
		}, scw.WithContext(context.Context()), scw.WithAllPages())
		if err != nil {
			return nil, fmt.Errorf("listing private NICs for instance group %s: %w", fi.ValueOf(p.Name), err)
		}
		for _, pNIC := range pNICs.PrivateNics {
			privateNICsFound = append(privateNICsFound, pNIC)
		}
	}

	if len(privateNICsFound) == 0 {
		return nil, nil
	}
	pNICFound := privateNICsFound[0]

	forAPIServer := false
	instanceRole := scaleway.InstanceRoleFromTags(pNICFound.Tags)
	if instanceRole == scaleway.TagRoleControlPlane {
		forAPIServer = true
	}

	return &PrivateNIC{
		//ID:             fi.PtrTo(pNICFound.ID),
		Name:           p.Name,
		Zone:           p.Zone,
		Tags:           pNICFound.Tags,
		ForAPIServer:   forAPIServer,
		Count:          len(privateNICsFound),
		Lifecycle:      p.Lifecycle,
		Instance:       p.Instance,
		PrivateNetwork: p.PrivateNetwork,
	}, nil
}

func (p *PrivateNIC) Run(context *fi.CloudupContext) error {
	return fi.CloudupDefaultDeltaRunMethod(p, context)
}

func (p *PrivateNIC) CheckChanges(actual, expected, changes *PrivateNIC) error {
	if actual != nil {
		if changes.Name != nil {
			return fi.CannotChangeField("Name")
		}
		if changes.Zone != nil {
			return fi.CannotChangeField("Zone")
		}
	} else {
		if expected.Name == nil {
			return fi.RequiredField("Name")
		}
		if expected.Zone == nil {
			return fi.RequiredField("Zone")
		}
		//if expected.InstanceID == nil {
		//	return fi.RequiredField("InstanceID")
		//}
	}
	return nil
}

func (_ *PrivateNIC) RenderScw(t *scaleway.ScwAPITarget, actual, expected, changes *PrivateNIC) error {
	cloud := t.Cloud.(scaleway.ScwCloud)
	zone := scw.Zone(fi.ValueOf(expected.Zone))
	clusterName := scaleway.ClusterNameFromTags(expected.Instance.Tags)
	igName := fi.ValueOf(expected.Name)

	var serversNeedUpdate []string
	var serversNeedPNIC []string
	servers, err := cloud.GetClusterServers(clusterName, &igName)
	if err != nil {
		return fmt.Errorf("rendering private NIC for instance group %q: getting servers: %w", igName, err)
	}
	for _, server := range servers {
		if len(server.PrivateNics) > 0 {
			serversNeedUpdate = append(serversNeedUpdate, server.ID)
		} else {
			serversNeedPNIC = append(serversNeedPNIC, server.ID)
		}
	}

	if actual != nil {

		for _, serverID := range serversNeedUpdate {
			pNICs, err := cloud.InstanceService().ListPrivateNICs(&instance.ListPrivateNICsRequest{
				Zone:     zone,
				ServerID: serverID,
			}, scw.WithAllPages())

			for _, pNIC := range pNICs.PrivateNics {
				_, err = cloud.InstanceService().UpdatePrivateNIC(&instance.UpdatePrivateNICRequest{
					Zone:         zone,
					ServerID:     serverID,
					PrivateNicID: pNIC.ID,
					Tags:         fi.PtrTo(expected.Tags),
				})
				if err != nil {
					return fmt.Errorf("updating Private NIC %s for server %q: %w", pNIC.ID, serverID, err)
				}
			}
		}
	}

	for _, serverID := range serversNeedPNIC {
		pNICCreated, err := cloud.InstanceService().CreatePrivateNIC(&instance.CreatePrivateNICRequest{
			Zone:             zone,
			ServerID:         serverID,
			PrivateNetworkID: fi.ValueOf(expected.PrivateNetwork.ID),
			Tags:             expected.Tags,
			//IPIDs:
		})
		if err != nil {
			return fmt.Errorf("creating private NIC between instance %s and private network %s: %w", serverID, fi.ValueOf(expected.PrivateNetwork.ID), err)
		}

		// We wait for the private nic to be ready
		_, err = cloud.InstanceService().WaitForPrivateNIC(&instance.WaitForPrivateNICRequest{
			ServerID:     serverID,
			PrivateNicID: pNICCreated.PrivateNic.ID,
			Zone:         zone,
		})
		if err != nil {
			return fmt.Errorf("waiting for private NIC %s: %w", pNICCreated.PrivateNic.ID, err)
		}

	}

	return nil
}
