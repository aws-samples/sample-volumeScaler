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

// -----------------------------------------------------------------------------
// 1) VolumeScaler CR struct (updated group/version: autoscaling.storage.k8s.io / v1alpha1)
// -----------------------------------------------------------------------------
type VolumeScaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec struct {
		PVCName        string `json:"pvcName"`
		Threshold      string `json:"threshold"`      // e.g., "70%"
		Scale          string `json:"scale"`          // e.g., "2Gi" or "30%"
		ScaleType      string `json:"scaleType"`      // "fixed" or "percentage"
		CooldownPeriod string `json:"cooldownPeriod"` // e.g. "10m"
		MaxSize        string `json:"maxSize"`        // e.g., "15Gi"
	} `json:"spec"`

	Status struct {
		ScaledAt          string `json:"scaledAt,omitempty"`
		ReachedMaxSize    bool   `json:"reachedMaxSize,omitempty"`
		ResizeInProgress  bool   `json:"resizeInProgress,omitempty"`
		LastRequestedSize string `json:"lastRequestedSize,omitempty"`
	} `json:"status,omitempty"`
}

// -------------------------------------
// 2) convertToGi: converts "5Gi" / "512Mi" / "1Ti" into float64 Gi
// -------------------------------------
func convertToGi(sizeStr string) (float64, error) {
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
		return 0, err
	}
	switch unitStr {
	case "Gi":
		return val, nil
	case "Mi":
		return val / 1024, nil
	case "Ti":
		return val * 1024, nil
	default:
		// Not recognized => interpret as Gi
		return val, nil
	}
}

// -------------------------------------
// 3) inClusterOrKubeconfig: tries in-cluster config first, fallback to KUBECONFIG
// -------------------------------------
func inClusterOrKubeconfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		return rest.InClusterConfig()
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

// ------------------------------------------------------
// 4) getPVCUIDsFromLocalMounts: returns slice of PVC UIDs + map PVC UID -> mountPath
// ------------------------------------------------------
func getPVCUIDsFromLocalMounts(kubeletPodsPath string, clientset kubernetes.Interface) ([]types.UID, map[types.UID]string) {
	pvcUIDSet := make(map[types.UID]struct{})
	pvcUIDToMount := make(map[types.UID]string)

	filepath.Walk(kubeletPodsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == "mount" && strings.Contains(path, filepath.Join("volumes", "kubernetes.io~csi")) {
			volumeDir := filepath.Base(filepath.Dir(path)) // e.g. "pvc-<uid>" or the actual PV name
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
					fmt.Printf("[WARN] Could not get PV '%s': %v\n", volumeDir, err)
					return nil
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

	var pvcUIDs []types.UID
	for uid := range pvcUIDSet {
		pvcUIDs = append(pvcUIDs, uid)
	}
	return pvcUIDs, pvcUIDToMount
}

// ---------------------------------------------
// 5) measureUsage: runs "df" -> usage% and usedGi
// ---------------------------------------------
func measureUsage(mountPath string, specSizeGi float64) (int, int, error) {
	if _, err := os.Stat(mountPath); os.IsNotExist(err) {
		return -1, -1, fmt.Errorf("mount path '%s' not found", mountPath)
	}
	out, err := exec.Command("df", mountPath).CombinedOutput()
	if err != nil {
		return -1, -1, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return -1, -1, fmt.Errorf("unexpected df output: %s", out)
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return -1, -1, fmt.Errorf("df output not as expected: %v", fields)
	}
	usedBlocksStr := fields[2] // "Used" column
	usedBlocks, err := strconv.ParseFloat(usedBlocksStr, 64)
	if err != nil {
		return -1, -1, err
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
		UID:        vsObj.UID,
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

// ----------------------------------------------------
// 8) mainLoop
// ----------------------------------------------------
func mainLoop(clientset *kubernetes.Clientset, dynClient dynamic.Interface, recorder record.EventRecorder, gvr schema.GroupVersionResource) {
	for {
		ctx := context.Background()

		// (A) discover local PVCs on this node
		pvcUIDs, pvcUIDToMount := getPVCUIDsFromLocalMounts("/var/lib/kubelet/pods", clientset)
		if len(pvcUIDs) == 0 {
			fmt.Println("[INFO] No PVCs found on this node. Sleeping 60s...")
			time.Sleep(60 * time.Second)
			continue
		}

		// (B) list all PVCs in cluster
		allPVCs, err := clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
		if err != nil {
			fmt.Printf("[ERROR] listing PVCs: %v\n", err)
			time.Sleep(60 * time.Second)
			continue
		}
		pvcMap := make(map[types.UID]*corev1.PersistentVolumeClaim, len(allPVCs.Items))
		for i := range allPVCs.Items {
			pvc := &allPVCs.Items[i]
			pvcMap[pvc.UID] = pvc
		}

		// (C) list all VolumeScalers
		vsList, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			fmt.Printf("[ERROR] listing VolumeScalers: %v\n", err)
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

			invRef := makeInvolvedObjectRef(vsUnstructMap[vsKey], vsObj)

			// 1) parse threshold
			thresholdF, err := strconv.ParseFloat(strings.TrimSuffix(vsObj.Spec.Threshold, "%"), 64)
			if err != nil {
				recorder.Eventf(invRef, corev1.EventTypeWarning, "InvalidThreshold",
					"Threshold '%s' invalid: %v", vsObj.Spec.Threshold, err)
				continue
			}

			// 2) parse maxSize
			maxSizeGi, err := convertToGi(vsObj.Spec.MaxSize)
			if err != nil {
				recorder.Eventf(invRef, corev1.EventTypeWarning, "InvalidMaxSize",
					"MaxSize '%s' invalid: %v", vsObj.Spec.MaxSize, err)
				continue
			}

			// 3) parse current spec & status sizes
			specSizeGi, _ := convertToGi(pvc.Spec.Resources.Requests.Storage().String())
			statusSizeGi, _ := convertToGi(pvc.Status.Capacity.Storage().String())

			// 4) measure usage from mount
			mountPath := pvcUIDToMount[uid]
			var usagePercent, usedGi int
			if mountPath == "" {
				recorder.Eventf(invRef, corev1.EventTypeWarning, "MountNotFound",
					"No mount path found for PVC '%s'", pvcName)
				usagePercent = -1
				usedGi = -1
			} else {
				usagePercent, usedGi, err = measureUsage(mountPath, specSizeGi)
				if err != nil {
					recorder.Eventf(invRef, corev1.EventTypeWarning, "MeasureFailed",
						"Failed measuring usage for mount '%s': %v", mountPath, err)
					usagePercent = -1
					usedGi = -1
				}
			}

			// 5) if specSize >= maxSize & status == spec => mark reached
			if specSizeGi >= maxSizeGi && specSizeGi == statusSizeGi {
				patchData := []byte(`{"status":{"reachedMaxSize":true}}`)
				_, _ = dynClient.Resource(gvr).Namespace(ns).
					Patch(ctx, vsUnstructMap[vsKey].Name, types.MergePatchType, patchData, metav1.PatchOptions{}, "status")
				msg := fmt.Sprintf("PVC '%s/%s' reached maxSize=%.0fGi. usage=%d%%",
					ns, pvcName, maxSizeGi, usagePercent)
				recorder.Event(invRef, corev1.EventTypeWarning, "AtMaxSize", msg)
				fmt.Printf("[WARNING] %s\n", msg)
				continue
			}

			// 6) is a resize in progress?
			inProgress := statusSizeGi < specSizeGi

			// 7) if was in progress but now complete
			if vsObj.Status.ResizeInProgress && !inProgress {
				reachedMax := specSizeGi >= maxSizeGi
				msg := fmt.Sprintf("PVC '%s/%s' expansion complete. Capacity=%.0fGi, usage=%d%%.",
					ns, pvcName, statusSizeGi, usagePercent)
				recorder.Event(invRef, corev1.EventTypeNormal, "ResizeComplete", msg)
				fmt.Printf("[INFO] %s\n", msg)

				nowStr := time.Now().UTC().Format(time.RFC3339)
				patchDone := []byte(fmt.Sprintf(
					`{"status":{"resizeInProgress":false,"scaledAt":"%s","reachedMaxSize":%t}}`,
					nowStr, reachedMax))
				_, pErr := dynClient.Resource(gvr).Namespace(ns).
					Patch(ctx, vsUnstructMap[vsKey].Name, types.MergePatchType, patchDone, metav1.PatchOptions{}, "status")
				if pErr != nil {
					fmt.Printf("[ERROR] clearing resizeInProgress for VolumeScaler %s: %v\n", vsKey, pErr)
				}
				continue
			}

			// 8) if still in progress, log an event
			if inProgress {
				pvcErrMsg := checkAndHandleResizeFailedEvents(ctx, clientset, pvcName, ns)
				if pvcErrMsg != "" {
					logMsg := fmt.Sprintf(
						"PVC '%s/%s' still resizing (Spec=%.0fGi, Status=%.0fGi) due to '%s'. usage=%dGi (%d%%).",
						ns, pvcName, specSizeGi, statusSizeGi, pvcErrMsg, usedGi, usagePercent)
					recorder.Event(invRef, corev1.EventTypeWarning, "StillResizing", logMsg)
					fmt.Printf("[ERROR] %s\n", logMsg)
				} else {
					logMsg := fmt.Sprintf(
						"PVC '%s/%s' still resizing (Spec=%.0fGi, Status=%.0fGi). usage=%dGi (%d%%).",
						ns, pvcName, specSizeGi, statusSizeGi, usedGi, usagePercent)
					recorder.Event(invRef, corev1.EventTypeWarning, "StillResizing", logMsg)
					fmt.Printf("[ERROR] %s\n", logMsg)
				}
				continue
			}

			// 9) usage >= threshold => attempt to expand
			if usagePercent >= int(thresholdF) {
				cd, err := parseCooldownDuration(vsObj.Spec.CooldownPeriod)
				if err != nil {
					recorder.Eventf(invRef, corev1.EventTypeWarning, "InvalidCooldown",
						"CooldownPeriod '%s' invalid: %v", vsObj.Spec.CooldownPeriod, err)
					continue
				}
				okToScale, err := canScaleNow(vsObj.Status.ScaledAt, cd)
				if err != nil {
					recorder.Eventf(invRef, corev1.EventTypeWarning, "CooldownError",
						"Failed to check cooldownPeriod: %v", err)
					continue
				}
				if !okToScale {
					msg := fmt.Sprintf(
						"PVC '%s/%s' usage=%d%% >= threshold=%s, but in cooldown. Skipping expansion.",
						ns, pvcName, usagePercent, vsObj.Spec.Threshold)
					fmt.Printf("[INFO] %s\n", msg)
					recorder.Event(invRef, corev1.EventTypeNormal, "CooldownActive", msg)
					continue
				}

				// compute new size
				newSizeGi, err := computeNewSize(vsObj.Spec.Scale, vsObj.Spec.ScaleType, specSizeGi)
				if err != nil {
					recorder.Eventf(invRef, corev1.EventTypeWarning, "ScaleParseError",
						"Failed parsing scale '%s' with type '%s': %v",
						vsObj.Spec.Scale, vsObj.Spec.ScaleType, err)
					continue
				}
				if newSizeGi > maxSizeGi {
					newSizeGi = maxSizeGi
				}
				if newSizeGi <= specSizeGi {
					msg := fmt.Sprintf(
						"Computed newSize=%.0fGi <= current=%.0fGi. usage=%d%% => no net expansion.",
						newSizeGi, specSizeGi, usagePercent)
					fmt.Printf("[INFO] %s\n", msg)
					continue
				}

				newSizeStr := fmt.Sprintf("%.0fGi", newSizeGi)
				pvcPatch := []byte(fmt.Sprintf(`{"spec":{"resources":{"requests":{"storage":"%s"}}}}`, newSizeStr))
				_, patchErr := clientset.CoreV1().PersistentVolumeClaims(ns).Patch(
					ctx, pvcName, types.MergePatchType, pvcPatch, metav1.PatchOptions{})
				if patchErr != nil {
					msg := fmt.Sprintf(
						"Failed initiating expansion from %.0fGi -> %s: %v",
						specSizeGi, newSizeStr, patchErr)
					recorder.Event(invRef, corev1.EventTypeWarning, "ResizeFailed", msg)
					fmt.Printf("[ERROR] %s\n", msg)
					continue
				}

				succMsg := fmt.Sprintf(
					"Initiated resize of PVC '%s/%s' from %.0fGi -> %s. usage=%d%%, used=%dGi",
					ns, pvcName, specSizeGi, newSizeStr, usagePercent, usedGi)
				recorder.Event(invRef, corev1.EventTypeNormal, "ResizeRequested", succMsg)
				fmt.Printf("[INFO] %s\n", succMsg)

				nowStr := time.Now().UTC().Format(time.RFC3339)
				stPatch := []byte(fmt.Sprintf(
					`{"status":{"resizeInProgress":true,"lastRequestedSize":"%s","scaledAt":"%s"}}`,
					newSizeStr, nowStr))
				_, stErr := dynClient.Resource(gvr).Namespace(ns).
					Patch(ctx, vsUnstructMap[vsKey].Name, types.MergePatchType, stPatch, metav1.PatchOptions{}, "status")
				if stErr != nil {
					fmt.Printf("[ERROR] updating CR status after expansion request for VolumeScaler %s: %v\n", vsKey, stErr)
				}

			} else {
				msg := fmt.Sprintf("PVC '%s/%s' usage=%d%% < threshold=%s; no expansion needed.",
					ns, pvcName, usagePercent, vsObj.Spec.Threshold)
				fmt.Printf("[INFO] %s\n", msg)
			}
		}
		// wait 60s before next iteration
		time.Sleep(60 * time.Second)
	}
}

// ------------------------------------------------------------
// 9) main
// ------------------------------------------------------------
func main() {
	config, err := inClusterOrKubeconfig()
	if err != nil {
		panic(err.Error())
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err.Error())
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

	fmt.Println("Starting VolumeScaler operator...")
	mainLoop(clientset, dynClient, recorder, gvr)
}
