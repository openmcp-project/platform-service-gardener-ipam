# The Cluster Controller

This is the main controller of this platform service. It watches `Cluster` resources and creates `ClusterConfig` resources next to them to inject CIDRs into the resulting `Shoot` manifest, as specified by the injection rules in the [config](../config/config.md).

For a reconciled `Cluster`, it aligns the CIDR annotation with the existing `ClusterConfig` resources (see documentation about [state](state.md) for further information) and creates `ClusterConfig` resources according to the specified rules.

## Reconciliation Trigger Specifics

This controller has a few quirks when it comes to what triggers a reconciliation of a `Cluster` resource, which are quickly outlined here:
- *Creation* of `Cluster` resources trigger a reconciliation, as does their *deletion*.
- Adding the *reconcile annotation* (`openmcp.cloud/operation: reconcile`) to a `Cluster` triggers its deletion.
- *Deletion* or *update* of a `ClusterConfig` resource managed by this controller (identified via labels) triggers a reconciliation of its owning `Cluster`.
- Changes to the injection rules in the [`IPAMConfig`](../config/config.md) trigger a deletion for all `Cluster` resources which match the selectors of at least one injection rule (after they have been updated).
  - This also happens whenever the controller is started or the state is refreshed.

The following events do usually *not* trigger a reconciliation:
- *Update* of a `Cluster`.
  - The reasoning here is that `Cluster` resources might get updated frequently from other sources, which could lead to a lot of unnecessary load on this controller.
  - This means that CIDRs are not automatically applied to a `Cluster` which is modified to match an injection rule it did not match before.
    - This trade-off was deemed acceptable, as this should not happen very often and it can easily be mitigated by adding the reconciliation annotation to the `Cluster` (see above).
- A `Cluster` getting a deletion timestamp does not trigger a reconciliation.
  - We can only release the CIDRs after the `Cluster` has been fully deleted. This is noticed via the corresponding `ClusterConfig` resources, which get a deletion timestamp automatically at this point due to their owner reference pointing to the `Cluster`.
- *Updates* of a `ClusterConfig` resource do not trigger a reconciliation. Other controllers are strongly discouraged to modify the `ClusterConfig` resources managed by this one, so this should not happen (except if this controller updates a `ClusterConfig`, but we don't want a reconciliation in that case).
  - An exception is the *reconcile annotation* being added to an owned `ClusterConfig.
- `Cluster` resources which don't match any injection rule or have the *ignore annotation* (`openmcp.cloud/operation: ignore`) are never reconciled by this controller.
  - Similarly, `ClusterConfig` resources with an *ignore annotation* are ignored for triggering reconciliations.
