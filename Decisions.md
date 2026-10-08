# Decisions

The `namespaceclass` resource is cluster wide resource, which means only cluster admin can
manage the `namespaceclass` resources. Each `namespace` that refers to a `namespaceclass` that 
exists in the cluster will have it's resource managed by the `namespaceclass`.

The CRD allows a list of resources to be provisioned. Currently only config maps and network
policies are allow, but code is written in a way that it should be easy to allow other resource
types are needed. The RBAC rules and inline validation rules are responsible for guarding against 
what resources can be provisioned. This was a tradeoff made against using typed config for the
purpose of extensibility.

There are two controllers under the same manager. Once watches `namespaces` for any changes that
could upsert or delete the reference to `namespaceclass` resource. The other one watches the
`namespaceclass` resources itself, to perform cleanup in cases the resource is modified/deleted.
