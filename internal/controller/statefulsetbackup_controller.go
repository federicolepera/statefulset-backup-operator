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

//FIX-ME: DUP functions executePreBackupHook executePostBackupHook
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

		//FIX-ME: Add in the CRD the containerName to use for pre-hook-execution
		containerName := pod.Spec.Containers[0].Name

		//EXEC to first container che hook command
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

func (r *StatefulSetBackupReconciler) applyRetentionPolicy(ctx context.Context,backup *backupv1alpha1.StatefulSetBackup) error {
	logger := log.FromContext(ctx)
	
	// Lista tutti i snapshot per questa policy NEL NAMESPACE CORRETTO
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

	// Raggruppa snapshot per PVC (ogni replica ha il suo PVC)
	snapshotsByPVC := make(map[string][]snapshotv1.VolumeSnapshot)
	for _, snapshot := range snapshotList.Items {
		if snapshot.Spec.Source.PersistentVolumeClaimName != nil {
			pvcName := *snapshot.Spec.Source.PersistentVolumeClaimName
			snapshotsByPVC[pvcName] = append(snapshotsByPVC[pvcName], snapshot)
		}
	}

	// Per ogni PVC, mantieni solo gli ultimi N snapshot
	for pvcName, pvcSnapshots := range snapshotsByPVC {
		if len(pvcSnapshots) <= backup.Spec.RetentionPolicy.KeepLast {
			continue // Non c'è nulla da cancellare
		}

		logger.Info("Applying retention policy", 
			"pvc", pvcName, 
			"total", len(pvcSnapshots), 
			"keepLast", backup.Spec.RetentionPolicy.KeepLast)

		// Ordina per data di creazione (dal più vecchio al più recente)
		sort.Slice(pvcSnapshots, func(i, j int) bool {
			return pvcSnapshots[i].CreationTimestamp.Before(&pvcSnapshots[j].CreationTimestamp)
		})

		// Calcola quanti snapshot eliminare
		toDelete := len(pvcSnapshots) - backup.Spec.RetentionPolicy.KeepLast

		// Elimina i più vecchi
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