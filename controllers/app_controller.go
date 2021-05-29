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
			log.V(0).Info("Deleting App Components" + app.Name)
			// Deployments
			foundDeployments := &appsv1.DeploymentList{}
			err = r.List(ctx, foundDeployments, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(app.Namespace))
			if err == nil {
				for _, dep := range foundDeployments.Items {
					if err = r.Delete(ctx, &dep); err != nil {
						return ctrl.Result{}, err
					}
					log.V(0).Info("Deployment " + dep.Name + " deleted!")
				}
			}
			// Services
			foundServices := &corev1.ServiceList{}
			err = r.List(ctx, foundServices, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(app.Namespace))
			if err == nil {
				for _, srv := range foundServices.Items {
					if err = r.Delete(ctx, &srv); err != nil {
						return ctrl.Result{}, err
					}
					log.V(0).Info("Service " + srv.Name + " deleted!")
				}
			}
			// Routes
			foundRoutes := &routev1.RouteList{}
			err = r.List(ctx, foundRoutes, client.MatchingLabels{"jumpapp-creator": "operator"}, client.InNamespace(app.Namespace))
			if err == nil {
				for _, route := range foundRoutes.Items {
					if err = r.Delete(ctx, &route); err != nil {
						return ctrl.Result{}, err
					}
					log.V(0).Info("Route " + route.Name + " deleted!")
				}
			}
			return ctrl.Result{}, nil
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
		appsDomain = status.Ingress[0].RouterCanonicalHostname
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
		foundDeployment := &appsv1.Deployment{}
		dep := r.deploymentForJumpApp(micro, app, appsDomain)
		err = r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, foundDeployment)
		if err != nil {
			if errors.IsNotFound(err) {
				// Define and create a new deployment
				if err = r.Create(ctx, dep); err != nil {
					podsStatus = append(podsStatus, micro.Name+" - ERROR")
					return ctrl.Result{}, err
				}
				log.V(0).Info("Deployment " + micro.Name + " created!")
				podsStatus = append(podsStatus, micro.Name+" - created!")
			} else {
				return ctrl.Result{}, err
			}
		} else {
			log.V(0).Info("Deployment " + micro.Name + " exists...")
			err = r.Update(ctx, dep)
			log.V(0).Info("Deployment " + micro.Name + " updated!")
			podsStatus = append(podsStatus, micro.Name+" - updated!")
		}

		// Check if the service already exists, if not create a new service
		foundService := &corev1.Service{}
		srv := r.serviceForJumpApp(micro, app)
		err = r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, foundService)
		if err != nil {
			if errors.IsNotFound(err) {
				// Define and create a new service
				if err = r.Create(ctx, srv); err != nil {
					svcsStatus = append(svcsStatus, micro.Name+" - ERROR")
					return ctrl.Result{}, err
				}
				log.V(0).Info("Service " + micro.Name + " created!")
				svcsStatus = append(svcsStatus, micro.Name+" - created!")
			} else {
				return ctrl.Result{}, err
			}
		} else {
			log.V(0).Info("Service " + micro.Name + " exists...")
			err = r.Update(ctx, srv)
			log.V(0).Info("Service " + micro.Name + " updated!")
			svcsStatus = append(svcsStatus, micro.Name+" - updated!")
		}

		if micro.Public == true {
			// Check if the route already exists, if not create a new route
			foundRoute := &routev1.Route{}
			route := r.routeForJumpApp(micro, app)
			err = r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, foundRoute)
			if err != nil {
				if errors.IsNotFound(err) {
					// Define and create a new route
					if err = r.Create(ctx, route); err != nil {
						routesStatus = append(routesStatus, micro.Name+" - ERROR")
						return ctrl.Result{}, err
					}
					log.V(0).Info("Route " + micro.Name + " created!")
					routesStatus = append(routesStatus, micro.Name+" - created!")
				} else {
					return ctrl.Result{}, err
				}
			} else {
				log.V(0).Info("Route " + micro.Name + " exists...")
				err = r.Update(ctx, route)
				log.V(0).Info("Route " + micro.Name + " updated!")
				routesStatus = append(routesStatus, micro.Name+" - updated!")
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
	}

	// Create deployment object
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
