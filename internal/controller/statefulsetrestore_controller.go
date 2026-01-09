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
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/federicolepera/statefulset-backup-operator/api/v1alpha1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
)

// StatefulSetRestoreReconciler reconciles a StatefulSetRestore object
type StatefulSetRestoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetrestores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetrestores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetrestores/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// It manages the restore workflow through different phases: ScalingDown, Restoring, and ScalingUp.
// The function orchestrates the restoration of StatefulSet volumes from snapshots.
func (r *StatefulSetRestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	logger.Info("=== Restore Reconcile triggered ===")

	restore := &backupv1alpha1.StatefulSetRestore{}
	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}


	// If restore is already completed or failed, do nothing
	if restore.Status.Phase == backupv1alpha1.RestorePhaseCompleted || restore.Status.Phase == backupv1alpha1.RestorePhaseFailed {
		return ctrl.Result{}, nil
	}

	// Get the target StatefulSet
	sts := &appsv1.StatefulSet{}
	stsKey := types.NamespacedName{
		Name:      restore.Spec.StatefulSetRef.Name,
		Namespace: restore.Spec.StatefulSetRef.Namespace,
	}

	if err := r.Get(ctx, stsKey, sts); err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseFailed, "StatefulSetNotFound", fmt.Sprintf("StatefulSet not found %v", err))
		return ctrl.Result{}, err
	}

	switch restore.Status.Phase {
	case "":
		return r.handleNewRestore(ctx, restore, sts)
	case backupv1alpha1.RestorePhaseScalingDown:
		return r.handleScalingDown(ctx, restore, sts)
	case backupv1alpha1.RestorePhaseRestoring:
		return r.handleRestoring(ctx, restore, sts)
	case backupv1alpha1.RestorePhaseScalingUp:
		return r.handleScalingUp(ctx, restore, sts)
	}

	return ctrl.Result{}, nil
}

// findSnapshotToRestore locates the volume snapshots to use for restoration.
// It searches for snapshots based on either the specified backup name or the latest backup
// for the StatefulSet. Returns a list of snapshots matching the restore criteria.
func (r *StatefulSetRestoreReconciler) findSnapshotToRestore(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore) ([]snapshotv1.VolumeSnapshot, error){
	logger := logf.FromContext(ctx)
	
	if restore.Spec.BackupName == "" && !restore.Spec.UseLatestBackup {
		return nil, fmt.Errorf("either backupName or useLatestBackup must be specified")
	}
	// Case 1: restore spec contains a specific backup name
	if restore.Spec.BackupName != "" {
		logger.Info("Search snapshot for StatefulSet " + restore.Spec.StatefulSetRef.Name + "backup with " + restore.Spec.BackupName)
		snapshotList := &snapshotv1.VolumeSnapshotList{}
		labels := client.MatchingLabels{
			"backup.sts-backup.io/policy": restore.Spec.BackupName,
			"backup.sts-backup.io/statefulset": restore.Spec.StatefulSetRef.Name,
		}
		if err := r.List(ctx, snapshotList, labels, client.InNamespace(restore.Spec.StatefulSetRef.Namespace)); err != nil {
			return nil, err
		}

		if len(snapshotList.Items) == 0 {
			return nil, fmt.Errorf("no snapshots found for StatefulSet %s", restore.Spec.StatefulSetRef.Name)
		}

		return snapshotList.Items, nil
	}

	// FIX-ME: Implement useLatestBackup logic
	return nil,nil
}

// restoreSnapshots performs the actual restoration of PVCs from snapshots.
// For each snapshot, it deletes the existing PVC and recreates it with the snapshot as data source.
// This allows the PVC to be populated with data from the snapshot.
func (r *StatefulSetRestoreReconciler)restoreSnapshots(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore, sts *appsv1.StatefulSet, snapshots []snapshotv1.VolumeSnapshot) (error) {
	logger := logf.FromContext(ctx)
	for _, snapshot := range snapshots {
		if snapshot.Spec.Source.PersistentVolumeClaimName == nil {
			return fmt.Errorf("Snapshot " + snapshot.Name + " has no PVC bounded")
		}
		pvcName := snapshot.Spec.Source.PersistentVolumeClaimName
		existingPVC := corev1.PersistentVolumeClaim{}
		pvcKey := types.NamespacedName{
			Name: *pvcName,
			Namespace: sts.Namespace,
		}
		// Get the PVC associated with the snapshot
		if err := r.Get(ctx, pvcKey, &existingPVC); err != nil {
			logger.Error(err, "Failed to get PVC " + pvcKey.Name + "from snapshot " + snapshot.Name)
			return err
		}
		// Delete the existing PVC
		if err := r.Delete(ctx, &existingPVC); err != nil {
			logger.Error(err, "Failed to delete PVC " + pvcKey.Name + "from snapshot " + snapshot.Name)
			return err
		}

		// Wait for PVC deletion to complete
		time.Sleep(10 * time.Second)

		// Recreate the PVC with snapshot as data source
		newPVC := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      *pvcName,
				Namespace: sts.Namespace,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *existingPVC.Spec.Resources.Requests.Storage(),
					},
				},
				DataSource: &corev1.TypedLocalObjectReference{
					APIGroup: func(s string) *string { return &s }("snapshot.storage.k8s.io"),
					Kind:     "VolumeSnapshot",
					Name:     snapshot.Name,
				},
				StorageClassName: existingPVC.Spec.StorageClassName,
			},
		}

		if err := r.Create(ctx, newPVC); err != nil {
			logger.Error(err, "Failed to creaye PVC " + newPVC.Name + "from snapshot " + snapshot.Name)
			return fmt.Errorf("failed to create PVC from snapshot: %w", err)
		}

		logger.Info("PVC restored from snapshot", "pvc", pvcName)
	}
	return nil
}

// handleScalingUp restores the StatefulSet to its original replica count after restore.
// It scales the StatefulSet back up and waits for all pods to become ready.
// Once all replicas are ready, it marks the restore as completed.
func (r *StatefulSetRestoreReconciler) handleScalingUp(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore, sts *appsv1.StatefulSet,) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Restore the original replica count
	if restore.Status.OriginalReplicas != nil {
		sts.Spec.Replicas = restore.Status.OriginalReplicas
		if err := r.Update(ctx, sts); err != nil {
			logger.Error(err, "Failed to scale up StatefulSet")
			return ctrl.Result{}, err
		}
	}

	// Wait for all pods to be ready
	if sts.Status.ReadyReplicas < *sts.Spec.Replicas {
		logger.Info("Waiting for pods to be ready",
			"ready", sts.Status.ReadyReplicas,
			"desired", *sts.Spec.Replicas)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Restore completed successfully
	logger.Info("Restore completed successfully")
	now := metav1.Now()
	restore.Status.CompletionTime = &now
	r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseCompleted,
		"Completed", "Restore completed successfully")
	return ctrl.Result{}, nil
}

// handleRestoring manages the actual restoration phase.
// It finds the appropriate snapshots and restores them to the PVCs.
// Once restoration is complete, it transitions to the ScalingUp phase.
func (r *StatefulSetRestoreReconciler) handleRestoring(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore, sts *appsv1.StatefulSet) (ctrl.Result, error){
	logger := logf.FromContext(ctx)
	logger.Info("Performing restore for statefulset: " + sts.GetName())

	snapshots, err := r.findSnapshotToRestore(ctx, restore);
	if err != nil {
		logger.Error(err, "Failed to find Snapshot")
		r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseFailed, "SnapshotsNotFound", fmt.Sprintf("Failed to find snapshots: %v", err))
	}

	err = r.restoreSnapshots(ctx, restore, sts, snapshots)
	if err != nil {
		r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseFailed, "RestoreFailed", "Some snaphosts failed to restore")
		return ctrl.Result{}, err
	}
	
	r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseScalingUp, "ScalingUp", "Restore completed, scaling up StatefulSet")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// handleScalingDown scales the StatefulSet down to zero replicas before restoration.
// This is necessary to ensure no pods are using the PVCs during the restore process.
// Once scaled down, it transitions to the Restoring phase.
func (r *StatefulSetRestoreReconciler) handleScalingDown(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore, sts *appsv1.StatefulSet) (ctrl.Result, error){
	logger := logf.FromContext(ctx)
	if *sts.Spec.Replicas == 0 {
		logger.Info("StatefulSet " + sts.GetName() + "scaled down successfully")
		r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseRestoring, "Restoring", "Statefulset scaled down, starting restore")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	} else {
		logger.Info("StatefulSet " + sts.GetName() + ": scaling down to zero replicas before restore")
		*sts.Spec.Replicas = 0
		if err := r.Update(ctx, sts); err != nil {
			r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhase(backupv1alpha1.BackupPhaseFailed), "StatefulSet not sclaed down", "Unable to scaling down StatefulSet "+ sts.GetName())
			return ctrl.Result{}, fmt.Errorf("Unable to scaling down StatefulSet " + sts.GetName())
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
}

// handleNewRestore initializes a new restore operation.
// It saves the original replica count and decides whether to scale down the StatefulSet
// based on the ScaleDown specification. Transitions to either ScalingDown or Restoring phase.
func (r *StatefulSetRestoreReconciler) handleNewRestore(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore, sts *appsv1.StatefulSet) (ctrl.Result, error){
	logger := logf.FromContext(ctx)
	logger.Info("Performing restore")

	restore.Status.OriginalReplicas = sts.Spec.Replicas
	now := metav1.Now()
	restore.Status.StartTime = &now

	scaleDown := true
	if restore.Spec.ScaleDown != nil {
		scaleDown = *restore.Spec.ScaleDown
	}

	if scaleDown {
		r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseScalingDown, "ScalingDown", "Scaling down StatefulSet to 0 replicas")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseRestoring, "Restoring", "Starting restore without scaling down")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// updateRestoreStatus updates the status phase and conditions of a restore resource.
// It sets the restore phase, adds or updates a status condition with the provided
// reason and message, and persists the changes to the Kubernetes API server.
func (r *StatefulSetRestoreReconciler) updateRestoreStatus(ctx context.Context,restore *backupv1alpha1.StatefulSetRestore,phase backupv1alpha1.RestorePhase,reason, message string,) {
	restore.Status.Phase = phase
	meta.SetStatusCondition(&restore.Status.Conditions, metav1.Condition{
		Type:               string(phase),
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: restore.Generation,
	})
	// FIX ME: CHECK UPDATE ERROR
	r.Status().Update(ctx, restore)
}

// SetupWithManager sets up the controller with the Manager.
func (r *StatefulSetRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.StatefulSetRestore{}).
		Named("statefulsetrestore").
		Complete(r)
}
