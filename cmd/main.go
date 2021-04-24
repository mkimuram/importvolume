// SPDX-FileCopyrightText: 2021 importvolume authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/mkimuram/importvolume/pkg/importer"
	flag "github.com/spf13/pflag"
	utilflag "k8s.io/component-base/cli/flag"
)

var (
	kubeconfig = flag.String("kubeconfig", "", "Absolute path to the kubeconfig file.")
	namespace  = flag.StringP("namespace", "n", "default", "Namespace to create PersistentVolumeClaim.")
	file       = flag.StringP("filename", "f", "", "Filename that contains PersistentVolumeClaim definition.")

	importParams map[string]string
	templatePath = "./config"
)

func init() {
	flag.VarP(utilflag.NewMapStringString(&importParams), "parameters", "p", "Parameters to specify the volume to be imported.")

	flag.Parse()
	// get the KUBECONFIG from env if specified
	kubeconfigEnv := os.Getenv("KUBECONFIG")
	if kubeconfigEnv != "" && *kubeconfig == "" {
		*kubeconfig = kubeconfigEnv
	}

	if *kubeconfig == "" {
		fmt.Fprintf(os.Stderr, "kubeconfig must be provide with -f option or KUBECONFIG environment variable\n")
		os.Exit(1)
	}

	if *file == "" {
		fmt.Fprintf(os.Stderr, "file must be provide with -f option\n")
		os.Exit(1)
	}

	templatePathEnv := os.Getenv("IMPORT_VOLUME_TEMPLATE")
	if templatePathEnv != "" {
		templatePath = templatePathEnv
	}

}

func main() {
	v, err := importer.NewVolumeImporter(*kubeconfig, *namespace, *file, importParams, templatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start importer: %v\n", err)
		os.Exit(1)
	}

	err = v.Import()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to import %q: %v\n", file, err)
		os.Exit(1)
	}
}
