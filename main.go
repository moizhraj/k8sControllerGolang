package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	v1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/util/runtime"
	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const (
	RebootAnnotation           = "reboot-agent.v1.sdlt.local/reboot"
	RebootNeededAnnotation     = "reboot-agent.v1.sdlt.local/reboot-needed"
	RebootInProgressAnnotation = "reboot-agent.v1.sdlt.local/reboot-in-progress"
)

func main() {
	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	} else {
		kubeconfig = ""
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Failed to build config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	// Create a shared informer factory and use it to create a node informer
	factory := informers.NewSharedInformerFactory(clientset, time.Second*10)
	nodeInformer := factory.Core().V1().Nodes().Informer()
	podInformer := factory.Core().V1().Pods().Informer()

	// Define event handlers for pod informer
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*v1.Pod)
			fmt.Printf("Pod added: %s\n", pod.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod := oldObj.(*v1.Pod)
			newPod := newObj.(*v1.Pod)
			fmt.Printf("Pod updated: %s\n", newPod.Name)

			// Check for changes in annotations
			if !equalAnnotations(oldPod.Annotations, newPod.Annotations) {
				fmt.Printf("Annotations updated on pod %s: %v\n", newPod.Name, newPod.Annotations)

				// Handle specific annotations
				handlePodAnnotations(newPod, clientset)
			}
		},
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*v1.Pod)
			fmt.Printf("Pod deleted: %s\n", pod.Name)
		},
	})

	// Define event handlers for node informer
	// nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
	// 	AddFunc: func(obj interface{}) {
	// 		node := obj.(*v1.Node)
	// 		fmt.Printf("Node added: %s\n", node.Name)
	// 	},
	// 	UpdateFunc: func(oldObj, newObj interface{}) {
	// 		oldNode := oldObj.(*v1.Node)
	// 		newNode := newObj.(*v1.Node)
	// 		fmt.Printf("Node updated: %s\n", newNode.Name)

	// 		// Check for changes in annotations
	// 		if !equalAnnotations(oldNode.Annotations, newNode.Annotations) {
	// 			fmt.Printf("Annotations updated on node %s: %v\n", newNode.Name, newNode.Annotations)

	// 			// Handle specific annotations
	// 			handleNodeAnnotations(clientset, newNode)
	// 		}
	// 	},
	// 	DeleteFunc: func(obj interface{}) {
	// 		node := obj.(*v1.Node)
	// 		fmt.Printf("Node deleted: %s\n", node.Name)
	// 	},
	// })

	// Start the informer
	factory.Start(stopCh)

	// Wait for all caches to sync
	if ok := cache.WaitForCacheSync(stopCh, nodeInformer.HasSynced); !ok {
		log.Fatalf("Failed to wait for caches to sync")
	}

	// Run forever
	<-stopCh
}

// Helper function to compare annotations
func equalAnnotations(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// Handle specific annotations
func handleNodeAnnotations(client *kubernetes.Clientset, node *v1.Node) {

	if shouldReboot(node) {
		// Set "reboot in progress" and clear reboot needed / reboot
		node.Annotations[RebootInProgressAnnotation] = ""
		delete(node.Annotations, RebootNeededAnnotation)
		delete(node.Annotations, RebootAnnotation)

		// Update the node object
		_, err := client.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
		if err != nil {
			glog.Errorf("Failed to set %s annotation: %v", RebootInProgressAnnotation, err)
			return // If we cannot update the state - do not reboot
		}
	}

	// Reboot complete - clear the rebootInProgress annotation
	// This is a niave assumption: the call to reboot is blocking - if we've reached this, assume the node has restarted.
	if rebootInProgress(node) {
		glog.Info("Clearing in-progress reboot annotation")
		delete(node.Annotations, RebootInProgressAnnotation)
		_, err := client.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
		if err != nil {
			glog.Errorf("Failed to remove %s annotation: %v", RebootInProgressAnnotation, err)
			return
		}
	}

	// annotations := node.Annotations
	// if annotations == nil {
	// 	return
	// }

	// if _, exists := annotations[RebootAnnotation]; exists {
	// 	fmt.Printf("Reboot annotation found on node %s. Initiating reboot.\n", node.Name)
	// 	initiateReboot(node.Name)
	// } else if _, exists := annotations[RebootNeededAnnotation]; exists {
	// 	fmt.Printf("Reboot needed annotation found on node %s.\n", node.Name)
	// } else if _, exists := annotations[RebootInProgressAnnotation]; exists {
	// 	fmt.Printf("Reboot in progress annotation found on node %s.\n", node.Name)
	// }
}

func shouldReboot(node *v1.Node) bool {
	_, reboot := node.Annotations[RebootAnnotation]
	_, inProgress := node.Annotations[RebootInProgressAnnotation]

	return reboot && !inProgress
}

func rebootInProgress(node *v1.Node) bool {
	_, inProgress := node.Annotations[RebootInProgressAnnotation]
	return inProgress
}

// Function to initiate a reboot on the node
func initiateReboot(nodeName string) {
	fmt.Printf("Rebooting node %s\n", nodeName)
	// This is a simple example of how you might initiate a reboot. In a real-world scenario, you would
	// need to securely communicate with the node to initiate the reboot.
	// cmd := exec.Command("ssh", nodeName, "sudo", "reboot")
	// if err := cmd.Run(); err != nil {
	//     fmt.Printf("Failed to reboot node %s: %v\n", nodeName, err)
	// } else {
	//     fmt.Printf("Node %s is rebooting.\n", nodeName)
	// }
}

// Handle specific annotations
func handlePodAnnotations(pod *v1.Pod, clientset *kubernetes.Clientset) {
	annotations := pod.Annotations
	if annotations == nil {
		return
	}

	if _, exists := annotations[RebootAnnotation]; exists {
		fmt.Printf("Reboot annotation found on pod %s. Restarting deployment.\n", pod.Name)
		restartDeployment(pod, clientset)
	} else if _, exists := annotations[RebootNeededAnnotation]; exists {
		fmt.Printf("Reboot needed annotation found on pod %s.\n", pod.Name)
	} else if _, exists := annotations[RebootInProgressAnnotation]; exists {
		fmt.Printf("Reboot in progress annotation found on pod %s.\n", pod.Name)
	}
}

// Function to restart the deployment of the pod
func restartDeployment(pod *v1.Pod, clientset *kubernetes.Clientset) {
	// Find the owner reference for the pod's deployment
	for _, ownerRef := range pod.OwnerReferences {
		if ownerRef.Kind == "ReplicaSet" {
			replicaSet, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(context.TODO(), ownerRef.Name, metav1.GetOptions{})
			if err != nil {
				fmt.Printf("Failed to get replicaset: %v\n", err)
				return
			}
			for _, ownerRef := range replicaSet.OwnerReferences {
				if ownerRef.Kind == "Deployment" {
					deployment, err := clientset.AppsV1().Deployments(pod.Namespace).Get(context.TODO(), ownerRef.Name, metav1.GetOptions{})
					if err != nil {
						fmt.Printf("Failed to get deployment: %v\n", err)
						return
					}
					// Initialize the annotations map if it's nil
					if deployment.Spec.Template.Annotations == nil {
						deployment.Spec.Template.Annotations = make(map[string]string)
					}
					// Patch the deployment to trigger a restart
					deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
					_, err = clientset.AppsV1().Deployments(pod.Namespace).Update(context.TODO(), deployment, metav1.UpdateOptions{})
					if err != nil {
						fmt.Printf("Failed to update deployment: %v\n", err)
					} else {
						fmt.Printf("Deployment %s restarted.\n", deployment.Name)
					}
				}
			}
		}
	}
}
