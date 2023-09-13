/*
Copyright 2020 The Kubernetes Authors.

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

package terraform

import (
	"bytes"
	"fmt"
	"sort"

	"k8s.io/klog/v2"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/featureflag"
	"k8s.io/kops/upup/pkg/fi/cloudup/scaleway"
	"k8s.io/kops/upup/pkg/fi/cloudup/terraformWriter"
)

func (t *TerraformTarget) finishHCL2() error {
	buf := &bytes.Buffer{}

	outputs, err := t.GetOutputs()
	if err != nil {
		return err
	}
	writeLocalsOutputs(buf, outputs)

	t.writeProviders(buf)

	resourcesByType, err := t.GetResourcesByType()
	if err != nil {
		return err
	}

	t.writeResources(buf, resourcesByType)

	dataSourcesByType, err := t.GetDataSourcesByType()
	if err != nil {
		return err
	}

	t.writeDataSources(buf, dataSourcesByType)

	t.writeTerraform(buf)

	t.Files["kubernetes.tf"] = buf.Bytes()

	return nil
}

type output struct {
	Value *terraformWriter.Literal
}

// writeLocalsOutputs creates the locals block and output blocks for all output variables
// Example:
//
//	locals {
//	  key1 = "value1"
//	  key2 = "value2"
//	}
//
//	output "key1" {
//	  value = "value1"
//	}
//
//	output "key2" {
//	  value = "value2"
//	}
func writeLocalsOutputs(buf *bytes.Buffer, outputs map[string]terraformWriter.OutputValue) {
	if len(outputs) == 0 {
		return
	}

	outputNames := make([]string, 0, len(outputs))
	locals := make(map[string]*terraformWriter.Literal, len(outputs))
	for k, v := range outputs {
		if _, ok := locals[k]; ok {
			panic(fmt.Sprintf("duplicate variable found: %s", k))
		}
		if v.Value != nil {
			locals[k] = v.Value
		} else {
			locals[k] = terraformWriter.LiteralListExpression(v.ValueArray...)
		}
		outputNames = append(outputNames, k)
	}
	sort.Strings(outputNames)

	mapToElement(locals).
		ToObject().
		Write(buf, 0, "locals")
	buf.WriteString("\n")

	for _, tfName := range outputNames {
		toElement(&output{Value: locals[tfName]}).Write(buf, 0, fmt.Sprintf("output %q", tfName))
		buf.WriteString("\n")
	}
	return
}

func (t *TerraformTarget) writeProviders(buf *bytes.Buffer) {
	providerName := string(t.Cloud.ProviderID())
	if t.Cloud.ProviderID() == kops.CloudProviderGCE {
		providerName = "google"
	}
	if t.Cloud.ProviderID() == kops.CloudProviderHetzner {
		providerName = "hcloud"
	}
	providerBody := map[string]string{}
	if t.Cloud.ProviderID() == kops.CloudProviderGCE {
		providerBody["project"] = t.Project
	}
	if t.Cloud.ProviderID() != kops.CloudProviderHetzner && t.Cloud.ProviderID() != kops.CloudProviderDO {
		providerBody["region"] = t.Cloud.Region()
	}
	if t.Cloud.ProviderID() == kops.CloudProviderScaleway {
		providerBody["zone"] = t.Cloud.(scaleway.ScwCloud).Zone()
	}
	for k, v := range tfGetProviderExtraConfig(t.clusterSpecTarget) {
		providerBody[k] = v
	}
	mapToElement(providerBody).
		ToObject().
		Write(buf, 0, fmt.Sprintf("provider %q", providerName))
	buf.WriteString("\n")

	// Add any additional provider definition for managed files
	keys := sortedKeysForMap(t.TerraformWriter.Providers)
	for _, key := range keys {
		provider := t.TerraformWriter.Providers[key]
		providerBody := map[string]string{}
		providerBody["alias"] = "files"
		for k, v := range provider.Arguments {
			providerBody[k] = v
		}
		for k, v := range tfGetFilesProviderExtraConfig(t.clusterSpecTarget) {
			providerBody[k] = v
		}
		mapToElement(providerBody).
			ToObject().
			Write(buf, 0, fmt.Sprintf("provider %q", provider.Name))
		buf.WriteString("\n")
	}
}

func sortedKeysForMap[K ~string, V any](m map[K]V) []K {
	var keys []K
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func (t *TerraformTarget) writeResources(buf *bytes.Buffer, resourcesByType map[string]map[string]interface{}) {
	resourceTypes := make([]string, 0, len(resourcesByType))
	for resourceType := range resourcesByType {
		resourceTypes = append(resourceTypes, resourceType)
	}
	sort.Strings(resourceTypes)
	for _, resourceType := range resourceTypes {
		resources := resourcesByType[resourceType]
		resourceNames := make([]string, 0, len(resources))
		for resourceName := range resources {
			resourceNames = append(resourceNames, resourceName)
		}
		sort.Strings(resourceNames)
		for _, resourceName := range resourceNames {
			toElement(resources[resourceName]).
				Write(buf, 0, fmt.Sprintf("resource %q %q", resourceType, resourceName))
			buf.WriteString("\n")
		}
	}
}

func (t *TerraformTarget) writeDataSources(buf *bytes.Buffer, dataSourcesByType map[string]map[string]interface{}) {
	dataSourceTypes := make([]string, 0, len(dataSourcesByType))
	for dataSourceType := range dataSourcesByType {
		dataSourceTypes = append(dataSourceTypes, dataSourceType)
	}
	sort.Strings(dataSourceTypes)
	for _, dataSourceType := range dataSourceTypes {
		dataSources := dataSourcesByType[dataSourceType]
		dataSourceNames := make([]string, 0, len(dataSources))
		for dataSourceName := range dataSources {
			dataSourceNames = append(dataSourceNames, dataSourceName)
		}
		sort.Strings(dataSourceNames)
		for _, dataSourceName := range dataSourceNames {
			toElement(dataSources[dataSourceName]).
				Write(buf, 0, fmt.Sprintf("data %q %q", dataSourceType, dataSourceName))
			buf.WriteString("\n")
		}
	}
}

func (t *TerraformTarget) writeTerraform(buf *bytes.Buffer) {
	buf.WriteString("terraform {\n")
	buf.WriteString("  required_version = \">= 0.15.0\"\n")
	buf.WriteString("  required_providers {\n")

	providers := make(map[string]bool)
	providerAliases := make(map[string][]string)
	if t.Cloud.ProviderID() == kops.CloudProviderGCE {
		providers["google"] = true
	} else if t.Cloud.ProviderID() == kops.CloudProviderHetzner {
		providers["hcloud"] = true
	} else if t.Cloud.ProviderID() == kops.CloudProviderScaleway {
		providers["scaleway"] = true
	} else if t.Cloud.ProviderID() == kops.CloudProviderAWS {
		providers["aws"] = true
		if featureflag.Spotinst.Enabled() {
			providers["spotinst"] = true
		}
	} else if t.Cloud.ProviderID() == kops.CloudProviderDO {
		providers["digitalocean"] = true
	}

	for _, tfProvider := range t.TerraformWriter.Providers {
		providers[tfProvider.Name] = true
		providerAliases[tfProvider.Name] = append(providerAliases[tfProvider.Name], "files")
	}

	providerKeys := sortedKeysForMap(providers)
	for _, provider := range providerKeys {
		// providerVersions could be a constant, but keeping it here
		// because it isn't shared and to allow for more complex logic in future.
		providerVersions := map[string]map[string]string{
			"aws": {
				"source":  "hashicorp/aws",
				"version": ">= 4.0.0",
			},
			"google": {
				"source":  "hashicorp/google",
				"version": ">= 2.19.0",
			},
			"hcloud": {
				"source":  "hetznercloud/hcloud",
				"version": ">= 1.35.1",
			},
			"spotinst": {
				"source":  "spotinst/spotinst",
				"version": ">= 1.33.0",
			},
			"scaleway": {
				"source":  "scaleway/scaleway",
				"version": ">= 2.2.1",
			},
			"digitalocean": {
				"source":  "digitalocean/digitalocean",
				"version": "~>2.0",
			},
		}

		providerVersion := providerVersions[provider]
		if providerVersion == nil {
			klog.Fatalf("unhandled provider %q", provider)
		}

		tf := make(map[string]*terraformWriter.Literal)
		for k, v := range providerVersion {
			tf[k] = terraformWriter.LiteralFromStringValue(v)
		}

		if aliases := providerAliases[provider]; len(aliases) != 0 {
			var configurationAliases []*terraformWriter.Literal
			for _, alias := range providerAliases[provider] {
				configurationAlias := terraformWriter.LiteralTokens(provider, alias)
				configurationAliases = append(configurationAliases, configurationAlias)
			}
			tf["configuration_aliases"] = terraformWriter.LiteralListExpression(configurationAliases...)
		}

		mapToElement(tf).Write(buf, 4, provider)
	}

	buf.WriteString("  }\n")
	buf.WriteString("}\n")
}
