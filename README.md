# Jump App Operator

This repository includes a Kubernetes Operator for deploying _Jump App_ in an alternative way as Helm + ArgoCD. Regarding documentation, it includes a set of designing decisions and some documentation around operator management and installation.

## Operator Design

Fist of all, it is required to make some design decisions before starting develop a new operator. It is possible to get more information during the next sections but the summary is listed below:

- Operator Scope -> Cluster-scoped
- Define the API and Controller
    - Version: v1alpha1
    - Group: jumpapp
    - Name: App
    - CRD -> api/v1alpha1/app_types.go

### Operator Scope

- **Namespaces**: namespace-scoped operator watches and manages resources in a single Namespace, whereas a cluster-scoped operator watches and manages resources cluster-wide.

- **Roles and Permissions**: Define a new role (namespace-scoped) or cluster roles (cluster-scoped operator) are required to manage k8s objects from the regular service account. Take a look at the following files:
    - config/rbac/role_binding.yaml
    - config/rbac/service_account.yaml
    - config/rbac/leader_election_role.yaml
    - config/rbac/leader_election_role_binding.yaml

- **Watching**: Instead of having any Namespaces hard-coded in the main.go file a good practice is to use an environment variable to allow the restrictive configurations. More information visit - [Using ENV vars to configure operator scope to a set of namespaces](https://sdk.operatorframework.io/docs/building-operators/golang/operator-scope/#configuring-watch-namespaces-dynamically).

Please, follow next link for more information [operator-scope documentation](https://sdk.operatorframework.io/docs/building-operators/golang/operator-scope/).

NOTE: By default, operator-sdk init scaffolds a cluster-scoped operator. 

### API and Controller

Once you have your operator scope defined, it is time to create a new Custom Resource Definition(CRD) API with the a specific _group_, _version_ and _kind_.

#### CRD

Operator-sdk command generates a generic, or scaffolding, API definition which is required as starting point. Once it is created, it is necessary to add some elements to the spec CRD definition in order to be able to use the required values during the operator implementation process.

In this case, it was necessary to add the following data:

- Visit [AppSpec struct](./api/v1alpha1/app_types.go) for more information about the CRD
- CustomResource Example: [App example](./config/samples/jumpapp_v1alpha1_app.yaml)

## Commands

- Init a project with an API

```$bash
operator-sdk init --domain acidonpe.com --repo github.com/acidonpe/jump-app-operator
operator-sdk create api --group jumpapp --version v1alpha1 --kind App --resource --controller
```

- Reload objects

```$bash
make manifests
make generate
```

## Test locally

### Requirements

First of all, it is required to have the following prerequisites installed:

- oc client
- An OCP cluster or Kubernetes cluster up and running

### Process

- Start operator

```$bash
oc login 
make install run
```

- Create a specific resource in order to start working

```$bash
# Regular App
oc apply -f config/samples/jumpapp_v1alpha1_app.yaml

# Service Mesh App
## IMPORTANT - It is required to install Service Mesh Operators and add the respective Namespace as ServiceMeshMember
oc apply -f config/samples/jumpapp_v1alpha1_app_mesh.yaml

# Knative App
## IMPORTANT - It is required to install Serverless Operator
oc apply -f config/samples/jumpapp_v1alpha1_app_knative.yaml

# Test the application
oc get app -o yaml
oc get all
```

- Clean up

```$bash
# Delete App
oc delete app app-sample
```
