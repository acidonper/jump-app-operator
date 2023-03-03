/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"istio.io/client-go/pkg/apis/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	jumpappv1alpha1 "github.com/acidonpe/jump-app-operator/api/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	v1alpha3Spec "istio.io/api/networking/v1alpha3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"
)

// AppReconciler reconciles a App object
type AppReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=jumpapp.acidonpe.com,resources=apps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=jumpapp.acidonpe.com,resources=apps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=jumpapp.acidonpe.com,resources=apps/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="networking.istio.io",resources=destinationrule;virtualservice;gateway,verbs=get;list;watch;create;update;patch;delete

func (r *AppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Define logs and required variables
	log := r.Log.WithValues("app", req.NamespacedName)
	podsStatus := []string{}
	svcsStatus := []string{}
	routesStatus := []string{}
	meshMicrosStatus := []string{}
	knativeMicrosStatus := []string{}
	istioNS := "istio-system"

	// Get Apps
	app := &jumpappv1alpha1.App{}
	err := r.Get(ctx, req.NamespacedName, app)
	if err != nil {
		if errors.IsNotFound(err) {
			// Delete App Components when App will be deleted
			log.V(0).Info("Deleting App Components" + app.Name)
			c, e := r.destroyJumpApp(ctx, app.Namespace, log)
			return c, e
		} else {
			return ctrl.Result{}, err
		}
	}

	// Get OCP Domain
	ocpConsoleRoute := &routev1.Route{}
	err = r.Get(ctx, types.NamespacedName{Name: "console", Namespace: "openshift-console"}, ocpConsoleRoute)
	var appsDomain string
	if err == nil {
		status := ocpConsoleRoute.Status.DeepCopy()
		routerDomain := status.Ingress[0].RouterCanonicalHostname
		s := strings.Split(routerDomain, ".")
		appsDomain = strings.Join(s[len(s)-5:], ".")
		log.V(0).Info("Openshift Domain -> " + appsDomain)
	} else {
		return ctrl.Result{}, err
	}

	if app.Spec.ServiceMesh && app.Spec.Knative {
		c, e, knativeNPStatus := r.createJumpAppKnativeMeshNP(ctx, app, log)
		knativeMicrosStatus = append(knativeMicrosStatus, knativeNPStatus)
		if e != nil {
			return c, e
		}
	}

	// Split Microservices and iterate for each of them
	jumpappItems := app.Spec.Microservices
	for _, micro := range jumpappItems {
		log.V(0).Info("Processing microservice " + micro.Name)

		//Define public domain, mesh domain and url
		meshDomain := istioNS + "." + appsDomain
		regularDomain := app.Namespace + "." + appsDomain
		publicMicroMeshDomain := micro.Name + "-" + meshDomain
		urlPattern := regularDomain
		if app.Spec.ServiceMesh {
			urlPattern = meshDomain
		}

		//Create Microservice Objects
		var c reconcile.Result
		var e error
		var podStatus string
		if micro.Knative && app.Spec.Knative {
			c, e, podStatus = r.createJumpAppKnativeServing(ctx, app, urlPattern, micro, log)
			podsStatus = append(podsStatus, podStatus)
			if e != nil {
				return c, e
			}
		} else {
			c, e, podStatus = r.createJumpAppDeployment(ctx, app, urlPattern, micro, log)
			podsStatus = append(podsStatus, podStatus)
			if e != nil {
				return c, e
			}

			c, e, svcStatus := r.createJumpAppService(ctx, app, micro, log)
			svcsStatus = append(svcsStatus, svcStatus)
			if e != nil {
				return c, e
			}

			if app.Spec.ServiceMesh {
				log.V(0).Info("Creating ServiceMesh objects")
				c, e, meshMicroStatus := r.createJumpAppMicroMesh(ctx, app, publicMicroMeshDomain, micro, log)
				meshMicrosStatus = append(meshMicrosStatus, meshMicroStatus)
				if e != nil {
					return c, e
				}
			}

			if micro.Public == true {
				c, e, routeStatus := r.createJumpAppRoute(ctx, app, istioNS, micro, log)
				routesStatus = append(routesStatus, routeStatus)
				if e != nil {
					return c, e
				}
			}
		}
	}

	// Update App Status
	log.V(0).Info("Updating App Status...")
	app.Status.Pods = podsStatus
	app.Status.Services = svcsStatus
	app.Status.Routes = routesStatus
	app.Status.Mesh = meshMicrosStatus
	app.Status.Knative = knativeMicrosStatus
	if err = r.Status().Update(ctx, app); err != nil {
		log.Error(err, "Failed to update App status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpApp(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	c, e := r.destroyJumpAppDeployments(ctx, ns, log)
	if e != nil {
		return c, e
	}
	c, e = r.destroyJumpAppServices(ctx, ns, log)
	if e != nil {
		return c, e
	}
	c, e = r.destroyJumpAppRoutes(ctx, ns, log)
	if e != nil {
		return c, e
	}
	c, e = r.destroyJumpAppMeshDR(ctx, ns, log)
	if e != nil {
		return c, e
	}
	c, e = r.destroyJumpAppMeshVS(ctx, ns, log)
	if e != nil {
		return c, e
	}
	c, e = r.destroyJumpAppMeshGW(ctx, ns, log)
	if e != nil {
		return c, e
	}
	c, e = r.destroyJumpAppKnativeServing(ctx, ns, log)
	if e != nil {
		return c, e
	}
	c, e = r.destroyJumpAppKnativeMeshNP(ctx, ns, log)
	if e != nil {
		return c, e
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppDeployments(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &appsv1.DeploymentList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, dep := range objs.Items {
			if err = r.Delete(ctx, &dep); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Deployment " + dep.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppServices(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &corev1.ServiceList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, srv := range objs.Items {
			if err = r.Delete(ctx, &srv); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Service " + srv.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppRoutes(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &routev1.RouteList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, route := range objs.Items {
			if err = r.Delete(ctx, &route); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Route " + route.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppMeshDR(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &v1alpha3.DestinationRuleList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, dep := range objs.Items {
			if err = r.Delete(ctx, &dep); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Destination Rule " + dep.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppMeshVS(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &v1alpha3.VirtualServiceList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, dep := range objs.Items {
			if err = r.Delete(ctx, &dep); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Virtual Service " + dep.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppMeshGW(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &v1alpha3.GatewayList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, dep := range objs.Items {
			if err = r.Delete(ctx, &dep); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Gateway " + dep.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppKnativeServing(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &servingv1.ServiceList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, dep := range objs.Items {
			if err = r.Delete(ctx, &dep); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Knative Serving " + dep.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) destroyJumpAppKnativeMeshNP(ctx context.Context, ns string, log logr.Logger) (ctrl.Result, error) {
	objs := &netv1.NetworkPolicyList{}
	err := r.List(ctx, objs, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(ns))
	if err == nil {
		for _, dep := range objs.Items {
			if err = r.Delete(ctx, &dep); err != nil {
				return ctrl.Result{}, err
			}
			log.V(0).Info("Network Policy " + dep.Name + " deleted!")
		}
	}
	return ctrl.Result{}, nil
}

func (r *AppReconciler) createJumpAppDeployment(ctx context.Context, app *jumpappv1alpha1.App, urlPattern string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findDeployment := &appsv1.Deployment{}
	deployment := r.deploymentForJumpApp(micro, app, urlPattern)
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findDeployment)
	var podStatus string
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, deployment); err != nil {
				podStatus = micro.Name + " - ERROR"
				return ctrl.Result{}, err, podStatus
			}
			log.V(0).Info("Deployment " + micro.Name + " created!")
			podStatus = micro.Name + " - created!"
		} else {
			return ctrl.Result{}, err, podStatus
		}
	} else {
		log.V(0).Info("Deployment " + micro.Name + " exists...")
		if err = r.Update(ctx, deployment); err != nil {
			podStatus = micro.Name + " - ERROR"
			return ctrl.Result{}, err, podStatus
		}

		log.V(0).Info("Deployment " + micro.Name + " updated!")
		podStatus = micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, podStatus
}

func (r *AppReconciler) createJumpAppService(ctx context.Context, app *jumpappv1alpha1.App, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findService := &corev1.Service{}
	service := r.serviceForJumpApp(micro, app)
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findService)
	var svcStatus string
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, service); err != nil {
				svcStatus = micro.Name + " - ERROR"
				return ctrl.Result{}, err, svcStatus
			}
			log.V(0).Info("Service " + micro.Name + " created!")
			svcStatus = micro.Name + " - created!"
		} else {
			return ctrl.Result{}, err, svcStatus
		}
	} else {
		log.V(0).Info("Service " + micro.Name + " exists...")
		if err = r.Update(ctx, service); err != nil {
			svcStatus = micro.Name + " - ERROR"
			return ctrl.Result{}, err, svcStatus
		}
		log.V(0).Info("Service " + micro.Name + " updated!")
		svcStatus = micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, svcStatus
}

func (r *AppReconciler) createJumpAppRoute(ctx context.Context, app *jumpappv1alpha1.App, istioNS string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findRoute := &routev1.Route{}
	route := &routev1.Route{}

	// Define the namespace where the route has to be created
	ns := app.Namespace
	if app.Spec.ServiceMesh {
		route = r.routeMeshForJumpApp(micro, app, istioNS)
		ns = istioNS
	} else {
		route = r.routeForJumpApp(micro, app)
	}

	var routeStatus string
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: ns}, findRoute)
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, route); err != nil {
				routeStatus = micro.Name + " - ERROR"
				return ctrl.Result{}, err, routeStatus
			}
			log.V(0).Info("Route " + micro.Name + " created!")
			routeStatus = micro.Name + " - created!"
		} else {
			return ctrl.Result{}, err, routeStatus
		}
	} else {
		log.V(0).Info("Route " + micro.Name + " exists...")
		if err = r.Update(ctx, route); err != nil {
			routeStatus = micro.Name + " - ERROR"
			return ctrl.Result{}, err, routeStatus
		}
		log.V(0).Info("Route " + micro.Name + " updated!")
		routeStatus = micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, routeStatus
}

func (r *AppReconciler) createJumpAppMicroMesh(ctx context.Context, app *jumpappv1alpha1.App, publicMeshDomain string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {

	meshMicroStatus := []string{}

	if micro.Public == true {
		c, e, gwStatus := r.createJumpAppMeshGW(ctx, app, publicMeshDomain, micro, log)
		meshMicroStatus = append(meshMicroStatus, gwStatus)
		if e != nil {
			return c, e, strings.Join(meshMicroStatus, " / ")
		}
	}
	c, e, svStatus := r.createJumpAppMeshVS(ctx, app, publicMeshDomain, micro, log)
	meshMicroStatus = append(meshMicroStatus, svStatus)
	if e != nil {
		return c, e, strings.Join(meshMicroStatus, " / ")
	}
	c, e, drStatus := r.createJumpAppMeshDR(ctx, app, micro, log)
	meshMicroStatus = append(meshMicroStatus, drStatus)
	if e != nil {
		return c, e, strings.Join(meshMicroStatus, " / ")
	}

	return c, nil, strings.Join(meshMicroStatus, " / ")
}

func (r *AppReconciler) createJumpAppMeshGW(ctx context.Context, app *jumpappv1alpha1.App, publicMeshDomain string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	// Check if the Gateway already exists, if not create a new one
	findGW := &v1alpha3.Gateway{}

	gw := r.gatewayForJumpAppMesh(micro, app, publicMeshDomain)
	var gwStatus string
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findGW)
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, gw); err != nil {
				gwStatus = micro.Name + " - ERROR"
				return ctrl.Result{}, err, gwStatus
			}
			log.V(0).Info("Gateway " + micro.Name + " created!")
			gwStatus = "Gateway " + micro.Name + " - created!"
		} else {
			return ctrl.Result{}, err, gwStatus
		}
	} else {
		log.V(0).Info("Gateway " + micro.Name + " exists...")
		if err = r.Update(ctx, gw); err != nil {
			gwStatus = micro.Name + " - ERROR"
			return ctrl.Result{}, err, gwStatus
		}
		log.V(0).Info("Gateway " + micro.Name + " updated!")
		gwStatus = "Gateway " + micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, gwStatus
}

func (r *AppReconciler) createJumpAppMeshVS(ctx context.Context, app *jumpappv1alpha1.App, publicMeshDomain string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findVS := &v1alpha3.VirtualService{}
	vs := r.virtualServiceForJumpAppMesh(micro, app, publicMeshDomain)
	var vsStatus string
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findVS)
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, vs); err != nil {
				vsStatus = micro.Name + " - ERROR"
				return ctrl.Result{}, err, vsStatus
			}
			log.V(0).Info("Virtual Service " + micro.Name + " created!")
			vsStatus = "Virtual Service " + micro.Name + " - created!"
		} else {
			return ctrl.Result{}, err, vsStatus
		}
	} else {
		log.V(0).Info("Virtual Service " + micro.Name + " exists...")
		if err = r.Update(ctx, vs); err != nil {
			vsStatus = micro.Name + " - ERROR"
			return ctrl.Result{}, err, vsStatus
		}
		log.V(0).Info("Virtual Service " + micro.Name + " updated!")
		vsStatus = "Virtual Service " + micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, vsStatus
}

func (r *AppReconciler) createJumpAppMeshDR(ctx context.Context, app *jumpappv1alpha1.App, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findDR := &v1alpha3.DestinationRule{}
	dr := r.destinationRuleForJumpAppMesh(micro, app)
	var drStatus string
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findDR)
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, dr); err != nil {
				drStatus = micro.Name + " - ERROR"
				return ctrl.Result{}, err, drStatus
			}
			log.V(0).Info("Destination Rule " + micro.Name + " created!")
			drStatus = "Destination Rule " + micro.Name + " - created!"
		} else {
			return ctrl.Result{}, err, drStatus
		}
	} else {
		log.V(0).Info("Destination Rule " + micro.Name + " exists...")
		if err = r.Update(ctx, dr); err != nil {
			drStatus = micro.Name + " - ERROR"
			return ctrl.Result{}, err, drStatus
		}
		log.V(0).Info("Destination Rule " + micro.Name + " updated!")
		drStatus = "Destination Rule " + micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, drStatus
}

func (r *AppReconciler) createJumpAppKnativeServing(ctx context.Context, app *jumpappv1alpha1.App, urlPattern string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findKnativeServing := &servingv1.Service{}
	serving := r.servingForJumpAppKnative(micro, app, urlPattern)
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findKnativeServing)
	var podStatus string
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, serving); err != nil {
				podStatus = micro.Name + " - ERROR"
				return ctrl.Result{}, err, podStatus
			}
			log.V(0).Info("Knative Serving " + micro.Name + " created!")
			podStatus = micro.Name + " - created!"
		} else {
			return ctrl.Result{}, err, podStatus
		}
	} else {
		log.V(0).Info("Knative Serving " + micro.Name + " exists...")
		if err = r.Update(ctx, serving); err != nil {
			podStatus = micro.Name + " - ERROR"
			return ctrl.Result{}, err, podStatus
		}
		log.V(0).Info("Knative Serving " + micro.Name + " updated!")
		podStatus = micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, podStatus
}

func (r *AppReconciler) createJumpAppKnativeMeshNP(ctx context.Context, app *jumpappv1alpha1.App, log logr.Logger) (ctrl.Result, error, string) {
	findNetworkPolicy := &netv1.NetworkPolicy{}
	npName := "allow-from-serving-system-namespace"
	networkPolicy := r.networkPolicyForJumpApp(app, npName)
	err := r.Get(ctx, types.NamespacedName{Name: npName, Namespace: app.Namespace}, findNetworkPolicy)
	var npStatus string
	if err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, networkPolicy); err != nil {
				npStatus = "Creating Network Policy allow-from-serving-system-namespace - ERROR"
				return ctrl.Result{}, err, npStatus
			}
			log.V(0).Info("Network Policy allow-from-serving-system-namespace created!")
			npStatus = "Network Policy allow-from-serving-system-namespace created!"
		} else {
			return ctrl.Result{}, err, npStatus
		}
	} else {
		log.V(0).Info("Network Policy allow-from-serving-system-namespace exists...")
		if err = r.Update(ctx, networkPolicy); err != nil {
			npStatus = "Updating Network Policy allow-from-serving-system-namespace - ERROR"
			return ctrl.Result{}, err, npStatus
		}
		log.V(0).Info("Network Policy allow-from-serving-system-namespace updated!")
		npStatus = "Network Policy allow-from-serving-system-namespace updated!"
	}
	return ctrl.Result{}, nil, npStatus
}

func (r *AppReconciler) deploymentForJumpApp(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App, urlPattern string) *appsv1.Deployment {

	// Define labels
	lbls := labelsForApp(micro.Name)

	// Define the number or replicas (Global or particular value)
	var replicas int32
	if micro.Replicas != 0 {
		replicas = micro.Replicas
	} else {
		replicas = app.Spec.Replicas
	}

	// Define envs
	envVars := []corev1.EnvVar{}
	if micro.Backend != "" {
		envVar := &corev1.EnvVar{}
		envVar.Name = "REACT_APP_BACK"
		envVar.Value = "https://" + micro.Backend + "-" + urlPattern + "/jump"
		envVars = append(envVars, *envVar)
		for _, appMicro := range app.Spec.Microservices {
			name := strings.Split(appMicro.Name, "-")
			envVar.Name = "REACT_APP_" + strings.ToUpper(name[1])
			if appMicro.Knative {
				envVar.Value = "http://" + appMicro.Name + "." + app.Namespace + ".svc.cluster.local"
			} else {
				envVar.Value = "http://" + appMicro.Name + ":" + strconv.Itoa(int(appMicro.SvcPort))
			}
			envVars = append(envVars, *envVar)
		}
	}

	// Define annotations
	annotations := map[string]string{}
	if app.Spec.ServiceMesh {
		annotations = map[string]string{
			"sidecar.istio.io/inject": "true",
		}
	}

	// Define deployment object
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      micro.Name,
			Namespace: app.Namespace,
			Labels:    lbls,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: lbls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      lbls,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: micro.Image,
						Name:  micro.Name,
						Ports: []corev1.ContainerPort{{
							ContainerPort: micro.PodPort,
							Protocol:      "TCP",
						}},
						Env: envVars,
					}},
				},
			},
		},
	}

	return dep
}

// serviceForJumpApp returns a Service object for data from micro and app
func (r *AppReconciler) serviceForJumpApp(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App) *corev1.Service {

	// Define labels
	lbls := labelsForApp(micro.Name)

	// Define service object
	srv := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      micro.Name,
			Namespace: app.Namespace,
			Labels:    lbls,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http-" + strconv.Itoa(int(micro.SvcPort)),
					Port:       micro.SvcPort,
					Protocol:   "TCP",
					TargetPort: intstr.FromInt(int(micro.PodPort)),
				},
			},
			Selector: lbls,
		},
	}

	return srv
}

func (r *AppReconciler) routeForJumpApp(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App) *routev1.Route {

	// Define labels
	lbls := labelsForApp(micro.Name)

	// Define route object
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      micro.Name,
			Namespace: app.Namespace,
			Labels:    lbls,
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: micro.Name,
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromString("http-" + strconv.Itoa(int(micro.SvcPort))),
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
		},
	}

	return route
}

func (r *AppReconciler) routeMeshForJumpApp(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App, istioNS string) *routev1.Route {

	// Define labels
	lbls := labelsForApp(micro.Name)

	// Define route object
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      micro.Name,
			Namespace: istioNS,
			Labels:    lbls,
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "istio-ingressgateway",
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromString("http2"),
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
		},
	}

	return route
}

func (r *AppReconciler) gatewayForJumpAppMesh(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App, publicMeshDomain string) *v1alpha3.Gateway {

	// Define labels
	lbls := labelsForApp(micro.Name)

	// Define Gateway object
	gw := &v1alpha3.Gateway{
		ObjectMeta: v1.ObjectMeta{
			Name:      micro.Name,
			Namespace: app.Namespace,
			Labels:    lbls,
		},
		Spec: v1alpha3Spec.Gateway{
			Selector: map[string]string{
				"istio": "ingressgateway",
			},
			Servers: []*v1alpha3Spec.Server{{
				Port: &v1alpha3Spec.Port{
					Number:   80,
					Protocol: "HTTP",
					Name:     "http",
				},
				Hosts: []string{
					publicMeshDomain,
				},
			}},
		},
	}

	return gw
}

func (r *AppReconciler) virtualServiceForJumpAppMesh(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App, publicMeshDomain string) *v1alpha3.VirtualService {

	// Define labels
	lbls := labelsForApp(micro.Name)

	// Define hosts and gateways to public services
	hosts := []string{
		micro.Name,
	}
	gateways := []string{
		"mesh",
	}
	if micro.Public {
		hosts = append(hosts, publicMeshDomain)
		gateways = append(gateways, micro.Name)
	}

	// Define Virtual Service object
	vs := &v1alpha3.VirtualService{
		ObjectMeta: v1.ObjectMeta{
			Name:      micro.Name,
			Namespace: app.Namespace,
			Labels:    lbls,
		},
		Spec: v1alpha3Spec.VirtualService{
			Gateways: gateways,
			Hosts:    hosts,
			Http: []*v1alpha3Spec.HTTPRoute{{
				Name: "default",
				Route: []*v1alpha3Spec.HTTPRouteDestination{{
					Destination: &v1alpha3Spec.Destination{
						Host:   micro.Name,
						Subset: "v1",
					},
					Weight: 100,
				}},
			}},
		},
	}

	return vs
}

func (r *AppReconciler) destinationRuleForJumpAppMesh(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App) *v1alpha3.DestinationRule {

	// Define labels
	lbls := labelsForApp(micro.Name)

	// Define Destination Rule object
	dr := &v1alpha3.DestinationRule{
		ObjectMeta: v1.ObjectMeta{
			Name:      micro.Name,
			Namespace: app.Namespace,
			Labels:    lbls,
		},
		Spec: v1alpha3Spec.DestinationRule{
			Host: micro.Name,
			Subsets: []*v1alpha3Spec.Subset{{
				Name:   "v1",
				Labels: lbls,
			}},
		},
	}

	return dr
}

func (r *AppReconciler) servingForJumpAppKnative(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App, urlPattern string) *servingv1.Service {

	// Define labels
	lbls := labelsForApp(micro.Name)
	if !micro.Public {
		lbls["networking.knative.dev/visibility"] = "cluster-local"
	}

	// Define envs
	envVars := []corev1.EnvVar{}
	if micro.Backend != "" {
		envVar := &corev1.EnvVar{}
		envVar.Name = "REACT_APP_BACK"
		envVar.Value = "https://" + micro.Backend + "-" + urlPattern + "/jump"
		envVars = append(envVars, *envVar)
		for _, appMicro := range app.Spec.Microservices {
			name := strings.Split(appMicro.Name, "-")
			envVar.Name = "REACT_APP_" + strings.ToUpper(name[1])
			envVar.Value = "http://" + appMicro.Name + ":" + strconv.Itoa(int(appMicro.SvcPort))
			envVars = append(envVars, *envVar)
		}
	}

	// Define annotations
	annotations := map[string]string{}
	if app.Spec.ServiceMesh {
		annotations = map[string]string{
			"sidecar.istio.io/inject": "true",
		}
	}

	// Helpers int64
	timeout := int64(300)
	concurrency := int64(0)

	// Define Destination Rule object
	service := &servingv1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:        micro.Name,
			Namespace:   app.Namespace,
			Labels:      lbls,
			Annotations: annotations,
		},
		Spec: servingv1.ServiceSpec{
			ConfigurationSpec: servingv1.ConfigurationSpec{
				Template: servingv1.RevisionTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      lbls,
						Annotations: annotations,
					},
					Spec: servingv1.RevisionSpec{
						PodSpec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Image: micro.Image,
								Name:  micro.Name,
								Ports: []corev1.ContainerPort{{
									ContainerPort: micro.PodPort,
									Protocol:      "TCP",
								}},
								Env: envVars,
							}},
						},
						TimeoutSeconds:       &timeout,
						ContainerConcurrency: &concurrency,
					},
				},
			},
		},
	}

	return service
}

func (r *AppReconciler) networkPolicyForJumpApp(app *jumpappv1alpha1.App, nsName string) *netv1.NetworkPolicy {

	// Define labels
	lbls := labelsForApp("NetworkPolicy")

	selector := map[string]string{
		"serving.knative.openshift.io/system-namespace": "true",
	}

	// Define Destination Rule object
	networkPolicy := &netv1.NetworkPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      nsName,
			Namespace: app.Namespace,
			Labels:    lbls,
		},
		Spec: netv1.NetworkPolicySpec{
			Ingress: []netv1.NetworkPolicyIngressRule{{
				From: []netv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: selector,
					},
				}},
			}},
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{
				netv1.PolicyTypeIngress,
			},
		},
	}

	return networkPolicy
}

func labelsForApp(name string) map[string]string {
	return map[string]string{
		"app":             name,
		"name":            name,
		"version":         "v1",
		"jumpapp-creator": "operator",
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jumpappv1alpha1.App{}).
		Complete(r)
}
