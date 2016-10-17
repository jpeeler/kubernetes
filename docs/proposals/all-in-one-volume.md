## Abstract

A proposal for enabling users to create a volume populated from secrets,
configmaps, and downward API.

## Motivation

This new feature will allow the user a method of combining data from different
sources within the same volume, which is not something that can be done
currently.

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

A user should be able to map any combination of resources mentioned above into a
single directory. The same available options for specifying the location within
a volume for each resource is available with the new single volume as well.

## Current State Overview

The only way of utilizing secrets, configmaps, and downward API currently is
to access the data using separate pathing as shown in the volumeMounts section
below:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: container-test
    image: jpeeler/scratch
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

There are several combinations of resources that can be used at once. All of
which need to be considered:

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

### Resources configured with the same path

There can not be an overlap of resources with the same paths on a given volume.
If a conflict does occur, a numeric prefix will be put in the path signifying
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
    image: jpeeler/scratch
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

Note the specified path for mysecret and myconfigmap are the same. The contents
of /system-volume could be:

/system-volume/my-group/data/1/some-secret-data!
/system-volume/my-group/data/2/configmap-data

The data values would depend upon the values configured by the user.

## Proposed Design

The new proposed method of utilizing secrets, configmaps, and downward API is 
more succinct, while also allowing the data to be populated in the same volume.
An example is demonstrated below:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: container-test
    image: jpeeler/scratch
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

### Proposed API objects

### Code changes
