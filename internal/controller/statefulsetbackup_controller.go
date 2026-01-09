/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/federicolepera/statefulset-backup-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
)

// StatefulSetBackupReconciler reconciles a StatefulSetBackup object
type StatefulSetBackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config *rest.Config
	ClientSet *kubernetes.Clientset
}

// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetbackups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// It fetches the StatefulSetBackup resource, evaluates the schedule, executes pre-backup hooks,
// creates volume snapshots, applies retention policy, and executes post-backup hooks.
func (r *StatefulSetBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	backup := &backupv1alpha1.StatefulSetBackup{}
	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to fetch StatefulSetBackup")
		return ctrl.Result{}, err
	}

	sts := &appsv1.StatefulSet{}
	stsKey := types.NamespacedName {
		Name: backup.Spec.StatefulSetRef.Name,
		Namespace: backup.Spec.StatefulSetRef.Namespace,
	}

	if err := r.Get(ctx, stsKey, sts); err != nil {
		logger.Error(err, "Unable to get StatefulSet")
		r.updateRestoreStatus(ctx,backup, backupv1alpha1.BackupPhaseFailed, "StatefulSetNotFound", err.Error())
		return ctrl.Result{}, err
	}

	shouldBackup, err := r.shouldCreateBackup(backup)
	if err != nil {
		logger.Error(err, "Error evaluating schedule")
		r.updateRestoreStatus(ctx,backup, backupv1alpha1.BackupPhaseFailed, "ScheduleError", err.Error())
		return ctrl.Result{}, err
	}

	if shouldBackup {
		logger.Info("Creating new backup")

		if err := r.executeBackupHook(ctx, sts, backup, backup.Spec.PreBackupHook.Command); err != nil {
			logger.Error(err, "Failed to execute pre-backup hook")
			r.updateRestoreStatus(ctx,backup, backupv1alpha1.BackupPhaseFailed, "PreBackupHookError", err.Error())
			return ctrl.Result{}, err
		}

		r.createSnapshots(ctx, sts, backup)

		now := metav1.Now()
		backup.Status.LastBackupTime = now
		backup.Status.Phase = backupv1alpha1.BackupPhaseReady

		if updateErr := r.Status().Update(ctx, backup); updateErr != nil {
			logger.Error(updateErr, "Failed to update status")
		}

		if err := r.executeBackupHook(ctx, sts, backup, backup.Spec.PostBackupHook.Command); err != nil {
			logger.Error(err, "Failed to execute post-backup hook")
			r.updateRestoreStatus(ctx,backup, backupv1alpha1.BackupPhaseFailed, "PostBackupHookError", err.Error())
			return ctrl.Result{}, err
		}

		if err := r.applyRetentionPolicy(ctx, backup); err != nil {
			logger.Error(err, "Failed to apply retention policy")
			r.updateRestoreStatus(ctx,backup, backupv1alpha1.BackupPhaseFailed, "RetentionPolicyError", err.Error())
			return ctrl.Result{}, err
		}
	}

	requeueAfter := r.calculateRequeueAfter(backup)
	if requeueAfter > 0 {
		logger.Info("Scheduling next reconcile", "after", requeueAfter.String())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	return ctrl.Result{}, nil
}

// executeBackupHook executes a command on all pods of a StatefulSet.
// It is used for both pre-backup and post-backup hooks.
// The function iterates through all replicas of the StatefulSet and executes
// the provided command in the first container of each pod using the Kubernetes exec API.
// Returns an error if the command execution fails on any pod.
func (r *StatefulSetBackupReconciler) executeBackupHook(ctx context.Context,sts *appsv1.StatefulSet,backup *backupv1alpha1.StatefulSetBackup, command []string, ) (error) {
	logger := logf.FromContext(ctx)
	if len(command) == 0 {
		logger.Info("No Pre Backup Hook command detected")
		return nil
	}

	stsKey := types.NamespacedName {
		Name: backup.Spec.StatefulSetRef.Name,
		Namespace: backup.Spec.StatefulSetRef.Namespace,
	}

	for i := 0; i < int(*sts.Spec.Replicas); i++ {
		podName := fmt.Sprintf("%s-%d",stsKey.Name,i)
		podKey := types.NamespacedName{
			Name: podName,
			Namespace: backup.Spec.StatefulSetRef.Namespace,
		}
		pod := &corev1.Pod{}
		if err := r.Client.Get(ctx, podKey, pod); err != nil {
			return fmt.Errorf("Unable to the pod %s. Error: %w", podName, err)
		}

		// FIX-ME: Add in the CRD the containerName to use for pre-hook-execution
		containerName := pod.Spec.Containers[0].Name

		// Execute the hook command on the first container
		req := r.ClientSet.CoreV1().RESTClient().Post().Resource("pods").Name(podName).Namespace(podKey.Namespace).SubResource("exec")
		req.VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command: backup.Spec.PreBackupHook.Command,
			Stdout: true,
			Stdin: true,
		}, scheme.ParameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(
			r.Config,
			"POST",
			req.URL(),
		)

		if err != nil {
			logger.Error(err, "Error in execution to pod")
			return fmt.Errorf("Error in execution to pod. Err: %w",err)
		}

		var stdout, stderr bytes.Buffer

		err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: &stdout,
			Stderr: &stderr,
		})
		if err != nil {
			logger.Error(err, "Error in execution to pod")
			return fmt.Errorf("Error in execution to pod. Err: %w",err)
		}
		
		logger.Info("hook executed",
			"stdout", stdout.String(),
			"stderr", stderr.String(),
		)
	}
	
	return nil
}

// applyRetentionPolicy enforces the retention policy for volume snapshots.
// It lists all snapshots associated with the backup policy, groups them by PVC,
// and deletes the oldest snapshots to keep only the specified number (KeepLast)
// for each PVC. This ensures that old backups are automatically cleaned up
// according to the configured retention policy.
func (r *StatefulSetBackupReconciler) applyRetentionPolicy(ctx context.Context,backup *backupv1alpha1.StatefulSetBackup) error {
	logger := log.FromContext(ctx)

	// List all snapshots for this policy in the correct namespace
	snapshotList := &snapshotv1.VolumeSnapshotList{}
	if err := r.List(ctx, snapshotList, 
		client.MatchingLabels{
			"backup.sts-backup.io/policy": backup.Name,
		},
		client.InNamespace(backup.Spec.StatefulSetRef.Namespace)); err != nil {
		return err
	}

	if len(snapshotList.Items) == 0 {
		return nil
	}

	// Group snapshots by PVC (each replica has its own PVC)
	snapshotsByPVC := make(map[string][]snapshotv1.VolumeSnapshot)
	for _, snapshot := range snapshotList.Items {
		if snapshot.Spec.Source.PersistentVolumeClaimName != nil {
			pvcName := *snapshot.Spec.Source.PersistentVolumeClaimName
			snapshotsByPVC[pvcName] = append(snapshotsByPVC[pvcName], snapshot)
		}
	}

	// For each PVC, keep only the last N snapshots
	for pvcName, pvcSnapshots := range snapshotsByPVC {
		if len(pvcSnapshots) <= backup.Spec.RetentionPolicy.KeepLast {
			continue
		}

		logger.Info("Applying retention policy", 
			"pvc", pvcName, 
			"total", len(pvcSnapshots), 
			"keepLast", backup.Spec.RetentionPolicy.KeepLast)

		// Sort by creation date (oldest to newest)
		sort.Slice(pvcSnapshots, func(i, j int) bool {
			return pvcSnapshots[i].CreationTimestamp.Before(&pvcSnapshots[j].CreationTimestamp)
		})

		// Calculate how many snapshots to delete
		toDelete := len(pvcSnapshots) - backup.Spec.RetentionPolicy.KeepLast

		// Delete the oldest snapshots
		for i := 0; i < toDelete; i++ {
			snapshot := &pvcSnapshots[i]
			logger.Info("Deleting old snapshot", 
				"snapshot", snapshot.Name, 
				"pvc", pvcName,
				"age", time.Since(snapshot.CreationTimestamp.Time))
			
			if err := r.Delete(ctx, snapshot); err != nil {
				logger.Error(err, "Failed to delete old snapshot", "snapshot", snapshot.Name)
			} else {
				logger.Info("Deleted old snapshot", "snapshot", snapshot.Name)
			}
		}
	}

	return nil
}

// calculateRequeueAfter determines when the next reconciliation should occur.
// It parses the cron schedule from the backup spec and calculates the duration
// until the next scheduled backup. If no schedule is defined, it returns 0 (no requeue).
// If the next scheduled time is in the past, it returns 10 seconds to trigger immediately.
// The maximum requeue duration is capped at 1 hour for safety.
func (r *StatefulSetBackupReconciler) calculateRequeueAfter(backup *backupv1alpha1.StatefulSetBackup) time.Duration {
	if backup.Spec.Schedule == "" {
		return 0
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(backup.Spec.Schedule)
	if err != nil {
		// If schedule is invalid, retry in 1 minute
		return 1 * time.Minute
	}

	now := time.Now()
	var nextScheduled time.Time

	if backup.Status.LastBackupTime.IsZero() {
		return 10 * time.Second
	}

	lastBackup := backup.Status.LastBackupTime.Time
	nextScheduled = schedule.Next(lastBackup)

	// If next backup is in the past (controller was down), execute immediately
	if nextScheduled.Before(now) || nextScheduled.Equal(now) {
		return 10 * time.Second
	}

	duration := time.Until(nextScheduled)

	// Add a small buffer to avoid arriving too early
	duration += 30 * time.Second

	// Limit maximum requeue to 1 hour for safety
	if duration > 1*time.Hour {
		return 1 * time.Hour
	}

	return duration
}

// createSnapshots creates volume snapshots for all PVCs in the StatefulSet.
// It iterates through all volume claim templates and replicas, creates a VolumeSnapshot
// resource for each PVC, and returns a list of snapshot information.
// Each snapshot is labeled with the StatefulSet name and backup policy name
// for easy identification and management.
func (r *StatefulSetBackupReconciler) createSnapshots(ctx context.Context,sts *appsv1.StatefulSet,backup *backupv1alpha1.StatefulSetBackup) ([]backupv1alpha1.SnapshotInfo, error) {
	var snapshots []backupv1alpha1.SnapshotInfo

	logger := log.FromContext(ctx)

	for _, vct := range sts.Spec.VolumeClaimTemplates {
		for i := int32(0); i < *sts.Spec.Replicas; i++ {
			pvcName := fmt.Sprintf("%s-%s-%d", vct.Name, sts.Name, i)
			pvc := &corev1.PersistentVolumeClaim{}

			pvcKey := types.NamespacedName{
				Name: pvcName,
				Namespace: sts.Namespace,
			}

			if err := r.Get(ctx, pvcKey, pvc); err != nil {
				logger.Error(err, "PVC not found", "pvc", pvcName)
				continue
			}

			snapshotName := fmt.Sprintf("%s-%s-%d-%d", backup.Name, vct.Name, time.Now().Unix(), i)
			snapshotClassName := "csi-hostpath-snapclass"
			snapshot := &snapshotv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name: snapshotName,
					Namespace: sts.Namespace,
					Labels: map[string]string{
						"backup.sts-backup.io/statefulset": sts.Name,
						"backup.sts-backup.io/policy":      backup.Name,
					},
				},
				Spec: snapshotv1.VolumeSnapshotSpec{
					Source: snapshotv1.VolumeSnapshotSource{
						PersistentVolumeClaimName: &pvcName,
					},
				VolumeSnapshotClassName: &snapshotClassName,
				},
			}

			// FIX-ME: Currently not possible to specify snapshot class, using default
			if err := r.Create(ctx, snapshot); err != nil {
				logger.Error(err, "Failed to create snapshot", "snapshot", snapshotName)
				return nil, err
			}

			snapshots = append(snapshots, backupv1alpha1.SnapshotInfo{
				Name:         snapshotName,
				CreationTime: metav1.Now(),
				PVCName:      pvcName,
				ReadyToUse:   false,
			})

			logger.Info("Created snapshot", "snapshot", snapshotName, "pvc", pvcName)
		}
	}

	return snapshots, nil
}
// shouldCreateBackup determines whether a backup should be created at this time.
// For manual backups (no schedule), it returns true only if no backup has been taken yet.
// For scheduled backups, it parses the cron expression and checks if the current time
// is past the next scheduled backup time based on the last backup timestamp.
// Returns true if a backup should be created, false otherwise.
func (r *StatefulSetBackupReconciler) shouldCreateBackup(backup *backupv1alpha1.StatefulSetBackup) (bool, error) {
	// If no schedule, manual backup (created only once at resource creation)
	if backup.Spec.Schedule == "" {
		if backup.Status.LastBackupTime.IsZero() {
			return true, nil
		} else {
			return false, nil
		}
	} else {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(backup.Spec.Schedule)
		if err != nil {
			return false, fmt.Errorf("invalid cron schedule %q: %w", backup.Spec.Schedule, err)
		}
		now := time.Now()

		if backup.Status.LastBackupTime.IsZero() {
			return true, nil
		}

		lastBackup := backup.Status.LastBackupTime.Time
		nextScheduled := schedule.Next(lastBackup)

		if now.After(nextScheduled) || now.Equal(nextScheduled){
			return true, nil
		}

		return false, nil
	}
}

// updateRestoreStatus updates the status phase and conditions of a backup resource.
// It sets the backup phase, adds or updates a status condition with the provided
// reason and message, and persists the changes to the Kubernetes API server.
// This function is typically called when an error occurs or when the backup state changes.
func (r *StatefulSetBackupReconciler) updateRestoreStatus(ctx context.Context,backup *backupv1alpha1.StatefulSetBackup,phase backupv1alpha1.BackupPhase,reason, message string,) {
	backup.Status.Phase = phase
	meta.SetStatusCondition(&backup.Status.Conditions, metav1.Condition{
		Type:               string(phase),
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: backup.Generation,
	})
	// FIX ME: CHECK UPDATE ERROR
	r.Status().Update(ctx, backup)
}

// SetupWithManager sets up the controller with the Manager.
func (r *StatefulSetBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {

	r.Config = mgr.GetConfig()

	cs, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		return err
	}
	r.ClientSet = cs

	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.StatefulSetBackup{}).
		Named("statefulsetbackup").
		Complete(r)
}