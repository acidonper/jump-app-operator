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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	jumpappv1alpha1 "github.com/acidonpe/jump-app-operator/api/v1alpha1"
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

	// Split Microservices and iterate for each of them
	jumpappItems := app.Spec.Microservices
	for _, micro := range jumpappItems {
		// Log micro name
		log.V(0).Info(micro)

		// Check if the deployment already exists, if not create a new deployment
		found := &appsv1.Deployment{}
		err = r.Get(ctx, types.NamespacedName{Name: micro, Namespace: app.Namespace}, found)
		if err != nil {
			if errors.IsNotFound(err) {
				// Define and create a new deployment.
				dep := r.deploymentForJumpApp(micro, app)
				if err = r.Create(ctx, dep); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{Requeue: true}, nil
			} else {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// deploymentForMemcached returns a Deployment object for data from m.
func (r *AppReconciler) deploymentForJumpApp(n string, m *jumpappv1alpha1.App) *appsv1.Deployment {
	lbls := labelsForApp(n)
	replicas := m.Spec.Replicas

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      n,
			Namespace: m.Namespace,
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
						Image: "quay.io/acidonpe/service-mesh-envoy-and-istio-control-plane:latest",
						Name:  n,
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8080,
							Protocol:      "TCP",
						}},
					}},
				},
			},
		},
	}

	return dep
}

// labelsForApp creates a simple set of labels for Memcached.
func labelsForApp(name string) map[string]string {
	return map[string]string{"app": name, "name": name, "version": "v1"}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jumpappv1alpha1.App{}).
		Owns(&appsv1.Deployment{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}
