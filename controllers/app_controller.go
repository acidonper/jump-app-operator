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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	jumpappv1alpha1 "github.com/acidonpe/jump-app-operator/api/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (r *AppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Define logs and status variables
	log := r.Log.WithValues("app", req.NamespacedName)
	podsStatus := []string{}
	svcsStatus := []string{}
	routesStatus := []string{}

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

	// Split Microservices and iterate for each of them
	jumpappItems := app.Spec.Microservices
	for _, micro := range jumpappItems {
		// Log micro name
		log.V(0).Info("Processing microservice " + micro.Name)

		// Check if the deployment already exists, if not create a new deployment
		c, e, podStatus := r.createJumpAppDeployment(ctx, app, appsDomain, micro, log)
		podsStatus = append(podsStatus, podStatus)
		if e != nil {
			return c, e
		}

		// Check if the service already exists, if not create a new service
		c, e, svcStatus := r.createJumpAppService(ctx, app, appsDomain, micro, log)
		svcsStatus = append(svcsStatus, svcStatus)
		if e != nil {
			return c, e
		}

		if micro.Public == true && app.Spec.ServiceMesh == false {
			// Check if the route already exists, if not create a new route
			c, e, routeStatus := r.createJumpAppRoute(ctx, app, appsDomain, micro, log)
			routesStatus = append(routesStatus, routeStatus)
			if e != nil {
				return c, e
			}
		}
	}

	// Update App Status
	log.V(0).Info("Updating App Status...")
	app.Status.Pods = podsStatus
	app.Status.Services = svcsStatus
	app.Status.Routes = routesStatus
	err = r.Status().Update(ctx, app)

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

func (r *AppReconciler) createJumpAppDeployment(ctx context.Context, app *jumpappv1alpha1.App, domain string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findDeployment := &appsv1.Deployment{}
	deployment := r.deploymentForJumpApp(micro, app, domain)
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findDeployment)
	var podStatus string
	if err != nil {
		if errors.IsNotFound(err) {
			// Define and create a new deployment
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
		err = r.Update(ctx, deployment)
		log.V(0).Info("Deployment " + micro.Name + " updated!")
		podStatus = micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, podStatus
}

func (r *AppReconciler) createJumpAppService(ctx context.Context, app *jumpappv1alpha1.App, domain string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findService := &corev1.Service{}
	service := r.serviceForJumpApp(micro, app)
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findService)
	var svcStatus string
	if err != nil {
		if errors.IsNotFound(err) {
			// Define and create a new service
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
		err = r.Update(ctx, service)
		log.V(0).Info("Service " + micro.Name + " updated!")
		svcStatus = micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, svcStatus
}

func (r *AppReconciler) createJumpAppRoute(ctx context.Context, app *jumpappv1alpha1.App, domain string, micro jumpappv1alpha1.Micro, log logr.Logger) (ctrl.Result, error, string) {
	findRoute := &routev1.Route{}
	route := r.routeForJumpApp(micro, app)
	var routeStatus string
	err := r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, findRoute)
	if err != nil {
		if errors.IsNotFound(err) {
			// Define and create a new route
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
		err = r.Update(ctx, route)
		log.V(0).Info("Route " + micro.Name + " updated!")
		routeStatus = micro.Name + " - updated!"
	}
	return ctrl.Result{}, nil, routeStatus
}

// deploymentForJumpApp returns a Deployment object for data from micro and app
func (r *AppReconciler) deploymentForJumpApp(micro jumpappv1alpha1.Micro, app *jumpappv1alpha1.App, dom string) *appsv1.Deployment {

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
		envVar.Value = "https://" + micro.Backend + "-" + app.Namespace + "." + dom + "/jump"
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

	// Create deployment object
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        micro.Name,
			Namespace:   app.Namespace,
			Labels:      lbls,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: lbls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: lbls,
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

	// Create service object
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

	// Create route object
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

// labelsForApp creates a simple set of labels for Memcached.
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
