# StatefulSet Backup Operator

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.20%2B-brightgreen.svg)](https://kubernetes.io)
[![Go Report Card](https://goreportcard.com/badge/github.com/federicolepera/statefulset-backup-operator)](https://goreportcard.com/report/github.com/federicolepera/statefulset-backup-operator)

> ⚠️ **Work in Progress** - Version 0.0.2
> This operator is under active development. APIs may change, and some features are still being implemented.

A Kubernetes operator for automated backup and restore of StatefulSets using native VolumeSnapshot APIs. Features scheduled snapshots, retention policies, pre/post hooks, and point-in-time recovery with a simple declarative interface.

## 🎯 Features

- ✅ **Automated Snapshots** - Schedule backups using cron expressions or trigger them on-demand
- ✅ **Coordinated Backups** - Create consistent snapshots across all replicas of a StatefulSet
- ✅ **Pre/Post Backup Hooks** - Execute commands inside pods before and after snapshots (e.g., database flush operations)
- ✅ **Retention Management** - Automatically clean up old snapshots based on configurable retention policies (per-replica)
- ✅ **Point-in-Time Recovery** - Restore StatefulSets to any previous snapshot with a single command
- ✅ **Native Kubernetes Integration** - Uses standard VolumeSnapshot APIs (CSI) for broad storage provider compatibility
- ✅ **Namespace Isolation** - Proper namespace scoping for multi-tenant environments
- ✅ **Comprehensive Test Suite** - 26 unit tests with 42.5% code coverage, fully CI-compatible without external dependencies

## 🚀 Why Not Velero?

Velero is excellent for full-cluster disaster recovery, but if you just need:

- Fast, automated backups for your StatefulSets
- Point-in-time recovery without external storage
- Minimal operational overhead
- Cost-effective snapshot-based backups

...then this operator is a better fit. Think of it as "the right tool for the right job" - lightweight, focused, and cloud-native.

### Comparison with Velero

| Feature | StatefulSet Backup Operator | Velero |
|---------|----------------------------|---------|
| Setup time | ~2 minutes | 15-30 minutes |
| Dependencies | None (CSI driver only) | Object storage (S3, GCS) + CLI |
| Backup speed | Seconds (snapshots) | Minutes (full copy) |
| Storage cost | Incremental snapshots | Full backups on S3 |
| StatefulSet hooks | ✅ Built-in | ⚠️ Via init containers |
| Cross-cluster DR | ❌ (roadmap) | ✅ |
| Per-replica restore | ✅ | ⚠️ Limited |
| GitOps friendly | ✅ 100% CRD-based | ⚠️ Mix CLI/CRD |

## 📋 Prerequisites

- Kubernetes 1.20+
- CSI driver with snapshot support (CSI Snapshot v1 API)
- VolumeSnapshotClass configured in your cluster
- Kubectl access to the cluster

### Required CRDs

The operator requires the following VolumeSnapshot CRDs to be installed:
- `volumesnapshotclasses.snapshot.storage.k8s.io`
- `volumesnapshots.snapshot.storage.k8s.io`
- `volumesnapshotcontents.snapshot.storage.k8s.io`

**Verify VolumeSnapshot API is available:**
```bash
kubectl api-resources | grep volumesnapshot
```

**Install VolumeSnapshot CRDs (if not present):**
```bash
# Install CSI snapshot controller and CRDs
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml
```

### Tested Environments

- ✅ Minikube (with CSI hostpath driver)
- ✅ Kind (with CSI snapshot support)
- 🔄 GKE, EKS, AKS (testing in progress)

### CSI Driver Compatibility

The operator works with any CSI driver that supports VolumeSnapshot v1 API:
- AWS EBS CSI Driver
- GCE Persistent Disk CSI Driver
- Azure Disk CSI Driver
- Ceph CSI
- Longhorn
- OpenEBS
- Portworx

## 🛠️ Installation

### Option 1: Build from Source

```bash
# Clone the repository
git clone https://github.com/federicolepera/statefulset-backup-operator.git
cd statefulset-backup-operator

# Build the Docker image
make docker-build IMG=<your-registry>/statefulset-backup-operator:v0.0.2

# Push to your registry
make docker-push IMG=<your-registry>/statefulset-backup-operator:v0.0.2

# Install CRDs
make install

# Deploy the operator
make deploy IMG=<your-registry>/statefulset-backup-operator:v0.0.2
```

### Option 2: Install CRDs and Deploy Manually

```bash
# Install CRDs
kubectl apply -f config/crd/bases/

# Deploy operator (update image in config/manager/manager.yaml first)
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

### 🎁 Helm Chart (Coming Soon)

A Helm chart is currently in development and will be available in the next release.

## 📖 Usage

### Basic Backup

Create a simple backup that runs once:

```yaml
apiVersion: backup.sts-backup.io/v1alpha1
kind: StatefulSetBackup
metadata:
  name: my-database-backup
  namespace: default
spec:
  statefulSetRef:
    name: postgresql
    namespace: default
  retentionPolicy:
    keepLast: 3  # Keep last 3 snapshots per PVC
```

> **Note**: VolumeSnapshotClass is currently hardcoded to `csi-hostpath-snapclass` in the operator. Configuration via CRD spec is planned for a future release.

### Scheduled Backup with Hooks

Create automated backups with pre/post hooks:

```yaml
apiVersion: backup.sts-backup.io/v1alpha1
kind: StatefulSetBackup
metadata:
  name: postgres-scheduled-backup
  namespace: production
spec:
  statefulSetRef:
    name: postgresql
    namespace: production
  schedule: "0 2 * * *"  # Every day at 2 AM (standard cron format)
  retentionPolicy:
    keepLast: 7  # Keep last 7 backups per PVC
  preBackupHook:
    containerName: postgres  # Optional: specify container (defaults to first container)
    command:
      - "psql"
      - "-U"
      - "postgres"
      - "-c"
      - "CHECKPOINT"
  postBackupHook:
    command:
      - "echo"
      - "Backup completed"
```

**Hook Behavior:**
- Hooks execute **sequentially** on each pod (pod-0, then pod-1, etc.)
- If a hook fails on any pod, the entire backup fails
- Hooks execute in the first container unless `containerName` is specified
- No timeout is currently configured for hooks

### Restore from Backup

Restore a StatefulSet to a previous snapshot:

```yaml
apiVersion: backup.sts-backup.io/v1alpha1
kind: StatefulSetRestore
metadata:
  name: restore-postgres
  namespace: production
spec:
  statefulSetRef:
    name: postgresql
    namespace: production
  backupName: postgres-scheduled-backup
  scaleDown: true  # Recommended: scales StatefulSet to 0 before restore
```

**Restore Phases:**
The restore process follows a multi-phase workflow:
1. **New**: Initial state, saves original replica count
2. **ScalingDown**: Reduces StatefulSet replicas to 0 (if `scaleDown: true`)
3. **Restoring**: Deletes existing PVCs and recreates them from snapshots
4. **ScalingUp**: Restores StatefulSet to original replica count
5. **Completed/Failed**: Final state

### Selective Snapshot Restore

Restore specific snapshots (useful for partial recovery):

```yaml
apiVersion: backup.sts-backup.io/v1alpha1
kind: StatefulSetRestore
metadata:
  name: restore-selective
  namespace: production
spec:
  statefulSetRef:
    name: postgresql
    namespace: production
  snapshotNames:
    - postgres-backup-data-20260112-120000-0  # Restore only replica-0
    - postgres-backup-data-20260112-120000-1  # Restore only replica-1
  scaleDown: true
```

> **Note**: The `useLatestBackup` field is defined in the CRD but not yet implemented. Use `backupName` or `snapshotNames` for restores.

## 🔍 Monitoring

### Check Backup Status

```bash
# List all backups
kubectl get statefulsetbackup

# Detailed status
kubectl describe statefulsetbackup my-database-backup

# Check created snapshots
kubectl get volumesnapshot
```

**Status Fields:**
- **Phase**: Current state - `Ready`, `InProgress`, or `Failed`
- **LastBackupTime**: Timestamp of the most recent successful backup
- **Conditions**: Standard Kubernetes conditions for state tracking
- **Message**: Human-readable status information

Example output:
```yaml
status:
  phase: Ready
  lastBackupTime: "2026-01-12T14:30:00Z"
  conditions:
    - type: Ready
      status: "True"
      lastTransitionTime: "2026-01-12T14:30:05Z"
```

### Check Restore Status

```bash
# List all restores
kubectl get statefulsetrestore

# Watch restore progress
kubectl get statefulsetrestore restore-postgres -w

# Detailed restore status
kubectl describe statefulsetrestore restore-postgres
```

**Restore Status Fields:**
- **Phase**: Current phase - `New`, `ScalingDown`, `Restoring`, `ScalingUp`, `Completed`, or `Failed`
- **OriginalReplicas**: Original replica count saved before scaling down
- **RestoredSnapshots**: Map of PVC names to restored snapshot names
- **StartTime/CompletionTime**: Timing information for the restore operation

Example output:
```yaml
status:
  phase: Completed
  originalReplicas: 3
  restoredSnapshots:
    data-postgresql-0: postgres-backup-data-20260112-120000-0
    data-postgresql-1: postgres-backup-data-20260112-120000-1
    data-postgresql-2: postgres-backup-data-20260112-120000-2
  startTime: "2026-01-12T15:00:00Z"
  completionTime: "2026-01-12T15:05:30Z"
```

### View Operator Logs

```bash
# Get operator pod
kubectl get pods -n statefulset-backup-operator-system

# View logs
kubectl logs -n statefulset-backup-operator-system <operator-pod-name> -f
```

## 🏗️ Architecture

### Snapshot Naming and Labels

**VolumeSnapshot Naming Format:**
```
{backup-name}-{volume-claim-template-name}-{timestamp}-{replica-index}
```

Example: `postgres-backup-data-20260112-143000-0`

**Labels Applied to Snapshots:**
- `backup.sts-backup.io/statefulset`: Name of the source StatefulSet
- `backup.sts-backup.io/policy`: Name of the backup resource

**PVC Naming for StatefulSets:**
```
{volume-claim-template-name}-{statefulset-name}-{replica-index}
```

Example: `data-postgresql-0`

### Backup Scheduling Behavior

**Manual Backups (no schedule):**
- Executed once when the backup resource is created
- No subsequent backups occur automatically
- Status updates to `Ready` after completion

**Scheduled Backups:**
- Uses standard cron format: `minute hour day-of-month month day-of-week`
- First backup executes immediately (within 10 seconds) if no `LastBackupTime` exists
- Next reconciliation scheduled up to 1 hour in advance
- If schedule is missed (controller downtime), executes immediately upon restart
- Invalid cron expressions cause backup to fail with 1-minute retry interval

### Backup Flow

1. **Reconcile Loop** checks if it's time for a backup (based on schedule or manual trigger)
2. **Pre-Backup Hook** executes in all StatefulSet pods (if configured)
3. **Snapshot Creation** creates VolumeSnapshots for each PVC
4. **Post-Backup Hook** executes in all StatefulSet pods (if configured)
5. **Retention Policy** removes old snapshots (keeping N most recent per PVC)
6. **Status Update** updates the StatefulSetBackup status with results

### Restore Flow

1. **New Restore** validates the restore request and saves original replica count
2. **Scale Down** scales StatefulSet to 0 replicas (if enabled)
3. **Find Snapshots** locates snapshots to restore based on backupName or useLatestBackup
4. **Delete PVCs** removes existing PVCs for each replica
5. **Recreate PVCs** creates new PVCs from VolumeSnapshots
6. **Scale Up** restores StatefulSet to original replica count
7. **Completion** waits for all pods to be ready and marks restore as complete

### Retention Policy

Retention policies are **per-PVC**, meaning:
- With 3 replicas and `keepLast: 2`
- Each PVC maintains its own 2 most recent snapshots
- Total snapshots: 6 (2 per PVC)
- Old snapshots are deleted based on creation timestamp (oldest first)

This ensures you can always restore all replicas to the same point in time.

**Configuration:**
```yaml
retentionPolicy:
  keepLast: 5  # Keep last 5 snapshots per PVC
```

> **Note**: The `keepDays` field exists in the CRD but is not yet implemented. Only `keepLast` is currently functional.

## 🚧 Work in Progress

The following features are currently under development or planned:

### Current Limitations

- ⚠️ **VolumeSnapshotClass Hardcoded** - Currently fixed to `csi-hostpath-snapclass`
  - Cannot be configured via CRD spec
  - Workaround: Modify controller source code
  - Fix planned: Make configurable via CRD field

- ⚠️ **Hook Timeout** - No timeout configuration for pre/post backup hooks
  - Hooks can hang indefinitely if command doesn't complete
  - Workaround: Ensure hook commands have internal timeouts
  - Enhancement planned: Add `timeoutSeconds` field to hook specification

- ⚠️ **Hook Container Selection** - Defaults to first container in pod
  - Use `containerName` field in hook spec to override
  - If container name is invalid, hook execution fails

- ⚠️ **Cross-Namespace Operations** - Backup and target StatefulSet must be in same namespace
  - Cross-namespace snapshots not supported
  - Enhancement planned: Support cross-namespace operations

- ⚠️ **Restore to Different StatefulSet** - Not supported
  - Restore only works with the original source StatefulSet
  - Workaround: Manually copy snapshots and recreate PVCs

- ⚠️ **PVC Deletion Timeout** - Hardcoded to 60 seconds during restore
  - If PVC takes longer to delete, restore fails
  - Polls every 2 seconds for deletion completion
  - Enhancement planned: Make timeout configurable

- ⚠️ **Snapshot Readiness Verification** - Snapshots not verified before applying retention
  - Retention policy is applied immediately after snapshot creation
  - VolumeSnapshot may still be in "Creating" state
  - Enhancement planned: Wait for ReadyToUse=true before cleanup

- ⚠️ **Feature Not Implemented** - `useLatestBackup` field exists but doesn't work
  - Field is defined in StatefulSetRestore CRD
  - Implementation planned for next release
  - Use `backupName` or `snapshotNames` instead

- ⚠️ **Feature Not Implemented** - `keepDays` retention policy not functional
  - Field exists in RetentionPolicy but has no effect
  - Only `keepLast` is currently implemented
  - Time-based retention planned for future release

### Roadmap

- [x] Comprehensive unit test suite (v0.0.2)
- [x] CI/CD integration with GitHub Actions (v0.0.2)
- [ ] Helm chart for easy installation
- [ ] Webhook validation for CRDs
- [ ] Configurable container selection for hooks
- [ ] Hook timeout configuration
- [ ] Backup verification and integrity checks
- [ ] Metrics and Prometheus integration
- [ ] Multi-cluster restore (cross-cluster DR)
- [ ] Support for encryption at rest
- [ ] CLI tool for backup/restore operations
- [ ] Dashboard/UI for visualization

## 🧪 Development

### Prerequisites

- Go 1.21+
- Docker
- Kubernetes cluster (Minikube recommended for development)
- Kubebuilder 3.0+

### Local Development Setup

```bash
# Install dependencies
go mod download

# Generate CRDs and code
make manifests generate

# Install CRDs into cluster
make install

# Run operator locally (outside cluster)
make run

# Or debug with VSCode
# Use the provided .vscode/launch.json configuration
```

### Testing

The operator includes a comprehensive test suite with 26 unit tests covering both backup and restore controllers.

```bash
# Setup test environment (first time only)
make setup-envtest

# Run all unit tests with coverage
make test

# Run specific controller tests
go test ./internal/controller/... -v

# Run a specific test
go test ./internal/controller/... -v -run "TestStatefulSetBackupController"
```

#### Test Coverage

- **26 total tests** implemented
- **24 tests pass** successfully ✅
- **2 tests skipped** (require VolumeSnapshot CRDs)
- **42.5% code coverage** of the codebase
- **GitHub Actions CI** runs all tests automatically

For detailed test documentation, see [TEST_DOCUMENTATION.md](TEST_DOCUMENTATION.md).

#### What's Tested

**StatefulSetBackup Controller (15 tests):**
- Manual and scheduled backup workflows
- Cron schedule validation and requeue logic
- Backup status management
- Resource lifecycle (creation, deletion)
- Error handling for missing StatefulSets

**StatefulSetRestore Controller (11 tests):**
- Restore phase workflow (ScalingDown → Restoring → ScalingUp)
- Scale down/up operations
- Snapshot search and restoration
- Parameter validation
- Completed/failed state handling

All tests are CI-compatible and run without requiring VolumeSnapshot CRDs to be installed.

### Integration Testing

```bash
# Run with a test StatefulSet
kubectl apply -f config/samples/apps_v1_statefulset.yaml
kubectl apply -f config/samples/backup_v1alpha1_statefulsetbackup.yaml

# Watch operator logs
# (if running locally, check terminal output)
```

### Building

```bash
# Build binary
make build

# Build and push Docker image
make docker-build docker-push IMG=<your-registry>/statefulset-backup-operator:tag
```

## 📝 Examples

### Example 1: PostgreSQL Backup

```yaml
apiVersion: backup.sts-backup.io/v1alpha1
kind: StatefulSetBackup
metadata:
  name: postgres-backup
spec:
  statefulSetRef:
    name: postgres
    namespace: databases
  schedule: "0 */6 * * *"  # Every 6 hours
  retentionPolicy:
    keepLast: 8  # Keep 48 hours of backups (8 snapshots per PVC)
  preBackupHook:
    containerName: postgres
    command: ["psql", "-U", "postgres", "-c", "CHECKPOINT"]
```

### Example 2: MongoDB Backup with Replica Sync

```yaml
apiVersion: backup.sts-backup.io/v1alpha1
kind: StatefulSetBackup
metadata:
  name: mongodb-backup
spec:
  statefulSetRef:
    name: mongodb
    namespace: databases
  schedule: "0 3 * * *"  # Daily at 3 AM
  retentionPolicy:
    keepLast: 7  # Keep last 7 snapshots per PVC
  preBackupHook:
    containerName: mongodb
    command:
      - "mongosh"
      - "--eval"
      - "db.fsyncLock()"
  postBackupHook:
    command:
      - "mongosh"
      - "--eval"
      - "db.fsyncUnlock()"
```

### Example 3: Redis Cluster Backup

```yaml
apiVersion: backup.sts-backup.io/v1alpha1
kind: StatefulSetBackup
metadata:
  name: redis-backup
spec:
  statefulSetRef:
    name: redis
    namespace: cache
  schedule: "*/30 * * * *"  # Every 30 minutes
  retentionPolicy:
    keepLast: 12  # Keep 6 hours of backups (12 snapshots per PVC)
  preBackupHook:
    containerName: redis
    command: ["redis-cli", "BGSAVE"]
```

## 🤝 Contributing

Contributions are welcome! This is an early-stage project, and we'd love your help.

### How to Contribute

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Areas Where We Need Help

- Testing on various Kubernetes distributions (GKE, EKS, AKS)
- Documentation improvements
- Additional storage provider testing
- Performance optimization
- Feature implementations from roadmap

## 📄 License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- Built with [Kubebuilder](https://github.com/kubernetes-sigs/kubebuilder)
- Uses [external-snapshotter](https://github.com/kubernetes-csi/external-snapshotter) for VolumeSnapshot support
- Inspired by the Kubernetes community's need for lightweight backup solutions

## 📞 Support

- 🐛 **Bug Reports**: [GitHub Issues](https://github.com/federicolepera/statefulset-backup-operator/issues)
- 💡 **Feature Requests**: [GitHub Discussions](https://github.com/federicolepera/statefulset-backup-operator/discussions)
- 📧 **Contact**: Federico Lepera

## ⭐ Star History

If you find this project useful, please consider giving it a star! It helps the project gain visibility and encourages continued development.

---

**Note**: This operator is in active development (v0.0.2). APIs and features may change. Not recommended for production use until v1.0.0 release.

## 📊 Changelog

### Version 0.0.2 (2026-01-12)

**New Features:**
- ✅ Comprehensive unit test suite with 26 tests covering both controllers
- ✅ GitHub Actions CI integration for automated testing
- ✅ Test documentation with detailed coverage information
- ✅ CI-compatible tests that run without VolumeSnapshot CRDs
- ✅ Automatic default VolumeSnapshotClass detection - operator now automatically uses the cluster's default VolumeSnapshotClass if not explicitly specified

**Test Coverage:**
- StatefulSetBackup Controller: 15 tests covering manual/scheduled backups, cron validation, status management, and resource lifecycle
- StatefulSetRestore Controller: 11 tests covering restore workflow phases, scale operations, snapshot search, and error handling
- Overall code coverage: 42.5%
- All tests pass in CI without external dependencies

**Documentation:**
- Added [TEST_DOCUMENTATION.md](TEST_DOCUMENTATION.md) with comprehensive test guide
- Updated README with testing instructions and coverage details

### Version 0.0.1 (2026-01-01)

**Initial Release:**
- Basic backup and restore functionality
- Cron-based scheduling
- Pre/post backup hooks
- Per-replica retention policies
- StatefulSet integration