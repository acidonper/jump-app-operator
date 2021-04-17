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

func (r *AppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("app", req.NamespacedName)

	// Get Apps
	app := &jumpappv1alpha1.App{}
	err := r.Get(ctx, req.NamespacedName, app)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
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
		err = r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, foundDeployment)
		if err != nil {
			if errors.IsNotFound(err) {
				// Define and create a new deployment
				dep := r.deploymentForJumpApp(micro, app, appsDomain)
				if err = r.Create(ctx, dep); err != nil {
					return ctrl.Result{}, err
				}
				log.V(0).Info("Deployment " + micro.Name + " created!")
				return ctrl.Result{Requeue: true}, nil
			} else {
				return ctrl.Result{}, err
			}
		} else {
			log.V(0).Info("Deployment " + micro.Name + " exists...")
		}

		// Check if the service already exists, if not create a new service
		foundService := &corev1.Service{}
		err = r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, foundService)
		if err != nil {
			if errors.IsNotFound(err) {
				// Define and create a new service
				dep := r.serviceForJumpApp(micro, app)
				if err = r.Create(ctx, dep); err != nil {
					return ctrl.Result{}, err
				}
				log.V(0).Info("Service " + micro.Name + " created!")
				return ctrl.Result{Requeue: true}, nil
			} else {
				return ctrl.Result{}, err
			}
		} else {
			log.V(0).Info("Service " + micro.Name + " exists...")
		}

		if micro.Public == true {
			// Check if the route already exists, if not create a new route
			foundRoute := &routev1.Route{}
			err = r.Get(ctx, types.NamespacedName{Name: micro.Name, Namespace: app.Namespace}, foundRoute)
			if err != nil {
				if errors.IsNotFound(err) {
					// Define and create a new route
					dep := r.routeForJumpApp(micro, app)
					if err = r.Create(ctx, dep); err != nil {
						return ctrl.Result{}, err
					}
					log.V(0).Info("Route " + micro.Name + " created!")
					return ctrl.Result{Requeue: true}, nil
				} else {
					return ctrl.Result{}, err
				}
			} else {
				log.V(0).Info("Route " + micro.Name + " exists...")
			}
		}
	}

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
	return map[string]string{"app": name, "name": name, "version": "v1"}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jumpappv1alpha1.App{}).
		Complete(r)
}
