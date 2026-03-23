package main

import (
	"context"
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
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
)

func TestReconcilePVC(t *testing.T) {
	// Create fake clients
	clientset := kfake.NewSimpleClientset()
	dynClient := dfake.NewSimpleDynamicClient(runtime.NewScheme())
	recorder := record.NewFakeRecorder(100)

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

	tests := []struct {
		name          string
		pvc           *corev1.PersistentVolumeClaim
		vs            *VolumeScaler
		vsName        types.NamespacedName
		usageInfo     *PVCUsageInfo
		expectError   bool
		expectResize  bool
		expectMaxSize bool
	}{
		{
			name: "normal resize case",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc", Namespace: "default"},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
				},
			},
			vs: &VolumeScaler{
				TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling.storage.k8s.io/v1alpha1", Kind: "VolumeScaler"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-vs", Namespace: "default"},
				Spec:       VolumeScalerSpec{PVCName: "test-pvc", Threshold: "70%", Scale: "2Gi", ScaleType: "fixed", CooldownPeriod: "5m", MaxSize: "10Gi"},
			},
			vsName:    types.NamespacedName{Namespace: "default", Name: "test-vs"},
			usageInfo: &PVCUsageInfo{UsedBytes: 4 * 1024 * 1024 * 1024, CapacityBytes: 5 * 1024 * 1024 * 1024, AvailableBytes: 1 * 1024 * 1024 * 1024, UsagePercent: 80, UsedGi: 4.0},
			expectResize: true,
		},
		{
			name: "at max size",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc-max", Namespace: "default"},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
				},
			},
			vs: &VolumeScaler{
				TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling.storage.k8s.io/v1alpha1", Kind: "VolumeScaler"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-vs-max", Namespace: "default"},
				Spec:       VolumeScalerSpec{PVCName: "test-pvc-max", Threshold: "70%", Scale: "2Gi", ScaleType: "fixed", CooldownPeriod: "5m", MaxSize: "10Gi"},
			},
			vsName:        types.NamespacedName{Namespace: "default", Name: "test-vs-max"},
			usageInfo:     &PVCUsageInfo{UsedBytes: 8 * 1024 * 1024 * 1024, CapacityBytes: 10 * 1024 * 1024 * 1024, AvailableBytes: 2 * 1024 * 1024 * 1024, UsagePercent: 80, UsedGi: 8.0},
			expectMaxSize: true,
		},
		{
			name: "invalid threshold",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc-invalid", Namespace: "default"},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
				},
			},
			vs: &VolumeScaler{
				TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling.storage.k8s.io/v1alpha1", Kind: "VolumeScaler"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-vs-invalid", Namespace: "default"},
				Spec:       VolumeScalerSpec{PVCName: "test-pvc-invalid", Threshold: "invalid%", Scale: "2Gi", ScaleType: "fixed", CooldownPeriod: "5m", MaxSize: "10Gi"},
			},
			vsName:      types.NamespacedName{Namespace: "default", Name: "test-vs-invalid"},
			usageInfo:   &PVCUsageInfo{UsedBytes: 4 * 1024 * 1024 * 1024, CapacityBytes: 5 * 1024 * 1024 * 1024, UsagePercent: 80, UsedGi: 4.0},
			expectError: true,
		},
		{
			name: "invalid max size",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc-invalid-max", Namespace: "default"},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
				},
			},
			vs: &VolumeScaler{
				TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling.storage.k8s.io/v1alpha1", Kind: "VolumeScaler"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-vs-invalid-max", Namespace: "default"},
				Spec:       VolumeScalerSpec{PVCName: "test-pvc-invalid-max", Threshold: "70%", Scale: "2Gi", ScaleType: "fixed", CooldownPeriod: "5m", MaxSize: "invalid"},
			},
			vsName:      types.NamespacedName{Namespace: "default", Name: "test-vs-invalid-max"},
			usageInfo:   &PVCUsageInfo{UsedBytes: 4 * 1024 * 1024 * 1024, CapacityBytes: 5 * 1024 * 1024 * 1024, UsagePercent: 80, UsedGi: 4.0},
			expectError: true,
		},
		{
			name: "resize in progress",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc-resize", Namespace: "default"},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("7Gi")},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
				},
			},
			vs: &VolumeScaler{
				TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling.storage.k8s.io/v1alpha1", Kind: "VolumeScaler"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-vs-resize", Namespace: "default"},
				Spec:       VolumeScalerSpec{PVCName: "test-pvc-resize", Threshold: "70%", Scale: "2Gi", ScaleType: "fixed", CooldownPeriod: "5m", MaxSize: "10Gi"},
				Status:     VolumeScalerStatus{ResizeInProgress: true},
			},
			vsName:    types.NamespacedName{Namespace: "default", Name: "test-vs-resize"},
			usageInfo: &PVCUsageInfo{UsedBytes: 4 * 1024 * 1024 * 1024, CapacityBytes: 5 * 1024 * 1024 * 1024, UsagePercent: 80, UsedGi: 4.0},
		},
		{
			name: "in cooldown period",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc-cooldown", Namespace: "default"},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
				},
			},
			vs: &VolumeScaler{
				TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling.storage.k8s.io/v1alpha1", Kind: "VolumeScaler"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-vs-cooldown", Namespace: "default"},
				Spec:       VolumeScalerSpec{PVCName: "test-pvc-cooldown", Threshold: "70%", Scale: "2Gi", ScaleType: "fixed", CooldownPeriod: "10m", MaxSize: "10Gi"},
				Status:     VolumeScalerStatus{ScaledAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339)},
			},
			vsName:    types.NamespacedName{Namespace: "default", Name: "test-vs-cooldown"},
			usageInfo: &PVCUsageInfo{UsedBytes: 4 * 1024 * 1024 * 1024, CapacityBytes: 5 * 1024 * 1024 * 1024, UsagePercent: 80, UsedGi: 4.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vsUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tt.vs)
			if err != nil {
				t.Fatalf("Failed to convert VolumeScaler to unstructured: %v", err)
			}

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

			err = controller.reconcilePVC(context.TODO(), tt.pvc, tt.vs, tt.vsName, tt.usageInfo)

			if (err != nil) != tt.expectError {
				t.Errorf("reconcilePVC() error = %v, expectError %v", err, tt.expectError)
			}

			updatedPVC, err := clientset.CoreV1().PersistentVolumeClaims(tt.pvc.Namespace).Get(context.TODO(), tt.pvc.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed to get updated PVC: %v", err)
			}

			if tt.expectResize {
				currentSize := updatedPVC.Spec.Resources.Requests[corev1.ResourceStorage]
				originalSize := tt.pvc.Spec.Resources.Requests[corev1.ResourceStorage]
				if currentSize.Cmp(originalSize) <= 0 {
					t.Errorf("Expected PVC size to increase, but it didn't. Original: %v, Current: %v", originalSize, currentSize)
				}
			}

			result, err := dynClient.Resource(controller.gvr).Namespace(tt.vs.Namespace).Get(context.TODO(), tt.vs.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed to get updated VolumeScaler: %v", err)
			}

			var updatedVS VolumeScaler
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(result.Object, &updatedVS)
			if err != nil {
				t.Fatalf("Failed to convert unstructured to VolumeScaler: %v", err)
			}

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

func TestReconcileLoop_WithMockedUsage(t *testing.T) {
	// Save and restore original
	originalFetch := fetchNodePVCUsageFunc
	defer func() { fetchNodePVCUsageFunc = originalFetch }()

	// Mock the usage fetcher to return 80% usage for our test PVC
	fetchNodePVCUsageFunc = func(ctx context.Context, clientset kubernetes.Interface, nodeName string) (map[string]*PVCUsageInfo, error) {
		return map[string]*PVCUsageInfo{
			"default/test-pvc": {
				UsedBytes:      4 * 1024 * 1024 * 1024,
				CapacityBytes:  5 * 1024 * 1024 * 1024,
				AvailableBytes: 1 * 1024 * 1024 * 1024,
				UsagePercent:   80,
				UsedGi:         4.0,
			},
		}, nil
	}

	// Create test PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pvc", Namespace: "default", UID: "12345678"},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
		},
	}

	// Create test VolumeScaler
	vs := &VolumeScaler{
		TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling.storage.k8s.io/v1alpha1", Kind: "VolumeScaler"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-vs", Namespace: "default"},
		Spec:       VolumeScalerSpec{PVCName: "test-pvc", Threshold: "70%", Scale: "2Gi", ScaleType: "fixed", CooldownPeriod: "5m", MaxSize: "10Gi"},
	}

	unstr, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vs)
	if err != nil {
		t.Fatalf("Failed to convert VolumeScaler to unstructured: %v", err)
	}

	clientset := kfake.NewSimpleClientset(pvc)
	dynClient := dfake.NewSimpleDynamicClient(runtime.NewScheme(), &unstructured.Unstructured{Object: unstr})
	recorder := record.NewFakeRecorder(100)

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

	// Set NODE_NAME_ENV for the reconcile loop
	t.Setenv("NODE_NAME_ENV", "test-node")

	err = controller.reconcileLoop(context.Background())
	if err != nil {
		t.Errorf("reconcileLoop() error = %v", err)
	}

	// Verify that the PVC was updated
	updatedPVC, err := clientset.CoreV1().PersistentVolumeClaims("default").Get(context.Background(), "test-pvc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get updated PVC: %v", err)
	}

	newSize := updatedPVC.Spec.Resources.Requests[corev1.ResourceStorage]
	if newSize.String() != "7Gi" {
		t.Errorf("Expected PVC size to be 7Gi, got %s", newSize.String())
	}
}
