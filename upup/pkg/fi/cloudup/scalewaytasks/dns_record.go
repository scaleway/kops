package scalewaytasks

import (
	"k8s.io/kops/upup/pkg/fi/cloudup/terraform"
	"k8s.io/kops/upup/pkg/fi/cloudup/terraformWriter"
)

/*

resource "scaleway_domain_record" "api" {
name = "api.amsterdam"
data     = scaleway_instance_server.control-plane-nl-ams-1.public_ip
dns_zone = "leila.sieben.fr"
type     = "A"
}

resource "scaleway_domain_record" "kops-controller" {
name = "kops-controller.internal.amsterdam"
data     = scaleway_instance_server.control-plane-nl-ams-1.private_ip
dns_zone = "leila.sieben.fr"
type     = "A"
}

resource "scaleway_domain_record" "api-internal" {
name = "api.internal.amsterdam"
data     = scaleway_instance_server.control-plane-nl-ams-1.private_ip
dns_zone = "leila.sieben.fr"
type     = "A"
}

*/

// +kops:fitask
type DNSRecord struct {
	Name    string
	Data    string
	DNSZone string
	Type    string
}

type terraformDNSRecord struct {
	Name    string                   `cty:"name"`
	Data    *terraformWriter.Literal `cty:"data"`
	DNSZone string                   `cty:"dns_zone"`
	Type    string                   `cty:"type"`
}

func (d *DNSRecord) RenderTerraform(t *terraform.TerraformTarget, actual, expected, changes *DNSRecord) error {
	//var serverIPs []*terraformWriter.Literal
	//for _, instance := range expected.ControlPlanes {
	//	if instance.Role != nil && *instance.Role == scaleway.TagRoleControlPlane {
	//		serverIPs = append(serverIPs, instance.TerraformLinkIP())
	//	}
	//}

	tf := terraformDNSRecord{
		Name:    expected.Name,
		Data:    nil,
		DNSZone: expected.DNSZone,
		Type:    expected.Type,
	}

	return t.RenderResource("scaleway_domain_record", expected.Name, tf)
}
