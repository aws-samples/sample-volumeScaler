package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	defaultKubeletPodsPath = "/var/lib/kubelet/pods"
	defaultPollInterval    = 60 * time.Second
	defaultMaxRetries      = 3
	defaultTimeout         = 30 * time.Second

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
	ScaledAt          string `json:"scaledAt,omitempty"`
	ReachedMaxSize    bool   `json:"reachedMaxSize,omitempty"`
	ResizeInProgress  bool   `json:"resizeInProgress,omitempty"`
	LastRequestedSize string `json:"lastRequestedSize,omitempty"`
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
	KubeletPodsPath string
	PollInterval    time.Duration
	MaxRetries      int
	Timeout         time.Duration
}

// NewDefaultConfig returns a default controller configuration
func NewDefaultConfig() *ControllerConfig {
	return &ControllerConfig{
		KubeletPodsPath: defaultKubeletPodsPath,
		PollInterval:    defaultPollInterval,
		MaxRetries:      defaultMaxRetries,
		Timeout:         defaultTimeout,
	}
}

// -----------------------------------------------------------------------------
// 1) VolumeScaler CR struct (updated group/version: autoscaling.storage.k8s.io / v1alpha1)
// -----------------------------------------------------------------------------

// -------------------------------------
// 2) convertToGi: converts "5Gi" / "512Mi" / "1Ti" into float64 Gi
// -------------------------------------
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

// -------------------------------------
// 3) inClusterOrKubeconfig: tries in-cluster config first, fallback to KUBECONFIG
// -------------------------------------
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

// ------------------------------------------------------
// 4) getPVCUIDsFromLocalMounts: returns slice of PVC UIDs + map PVC UID -> mountPath
// ------------------------------------------------------
func getPVCUIDsFromLocalMounts(kubeletPodsPath string, clientset kubernetes.Interface) ([]types.UID, map[types.UID]string, error) {
	pvcUIDSet := make(map[types.UID]struct{})
	pvcUIDToMount := make(map[types.UID]string)

	if _, err := os.Stat(kubeletPodsPath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("kubelet pods path '%s' does not exist", kubeletPodsPath)
	}

	err := filepath.Walk(kubeletPodsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error accessing path '%s': %v", path, err)
		}

		if info.IsDir() && info.Name() == "mount" && strings.Contains(path, filepath.Join("volumes", "kubernetes.io~csi")) {
			volumeDir := filepath.Base(filepath.Dir(path))
			if strings.HasPrefix(volumeDir, "pvc-") {
				pvcUIDStr := strings.TrimPrefix(volumeDir, "pvc-")
				if pvcUIDStr != "" {
					uid := types.UID(pvcUIDStr)
					pvcUIDSet[uid] = struct{}{}
					pvcUIDToMount[uid] = path
				}
			} else {
				// Possibly the PV name. Attempt to find the claimRef UID
				pv, err := clientset.CoreV1().PersistentVolumes().Get(context.TODO(), volumeDir, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("could not get PV '%s': %v", volumeDir, err)
				}
				if pv.Spec.ClaimRef != nil {
					claimUID := pv.Spec.ClaimRef.UID
					pvcUIDSet[claimUID] = struct{}{}
					pvcUIDToMount[claimUID] = path
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, nil, fmt.Errorf("error walking kubelet pods path: %v", err)
	}

	pvcUIDs := make([]types.UID, 0, len(pvcUIDSet))
	for uid := range pvcUIDSet {
		pvcUIDs = append(pvcUIDs, uid)
	}
	return pvcUIDs, pvcUIDToMount, nil
}

// ---------------------------------------------
// 5) measureUsage: runs "df" -> usage% and usedGi
// ---------------------------------------------
func measureUsage(mountPath string, specSizeGi float64) (int, int, error) {
	if _, err := os.Stat(mountPath); os.IsNotExist(err) {
		return -1, -1, fmt.Errorf("mount path '%s' not found", mountPath)
	}

	cmd := exec.Command("df", mountPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return -1, -1, fmt.Errorf("failed to run df command: %v, output: %s", err, string(out))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return -1, -1, fmt.Errorf("unexpected df output format: %s", string(out))
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return -1, -1, fmt.Errorf("unexpected df output fields: %v", fields)
	}

	usedBlocksStr := fields[2] // "Used" column
	usedBlocks, err := strconv.ParseFloat(usedBlocksStr, 64)
	if err != nil {
		return -1, -1, fmt.Errorf("failed to parse used blocks '%s': %v", usedBlocksStr, err)
	}

	// Usually df blocksize is 1K => usedBlocks => usedKB
	usedGi := usedBlocks / (1024.0 * 1024.0)
	usagePercent := int((usedGi / specSizeGi) * 100)
	return usagePercent, int(usedGi + 0.5), nil
}

// -------------------------------------------------------------
// 6) checkAndHandleResizeFailedEvents:
// Looks for warnings with reason="VolumeResizeFailed"
// -------------------------------------------------------------
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

// ------------------------------------------------------------
// 7) makeInvolvedObjectRef -> so events appear on the CR
// ------------------------------------------------------------
func makeInvolvedObjectRef(vsName types.NamespacedName, vsObj *VolumeScaler) *corev1.ObjectReference {
	return &corev1.ObjectReference{
		APIVersion: vsObj.APIVersion,
		Kind:       vsObj.Kind,
		Namespace:  vsName.Namespace,
		Name:       vsName.Name,
		UID:        vsObj.ObjectMeta.UID,
	}
}

// ------------------------------------------------------------
// helper: parseCooldownDuration
// ------------------------------------------------------------
func parseCooldownDuration(cooldownStr string) (time.Duration, error) {
	if cooldownStr == "" {
		return 0, nil
	}
	return time.ParseDuration(cooldownStr)
}

// ------------------------------------------------------------
// helper: canScaleNow
// ------------------------------------------------------------
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

// ------------------------------------------------------------
// helper: computeNewSize
// ------------------------------------------------------------
func computeNewSize(scale, scaleType string, currentSizeGi float64) (float64, error) {
	switch scaleType {
	case "fixed":
		// e.g., "2Gi"
		fixedInc, err := convertToGi(scale)
		if err != nil {
			return 0, fmt.Errorf("invalid fixed scale '%s': %v", scale, err)
		}
		return currentSizeGi + fixedInc, nil
	case "VolumeScaler":
		// e.g., "30%"
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

// ------------------------------------------------------------
// 8) mainLoop
// ------------------------------------------------------------
type VolumeScalerController struct {
	config    *ControllerConfig
	clientset *kubernetes.Clientset
	dynClient dynamic.Interface
	recorder  record.EventRecorder
	gvr       schema.GroupVersionResource
}

func NewVolumeScalerController(config *ControllerConfig, clientset *kubernetes.Clientset, dynClient dynamic.Interface, recorder record.EventRecorder, gvr schema.GroupVersionResource) *VolumeScalerController {
	return &VolumeScalerController{
		config:    config,
		clientset: clientset,
		dynClient: dynClient,
		recorder:  recorder,
		gvr:       gvr,
	}
}

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

func (c *VolumeScalerController) reconcileLoop(ctx context.Context) error {
	// (A) discover local PVCs on this node
	pvcUIDs, pvcUIDToMount, err := getPVCUIDsFromLocalMounts(c.config.KubeletPodsPath, c.clientset)
	if err != nil {
		return fmt.Errorf("getting local PVCs: %v", err)
	}

	if len(pvcUIDs) == 0 {
		fmt.Println("[INFO] No PVCs found on this node. Sleeping...")
		return nil
	}

	// (B) list all PVCs in cluster
	allPVCs, err := c.clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing PVCs: %v", err)
	}

	pvcMap := make(map[types.UID]*corev1.PersistentVolumeClaim, len(allPVCs.Items))
	for i := range allPVCs.Items {
		pvc := &allPVCs.Items[i]
		pvcMap[pvc.UID] = pvc
	}

	// (C) list all VolumeScalers
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

	// (D) process each PVC that has an associated VolumeScaler
	for _, uid := range pvcUIDs {
		pvc, ok := pvcMap[uid]
		if !ok {
			continue
		}

		ns := pvc.Namespace
		pvcName := pvc.Name
		vsKey := ns + "/" + pvcName

		vsObj, hasScaler := vsMap[vsKey]
		if !hasScaler {
			continue
		}

		if err := c.reconcilePVC(ctx, pvc, vsObj, vsUnstructMap[vsKey], pvcUIDToMount[uid]); err != nil {
			fmt.Printf("[ERROR] reconciling PVC '%s/%s': %v\n", ns, pvcName, err)
		}
	}

	return nil
}

func (c *VolumeScalerController) reconcilePVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim, vsObj *VolumeScaler, vsName types.NamespacedName, mountPath string) error {
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

	// 4) measure usage from mount
	var usagePercent, usedGi int
	if mountPath == "" {
		c.recorder.Eventf(invRef, corev1.EventTypeWarning, "MountNotFound",
			"No mount path found for PVC '%s'", pvc.Name)
		usagePercent = -1
		usedGi = -1
	} else {
		usagePercent, usedGi, err = measureUsage(mountPath, specSizeGi)
		if err != nil {
			c.recorder.Eventf(invRef, corev1.EventTypeWarning, "MeasureFailed",
				"Failed measuring usage for mount '%s': %v", mountPath, err)
			return fmt.Errorf("measuring usage: %v", err)
		}
	}

	// 5) if specSize >= maxSize & status == spec => mark reached
	if specSizeGi >= maxSizeGi && specSizeGi == statusSizeGi {
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

	// 6) is a resize in progress?
	inProgress := statusSizeGi < specSizeGi

	// 7) if was in progress but now complete
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

	// 8) if still in progress, log an event
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

	// 9) usage >= threshold => attempt to expand
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

		if newSizeGi > maxSizeGi {
			newSizeGi = maxSizeGi
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
// 9) main
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
