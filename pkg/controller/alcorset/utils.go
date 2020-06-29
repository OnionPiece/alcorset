package alcorset

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	alcor "github.com/onionpiece/alcorset/pkg/apis/alcor/v1alpha1"
	ipclaim "github.com/onionpiece/ipclaim/pkg/apis/alcor/v1alpha1"
	vpcipclaim "github.com/onionpiece/vpcipclaim/pkg/apis/alcor/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// PodsRaisingPhase stands for pods are creating, but not ready yet, in sequence case
	PodsRaisingPhase = "Sequence case, pod in raising phase"
	// PodsFallingPhase stands for pods are terminted in sequence case
	PodsFallingPhase = "Sequence case, pod in falling phase"

	// FinalizerIPClaim is finalizer for AlcorSet using IPClaim or VPCIPClaim resources
	FinalizerIPClaim = "ipclaim.finalizer.alcorset.alcor.io"
	// FinalizerVPCIPClaim is finalizer for AlcorSet using VPCIPClaim resources
	// VPCIPClaims have finalizer themselves, but those are for VPCIPClaim to call VPC API
	// to release IPs.
	FinalizerVPCIPClaim = "vpcipclaim.finalizer.alcorset.alcor.io"

	// CalicoBackend is ippool backend for calico
	CalicoBackend = "calico"
	// CalicoAnnotationKey is annotation key to specify IP in calico
	CalicoAnnotationKey = "cni.projectcalico.org/ipAddrs"
	// PodNameIndexSep is seperator to join pod name with index
	PodNameIndexSep = "-"
	// AlcorSetAppLabel is label for resources owned by AlcorSet
	AlcorSetAppLabel = "app.alcorset.alcor.io"
	// AlcorSetSpecLabel will have a value with md5 of spec pod used to create
	AlcorSetSpecLabel = "spec.alcorset.alcor.io"

	StatusFailedToClaimIP    = "Failed to claim IP"
	StatusIPClaimNotReady    = "IPClaim not ready"
	StatusPodDeletedNotFound = "Pod deleted not found"
	StatusWaitIPClaimReady   = "Wait IPClaim ready"
)

var (
	emptyContainers = []v1.Container{}
	emptyPodSpec    = v1.PodSpec{
		Containers: emptyContainers,
	}

	allFinalizers = []string{FinalizerIPClaim, FinalizerVPCIPClaim}
)

func getPodName(alcorset *alcor.AlcorSet, podIdx int) string {
	return fmt.Sprintf("%s%s%d", alcorset.Name, PodNameIndexSep, podIdx)
}

func getPodHostname(alcorset *alcor.AlcorSet, podIdx int) string {
	return fmt.Sprintf("%s%s%d", alcorset.Spec.HostnamePrefix, PodNameIndexSep, podIdx)
}

func getIndexByName(podName string) int {
	fields := strings.Split(podName, PodNameIndexSep)
	idx, _ := strconv.Atoi(fields[len(fields)-1])
	return idx
}

func containsInt(list []int, i int) bool {
	for _, v := range list {
		if v == i {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func getPodMap(pods *corev1.PodList) map[string]corev1.Pod {
	podMap := make(map[string]corev1.Pod)
	for _, pod := range pods.Items {
		podMap[pod.Name] = pod
	}
	return podMap
}

func asSha256(o interface{}) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%v", o)))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func newOwnerReference(als *alcor.AlcorSet) *metav1.OwnerReference {
	blockOwnerDeletion := true
	controller := true
	ownerRef := metav1.OwnerReference{
		APIVersion:         als.APIVersion,
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &controller,
		Kind:               als.Kind,
		Name:               als.Name,
		UID:                als.UID,
	}
	return &ownerRef
}

func newIPClaimForCR(als *alcor.AlcorSet, name string) *ipclaim.IPClaim {
	return &ipclaim.IPClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: als.Namespace,
			Labels: map[string]string{
				AlcorSetAppLabel: als.Name,
			},
			OwnerReferences: []metav1.OwnerReference{*newOwnerReference(als)},
		},
		Spec: ipclaim.IPClaimSpec{
			IPPool: als.Spec.IPPool,
			Mbps:   int32(als.Spec.Mbps),
		},
	}
}

func newVPCIPClaimForCR(als *alcor.AlcorSet, name string) *vpcipclaim.VPCIPClaim {
	return &vpcipclaim.VPCIPClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: als.Namespace,
			Labels: map[string]string{
				AlcorSetAppLabel: als.Name,
			},
			OwnerReferences: []metav1.OwnerReference{*newOwnerReference(als)},
		},
		Spec: vpcipclaim.VPCIPClaimSpec{
			Pod: name,
		},
	}
}

func newPodForCR(als *alcor.AlcorSet, name, hostname string, inStage bool, annotations map[string]string) *corev1.Pod {
	metadata := metav1.ObjectMeta{
		Name:        name,
		Namespace:   als.Namespace,
		Labels:      als.Spec.PodTemplateSpec.Labels,
		Annotations: als.Spec.PodTemplateSpec.Annotations,
	}
	if app, ok := metadata.Labels[AlcorSetAppLabel]; !ok || app != als.Name {
		if metadata.Labels == nil {
			metadata.Labels = map[string]string{
				AlcorSetAppLabel: als.Name,
			}
		} else {
			metadata.Labels[AlcorSetAppLabel] = als.Name
		}
	}
	if metadata.Annotations == nil {
		metadata.Annotations = annotations
	} else {
		for k, v := range annotations {
			metadata.Annotations[k] = v
		}
	}
	podSpec := als.Spec.PodTemplateSpec.Spec
	podSpec.Hostname = hostname
	return &corev1.Pod{
		ObjectMeta: metadata,
		Spec:       podSpec,
	}
}
