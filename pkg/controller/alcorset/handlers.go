package alcorset

import (
	"context"
	"fmt"
	"log"

	alcorv1alpha1 "github.com/onionpiece/alcorset/pkg/apis/alcor/v1alpha1"
	ipclaim "github.com/onionpiece/ipclaim/pkg/apis/alcor/v1alpha1"
	saishang "github.com/onionpiece/saishang/pkg/types"
	"github.com/onionpiece/vpcapi"
	vpcipclaim "github.com/onionpiece/vpcipclaim/pkg/apis/alcor/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *ReconcileAlcorSet) initStatus(als *alcorv1alpha1.AlcorSet) error {
	doInit := false
	alsStatus := als.Status.DeepCopy()
	if alsStatus.ClaimedIPs == nil {
		alsStatus.ClaimedIPs = []string{}
		doInit = true
	}
	if doInit {
		als.Status = *alsStatus
		if err := r.client.Status().Update(context.TODO(), als); err != nil {
			return fmt.Errorf("Failed to update status for psHash, since: %v", err)
		}
	}
	return nil
}

func (r *ReconcileAlcorSet) getIPClaimRef(als *alcorv1alpha1.AlcorSet, podName string) (*ipclaim.IPClaim, error) {
	ipClaimRef := &ipclaim.IPClaim{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: als.Namespace}, ipClaimRef)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating a new IPClaim for %s.%s on %s", als.Namespace, podName, als.Spec.IPPool)
		newIPClaim := newIPClaimForCR(als, podName)
		if err := r.client.Create(context.TODO(), newIPClaim); err != nil {
			return nil, fmt.Errorf("Fail to create ipclaim")
		}
		return nil, nil
	}
	if ipClaimRef.Status.IP == "" {
		return nil, nil
	}
	log.Printf("Get vpcipclaim(%v) for alcorset(%s.%s)", ipClaimRef.Status, als.Namespace, podName)
	return ipClaimRef, nil
}

func (r *ReconcileAlcorSet) getVPCIPClaimRef(als *alcorv1alpha1.AlcorSet, podName string) (*vpcipclaim.VPCIPClaim, error) {
	vpcIPClaimRef := &vpcipclaim.VPCIPClaim{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: als.Namespace}, vpcIPClaimRef)
	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating a new VPCIPClaim for %s.%s", als.Namespace, podName)
		newVPCIPClaim := newVPCIPClaimForCR(als, podName)
		if err := r.client.Create(context.TODO(), newVPCIPClaim); err != nil {
			return nil, fmt.Errorf("Fail to create vpcipclaim")
		}
		return nil, nil
	}
	if vpcIPClaimRef.Status.IP == "" {
		return nil, nil
	}
	log.Printf("Get vpcipclaim(%v) for alcorset(%s.%s)", vpcIPClaimRef.Status, als.Namespace, podName)
	return vpcIPClaimRef, nil
}

func (r *ReconcileAlcorSet) addFinalizers(alcorset *alcorv1alpha1.AlcorSet, toAdd []string) error {
	fins := alcorset.GetFinalizers()
	fins = append(fins, toAdd...)
	alcorset.SetFinalizers(fins)
	err := r.client.Update(context.TODO(), alcorset)
	if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileAlcorSet) removeFinalizer(alcorset *alcorv1alpha1.AlcorSet, toRemove string) error {
	finalizers := []string{}
	for _, fin := range alcorset.GetFinalizers() {
		if fin != toRemove {
			finalizers = append(finalizers, fin)
		}
	}
	alcorset.SetFinalizers(finalizers)
	err := r.client.Update(context.TODO(), alcorset)
	if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileAlcorSet) getPods(alcorset *alcorv1alpha1.AlcorSet) (*corev1.PodList, error) {
	pods := &corev1.PodList{}
	opts := []client.ListOption{
		client.InNamespace(alcorset.Namespace),
		client.MatchingLabels{AlcorSetAppLabel: alcorset.Name},
	}
	if err := r.client.List(context.TODO(), pods, opts...); err != nil {
		return pods, err
	}
	if alcorset.Spec.Sequence {
		allRunningAndReady := true
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp != nil {
				return pods, fmt.Errorf(PodsFallingPhase)
			}
			if &pod.Status == nil || pod.Status.Phase != corev1.PodRunning || !podutil.IsPodReady(&pod) {
				allRunningAndReady = false
				break
			}
		}
		if !allRunningAndReady {
			return pods, fmt.Errorf(PodsRaisingPhase)
		}
	}
	return pods, nil
}

func (r *ReconcileAlcorSet) tearDownPods(als *alcorv1alpha1.AlcorSet, pods *corev1.PodList, deleteAll bool) error {
	if als.Spec.Sequence {
		var pop *corev1.Pod
		index := -1
		// find pod with biggest index to pop
		for _, pod := range pods.Items {
			_index := getIndexByName(pod.Name)
			if _index > index {
				index = _index
				pop = &pod
			}
		}
		return r.client.Delete(context.TODO(), pop)
	}
	border := als.Spec.Replicas
	if deleteAll {
		border = 0
	}
	for _, pod := range pods.Items {
		if getIndexByName(pod.Name) >= border {
			if err := r.client.Delete(context.TODO(), &pod); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ReconcileAlcorSet) createPod(als *alcorv1alpha1.AlcorSet, pods *corev1.PodList) (bool, error) {
	inStage := false
	podMap := getPodMap(pods)
	numToCreate := 1
	if !als.Spec.Sequence {
		numToCreate = als.Spec.Replicas - len(pods.Items)
	}
	for i := 0; i != numToCreate; i++ {
		podName := ""
		podHostname := ""
		for idx := 0; idx != als.Spec.Replicas; idx++ {
			podName = getPodName(als, idx)
			podHostname = getPodHostname(als, idx)
			if _, ok := podMap[podName]; !ok {
				break
			}
		}

		annotations := make(map[string]string)
		// Verify IPClaim or VPCIPClaim already exists
		if als.Spec.OnVPC {
			vpcIPClaimRef, err := r.getVPCIPClaimRef(als, podName)
			if err != nil {
				log.Printf("Failed to create VPCIPClaim for %s.%s, since: %v", als.Namespace, als.Name, err)
				return true, nil
			} else if vpcIPClaimRef == nil {
				log.Printf("VPCIPClaim for %s.%s not ready yet, will requeue", als.Namespace, podName)
				return true, nil
			}
			if vpcIPClaimRef.Status.IP != "" && !contains(als.Status.ClaimedIPs, vpcIPClaimRef.Status.IP) {
				alsStatus := als.Status.DeepCopy()
				alsStatus.ClaimedIPs = append(alsStatus.ClaimedIPs, vpcIPClaimRef.Status.IP)
				als.Status = *alsStatus
				if err := r.client.Status().Update(context.TODO(), als); err != nil {
					return false, err
				}
			}
			annotations[vpcapi.AnnoKeyVPCIP] = vpcIPClaimRef.Status.IP
			annotations[vpcapi.AnnoKeyVPCNICMAC] = vpcIPClaimRef.Status.InterfaceMACAddress
			annotations[vpcapi.AnnoKeyVPCNICID] = vpcIPClaimRef.Status.InterfaceID
			annotations[vpcapi.AnnoKeyVPCInstanceID] = vpcIPClaimRef.Status.InstanceID
			annotations[vpcapi.AnnoKeyVPCIPRetain] = "true"
		} else {
			ipClaimRef, err := r.getIPClaimRef(als, podName)
			if err != nil {
				log.Printf("Failed to create IPClaim for %s.%s, since: %v", als.Namespace, als.Name, err)
				return true, nil
			} else if ipClaimRef == nil {
				log.Printf("IPClaim for %s.%s not ready yet, will requeue", als.Namespace, podName)
				return true, nil
			}
			if ipClaimRef.Status.IP != "" && !contains(als.Status.ClaimedIPs, ipClaimRef.Status.IP) {
				alsStatus := als.Status.DeepCopy()
				alsStatus.ClaimedIPs = append(alsStatus.ClaimedIPs, ipClaimRef.Status.IP)
				als.Status = *alsStatus
				if err := r.client.Status().Update(context.TODO(), als); err != nil {
					return false, err
				}
			}
			// TODO
			annotations[saishang.AnnoKeySriovIP] = ipClaimRef.Status.IP
			annotations[saishang.AnnoKeySriovVlan] = ""
			annotations[saishang.AnnoKeySriovRoute] = ""
			annotations[saishang.AnnoKeySriovMask] = ""
			annotations[saishang.AnnoKeySriovMbps] = ""
		}
		log.Printf("Going to use annotations: %v", annotations)

		// Define a new Pod object
		pod := newPodForCR(als, podName, podHostname, inStage, annotations)

		// Set als instance as the owner and controller
		if err := controllerutil.SetControllerReference(als, pod, r.scheme); err != nil {
			return false, err
		}
		// Check if this Pod already exists
		found := &corev1.Pod{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
		if err != nil && errors.IsNotFound(err) {
			log.Print("Creating a new Pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
			if err := r.client.Create(context.TODO(), pod); err != nil {
				return false, err
			}
			alsStatus := *als.Status.DeepCopy()
			alsStatus.Count++
			als.Status = alsStatus
			if err := r.client.Status().Update(context.TODO(), als); err != nil {
				return false, err
			}
			podMap[podName] = *pod
		} else {
			log.Print("Found a pod", "Name", found.Name, "Status", found.Status.Phase)
		}
	}
	return false, nil
}

func (r *ReconcileAlcorSet) deleteIPClaims(als *alcorv1alpha1.AlcorSet) error {
	ipclaims := &ipclaim.IPClaimList{}
	opts := []client.ListOption{
		client.InNamespace(als.Namespace),
		client.MatchingLabels{AlcorSetAppLabel: als.Name},
	}
	if err := r.client.List(context.TODO(), ipclaims, opts...); err != nil {
		if errors.IsNotFound(err) {
			log.Print("No ipclaims found, consider the resources are deleted")
			return nil
		}
		return fmt.Errorf("Failed to release ipclaims, found error when list ipclaims: %v", err)
	}
	claimedIPs := []string{}
	for _, ipclaim := range ipclaims.Items {
		claimedIPs = append(claimedIPs, ipclaim.Status.IP)
		if err := r.client.Delete(context.TODO(), &ipclaim); err != nil {
			return fmt.Errorf("Failed to release ipclaims, found error when delete ipclaim: %v", err)
		}
	}

	ipLeft := []string{}
	for _, ip := range als.Status.ClaimedIPs {
		if contains(claimedIPs, ip) {
			ipLeft = append(ipLeft, ip)
		}
	}
	alsStatus := als.Status.DeepCopy()
	alsStatus.ClaimedIPs = ipLeft
	als.Status = *alsStatus
	if err := r.client.Status().Update(context.TODO(), als); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileAlcorSet) deleteVPCIPClaims(als *alcorv1alpha1.AlcorSet) error {
	log.Printf("Deleting VPCIPClaim for %s.%s", als.Namespace, als.Name)
	vpcipclaims := &vpcipclaim.VPCIPClaimList{}
	opts := []client.ListOption{
		client.InNamespace(als.Namespace),
		client.MatchingLabels{AlcorSetAppLabel: als.Name},
	}
	if err := r.client.List(context.TODO(), vpcipclaims, opts...); err != nil {
		if errors.IsNotFound(err) {
			log.Print("No vpcipclaims found, consider the resources are deleted")
			return nil
		}
		return fmt.Errorf("Failed to release vpcipclaims, found error when list vpcipclaims: %v", err)
	}
	claimedIPs := []string{}
	for _, vpcipclaim := range vpcipclaims.Items {
		claimedIPs = append(claimedIPs, vpcipclaim.Status.IP)
		log.Printf("To delete vpcipclaim %s", vpcipclaim.Name)
		if err := r.client.Delete(context.TODO(), &vpcipclaim); err != nil {
			return fmt.Errorf("Failed to release vpcipclaims, found error when delete vpcipclaim: %v", err)
		}
	}

	ipLeft := []string{}
	for _, ip := range als.Status.ClaimedIPs {
		if contains(claimedIPs, ip) {
			ipLeft = append(ipLeft, ip)
		}
	}
	if len(ipLeft) < len(als.Status.ClaimedIPs) {
		alsStatus := als.Status.DeepCopy()
		alsStatus.ClaimedIPs = ipLeft
		als.Status = *alsStatus
		if err := r.client.Status().Update(context.TODO(), als); err != nil {
			return err
		}
	}
	return nil
}
