package alcorset

import (
	"context"
	"fmt"
	"log"

	alcorv1alpha1 "github.com/onionpiece/alcorset/pkg/apis/alcor/v1alpha1"
	ipclaim "github.com/onionpiece/ipclaim/pkg/apis/alcor/v1alpha1"
	vpcipclaim "github.com/onionpiece/vpcipclaim/pkg/apis/alcor/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Add creates a new AlcorSet Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileAlcorSet{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("alcorset-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource AlcorSet
	err = c.Watch(&source.Kind{Type: &alcorv1alpha1.AlcorSet{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for pods, since they are subresources
	if err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &alcorv1alpha1.AlcorSet{},
	}); err != nil {
		return err
	}

	// Watch for vpcipclaims, since they are subresources
	if err = c.Watch(&source.Kind{Type: &vpcipclaim.VPCIPClaim{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &alcorv1alpha1.AlcorSet{},
	}); err != nil {
		return err
	}

	// Watch for ipclaims, since they are subresources
	if err = c.Watch(&source.Kind{Type: &ipclaim.IPClaim{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &alcorv1alpha1.AlcorSet{},
	}); err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileAlcorSet implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileAlcorSet{}

// ReconcileAlcorSet reconciles a AlcorSet object
type ReconcileAlcorSet struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a AlcorSet object and makes changes based on the state read
// and what is in the AlcorSet.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileAlcorSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	log.Printf("Reconciling AlcorSet for %v", request.NamespacedName)

	als := &alcorv1alpha1.AlcorSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, als)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Print("Request object not found, could have been deleted after reconcile request.")
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if als.GetDeletionTimestamp() != nil {
		log.Print("AlcorSet has been marked as deleted, do cleanup")
		/*
		 *	NOTE: consider delete pod before deleting IPClaim or VPCIPClaim created by AlcorSet
		 *	For example, if VPCIPClaim get deleted before pod with big terminationGracePeriodSeconds,
		 *	VPC IP will get freed before Pod deleted, and this make it possible that another
		 *	new created VPCIPClaim can get this IP and make this IP used by another Pod.
		 *	In such a case, in cluster, there will be two Pods with the same IP.
		 *	It will be harmful to IPClaim scenario, and VPCIPClaim(specially for case pods
		 *	communicating on the same node)
		 */
		pods, err := r.getPods(als)
		if err != nil {
			// Sequence case ...
			if err.Error() == PodsRaisingPhase {
				log.Print("Waiting pod raise up")
				return reconcile.Result{}, nil
			} else if err.Error() == PodsFallingPhase {
				log.Print("Waiting pod tear down")
				return reconcile.Result{Requeue: true}, nil
			}
			log.Print(err, "Failed to get pods")
			return reconcile.Result{}, err
		}
		if len(pods.Items) > 0 {
			err := r.tearDownPods(als, pods, true)
			return reconcile.Result{}, err
		}

		if contains(als.GetFinalizers(), FinalizerIPClaim) {
			if len(als.Status.ClaimedIPs) > 0 {
				if err := r.deleteIPClaims(als); err != nil {
					return reconcile.Result{}, err
				}
			}
			if err := r.removeFinalizer(als, FinalizerIPClaim); err != nil {
				return reconcile.Result{}, fmt.Errorf("Failed to remove finalizer %s, found error: %v", FinalizerIPClaim, err)
			}
		} else if contains(als.GetFinalizers(), FinalizerVPCIPClaim) {
			if len(als.Status.ClaimedIPs) > 0 {
				if err := r.deleteVPCIPClaims(als); err != nil {
					return reconcile.Result{}, err
				}
			}
			if err := r.removeFinalizer(als, FinalizerVPCIPClaim); err != nil {
				return reconcile.Result{}, fmt.Errorf("Failed to remove finalizer %s, found error: %v", FinalizerVPCIPClaim, err)
			}
		}
		// it's safe to exit, either no finalizers, or all subresources are deleted sucessfully on api
		return reconcile.Result{}, nil
	}

	finsToAdd := []string{}
	if als.Spec.OnVPC {
		if !contains(als.GetFinalizers(), FinalizerVPCIPClaim) {
			finsToAdd = append(finsToAdd, FinalizerVPCIPClaim)
		}
	} else {
		if !contains(als.GetFinalizers(), FinalizerIPClaim) {
			finsToAdd = append(finsToAdd, FinalizerIPClaim)
		}
	}
	if len(finsToAdd) != 0 {
		if err := r.addFinalizers(als, finsToAdd); err != nil {
			return reconcile.Result{}, fmt.Errorf("Failed to add finalizers %v, found error: %v", finsToAdd, err)
		}
	}

	// init status
	if err := r.initStatus(als); err != nil {
		return reconcile.Result{}, err
	}

	pods, err := r.getPods(als)
	if err != nil {
		// Sequence case ...
		if err.Error() == PodsRaisingPhase {
			log.Print("Waiting pod raise up")
			return reconcile.Result{}, nil
		} else if err.Error() == PodsFallingPhase {
			log.Print("Waiting pod tear down")
			return reconcile.Result{Requeue: true}, nil
		}
		log.Print(err, "Failed to get pods")
		return reconcile.Result{}, err
	}

	if len(pods.Items) > als.Spec.Replicas {
		log.Print("Going to tear down pods...")
		err := r.tearDownPods(als, pods, false)
		return reconcile.Result{}, err
	} else if len(pods.Items) != als.Spec.Replicas {
		log.Print(fmt.Sprintf("Pod missing, current pods count: %d", len(pods.Items)))
		if requeue, err := r.createPod(als, pods); err != nil {
			return reconcile.Result{}, err
		} else if requeue {
			return reconcile.Result{Requeue: true}, nil
		}
	} else {
		log.Print("Nothing to do...")
	}
	return reconcile.Result{}, nil
}
