module github.com/acidonpe/jump-app-operator

go 1.15

require (
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v0.0.0-20210416130433-86964261530c
	github.com/shirou/gopsutil v3.21.8+incompatible
	github.com/tklauser/go-sysconf v0.3.9 // indirect
	istio.io/api v0.0.0-20210914150558-75b5398bacb9
	istio.io/client-go v1.8.0
	k8s.io/api v0.21.0-rc.0
	k8s.io/apimachinery v0.21.0
	k8s.io/client-go v0.19.2
	sigs.k8s.io/controller-runtime v0.7.2
	knative.dev/serving v0.20.2
)

replace k8s.io/api => k8s.io/api v0.19.2
