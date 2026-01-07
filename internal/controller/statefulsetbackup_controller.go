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

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
}

// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=backup.sts-backup.io,resources=statefulsetbackups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the StatefulSetBackup object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/reconcile
func (r *StatefulSetBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	backup := &backupv1alpha1.StatefulSetBackup{}
	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "ciao")
		return ctrl.Result{}, err
	}

	sts := &appsv1.StatefulSet{}
	stsKey := types.NamespacedName {
		Name: backup.Spec.StatefulSetRef.Name,
		Namespace: backup.Spec.StatefulSetRef.Namespace,
	}

	if err := r.Get(ctx, stsKey, sts); err != nil {
		logger.Error(err, "Unable to get StatefulSet")
		backup.Status.Phase = backupv1alpha1.BackupPhaseFailed
		if updateErr := r.Status().Update(ctx, backup); updateErr != nil {
			logger.Error(updateErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	shouldBackup, err := r.shouldCreateBackup(backup)
	if err != nil {
		logger.Error(err, "")
		backup.Status.Phase = backupv1alpha1.BackupPhaseFailed
		if updateErr := r.Status().Update(ctx, backup); updateErr != nil {
			logger.Error(updateErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	if shouldBackup {
		logger.Info("Creating new backup")

		r.createSnapshots(ctx, sts, backup)

		now := metav1.Now()
		backup.Status.LastBackupTime = now
		backup.Status.Phase = backupv1alpha1.BackupPhaseReady

		if updateErr := r.Status().Update(ctx, backup); updateErr != nil {
			logger.Error(updateErr, "Failed to update status")
		}
	}

	requeueAfter := r.calculateRequeueAfter(backup)
	if requeueAfter > 0 {
		logger.Info("Scheduling next reconcile", "after", requeueAfter.String())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	return ctrl.Result{}, nil
}


func (r *StatefulSetBackupReconciler) calculateRequeueAfter(backup *backupv1alpha1.StatefulSetBackup) time.Duration {
	// Se non c'è schedule, non c'è bisogno di requeue
	if backup.Spec.Schedule == "" {
		return 0
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(backup.Spec.Schedule)
	if err != nil {
		// Se lo schedule è invalido, riprova tra 1 minuto
		return 1 * time.Minute
	}

	now := time.Now()
	var nextScheduled time.Time

	if backup.Status.LastBackupTime.IsZero() {
		// Se non c'è mai stato un backup, il prossimo è già "ora"
		return 10 * time.Second
	}

	// Calcola il prossimo backup dopo l'ultimo
	lastBackup := backup.Status.LastBackupTime.Time
	nextScheduled = schedule.Next(lastBackup)

	// Se il prossimo backup è nel passato (perché il controller era down),
	// dovrebbe essere eseguito subito
	if nextScheduled.Before(now) || nextScheduled.Equal(now) {
		return 10 * time.Second
	}

	// Altrimenti calcola quanto tempo manca
	duration := time.Until(nextScheduled)

	// Aggiungi un piccolo buffer per evitare di arrivare troppo presto
	// e controlla qualche secondo dopo l'orario previsto
	duration += 30 * time.Second

	// Limita il requeue massimo a 1 ora per sicurezza
	// (così se c'è un problema, non aspettiamo giorni)
	if duration > 1*time.Hour {
		return 1 * time.Hour
	}

	return duration
}

func (r *StatefulSetBackupReconciler) createSnapshots(ctx context.Context,sts *appsv1.StatefulSet,backup *backupv1alpha1.StatefulSetBackup,) ([]backupv1alpha1.SnapshotInfo, error) {
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

			snapshotName := fmt.Sprintf("%s-%s-%d", backup.Name, vct.Name, time.Now().Unix())
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
				},
			}

			//Per ora non possibile inserire la snapshotclass quindi settata di default
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
func (r *StatefulSetBackupReconciler) shouldCreateBackup(backup *backupv1alpha1.StatefulSetBackup) (bool, error) {
	// Se non c'è schedule, backup manuale (creato solo alla creazione della risorsa)
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
// SetupWithManager sets up the controller with the Manager.
func (r *StatefulSetBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.StatefulSetBackup{}).
		Named("statefulsetbackup").
		Complete(r)
}
