// SPDX-FileCopyrightText: 2021 importvolume authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"os"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
)

type secretType string

const (
	ProvisionerSecret       secretType = "ProvisionerSecret"
	ControllerPublishSecret secretType = "ControllerPublishSecret"
	NodeStageSecret         secretType = "NodeStageSecret"
	NodePublishSecret       secretType = "NodePublishSecret"
	ControllerExpandSecret  secretType = "ControllerExpandSecret"

	tokenPVNameKey       = "pv.name"
	tokenPVCNameKey      = "pvc.name"
	tokenPVCNamespaceKey = "pvc.namespace"
)

var (
	nsKey = map[secretType]string{
		ProvisionerSecret:       "csi.storage.k8s.io/provisioner-secret-namespace",
		ControllerPublishSecret: "csi.storage.k8s.io/controller-publish-secret-namespace",
		NodeStageSecret:         "csi.storage.k8s.io/node-stage-secret-namespace",
		NodePublishSecret:       "csi.storage.k8s.io/node-publish-secret-namespace",
		ControllerExpandSecret:  "csi.storage.k8s.io/controller-expand-secret-namespace",
	}
	deprecatedNsKey = map[secretType]string{
		ProvisionerSecret:       "provisioner-secret-namespace",
		ControllerPublishSecret: "controller-publish-secret-namespace",
		NodeStageSecret:         "node-stage-secret-namespace",
		NodePublishSecret:       "node-publish-secret-namespace",
		ControllerExpandSecret:  "controller-expand-secret-namespace",
	}
	nameKey = map[secretType]string{
		ProvisionerSecret:       "csi.storage.k8s.io/provisioner-secret-name",
		ControllerPublishSecret: "csi.storage.k8s.io/controller-publish-secret-name",
		NodeStageSecret:         "csi.storage.k8s.io/node-stage-secret-name",
		NodePublishSecret:       "csi.storage.k8s.io/node-publish-secret-name",
		ControllerExpandSecret:  "csi.storage.k8s.io/controller-expand-secret-name",
	}
	deprecatedNameKey = map[secretType]string{
		ProvisionerSecret:       "provisioner-secret-name",
		ControllerPublishSecret: "controller-publish-secret-name",
		NodeStageSecret:         "node-stage-secret-name",
		NodePublishSecret:       "node-publish-secret-name",
		ControllerExpandSecret:  "controller-expand-secret-name",
	}
)

func GetSecret(scParams map[string]string, sType secretType, pvName string, pvc *v1.PersistentVolumeClaim) (*v1.SecretReference, error) {
	nsTempl, nameTempl, err := getSecretTemplate(scParams, sType)
	if err != nil {
		return nil, err
	}
	if nsTempl == "" || nameTempl == "" {
		return nil, nil
	}

	ns, err := resolveTemplate(nsTempl, pvName, pvc, false /* isName */)
	if err != nil {
		return nil, err
	}

	name, err := resolveTemplate(nameTempl, pvName, pvc, true /* isName */)
	if err != nil {
		return nil, err
	}

	return &v1.SecretReference{Namespace: ns, Name: name}, nil
}

func getSecretTemplate(scParams map[string]string, sType secretType) (string, string, error) {
	var nsTempl, nameTempl string
	var nsOK, nameOK bool

	if nsTempl, nsOK = scParams[nsKey[sType]]; !nsOK {
		nsTempl, nsOK = scParams[deprecatedNsKey[sType]]
	}

	if nameTempl, nameOK = scParams[nameKey[sType]]; !nameOK {
		nameTempl, nameOK = scParams[deprecatedNameKey[sType]]
	}

	if nsOK != nameOK {
		return "", "", fmt.Errorf("only namespace or name is found for secret, namespace %q, name %q: %v", nsTempl, nameTempl, scParams)
	} else if !nsOK && !nameOK {
		// Not defined in parameters
		return "", "", nil
	}

	return nsTempl, nameTempl, nil
}

func resolveTemplate(template string, pvName string, pvc *v1.PersistentVolumeClaim, isName bool) (string, error) {
	params := map[string]string{
		tokenPVNameKey:       pvName,
		tokenPVCNamespaceKey: pvc.Namespace,
	}

	if isName {
		params[tokenPVCNameKey] = pvc.Name
		for k, v := range pvc.Annotations {
			params["pvc.annotations['"+k+"']"] = v
		}
	}

	missingParams := sets.NewString()
	resolved := os.Expand(template, func(k string) string {
		v, ok := params[k]
		if !ok {
			missingParams.Insert(k)
		}
		return v
	})
	if missingParams.Len() > 0 {
		return "", fmt.Errorf("invalid tokens: %q", missingParams.List())
	}
	if len(validation.IsDNS1123Subdomain(resolved)) > 0 {
		return "", fmt.Errorf("%q is resolved to %q, but is not a valid dns name", template, resolved)
	}

	return resolved, nil
}
