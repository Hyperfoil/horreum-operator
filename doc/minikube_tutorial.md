# Install Horreum on MiniKube

This tutorial provides step-by-step instructions on how to install Horreum into minikube using this operator.

## Install minikube

Follow [the official minikube documentation](https://minikube.sigs.k8s.io/docs/start/) to install minikube on your system. 

Avoid the `QEMU` driver on Linux (the default) as it lacks the proper network support. See [the list of available drivers](https://minikube.sigs.k8s.io/docs/drivers/) and instructions on how to select them.

Suggestion: if there are permission issues when starting the cluster, set `MINIKUBE_HOME` to a custom location.
                                
## Start the cluster

Test the cluster installation by starting it:
```
minikube start
```
This take a little while. After that confirm the installation with:
```
minikube status
```
You should see something like: 
```
host: Running
kubelet: Running
apiserver: Running
kubeconfig: Configured
```
Register the cluster IP that you get from:
```
minikube ip
```
            
#### Optional: dashboard addon

Minikube dashboard eases the process of managing and monitoring the cluster. On a different terminal, enable the addon:
```
minikube addons enable dashboard
```
Start the dashboard with:
```
minikube dashboard
```
This will open the dashboard in the browser.

## Get Horreum-operator   
        
Checkout the source code:
```
git clone https://github.com/Hyperfoil/horreum-operator.git
```
Build the operator (Go 1.19 required):
```
make build 
```
Install Horreum CRD (Custom Resource Definition) into the cluster:
```
make install
```
This will make possible to define Horreum resources that the operator will then manage. 
  
## The Horreum resource 

There is an example resource in [config/samples/_v1alpha1_horreum.yaml](config/samples/_v1alpha1_horreum.yaml) that you can edit.

Change the `nodeHost` attribute to the address of the cluster (obtained from `minikube ip`).
Optionally, change the name of the service. From here on, the examples will stick with `horreum` as the service name.
To use a custom namespace, you need to define it the resource as well. 

Deploy the example with:
```
make deploy-samples
```

## Run the Horreum operator
            
In a different terminal, run:
```
make run
```
The operator will start Horreum. 

## Access Horreum
         
The following command will open Horreum in your browser:
```
minikube service horreum
```

## Login

The operator generates credentials for the all the necessary services. You can view the credentials for the `horreum-admin` user in the dashboard, on the `Secrets` tab. 
Other option to see the secret is from command line:
```
kubectl get secret horreum-admin -o json | jq '{ username: .data.username | @base64d, password: .data.password | @base64d }'
```
Similarly, the credentials for keycloak are `horreum-keycloak-admin` and can be obtained in the same way.
