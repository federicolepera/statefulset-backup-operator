# Test Documentation

## Overview

This document describes the tests implemented for the StatefulSet Backup Operator.

## Implemented Tests

Complete tests have been implemented for both main controllers: **StatefulSetBackup** and **StatefulSetRestore**.

### StatefulSetBackup Controller Tests

The tests cover the following controller functionalities:

#### 1. **Manual Backup Tests**
- `should fail when StatefulSet does not exist`: Verifies that backup fails correctly when the referenced StatefulSet doesn't exist
- `should process backup when StatefulSet exists`: Verifies that backup can be processed when StatefulSet exists
- `should handle backup status correctly`: Verifies that backup status is handled correctly

#### 2. **Scheduled Backup Tests**
- `should respect cron schedule and return requeue duration`: Verifies that scheduled backups respect the cron expression and return correct RequeueAfter
- `should handle invalid cron schedule`: Verifies that invalid cron schedules are handled correctly with Failed status

#### 3. **shouldCreateBackup Function Tests**
- `should return true for manual backup without LastBackupTime`: Verifies manual backup creation logic
- `should return false for manual backup with LastBackupTime set`: Verifies that manual backups are not duplicated
- `should return true for scheduled backup when time has passed`: Verifies temporal scheduling logic
- `should return error for invalid cron expression`: Verifies error handling for invalid cron expressions

#### 4. **calculateRequeueAfter Function Tests**
- `should return 0 for manual backups`: Verifies that manual backups don't require requeue
- `should return immediate requeue when LastBackupTime is zero`: Verifies that first backup is executed immediately
- `should return positive duration for valid schedule`: Verifies correct calculation of next execution
- `should return 1 minute for invalid schedule`: Verifies behavior with invalid schedules

#### 5. **Resource Management Tests**
- `should handle gracefully when resource is deleted`: Verifies that resource deletion is handled correctly
- `should create and retrieve backup resource`: Verifies creation and reading of StatefulSetBackup resources

### StatefulSetRestore Controller Tests

The tests cover the following restore controller functionalities:

#### 1. **Restore without StatefulSet**
- `should fail when StatefulSet does not exist`: Verifies that restore fails correctly when StatefulSet doesn't exist

#### 2. **Restore with StatefulSet**
- `should initialize new restore correctly`: Verifies correct initialization with ScalingDown phase and saving of original replicas
- `should handle restore without scale down`: Verifies that restore can proceed without scale down when ScaleDown=false
- `should not reconcile already completed restore`: Verifies that completed restores are not reconciled again
- `should not reconcile already failed restore`: Verifies that failed restores are not reconciled again

#### 3. **findSnapshotToRestore Function Tests**
- `should return error when neither backupName nor useLatestBackup is specified`: Verifies parameter validation
- `should handle backup name specification`: Verifies snapshot search by backup name

#### 4. **handleScalingDown Function Tests**
- `should scale down StatefulSet to zero`: Verifies that StatefulSet is correctly scaled to 0 replicas

#### 5. **Resource Management Tests**
- `should handle gracefully when resource is deleted`: Verifies correct handling of resource deletion
- `should create and retrieve restore resource`: Verifies creation and reading of StatefulSetRestore resources

#### 6. **Restore Phases Workflow Tests**
- `should validate phase transitions`: Verifies that all restore phases are correctly defined

## Running Tests

### Prerequisites

Before running tests, you need to configure the test environment:

```bash
make setup-envtest
```

This command downloads the necessary binaries (etcd, kube-apiserver, kubectl) for running tests.

### Execute Tests

To run all unit tests:

```bash
make test
```

The command executes:
1. CRD manifest generation
2. DeepCopy code generation
3. Code formatting (`go fmt`)
4. Code verification (`go vet`)
5. Test execution with coverage

### Individual Tests

To run only controller tests:

```bash
go test ./internal/controller/... -v
```

To run a specific test:

```bash
go test ./internal/controller/... -v -run "TestName"
```

## Technical Details

### Test Environment

Tests use controller-runtime's `envtest` which provides:
- A local etcd instance
- A local kube-apiserver
- Necessary CRDs for testing

### Important Notes

1. **VolumeSnapshot CRDs**: Tests that require VolumeSnapshot creation are automatically skipped if CRDs are not available in the test environment. This allows tests to run in CI without external dependencies.

2. **Test Isolation**: Each test creates resources with unique timestamp-based names to avoid conflicts between parallel tests.

3. **Cleanup**: Each test includes cleanup logic to remove created resources, ensuring a clean environment for subsequent tests.

## Coverage

Current tests cover approximately **42.5%** of the code, focusing on critical functionalities of both controllers:

**StatefulSetBackup Controller:**
- Scheduling logic
- Backup state management
- Resource validation
- Requeue calculation

**StatefulSetRestore Controller:**
- Restore phase workflow (ScalingDown → Restoring → ScalingUp)
- Scale down/up management
- Snapshot search
- Parameter validation

## Test Statistics

After complete implementation:
- **26 total tests** implemented
- **24 tests pass** successfully ✅
- **2 tests skipped** (require VolumeSnapshot CRDs)
- **0 tests failed** ❌
- **Coverage: 42.5%** of code

## GitHub Actions CI

Tests are configured to run automatically in GitHub Actions via the `.github/workflows/test.yml` workflow:

```yaml
- name: Running Tests
  run: |
    go mod tidy
    make test
```

All tests pass successfully in CI without requiring external dependencies like VolumeSnapshot CRDs.

## Future Extensions

To further improve test coverage, consider:

1. **Integration Tests with VolumeSnapshot**: Add e2e tests using a real cluster with VolumeSnapshot CRDs installed
2. **Hook Testing**: Tests to verify execution of pre/post backup hooks
3. **Retention Policy Tests**: Tests to verify automatic deletion of old snapshots
4. **Error Handling**: More tests for edge cases and error scenarios

## Troubleshooting

### Error: "no such file or directory: bin/k8s"

Solution: Run `make setup-envtest` to download necessary binaries.

### Tests Failing for VolumeSnapshot

If tests fail because VolumeSnapshot CRDs are not found, this is normal. Tests are designed to skip them automatically.

### Slow Tests

Tests use `Eventually` with 10-second timeout. If tests are slow, verify:
- Available system resources
- No zombie test processes running

## Contributing

When adding new features to the controller, make sure to:
1. Add corresponding tests
2. Run `make test` before committing
3. Verify that coverage doesn't decrease significantly
