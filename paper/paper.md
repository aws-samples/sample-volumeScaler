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
affiliations:
 - name: Department of Computer Science, Munster Technological University Cork
   index: 1
date: 23 April 2024
bibliography: paper.bib
---

# Summary

The management of storage resources in Kubernetes clusters is a critical aspect of cloud-native application deployment. PersistentVolumeClaims (PVCs) provide a way for applications to request storage resources, but their static nature can lead to inefficient resource utilization and operational challenges. The field of "cloud storage management," which aims to optimize storage resources in containerized environments, is becoming increasingly important as organizations adopt cloud-native architectures. While Kubernetes provides robust mechanisms for scaling compute resources, storage resources have traditionally required manual intervention for scaling operations.

`VolumeScaler` is a Kubernetes controller that extends the native capabilities of PVC management by introducing automated scaling functionality. The implementation leverages the Kubernetes Operator pattern and integrates with the Kubernetes API to provide a seamless experience for cluster administrators and application developers. The API for `VolumeScaler` was designed to provide a declarative and user-friendly interface to fast implementations of common operations such as PVC usage monitoring, storage capacity evaluation, and dynamic resource adjustment. `VolumeScaler` also relies heavily on and interfaces well with the implementations of storage classes and provisioners in the Kubernetes ecosystem [@KubernetesResourceManagement] (`storage.k8s.io` and `csi.storage.k8s.io`).

`VolumeScaler` was designed to be used by both cloud platform administrators and application developers managing storage resources in Kubernetes environments. It has been implemented using Kubernetes Custom Resource Definitions (CRDs) and integrates with Container Storage Interface (CSI) drivers [@csi_spec_2022] (`storage.k8s.io` and `csi.storage.k8s.io`). The combination of automation, flexibility, and support for Kubernetes storage functionality in `VolumeScaler` will enable efficient management of storage resources in dynamic cloud environments. The source code for `VolumeScaler` is available on GitHub.

# Acknowledgements

I would like to acknowledge the support and guidance from my supervisors and colleagues at Munster Technological University Cork. Special thanks to the Kubernetes community for their continuous development of the platform and its ecosystem.

