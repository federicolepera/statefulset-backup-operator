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

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/federicolepera/statefulset-backup-operator/api/v1alpha1"
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
		return ctrl.Result{}, err
	}

	
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StatefulSetRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.StatefulSetRestore{}).
		Named("statefulsetrestore").
		Complete(r)
}
