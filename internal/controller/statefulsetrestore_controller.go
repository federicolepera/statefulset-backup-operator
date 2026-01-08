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
// TODO(user): Modify the Reconcile function to compare the state specified by
// the StatefulSetRestore object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/reconcile
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
	
	// Se il restore è già completato o fallito, non fare nulla
	if restore.Status.Phase == backupv1alpha1.RestorePhaseCompleted || restore.Status.Phase == backupv1alpha1.RestorePhaseFailed {
		return ctrl.Result{}, nil
	}

	// Recupera lo StatefulSet target
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
func (r *StatefulSetRestoreReconciler) findSnapshotToRestore(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore) ([]snapshotv1.VolumeSnapshot, error){
	logger := logf.FromContext(ctx)
	
	if restore.Spec.BackupName == "" && !restore.Spec.UseLatestBackup {
		return nil, fmt.Errorf("either backupName or useLatestBackup must be specified")
	}
	//case 1: cr contains BackupName cr
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

	//FIX-ME this is only for make go compile
	return nil,nil
}

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
		//GET the PVC from snapshot
		if err := r.Get(ctx, pvcKey, &existingPVC); err != nil {
			logger.Error(err, "Failed to get PVC " + pvcKey.Name + "from snapshot " + snapshot.Name)
			return err
		}
		//DELETE the PVC
		if err := r.Delete(ctx, &existingPVC); err != nil {
			logger.Error(err, "Failed to delete PVC " + pvcKey.Name + "from snapshot " + snapshot.Name)
			return err
		}

		//Wait 10 second for deleting pvc process
		time.Sleep(10 * time.Second)

		//Recrate the same PVC with new DataSource
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
func (r *StatefulSetRestoreReconciler) handleScalingUp(ctx context.Context, restore *backupv1alpha1.StatefulSetRestore, sts *appsv1.StatefulSet,) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Ripristina il numero di repliche originali
	if restore.Status.OriginalReplicas != nil {
		sts.Spec.Replicas = restore.Status.OriginalReplicas
		if err := r.Update(ctx, sts); err != nil {
			logger.Error(err, "Failed to scale up StatefulSet")
			return ctrl.Result{}, err
		}
	}

	// Aspetta che i pod siano pronti
	if sts.Status.ReadyReplicas < *sts.Spec.Replicas {
		logger.Info("Waiting for pods to be ready",
			"ready", sts.Status.ReadyReplicas,
			"desired", *sts.Spec.Replicas)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Completato!
	logger.Info("Restore completed successfully")
	now := metav1.Now()
	restore.Status.CompletionTime = &now
	r.updateRestoreStatus(ctx, restore, backupv1alpha1.RestorePhaseCompleted,
		"Completed", "Restore completed successfully")
	return ctrl.Result{}, nil
}
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
