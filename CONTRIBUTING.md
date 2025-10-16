# Contributing to VolumeScaler

Thank you for your interest in contributing to VolumeScaler! This document provides guidelines and instructions for contributing to the project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Testing](#testing)
- [Submitting Changes](#submitting-changes)
- [Release Process](#release-process)

## Code of Conduct

This project adheres to a code of conduct that we expect all contributors to follow. Please be respectful and professional in all interactions.

## Getting Started

### Prerequisites

- Go 1.23 or later
- Docker and Docker Buildx
- kubectl configured with access to a Kubernetes cluster
- A Kubernetes cluster with CSI drivers that support volume expansion (see [CSI Drivers](https://kubernetes-csi.github.io/docs/drivers.html))

### Fork and Clone

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/sample-volumeScaler.git
   cd sample-volumeScaler
   git remote add upstream https://github.com/aws-samples/sample-volumeScaler.git
   ```

## Development Setup

### Local Development

1. **Install dependencies:**
   ```bash
   go mod tidy
   ```

2. **Build the project:**
   ```bash
   make build
   ```

3. **Run tests:**
   ```bash
   make test
   ```

4. **Generate coverage report:**
   ```bash
   make coverage
   ```

### Testing Environment

For comprehensive testing, we recommend using a production-grade Kubernetes cluster with proper CSI drivers:

- **AWS EKS** with EBS CSI driver
- **Azure AKS** with Azure Disk CSI driver  
- **Google GKE** with Persistent Disk CSI driver
- **Production clusters** with any [supported CSI drivers](https://kubernetes-csi.github.io/docs/drivers.html)

**Note:** VolumeScaler is designed for production environments and requires CSI drivers that support online volume expansion. Lightweight distributions like k3s may not provide the full CSI functionality needed for proper testing.

## Making Changes

### Branch Strategy

1. Create a feature branch from `main`:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. Make your changes following the coding standards below

3. Commit your changes with clear, descriptive messages:
   ```bash
   git commit -m "feat: add new scaling algorithm for predictable workloads"
   ```

### Coding Standards

- Follow Go best practices and conventions
- Write clear, self-documenting code
- Add unit tests for new functionality
- Update documentation as needed
- Use meaningful variable and function names

### Commit Message Format

We follow conventional commit format:

```
type(scope): description

[optional body]

[optional footer]
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

## Testing

### Unit Tests

Run the full test suite:
```bash
make test
```

### Integration Testing

For integration testing, deploy VolumeScaler to a cluster with proper CSI drivers:

1. **Deploy VolumeScaler:**
   ```bash
   kubectl apply -f volumescaler.yaml
   ```

2. **Deploy test workload:**
   ```bash
   kubectl apply -f test-pod-data-generator.yaml
   ```

3. **Monitor scaling behavior:**
   ```bash
   kubectl get volumescalers -w
   kubectl get pvc -w
   ```

### Manual Testing

1. Create a StorageClass with volume expansion enabled:
   ```yaml
   apiVersion: storage.k8s.io/v1
   kind: StorageClass
   metadata:
     name: ebs-sc
   provisioner: ebs.csi.aws.com
   allowVolumeExpansion: true
   ```

2. Create a PVC using the StorageClass
3. Deploy a VolumeScaler resource to monitor the PVC
4. Generate data in the volume to trigger scaling

## Submitting Changes

### Pull Request Process

1. **Update your branch:**
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. **Push your changes:**
   ```bash
   git push origin feature/your-feature-name
   ```

3. **Create a Pull Request** with:
   - Clear description of changes
   - Reference to related issues
   - Testing instructions
   - Screenshots if applicable

### Pull Request Requirements

- All tests must pass
- Code coverage should not decrease
- Documentation must be updated
- Changes must be reviewed and approved
- CI/CD pipeline must pass

## Release Process

### Version Management

We use semantic versioning (semver):
- `MAJOR.MINOR.PATCH` (e.g., `1.2.3`)
- MAJOR: Breaking changes
- MINOR: New features (backward compatible)
- PATCH: Bug fixes (backward compatible)

### Release Steps

1. Update version in `makefile`
2. Update CHANGELOG.md
3. Create release tag
4. Build and push multi-arch Docker images
5. Update Helm charts
6. Publish release notes

## CI/CD Pipeline

Our GitHub Actions workflow automatically:
- Runs linting and formatting checks
- Executes unit tests
- Builds multi-architecture Docker images
- Runs security scans
- Validates Kubernetes manifests

## Getting Help

- **Issues:** Use GitHub Issues for bug reports and feature requests
- **Discussions:** Use GitHub Discussions for questions and community chat
- **Documentation:** Check the README.md and inline code comments

## License

By contributing to VolumeScaler, you agree that your contributions will be licensed under the MIT License.

---

Thank you for contributing to VolumeScaler! Your efforts help make Kubernetes storage management more efficient and cost-effective.
