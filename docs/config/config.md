# Configuration Resource

The PlatformService Gardener-IPAM is configured via a kubernetes resource of type `IPAMConfig`, which is explained in this document. The config is required and the controllers won't do anything as long as the config resource with the same name as the `PlatformService` resource is missing.

The `IPAMConfig` resource is cluster-scoped.

```yaml
apiVersion: gardener.clusters.openmcp.cloud/v1alpha1
kind: IPAMConfig
metadata:
  name: ipam
spec:
  internalStateRefreshCycleDuration: 24h # optional
  parentCIDRs:
    foo:
    - 10.27.0.0/16
    bar:
    - 10.28.0.0/24
  injectionRules:
  - id: mcp-clusters
    matchPurposes:
    - operator: ContainsAll
      values:
      - mcp
    injections:
    - path: .spec.network.cidr
      parents:
      - foo # refers to the 'foo' parent CIDR defined above
      - bar # refers to the 'bar' parent CIDR defined above
      subnetSize: 28 # requests a /28 CIDR
  - id: test
    matchLabels:
      clusters.openmcp.cloud/test: "true"
    matchPurposes:
    - operator: ContainsNone
      values:
      - mcp
      - onboarding
      - platform
      - workload
    injections:
    - id: test-cidr
      path: /spec/network/subnetA
      parents:
      - foo # refers to the 'foo' parent CIDR defined above
      subnetSize: 24
    - path: .spec.network.subnetB
      parents:
      - test-cidr # refers to the CIDR injected for the previous injection
      subnetSize: 30
```

## Understanding the Spec

The purpose of the Gardener-IPAM platform service is to inject disjunct CIDRs into shoot manifests created for `Cluster` resources that match certain criteria. The following fields in its `spec` can be used to control its behavior:

- `internalStateRefreshCycleDuration` *(optional)*: If set to a non-zero duration, the controller will discard its internal state (see below) and recover it from resources in the cluster (the same way it is recovered when the controller is started) after the specified time has elapsed. This can be used to mitigate potential bugs or other problems which cause the internal state to get out of sync with the cluster state. If not specified, no periodic state refreshing will happen.
- `parentCIDRs`: Defines a mapping from arbitrarily chosen identifiers to lists of parent CIDRs from which the disjunct child CIDRs will be sliced (traversing the list from top to bottom until a parent CIDR with enough capacity is found). The identifiers are used to refer to the individual parent CIDR lists in the injection rules, which allows different rules to use different sets of parent CIDRs.
- `injectionRules`: The actual definition of which size of CIDR to slice from which parent CIDR(s) and where to inject it in the shoot manifest. Note that multiple injection rules can be specified, while each individual rule can also inject multiple CIDRs - use multiple rules if you want to differentiate between multiple `Cluster` types (see '[Selectors](#selectors)' below) and multiple injections with in the same rule if you need to inject more than one CIDR into the shoot manifest.
  - `id`: Used to identify individual injection rules. Mainly for logging and debugging purposes, but this is also used as a label value and as part of a generated resource name, so it subject to the usual kubernetes naming conventions. Must be unique among all injection rules.
  - `matchIdentities`/`matchPurposes`/`matchLabels`/`matchExpressions`: These fields allow specifing filters for which `Cluster` resources this rule should apply to. They are explained in more detail in the [corresponding section](#selectors) below.
  - `injections`: A list of CIDRs to slice and inject into the shoot manifest.
    - `id` *(optional)*: An optional identifier for the injection. This is only needed if the CIDR generated for this injection should serve as a parent CIDR for a subsequent injection and has no effect otherwise.
    - `path`: The path within the shoot manifest where the CIDR should be injected. Supports JSONPatch (`/foo/bar/baz`) as well as a JSONPath-like (`.foo.bar.baz`) syntax, see [here](https://github.com/openmcp-project/controller-utils/blob/main/docs/libs/jsonpatch.md#patch-syntax) for further details.
    - `parents`: A list of parent CIDRs. The controller will traverse it from top to bottom and try to slice the requested child CIDR until it succeeds. In addition to the 'global' parent CIDRs from `spec.parentCIDRs`, also IDs from *previous injections of the same injection rule* can be used here, in which case the CIDR generated for the referenced injection will be used as a parent. If this list is empty, then all global parent CIDRs (but no local ones) are eligible and will be tried in an undefined order.
    - `subnetSize`: Defines the size of the requested CIDR. Note that this refers to the part after the `/` in the CIDR, meaning bigger values result in smaller IP ranges, with the maximum value of `32` resulting in a range consisting only of a single IP. Child CIDRs must be smaller than their parents, to take care to only reference parent CIDRs in the injection which have a smaller number at the end than defined here.

### Selectors

Selectors are used to define which clusters should be affected by a specified injection rule. Three different selectors can be combined:

- `matchIdentity` is the identity selector. It takes a list of `name` and `namespace` definitions which exactly specify the resources the rule applies to.
  - Note that the identity selector outrules every other selector - as soon as this is set, all other selectors are ignored and only this one is evaluated.
  - Also, an empty identity selector `{}` matches no `Cluster`, so the rule will never be applied to anything. Leave out the field or set it to `null` to disable the identity selector.
- `matchLabels` is the standard k8s label selector. It takes a mapping from label keys to values and only selects Clusters where exactly these labels are present.
- `matchExpressions` is also from the standard k8s label selector and allows more fine-granular filters than `matchLabels`. It takes a list of `key`, `operator` and `values` definitions.
  - Allowed operators are `In`, `NotIn`, `Exists`, and `DoesNotExist`. The former two require the `values` array to not be empty, while it must be empty for the latter two.
  - The requirements from the list are ANDed. If `matchLabels` is specified, it is merged with this by each entry resulting in a `In` definition with the specified label value as the only element of `values`.
- `matchPurposes` allows to filter for the purposes of the `Cluster` resources. Its design mimicks the `matchExpressions` one, except that it lacks the `key` field and the operators differ.
  - Allowed operators are `ContainsAll`, `ContainsAny`, `ContainsNone`, and `Equals`. `ContainsAll` requires all purposes from the `values` array to be in `spec.purposes` of the `Cluster`, for `ContainsAny` one of them is enough, and `ContainsNone` only matches if none of them is present. For `Equals`, `values` and `spec.purposes` have to be identical (ignoring order and duplicates).

Multiple selectors (except for `matchIdentity`) can be combined, in which case their requirements are ANDed. If `matchIdentity` is non-nil, all other selectors are ignored and only this one is evaluated.

If no selector is specified, all `Cluster` resources are affected by the rule.

See also [here](https://github.com/openmcp-project/openmcp-operator/blob/main/docs/libraries/selectors.md) for more information and examples regarding the selectors.

## Config Modifications

The platform service assumes CIDRs to be immutable. This means that it never attempts to modify a CIDR that it already has assigned to a shoot. As a result, updating the config might not behave as expected. The following paragraphs aim to explain how different changes to the config affect the system.

### Changing Parent CIDRs

New parent CIDRs can simply be added to the mapping without any problems. They will be used for new Clusters as usual.

Removing parent CIDRs is also possible. However, this does not lead to any child CIDRs being revoked or something similar, it just means that new `Cluster` resources will not get CIDRs from this parent assigned anymore. Existing shoots with child CIDRs from the removed parent(s) keep their CIDRs until the shoot is deleted.

### Changing Rule Selectors

If a rule's selector changes, all `Cluster` resources which were not matched previously but now are will immediately be affected by the rule. Clusters which were matched before and now are not anymore remain unchanged, the CIDRs remain blocked and no `ClusterConfig` is removed. 

This also applies if selectors are changed in a way that some Clusters which were previously matched by rule A are now matched by rule B, but not A anymore - existing CIDR assignments from A are kept, those from B will be added. New Clusters will only receive the ones from B.

Note that the controller only reconciles `Cluster` resources and manages `ClusterConfig` resources for Clusters which match at least one rule. If no rule is matched, the resources remain unchanged, but they will not be restored, if something is modified or deleted manually.

### Changing Injections

Each rule (identified by its `id`) is applied to each matching `Cluster` only once. This means that changes to a rule's injections will only take effect for new `Cluster` resources, existing ones won't be modified as they have already been affected by the rule before.

### Changing Rule IDs

This is dangerous because it can easily lead to conflicts. The controller stores information about which rule has been applied to which `Cluster` to avoid generating CIDRs for the same rule multiple times. If a rule's ID is changed, the controller considers it a new rule and will assign CIDRs to all matching Clusters again, which might clash with the previously assigned ones, unless the rule's selector and/or injections have simultaneously been modified to avoid such clashes.

