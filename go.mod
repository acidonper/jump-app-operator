module github.com/acidonpe/jump-app-operator

go 1.15

require (
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v0.0.0-20210416130433-86964261530c
	istio.io/api v0.0.0-20210910210758-d6ce87e3e159
	k8s.io/api v0.21.0-rc.0
	k8s.io/apimachinery v0.21.0
	k8s.io/client-go v0.19.2
	sigs.k8s.io/controller-runtime v0.7.2
)

replace k8s.io/api => k8s.io/api v0.19.2
