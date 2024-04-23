/*
Copyright 2017 The Kubernetes Authors.

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

package validation

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/kops/pkg/nodeidentity/aws"

	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/apis/kops/util"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/cloudup/awsup"
	"k8s.io/kops/upup/pkg/fi/cloudup/gce"
)

// ValidateInstanceGroup is responsible for validating the configuration of a instancegroup
func ValidateInstanceGroup(g *kops.InstanceGroup, cloud fi.Cloud, strict bool) field.ErrorList {
	allErrs := field.ErrorList{}

	if g.ObjectMeta.Name == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("objectMeta", "name"), ""))
	}

	switch g.Spec.Role {
	case "":
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "role"), "Role must be set"))
	case kops.InstanceGroupRoleControlPlane:
		if len(g.Spec.Subnets) == 0 {
			allErrs = append(allErrs, field.Required(field.NewPath("spec", "subnets"), "controlPlane InstanceGroup must specify at least one Subnet"))
		}
		if fi.ValueOf(g.Spec.MinSize) > 1 {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "minSize"), fi.ValueOf(g.Spec.MinSize), "controlPlane InstanceGroup must have minSize set to 1"))
		}
		if fi.ValueOf(g.Spec.MaxSize) > 1 {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "maxSize"), fi.ValueOf(g.Spec.MaxSize), "controlPlane InstanceGroup must have maxSize set to 1, add more InstanceGroups instead"))
		}
	case kops.InstanceGroupRoleNode:
	case kops.InstanceGroupRoleBastion:
	case kops.InstanceGroupRoleAPIServer:
	default:
		var supported []string
		for _, role := range kops.AllInstanceGroupRoles {
			supported = append(supported, string(role))
		}
		allErrs = append(allErrs, field.NotSupported(field.NewPath("spec", "role"), g.Spec.Role, supported))
	}

	if g.Spec.Tenancy != "" {
		tenancy := ec2types.Tenancy(g.Spec.Tenancy)
		allErrs = append(allErrs, IsValidValue(field.NewPath("spec", "tenancy"), &tenancy, ec2types.Tenancy("").Values())...)
	}

	if strict && g.Spec.Manager == kops.InstanceManagerCloudGroup {
		if g.Spec.MaxSize == nil {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "maxSize"), "maxSize must be set"))
		}
		if g.Spec.MinSize == nil {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "minSize"), "minSize must be set"))
		}
	}

	if g.Spec.MaxSize != nil && g.Spec.MinSize != nil {
		if *g.Spec.MaxSize < *g.Spec.MinSize {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "maxSize"), "maxSize must be greater than or equal to minSize."))
		}
	}

	if strict && g.Spec.Image == "" {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "image"), "image must be specified."))
	}

	if g.Spec.RootVolume != nil {
		if fi.ValueOf(g.Spec.RootVolume.IOPS) < 0 {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "rootVolume", "iops"), g.Spec.RootVolume.IOPS, "IOPS must be greater than 0"))
		}

		if fi.ValueOf(g.Spec.RootVolume.Throughput) < 0 {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "rootVolume", "throughput"), g.Spec.RootVolume.Throughput, "Throughput must be greater than 0"))
		}
	}

	// @check all the hooks are valid in this instancegroup
	for i := range g.Spec.Hooks {
		allErrs = append(allErrs, validateHookSpec(&g.Spec.Hooks[i], field.NewPath("spec", "hooks").Index(i))...)
	}

	// @check the fileAssets for this instancegroup are valid
	for i := range g.Spec.FileAssets {
		allErrs = append(allErrs, validateFileAssetSpec(&g.Spec.FileAssets[i], field.NewPath("spec", "fileAssets").Index(i))...)
	}

	for _, UserDataInfo := range g.Spec.AdditionalUserData {
		allErrs = append(allErrs, validateExtraUserData(&UserDataInfo)...)
	}

	// @step: iterate and check the volume specs
	for i, x := range g.Spec.Volumes {
		devices := make(map[string]bool)
		path := field.NewPath("spec", "volumes").Index(i)

		allErrs = append(allErrs, validateVolumeSpec(path, x)...)

		// @check the device name has not been used already
		if _, found := devices[x.Device]; found {
			allErrs = append(allErrs, field.Duplicate(path.Child("device"), x.Device))
		}

		devices[x.Device] = true
	}

	// @step: iterate and check the volume mount specs
	for i, x := range g.Spec.VolumeMounts {
		used := make(map[string]bool)
		path := field.NewPath("spec", "volumeMounts").Index(i)

		allErrs = append(allErrs, validateVolumeMountSpec(path, x)...)
		if _, found := used[x.Device]; found {
			allErrs = append(allErrs, field.Duplicate(path.Child("device"), x.Device))
		}
		if _, found := used[x.Path]; found {
			allErrs = append(allErrs, field.Duplicate(path.Child("path"), x.Path))
		}
	}

	allErrs = append(allErrs, validateInstanceProfile(g.Spec.IAM, field.NewPath("spec", "iam"))...)

	for i, sysctlParameter := range g.Spec.SysctlParameters {
		if !strings.ContainsRune(sysctlParameter, '=') {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "sysctlParameters").Index(i), sysctlParameter, "must contain a \"=\" character"))
		}
	}

	if g.Spec.RollingUpdate != nil {
		allErrs = append(allErrs, validateRollingUpdate(g.Spec.RollingUpdate, field.NewPath("spec", "rollingUpdate"), g.Spec.Role == kops.InstanceGroupRoleControlPlane)...)
	}

	if g.Spec.NodeLabels != nil {
		allErrs = append(allErrs, validateNodeLabels(g.Spec.NodeLabels, field.NewPath("spec", "nodeLabels"))...)
	}

	if g.Spec.CloudLabels != nil {
		allErrs = append(allErrs, validateIGCloudLabels(g, field.NewPath("spec", "cloudLabels"))...)
	}

	if cloud != nil {
		switch cloud.ProviderID() {
		case kops.CloudProviderAWS:
			allErrs = append(allErrs, awsValidateInstanceGroup(g, cloud.(awsup.AWSCloud))...)
		case kops.CloudProviderGCE:
			allErrs = append(allErrs, gceValidateInstanceGroup(g, cloud.(gce.GCECloud))...)
			//case kops.CloudProviderScaleway:
			//	allErrs = append(allErrs, scalewayValidateInstanceGroup(g, cloud.(scaleway.ScwCloud))...)
		}
	}

	for i, lb := range g.Spec.ExternalLoadBalancers {
		path := field.NewPath("spec", "externalLoadBalancers").Index(i)

		allErrs = append(allErrs, validateExternalLoadBalancer(&lb, path)...)
	}

	allErrs = append(allErrs, IsValidValue(field.NewPath("spec", "updatePolicy"), g.Spec.UpdatePolicy, []string{kops.UpdatePolicyAutomatic, kops.UpdatePolicyExternal})...)

	taintKeys := sets.NewString()
	for i, taint := range g.Spec.Taints {
		path := field.NewPath("spec", "taints").Index(i)
		_, err := util.ParseTaint(taint)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(path, taint, "invalid taint value"))
		}
		if taintKeys.Has(taint) {
			allErrs = append(allErrs, field.Duplicate(path, taint))
		} else {
			taintKeys.Insert(taint)
		}
	}

	return allErrs
}

// validateVolumeSpec is responsible for checking a volume spec is ok
func validateVolumeSpec(path *field.Path, v kops.VolumeSpec) field.ErrorList {
	allErrs := field.ErrorList{}

	if v.Device == "" {
		allErrs = append(allErrs, field.Required(path.Child("device"), "device name required"))
	}
	if v.Size <= 0 {
		allErrs = append(allErrs, field.Invalid(path.Child("size"), v.Size, "must be greater than zero"))
	}

	return allErrs
}

// validateVolumeMountSpec is responsible for checking the volume mount is ok
func validateVolumeMountSpec(path *field.Path, spec kops.VolumeMountSpec) field.ErrorList {
	allErrs := field.ErrorList{}

	if spec.Device == "" {
		allErrs = append(allErrs, field.Required(path.Child("device"), "device name required"))
	}
	if spec.Filesystem == "" {
		allErrs = append(allErrs, field.Required(path.Child("filesystem"), "filesystem type required"))
	}
	if spec.Path == "" {
		allErrs = append(allErrs, field.Required(path.Child("path"), "mount path required"))
	}
	allErrs = append(allErrs, IsValidValue(path.Child("filesystem"), &spec.Filesystem, kops.SupportedFilesystems)...)

	return allErrs
}

// CrossValidateInstanceGroup performs validation of the instance group, including that it is consistent with the Cluster
// It calls ValidateInstanceGroup, so all that validation is included.
func CrossValidateInstanceGroup(g *kops.InstanceGroup, cluster *kops.Cluster, cloud fi.Cloud, strict bool) field.ErrorList {
	allErrs := ValidateInstanceGroup(g, cloud, strict)

	if g.Spec.Role == kops.InstanceGroupRoleControlPlane {
		allErrs = append(allErrs, ValidateControlPlaneInstanceGroup(g, cluster)...)
	}

	if g.Spec.Role == kops.InstanceGroupRoleAPIServer {
		if cluster.Spec.GetCloudProvider() != kops.CloudProviderAWS {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "role"), "APIServer role only supported on AWS"))
		}
		if cluster.UsesNoneDNS() {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "role"), "APIServer cannot be used with topology.dns.type=None"))
		}
	}

	// Check that instance groups are defined in subnets that are defined in the cluster
	{
		clusterSubnets := make(map[string]*kops.ClusterSubnetSpec)
		for i := range cluster.Spec.Networking.Subnets {
			s := &cluster.Spec.Networking.Subnets[i]
			clusterSubnets[s.Name] = s
		}

		for i, z := range g.Spec.Subnets {
			if clusterSubnets[z] == nil {
				allErrs = append(allErrs, field.NotFound(field.NewPath("spec", "networking", "subnets").Index(i), z))
			}
		}
	}

	if cluster.Spec.GetCloudProvider() == kops.CloudProviderAWS {
		if g.Spec.RootVolume != nil && g.Spec.RootVolume.Type != nil {
			allErrs = append(allErrs, IsValidValue(field.NewPath("spec", "rootVolume", "type"), g.Spec.RootVolume.Type, []string{"standard", "gp3", "gp2", "io1", "io2"})...)
		}

		warmPool := cluster.Spec.CloudProvider.AWS.WarmPool.ResolveDefaults(g)
		if warmPool.MaxSize == nil || *warmPool.MaxSize != 0 {
			if g.Spec.Role != kops.InstanceGroupRoleNode && g.Spec.Role != kops.InstanceGroupRoleAPIServer {
				allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "warmPool"), "warm pool only allowed on instance groups with role Node or APIServer"))
			}
			if g.Spec.MixedInstancesPolicy != nil {
				allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "warmPool"), "warm pool cannot be combined with a mixed instances policy"))
			}
			if g.Spec.MaxPrice != nil {
				allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "warmPool"), "warm pool cannot be used with spot instances"))
			}
		}
		if warmPool.MaxSize != nil {
			if *warmPool.MaxSize < 0 {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "warmPool", "maxSize"), *warmPool.MaxSize, "warm pool maxSize cannot be negative"))
			} else if warmPool.MinSize > *warmPool.MaxSize {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "warmPool", "maxSize"), *warmPool.MaxSize, "warm pool maxSize cannot be set to lower than minSize"))
			}
		}
		if warmPool.MinSize < 0 {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "warmPool", "minSize"), warmPool.MinSize, "warm pool minSize cannot be negative"))
		}
	}

	if g.Spec.Containerd != nil {
		allErrs = append(allErrs, validateContainerdConfig(&cluster.Spec, g.Spec.Containerd, field.NewPath("spec", "containerd"), false)...)
	}

	return allErrs
}

func ValidateControlPlaneInstanceGroup(g *kops.InstanceGroup, cluster *kops.Cluster) field.ErrorList {
	allErrs := field.ErrorList{}
	for _, etcd := range cluster.Spec.EtcdClusters {
		hasEtcd := false
		for _, m := range etcd.Members {
			if fi.ValueOf(m.InstanceGroup) == g.ObjectMeta.Name {
				hasEtcd = true
				break
			}
		}
		if !hasEtcd {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "metadata", "name"), fmt.Sprintf("InstanceGroup \"%s\" with role ControlPlane must have a member in etcd cluster \"%s\"", g.ObjectMeta.Name, etcd.Name)))
		}
	}
	return allErrs
}

var validUserDataTypes = []string{
	"text/x-include-once-url",
	"text/x-include-url",
	"text/cloud-config-archive",
	"text/upstart-job",
	"text/cloud-config",
	"text/part-handler",
	"text/x-shellscript",
	"text/cloud-boothook",
}

func validateExtraUserData(userData *kops.UserData) field.ErrorList {
	allErrs := field.ErrorList{}
	fieldPath := field.NewPath("additionalUserData")

	if userData.Name == "" {
		allErrs = append(allErrs, field.Required(fieldPath.Child("name"), "field must be set"))
	}

	if userData.Content == "" {
		allErrs = append(allErrs, field.Required(fieldPath.Child("content"), "field must be set"))
	}

	allErrs = append(allErrs, IsValidValue(fieldPath.Child("type"), &userData.Type, validUserDataTypes)...)

	return allErrs
}

// validateInstanceProfile checks the String values for the AuthProfile
func validateInstanceProfile(v *kops.IAMProfileSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if v != nil && v.Profile != nil {
		instanceProfileARN := *v.Profile
		parsedARN, err := arn.Parse(instanceProfileARN)
		if err != nil || !strings.HasPrefix(parsedARN.Resource, "instance-profile/") {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("profile"), instanceProfileARN,
				"Instance Group IAM Instance Profile must be a valid aws arn such as arn:aws:iam::123456789012:instance-profile/KopsExampleRole"))
		}
	}
	return allErrs
}

func validateNodeLabels(labels map[string]string, fldPath *field.Path) (allErrs field.ErrorList) {
	for key := range labels {
		if strings.Count(key, "/") > 1 {
			allErrs = append(allErrs, field.Invalid(fldPath, key, "Node label may only contain a single slash"))
		}
	}
	return allErrs
}

func validateIGCloudLabels(ig *kops.InstanceGroup, fldPath *field.Path) (allErrs field.ErrorList) {
	labels := ig.Spec.CloudLabels
	if labels == nil {
		return allErrs
	}

	genericLabels := make(map[string]string)

	for key, value := range labels {
		if key == aws.CloudTagInstanceGroupName {
			if value != ig.ObjectMeta.Name {
				allErrs = append(allErrs, field.Invalid(fldPath.Child(aws.CloudTagInstanceGroupName), key, "value must match instance group name"))
			}
		} else {
			genericLabels[key] = value
		}
	}

	allErrs = append(allErrs, validateCloudLabels(genericLabels, fldPath)...)

	return allErrs
}

func validateExternalLoadBalancer(lb *kops.LoadBalancerSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if lb.LoadBalancerName != nil && lb.TargetGroupARN != nil {
		allErrs = append(allErrs, field.TooMany(fldPath, 2, 1))
	}

	if lb.LoadBalancerName != nil {
		name := fi.ValueOf(lb.LoadBalancerName)
		if len(name) > 32 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("loadBalancerName"), name,
				"Load Balancer name must have at most 32 characters"))
		}
	}

	if lb.TargetGroupARN != nil {
		actual := fi.ValueOf(lb.TargetGroupARN)

		parsed, err := arn.Parse(actual)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("targetGroupARN"), actual,
				fmt.Sprintf("Target Group ARN must be a valid AWS ARN: %v", err)))
			return allErrs
		}

		resource := strings.Split(parsed.Resource, "/")
		if len(resource) != 3 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("targetGroupARN"), actual,
				"Target Group ARN resource must be a valid AWS ARN resource such as \"targetgroup/tg-name/1234567890123456\""))
			return allErrs
		}

		kind := resource[0]
		if kind != "targetgroup" {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("targetGroupARN"), kind,
				"Target Group ARN resource type must be \"targetgroup\""))
		}

		name := resource[1]
		if len(name) > 32 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("targetGroupARN"), name,
				"Target Group ARN resource name must have at most 32 characters"))
		}
	}

	return allErrs
}
