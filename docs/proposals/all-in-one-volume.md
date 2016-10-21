## Abstract

Describes a proposal for a new volume type that can project secrets,
configmaps, and downward API items.

## Motivation

Users often need to build directories that contain multiple types of
configuration and secret data. For example, a configuration directory for some
software package may contain both config files and credentials. Currently, there
is no way to achieve this in Kubernetes without scripting inside of a container.

## Constraints and Assumptions

1.  The volume types must remain unchanged for backward compatability.
2.  There will be a new volume type for this proposed functionality, but no
    other API changes.
3.  The new volume type should support atomic updates in the event of an input
    change.

## Use Cases

1.  As a user, I want to automatically populate a single volume with the keys
    from multiple secrets, configmaps, and with downward API information, so
    that I can synthesize a single directory with various sources of
    information.
2.  As a user, I want to populate a single volume with the keys from multiple
    secrets, configmaps, and with downward API information, explicitly
    specifying paths for each item, so that I can have full control over the
    contents of that volume.

### Populating a single volume without pathing

A user should be able to map any combination of resources mentioned above into a
single directory. There are plenty of examples of software that needs to be
configured both with config files and secret data. The combination of having
that data not only accessible, but in the same location provides for an easier
user experience.

### Populating a single volume with pathing

Currently it is possible to define the path within a volume for specific
resources. Therefore the same is true for each resource contained within the
new single volume.

## Current State Overview

The only way of utilizing secrets, configmaps, and downward API currently is
to access the data using separate mount paths as shown in the volumeMounts
section below:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: container-test
    image: busybox
    volumeMounts:
    - name: mysecret
      mountPath: "/secrets"
      readOnly: true
    - name: podInfo
      mountPath: "/podinfo"
      readOnly: true
    - name: config-volume
      mountPath: "/config"
      readOnly: true
  volumes:
  - name: mysecret
    secret:
      secretName: jpeeler-db-secret
      items:
      - key: username
        path: my-group/my-username
  - name: podInfo
    downwardAPI:
      items:
        - path: "labels"
          fieldRef:
            fieldPath: metadata.labels
        - path: "annotations"
          fieldRef:
            fieldPath: metadata.annotations
  - name: config-volume
    configMap:
      name: special-config
      items:
      - key: special.how
        path: path/to/special-key
```

## Analysis

There are several combinations of resources that can be used at once, which 
all warrant consideration. The combinations are listed with one instance of
each resource, but real world usage will support multiple instances of a
specific resource too.

### ConfigMap + Secrets + Downward API

The user wishes to deploy containers with configuration data that includes
passwords. An application using these resources could be deploying OpenStack
on Kubernetes. The configuration data may need to be assembled differently
depending on if the services are going to be used for production or for
testing. If a pod is labeled with production or testing, the downward API
selector metadata.labels can be used to produce the correct OpenStack configs.

### ConfigMap + Secrets

Again, the user wishes to deploy containers with configuration data that
includes passwords. In this case with MariaDB running, the operator may wish
the container to have a ~/.my.cnf file that includes the username and password
for the database.

### ConfigMap + Downward API

In this case, the user wishes to generate a config including the pod’s name
(available via the metadata.name selector). This application may then pass the
pod name along with requests in order to easily determine the source without
using IP tracking.

### Secrets + Downward API

A user may wish to use a secret as a public key to encrypt the namespace of
the pod (available via the metadata.namespace selector). This example may be
the most contrived, but perhaps the operator wishes to use the application to
deliver the namespace information securely without using an encrypted
transport.

### Collisions between keys when all keys from a resource are projected

In the event of a user not explicitly defining keys for pathing within a volume,
there can not be an overlap of resources with the same paths on a given volume.
If a conflict does occur, a numeric prefix will be put in the name signifying
the order that the resources were specified in the pod spec. An example of this
resolution is as follows:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: container-test
    image: busybox
    volumeMounts:
    - name: all-in-one
      mountPath: "/system-volume"
      readOnly: true
  volumes:
  - name: all-in-one
    system:
      items:
      - secretName: mysecret
      - configMapName: myconfigmap
```

Here the assumption is that the data values configured for each resource is the
same. The contents of /system-volume may look like this:

/system-volume/1-very-generic
/system-volume/2-very-generic

### Collisions between keys when a configured path is identical

In the event the user specifies any keys with the same path, the pod spec will
not be accepted as valid. Note the specified path for mysecret and myconfigmap
are the same:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: container-test
    image: busybox
    volumeMounts:
    - name: all-in-one
      mountPath: "/system-volume"
      readOnly: true
  volumes:
  - name: all-in-one
    system:
      items:
      - secretName: mysecret
        items:
        - key: username
          path: my-group/data
      - configMapName: myconfigmap
        items:
        - key: config
          path: my-group/data
```

### Collisions when one path is explicit and the other is automatically projected

In the event that there is a collision due to a user specified path matching
data that is automatically projected, resolution will be done using the same
numeric prefix addition as described in the first scenario.

## Code changes

### Proposed API objects

```go
type SystemVolumeSource struct {
	Items []SystemVolumeProjection `json:"items,omitempty"`
}

type SystemVolumeProjection struct {
	SecretName    string `json:"secretName,omitempty"`
	ConfigMapName string `json:"configMapName,omitempty"`
	DownwardAPI   string `json:"downwardAPI,omitempty"`
}
```

### Additional required modifications

Add to VolumeSource struct:

```go
SystemVolume *SystemVolumeSource `json:"system,omitempty"`
```

The appropriate conversion code would need to be generated for v1 as well.

## Examples

### Sample pod spec with a secret, a downward API, and a configmap

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: container-test
    image: busybox
    volumeMounts:
    - name: all-in-one
      mountPath: "/system-volume"
      readOnly: true
  volumes:
  - name: all-in-one
    system:
      items:
      - secretName: mysecret
        items:
        - key: username
          path: my-group/my-username
      - downwardAPI:
        items:
        - path: "labels"
          fieldRef:
            fieldPath: metadata.labels
        - path: "cpu_limit"
          resourceFieldRef:
            containerName: container-test
            resource: limits.cpu
      - configMapName: myconfigmap
        items:
        - key: config
          path: my-group/my-config
```

### Sample pod spec with multiple secrets with a non-default permission mode set

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: container-test
    image: busybox
    volumeMounts:
    - name: all-in-one
      mountPath: "/system-volume"
      readOnly: true
  volumes:
  - name: all-in-one
    system:
      items:
      - secretName: mysecret
        items:
        - key: username
          path: my-group/my-username
        - key: password
          path: my-group/my-password
          mode: 511
```
