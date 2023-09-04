# Horreum Operator

This operator installs [Horreum](https://github.com/Hyperfoil/Horreum) and services it depends on (PostgreSQL database and Keycloak SSO)
   
## Deploy the operator in minikube

First start by [installing minikube](https://minikube.sigs.k8s.io/docs/start/). Start the cluster with `minikube start`. (*Suggestion:* use `minikube dashboard` to monitor the cluster) (*Note:* The QEMU driver on linux will not allow external access to the services in the cluster)

Build and install Hyperfoil operator (Go 1.19 is required) with `make build install`.

Deploy the example resource in [config/samples/_v1alpha1_horreum.yaml](config/samples/_v1alpha1_horreum.yaml) in the cluster with `make deploy-samples`

Run the operator with `make run`. Once the horreum has started the operator can be stopped, as it only reacts to changes.

Horreum should be running by now. The services can be accessed using the URLs at `minikube service --all --url`. (*Note:* It may be necessary to reconfigure the running Horreum custom resource with the external URLs to the services)

### Undeploy 

Undeploy samples from cluster `make undeploy-samples` (*Note:* this does not require the operator to be running)

Stop the minikube cluster with `minikube stop`. Optionally delete the cluster with `minikube delete --all`
     
## Configuration

For detailed description of all properties [refer to the CRD](config/crd/bases/hyperfoil.io_horreums.yaml).

When using persistent volumes make sure that the access rights are set correctly and the pods have write access; in particular the PostgreSQL database requires that the mapped directory is owned by user with id `999`.

If you're planning to use secured routes (edge termination) it is recommended to set the `tls: my-tls-secret` at the first deploy; otherwise it is necessary to update URLs for clients `horreum` and `horreum-ui` in Keycloak manually. Also the Horreum pod needs to be restarted after keycloak route update.

Currently you must set both Horreum and Keycloak route host explicitly, otherwise you could not log in (TODO).

When the `horreum` resource gets ready, login into Keycloak using administrator credentials (these are automatically created if you don't specify existing secret) and create a new user in the `horreum` realm, a new team role (with `-team` suffix) and assign it to the user along with other appropriate predefined roles. Administrator credentials can be found using this:

```sh
NAME=$(oc get horreum -o jsonpath='{$.items[0].metadata.name}')
oc get secret $NAME-keycloak-admin -o json | \
    jq '{ user: .data.user | @base64d, password: .data.password | @base64d }'
```

For details of roles in Horreum please refer to [its documentation](https://horreum.hyperfoil.io/)

## Hyperfoil integration

For your convenience this operator creates also a config map (`*-hyperfoil-upload`) that can be used in [Hyperfoil resource](https://github.com/Hyperfoil/hyperfoil-operator) to upload Hyperfoil results to this instance - you can use it directly or merge that into another config map you use for post-hooks. However, it is necessary to define & mount a secret with these keys:

```sh
# Credentials of the user you've created in Keycloak
HORREUM_USER=user
HORREUM_PASSWORD=password
# Role for the team the user belongs to (something you've created)
HORREUM_GROUP=engineers-team

oc create secret generic hyperfoil-horreum \
    --from-literal=HORREUM_USER=$HORREUM_USER \
    --from-literal=HORREUM_PASSWORD=$HORREUM_PASSWORD \
    --from-literal=HORREUM_GROUP=$HORREUM_GROUP \
```

Then set it up in the `hyperfoil` resource:

```yaml
apiVersion: hyperfoil.io/v1alpha1
kind: Hyperfoil
metadata:
  name: example-hyperfoil
  namespace: hyperfoil
spec:
  # ...
  postHooks: example-horreum-hyperfoil-upload
  secretEnvVars:
  - hyperfoil-horreum
```

This operator automatically inserts a webhook to convert test results into Hyperfoil report; In order to link from test to report you have to add a schema (matching the URI used in your Hyperfoil version, usually something like `http://hyperfoil.io/run-schema/0.8` and add it an extractor `info` with JSON path `$.info`. Subsequently go to the test and add a view component with header 'Report', accessor you've created in the previous step and this rendering script (replacing the hostname):

```js
(value, all) => {
  let info = JSON.parse(value);
  return '<a href="http://example-horreum-report-hyperfoil.apps.mycloud.example.com/' + all.id + '-' + info.id +'-' + info.benchmark + '.html" target=_blank>Show</a>'
}
```