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
	githubcomv1alpha1 "github.com/bohunn/thedealer/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
)

//getDiscoveryClient returns a discovery client for the current reconciler
//func getDiscoveryClient(config *rest.Config) (*discovery.DiscoveryClient, error) {
//	return discovery.NewDiscoveryClient(config), nil
//}

// DatabaseReconciler reconciles a Database object
type DatabaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=github.com,resources=databases,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=github.com,resources=databases/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=github.com,resources=databases/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Database object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *DatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx, "Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Reconciling Database")

	// Fetch the Database instance
	instance := &githubcomv1alpha1.Database{}

	err := r.Client.Get(context.TODO(), req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	//-- Checking if config map has been changed
	configMapChanged, err := r.ensureLatestConfigMap(instance)
	if err != nil {
		return reconcile.Result{}, err
	}
	err = r.ensureLatestPod(instance, configMapChanged)
	if err != nil {
		return reconcile.Result{}, err
	}

	//-- Checking if Service has been changed
	serviceChanged, err := r.ensureServiceForCr(instance)

	if err != nil {
		return reconcile.Result{}, err
	}
	err = r.ensureLatestPod(instance, serviceChanged)

	if err != nil {
		return reconcile.Result{}, err
	}

	config, err := ctrl.GetConfig()
	if err == nil && config != nil {
		dclient, err := getDiscoveryClient(config)
		if err == nil && dclient != nil {
			apiGroupList, err := dclient.ServerGroups()
			if err != nil {
				reqLogger.Info("Error while querying ServerGroups, assuming we're on Vanilla Kubernetes")
			} else {
				for i := 0; i < len(apiGroupList.Groups); i++ {
					if strings.HasSuffix(apiGroupList.Groups[i].Name, ".openshift.io") {
						//isOpenShift = true
						reqLogger.Info("Detected Openshift ")
						break
					}
				}
			}
		} else {
			reqLogger.Info("Cannot retrieve a DiscoveryClient, assuming Kubernetes")
		}
	}

	return ctrl.Result{}, nil
}

// getDiscoveryClient returns a discovery client for the current reconciler
func getDiscoveryClient(config *rest.Config) (*discovery.DiscoveryClient, error) {
	return discovery.NewDiscoveryClientForConfig(config)
}

//func ignoreDeletionPredicate() predicate.Predicate {
//	reqLogger := log.Log //log.FromContext(ctx, "Request.Namespace", req.Namespace, "Request.Name", req.Name)
//	reqLogger.Info("ignoreDeletionPredicate")
//
//	return predicate.Funcs{
//		UpdateFunc: func(event event.UpdateEvent) bool {
//			return event.ObjectOld.GetGeneration() != event.ObjectNew.GetGeneration()
//		},
//		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
//			return !deleteEvent.DeleteStateUnknown
//		},
//	}
//}

// SetupWithManager sets up the controller with the Manager.
func (r *DatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&githubcomv1alpha1.Database{}).
		Complete(r)
}

func newServiceForCr(cr *githubcomv1alpha1.Database) *v1.Service {
	logger := log.Log.WithName("newServiceForCr")
	logger.Info("Creating a new service")
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-service",
			Namespace: cr.Namespace,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Protocol: v1.ProtocolTCP,
					Port:     8090,
					//NodePort: 8090,
				},
			},
			Type: "NodePort",
		},
		Status: v1.ServiceStatus{},
	}
}

func (r *DatabaseReconciler) ensureServiceForCr(instance *githubcomv1alpha1.Database) (bool, error) {
	logger := log.Log.WithName("ensureServiceForCr")
	service := newServiceForCr(instance)

	foundService := &v1.Service{}

	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: service.Name}, foundService)

	logger.Info("Finding service", "err", err)
	if err != nil && errors.IsNotFound(err) {
		err = r.Client.Create(context.TODO(), service)
	}
	if err != nil && errors.IsAlreadyExists(err) {
		//err = r.Client.Update(context.TODO(), service)
		err = nil
	}
	if err != nil {
		return false, err
	}

	//if service.Kind != foundService.Kind {
	//	err = r.Client.Update(context.TODO(), service)
	//	if err != nil {
	//		return false, err
	//	}
	//	return true, nil
	//}

	return false, nil

}

func newPodForCR(cr *githubcomv1alpha1.Database) *v1.Pod {
	labels := map[string]string{
		"app": cr.Name,
	}
	volumeName := cr.Name + "-config"
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pod",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "document-service",
					Image: cr.Spec.Image, //"bohunn/document-service",
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      volumeName,
							MountPath: "/config",
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: volumeName,
					VolumeSource: v1.VolumeSource{
						ConfigMap: &v1.ConfigMapVolumeSource{
							LocalObjectReference: v1.LocalObjectReference{
								Name: cr.Name + "-config",
							},
						},
					},
				},
			},
		},
	}
}

func (r *DatabaseReconciler) ensureLatestService(instance *githubcomv1alpha1.Database) (bool, error) {
	service := newService(instance)

	// Set Database instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, service, r.Scheme); err != nil {
		return false, err
	}

	// Check if this ConfigMap already exists
	foundService := &v1.Service{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService)
	if err != nil && errors.IsNotFound(err) {
		err = r.Client.Create(context.TODO(), service)
		if errors.IsAlreadyExists(err) {
			//err = r.Client.Update(context.TODO(), service)
			err = nil
		}
		if err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}

	//if foundService.Kind != service.Kind {
	//	err = r.Client.Update(context.TODO(), service)
	//	if err != nil {
	//		return false, err
	//	}
	//	return true, nil
	//}
	return false, nil

}

func newService(cr *githubcomv1alpha1.Database) *v1.Service {
	labels := map[string]string{
		"app": cr.Name,
	}
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-service",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Protocol: v1.ProtocolTCP,
					Port:     8090,
					//NodePort: 8090,
				},
			},
			Type: "NodePort",
		},
		Status: v1.ServiceStatus{},
	}
}

func (r *DatabaseReconciler) ensureLatestConfigMap(instance *githubcomv1alpha1.Database) (bool, error) {
	configMap := newConfigMap(instance)

	// Set Database instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, configMap, r.Scheme); err != nil {
		return false, err
	}

	// Check if this ConfigMap already exists
	foundMap := &v1.ConfigMap{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, foundMap)
	if err != nil && errors.IsNotFound(err) {
		err = r.Client.Create(context.TODO(), configMap)
		if err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}

	if foundMap.Data["database.md"] != configMap.Data["database.md"] {
		err = r.Client.Update(context.TODO(), configMap)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func newConfigMap(cr *githubcomv1alpha1.Database) *v1.ConfigMap {
	labels := map[string]string{
		"app": cr.Name,
	}
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-config",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			"database.md": cr.Spec.Markdown,
		},
	}
}

func (r *DatabaseReconciler) ensureLatestPod(instance *githubcomv1alpha1.Database, configMapChanged bool) error {
	// Define a new Pod object
	pod := newPodForCR(instance)

	// Set Database instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, pod, r.Scheme); err != nil {
		return err
	}
	// Check if this Pod already exists
	found := &v1.Pod{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		err = r.Client.Create(context.TODO(), pod)
		if err != nil {
			return err
		}

		// Pod created successfully - don't requeue
		return nil
	} else if err != nil {

		return err
	}

	if configMapChanged {
		err = r.Client.Delete(context.TODO(), found)
		if err != nil {
			return err
		}
		err = r.Client.Create(context.TODO(), pod)
		if err != nil {
			return err
		}
	}
	return nil
}
