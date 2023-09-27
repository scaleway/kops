package scalewaytasks

import (
	"fmt"

	domain "github.com/scaleway/scaleway-sdk-go/api/domain/v2beta1"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/cloudup/scaleway"
	"k8s.io/kops/upup/pkg/fi/cloudup/terraform"
	"k8s.io/kops/upup/pkg/fi/cloudup/terraformWriter"
)

const defaultTTL = uint32(60)

// +kops:fitask
type DNSRecord struct {
	Name      *string
	Data      *string
	DNSZone   *string
	Type      *string
	Lifecycle fi.Lifecycle
}

var _ fi.CloudupTask = &DNSRecord{}

func (l *DNSRecord) Find(context *fi.CloudupContext) (*DNSRecord, error) {
	cloud := context.T.Cloud.(scaleway.ScwCloud)
	records, err := cloud.DomainService().ListDNSZoneRecords(&domain.ListDNSZoneRecordsRequest{
		DNSZone: fi.ValueOf(l.DNSZone),
		Name:    fi.ValueOf(l.Name),
		Type:    domain.RecordType(fi.ValueOf(l.Type)),
	}, scw.WithContext(context.Context()), scw.WithAllPages())
	if err != nil {
		return nil, fmt.Errorf("listing DNS records named %q in zone %q: %w", l.Name, l.DNSZone, err)
	}

	if records.TotalCount == 0 {
		return nil, nil
	}
	// if records.TotalCount > 1 {}
	recordFound := records.Records[0]

	return &DNSRecord{
		Name:      fi.PtrTo(recordFound.Name),
		Data:      fi.PtrTo(recordFound.Data),
		DNSZone:   l.DNSZone,
		Type:      fi.PtrTo(recordFound.Type.String()),
		Lifecycle: l.Lifecycle,
	}, nil
}

func (d *DNSRecord) Run(context *fi.CloudupContext) error {
	return fi.CloudupDefaultDeltaRunMethod(d, context)
}

func (_ *DNSRecord) CheckChanges(actual, expected, changes *DNSRecord) error {
	if actual != nil {
		if changes.Name != nil {
			return fi.CannotChangeField("Name")
		}
		if changes.DNSZone != nil {
			return fi.CannotChangeField("DNSZone")
		}
		if changes.Type != nil {
			return fi.CannotChangeField("Type")
		}
	} else {
		if expected.Name == nil {
			return fi.RequiredField("Name")
		}
		if expected.DNSZone == nil {
			return fi.RequiredField("DNSZone")
		}
		if expected.Type == nil {
			return fi.RequiredField("Type")
		}
		if expected.Data == nil {
			return fi.RequiredField("Data")
		}
	}
	return nil
}

func (d *DNSRecord) RenderScw(t *scaleway.ScwAPITarget, actual, expected, changes *DNSRecord) error {
	if actual != nil {
		//TODO: see what we can update
		return nil
	}
	cloud := t.Cloud.(scaleway.ScwCloud)
	_, err := cloud.DomainService().UpdateDNSZoneRecords(&domain.UpdateDNSZoneRecordsRequest{
		DNSZone: fi.ValueOf(expected.DNSZone),
		Changes: []*domain.RecordChange{
			{
				Add: &domain.RecordChangeAdd{
					Records: []*domain.Record{
						{
							Data: fi.ValueOf(expected.Data),
							Name: fi.ValueOf(expected.Name),
							TTL:  defaultTTL,
							Type: domain.RecordType(fi.ValueOf(expected.Type)),
						},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating DNS record %q in zone %q: %w", fi.ValueOf(expected.Name), fi.ValueOf(expected.DNSZone), err)
	}
	return nil
}

type terraformDNSRecord struct {
	Name      *string              `cty:"name"`
	Data      *string              `cty:"data"`
	DNSZone   *string              `cty:"dns_zone"`
	Type      *string              `cty:"type"`
	Lifecycle *terraform.Lifecycle `cty:"lifecycle"`
}

func (_ *DNSRecord) RenderTerraform(t *terraform.TerraformTarget, actual, expected, changes *DNSRecord) error {
	tf := terraformDNSRecord{
		Name:    expected.Name,
		Data:    expected.Data,
		DNSZone: expected.DNSZone,
		Type:    expected.Type,
		Lifecycle: &terraform.Lifecycle{
			IgnoreChanges: []*terraformWriter.Literal{{String: "data"}},
		},
	}
	return t.RenderResource("scaleway_domain_record", fi.ValueOf(expected.Name), tf)
}

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
