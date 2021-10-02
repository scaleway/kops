/*
Copyright The Kubernetes Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package v1alpha3

import (
	rest "k8s.io/client-go/rest"
	v1alpha3 "k8s.io/kops/pkg/apis/kops/v1alpha3"
	"k8s.io/kops/pkg/client/clientset_generated/internalclientset/scheme"
)

type KopsV1alpha3Interface interface {
	RESTClient() rest.Interface
	ClustersGetter
	InstanceGroupsGetter
	KeysetsGetter
	SSHCredentialsGetter
}

// KopsV1alpha3Client is used to interact with features provided by the kops.k8s.io group.
type KopsV1alpha3Client struct {
	restClient rest.Interface
}

func (c *KopsV1alpha3Client) Clusters(namespace string) ClusterInterface {
	return newClusters(c, namespace)
}

func (c *KopsV1alpha3Client) InstanceGroups(namespace string) InstanceGroupInterface {
	return newInstanceGroups(c, namespace)
}

func (c *KopsV1alpha3Client) Keysets(namespace string) KeysetInterface {
	return newKeysets(c, namespace)
}

func (c *KopsV1alpha3Client) SSHCredentials(namespace string) SSHCredentialInterface {
	return newSSHCredentials(c, namespace)
}

// NewForConfig creates a new KopsV1alpha3Client for the given config.
func NewForConfig(c *rest.Config) (*KopsV1alpha3Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}
	return &KopsV1alpha3Client{client}, nil
}

// NewForConfigOrDie creates a new KopsV1alpha3Client for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *KopsV1alpha3Client {
	client, err := NewForConfig(c)
	if err != nil {
		panic(err)
	}
	return client
}

// New creates a new KopsV1alpha3Client for the given RESTClient.
func New(c rest.Interface) *KopsV1alpha3Client {
	return &KopsV1alpha3Client{c}
}

func setConfigDefaults(config *rest.Config) error {
	gv := v1alpha3.SchemeGroupVersion
	config.GroupVersion = &gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	return nil
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *KopsV1alpha3Client) RESTClient() rest.Interface {
	if c == nil {
		return nil
	}
	return c.restClient
}
