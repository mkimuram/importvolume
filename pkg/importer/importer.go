// SPDX-FileCopyrightText: 2021 importvolume authors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/mkimuram/importvolume/pkg/util"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	pvPrefix           = "pv-"
	tokenFsTypeKey     = "csi.storage.k8s.io/fsType"
	csiParameterPrefix = "csi.storage.k8s.io/"
)

type VolumeImporter struct {
	cs        kubernetes.Interface
	namespace string

	templatePath string
	importParams map[string]string

	pvc *v1.PersistentVolumeClaim
	sc  *storagev1.StorageClass

	pvName       string
	volumeHandle string
	capacity     resource.Quantity
	attributes   map[string]string
	readOnly     bool
	volumeMode   *v1.PersistentVolumeMode
	fsType       string

	controllerPublishSecret *v1.SecretReference
	nodeStageSecret         *v1.SecretReference
	nodePublishSecret       *v1.SecretReference
	controllerExpandSecret  *v1.SecretReference
}

func NewVolumeImporter(kubeconfig, ns, file string, importParams map[string]string, templatePath string) (*VolumeImporter, error) {
	v := &VolumeImporter{
		namespace:    ns,
		importParams: importParams,
		templatePath: templatePath,
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	v.cs, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	v.pvc, err = parsePVCfile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file %q: %v", file, err)
	}
	// Set namespace to pvc here for pvc.Namespace is referenced as pvc's namespace, later
	v.pvc.Namespace = v.namespace

	v.sc, err = v.cs.StorageV1().StorageClasses().Get(context.TODO(), *v.pvc.Spec.StorageClassName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get StorageClass %q: %v", *v.pvc.Spec.StorageClassName, err)
	}

	v.pvName = pvPrefix + v.pvc.Name

	// TODO: Capacity should be decided from both the pvc spec and the actual volume size
	v.capacity = v.pvc.Spec.Resources.Requests[v1.ResourceStorage]

	v.volumeHandle, err = v.genVolumeHandle()
	if err != nil {
		return nil, err
	}

	v.setSecret()

	// TODO: set readOnly properly
	v.readOnly = false
	v.volumeMode = v.pvc.Spec.VolumeMode
	// volumeMode should be set before calling genFsType()
	v.fsType = v.genFsType()
	v.attributes = v.genAttributes()

	return v, nil
}

func (v *VolumeImporter) setSecret() error {
	var err error

	v.controllerPublishSecret, err = util.GetSecret(v.sc.Parameters, util.ControllerPublishSecret, v.pvName, v.pvc)
	if err != nil {
		return err
	}

	v.nodeStageSecret, err = util.GetSecret(v.sc.Parameters, util.NodeStageSecret, v.pvName, v.pvc)
	if err != nil {
		return err
	}

	v.nodePublishSecret, err = util.GetSecret(v.sc.Parameters, util.NodePublishSecret, v.pvName, v.pvc)
	if err != nil {
		return err
	}

	v.controllerExpandSecret, err = util.GetSecret(v.sc.Parameters, util.ControllerExpandSecret, v.pvName, v.pvc)
	if err != nil {
		return err
	}

	return nil
}

func (v *VolumeImporter) genFsType() string {
	// fsType should be "" when volumeMode is PersistentVolumeBlock
	if v.volumeMode != nil && *v.volumeMode == v1.PersistentVolumeBlock {
		return ""
	}

	if fsType, ok := v.sc.Parameters[tokenFsTypeKey]; ok {
		return fsType
	}

	// TODO: Each CSI driver has it's own default?
	return ""
}

func (v *VolumeImporter) genAttributes() map[string]string {
	params := map[string]string{}

	// parameters prefixed with "csi.storage.k8s.io/" should be removed
	for key, value := range v.sc.Parameters {
		if !strings.HasPrefix(key, csiParameterPrefix) {
			params[key] = value
		}
	}

	// TODO: Each CSI driver has its specific attributes?
	// TODO: "storage.kubernetes.io/csiProvisionerIdentity" needs to be set here?
	return params
}

func (v *VolumeImporter) Import() error {
	if err := v.createPV(); err != nil {
		return err
	}
	if err := v.createPVC(); err != nil {
		return err
	}
	return nil
}

func parsePVCfile(file string) (*v1.PersistentVolumeClaim, error) {
	f, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode([]byte(f), nil, nil)
	if err != nil {
		return nil, err
	}

	switch o := obj.(type) {
	case *v1.PersistentVolumeClaim:
		return o, nil
	default:
		return nil, fmt.Errorf("resource type is not PersistentVolumeClaim: %s", file)
	}
}

func (v *VolumeImporter) genVolumeHandle() (string, error) {
	template, err := ioutil.ReadFile(path.Join(v.templatePath, v.sc.Provisioner))
	if err != nil {
		return "", err
	}

	missingParams := sets.NewString()
	volumeHandle := os.Expand(strings.TrimSuffix(string(template), "\n"), func(k string) string {
		val, ok := v.importParams[k]
		if !ok {
			missingParams.Insert(k)
		}
		return val
	})
	if missingParams.Len() > 0 {
		return "", fmt.Errorf("invalid tokens: %q", missingParams.List())
	}

	return volumeHandle, nil
}

func (v *VolumeImporter) createPV() error {
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: v.pvName,
			Annotations: map[string]string{
				"pv.kubernetes.io/provisioned-by": v.sc.Provisioner,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			StorageClassName: *v.pvc.Spec.StorageClassName,
			Capacity: v1.ResourceList{
				v1.ResourceStorage: v.capacity,
			},
			AccessModes: v.pvc.Spec.AccessModes,
			ClaimRef: &v1.ObjectReference{
				Kind:       "PersistentVolumeClaim",
				APIVersion: "v1",
				Namespace:  v.pvc.Namespace,
				Name:       v.pvc.Name,
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{
					Driver:                     v.sc.Provisioner,
					VolumeHandle:               v.volumeHandle,
					ControllerPublishSecretRef: v.controllerPublishSecret,
					NodeStageSecretRef:         v.nodeStageSecret,
					NodePublishSecretRef:       v.nodePublishSecret,
					ControllerExpandSecretRef:  v.controllerExpandSecret,
					VolumeAttributes:           v.attributes,
					FSType:                     v.fsType,
					ReadOnly:                   v.readOnly,
				},
			},
		},
	}

	_, err := v.cs.CoreV1().PersistentVolumes().Create(context.TODO(), pv, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (v *VolumeImporter) createPVC() error {
	_, err := v.cs.CoreV1().PersistentVolumeClaims(v.namespace).Create(context.TODO(), v.pvc, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}
