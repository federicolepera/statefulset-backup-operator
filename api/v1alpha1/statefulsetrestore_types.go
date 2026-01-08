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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// StatefulSetRestoreSpec definisce come fare il restore
type StatefulSetRestoreSpec struct {
	// Riferimento allo StatefulSet da ripristinare
	StatefulSetRef StatefulSetRef `json:"statefulSetRef"`
	
	// Nome del backup da cui ripristinare
	// Se non specificato, usa l'ultimo backup disponibile
	// +optional
	BackupName string `json:"backupName,omitempty"`
	
	// Restore dal backup più recente
	// Se true, ignora BackupName e usa l'ultimo backup disponibile
	// +optional
	UseLatestBackup bool `json:"useLatestBackup,omitempty"`
	
	// Lista specifica di snapshot da ripristinare (uno per PVC)
	// Esempio: ["nginx-backup-1234567890-data-0", "nginx-backup-1234567890-data-1"]
	// Utile per restore selettivo (es: ripristina solo il PVC del pod-0)
	// Se non specificata, ripristina tutti i PVC dello StatefulSet
	// +optional
	SnapshotNames []string `json:"snapshotNames,omitempty"`
	
	// Se true, scala lo StatefulSet a 0 prima del restore
	// Default: true (consigliato per consistenza)
	// +optional
	ScaleDown *bool `json:"scaleDown,omitempty"`
}

// StatefulSetRestoreStatus definisce lo stato del restore
type StatefulSetRestoreStatus struct {
	// Fase corrente del restore
	Phase RestorePhase `json:"phase,omitempty"`
	
	// Condizioni dello stato del restore
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	
	// Tempo di inizio del restore
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	
	// Tempo di completamento del restore
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	
	// Snapshot ripristinati
	RestoredSnapshots []RestoredSnapshotInfo `json:"restoredSnapshots,omitempty"`
	
	// Numero di repliche originali (per scale up dopo restore)
	OriginalReplicas *int32 `json:"originalReplicas,omitempty"`
}

type RestoredSnapshotInfo struct {
	SnapshotName string      `json:"snapshotName"`
	PVCName      string      `json:"pvcName"`
	RestoreTime  metav1.Time `json:"restoreTime"`
	Success      bool        `json:"success"`
}

type RestorePhase string

const (
	RestorePhaseNew        RestorePhase = "New"
	RestorePhaseScalingDown RestorePhase = "ScalingDown"
	RestorePhaseRestoring  RestorePhase = "Restoring"
	RestorePhaseScalingUp  RestorePhase = "ScalingUp"
	RestorePhaseCompleted  RestorePhase = "Completed"
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
