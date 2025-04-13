package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
)

func TestReconcilePVC(t *testing.T) {
	// Save original measureUsage function and restore it after test
	originalMeasureUsage := measureUsageFunc
	defer func() {
		measureUsageFunc = originalMeasureUsage
	}()

	// Mock measureUsage function
	measureUsageFunc = func(mountPath string, specSizeGi float64) (int, int, error) {
		return 80, int(specSizeGi * 0.8), nil // Return 80% usage
	}

	// Create fake clients
	clientset := kfake.NewSimpleClientset()
	dynClient := dfake.NewSimpleDynamicClient(runtime.NewScheme())
	recorder := record.NewFakeRecorder(100)

	// Create test controller
	controller := &VolumeScalerController{
		config:    NewDefaultConfig(),
		clientset: clientset,
		dynClient: dynClient,
		recorder:  recorder,
		gvr: schema.GroupVersionResource{
			Group:    "autoscaling.storage.k8s.io",
			Version:  "v1alpha1",
			Resource: "volumescalers",
		},
	}

	// Test cases
	tests := []struct {
		name          string
		pvc           *corev1.PersistentVolumeClaim
		vs            *VolumeScaler
		vsName        types.NamespacedName
		mountPath     string
		expectError   bool
		expectResize  bool
		expectMaxSize bool
	}{
		{
			name: "normal resize case",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("5Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
			vs: &VolumeScaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
					Kind:       "VolumeScaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vs",
					Namespace: "default",
				},
				Spec: VolumeScalerSpec{
					PVCName:        "test-pvc",
					Threshold:      "70%",
					Scale:          "2Gi",
					ScaleType:      "fixed",
					CooldownPeriod: "5m",
					MaxSize:        "10Gi",
				},
				Status: VolumeScalerStatus{
					ScaledAt:         "",
					ReachedMaxSize:   false,
					ResizeInProgress: false,
				},
			},
			vsName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-vs",
			},
			mountPath:     "/test/mount/path",
			expectError:   false,
			expectResize:  true,
			expectMaxSize: false,
		},
		{
			name: "at max size",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc-max",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			vs: &VolumeScaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
					Kind:       "VolumeScaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vs-max",
					Namespace: "default",
				},
				Spec: VolumeScalerSpec{
					PVCName:        "test-pvc-max",
					Threshold:      "70%",
					Scale:          "2Gi",
					ScaleType:      "fixed",
					CooldownPeriod: "5m",
					MaxSize:        "10Gi",
				},
				Status: VolumeScalerStatus{
					ScaledAt:         "",
					ReachedMaxSize:   false,
					ResizeInProgress: false,
				},
			},
			vsName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-vs-max",
			},
			mountPath:     "/test/mount/path",
			expectError:   false,
			expectResize:  false,
			expectMaxSize: true,
		},
		{
			name: "invalid threshold",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc-invalid",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("5Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
			vs: &VolumeScaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
					Kind:       "VolumeScaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vs-invalid",
					Namespace: "default",
				},
				Spec: VolumeScalerSpec{
					PVCName:        "test-pvc-invalid",
					Threshold:      "invalid%",
					Scale:          "2Gi",
					ScaleType:      "fixed",
					CooldownPeriod: "5m",
					MaxSize:        "10Gi",
				},
			},
			vsName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-vs-invalid",
			},
			mountPath:     "/test/mount/path",
			expectError:   true,
			expectResize:  false,
			expectMaxSize: false,
		},
		{
			name: "invalid max size",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc-invalid-max",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("5Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
			vs: &VolumeScaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
					Kind:       "VolumeScaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vs-invalid-max",
					Namespace: "default",
				},
				Spec: VolumeScalerSpec{
					PVCName:        "test-pvc-invalid-max",
					Threshold:      "70%",
					Scale:          "2Gi",
					ScaleType:      "fixed",
					CooldownPeriod: "5m",
					MaxSize:        "invalid",
				},
			},
			vsName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-vs-invalid-max",
			},
			mountPath:     "/test/mount/path",
			expectError:   true,
			expectResize:  false,
			expectMaxSize: false,
		},
		{
			name: "resize in progress",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc-resize",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("7Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
			vs: &VolumeScaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
					Kind:       "VolumeScaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vs-resize",
					Namespace: "default",
				},
				Spec: VolumeScalerSpec{
					PVCName:        "test-pvc-resize",
					Threshold:      "70%",
					Scale:          "2Gi",
					ScaleType:      "fixed",
					CooldownPeriod: "5m",
					MaxSize:        "10Gi",
				},
				Status: VolumeScalerStatus{
					ResizeInProgress: true,
				},
			},
			vsName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-vs-resize",
			},
			mountPath:     "/test/mount/path",
			expectError:   false,
			expectResize:  false,
			expectMaxSize: false,
		},
		{
			name: "in cooldown period",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc-cooldown",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("5Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
			vs: &VolumeScaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
					Kind:       "VolumeScaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vs-cooldown",
					Namespace: "default",
				},
				Spec: VolumeScalerSpec{
					PVCName:        "test-pvc-cooldown",
					Threshold:      "70%",
					Scale:          "2Gi",
					ScaleType:      "fixed",
					CooldownPeriod: "10m",
					MaxSize:        "10Gi",
				},
				Status: VolumeScalerStatus{
					ScaledAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
				},
			},
			vsName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-vs-cooldown",
			},
			mountPath:     "/test/mount/path",
			expectError:   false,
			expectResize:  false,
			expectMaxSize: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert VolumeScaler to unstructured for the dynamic client
			vsUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tt.vs)
			if err != nil {
				t.Fatalf("Failed to convert VolumeScaler to unstructured: %v", err)
			}

			// Create the objects in the fake clients
			_, err = clientset.CoreV1().PersistentVolumeClaims(tt.pvc.Namespace).Create(context.TODO(), tt.pvc, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Failed to create PVC: %v", err)
			}

			_, err = dynClient.Resource(controller.gvr).Namespace(tt.vs.Namespace).Create(
				context.TODO(),
				&unstructured.Unstructured{Object: vsUnstructured},
				metav1.CreateOptions{},
			)
			if err != nil {
				t.Fatalf("Failed to create VolumeScaler: %v", err)
			}

			// Call reconcilePVC
			err = controller.reconcilePVC(context.TODO(), tt.pvc, tt.vs, tt.vsName, tt.mountPath)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("reconcilePVC() error = %v, expectError %v", err, tt.expectError)
			}

			// Get the updated PVC
			updatedPVC, err := clientset.CoreV1().PersistentVolumeClaims(tt.pvc.Namespace).Get(context.TODO(), tt.pvc.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed to get updated PVC: %v", err)
			}

			// Check if resize was initiated
			if tt.expectResize {
				currentSize := updatedPVC.Spec.Resources.Requests[corev1.ResourceStorage]
				originalSize := tt.pvc.Spec.Resources.Requests[corev1.ResourceStorage]
				if currentSize.Cmp(originalSize) <= 0 {
					t.Errorf("Expected PVC size to increase, but it didn't. Original: %v, Current: %v", originalSize, currentSize)
				}
			}

			// Get the updated VolumeScaler
			result, err := dynClient.Resource(controller.gvr).Namespace(tt.vs.Namespace).Get(context.TODO(), tt.vs.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed to get updated VolumeScaler: %v", err)
			}

			var updatedVS VolumeScaler
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(result.Object, &updatedVS)
			if err != nil {
				t.Fatalf("Failed to convert unstructured to VolumeScaler: %v", err)
			}

			// Check max size status
			if tt.expectMaxSize && !updatedVS.Status.ReachedMaxSize {
				t.Error("Expected VolumeScaler to be marked as reached max size, but it wasn't")
			}
		})
	}
}

func TestNewVolumeScalerController(t *testing.T) {
	config := NewDefaultConfig()
	clientset := kfake.NewSimpleClientset()
	dynClient := dfake.NewSimpleDynamicClient(runtime.NewScheme())
	recorder := record.NewFakeRecorder(100)
	gvr := schema.GroupVersionResource{
		Group:    "autoscaling.storage.k8s.io",
		Version:  "v1alpha1",
		Resource: "volumescalers",
	}

	controller := NewVolumeScalerController(config, clientset, dynClient, recorder, gvr)

	if controller == nil {
		t.Fatal("Expected non-nil controller")
	}
	if controller.config != config {
		t.Error("Expected config to match")
	}
	if controller.clientset != clientset {
		t.Error("Expected clientset to match")
	}
	if controller.dynClient != dynClient {
		t.Error("Expected dynClient to match")
	}
	if controller.recorder != recorder {
		t.Error("Expected recorder to match")
	}
	if controller.gvr != gvr {
		t.Error("Expected gvr to match")
	}
}

func TestNewDefaultConfig(t *testing.T) {
	config := NewDefaultConfig()

	if config.KubeletPodsPath != defaultKubeletPodsPath {
		t.Errorf("Expected KubeletPodsPath %s, got %s", defaultKubeletPodsPath, config.KubeletPodsPath)
	}
	if config.PollInterval != defaultPollInterval {
		t.Errorf("Expected PollInterval %v, got %v", defaultPollInterval, config.PollInterval)
	}
	if config.MaxRetries != defaultMaxRetries {
		t.Errorf("Expected MaxRetries %d, got %d", defaultMaxRetries, config.MaxRetries)
	}
	if config.Timeout != defaultTimeout {
		t.Errorf("Expected Timeout %v, got %v", defaultTimeout, config.Timeout)
	}
}

func TestGetPVCUIDsFromLocalMounts(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test-mounts")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test directory structure
	testCases := []struct {
		name     string
		setup    func() error
		expected int
	}{
		{
			name: "no mounts",
			setup: func() error {
				return nil
			},
			expected: 0,
		},
		{
			name: "valid PVC mount",
			setup: func() error {
				podPath := filepath.Join(tempDir, "pod1")
				volumePath := filepath.Join(podPath, "volumes", "kubernetes.io~csi", "pvc-12345678")
				mountPath := filepath.Join(volumePath, "mount")
				return os.MkdirAll(mountPath, 0755)
			},
			expected: 1,
		},
		{
			name: "multiple PVC mounts",
			setup: func() error {
				pod1Path := filepath.Join(tempDir, "pod1")
				volume1Path := filepath.Join(pod1Path, "volumes", "kubernetes.io~csi", "pvc-12345678")
				mount1Path := filepath.Join(volume1Path, "mount")
				if err := os.MkdirAll(mount1Path, 0755); err != nil {
					return err
				}

				pod2Path := filepath.Join(tempDir, "pod2")
				volume2Path := filepath.Join(pod2Path, "volumes", "kubernetes.io~csi", "pvc-87654321")
				mount2Path := filepath.Join(volume2Path, "mount")
				return os.MkdirAll(mount2Path, 0755)
			},
			expected: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clean up temp dir before each test
			os.RemoveAll(tempDir)
			os.MkdirAll(tempDir, 0755)

			if err := tc.setup(); err != nil {
				t.Fatalf("Failed to setup test case: %v", err)
			}

			// Create fake clientset
			clientset := kfake.NewSimpleClientset()
			pvcUIDs, pvcUIDToMount, err := getPVCUIDsFromLocalMounts(tempDir, clientset)
			if err != nil {
				t.Fatalf("getPVCUIDsFromLocalMounts failed: %v", err)
			}

			if len(pvcUIDs) != tc.expected {
				t.Errorf("Expected %d PVC UIDs, got %d", tc.expected, len(pvcUIDs))
			}
			if len(pvcUIDToMount) != tc.expected {
				t.Errorf("Expected %d PVC mount paths, got %d", tc.expected, len(pvcUIDToMount))
			}
		})
	}
}

func TestMeasureUsage(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test-usage")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file to simulate disk usage
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test data"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	testCases := []struct {
		name          string
		mountPath     string
		specSizeGi    float64
		expectedUsage int
		expectedUsed  int
		wantErr       bool
	}{
		{
			name:          "valid mount path",
			mountPath:     tempDir,
			specSizeGi:    1.0,
			expectedUsage: 0, // Should be very small
			expectedUsed:  0, // Should be very small
			wantErr:       false,
		},
		{
			name:          "non-existent path",
			mountPath:     "/non/existent/path",
			specSizeGi:    1.0,
			expectedUsage: -1,
			expectedUsed:  -1,
			wantErr:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			usage, used, err := measureUsage(tc.mountPath, tc.specSizeGi)
			if (err != nil) != tc.wantErr {
				t.Errorf("measureUsage() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if !tc.wantErr {
				if usage < 0 {
					t.Errorf("measureUsage() usage = %v, want >= 0", usage)
				}
				if used < 0 {
					t.Errorf("measureUsage() used = %v, want >= 0", used)
				}
			}
		})
	}
}

func TestCheckAndHandleResizeFailedEvents(t *testing.T) {
	now := metav1.Now()
	testCases := []struct {
		name        string
		events      []corev1.Event
		expectedMsg string
	}{
		{
			name: "no resize failed events",
			events: []corev1.Event{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "event-1",
					},
					Type:    corev1.EventTypeNormal,
					Reason:  "SomeOtherReason",
					Message: "Some message",
				},
			},
			expectedMsg: "",
		},
		{
			name: "single resize failed event",
			events: []corev1.Event{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "event-2",
					},
					Type:          corev1.EventTypeWarning,
					Reason:        "VolumeResizeFailed",
					Message:       "Failed to resize volume",
					LastTimestamp: now,
				},
			},
			expectedMsg: "Failed to resize volume",
		},
		{
			name: "multiple resize failed events",
			events: []corev1.Event{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "event-3",
					},
					Type:          corev1.EventTypeWarning,
					Reason:        "VolumeResizeFailed",
					Message:       "Older failed message",
					LastTimestamp: metav1.NewTime(now.Add(-1 * time.Hour)),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "event-4",
					},
					Type:          corev1.EventTypeWarning,
					Reason:        "VolumeResizeFailed",
					Message:       "Newer failed message",
					LastTimestamp: now,
				},
			},
			expectedMsg: "Newer failed message",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create fake clientset with events
			clientset := kfake.NewSimpleClientset()
			for _, ev := range tc.events {
				_, err := clientset.CoreV1().Events("default").Create(context.TODO(), &ev, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create event: %v", err)
				}
			}

			msg := checkAndHandleResizeFailedEvents(context.TODO(), clientset, "test-pvc", "default")
			if msg != tc.expectedMsg {
				t.Errorf("Expected message '%s', got '%s'", tc.expectedMsg, msg)
			}
		})
	}
}

func TestMakeInvolvedObjectRef(t *testing.T) {
	testCases := []struct {
		name     string
		vsName   types.NamespacedName
		vsObj    *VolumeScaler
		expected *corev1.ObjectReference
	}{
		{
			name: "basic volume scaler",
			vsName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-vs",
			},
			vsObj: &VolumeScaler{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
					Kind:       "VolumeScaler",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vs",
					Namespace: "default",
					UID:       "12345678",
				},
				Spec: VolumeScalerSpec{
					PVCName:        "test-pvc",
					Threshold:      "70%",
					Scale:          "2Gi",
					ScaleType:      "fixed",
					CooldownPeriod: "5m",
					MaxSize:        "10Gi",
				},
			},
			expected: &corev1.ObjectReference{
				APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
				Kind:       "VolumeScaler",
				Namespace:  "default",
				Name:       "test-vs",
				UID:        "12345678",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Convert to unstructured and back to ensure TypeMeta is set correctly
			unstr, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tc.vsObj)
			if err != nil {
				t.Fatalf("Failed to convert to unstructured: %v", err)
			}
			unstr["apiVersion"] = tc.vsObj.TypeMeta.APIVersion
			unstr["kind"] = tc.vsObj.TypeMeta.Kind

			vsObj := &VolumeScaler{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstr, vsObj)
			if err != nil {
				t.Fatalf("Failed to convert from unstructured: %v", err)
			}

			ref := makeInvolvedObjectRef(tc.vsName, vsObj)
			if ref.APIVersion != tc.expected.APIVersion {
				t.Errorf("Expected APIVersion %s, got %s", tc.expected.APIVersion, ref.APIVersion)
			}
			if ref.Kind != tc.expected.Kind {
				t.Errorf("Expected Kind %s, got %s", tc.expected.Kind, ref.Kind)
			}
			if ref.Namespace != tc.expected.Namespace {
				t.Errorf("Expected Namespace %s, got %s", tc.expected.Namespace, ref.Namespace)
			}
			if ref.Name != tc.expected.Name {
				t.Errorf("Expected Name %s, got %s", tc.expected.Name, ref.Name)
			}
			if ref.UID != tc.expected.UID {
				t.Errorf("Expected UID %s, got %s", tc.expected.UID, ref.UID)
			}
		})
	}
}

func TestVolumeScalerController_Run(t *testing.T) {
	config := NewDefaultConfig()
	config.PollInterval = 100 * time.Millisecond // Shorter interval for testing
	clientset := kfake.NewSimpleClientset()
	dynClient := dfake.NewSimpleDynamicClient(runtime.NewScheme())
	recorder := record.NewFakeRecorder(100)
	gvr := schema.GroupVersionResource{
		Group:    "autoscaling.storage.k8s.io",
		Version:  "v1alpha1",
		Resource: "volumescalers",
	}

	controller := NewVolumeScalerController(config, clientset, dynClient, recorder, gvr)

	// Create a context that will be cancelled after a short time
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run the controller
	err := controller.Run(ctx)
	if err != nil {
		t.Errorf("Run() error = %v", err)
	}
}

func TestVolumeScalerController_ReconcileLoop(t *testing.T) {
	// Save original measureUsage function and restore it after test
	originalMeasureUsage := measureUsageFunc
	defer func() {
		measureUsageFunc = originalMeasureUsage
	}()

	// Mock measureUsage function
	measureUsageFunc = func(mountPath string, specSizeGi float64) (int, int, error) {
		return 80, int(specSizeGi * 0.8), nil // Return 80% usage
	}

	// Create test PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "default",
			UID:       "12345678",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("5Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("5Gi"),
			},
		},
	}

	// Create test VolumeScaler
	vs := &VolumeScaler{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "autoscaling.storage.k8s.io/v1alpha1",
			Kind:       "VolumeScaler",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-vs",
			Namespace: "default",
		},
		Spec: VolumeScalerSpec{
			PVCName:        "test-pvc",
			Threshold:      "70%",
			Scale:          "2Gi",
			ScaleType:      "fixed",
			CooldownPeriod: "5m",
			MaxSize:        "10Gi",
		},
	}

	// Convert VolumeScaler to unstructured
	unstr, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vs)
	if err != nil {
		t.Fatalf("Failed to convert VolumeScaler to unstructured: %v", err)
	}

	// Create fake clients
	clientset := kfake.NewSimpleClientset(pvc)
	dynClient := dfake.NewSimpleDynamicClient(runtime.NewScheme(), &unstructured.Unstructured{Object: unstr})
	recorder := record.NewFakeRecorder(100)

	// Create test controller
	controller := &VolumeScalerController{
		config:    NewDefaultConfig(),
		clientset: clientset,
		dynClient: dynClient,
		recorder:  recorder,
		gvr: schema.GroupVersionResource{
			Group:    "autoscaling.storage.k8s.io",
			Version:  "v1alpha1",
			Resource: "volumescalers",
		},
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test-mounts")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test directory structure
	podPath := filepath.Join(tempDir, "pod1")
	volumePath := filepath.Join(podPath, "volumes", "kubernetes.io~csi", "pvc-12345678")
	mountPath := filepath.Join(volumePath, "mount")
	if err := os.MkdirAll(mountPath, 0755); err != nil {
		t.Fatalf("Failed to create mount path: %v", err)
	}

	// Set kubelet pods path
	controller.config.KubeletPodsPath = tempDir

	// Run reconcile loop
	err = controller.reconcileLoop(context.Background())
	if err != nil {
		t.Errorf("reconcileLoop() error = %v", err)
	}

	// Verify that the PVC was updated
	updatedPVC, err := clientset.CoreV1().PersistentVolumeClaims("default").Get(context.Background(), "test-pvc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get updated PVC: %v", err)
	}

	// Check if the PVC size was increased
	newSize := updatedPVC.Spec.Resources.Requests[corev1.ResourceStorage]
	if newSize.String() != "7Gi" {
		t.Errorf("Expected PVC size to be 7Gi, got %s", newSize.String())
	}
}

func TestInClusterOrKubeconfig(t *testing.T) {
	// Save original environment and restore it after test
	originalKubeconfig := os.Getenv("KUBECONFIG")
	defer os.Setenv("KUBECONFIG", originalKubeconfig)

	// Create a temporary kubeconfig file
	tempKubeconfig, err := os.CreateTemp("", "kubeconfig")
	if err != nil {
		t.Fatalf("Failed to create temp kubeconfig: %v", err)
	}
	defer os.Remove(tempKubeconfig.Name())

	// Write a minimal kubeconfig
	kubeconfigContent := `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://localhost:8443
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
	if err := os.WriteFile(tempKubeconfig.Name(), []byte(kubeconfigContent), 0644); err != nil {
		t.Fatalf("Failed to write kubeconfig: %v", err)
	}

	// Test with KUBECONFIG set
	os.Setenv("KUBECONFIG", tempKubeconfig.Name())
	config, err := inClusterOrKubeconfig()
	if err != nil {
		t.Errorf("inClusterOrKubeconfig() with KUBECONFIG error = %v", err)
	}
	if config == nil {
		t.Error("inClusterOrKubeconfig() with KUBECONFIG returned nil config")
	}
	if config != nil && config.Host != "https://localhost:8443" {
		t.Errorf("inClusterOrKubeconfig() with KUBECONFIG got host %s, want https://localhost:8443", config.Host)
	}

	// Test without KUBECONFIG set
	os.Unsetenv("KUBECONFIG")
	config, err = inClusterOrKubeconfig()
	if err == nil {
		t.Error("inClusterOrKubeconfig() without KUBECONFIG should return error when not in cluster")
	}
}
