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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatefulSetRestoreSpec defines how to perform the restore operation.
type StatefulSetRestoreSpec struct {
	// StatefulSetRef references the StatefulSet to restore
	StatefulSetRef StatefulSetRef `json:"statefulSetRef"`

	// BackupName specifies the name of the backup to restore from
	// If not specified, use UseLatestBackup instead
	// +optional
	BackupName string `json:"backupName,omitempty"`

	// UseLatestBackup restores from the most recent backup
	// If true, ignores BackupName and uses the latest available backup
	// +optional
	UseLatestBackup bool `json:"useLatestBackup,omitempty"`

	// SnapshotNames specifies a list of specific snapshots to restore (one per PVC)
	// Example: ["nginx-backup-1234567890-data-0", "nginx-backup-1234567890-data-1"]
	// Useful for selective restore (e.g., restore only pod-0's PVC)
	// If not specified, restores all PVCs of the StatefulSet
	// +optional
	SnapshotNames []string `json:"snapshotNames,omitempty"`

	// ScaleDown determines whether to scale the StatefulSet to 0 before restore
	// Default: true (recommended for consistency)
	// +optional
	ScaleDown *bool `json:"scaleDown,omitempty"`
}

// StatefulSetRestoreStatus defines the observed state of the restore operation.
type StatefulSetRestoreStatus struct {
	// Phase represents the current phase of the restore operation
	// +optional
	Phase RestorePhase `json:"phase,omitempty"`

	// Conditions represent the current state of the restore resource
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// StartTime is when the restore operation started
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the restore operation completed
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// RestoredSnapshots contains information about restored snapshots
	// +optional
	RestoredSnapshots []RestoredSnapshotInfo `json:"restoredSnapshots,omitempty"`

	// OriginalReplicas stores the original replica count (used for scaling up after restore)
	// +optional
	OriginalReplicas *int32 `json:"originalReplicas,omitempty"`
}

// RestoredSnapshotInfo contains information about a restored snapshot.
type RestoredSnapshotInfo struct {
	// SnapshotName is the name of the snapshot that was restored
	SnapshotName string      `json:"snapshotName"`
	// PVCName is the name of the PVC that was restored
	PVCName      string      `json:"pvcName"`
	// RestoreTime is when the snapshot was restored
	RestoreTime  metav1.Time `json:"restoreTime"`
	// Success indicates whether the restore was successful
	Success      bool        `json:"success"`
}

// RestorePhase represents the current phase of a restore operation.
type RestorePhase string

const (
	// RestorePhaseNew indicates a newly created restore resource
	RestorePhaseNew        RestorePhase = "New"
	// RestorePhaseScalingDown indicates the StatefulSet is being scaled down to 0
	RestorePhaseScalingDown RestorePhase = "ScalingDown"
	// RestorePhaseRestoring indicates PVCs are being restored from snapshots
	RestorePhaseRestoring  RestorePhase = "Restoring"
	// RestorePhaseScalingUp indicates the StatefulSet is being scaled back up
	RestorePhaseScalingUp  RestorePhase = "ScalingUp"
	// RestorePhaseCompleted indicates the restore completed successfully
	RestorePhaseCompleted  RestorePhase = "Completed"
	// RestorePhaseFailed indicates the restore failed
	RestorePhaseFailed     RestorePhase = "Failed"
)
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// StatefulSetRestore is the Schema for the statefulsetrestores API
type StatefulSetRestore struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of StatefulSetRestore
	// +required
	Spec StatefulSetRestoreSpec `json:"spec"`

	// status defines the observed state of StatefulSetRestore
	// +optional
	Status StatefulSetRestoreStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// StatefulSetRestoreList contains a list of StatefulSetRestore
type StatefulSetRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StatefulSetRestore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StatefulSetRestore{}, &StatefulSetRestoreList{})
}
