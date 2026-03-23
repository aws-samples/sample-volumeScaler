package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
)

const (
	// Default values
	defaultPollInterval = 60 * time.Second
	defaultMaxRetries   = 3
	defaultTimeout      = 30 * time.Second

	// Event reasons
	eventReasonResizeFailed    = "ResizeFailed"
	eventReasonResizeComplete  = "ResizeComplete"
	eventReasonResizeRequested = "ResizeRequested"
	eventReasonAtMaxSize       = "AtMaxSize"
	eventReasonStillResizing   = "StillResizing"
	eventReasonCooldownActive  = "CooldownActive"

	// Scale types
	scaleTypeFixed      = "fixed"
	scaleTypePercentage = "percentage"
)

// VolumeScalerSpec defines the desired state of VolumeScaler
type VolumeScalerSpec struct {
	PVCName        string `json:"pvcName"`
	Threshold      string `json:"threshold"`      // e.g., "70%"
	Scale          string `json:"scale"`          // e.g., "2Gi" or "30%"
	ScaleType      string `json:"scaleType"`      // "fixed" or "percentage"
	CooldownPeriod string `json:"cooldownPeriod"` // e.g. "10m"
	MaxSize        string `json:"maxSize"`        // e.g., "15Gi"
}

// VolumeScalerStatus defines the observed state of VolumeScaler
type VolumeScalerStatus struct {
	ScaledAt            string `json:"scaledAt,omitempty"`
	ReachedMaxSize      bool   `json:"reachedMaxSize,omitempty"`
	ResizeInProgress    bool   `json:"resizeInProgress,omitempty"`
	LastRequestedSize   string `json:"lastRequestedSize,omitempty"`
	CurrentUsagePercent int    `json:"currentUsagePercent,omitempty"`
	CurrentUsedGi       string `json:"currentUsedGi,omitempty"`
	CurrentSizeGi       string `json:"currentSizeGi,omitempty"`
}

// VolumeScaler is the Schema for the volumescalers API
type VolumeScaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VolumeScalerSpec   `json:"spec"`
	Status VolumeScalerStatus `json:"status,omitempty"`

	// Additional fields for Kubernetes API compatibility
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

// StorageSize represents a storage size with unit
type StorageSize string

// Percentage represents a percentage value
type Percentage string

// ToGiB converts a storage size string to GiB
func (s StorageSize) ToGiB() (float64, error) {
	return convertToGi(string(s))
}

// ToFloat converts a percentage string to float
func (p Percentage) ToFloat() (float64, error) {
	val := strings.TrimSuffix(string(p), "%")
	return strconv.ParseFloat(val, 64)
}

// ControllerConfig holds configuration for the controller
type ControllerConfig struct {
	PollInterval time.Duration
	MaxRetries   int
	Timeout      time.Duration
}

// NewDefaultConfig returns a default controller configuration with predefined values
// suitable for most production environments.
func NewDefaultConfig() *ControllerConfig {
	return &ControllerConfig{
		PollInterval: defaultPollInterval,
		MaxRetries:   defaultMaxRetries,
		Timeout:      defaultTimeout,
	}
}

// -----------------------------------------------------------------------------
// Kubelet Stats Summary types for parsing /stats/summary API response
// -----------------------------------------------------------------------------

// StatsSummary is the top-level response from the kubelet /stats/summary endpoint.
type StatsSummary struct {
	Pods []PodStats `json:"pods"`
}

// PodStats holds per-pod statistics from the kubelet stats summary.
type PodStats struct {
	PodRef           PodReference  `json:"podRef"`
	VolumeStats      []VolumeStats `json:"volume,omitempty"`
	EphemeralStorage *FsStats      `json:"ephemeral-storage,omitempty"`
}

// PodReference identifies a pod by name and namespace.
type PodReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// VolumeStats holds per-volume statistics.
type VolumeStats struct {
	Name   string        `json:"name"`
	PVCRef *PVCReference `json:"pvcRef,omitempty"`
	FsStats
}

// PVCReference identifies a PVC by name and namespace.
type PVCReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// FsStats holds filesystem usage statistics in bytes.
type FsStats struct {
	AvailableBytes *uint64 `json:"availableBytes,omitempty"`
	CapacityBytes  *uint64 `json:"capacityBytes,omitempty"`
	UsedBytes      *uint64 `json:"usedBytes,omitempty"`
}

// PVCUsageInfo holds the computed usage information for a PVC.
type PVCUsageInfo struct {
	UsedBytes      uint64
	CapacityBytes  uint64
	AvailableBytes uint64
	UsagePercent   int
	UsedGi         float64
}

// -----------------------------------------------------------------------------
// convertToGi: converts "5Gi" / "512Mi" / "1Ti" into float64 Gi
// -----------------------------------------------------------------------------
func convertToGi(sizeStr string) (float64, error) {
	if sizeStr == "" {
		return 0, fmt.Errorf("empty size string")
	}

	var numberStr, unitStr string
	for i, r := range sizeStr {
		if r < '0' || r > '9' {
			numberStr = sizeStr[:i]
			unitStr = sizeStr[i:]
			break
		}
	}

	// If we never encountered a letter, treat entire string as Gi
	if numberStr == "" && unitStr == "" {
		numberStr = sizeStr
		unitStr = "Gi"
	}

	val, err := strconv.ParseFloat(numberStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number in size string '%s': %v", sizeStr, err)
	}

	switch unitStr {
	case "Gi":
		return val, nil
	case "Mi":
		return val / 1024, nil
	case "Ti":
		return val * 1024, nil
	default:
		return 0, fmt.Errorf("unsupported unit '%s' in size string '%s'", unitStr, sizeStr)
	}
}

// -----------------------------------------------------------------------------
// inClusterOrKubeconfig: tries in-cluster config first, fallback to KUBECONFIG
// -----------------------------------------------------------------------------
func inClusterOrKubeconfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %v", err)
		}
		return config, nil
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig: %v", err)
	}
	return config, nil
}

// -----------------------------------------------------------------------------
// fetchNodePVCUsage queries the kubelet /stats/summary API via the Kubernetes
// API proxy and returns a map of "namespace/pvcName" -> PVCUsageInfo for all
// PVC-backed volumes on the given node.
//
// This approach is portable across all Kubernetes environments (EKS, GKE, KIND,
// Minikube, etc.) because it uses the standard Kubernetes API rather than
// relying on host-level filesystem paths or running shell commands like "df".
// -----------------------------------------------------------------------------
func fetchNodePVCUsage(ctx context.Context, clientset kubernetes.Interface, nodeName string) (map[string]*PVCUsageInfo, error) {
	// Call the kubelet stats/summary API via the Kubernetes API server proxy
	req := clientset.CoreV1().RESTClient().Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix("stats/summary")

	resp, err := req.DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stats/summary from node '%s': %v", nodeName, err)
	}

	var summary StatsSummary
	if err := json.Unmarshal(resp, &summary); err != nil {
		return nil, fmt.Errorf("failed to parse stats/summary response: %v", err)
	}

	result := make(map[string]*PVCUsageInfo)
	for _, pod := range summary.Pods {
		for _, vol := range pod.VolumeStats {
			if vol.PVCRef == nil {
				continue
			}
			if vol.UsedBytes == nil || vol.CapacityBytes == nil {
				continue
			}

			key := vol.PVCRef.Namespace + "/" + vol.PVCRef.Name
			usedGi := float64(*vol.UsedBytes) / (1024.0 * 1024.0 * 1024.0)
			capacityGi := float64(*vol.CapacityBytes) / (1024.0 * 1024.0 * 1024.0)

			usagePercent := 0
			if capacityGi > 0 {
				usagePercent = int((usedGi / capacityGi) * 100)
			}

			available := uint64(0)
			if vol.AvailableBytes != nil {
				available = *vol.AvailableBytes
			}

			result[key] = &PVCUsageInfo{
				UsedBytes:      *vol.UsedBytes,
				CapacityBytes:  *vol.CapacityBytes,
				AvailableBytes: available,
				UsagePercent:   usagePercent,
				UsedGi:         usedGi,
			}
		}
	}

	return result, nil
}

// For testing purposes — allows mocking the usage fetcher in tests
var fetchNodePVCUsageFunc = fetchNodePVCUsage

// -----------------------------------------------------------------------------
// checkAndHandleResizeFailedEvents: looks for warnings with reason="VolumeResizeFailed"
// -----------------------------------------------------------------------------
func checkAndHandleResizeFailedEvents(ctx context.Context, clientset kubernetes.Interface, pvcName, pvcNamespace string) string {
	fieldSelector := fmt.Sprintf("involvedObject.kind=PersistentVolumeClaim,involvedObject.name=%s", pvcName)
	evList, err := clientset.CoreV1().Events(pvcNamespace).List(ctx, metav1.ListOptions{FieldSelector: fieldSelector})
	if err != nil {
		fmt.Printf("[ERROR] listing events for PVC '%s/%s': %v\n", pvcNamespace, pvcName, err)
		return ""
	}
	var latestMsg string
	var latestTime time.Time
	for _, ev := range evList.Items {
		if ev.Type == corev1.EventTypeWarning && ev.Reason == "VolumeResizeFailed" {
			t := ev.LastTimestamp.Time
			if t.IsZero() {
				t = ev.CreationTimestamp.Time
			}
			if t.After(latestTime) {
				latestTime = t
				latestMsg = ev.Message
			}
		}
	}
	latestMsg = strings.ReplaceAll(latestMsg, "(MISSING)", "")
	return strings.TrimSpace(latestMsg)
}

// makeInvolvedObjectRef creates an ObjectReference so events appear on the CR
func makeInvolvedObjectRef(vsName types.NamespacedName, vsObj *VolumeScaler) *corev1.ObjectReference {
	return &corev1.ObjectReference{
		APIVersion: vsObj.APIVersion,
		Kind:       vsObj.Kind,
		Namespace:  vsName.Namespace,
		Name:       vsName.Name,
		UID:        vsObj.ObjectMeta.UID,
	}
}

// parseCooldownDuration parses a cooldown duration string into a time.Duration.
func parseCooldownDuration(cooldownStr string) (time.Duration, error) {
	if cooldownStr == "" {
		return 0, nil
	}
	return time.ParseDuration(cooldownStr)
}

// canScaleNow determines if enough time has passed since the last scaling operation.
func canScaleNow(lastScaledAtStr string, cooldown time.Duration) (bool, error) {
	if cooldown == 0 {
		return true, nil
	}
	if lastScaledAtStr == "" {
		return true, nil
	}
	t, err := time.Parse(time.RFC3339, lastScaledAtStr)
	if err != nil {
		return false, fmt.Errorf("cannot parse scaledAt '%s': %v", lastScaledAtStr, err)
	}
	if time.Since(t) < cooldown {
		return false, nil
	}
	return true, nil
}

// computeNewSize calculates the new PVC size based on the current size and scaling policy.
func computeNewSize(scale, scaleType string, currentSizeGi float64) (float64, error) {
	switch scaleType {
	case "fixed":
		fixedInc, err := convertToGi(scale)
		if err != nil {
			return 0, fmt.Errorf("invalid fixed scale '%s': %v", scale, err)
		}
		return currentSizeGi + fixedInc, nil
	case "VolumeScaler":
		scaleStr := strings.TrimSuffix(scale, "%")
		scaleF, err := strconv.ParseFloat(scaleStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid VolumeScaler scale '%s': %v", scale, err)
		}
		inc := currentSizeGi * (scaleF / 100.0)
		return currentSizeGi + inc, nil
	default:
		// If not recognized, treat as percentage
		scaleStr := strings.TrimSuffix(scale, "%")
		scaleF, err := strconv.ParseFloat(scaleStr, 64)
		if err != nil {
			return 0, fmt.Errorf("unknown scaleType '%s' on scale '%s': %v", scaleType, scale, err)
		}
		inc := currentSizeGi * (scaleF / 100.0)
		return currentSizeGi + inc, nil
	}
}

// -----------------------------------------------------------------------------
// VolumeScalerController
// -----------------------------------------------------------------------------

// VolumeScalerController implements the core logic for automated PVC scaling.
// It runs as a DaemonSet on each node, querying the kubelet stats/summary API
// for PVC usage metrics and automatically scaling PVCs based on configurable policies.
type VolumeScalerController struct {
	config    *ControllerConfig
	clientset kubernetes.Interface
	dynClient dynamic.Interface
	recorder  record.EventRecorder
	gvr       schema.GroupVersionResource
}

// NewVolumeScalerController creates a new instance of VolumeScalerController.
func NewVolumeScalerController(config *ControllerConfig, clientset kubernetes.Interface, dynClient dynamic.Interface, recorder record.EventRecorder, gvr schema.GroupVersionResource) *VolumeScalerController {
	return &VolumeScalerController{
		config:    config,
		clientset: clientset,
		dynClient: dynClient,
		recorder:  recorder,
		gvr:       gvr,
	}
}

// Run starts the main controller loop that continuously monitors and scales PVCs.
func (c *VolumeScalerController) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.reconcileLoop(ctx); err != nil {
				fmt.Printf("[ERROR] reconcile loop failed: %v\n", err)
			}
		}
	}
}

// reconcileLoop performs one complete reconciliation cycle.
// It fetches PVC usage from the kubelet stats/summary API on this node,
// then evaluates each PVC against its VolumeScaler configuration.
func (c *VolumeScalerController) reconcileLoop(ctx context.Context) error {
	nodeName := os.Getenv("NODE_NAME_ENV")
	if nodeName == "" {
		return fmt.Errorf("NODE_NAME_ENV environment variable is not set")
	}

	// (A) Fetch PVC usage from the kubelet stats/summary API
	pvcUsageMap, err := fetchNodePVCUsageFunc(ctx, c.clientset, nodeName)
	if err != nil {
		return fmt.Errorf("fetching PVC usage from node '%s': %v", nodeName, err)
	}

	if len(pvcUsageMap) == 0 {
		fmt.Println("[INFO] No PVC usage data found on this node. Sleeping...")
		return nil
	}

	// (B) List all VolumeScalers
	vsList, err := c.dynClient.Resource(c.gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing VolumeScalers: %v", err)
	}

	vsMap := make(map[string]*VolumeScaler)
	vsUnstructMap := make(map[string]types.NamespacedName)
	if vsList != nil && len(vsList.Items) > 0 {
		for _, unstr := range vsList.Items {
			vsObj := &VolumeScaler{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstr.Object, vsObj)
			if err != nil {
				fmt.Printf("[ERROR] converting VolumeScaler: %v\n", err)
				continue
			}
			key := unstr.GetNamespace() + "/" + vsObj.Spec.PVCName
			vsMap[key] = vsObj
			vsUnstructMap[key] = types.NamespacedName{
				Namespace: unstr.GetNamespace(),
				Name:      unstr.GetName(),
			}
		}
	}

	// (C) For each PVC with usage data and a VolumeScaler, reconcile
	for pvcKey, usageInfo := range pvcUsageMap {
		vsObj, hasScaler := vsMap[pvcKey]
		if !hasScaler {
			continue
		}

		// Fetch the actual PVC object
		parts := strings.SplitN(pvcKey, "/", 2)
		if len(parts) != 2 {
			continue
		}
		ns, pvcName := parts[0], parts[1]

		pvc, err := c.clientset.CoreV1().PersistentVolumeClaims(ns).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			fmt.Printf("[ERROR] fetching PVC '%s': %v\n", pvcKey, err)
			continue
		}

		if err := c.reconcilePVC(ctx, pvc, vsObj, vsUnstructMap[pvcKey], usageInfo); err != nil {
			fmt.Printf("[ERROR] reconciling PVC '%s': %v\n", pvcKey, err)
		}
	}

	return nil
}

// reconcilePVC evaluates a single PVC against its VolumeScaler configuration and
// triggers scaling operations when thresholds are exceeded.
//
// Instead of relying on host-level mount paths and the "df" command, this function
// receives pre-computed usage information from the kubelet stats/summary API,
// making it portable across all Kubernetes environments.
func (c *VolumeScalerController) reconcilePVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim, vsObj *VolumeScaler, vsName types.NamespacedName, usageInfo *PVCUsageInfo) error {
	invRef := makeInvolvedObjectRef(vsName, vsObj)

	// 1) parse threshold
	thresholdF, err := strconv.ParseFloat(strings.TrimSuffix(vsObj.Spec.Threshold, "%"), 64)
	if err != nil {
		c.recorder.Eventf(invRef, corev1.EventTypeWarning, "InvalidThreshold",
			"Threshold '%s' invalid: %v", vsObj.Spec.Threshold, err)
		return fmt.Errorf("invalid threshold: %v", err)
	}

	// 2) parse maxSize
	maxSizeGi, err := convertToGi(vsObj.Spec.MaxSize)
	if err != nil {
		c.recorder.Eventf(invRef, corev1.EventTypeWarning, "InvalidMaxSize",
			"MaxSize '%s' invalid: %v", vsObj.Spec.MaxSize, err)
		return fmt.Errorf("invalid max size: %v", err)
	}

	// 3) parse current spec & status sizes
	specSizeGi, _ := convertToGi(pvc.Spec.Resources.Requests.Storage().String())
	statusSizeGi, _ := convertToGi(pvc.Status.Capacity.Storage().String())

	// 4) usage from kubelet stats/summary API
	// Recalculate usage percentage against the PVC spec size (what the user requested)
	// rather than the filesystem capacity reported by the kubelet, since the underlying
	// storage provider may allocate more than requested (e.g., EBS minimum volume size).
	usedGi := int(usageInfo.UsedGi + 0.5)
	usagePercent := usageInfo.UsagePercent
	if specSizeGi > 0 {
		usagePercent = int((usageInfo.UsedGi / specSizeGi) * 100)
	}

	// 4b) Always update current usage in VolumeScaler status so users can see it via kubectl
	usagePatch := []byte(fmt.Sprintf(
		`{"status":{"currentUsagePercent":%d,"currentUsedGi":"%.1fGi","currentSizeGi":"%.0fGi"}}`,
		usagePercent, usageInfo.UsedGi, specSizeGi))
	_, err = c.dynClient.Resource(c.gvr).Namespace(vsName.Namespace).
		Patch(ctx, vsName.Name, types.MergePatchType, usagePatch, metav1.PatchOptions{}, "status")
	if err != nil {
		fmt.Printf("[WARN] failed to patch usage status for '%s/%s': %v\n", vsName.Namespace, vsName.Name, err)
	}

	// 5) is a resize in progress?
	inProgress := statusSizeGi < specSizeGi

	// 6) if was in progress but now complete
	if vsObj.Status.ResizeInProgress && !inProgress {
		reachedMax := specSizeGi >= maxSizeGi
		msg := fmt.Sprintf("PVC '%s/%s' expansion complete. Capacity=%.0fGi, usage=%d%%.",
			vsName.Namespace, pvc.Name, statusSizeGi, usagePercent)
		c.recorder.Event(invRef, corev1.EventTypeNormal, eventReasonResizeComplete, msg)
		fmt.Printf("[INFO] %s\n", msg)

		nowStr := time.Now().UTC().Format(time.RFC3339)
		patchDone := []byte(fmt.Sprintf(
			`{"status":{"resizeInProgress":false,"scaledAt":"%s","reachedMaxSize":%t}}`,
			nowStr, reachedMax))
		_, err = c.dynClient.Resource(c.gvr).Namespace(vsName.Namespace).
			Patch(ctx, vsName.Name, types.MergePatchType, patchDone, metav1.PatchOptions{}, "status")
		if err != nil {
			return fmt.Errorf("patching resize completion: %v", err)
		}
		return nil
	}

	// 7) if still in progress, log an event
	if inProgress {
		pvcErrMsg := checkAndHandleResizeFailedEvents(ctx, c.clientset, pvc.Name, vsName.Namespace)
		if pvcErrMsg != "" {
			logMsg := fmt.Sprintf(
				"PVC '%s/%s' still resizing (Spec=%.0fGi, Status=%.0fGi) due to '%s'. usage=%dGi (%d%%).",
				vsName.Namespace, pvc.Name, specSizeGi, statusSizeGi, pvcErrMsg, usedGi, usagePercent)
			c.recorder.Event(invRef, corev1.EventTypeWarning, eventReasonStillResizing, logMsg)
			fmt.Printf("[ERROR] %s\n", logMsg)
		} else {
			logMsg := fmt.Sprintf(
				"PVC '%s/%s' still resizing (Spec=%.0fGi, Status=%.0fGi). usage=%dGi (%d%%).",
				vsName.Namespace, pvc.Name, specSizeGi, statusSizeGi, usedGi, usagePercent)
			c.recorder.Event(invRef, corev1.EventTypeWarning, eventReasonStillResizing, logMsg)
			fmt.Printf("[ERROR] %s\n", logMsg)
		}
		return nil
	}

	// 8) usage >= threshold => attempt to expand
	if usagePercent >= int(thresholdF) {
		cd, err := parseCooldownDuration(vsObj.Spec.CooldownPeriod)
		if err != nil {
			c.recorder.Eventf(invRef, corev1.EventTypeWarning, "InvalidCooldown",
				"CooldownPeriod '%s' invalid: %v", vsObj.Spec.CooldownPeriod, err)
			return fmt.Errorf("invalid cooldown period: %v", err)
		}

		okToScale, err := canScaleNow(vsObj.Status.ScaledAt, cd)
		if err != nil {
			c.recorder.Eventf(invRef, corev1.EventTypeWarning, "CooldownError",
				"Failed to check cooldownPeriod: %v", err)
			return fmt.Errorf("checking cooldown: %v", err)
		}

		if !okToScale {
			msg := fmt.Sprintf(
				"PVC '%s/%s' usage=%d%% >= threshold=%s, but in cooldown. Skipping expansion.",
				vsName.Namespace, pvc.Name, usagePercent, vsObj.Spec.Threshold)
			fmt.Printf("[INFO] %s\n", msg)
			c.recorder.Event(invRef, corev1.EventTypeNormal, eventReasonCooldownActive, msg)
			return nil
		}

		// compute new size
		newSizeGi, err := computeNewSize(vsObj.Spec.Scale, vsObj.Spec.ScaleType, specSizeGi)
		if err != nil {
			c.recorder.Eventf(invRef, corev1.EventTypeWarning, "ScaleParseError",
				"Failed parsing scale '%s' with type '%s': %v",
				vsObj.Spec.Scale, vsObj.Spec.ScaleType, err)
			return fmt.Errorf("computing new size: %v", err)
		}

		// If we can't scale up because we're at max size, mark as reached max size
		if newSizeGi > maxSizeGi {
			newSizeGi = maxSizeGi
			if specSizeGi >= maxSizeGi {
				patchData := []byte(`{"status":{"reachedMaxSize":true}}`)
				_, err = c.dynClient.Resource(c.gvr).Namespace(vsName.Namespace).
					Patch(ctx, vsName.Name, types.MergePatchType, patchData, metav1.PatchOptions{}, "status")
				if err != nil {
					return fmt.Errorf("patching reachedMaxSize: %v", err)
				}

				msg := fmt.Sprintf("PVC '%s/%s' reached maxSize=%.0fGi. usage=%d%%",
					vsName.Namespace, pvc.Name, maxSizeGi, usagePercent)
				c.recorder.Event(invRef, corev1.EventTypeWarning, eventReasonAtMaxSize, msg)
				fmt.Printf("[WARNING] %s\n", msg)
				return nil
			}
		}

		if newSizeGi <= specSizeGi {
			msg := fmt.Sprintf(
				"Computed newSize=%.0fGi <= current=%.0fGi. usage=%d%% => no net expansion.",
				newSizeGi, specSizeGi, usagePercent)
			fmt.Printf("[INFO] %s\n", msg)
			return nil
		}

		newSizeStr := fmt.Sprintf("%.0fGi", newSizeGi)
		pvcPatch := []byte(fmt.Sprintf(`{"spec":{"resources":{"requests":{"storage":"%s"}}}}`, newSizeStr))
		_, err = c.clientset.CoreV1().PersistentVolumeClaims(vsName.Namespace).Patch(
			ctx, pvc.Name, types.MergePatchType, pvcPatch, metav1.PatchOptions{})
		if err != nil {
			msg := fmt.Sprintf(
				"Failed initiating expansion from %.0fGi -> %s: %v",
				specSizeGi, newSizeStr, err)
			c.recorder.Event(invRef, corev1.EventTypeWarning, eventReasonResizeFailed, msg)
			fmt.Printf("[ERROR] %s\n", msg)
			return fmt.Errorf("patching PVC: %v", err)
		}

		succMsg := fmt.Sprintf(
			"Initiated resize of PVC '%s/%s' from %.0fGi -> %s. usage=%d%%, used=%dGi",
			vsName.Namespace, pvc.Name, specSizeGi, newSizeStr, usagePercent, usedGi)
		c.recorder.Event(invRef, corev1.EventTypeNormal, eventReasonResizeRequested, succMsg)
		fmt.Printf("[INFO] %s\n", succMsg)

		nowStr := time.Now().UTC().Format(time.RFC3339)
		stPatch := []byte(fmt.Sprintf(
			`{"status":{"resizeInProgress":true,"lastRequestedSize":"%s","scaledAt":"%s"}}`,
			newSizeStr, nowStr))
		_, err = c.dynClient.Resource(c.gvr).Namespace(vsName.Namespace).
			Patch(ctx, vsName.Name, types.MergePatchType, stPatch, metav1.PatchOptions{}, "status")
		if err != nil {
			return fmt.Errorf("patching VolumeScaler status: %v", err)
		}
	} else {
		msg := fmt.Sprintf("PVC '%s/%s' usage=%d%% < threshold=%s; no expansion needed.",
			vsName.Namespace, pvc.Name, usagePercent, vsObj.Spec.Threshold)
		fmt.Printf("[INFO] %s\n", msg)
	}

	return nil
}

// ------------------------------------------------------------
// main
// ------------------------------------------------------------
func main() {
	config, err := inClusterOrKubeconfig()
	if err != nil {
		fmt.Printf("[FATAL] Failed to get Kubernetes config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("[FATAL] Failed to create Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Printf("[FATAL] Failed to create dynamic client: %v\n", err)
		os.Exit(1)
	}

	// Setup event broadcaster
	scheme := runtime.NewScheme()
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: clientset.CoreV1().Events(""),
	})
	recorder := broadcaster.NewRecorder(scheme, corev1.EventSource{Component: "volumescaler-controller"})

	// Updated GVR to use new group/version
	gvr := schema.GroupVersionResource{
		Group:    "autoscaling.storage.k8s.io",
		Version:  "v1alpha1",
		Resource: "volumescalers",
	}

	controller := NewVolumeScalerController(
		NewDefaultConfig(),
		clientset,
		dynClient,
		recorder,
		gvr,
	)

	fmt.Println("Starting VolumeScaler operator...")
	if err := controller.Run(context.Background()); err != nil {
		fmt.Printf("[FATAL] Controller failed: %v\n", err)
		os.Exit(1)
	}
}
