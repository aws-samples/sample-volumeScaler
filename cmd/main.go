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
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
)

// -----------------------------------------------------------------------------
// Annotation keys to define the "scaling" configuration
// Renamed to follow "volumescaler.autoscaling.storage.k8s.io/" group convention
// -----------------------------------------------------------------------------
const (
	annotationThreshold = "volumescaler.autoscaling.storage.k8s.io/threshold"      // e.g. "70%"
	annotationScale     = "volumescaler.autoscaling.storage.k8s.io/scale"          // e.g. "2Gi" or "30%"
	annotationScaleType = "volumescaler.autoscaling.storage.k8s.io/scaleType"      // e.g. "fixed" or "percentage"
	annotationCooldown  = "volumescaler.autoscaling.storage.k8s.io/cooldownPeriod" // e.g. "10m", "1h"
	annotationMaxSize   = "volumescaler.autoscaling.storage.k8s.io/maxSize"        // e.g. "20Gi"

	// "Status" annotations that the controller sets/updates
	annotationScaledAt         = "volumescaler.autoscaling.storage.k8s.io/scaledAt"
	annotationResizeInProgress = "volumescaler.autoscaling.storage.k8s.io/resizeInProgress"
	annotationReachedMax       = "volumescaler.autoscaling.storage.k8s.io/reachedMaxSize"
)

// -----------------------------------------------------------------------------
// convertToGi: converts e.g. "5Gi", "512Mi", or "1Ti" into float64 Gi
// -----------------------------------------------------------------------------
func convertToGi(sizeStr string) (float64, error) {
	var numberStr, unitStr string
	for i, r := range sizeStr {
		if r < '0' || r > '9' {
			numberStr = sizeStr[:i]
			unitStr = sizeStr[i:]
			break
		}
	}
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
		// unknown => treat as Gi
		return val, nil
	}
}

// -----------------------------------------------------------------------------
// inClusterOrKubeconfig: tries in-cluster config first, then KUBECONFIG
// -----------------------------------------------------------------------------
func inClusterOrKubeconfig() (*rest.Config, error) {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

// -----------------------------------------------------------------------------
// getPVCUIDsFromLocalMounts: returns slice of PVC UIDs + map PVC UID -> mountPath
// -----------------------------------------------------------------------------
func getPVCUIDsFromLocalMounts(kubeletPodsPath string, clientset kubernetes.Interface) ([]types.UID, map[types.UID]string) {
	pvcUIDSet := make(map[types.UID]struct{})
	pvcUIDToMount := make(map[types.UID]string)

	_ = filepath.Walk(kubeletPodsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Looking for subdirs named "mount" under CSI volumes
		if info.IsDir() && info.Name() == "mount" &&
			strings.Contains(path, filepath.Join("volumes", "kubernetes.io~csi")) {

			volumeDir := filepath.Base(filepath.Dir(path)) // e.g. "pvc-<uid>" or the PV name

			if strings.HasPrefix(volumeDir, "pvc-") {
				// name is "pvc-<UID>"
				pvcUIDStr := strings.TrimPrefix(volumeDir, "pvc-")
				if pvcUIDStr != "" {
					uid := types.UID(pvcUIDStr)
					pvcUIDSet[uid] = struct{}{}
					pvcUIDToMount[uid] = path
				}
			} else {
				// Possibly the PV name
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

// -----------------------------------------------------------------------------
// measureUsage: runs "df" on the mountPath -> usage% and usedGi
// -----------------------------------------------------------------------------
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

	usedBlocksStr := fields[2] // The "Used" column
	usedBlocks, err := strconv.ParseFloat(usedBlocksStr, 64)
	if err != nil {
		return -1, -1, err
	}

	// Typically df blocksize is 1K => usedBlocks => usedKB
	usedGiFloat := usedBlocks / (1024.0 * 1024.0)
	usagePercent := int((usedGiFloat / specSizeGi) * 100)
	usedGi := int(usedGiFloat + 0.5) // round
	return usagePercent, usedGi, nil
}

// -----------------------------------------------------------------------------
// parseCooldownDuration: returns 0 if empty
// -----------------------------------------------------------------------------
func parseCooldownDuration(cooldownStr string) (time.Duration, error) {
	if cooldownStr == "" {
		return 0, nil
	}
	return time.ParseDuration(cooldownStr)
}

// -----------------------------------------------------------------------------
// canScaleNow: checks if lastScaledAt + cooldown <= now
// -----------------------------------------------------------------------------
func canScaleNow(lastScaledAtStr string, cooldown time.Duration) (bool, error) {
	if cooldown == 0 {
		return true, nil
	}
	if lastScaledAtStr == "" {
		// never scaled
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

// -----------------------------------------------------------------------------
// computeNewSize:
//   if scaleType="fixed" => add Gi
//   if scaleType="percentage" => add that %
// -----------------------------------------------------------------------------
func computeNewSize(scale, scaleType string, currentSizeGi float64) (float64, error) {
	switch scaleType {
	case "fixed":
		// e.g. "2Gi"
		fixedInc, err := convertToGi(scale)
		if err != nil {
			return 0, fmt.Errorf("invalid fixed scale '%s': %v", scale, err)
		}
		return currentSizeGi + fixedInc, nil

	case "percentage":
		// e.g. "30%"
		scaleStr := strings.TrimSuffix(scale, "%")
		scaleF, err := strconv.ParseFloat(scaleStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid percentage scale '%s': %v", scale, err)
		}
		inc := currentSizeGi * (scaleF / 100.0)
		return currentSizeGi + inc, nil

	default:
		// fallback => interpret as percentage
		scaleStr := strings.TrimSuffix(scale, "%")
		scaleF, err := strconv.ParseFloat(scaleStr, 64)
		if err != nil {
			return 0, fmt.Errorf("unknown scaleType '%s' and parse failure on '%s': %v", scaleType, scale, err)
		}
		inc := currentSizeGi * (scaleF / 100.0)
		return currentSizeGi + inc, nil
	}
}

// -----------------------------------------------------------------------------
// updatePVCAnnotation updates a single annotation key in a PVC via PATCH
// -----------------------------------------------------------------------------
func updatePVCAnnotation(ctx context.Context, clientset *kubernetes.Clientset, pvc *corev1.PersistentVolumeClaim, key, value string) {
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}
	pvc.Annotations[key] = value
	patchData := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, key, value))
	_, err := clientset.CoreV1().PersistentVolumeClaims(pvc.Namespace).Patch(
		ctx, pvc.Name, types.MergePatchType, patchData, metav1.PatchOptions{},
	)
	if err != nil {
		fmt.Printf("[ERROR] Patching PVC annotation '%s=%s' for %s/%s failed: %v\n",
			key, value, pvc.Namespace, pvc.Name, err)
	}
}

// -----------------------------------------------------------------------------
// mainLoop
// -----------------------------------------------------------------------------
func mainLoop(clientset *kubernetes.Clientset, recorder record.EventRecorder) {
	for {
		ctx := context.Background()

		// (A) discover local PVCs on this node
		pvcUIDs, pvcUIDToMount := getPVCUIDsFromLocalMounts("/var/lib/kubelet/pods", clientset)
		if len(pvcUIDs) == 0 {
			fmt.Println("[INFO] No PVCs found on this node. Sleeping 60s...")
			time.Sleep(60 * time.Second)
			continue
		}

		// (B) list all PVCs in the cluster
		allPVCs, err := clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
		if err != nil {
			fmt.Printf("[ERROR] listing PVCs: %v\n", err)
			time.Sleep(60 * time.Second)
			continue
		}
		pvcMap := make(map[types.UID]*corev1.PersistentVolumeClaim, len(allPVCs.Items))
		for i := range allPVCs.Items {
			p := &allPVCs.Items[i]
			pvcMap[p.UID] = p
		}

		// (C) process each PVC that is on this node
		for _, uid := range pvcUIDs {
			pvc, ok := pvcMap[uid]
			if !ok {
				continue
			}
			ann := pvc.Annotations
			if ann == nil {
				continue
			}

			thresholdStr := ann[annotationThreshold] // e.g. "70%"
			scaleStr := ann[annotationScale]         // e.g. "2Gi" or "30%"
			scaleType := ann[annotationScaleType]    // e.g. "fixed" or "percentage"
			cooldownStr := ann[annotationCooldown]   // e.g. "10m"
			maxSizeStr := ann[annotationMaxSize]     // e.g. "20Gi"

			// Must have at least threshold & scale
			if thresholdStr == "" || scaleStr == "" {
				continue
			}

			// parse threshold
			thresholdF, err := strconv.ParseFloat(strings.TrimSuffix(thresholdStr, "%"), 64)
			if err != nil {
				msg := fmt.Sprintf("Invalid threshold annotation '%s': %v", thresholdStr, err)
				recorder.Eventf(pvc, corev1.EventTypeWarning, "InvalidThreshold", msg)
				continue
			}

			// parse maxSize (if present)
			var maxSizeGi float64
			if maxSizeStr != "" {
				maxSizeGi, err = convertToGi(maxSizeStr)
				if err != nil {
					msg := fmt.Sprintf("Invalid maxSize annotation '%s': %v", maxSizeStr, err)
					recorder.Eventf(pvc, corev1.EventTypeWarning, "InvalidMaxSize", msg)
					continue
				}
			}

			// parse current spec & status sizes
			specSizeGi, _ := convertToGi(pvc.Spec.Resources.Requests.Storage().String())
			statusSizeGi, _ := convertToGi(pvc.Status.Capacity.Storage().String())

			// measure usage
			mountPath := pvcUIDToMount[uid]
			usagePercent, usedGi, err := measureUsage(mountPath, specSizeGi)
			if err != nil {
				recorder.Eventf(pvc, corev1.EventTypeWarning, "MeasureFailed",
					"Failed to measure usage at '%s': %v", mountPath, err)
				continue
			}

			// check if we are at or beyond maxSize
			if maxSizeStr != "" &&
				specSizeGi >= maxSizeGi &&
				specSizeGi == statusSizeGi {

				// Mark as reachedMax
				updatePVCAnnotation(ctx, clientset, pvc, annotationReachedMax, "true")
				msg := fmt.Sprintf("PVC '%s/%s' reached maxSize=%.0fGi. usage=%d%% => no expansion needed.",
					pvc.Namespace, pvc.Name, maxSizeGi, usagePercent)
				recorder.Event(pvc, corev1.EventTypeNormal, "AtMaxSize", msg)
				fmt.Printf("[INFO] %s\n", msg)
				continue
			}

			inProgress := statusSizeGi < specSizeGi
			wasInProgress := (ann[annotationResizeInProgress] == "true")

			// if we *were* in progress, but now complete
			if wasInProgress && !inProgress {
				updatePVCAnnotation(ctx, clientset, pvc, annotationResizeInProgress, "false")
				nowStr := time.Now().UTC().Format(time.RFC3339)
				updatePVCAnnotation(ctx, clientset, pvc, annotationScaledAt, nowStr)

				if maxSizeStr != "" && specSizeGi >= maxSizeGi {
					updatePVCAnnotation(ctx, clientset, pvc, annotationReachedMax, "true")
				}

				msg := fmt.Sprintf("PVC '%s/%s' expansion complete. Capacity=%.0fGi, usage=%d%%.",
					pvc.Namespace, pvc.Name, statusSizeGi, usagePercent)
				recorder.Event(pvc, corev1.EventTypeNormal, "ResizeComplete", msg)
				fmt.Printf("[INFO] %s\n", msg)
				continue
			}

			// if still in progress
			if inProgress {
				msg := fmt.Sprintf("PVC '%s/%s' still resizing (Spec=%.0fGi, Status=%.0fGi). usage=%dGi (%d%%).",
					pvc.Namespace, pvc.Name, specSizeGi, statusSizeGi, usedGi, usagePercent)
				recorder.Event(pvc, corev1.EventTypeWarning, "StillResizing", msg)
				fmt.Printf("[INFO] %s\n", msg)
				continue
			}

			// usage vs threshold
			if usagePercent >= int(thresholdF) {
				// parse cooldown
				cd, err := parseCooldownDuration(cooldownStr)
				if err != nil {
					msg := fmt.Sprintf("Invalid cooldownPeriod '%s': %v", cooldownStr, err)
					recorder.Event(pvc, corev1.EventTypeWarning, "InvalidCooldown", msg)
					continue
				}
				// check scaledAt
				lastScaledAtStr := ann[annotationScaledAt]
				okToScale, err := canScaleNow(lastScaledAtStr, cd)
				if err != nil {
					recorder.Eventf(pvc, corev1.EventTypeWarning, "CooldownError",
						"Failed to check cooldownPeriod: %v", err)
					continue
				}
				if !okToScale {
					msg := fmt.Sprintf("PVC '%s/%s' usage=%d%% >= threshold=%s, but still in cooldown. Skipping expansion.",
						pvc.Namespace, pvc.Name, usagePercent, thresholdStr)
					fmt.Printf("[INFO] %s\n", msg)
					recorder.Event(pvc, corev1.EventTypeNormal, "InCooldown", msg)
					continue
				}

				// compute new size
				newSizeGi, err := computeNewSize(scaleStr, scaleType, specSizeGi)
				if err != nil {
					msg := fmt.Sprintf("Failed to parse scale '%s' with type '%s': %v", scaleStr, scaleType, err)
					recorder.Event(pvc, corev1.EventTypeWarning, "ScaleParseError", msg)
					continue
				}
				// enforce maxSize
				if maxSizeStr != "" && newSizeGi > maxSizeGi {
					newSizeGi = maxSizeGi
				}
				// if no net expansion
				if newSizeGi <= specSizeGi {
					msg := fmt.Sprintf("No net expansion (newSize=%.0fGi <= current=%.0fGi). usage=%d%% => skip.",
						newSizeGi, specSizeGi, usagePercent)
					fmt.Printf("[INFO] %s\n", msg)
					continue
				}

				// Patch the PVC spec
				newSizeStr := fmt.Sprintf("%.0fGi", newSizeGi)
				pvcPatch := []byte(fmt.Sprintf(`{"spec":{"resources":{"requests":{"storage":"%s"}}}}`, newSizeStr))
				_, patchErr := clientset.CoreV1().PersistentVolumeClaims(pvc.Namespace).Patch(
					ctx, pvc.Name, types.MergePatchType, pvcPatch, metav1.PatchOptions{},
				)
				if patchErr != nil {
					msg := fmt.Sprintf("Failed to initiate expansion from %.0fGi -> %s: %v",
						specSizeGi, newSizeStr, patchErr)
					recorder.Event(pvc, corev1.EventTypeWarning, "ResizeFailed", msg)
					fmt.Printf("[ERROR] %s\n", msg)
					continue
				}

				succMsg := fmt.Sprintf("Initiated resize of PVC '%s/%s' from %.0fGi -> %s. usage=%d%%, used=%dGi",
					pvc.Namespace, pvc.Name, specSizeGi, newSizeStr, usagePercent, usedGi)
				recorder.Event(pvc, corev1.EventTypeNormal, "ResizeRequested", succMsg)
				fmt.Printf("[INFO] %s\n", succMsg)

				// update annotation status
				nowStr := time.Now().UTC().Format(time.RFC3339)
				updatePVCAnnotation(ctx, clientset, pvc, annotationResizeInProgress, "true")
				updatePVCAnnotation(ctx, clientset, pvc, annotationScaledAt, nowStr)

			} else {
				msg := fmt.Sprintf("PVC '%s/%s' usage=%d%% < threshold=%s; no expansion needed.",
					pvc.Namespace, pvc.Name, usagePercent, thresholdStr)
				fmt.Printf("[INFO] %s\n", msg)
			}
		}

		time.Sleep(60 * time.Second)
	}
}

// -----------------------------------------------------------------------------
// main - sets up the client, scheme, event recorder, then loops
// -----------------------------------------------------------------------------
func main() {
	config, err := inClusterOrKubeconfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// Make sure the core APIs (like PVC) are registered in the scheme
	// so the event recorder can create a proper reference
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))

	// Setup event broadcaster & recorder
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: clientset.CoreV1().Events(""),
	})

	// Use the global scheme (which has corev1)
	recorder := broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "pvc-volumescaler"})

	fmt.Println("Starting PVC-annotation-based VolumeScaler...")

	// main loop
	mainLoop(clientset, recorder)
}
