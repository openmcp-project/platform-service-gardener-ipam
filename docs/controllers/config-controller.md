# The Config Controller

The config controller is fairly simple, as its job is only to detect changes to the [`IPAMConfig`](../config/config.md) resource and act accordingly. If any of the injection rules in the config changed, it triggers a reconciliation for all `Cluster` resources that match at least one of the injection rules (after the update), which is handled by the [cluster controller](./cluster-controller.md).

If the periodic state refresh feature is enabled (see `spec.internalStateRefreshCycleDuration` in the [config](../config/config.md)), this controller reconciles the config resource periodically and triggers a refresh of the internal state after the specified duration.
