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

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	}
	return ctrl.Result{}, nil
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
