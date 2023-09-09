package horreum

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	hyperfoilv1alpha1 "github.com/Hyperfoil/horreum-operator/api/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func keycloakConfigMap(cr *hyperfoilv1alpha1.Horreum) *corev1.ConfigMap {
	req, err := http.NewRequest("GET", "https://raw.githubusercontent.com/Hyperfoil/Horreum/0.7.11/infra/keycloak-horreum.json", nil)
	if err != nil {
		return nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}

	defer resp.Body.Close()

	horreumRealm, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-keycloak-horreum-realm",
			Namespace: cr.Namespace,
			Labels: map[string]string{
				"app": cr.Name,
			},
		},
		Data: map[string]string{
			"keycloak-horreum.json": string(horreumRealm),
		},
	}
}

func keycloakPod(cr *hyperfoilv1alpha1.Horreum, keycloakPublicUrl string) *corev1.Pod {
	secretName := cr.Name + "-keycloak-certs"
	if cr.Spec.Keycloak.Route.Type == "passthrough" {
		secretName = cr.Spec.Keycloak.Route.TLS
	}
	publicUrl, err := url.ParseRequestURI(keycloakPublicUrl)
	if err != nil {
		return nil
	}
	// TODO: setup X509_CA_BUNDLE
	volumes := []corev1.Volume{
		{
			Name: "certs",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		},
		{
			Name: "keycloak-horreum-realm",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cr.Name + "-keycloak-horreum-realm",
					},
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "certs",
			MountPath: "/etc/x509/https",
		},
		{
			Name:      "keycloak-horreum-realm",
			MountPath: "/opt/keycloak/data/import",
		},
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-keycloak",
			Namespace: cr.Namespace,
			Labels: map[string]string{
				"app":     cr.Name,
				"service": "keycloak",
			},
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &[]int64{0}[0],
			InitContainers: []corev1.Container{
				{
					Name:            "init",
					Image:           withDefault(cr.Spec.Keycloak.Image, "quay.io/keycloak/keycloak:latest"),
					ImagePullPolicy: corev1.PullAlways,
					Args: []string{
						"build",
					},
					Env: []corev1.EnvVar{
						secretEnv("KEYCLOAK_ADMIN", keycloakAdminSecret(cr), corev1.BasicAuthUsernameKey),
						secretEnv("KEYCLOAK_ADMIN_PASSWORD", keycloakAdminSecret(cr), corev1.BasicAuthPasswordKey),
						{
							Name:  "DB_ADDR",
							Value: withDefault(cr.Spec.Keycloak.Database.Host, dbDefaultHost(cr)),
						},
						{
							Name:  "DB_PORT",
							Value: withDefaultInt(cr.Spec.Keycloak.Database.Port, 5432),
						},
						{
							Name:  "DB_DATABASE",
							Value: withDefault(cr.Spec.Keycloak.Database.Name, "keycloak"),
						},
						// For simplicity of development the image has HTTP enabled, which is not suitable for production
						{
							Name:  "KC_HTTP_ENABLED",
							Value: "false",
						},
						{
							Name:  "KC_HTTPS_PORT",
							Value: "8443",
						},
						{
							Name:  "KC_HTTPS_CERTIFICATE_FILE",
							Value: "/etc/x509/https/tls.crt",
						},
						{
							Name:  "KC_HTTPS_CERTIFICATE_KEY_FILE",
							Value: "/etc/x509/https/tls.key",
						},
						{
							Name:  "KC_HOSTNAME",
							Value: publicUrl.Host,
						},
						{
							Name:  "KC_PROXY",
							Value: "passthrough", // TODO at least for NodePort?
						},
						secretEnv("KC_DB_USERNAME", keycloakDbSecret(cr), corev1.BasicAuthUsernameKey),
						secretEnv("KC_DB_PASSWORD", keycloakDbSecret(cr), corev1.BasicAuthPasswordKey),
					},
					VolumeMounts: volumeMounts,
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "keycloak",
					Image: withDefault(cr.Spec.Keycloak.Image, "quay.io/keycloak/keycloak:latest"),
					Args: []string{
						"start", "--optimized", "--import-realm",
					},
					Env: []corev1.EnvVar{
						secretEnv("KEYCLOAK_ADMIN", keycloakAdminSecret(cr), corev1.BasicAuthUsernameKey),
						secretEnv("KEYCLOAK_ADMIN_PASSWORD", keycloakAdminSecret(cr), corev1.BasicAuthPasswordKey),
						{
							Name:  "DB_ADDR",
							Value: withDefault(cr.Spec.Keycloak.Database.Host, dbDefaultHost(cr)),
						},
						{
							Name:  "DB_PORT",
							Value: withDefaultInt(cr.Spec.Keycloak.Database.Port, 5432),
						},
						{
							Name:  "DB_DATABASE",
							Value: withDefault(cr.Spec.Keycloak.Database.Name, "keycloak"),
						},
						// For simplicity of development the image has HTTP enabled, which is not suitable for production
						{
							Name:  "KC_HTTP_ENABLED",
							Value: "false",
						},
						{
							Name:  "KC_HTTPS_PORT",
							Value: "8443",
						},
						{
							Name:  "KC_HTTPS_CERTIFICATE_FILE",
							Value: "/etc/x509/https/tls.crt",
						},
						{
							Name:  "KC_HTTPS_CERTIFICATE_KEY_FILE",
							Value: "/etc/x509/https/tls.key",
						},
						{
							Name:  "KC_HOSTNAME",
							Value: publicUrl.Host,
						},
						{
							Name:  "KC_PROXY",
							Value: "passthrough", // TODO at least for NodePort?
						},
						secretEnv("KC_DB_USERNAME", keycloakDbSecret(cr), corev1.BasicAuthUsernameKey),
						secretEnv("KC_DB_PASSWORD", keycloakDbSecret(cr), corev1.BasicAuthPasswordKey),
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "https",
							ContainerPort: 8443,
						},
					},
					VolumeMounts: volumeMounts,
				},
			},
			Volumes: volumes,
		},
	}
}

func keycloakService(cr *hyperfoilv1alpha1.Horreum, r *HorreumReconciler) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-keycloak",
			Namespace: cr.Namespace,
			Annotations: map[string]string{
				"service.beta.openshift.io/serving-cert-secret-name": cr.Name + "-keycloak-certs",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: serviceType(cr.Spec.Keycloak.ServiceType, r),
			Ports: []corev1.ServicePort{
				{
					Name: "https",
					Port: int32(443),
					TargetPort: intstr.IntOrString{
						IntVal: 8443,
					},
				},
			},
			Selector: map[string]string{
				"app":     cr.Name,
				"service": "keycloak",
			},
		},
	}
}

func keycloakRoute(cr *hyperfoilv1alpha1.Horreum, r *HorreumReconciler) (*routev1.Route, error) {
	routeType := cr.Spec.Keycloak.Route.Type
	if routeType != "passthrough" && routeType != "reencrypt" && routeType != "" {
		return nil, errors.New("keycloak supports only TLS-encrypted routes")
	}
	return route(cr.Spec.Keycloak.Route, "-keycloak", cr, r)
}
