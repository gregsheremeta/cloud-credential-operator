/*
Copyright 2019 The OpenShift Authors.

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

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	yaml "k8s.io/apimachinery/pkg/util/yaml"

	minterv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	"github.com/openshift/cloud-credential-operator/pkg/assets"
	"github.com/openshift/cloud-credential-operator/pkg/controller/utils"
)

const (
	podTemplate = `apiVersion: v1
kind: Pod
metadata:
  name: cloud-credential-operator
  namespace: openshift-cloud-credential-operator
spec:
  containers:
  - command:
    - /root/manager
    args:
    - --log-level=debug
    - --kubeconfig=/etc/kubernetes/secrets/kubeconfig
    image: %s
    imagePullPolicy: IfNotPresent
    name: manager
    volumeMounts:
    - mountPath: /etc/kubernetes/secrets
      name: secrets
      readOnly: true
  hostNetwork: true
  volumes:
  - hostPath:
      path: /etc/kubernetes/bootstrap-secrets
    name: secrets`
)

const (
	manifestsDir          = "manifests"
	bootstrapManifestsDir = "bootstrap-manifests"
)

var (
	renderAssets = []string{
		"config/crds/cloudcredential_v1_credentialsrequest.yaml",
		"config/manager/namespace.yaml",
	}

	renderCmd = &cobra.Command{
		Use:   "render",
		Short: "Cloud Credential Operator render manifests",
		Long:  "Have the Cloud Credential Operator render manifests for use as a bootstrap static pod",
		Run:   runRenderCmd,
	}

	renderOpts struct {
		destinationDir string
		ccoImage       string
		logLevel       string
	}
)

func newRenderCommand() *cobra.Command {
	renderCmd.Flags().StringVar(&renderOpts.destinationDir, "dest-dir", "", "The destination directory where CCO writes the manifests.")
	renderCmd.MarkFlagRequired("dest-dir")
	renderCmd.Flags().StringVar(&renderOpts.ccoImage, "cloud-credential-operator-image", "", "Image for Cloud Credential Operator.")
	renderCmd.MarkFlagRequired("cloud-credential-operator-image")
	renderCmd.Flags().StringVar(&renderOpts.logLevel, "log-level", defaultLogLevel, "Logging verbosity")

	return renderCmd
}

func runRenderCmd(cmd *cobra.Command, args []string) {
	// Set log level
	level, err := log.ParseLevel(renderOpts.logLevel)
	if err != nil {
		log.WithError(err).Fatal("Cannot parse log level")
	}
	log.SetLevel(level)
	log.Debug("debug logging enabled")

	operatorDisabled := isDisabled()

	log.Infof("Rendering files to %s", renderOpts.destinationDir)

	ccoRenderDir := renderOpts.destinationDir

	// render manifests
	if err := os.MkdirAll(filepath.Join(ccoRenderDir, manifestsDir), 0775); err != nil {
		log.WithError(err).Fatal("error creating manifests directory")
	}

	for _, assetName := range renderAssets {
		asset, err := assets.Asset(assetName)
		if err != nil {
			log.WithError(err).Fatal("failed to read static asset")
		}

		assetRenderPath := filepath.Join(ccoRenderDir, "manifests", "cco-"+filepath.Base(assetName))
		log.Infof("Writing file: %s", assetRenderPath)
		err = ioutil.WriteFile(assetRenderPath, asset, 0644)
		if err != nil {
			log.WithError(err).Fatal("failed to write file")
		}
	}

	if !operatorDisabled {
		log.Info("Rendering static pod")
		if err := os.Mkdir(filepath.Join(ccoRenderDir, bootstrapManifestsDir), 0775); err != nil {
			log.WithError(err).Fatal("error creating bootstrap-manifests directory")
		}
		podPath := filepath.Join(ccoRenderDir, bootstrapManifestsDir, "cloud-credential-operator-pod.yaml")
		podContent := fmt.Sprintf(podTemplate, renderOpts.ccoImage)
		log.Infof("writing file: %s", podPath)
		err = ioutil.WriteFile(podPath, []byte(podContent), 0644)
		if err != nil {
			log.WithError(err).Fatal("failed to write file")
		}
	} else {
		log.Info("CCO disabled, skipping static pod manifest.")
	}
}

// isDisabled will search through all the files in destinationDir (which also contains
// the source manifests) for a configmap indicating whether or not CCO is disabled. In
// the absence of any configmap, it will return the default disabled setting (which is
// that the operator is enabled by default) for the operator.
func isDisabled() bool {

	files, err := ioutil.ReadDir(renderOpts.destinationDir)
	if err != nil {
		log.WithError(err).Errorf("failed to list files in %s, using defualt operator settings", renderOpts.destinationDir)
		return utils.OperatorDisabledDefault
	}

	for _, fInfo := range files {
		// non-recursive checking of all files where the the source manifests are located
		if fInfo.IsDir() {
			continue
		}

		fullPath := filepath.Join(renderOpts.destinationDir, fInfo.Name())
		log.Debugf("checking file: %s", fullPath)
		file, err := os.Open(fullPath)
		if err != nil {
			log.WithError(err).Warn("failed to open file while searching for configmap")
			continue
		}
		decoder := yaml.NewYAMLOrJSONDecoder(file, 4096)
		configMap := &corev1.ConfigMap{}
		if err := decoder.Decode(configMap); err != nil {
			log.WithError(err).Debug("failed to decode into configmap")
			continue
		}

		if configMap.Namespace == minterv1.CloudCredOperatorNamespace && configMap.Name == minterv1.CloudCredOperatorConfigMap {
			logger := log.New()
			logger.SetLevel(log.GetLevel())
			disabled, err := utils.CCODisabledCheck(configMap, logger)
			if err != nil {
				return utils.OperatorDisabledDefault
			}

			return disabled
		}
	}

	return utils.OperatorDisabledDefault
}
