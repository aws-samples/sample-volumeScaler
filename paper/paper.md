---
title: 'VolumeScaler: A Kubernetes Controller for Automated PVC Scaling'
tags:
  - Kubernetes
  - Cloud Computing
  - Storage Management
  - Container Orchestration
  - Persistent Volumes
authors:
  - name: Ahmed Ghanem
    orcid: 0009-0005-0012-4470
    affiliation: 1
  - name: Ruairi O'Reilly
    orcid: 0000-0001-7990-3461
    affiliation: 2
affiliations:
 - name: Department of Computer Science, Munster Technological University Cork, Ireland
   index: 1
date: 23 April 2025
bibliography: paper.bib
---

# Summary

The management of storage resources in Kubernetes clusters is a critical aspect of cloud-native application deployment. PersistentVolumeClaims (PVCs) provide a way for applications to request storage resources, but their static nature can lead to inefficient resource utilization and operational challenges. The field of "cloud storage management," which aims to optimize storage resources in containerized environments, is becoming increasingly important as organizations adopt cloud-native architectures. While Kubernetes provides robust mechanisms for scaling compute resources, storage resources have traditionally required manual intervention for scaling operations.

`VolumeScaler` is a Kubernetes controller that extends the native capabilities of PVC management by introducing automated scaling functionality. The implementation leverages the Kubernetes Operator pattern and integrates with the Kubernetes API to provide a seamless experience for cluster administrators and application developers. The API for `VolumeScaler` was designed to provide a declarative and user-friendly interface to fast implementations of common operations such as PVC usage monitoring, storage capacity evaluation, and dynamic resource adjustment. `VolumeScaler` also relies heavily on and interfaces well with the implementations of storage classes and provisioners in the Kubernetes ecosystem [@KubernetesResourceManagement].

`VolumeScaler` was designed to be used by both cloud platform administrators and application developers managing storage resources in Kubernetes environments. It has been implemented using Kubernetes Custom Resource Definitions (CRDs) and integrates with Container Storage Interface (CSI) drivers [@csi_spec_2022]. **By design, VolumeScaler works with any Kubernetes provider that uses CSI drivers, making it universally compatible across different cloud environments and on-premises deployments.** The combination of automation, flexibility, and support for Kubernetes storage functionality in `VolumeScaler` will enable efficient management of storage resources in dynamic cloud environments. The source code for `VolumeScaler` is available on GitHub.

# State of the Field

The challenge of automated storage scaling in Kubernetes represents a significant gap in the current ecosystem. While Kubernetes provides comprehensive solutions for scaling compute resources through features like Horizontal Pod Autoscaling (HPA) [@HPA], **no standardized, production-ready solution exists for automated storage scaling**. This section examines the current state and demonstrates why `VolumeScaler` addresses a critical unmet need.

## Current State: No Automated Storage Scaling Solutions

**Kubernetes Native Capabilities**: Kubernetes supports online volume expansion through the `allowVolumeExpansion` feature in StorageClasses [@KubernetesResourceManagement], but this capability is limited to manual operations and requires explicit administrator intervention. The platform lacks built-in automation for triggering expansions based on usage patterns.

**Existing Tools Focus on Different Problems**: 
- **Velero** [@velero_2022] provides backup and restore functionality but offers no storage scaling capabilities
- **STORK** [@stork_docs] enhances storage orchestration and scheduling but does not address dynamic scaling
- **External CSI drivers** support volume expansion but require manual PVC patching to trigger size increases

## Research Prototypes vs. Production Solutions

**Konev et al.'s Research** [@konev2022] represents the only academic work attempting to address automated PV scaling. While theoretically promising, their approach has fundamental limitations that make it unsuitable for production use:

- **Restricted to StatefulSets only** - cannot work with Deployments, DaemonSets, or standalone Pods
- **Dangerous update approach** - requires deleting and recreating StatefulSets, introducing downtime and risk
- **Proof-of-concept implementation** - lacks production-grade reliability, error handling, and testing
- **No CSI driver integration** - limited to basic Kubernetes APIs without storage backend awareness
- **Single-replica limitation** - avoids the complexities of multi-replica coordination

## Custom Scripts and Ad-Hoc Solutions

Many organizations develop custom, ad-hoc solutions to manage storage scaling [@In-Memory_Storage]. These approaches suffer from critical limitations:

- **No standardization** - each implementation is unique and non-portable
- **Lack of integration** - difficult to integrate with existing Kubernetes workflows
- **Maintenance overhead** - error-prone, difficult to maintain, and challenging to scale
- **No reliability guarantees** - lack comprehensive error handling and recovery mechanisms
- **Limited scope** - typically work only with specific storage backends or cluster configurations

## The Gap: Why VolumeScaler is Needed

The current landscape reveals a critical gap: **there exists no standardized, production-ready solution for automated Kubernetes storage scaling**. This gap results in:

1. **Operational inefficiency** - administrators must manually monitor and scale storage
2. **Resource waste** - over-provisioning to avoid outages or under-provisioning leading to performance issues
3. **Inconsistent practices** - each organization reinvents the wheel with custom solutions
4. **Limited scalability** - manual approaches cannot handle dynamic, large-scale environments

## VolumeScaler's Unique Position

`VolumeScaler` addresses this gap by providing the **first standardized, production-ready solution** for automated Kubernetes storage scaling. Its design specifically addresses the limitations of existing approaches:

**Universal Compatibility**: Unlike research prototypes restricted to specific workload types, `VolumeScaler` works with **any PVC regardless of workload type** (Deployments, DaemonSets, StatefulSets, or standalone Pods).

**CSI Driver Agnostic**: By design, `VolumeScaler` works with **any Kubernetes provider that uses CSI drivers**. This includes:
- AWS EBS CSI Driver
- Azure Disk CSI Driver  
- Google Cloud PD CSI Driver
- VMware vSphere CSI Driver
- Any other CSI-compliant storage driver

**Production-Ready Implementation**: Unlike research prototypes, `VolumeScaler` is built as a production-grade Kubernetes controller with:
- Comprehensive error handling and recovery
- Event recording and status management
- Safe PVC patching operations (no resource deletion/recreation)
- Integration with Kubernetes RBAC and security models

**Standardized Approach**: Unlike custom scripts, `VolumeScaler` provides a consistent, declarative interface through Kubernetes Custom Resource Definitions (CRDs), ensuring:
- Portability across different environments
- Integration with existing Kubernetes tooling
- Consistent behavior and predictable outcomes
- Easy adoption and maintenance

This approach fills the critical gap in the Kubernetes ecosystem by providing the missing link between storage monitoring and automated scaling, establishing a new standard for storage management rather than competing with existing solutions.

# Statement of need

The research in cloud-native storage management has gained significant attention as organizations increasingly adopt containerized architectures. While Kubernetes provides comprehensive solutions for scaling compute resources through features like Horizontal Pod Autoscaling (HPA) [@HPA], the platform lacks native mechanisms for automatically scaling storage resources. This gap in functionality presents an important research challenge in the field of cloud resource management.

Recent studies in cloud storage optimization [@k8s_storage_2023] have highlighted several key challenges in storage resource management:

1. Operational overhead: Administrators must manually monitor storage usage and perform scaling operations
2. Resource inefficiency: Static storage allocation often leads to either over-provisioning (increasing costs) or under-provisioning (risking performance degradation)
3. Response latency: Manual scaling operations cannot respond quickly to sudden changes in storage requirements

`VolumeScaler` addresses these research challenges by providing an automated, policy-driven approach to PVC scaling. The controller implements novel algorithms for:
- Real-time storage usage pattern analysis
- Dynamic threshold-based scaling decisions
- Integration with multiple storage backends through CSI [@csi_spec_2022]

This research contribution advances the field of cloud storage management by providing a practical solution to the storage scaling problem while maintaining compatibility with existing Kubernetes infrastructure. The implementation follows Kubernetes best practices [@k8s_controllers_2021] and serves as a reference architecture for future research in automated storage management.


# Acknowledgements

I would like to acknowledge the support and guidance from my supervisors and colleagues at Munster Technological University Cork. Special thanks to the Kubernetes community for their continuous development of the platform and its ecosystem.

# References
