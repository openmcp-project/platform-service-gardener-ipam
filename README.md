# PlatformService Gardener-IPAM

## About this project

For environments without VPC support, the responsible Gardener extension usually requires `Shoot` manifests to have disjunct IP ranges in their infrastructure-specific configuration, not only within the manifest for a single shoot, but across all shoots that are created for the specific hyperscaler. The [ClusterProvider Gardener](https://github.com/openmcp-project/cluster-provider-gardener) currently uses a static template for shoots and does not contain any logic for assigning disjunct CIDRs.

This PlatformServices solves the problem by creating a `ClusterConfig` for each `Cluster` (that matches the selector) which injects an unused CIDR from one of multiple given parent networks into the shoot manifest.

## Requirements and Setup

Checkout the available tasks by running `task` in this folder. If you don't know about taskfiles, run `make help` to get further information.

This controller is meant to be run as a [platform service](https://github.com/openmcp-project/openmcp-operator/blob/main/docs/controller/deployment.md). The `task platformservice` command can be used to generate a `PlatformService` manifest based on the currently checked-out version.

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/SAP/<your-project>/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure
If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/SAP/<your-project>/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2026 SAP SE or an SAP affiliate company and platform-service-gardener-ipam contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/SAP/platform-service-gardener-ipam).
