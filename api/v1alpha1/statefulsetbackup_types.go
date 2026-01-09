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

// StatefulSetRef references a StatefulSet resource in a specific namespace.
type StatefulSetRef struct {
	// Name of the StatefulSet
	Name string `json:"name,omitempty"`
	// Namespace where the StatefulSet is located
	Namespace string `json:"namespace,omitempty"`
}

// RetentionPolicy defines how long backups should be retained.
type RetentionPolicy struct {
	// KeepLast specifies how many recent backups to keep per PVC
	KeepLast int `json:"keepLast"`
	// KeepDays specifies how many days to keep backups (optional, not yet implemented)
	// +optional
	KeepDays *int `json:"keepDays,omitempty"`
}

// BackupHook defines a command to execute during backup operations.
type BackupHook struct {
	// Command is the array of command and arguments to execute
	Command []string `json:"command,omitempty"`
}
// StatefulSetBackupSpec defines the desired state of StatefulSetBackup.
type StatefulSetBackupSpec struct {
	// StatefulSetRef references the StatefulSet to backup
	// +optional
	StatefulSetRef StatefulSetRef `json:"statefulSetRef,omitempty"`

	// Schedule is a cron expression for scheduled backups (e.g., "0 2 * * *" for daily at 2 AM)
	// If empty, the backup is created only once manually
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// RetentionPolicy specifies how many backups to keep
	// +optional
	RetentionPolicy RetentionPolicy `json:"retentionPolicy,omitempty"`

	// PreBackupHook defines a command to execute before taking the backup
	// Useful for flushing caches, creating consistent snapshots, etc.
	// +optional
	PreBackupHook BackupHook `json:"preBackupHook,omitempty"`

	// PostBackupHook defines a command to execute after taking the backup
	// Useful for cleanup or notifications
	// +optional
	PostBackupHook BackupHook `json:"postBackupHook,omitempty"`
}

// BackupPhase represents the current phase of a backup operation.
type BackupPhase string

const (
	// BackupPhaseReady indicates the backup is ready and completed successfully
	BackupPhaseReady      BackupPhase = "Ready"
	// BackupPhaseInProgress indicates the backup is currently in progress
	BackupPhaseInProgress BackupPhase = "InProgress"
	// BackupPhaseFailed indicates the backup failed
	BackupPhaseFailed     BackupPhase = "Failed"
)
// StatefulSetBackupStatus defines the observed state of StatefulSetBackup.
type StatefulSetBackupStatus struct {
	// Conditions represent the current state of the backup resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase represents the current phase of the backup operation
	// +optional
	Phase BackupPhase `json:"phase,omitempty"`

	// LastBackupTime is the timestamp of the most recent backup
	// +optional
	LastBackupTime metav1.Time `json:"lastBackupTime,omitempty"`
}

// SnapshotInfo contains information about a volume snapshot.
type SnapshotInfo struct {
	// Name of the VolumeSnapshot resource
	Name         string      `json:"name"`
	// CreationTime is when the snapshot was created
	CreationTime metav1.Time `json:"creationTime"`
	// PVCName is the name of the PVC that was snapshotted
	PVCName      string      `json:"pvcName"`
	// Size is the size of the snapshot (optional)
	// +optional
	Size         string      `json:"size,omitempty"`
	// ReadyToUse indicates if the snapshot is ready to be used for restore
	ReadyToUse   bool        `json:"readyToUse"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// StatefulSetBackup is the Schema for the statefulsetbackups API
type StatefulSetBackup struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of StatefulSetBackup
	// +required
	Spec StatefulSetBackupSpec `json:"spec"`

	// status defines the observed state of StatefulSetBackup
	// +optional
	Status StatefulSetBackupStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// StatefulSetBackupList contains a list of StatefulSetBackup
type StatefulSetBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StatefulSetBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StatefulSetBackup{}, &StatefulSetBackupList{})
}
