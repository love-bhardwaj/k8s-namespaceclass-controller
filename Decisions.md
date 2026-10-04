# Decisions

The NamespaceClass resource is cluster wide resource as to allow multiple Namespaces
to refer to it. Each Namespace that refers to a NamespaceClass that exists in the cluster
will have it's resource managed by the NamespaceClass.

The spec allows a list of resources to be provided. The resource provided should be a valid
k8s object resource with metadata.name as a requirement, as that us used by NamespaceClass
to identify the resource, as such it requires to be stable and not generated dynamically.
Having a dynamic name would mean every reconcile creates a new resource.

