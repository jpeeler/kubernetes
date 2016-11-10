/*
Copyright 2015 The Kubernetes Authors.

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

package system

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/fieldpath"
	"k8s.io/kubernetes/pkg/types"
	utilerrors "k8s.io/kubernetes/pkg/util/errors"
	utilstrings "k8s.io/kubernetes/pkg/util/strings"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/configmap"
	"k8s.io/kubernetes/pkg/volume/secret"
	volumeutil "k8s.io/kubernetes/pkg/volume/util"
)

// ProbeVolumePlugin is the entry point for plugin detection in a package.
func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&systemPlugin{}}
}

const (
	systemPluginName = "kubernetes.io/system"
)

type systemPlugin struct {
	host volume.VolumeHost
}

var _ volume.VolumePlugin = &systemPlugin{}

func wrappedVolumeSpec() volume.Spec {
	return volume.Spec{
		Volume: &api.Volume{
			VolumeSource: api.VolumeSource{
				EmptyDir: &api.EmptyDirVolumeSource{Medium: api.StorageMediumMemory},
			},
		},
	}
}

func getPath(uid types.UID, volName string, host volume.VolumeHost) string {
	return host.GetPodVolumeDir(uid, utilstrings.EscapeQualifiedNameForDisk(systemPluginName), volName)
}

func (plugin *systemPlugin) Init(host volume.VolumeHost) error {
	plugin.host = host
	return nil
}

func (plugin *systemPlugin) GetPluginName() string {
	return systemPluginName
}

func (plugin *systemPlugin) GetVolumeName(spec *volume.Spec) (string, error) {
	_, _, err := getVolumeSource(spec)
	if err != nil {
		return "", err
	}

	return spec.Name(), nil
}

func (plugin *systemPlugin) CanSupport(spec *volume.Spec) bool {
	return spec.Volume != nil && spec.Volume.SystemProjection != nil
}

func (plugin *systemPlugin) RequiresRemount() bool {
	return true
}

func (plugin *systemPlugin) NewMounter(spec *volume.Spec, pod *api.Pod, opts volume.VolumeOptions) (volume.Mounter, error) {
	return &systemVolumeMounter{
		systemVolume: &systemVolume{
			volName: spec.Name(),
			sources: spec.Volume.SystemProjection.Sources,
			podUID:  pod.UID,
			plugin:  plugin,
		},
		source: *spec.Volume.SystemProjection,
		pod:    pod,
		opts:   &opts,
	}, nil
}

func (plugin *systemPlugin) NewUnmounter(volName string, podUID types.UID) (volume.Unmounter, error) {
	return &systemVolumeUnmounter{
		&systemVolume{
			volName: volName,
			podUID:  podUID,
			plugin:  plugin,
		},
	}, nil
}

func (plugin *systemPlugin) ConstructVolumeSpec(volumeName, mountPath string) (*volume.Spec, error) {
	systemVolume := &api.Volume{
		Name: volumeName,
		VolumeSource: api.VolumeSource{
			SystemProjection: &api.SystemProjections{},
		},
	}

	return volume.NewSpecFromVolume(systemVolume), nil
}

type systemVolume struct {
	volName string
	sources []api.SystemVolumeProjection
	podUID  types.UID
	plugin  *systemPlugin
	//	mounter           mount.Interface
	//	writer            ioutil.Writer
	volume.MetricsNil
}

var _ volume.Volume = &systemVolume{}

func (sv *systemVolume) GetPath() string {
	return getPath(sv.podUID, sv.volName, sv.plugin.host)
}

type systemVolumeMounter struct {
	*systemVolume

	source api.SystemProjections
	pod    *api.Pod
	opts   *volume.VolumeOptions
}

var _ volume.Mounter = &systemVolumeMounter{}

func (sv *systemVolume) GetAttributes() volume.Attributes {
	return volume.Attributes{
		ReadOnly:        true,
		Managed:         true,
		SupportsSELinux: true,
	}

}

// Checks prior to mount operations to verify that the required components (binaries, etc.)
// to mount the volume are available on the underlying node.
// If not, it returns an error
func (s *systemVolumeMounter) CanMount() error {
	return nil
}

func (s *systemVolumeMounter) SetUp(fsGroup *int64) error {
	return s.SetUpAt(s.GetPath(), fsGroup)
}

func (s *systemVolumeMounter) SetUpAt(dir string, fsGroup *int64) error {
	glog.V(3).Infof("Setting up volume %v for pod %v at %v", s.volName, s.pod.UID, dir)

	wrapped, err := s.plugin.host.NewWrapperMounter(s.volName, wrappedVolumeSpec(), s.pod, *s.opts)
	if err != nil {
		return err
	}
	if err := wrapped.SetUpAt(dir, fsGroup); err != nil {
		return err
	}

	data, err := s.collectData(s.source.DefaultMode)
	if err != nil {
		glog.Errorf("Error preparing data for system volume %v for pod %v/%v: %s", s.volName, s.pod.Namespace, s.pod.Name, err.Error())
	}

	writerContext := fmt.Sprintf("pod %v/%v volume %v", s.pod.Namespace, s.pod.Name, s.volName)
	writer, err := volumeutil.NewAtomicWriter(dir, writerContext)
	if err != nil {
		glog.Errorf("Error creating atomic writer: %v", err)
		return err
	}

	err = writer.Write(data)
	if err != nil {
		glog.Errorf("Error writing paylot to dir: %v", err)
		return err
	}

	err = volume.SetVolumeOwnership(s, fsGroup)
	if err != nil {
		glog.Errorf("Error applying volume ownership settings for group: %v", fsGroup)
		return err
	}

	return nil
}

func (s *systemVolumeMounter) collectData(defaultMode *int32) (map[string]volumeutil.FileProjection, error) {
	if defaultMode == nil {
		return nil, fmt.Errorf("No defaultMode used, not even the default value for it")
	}

	kubeClient := s.plugin.host.GetKubeClient()
	if kubeClient == nil {
		return nil, fmt.Errorf("Cannot setup system volume %v because kube client is not configured", s.volName)
	}

	errlist := []error{}
	payload := make(map[string]volumeutil.FileProjection)
	for _, source := range s.source.Sources {
		if source.Secret != nil {
			// JPEELER: fix this to Secret.Name
			secretapi, err := kubeClient.Core().Secrets(s.pod.Namespace).Get(source.Secret.SecretName)
			if err != nil {
				glog.Errorf("Couldn't get secret %v/%v", s.pod.Namespace, source.Secret.SecretName)
				errlist = append(errlist, err)
			}

			secretPayload, err := secret.MakePayload(source.Secret.Items, secretapi, defaultMode)
			if err != nil {
				glog.Errorf("Couldn't get secret %v/%v: %v", s.pod.Namespace, source.Secret.SecretName, err)
				errlist = append(errlist, err)
				continue
			}

			for k, v := range secretPayload {
				payload[k] = v
			}
		} else if source.ConfigMap != nil {
			configMap, err := kubeClient.Core().ConfigMaps(s.pod.Namespace).Get(source.ConfigMap.Name)
			if err != nil {
				glog.Errorf("Couldn't get configMap %v/%v: %v", s.pod.Namespace, source.ConfigMap.Name, err)
				errlist = append(errlist, err)
				continue
			}

			configMapPayload, err := configmap.MakePayload(source.ConfigMap.Items, configMap, defaultMode)
			if err != nil {
				errlist = append(errlist, err)
				continue
			}
			for k, v := range configMapPayload {
				payload[k] = v
			}
			// uses Items.DownwardAPIVolumeFile
		} else if source.DownwardAPI != nil {
			for _, fileInfo := range source.DownwardAPI.Items {
				var fileProjection volumeutil.FileProjection
				fPath := path.Clean(fileInfo.Path)
				if fileInfo.Mode != nil {
					fileProjection.Mode = *fileInfo.Mode
				} else {
					fileProjection.Mode = *defaultMode
				}
				if fileInfo.FieldRef != nil {
					// TODO: unify with Kubelet.podFieldSelectorRuntimeValue
					if values, err := fieldpath.ExtractFieldPathAsString(s.pod, fileInfo.FieldRef.FieldPath); err != nil {
						glog.Errorf("Unable to extract field %s: %s", fileInfo.FieldRef.FieldPath, err.Error())
						errlist = append(errlist, err)
					} else {
						fileProjection.Data = []byte(sortLines(values))
					}
				} else if fileInfo.ResourceFieldRef != nil {
					containerName := fileInfo.ResourceFieldRef.ContainerName
					nodeAllocatable, err := s.plugin.host.GetNodeAllocatable()
					if err != nil {
						errlist = append(errlist, err)
					} else if values, err := fieldpath.ExtractResourceValueByContainerNameAndNodeAllocatable(fileInfo.ResourceFieldRef, s.pod, containerName, nodeAllocatable); err != nil {
						glog.Errorf("Unable to extract field %s: %s", fileInfo.ResourceFieldRef.Resource, err.Error())
						errlist = append(errlist, err)
					} else {
						fileProjection.Data = []byte(sortLines(values))
					}
				}

				payload[fPath] = fileProjection
			}
		}
	}
	return payload, utilerrors.NewAggregate(errlist)
}

func sortLines(values string) string {
	splitted := strings.Split(values, "\n")
	sort.Strings(splitted)
	return strings.Join(splitted, "\n")
}

type systemVolumeUnmounter struct {
	*systemVolume
}

var _ volume.Unmounter = &systemVolumeUnmounter{}

func (c *systemVolumeUnmounter) TearDown() error {
	return c.TearDownAt(c.GetPath())
}

func (c *systemVolumeUnmounter) TearDownAt(dir string) error {
	glog.V(3).Info("Tearing down volume %v for pod %v at %v", c.volName, c.podUID, dir)

	wrapped, err := c.plugin.host.NewWrapperUnmounter(c.volName, wrappedVolumeSpec(), c.podUID)
	if err != nil {
		return err
	}
	return wrapped.TearDownAt(dir)
}

func getVolumeSource(spec *volume.Spec) (*api.SystemProjections, bool, error) {
	var readOnly bool
	var volumeSource *api.SystemProjections

	if spec.Volume != nil && spec.Volume.SystemProjection != nil {
		volumeSource = spec.Volume.SystemProjection
		readOnly = spec.ReadOnly
	}

	return volumeSource, readOnly, fmt.Errorf("Spec does not reference a System volume type")
}
