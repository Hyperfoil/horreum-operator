package horreum

import (
	hyperfoilv1alpha1 "github.com/Hyperfoil/horreum-operator/pkg/apis/hyperfoil/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func keycloakPod(cr *hyperfoilv1alpha1.Horreum) *corev1.Pod {
	initContainers := make([]corev1.Container, 0)
	if cr.Spec.Postgres.ExternalHost == "" {
		dbName := withDefault(cr.Spec.Keycloak.Database.Name, "keycloak")
		script := `
		    psql -c "SELECT 1;" || exit 1 # fail if connection does not work
			if psql -t -c "SELECT 1 FROM pg_roles WHERE rolname = '$(KEYCLOAK_USER)';" | grep -q 1; then
                echo "Database role $(KEYCLOAK_USER) already exists.";
			else
				psql -c "CREATE ROLE $(KEYCLOAK_USER) noinherit login password '$(KEYCLOAK_PASSWORD)';";
			fi
			if psql -t -c "SELECT 1 FROM pg_database WHERE datname = '` + dbName + `';" | grep -q 1; then
			    echo "Database "` + dbName + `" already exists.";
			else
				psql -c "CREATE DATABASE ` + dbName + ` WITH OWNER = '$(KEYCLOAK_USER)';";
			fi
		`
		initContainers = append(initContainers, corev1.Container{
			Name:    "init-db",
			Image:   dbImage(cr),
			Command: []string{"bash", "-x", "-c", script},
			Env: append(databaseAccessEnvVars(cr),
				secretEnv("KEYCLOAK_USER", keycloakDbSecret(cr), "user"),
				secretEnv("KEYCLOAK_PASSWORD", keycloakDbSecret(cr), "password"),
			),
		})
	}
	initContainers = append(initContainers, corev1.Container{
		Name:            "copy-imports",
		Image:           appImage(cr),
		ImagePullPolicy: corev1.PullAlways,
		Command: []string{
			"sh", "-x", "-c", `jq -r '.clients |= map(if .clientId | startswith("hyperfoil-repo") then ` +
				`(.rootUrl = "$(APP_URL)/") | (.adminUrl = "$(APP_URL)") | ` +
				`(.webOrigins = [ "$(APP_URL)" ]) | (.redirectUris = [ "$(APP_URL)/*"]) else . end)' ` +
				`/deployments/imports/keycloak-hyperfoil.json > /etc/keycloak/imports/keycloak-hyperfoil.json`,
		},
		Env: []corev1.EnvVar{
			corev1.EnvVar{
				Name: "APP_URL",
				// TODO: this won't work without route set
				Value: "http://" + withDefault(cr.Spec.Route, "must-set-route.io"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				Name:      "imports",
				MountPath: "/etc/keycloak/imports",
			},
		},
	})
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
			InitContainers: initContainers,
			Containers: []corev1.Container{
				corev1.Container{
					Name:  "keycloak",
					Image: withDefault(cr.Spec.Keycloak.Image, "docker.io/jboss/keycloak:latest"),
					Args: []string{
						"-Dkeycloak.profile.feature.upload_scripts=enabled",
						"-Dkeycloak.migration.action=import",
						"-Dkeycloak.migration.provider=singleFile",
						"-Dkeycloak.migration.file=/etc/keycloak/imports/keycloak-hyperfoil.json",
						"-Dkeycloak.migration.strategy=IGNORE_EXISTING",
					},
					Env: []corev1.EnvVar{
						secretEnv("KEYCLOAK_USER", keycloakAdminSecret(cr), "user"),
						secretEnv("KEYCLOAK_PASSWORD", keycloakAdminSecret(cr), "password"),
						corev1.EnvVar{
							Name:  "DB_VENDOR",
							Value: "postgres",
						},
						corev1.EnvVar{
							Name:  "DB_ADDR",
							Value: withDefault(cr.Spec.Keycloak.Database.Host, dbDefaultHost(cr)),
						},
						corev1.EnvVar{
							Name:  "DB_PORT",
							Value: withDefaultInt(cr.Spec.Keycloak.Database.Port, dbDefaultPort(cr)),
						},
						corev1.EnvVar{
							Name:  "DB_DATABASE",
							Value: withDefault(cr.Spec.Keycloak.Database.Name, "keycloak"),
						},
						secretEnv("DB_USER", keycloakDbSecret(cr), "user"),
						secretEnv("DB_PASSWORD", keycloakDbSecret(cr), "password"),
					},
					Ports: []corev1.ContainerPort{
						corev1.ContainerPort{
							Name:          "http",
							ContainerPort: 8080,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						corev1.VolumeMount{
							Name:      "imports",
							MountPath: "/etc/keycloak/imports",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				corev1.Volume{
					Name: "imports",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}
}

func keycloakService(cr *hyperfoilv1alpha1.Horreum) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-keycloak",
			Namespace: cr.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Name: "http",
					Port: int32(80),
					TargetPort: intstr.IntOrString{
						IntVal: 8080,
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

func keycloakRoute(cr *hyperfoilv1alpha1.Horreum) *routev1.Route {
	subdomain := ""
	if cr.Spec.Keycloak.Route == "" {
		subdomain = cr.Name + "-keycloak"
	}
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-keycloak",
			Namespace: cr.Namespace,
		},
		Spec: routev1.RouteSpec{
			Host:      cr.Spec.Keycloak.Route,
			Subdomain: subdomain,
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: cr.Name + "-keycloak",
			},
		},
	}
}