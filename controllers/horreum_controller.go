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

package horreum

import (
	"context"
	stdErrors "errors"
	"fmt"
	"reflect"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfoilv1alpha1 "github.com/Hyperfoil/horreum-operator/api/v1alpha1"
	"github.com/google/go-cmp/cmp"

	logr "github.com/go-logr/logr"

	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	// rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// HorreumReconciler reconciles a Horreum object
type HorreumReconciler struct {
	client.Client
	Log             logr.Logger
	Scheme          *runtime.Scheme
	RoutesAvailable bool
	UseRedHatImages bool
}

type compareFunc func(interface{}, interface{}, logr.Logger) bool
type checkFunc func(interface{}) (bool, string, string)

var nocompare = func(interface{}, interface{}, logr.Logger) bool {
	return true
}
var nocheck = func(interface{}) (bool, string, string) {
	return true, "", ""
}

//+kubebuilder:rbac:groups=hyperfoil.io,resources=horreums,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=hyperfoil.io,resources=horreums/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=hyperfoil.io,resources=horreums/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods;services;services/finalizers;endpoints;persistentvolumeclaims;events;configmaps;secrets;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;create
//+kubebuilder:rbac:groups=apps,resourceNames=horreum-operator,resources=deployments/finalizers,verbs=update
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes;routes/custom-host,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,resourceNames=nonroot,verbs=use

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Horreum object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *HorreumReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	logger.Info("Reconciling Horreum")

	// Fetch the Horreum cr
	cr := &hyperfoilv1alpha1.Horreum{}
	err := r.Get(ctx, request.NamespacedName, cr)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if cr.Spec.NodeHost == "" &&
		(isNodePort(r, cr.Spec.ServiceType) || isNodePort(r, cr.Spec.Keycloak.ServiceType)) {
		msg := "service of type NodePort is used but spec.nodeHost is not defined"
		updateStatus(r, cr, "Error", msg)
		return reconcile.Result{}, stdErrors.New(msg)
	}

	if cr.Status.Status != "Ready" {
		adminSecret := horreumAdminSecret(cr)
		cr.Status.Status = "Ready"
		cr.Status.Reason = "For admin (" + adminSecret + ") password run: kubectl get secret " + adminSecret + " -o go-template='{{.data.password|base64decode}}'"
		cr.Status.LastUpdate = metav1.Now()
	}

	if !r.RoutesAvailable {
		ca, caPrivateKey, err := createCA(cr, r, logger)
		if err != nil {
			return reconcile.Result{}, err
		}
		err = createServiceCert(cr, r, logger, ca, caPrivateKey, cr.GetName()+"-app-certs", cr.GetName(), 1000)
		if err != nil {
			return reconcile.Result{}, err
		}
		err = createServiceCert(cr, r, logger, ca, caPrivateKey, cr.GetName()+"-keycloak-certs", cr.GetName()+"-keycloak", 2000)
		if err != nil {
			return reconcile.Result{}, err
		}
	} else {
		serviceCaConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "service-ca.crt",
				Namespace: cr.Namespace,
				Annotations: map[string]string{
					"service.beta.openshift.io/inject-cabundle": "true",
				},
			},
		}
		if err := ensureSame(r, cr, logger, serviceCaConfigMap, &corev1.ConfigMap{}, nocompare, nocheck); err != nil {
			return reconcile.Result{}, err
		}
	}

	dbAdminSecret := newSecret(cr, dbAdminSecret(cr))
	if err := ensureSame(r, cr, logger, dbAdminSecret, &corev1.Secret{}, nocompare,
		checkSecret(corev1.BasicAuthUsernameKey, corev1.BasicAuthPasswordKey)); err != nil {
		return reconcile.Result{}, err
	}
	appSecret := newSecret(cr, appUserSecret(cr))
	appSecret.StringData["dbsecret"] = generatePassword()
	if err := ensureSame(r, cr, logger, appSecret, &corev1.Secret{}, nocompare,
		checkSecret(corev1.BasicAuthUsernameKey, corev1.BasicAuthPasswordKey, "dbsecret")); err != nil {
		return reconcile.Result{}, err
	}
	keycloakAdminSecret := newSecret(cr, keycloakAdminSecret(cr))
	if err := ensureSame(r, cr, logger, keycloakAdminSecret, &corev1.Secret{}, nocompare,
		checkSecret(corev1.BasicAuthUsernameKey, corev1.BasicAuthPasswordKey)); err != nil {
		return reconcile.Result{}, err
	}
	keycloakDbSecret := newSecret(cr, keycloakDbSecret(cr))
	if err := ensureSame(r, cr, logger, keycloakDbSecret, &corev1.Secret{}, nocompare,
		checkSecret(corev1.BasicAuthUsernameKey, corev1.BasicAuthPasswordKey)); err != nil {
		return reconcile.Result{}, err
	}
	horreumAdminSecret := newSecret(cr, horreumAdminSecret(cr))
	if err := ensureSame(r, cr, logger, horreumAdminSecret, &corev1.Secret{}, nocompare,
		checkSecret(corev1.BasicAuthUsernameKey, corev1.BasicAuthPasswordKey)); err != nil {
		return reconcile.Result{}, err
	}

	postgresConfigMap := postgresConfigMap(cr)
	postgresPod := postgresPod(cr, r)
	postgresService := postgresService(cr)
	if cr.Spec.Postgres.Enabled != nil && !*cr.Spec.Postgres.Enabled {
		if err := ensureDeleted(r, cr, postgresPod, &corev1.Pod{}); err != nil {
			return reconcile.Result{}, err
		}
		if err := ensureDeleted(r, cr, postgresService, &corev1.Service{}); err != nil {
			return reconcile.Result{}, err
		}
	} else {
		if err := ensureSame(r, cr, logger, postgresConfigMap, &corev1.ConfigMap{}, compareConfigMap, nocheck); err != nil {
			return reconcile.Result{}, err
		}
		if err := ensureSame(r, cr, logger, postgresPod, &corev1.Pod{}, comparePods, checkPod); err != nil {
			return reconcile.Result{}, err
		}
		if err := ensureSame(r, cr, logger, postgresService, &corev1.Service{}, compareService, nocheck); err != nil {
			return reconcile.Result{}, err
		}
	}

	keycloakService := keycloakService(cr, r)
	keycloakRoute, err := keycloakRoute(cr, r)
	if err != nil {
		return reconcile.Result{}, err
	}
	keycloakPublicUrl := cr.Spec.Keycloak.External.PublicUri
	if keycloakPublicUrl == "" {
		if err := ensureSame(r, cr, logger, keycloakService, &corev1.Service{}, compareService, nocheck); err != nil {
			return reconcile.Result{}, err
		}
		if isNodePort(r, cr.Spec.Keycloak.ServiceType) {
			nodePort, err := getNodePort(r, keycloakService, logger)
			if err != nil {
				return reconcile.Result{}, err
			} else if nodePort == 0 {
				updateStatus(r, cr, "Pending", "Waiting for Keycloak service node port")
				logger.Info("Waiting for Keycloak service node port to be assigned")
				return reconcile.Result{Requeue: true}, nil
			}
			keycloakPublicUrl = fmt.Sprintf("https://%s:%d", cr.Spec.NodeHost, nodePort)
		} else {
			if cr.Spec.Keycloak.ServiceType == corev1.ServiceTypeLoadBalancer {
				keycloakPublicUrl, err = getLoadBalancer(r, keycloakService, logger)
				if err != nil {
					return reconcile.Result{}, err
				}
			} else if r.RoutesAvailable {
				foundRoute := &routev1.Route{}
				if err := ensureSame(r, cr, logger, keycloakRoute, foundRoute, compareRoute, checkRoute); err != nil {
					return reconcile.Result{}, err
				}
				keycloakPublicUrl = getRouteUrl(foundRoute)
			}
			if keycloakPublicUrl == "" {
				updateStatus(r, cr, "Pending", "Waiting for Keycloak service URL")
				logger.Info("Waiting for Keycloak service URL to be assigned")
				return reconcile.Result{Requeue: true}, nil
			}
		}
	}
	cr.Status.KeycloakUrl = keycloakPublicUrl

	keycloakConfigMap := keycloakConfigMap(cr)
	if err := ensureSame(r, cr, logger, keycloakConfigMap, &corev1.ConfigMap{}, compareConfigMap, nocheck); err != nil {
		return reconcile.Result{}, err
	}
	keycloakPod := keycloakPod(cr, keycloakPublicUrl)
	if cr.Spec.Keycloak.External.PublicUri != "" {
		if err := ensureDeleted(r, cr, keycloakPod, &corev1.Pod{}); err != nil {
			return reconcile.Result{}, err
		}
		if err := ensureDeleted(r, cr, keycloakService, &corev1.Service{}); err != nil {
			return reconcile.Result{}, err
		}
		if r.RoutesAvailable {
			if err := ensureDeleted(r, cr, keycloakRoute, &routev1.Route{}); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		if err := ensureSame(r, cr, logger, keycloakPod, &corev1.Pod{}, comparePods, checkPod); err != nil {
			return reconcile.Result{}, err
		}
	}

	appinitConfigMap := appinitConfigMap(cr)
	if err := ensureSame(r, cr, logger, appinitConfigMap, &corev1.ConfigMap{}, compareConfigMap, nocheck); err != nil {
		return reconcile.Result{}, err
	}
	appService := appService(cr, r)
	appRoute, err := appRoute(cr, r)
	if err != nil {
		return reconcile.Result{}, err
	}
	if err := ensureSame(r, cr, logger, appService, &corev1.Service{}, compareService, nocheck); err != nil {
		return reconcile.Result{}, err
	}
	var appPublicUrl string
	if isNodePort(r, cr.Spec.ServiceType) {
		nodePort, err := getNodePort(r, appService, logger)
		if err != nil {
			return reconcile.Result{}, err
		} else if nodePort == 0 {
			logger.Info("Waiting for app service node port to be assigned")
			updateStatus(r, cr, "Pending", "Waiting for service node port")
			return reconcile.Result{Requeue: true}, nil
		}
		appPublicUrl = fmt.Sprintf("https://%s:%d", cr.Spec.NodeHost, nodePort)
	} else {
		if cr.Spec.ServiceType == corev1.ServiceTypeLoadBalancer {
			appPublicUrl, err = getLoadBalancer(r, appService, logger)
			if err != nil {
				return reconcile.Result{}, err
			}
		} else if r.RoutesAvailable {
			foundRoute := &routev1.Route{}
			if err := ensureSame(r, cr, logger, appRoute, foundRoute, compareRoute, checkRoute); err != nil {
				return reconcile.Result{}, err
			}
			appPublicUrl = getRouteUrl(foundRoute)
		}
		if appPublicUrl == "" {
			updateStatus(r, cr, "Pending", "Waiting for Horreum service URL")
			logger.Info("Waiting for Horreum service URL to be assigned")
			return reconcile.Result{Requeue: true}, nil
		}
	}
	cr.Status.PublicUrl = appPublicUrl

	appServiceAccount := appServiceAccount(cr)
	if err := ensureSame(r, cr, logger, appServiceAccount, &corev1.ServiceAccount{}, nocompare, nocheck); err != nil {
		return reconcile.Result{}, err
	}
	// appClusterRole := appClusterRole(cr)
	// if err := ensureSame(r, cr, logger, appClusterRole, &rbacv1.ClusterRole{}, nocompare, nocheck); err != nil {
	// 	return reconcile.Result{}, err
	// }
	// appClusterRoleBinding := appClusterRoleBinding(cr)
	// if err := ensureSame(r, cr, logger, appClusterRoleBinding, &rbacv1.ClusterRoleBinding{}, nocompare, nocheck); err != nil {
	// 	return reconcile.Result{}, err
	// }
	appPod := appPod(cr, keycloakPublicUrl, appPublicUrl)
	if err := ensureSame(r, cr, logger, appPod, &corev1.Pod{}, comparePods, checkPod); err != nil {
		return reconcile.Result{}, err
	}

	uploadConfig := uploadConfig(cr)
	if err := ensureSame(r, cr, logger, uploadConfig, &corev1.ConfigMap{}, nocompare, nocheck); err != nil {
		return reconcile.Result{}, err
	}

	r.Status().Update(ctx, cr)

	return reconcile.Result{}, nil
}

type resource interface {
	metav1.Object
	runtime.Object
}

func ensureSame(r *HorreumReconciler, cr *hyperfoilv1alpha1.Horreum, logger logr.Logger,
	object resource, out client.Object,
	compare compareFunc, check checkFunc) error {
	// Set Hyperfoil instance as the owner and controller
	if err := controllerutil.SetControllerReference(cr, object, r.Scheme); err != nil {
		return err
	}

	kind := reflect.TypeOf(object).Elem().Name()
	// Check if this Pod already exists
	err := r.Get(context.TODO(), types.NamespacedName{Name: object.GetName(), Namespace: object.GetNamespace()}, out)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating a new "+kind, kind+".Namespace", object.GetNamespace(), kind+".Name", object.GetName())
		err = r.Create(context.TODO(), object)
		if err != nil {
			updateStatus(r, cr, "Error", "Cannot create "+kind+" "+object.GetName())
			return err
		}
		setStatus(r, cr, "Pending", "Creating "+kind+" "+object.GetName())
	} else if err != nil {
		updateStatus(r, cr, "Error", "Cannot find "+kind+" "+object.GetName())
		return err
	} else if compare(object, out, logger) {
		logger.Info(kind + " " + object.GetName() + " already exists and matches.")
		if ok, status, reason := check(out); !ok {
			setStatus(r, cr, status, kind+" "+object.GetName()+" "+reason)
		}
	} else {
		logger.Info(kind + " " + object.GetName() + " already exists but does not match. Deleting existing object.")
		if err = r.Delete(context.TODO(), out); err != nil {
			logger.Error(err, "Cannot delete "+kind+" "+object.GetName())
			updateStatus(r, cr, "Error", "Cannot delete "+kind+" "+object.GetName())
			return err
		}
		logger.Info("Creating a new " + kind)
		if err = r.Create(context.TODO(), object); err != nil {
			updateStatus(r, cr, "Error", "Cannot create "+kind+" "+object.GetName())
			return err
		}
		setStatus(r, cr, "Pending", "Creating "+kind+" "+object.GetName())
	}
	return nil
}

func ensureDeleted(r *HorreumReconciler, instance *hyperfoilv1alpha1.Horreum, object resource, out client.Object) error {
	kind := reflect.TypeOf(object).Elem().Name()
	err := r.Get(context.TODO(), types.NamespacedName{Name: object.GetName(), Namespace: object.GetNamespace()}, out)
	if err != nil && errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		updateStatus(r, instance, "Error", "Cannot find "+kind+" "+object.GetName())
		return err
	} else {
		if err = r.Delete(context.TODO(), out); err != nil {
			updateStatus(r, instance, "Error", "Cannot delete "+kind+" "+object.GetName())
			return err
		}
	}
	return nil
}

func isNodePort(r *HorreumReconciler, serviceType corev1.ServiceType) bool {
	return serviceType == corev1.ServiceTypeNodePort || serviceType == "" && !r.RoutesAvailable
}

func getNodePort(r *HorreumReconciler, service *corev1.Service, logger logr.Logger) (int32, error) {
	svc, err := getService(r, service, logger)
	if err == nil {
		return svc.Spec.Ports[0].NodePort, nil
	} else {
		return 0, err
	}
}

func getLoadBalancer(r *HorreumReconciler, service *corev1.Service, logger logr.Logger) (string, error) {
	svc, err := getService(r, service, logger)
	if err != nil {
		ingress := svc.Status.LoadBalancer.Ingress
		if len(ingress) == 0 {
			return "", nil
		} else {
			var port int32
			if ingress[0].Ports == nil || len(ingress[0].Ports) == 0 {
				port = svc.Spec.Ports[0].NodePort
			} else {
				port = ingress[0].Ports[0].Port
			}
			if ingress[0].Hostname != "" {
				return fmt.Sprintf("https://%s:%d", ingress[0].Hostname, port), nil
			} else if ingress[0].IP != "" {
				return fmt.Sprintf("https://%s:%d", ingress[0].IP, port), nil
			} else {
				return "", nil
			}
		}
	} else {
		return "", err
	}
}

func getService(r *HorreumReconciler, service *corev1.Service, logger logr.Logger) (*corev1.Service, error) {
	var foundService = &corev1.Service{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: service.Namespace, Name: service.Name}, foundService)
	if err != nil {
		logger.Error(err, "Cannot fetch current state of service "+service.Name)
		return nil, err
	}
	return foundService, nil
}

func getRouteUrl(route *routev1.Route) string {
	ingress := route.Status.Ingress
	if len(ingress) == 0 {
		return ""
	}
	if ingress[0].Host == "" {
		return ""
	}
	schema := ifThenElse(route.Spec.TLS != nil, "https", "http")
	// We will use the default port for given protocol
	return schema + "://" + ingress[0].Host
}

func setStatus(r *HorreumReconciler, instance *hyperfoilv1alpha1.Horreum, status string, reason string) {
	if instance.Status.Status == "Error" && status == "Pending" {
		return
	}
	instance.Status.Status = status
	instance.Status.Reason = reason
	instance.Status.LastUpdate = metav1.Now()
}

func updateStatus(r *HorreumReconciler, instance *hyperfoilv1alpha1.Horreum, status string, reason string) {
	setStatus(r, instance, status, reason)
	r.Status().Update(context.TODO(), instance)
}

func comparePods(i1 interface{}, i2 interface{}, logger logr.Logger) bool {
	p1, ok1 := i1.(*corev1.Pod)
	p2, ok2 := i2.(*corev1.Pod)
	if !ok1 || !ok2 {
		logger.Info("Cannot cast to Pods: " + fmt.Sprintf("%v | %v", i1, i2))
		return false
	}

	if equality.Semantic.DeepDerivative(p1.Spec, p2.Spec) {
		return true
	}

	diff := cmp.Diff(p1.Spec, p2.Spec)
	logger.Info("Pod " + p1.GetName() + " diff (-want,+got):\n" + diff)
	return false
}

func uploadConfig(cr *hyperfoilv1alpha1.Horreum) *corev1.ConfigMap {
	keycloakURL := keycloakInternalURL(cr)
	horreumURL := innerProtocol(cr.Spec.Route) + cr.Name + "." + cr.Namespace + `.svc`
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-hyperfoil-upload",
			Namespace: cr.Namespace,
		},
		Data: map[string]string{
			"50-upload-to-horreum": `
			#!/bin/bash

			TOKEN=$(curl -s -X POST	` + keycloakURL + `/auth/realms/horreum/protocol/openid-connect/token ` +
				` -H 'content-type: application/x-www-form-urlencoded' ` +
				` -d 'username='$HORREUM_USER'&password='$HORREUM_PASSWORD'&grant_type=password&client_id=horreum-ui'` +
				` | jq -r .access_token)

			curl -s	'` + horreumURL + `/api/run/data?owner='$HORREUM_GROUP'&access=PUBLIC&test=$.info.benchmark&start=$.info.startTime&stop=$.info.terminateTime'` +
				` -X POST -H 'content-type: application/json' -d @$RUN_DIR/all.json -H 'Authorization: Bearer '$TOKEN
			`,
		},
	}
}

func checkSecret(keys ...string) checkFunc {
	return func(obj interface{}) (bool, string, string) {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			return false, "Error", " is not a secret"
		}
		for _, key := range keys {
			if _, ok := secret.Data[key]; !ok {
				return false, "Error", " missing data " + key
			}
		}
		return true, "", ""
	}
}

func checkPod(i interface{}) (bool, string, string) {
	pod, ok := i.(*corev1.Pod)
	if !ok {
		return false, "Error", " is not a pod"
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
				return false, "Error", " cannot pull container image"
			}
		} else if cs.State.Terminated != nil {
			return false, "Pending", " has terminated container"
		}
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true, "", ""
		}
	}
	return false, "Pending", " is not ready"
}

func compareService(i1, i2 interface{}, logger logr.Logger) bool {
	s1, ok1 := i1.(*corev1.Service)
	s2, ok2 := i2.(*corev1.Service)
	if !ok1 || !ok2 {
		logger.Info("Cannot cast to Services: " + fmt.Sprintf("%v | %v", i1, i2))
		return false
	}
	if s1.Spec.Type != s2.Spec.Type {
		logger.Info("Type of services does not match: " + fmt.Sprintf("%v | %v", s1, s2))
		return false
	}
	if len(s1.Spec.Ports) != len(s2.Spec.Ports) {
		logger.Info("Number of ports does not match: " + fmt.Sprintf("%v | %v", s1, s2))
		return false
	}
	for i, p1 := range s1.Spec.Ports {
		p2 := s2.Spec.Ports[i]
		if p1.Port != p2.Port {
			logger.Info("Ports don't match: " + fmt.Sprintf("%v | %v", s1, s2))
			return false
		}
	}
	return true
}

func compareRoute(i1, i2 interface{}, logger logr.Logger) bool {
	r1, ok1 := i1.(*routev1.Route)
	r2, ok2 := i2.(*routev1.Route)
	if !ok1 || !ok2 {
		logger.Info("Cannot cast to Routes: " + fmt.Sprintf("%v | %v", i1, i2))
		return false
	}
	if r1.Spec.Host == "" {
		return r1.Spec.Subdomain == r2.Spec.Subdomain
	}
	if r1.Spec.Host != r2.Spec.Host {
		return false
	}
	if !reflect.DeepEqual(r1.Spec.TLS, r2.Spec.TLS) {
		logger.Info("TLS configuration does not match: " + fmt.Sprintf("%v | %v", r1.Spec.TLS, r2.Spec.TLS))
		return false
	}
	return true
}

func checkRoute(i interface{}) (bool, string, string) {
	route, ok := i.(*routev1.Route)
	if !ok {
		return false, "Error", " is not a route"
	}
	for _, ri := range route.Status.Ingress {
		for _, c := range ri.Conditions {
			if c.Type == routev1.RouteAdmitted {
				if c.Status == corev1.ConditionTrue {
					return true, "", ""
				}
				return false, "Error", " was not admitted"
			}
		}
	}
	return false, "Pending", " is in unknown state"
}

func compareConfigMap(i1, i2 interface{}, logger logr.Logger) bool {
	cm1, ok1 := i1.(*corev1.ConfigMap)
	cm2, ok2 := i2.(*corev1.ConfigMap)
	if !ok1 || !ok2 {
		logger.Info("Cannot cast to ConfigMaps: " + fmt.Sprintf("%v | %v", i1, i2))
		return false
	}
	if len(cm1.Data) != len(cm2.Data) {
		logger.Info("Different sizes: " + fmt.Sprintf("%d | %d", len(cm1.Data), len(cm2.Data)))
		return false
	}
	for key, val1 := range cm1.Data {
		if val2, ok := cm2.Data[key]; !ok || val1 != val2 {
			logger.Info("Key " + key + " differs: " + val1 + " | " + val2)
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *HorreumReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller := ctrl.NewControllerManagedBy(mgr).
		For(&hyperfoilv1alpha1.Horreum{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{})
	if r.RoutesAvailable {
		controller = controller.Owns(&routev1.Route{})
	}
	return controller.Complete(r)
}
