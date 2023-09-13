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
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/scaleway/scaleway-sdk-go/scw"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/upup/pkg/fi"
)

// isHTTPCodeError returns true if err is an http error with code statusCode
func isHTTPCodeError(err error, statusCode int) bool {
	if err == nil {
		return false
	}

	responseError := &scw.ResponseError{}
	if errors.As(err, &responseError) && responseError.StatusCode == statusCode {
		return true
	}
	return false
}

// is404Error returns true if err is an HTTP 404 error
func is404Error(err error) bool {
	notFoundError := &scw.ResourceNotFoundError{}
	return isHTTPCodeError(err, http.StatusNotFound) || errors.As(err, &notFoundError)
}

func ParseZoneFromClusterSpec(clusterSpec kops.ClusterSpec) (scw.Zone, error) {
	zone := ""
	for _, subnet := range clusterSpec.Networking.Subnets {
		if zone == "" {
			zone = subnet.Zone
		} else if zone != subnet.Zone {
			return "", fmt.Errorf("scaleway currently only supports clusters in the same zone")
		}
	}
	return scw.Zone(zone), nil
}

func ParseRegionFromZone(zone scw.Zone) (region scw.Region, err error) {
	region, err = scw.ParseRegion(strings.TrimRight(string(zone), "-123"))
	if err != nil {
		return "", fmt.Errorf("could not determine region from zone %s: %w", zone, err)
	}
	return region, nil
}

func ClusterNameFromTags(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, TagClusterName) {
			return strings.TrimPrefix(tag, TagClusterName+"=")
		}
	}
	return ""
}

func InstanceGroupNameFromTags(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, TagInstanceGroup) {
			return strings.TrimPrefix(tag, TagInstanceGroup+"=")
		}
	}
	return ""
}

func InstanceRoleFromTags(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, TagNameRolePrefix) {
			return strings.TrimPrefix(tag, TagNameRolePrefix+"=")
		}
	}
	return ""
}

func getScalewayProfile() (*scw.Profile, error) {
	scwProfileName := os.Getenv("SCW_PROFILE")
	if scwProfileName == "" {
		return nil, nil
	}
	config, err := scw.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading Scaleway config file: %w", err)
	}
	profile, ok := config.Profiles[scwProfileName]
	if !ok {
		return nil, fmt.Errorf("could not find Scaleway profile %q", scwProfileName)
	}
	return profile, nil
}

func checkCredentials(accessKey, secretKey, projectID string) []error {
	errList := []error(nil)
	if accessKey == "" {
		errList = append(errList, fmt.Errorf("SCW_ACCESS_KEY has to be set"))
	}
	if secretKey == "" {
		errList = append(errList, fmt.Errorf("SCW_SECRET_KEY has to be set"))
	}
	if projectID == "" {
		errList = append(errList, fmt.Errorf("SCW_DEFAULT_PROJECT_ID has to be set"))
	}
	return errList
}

func CreateValidScalewayProfile() (*scw.Profile, error) {
	var profile scw.Profile

	// If the profile is REDACTED, we're running integration tests so no need to return any credentials
	if profileName := os.Getenv("SCW_PROFILE"); profileName == "REDACTED" {
		return &profile, nil
	}

	// We load the credentials form the profile first
	profileFromScwConfig, err := getScalewayProfile()
	if err != nil {
		return nil, err
	}
	// We load the credentials from the environment second
	var profileFromEnv scw.Profile
	if accessKey := os.Getenv("SCW_ACCESS_KEY"); accessKey != "" {
		profileFromEnv.AccessKey = &accessKey
	}
	if secretKey := os.Getenv("SCW_SECRET_KEY"); secretKey != "" {
		profileFromEnv.SecretKey = &secretKey
	}
	if projectID := os.Getenv("SCW_DEFAULT_PROJECT_ID"); projectID != "" {
		profileFromEnv.DefaultProjectID = &projectID
	}

	// We merge the profiles: the environment will override the values from the profile
	if profileFromScwConfig == nil {
		profile = profileFromEnv
	} else {
		profile = *scw.MergeProfiles(profileFromScwConfig, &profileFromEnv)
	}

	// We check that the profile has an access key, a secret key and a default project ID
	if errList := checkCredentials(fi.ValueOf(profile.AccessKey), fi.ValueOf(profile.SecretKey), fi.ValueOf(profile.DefaultProjectID)); errList != nil {
		errMsg := k8serrors.NewAggregate(errList).Error()
		if scwProfileName := os.Getenv("SCW_PROFILE"); scwProfileName != "" {
			errMsg += fmt.Sprintf(" in profile %q", scwProfileName)
		} else {
			errMsg += " in a Scaleway profile or as an environment variable"
		}
		return nil, fmt.Errorf(errMsg)
	}

	return &profile, nil
}

//func CreateScalewayClient(clientOptions ...scw.ClientOption) (*scw.Client, error) {
//	profile, err := CreateValidScalewayProfile()
//	if err != nil {
//		return nil, err
//	}
//	clientOptions = append(clientOptions, scw.WithProfile(profile))
//	clientOptions = append(clientOptions, scw.WithUserAgent(KopsUserAgentPrefix+kopsv.Version))
//
//	scwClient, err := scw.NewClient(clientOptions...)
//	if err != nil {
//		return nil, err
//	}
//	return scwClient, nil
//}

func displayEnv() {
	fmt.Printf("******************* Scaleway credentials *******************\n\n")

	fmt.Printf(fmt.Sprintf("SCW_ACCESS_KEY = %s\n", os.Getenv("SCW_ACCESS_KEY")))
	fmt.Printf(fmt.Sprintf("SCW_SECRET_KEY = %s\n", os.Getenv("SCW_SECRET_KEY")))
	fmt.Printf(fmt.Sprintf("SCW_DEFAULT_PROJECT_ID = %s\n", os.Getenv("SCW_DEFAULT_PROJECT_ID")))

	fmt.Printf("\n********************* S3 credentials *********************\n\n")

	fmt.Printf(fmt.Sprintf("S3_REGION = %s\n", os.Getenv("S3_REGION")))
	fmt.Printf(fmt.Sprintf("S3_ENDPOINT = %s\n", os.Getenv("S3_ENDPOINT")))
	fmt.Printf(fmt.Sprintf("S3_ACCESS_KEY_ID = %s\n", os.Getenv("S3_ACCESS_KEY_ID")))
	fmt.Printf(fmt.Sprintf("S3_SECRET_ACCESS_KEY = %s\n", os.Getenv("S3_SECRET_ACCESS_KEY")))

	fmt.Printf("\n\t*********** State-store bucket *************\n\n")

	fmt.Printf(fmt.Sprintf("KOPS_STATE_STORE = %s\n", os.Getenv("KOPS_STATE_STORE")))
	fmt.Printf(fmt.Sprintf("S3_BUCKET_NAME = %s\n", os.Getenv("S3_BUCKET_NAME")))

	fmt.Printf("\n\t*********** State-store bucket *************\n\n")

	fmt.Printf(fmt.Sprintf("NODEUP_BUCKET = %s\n", os.Getenv("NODEUP_BUCKET")))
	fmt.Printf(fmt.Sprintf("UPLOAD_DEST = %s\n", os.Getenv("UPLOAD_DEST")))
	fmt.Printf(fmt.Sprintf("KOPS_BASE_URL = %s\n", os.Getenv("KOPS_BASE_URL")))
	fmt.Printf(fmt.Sprintf("KOPSCONTROLLER_IMAGE = %s\n", os.Getenv("KOPSCONTROLLER_IMAGE")))
	fmt.Printf(fmt.Sprintf("DNSCONTROLLER_IMAGE = %s\n", os.Getenv("DNSCONTROLLER_IMAGE")))

	fmt.Printf("\n********************* Registry access *********************\n\n")

	fmt.Printf(fmt.Sprintf("DOCKER_REGISTRY = %s\n", os.Getenv("DOCKER_REGISTRY")))
	fmt.Printf(fmt.Sprintf("DOCKER_IMAGE_PREFIX = %s\n", os.Getenv("DOCKER_IMAGE_PREFIX")))

	fmt.Printf("\n********************* Other *********************\n\n")

	fmt.Printf(fmt.Sprintf("KOPS_FEATURE_FLAGS = %s\n", os.Getenv("KOPS_FEATURE_FLAGS")))
	fmt.Printf(fmt.Sprintf("KOPS_ARCH = %s\n", os.Getenv("KOPS_ARCH")))
	fmt.Printf(fmt.Sprintf("KOPS_VERSION = %s\n\n", os.Getenv("KOPS_VERSION")))
}
