# State

The platform service Gardener-IPAM needs to remember which CIDRs are currently assigned to a `Cluster`. This document shortly explains how this information is stored.

## The Internal State

The [go-ipam](https://github.com/metal-stack/go-ipam) library is used internally to handle the CIDR slicing. This library maintains a state itself, which is further labeled the *internal state*. While the library offers possibilities to persist its state, e.g. on a filesystem or in a database, the controller does not make use of this feature. The internal state is kept in memory only.

This state is reconstructed from the cluster state when the controller is started. It is also discarded and reconcstructed from the cluster state whenever a configured periodic state refreshing (see `spec.internalStateRefreshCycleDuration` in the [config](../config/config.md)) happens.

## The Cluster State

In order to keep the controller state-less and prevent it from requiring a persisted volume, it also stores the information about assigned CIDRs in k8s resources. This is considered to be the ground truth and whenever the controller is restarted or refreshes its state for other reasons, the information from the in-cluster resources is used to reconstruct the internal state for the library.

### ClusterConfig Resources

The [cluster controller](./cluster-controller.md) creates `ClusterConfig` resources in order to inject the generated CIDRs into the `Shoot` manifests belonging to the `Cluster` resources. This means that these `ClusterConfig`s by default contain the information which CIDRs have been assigned to which `Cluster`. 

`ClusterConfig` resources that are managed by the cluster controller contain a specific set of labels which helps the controller to identify them:
```yaml
labels:
  openmcp.cloud/managed-by: <provider name> # name of the PlatformService resource that created this controller instance
  openmcp.cloud/managed-purpose: gardener-ipam
  openmcp.cloud/environment: <environment name> # the environment passed into the controller as an argument
  ipam.gardener.clusters.openmcp.cloud/cluster: <cluster name> # name of the Cluster resource this ClusterConfig belongs to
  ipam.gardener.clusters.openmcp.cloud/injection-rule: <injection rule id> # id of the injection rule that this ClusterConfig is for
```

Each injection rule from the [config](../config/config.md) whose selectors match a `Cluster` results in one `ClusterConfig` resource for that `Cluster`.

Look [here](../config/references.md) for information about the naming of the generated `ClusterConfig` resources.

### Applied Rules Annotation

In order to recover from accidentally modified or deleted `ClusterConfig` resources, the information is duplicated into an annotation on the affected `Cluster`.
This annotation uses the key `ipam.gardener.clusters.openmcp.cloud/applied-injection-rules`. Its value is a JSON representation of a multi-dimensional mapping: IDs of injection rules are mapped to mappings from paths (within the `Shoot` manifest, where the CIDRs have been injected) to the correponding CIDRs.

```yaml
annotations:
  ipam.gardener.clusters.openmcp.cloud/applied-injection-rules: TODO add example here
```

## Inconsistent or Missing State

If some state information - internal, ClusterConfigs, or annotation - is missing (e.g. in case of the internal state because the controller just started), it is recovered as described below. Since CIDRs are considered immutable and can neither be modified nor removed once injected, the logic behind this is fairly easy.

- Injections described in ClusterConfigs which are not reflected in the applied rules annotation are added to the annotation. The rule's name can be inferred from the `ipam.gardener.clusters.openmcp.cloud/injection-rule` label, the CIDRs and the respective paths they have been injected into are retrieved from the JSONPatch statements in the `ClusterConfig.
- Injections described by the applied rules annotation which are not reflected in the existing `ClusterConfig` resources are added, either by updating existing `ClusterConfig`s or by creating new ones. The annotation contains the injection rule ID, path, and CIDR for every injection, which is enough information to generate the `ClusterConfig` resources.
- The internal state is never used to restore any other state information, as it contains only the assigned CIDRs, but not the information where these CIDRs have been injected into or which injection rule they come from.
  - In addition to the generated CIDRs, the internal state also has entries for the parent CIDRs specified in the [config](../config/config.md).
