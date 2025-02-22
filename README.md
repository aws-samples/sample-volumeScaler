# VolumeScaler

**VolumeScaler** is a Kubernetes controller that automatically scales PersistentVolumeClaims (PVCs) when a specified utilization threshold is reached. It is implemented as a DaemonSet running on each node, monitoring disk usage for PVCs mounted by pods on that node, and dynamically adjusting the PVC request size up to a defined maximum.

## current challenges

When deploying volume in k8s we normally facing the following challenges.

- Under-provisioning → system goes down
- Over-provisioning → wasted cost
- keep watching it every few hours/days to take an actions.

## Prerequisites

- Kubernetes cluster with a storage provider that supports online volume expansion (e.g., EBS CSI driver)
- A StorageClass that enables volume expansion
- RBAC permissions allowing the VolumeScaler DaemonSet to list pods, PVCs, and VolumeScaler CRs, and patch their status

## Installation

### Install with kubectl

   ```bash
   git clone git@github.com:zghanem0/AmazonVolumeScaler.git
   cd AmazonVolumeScaler && kubectl apply -f volumescaler.yaml
   ```

### Install with Helm

  ```bash
  helm repo add amazonvolumescaler https://zghanem0.github.io/AmazonVolumeScaler
  helm repo update                                                                 
  helm upgrade --install my-release amazonvolumescaler/volumescaler --version 0.1.6
  ```

## Testing
 **Deploy pod-data-generator for testing**:

  ```bash
  kubectl apply -f test-pod-data-generator.yaml
  ```

## How It Works

### 1. VolumeScaler

Define a VolumeScaler custom resource in the same namespace as the PVC. It should specify:

- `pvcName`: The name of the PVC to monitor
- `threshold`: Utilization threshold in percentage (e.g., 70%)
- `scale`: The percentage increase in PVC size when threshold is exceeded (e.g., 30%)
- `maxSize`: The maximum PVC size (e.g., 100Gi)
it is just all about predictable and unpredictable workload
- `scaleType`: it is just all about predictable and unpredictable workload
  - percentage: for unpredictable workload
  - Fixed: for predictable workload

### 2. Monitoring Utilization

The DaemonSet runs on every node, listing pods running on that node. For each pod volume that references a PVC, it checks the disk usage using. The usage is compared against the threshold from the matching VolumeScaler resource.

### 3. Scaling PVC

If the utilization is above the threshold and cooldown conditions are met (not scaled recently), the controller:

- Calculates the new size based on the scale percentage
- Ensures it does not exceed maxSize
- Patches the PVC to request the new size
- Updates the VolumeScaler status with the time of the last scale

### 4. Reaching Max Size

When the PVC reaches or is near the maxSize, the VolumeScaler status is updated to indicate `reachedMaxSize`. No further scaling is performed once this is true.

## Example VolumeScaler

```yaml
apiVersion: autoscaling.storage.k8s.io/v1alpha1
kind: VolumeScaler
metadata:
  name: example-pvc
  namespace: default
spec:
  pvcName: example-pvc
  threshold: "70%"
  scaleType: "percentage"        # "fixed" or "percentage"
  scale: "30%"
  maxSize: "10Gi"
```



## Contributing

Contributions are welcome! Please open issues or pull requests on the repository for bug fixes, new features, or documentation improvements.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
