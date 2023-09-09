package horreum

import (
	hyperfoilv1alpha1 "github.com/Hyperfoil/horreum-operator/api/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	// rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func appinitConfigMap(cr *hyperfoilv1alpha1.Horreum) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-app-init",
			Namespace: cr.Namespace,
			Labels: map[string]string{
				"app": cr.Name,
			},
		},
		Data: map[string]string{
			// Note: 0.7.11 of Horreum used here still has grafana in the k8s-setup.sh script
			"app_init.sh": `
			    echo 'Injecting service-ca certificate ...'
				keytool -noprompt -import -alias service-ca -file /etc/ssl/certs/service-ca.crt -cacerts -storepass changeit
				until cat /deployments/k8s-setup.sh | sed '/grafana/d' | sh -x
				do
			  		echo 'Re-trying init ...'
			  		sleep 10
				done
			`,
		},
	}
}

func appPod(cr *hyperfoilv1alpha1.Horreum, keycloakPublicUrl, appPublicUrl string) *corev1.Pod {
	keycloakInternalURL := keycloakInternalURL(cr)

	horreumEnv := []corev1.EnvVar{
		{
			Name:  "QUARKUS_DATASOURCE_JDBC_URL",
			Value: dbURL(cr, &cr.Spec.Database, "horreum"),
		},
		secretEnv("QUARKUS_DATASOURCE_USERNAME", appUserSecret(cr), corev1.BasicAuthUsernameKey),
		secretEnv("QUARKUS_DATASOURCE_PASSWORD", appUserSecret(cr), corev1.BasicAuthPasswordKey),
		{
			Name:  "QUARKUS_DATASOURCE_MIGRATION_JDBC_URL",
			Value: dbURL(cr, &cr.Spec.Database, "horreum"),
		},
		secretEnv("QUARKUS_DATASOURCE_MIGRATION_USERNAME", dbAdminSecret(cr), corev1.BasicAuthUsernameKey),
		secretEnv("QUARKUS_DATASOURCE_MIGRATION_PASSWORD", dbAdminSecret(cr), corev1.BasicAuthPasswordKey),
		secretEnv("HORREUM_DB_SECRET", appUserSecret(cr), "dbsecret"),
		{
			Name:  "QUARKUS_OIDC_AUTH_SERVER_URL",
			Value: keycloakInternalURL + "/realms/horreum",
		},
		{
			Name:  "QUARKUS_OIDC_TOKEN_ISSUER",
			Value: keycloakPublicUrl + "/realms/horreum",
		},
		{
			// TODO: it's not possible to set up custom CA for OIDC
			// https://github.com/quarkusio/quarkus/issues/18002
			Name:  "QUARKUS_OIDC_TLS_VERIFICATION",
			Value: "none",
		},
		{
			Name:  "HORREUM_URL",
			Value: appPublicUrl,
		},
		{
			Name:  "HORREUM_INTERNAL_URL",
			Value: innerProtocol(cr.Spec.Route) + cr.Name + "." + cr.Namespace + ".svc",
		},
		{
			Name:  "HORREUM_KEYCLOAK_URL",
			Value: keycloakPublicUrl + "/",
		},
	}
	if javaOptions, ok := cr.ObjectMeta.Annotations["java-options"]; ok {
		horreumEnv = append(horreumEnv, corev1.EnvVar{
			Name:  "JAVA_OPTIONS",
			Value: javaOptions,
		})
	}
	volumes := []corev1.Volume{
		{
			Name: "app-init",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cr.Name + "-app-init",
					},
					DefaultMode: func(i int32) *int32 { return &i }(0555),
				},
			},
		},
		{
			Name: "imports",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "service-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "service-ca.crt",
					},
				},
			},
		},
	}
	mounts := []corev1.VolumeMount{
		{
			Name:      "imports",
			MountPath: "/etc/horreum/imports",
		},
	}
	routeType := cr.Spec.Route.Type
	if routeType == "passthrough" || routeType == "reencrypt" || routeType == "" {
		secretName := cr.Name + "-app-certs"
		if cr.Spec.Route.Type == "passthrough" {
			secretName = cr.Spec.Route.TLS
		}
		volumes = append(volumes, corev1.Volume{
			Name: "certs",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "certs",
			MountPath: "/opt/certs",
		}, corev1.VolumeMount{
			Name:      "service-ca",
			MountPath: "/etc/ssl/certs/service-ca.crt",
			SubPath:   "service-ca.crt",
		})
		horreumEnv = append(horreumEnv, corev1.EnvVar{
			Name:  "QUARKUS_HTTP_SSL_CERTIFICATE_FILE",
			Value: "/opt/certs/" + corev1.TLSCertKey,
		}, corev1.EnvVar{
			Name:  "QUARKUS_HTTP_SSL_CERTIFICATE_KEY_FILE",
			Value: "/opt/certs/" + corev1.TLSPrivateKeyKey,
		})
	}
	caCertArg := ""
	if routeType == "reencrypt" || routeType == "" {
		caCertArg = "--cacert /etc/ssl/certs/service-ca.crt"
	}
	// userId := int64(0)
	return &corev1.Pod{

		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-app",
			Namespace: cr.Namespace,
			Labels: map[string]string{
				"app":     cr.Name,
				"service": "app",
			},
		},
		Spec: corev1.PodSpec{
			// ServiceAccountName:            "horreum-init",
			TerminationGracePeriodSeconds: &[]int64{0}[0],
			InitContainers: []corev1.Container{
				{
					Name:            "init",
					Image:           appImage(cr),
					ImagePullPolicy: corev1.PullAlways,
					// SecurityContext: &corev1.SecurityContext{
					// 	RunAsUser: &[]int64{userId}[0],
					// },
					Command: []string{
						"sh", "-x", "-c", "/deployments/app_init.sh;",
					},
					Env: []corev1.EnvVar{
						secretEnv("KEYCLOAK_USER", keycloakAdminSecret(cr), corev1.BasicAuthUsernameKey),
						secretEnv("KEYCLOAK_PASSWORD", keycloakAdminSecret(cr), corev1.BasicAuthPasswordKey),
						secretEnv("ADMIN_USERNAME", horreumAdminSecret(cr), corev1.BasicAuthUsernameKey),
						secretEnv("ADMIN_PASSWORD", horreumAdminSecret(cr), corev1.BasicAuthPasswordKey),
						{
							Name:  "KC_URL",
							Value: keycloakInternalURL,
						},
						{
							Name:  "CA_CERT_ARG",
							Value: caCertArg,
						},
						{
							Name:  "APP_URL",
							Value: appPublicUrl,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "app-init",
							MountPath: "/deployments/app_init.sh",
							SubPath:   "app_init.sh",
						},
						{
							Name:      "imports",
							MountPath: "/etc/horreum/imports",
						},
						{
							Name:      "service-ca",
							MountPath: "/etc/ssl/certs/service-ca.crt",
							SubPath:   "service-ca.crt",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "horreum",
					Image: appImage(cr),
					Command: []string{
						"sh", "-c", `
							export QUARKUS_OIDC_CREDENTIALS_SECRET=$$(cat /etc/horreum/imports/clientsecret)
							/deployments/horreum.sh
						`,
					},
					Env:          horreumEnv,
					VolumeMounts: mounts,
				},
			},
			Volumes: volumes,
		},
	}
}

func appService(cr *hyperfoilv1alpha1.Horreum, r *HorreumReconciler) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Annotations: map[string]string{
				"service.beta.openshift.io/serving-cert-secret-name": cr.Name + "-app-certs",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: serviceType(cr.Spec.ServiceType, r),
			Ports: []corev1.ServicePort{
				servicePort(cr.Spec.Route, 8080, 8443),
			},
			Selector: map[string]string{
				"app":     cr.Name,
				"service": "app",
			},
		},
	}
}

func appRoute(cr *hyperfoilv1alpha1.Horreum, r *HorreumReconciler) (*routev1.Route, error) {
	return route(cr.Spec.Route, "", cr, r)
}

func appServiceAccount(cr *hyperfoilv1alpha1.Horreum) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "horreum-init",
			Namespace: cr.Namespace,
		},
	}
}

// func appClusterRole(cr *hyperfoilv1alpha1.Horreum) *rbacv1.ClusterRole {
// 	return &rbacv1.ClusterRole{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      "horreum-init-cluster-role",
// 		},
// 		Rules: []rbacv1.PolicyRule{
// 			{
// 				APIGroups: []string{
// 					"security.openshift.io",
// 				},
// 				ResourceNames: []string{
// 					"anyuid",
// 				},
// 				Resources: []string{
// 					"securitycontextconstraints",
// 				},
// 				Verbs: []string{
// 					"use",
// 				},
// 			},
// 		},
// 	}
// }

// func appClusterRoleBinding(cr *hyperfoilv1alpha1.Horreum) *rbacv1.ClusterRoleBinding {
// 	return &rbacv1.ClusterRoleBinding{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: "horreum-init-cluster-role-binding",
// 		},
// 		RoleRef: rbacv1.RoleRef{
// 			APIGroup: "rbac.authorization.k8s.io",
// 			Kind:     "ClusterRole",
// 			Name:     "horreum-init-cluster-role",
// 		},
// 		Subjects: []rbacv1.Subject{
// 			{
// 				Kind:      "ServiceAccount",
// 				Name:      "horreum-init",
// 				Namespace: cr.Namespace,
// 			},
// 		},
// 	}
// }
