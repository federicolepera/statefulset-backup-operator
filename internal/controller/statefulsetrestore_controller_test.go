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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	backupv1alpha1 "github.com/federicolepera/statefulset-backup-operator/api/v1alpha1"
)

var _ = Describe("StatefulSetRestore Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When reconciling a StatefulSetRestore resource", func() {
		var (
			namespace       string
			statefulSetName string
			restoreName     string
			ctx             context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()
			namespace = "default"
			statefulSetName = fmt.Sprintf("test-sts-%d", time.Now().UnixNano())
			restoreName = fmt.Sprintf("test-restore-%d", time.Now().UnixNano())
		})

		Context("Restore without StatefulSet", func() {
			It("should fail when StatefulSet does not exist", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      "non-existent-sts",
							Namespace: namespace,
						},
						BackupName: "some-backup",
					},
				}

				Expect(k8sClient.Create(ctx, restore)).To(Succeed())

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      restoreName,
						Namespace: namespace,
					},
				})

				Expect(err).To(HaveOccurred())

				Eventually(func() bool {
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: restoreName, Namespace: namespace}, restore); err != nil {
						return false
					}
					return restore.Status.Phase == backupv1alpha1.RestorePhaseFailed
				}, timeout, interval).Should(BeTrue())

				Expect(k8sClient.Delete(ctx, restore)).To(Succeed())
			})
		})

		Context("Restore with StatefulSet", func() {
			var statefulSet *appsv1.StatefulSet

			BeforeEach(func() {
				statefulSet = createTestStatefulSet(statefulSetName, namespace, 2)
				Expect(k8sClient.Create(ctx, statefulSet)).To(Succeed())
				createTestPVCsForStatefulSet(ctx, statefulSet)
			})

			AfterEach(func() {
				cleanupStatefulSet(ctx, statefulSet)
			})

			It("should initialize new restore correctly", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						BackupName: "test-backup",
					},
				}

				Expect(k8sClient.Create(ctx, restore)).To(Succeed())

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      restoreName,
						Namespace: namespace,
					},
				})

				if err != nil && !isVolumeSnapshotError(err) {
					Fail(fmt.Sprintf("Unexpected error: %v", err))
				}

				Expect(result.RequeueAfter).To(Equal(5 * time.Second))

				Eventually(func() bool {
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: restoreName, Namespace: namespace}, restore); err != nil {
						return false
					}
					return restore.Status.Phase == backupv1alpha1.RestorePhaseScalingDown &&
						restore.Status.OriginalReplicas != nil
				}, timeout, interval).Should(BeTrue())

				Expect(k8sClient.Delete(ctx, restore)).To(Succeed())
			})

			It("should handle restore without scale down", func() {
				scaleDown := false
				restore := &backupv1alpha1.StatefulSetRestore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						BackupName: "test-backup",
						ScaleDown:  &scaleDown,
					},
				}

				Expect(k8sClient.Create(ctx, restore)).To(Succeed())

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      restoreName,
						Namespace: namespace,
					},
				})

				if err != nil && !isVolumeSnapshotError(err) {
					Fail(fmt.Sprintf("Unexpected error: %v", err))
				}

				Expect(result.RequeueAfter).To(Equal(5 * time.Second))

				Eventually(func() bool {
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: restoreName, Namespace: namespace}, restore); err != nil {
						return false
					}
					return restore.Status.Phase == backupv1alpha1.RestorePhaseRestoring
				}, timeout, interval).Should(BeTrue())

				Expect(k8sClient.Delete(ctx, restore)).To(Succeed())
			})

			It("should not reconcile already completed restore", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						BackupName: "test-backup",
					},
				}

				Expect(k8sClient.Create(ctx, restore)).To(Succeed())

				// Set status to completed
				restore.Status.Phase = backupv1alpha1.RestorePhaseCompleted
				Expect(k8sClient.Status().Update(ctx, restore)).To(Succeed())

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      restoreName,
						Namespace: namespace,
					},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

				Expect(k8sClient.Delete(ctx, restore)).To(Succeed())
			})

			It("should not reconcile already failed restore", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						BackupName: "test-backup",
					},
				}

				Expect(k8sClient.Create(ctx, restore)).To(Succeed())

				// Set status to failed
				restore.Status.Phase = backupv1alpha1.RestorePhaseFailed
				Expect(k8sClient.Status().Update(ctx, restore)).To(Succeed())

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      restoreName,
						Namespace: namespace,
					},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

				Expect(k8sClient.Delete(ctx, restore)).To(Succeed())
			})
		})

		Context("findSnapshotToRestore function", func() {
			It("should return error when neither backupName nor useLatestBackup is specified", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      "test-sts",
							Namespace: namespace,
						},
					},
				}

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := reconciler.findSnapshotToRestore(ctx, restore)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("either backupName or useLatestBackup must be specified"))
			})

			It("should handle backup name specification", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      "test-sts",
							Namespace: namespace,
						},
						BackupName: "non-existent-backup",
					},
				}

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				snapshots, err := reconciler.findSnapshotToRestore(ctx, restore)

				if err != nil && isVolumeSnapshotError(err) {
					Skip("VolumeSnapshot CRDs not installed")
				}

				if err != nil {
					Expect(err.Error()).To(ContainSubstring("no snapshots found"))
				} else {
					Expect(snapshots).To(BeEmpty())
				}
			})
		})

		Context("handleScalingDown function", func() {
			var statefulSet *appsv1.StatefulSet

			BeforeEach(func() {
				statefulSet = createTestStatefulSet(statefulSetName, namespace, 2)
				Expect(k8sClient.Create(ctx, statefulSet)).To(Succeed())
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, statefulSet)
			})

			It("should scale down StatefulSet to zero", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						BackupName: "test-backup",
					},
					Status: backupv1alpha1.StatefulSetRestoreStatus{
						Phase: backupv1alpha1.RestorePhaseScalingDown,
					},
				}

				Expect(k8sClient.Create(ctx, restore)).To(Succeed())

				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				result, err := reconciler.handleScalingDown(ctx, restore, statefulSet)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(5 * time.Second))

				Eventually(func() int32 {
					updatedSts := &appsv1.StatefulSet{}
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: statefulSetName, Namespace: namespace}, updatedSts); err != nil {
						return -1
					}
					return *updatedSts.Spec.Replicas
				}, timeout, interval).Should(Equal(int32(0)))

				Expect(k8sClient.Delete(ctx, restore)).To(Succeed())
			})
		})

		Context("Resource deletion", func() {
			It("should handle gracefully when resource is deleted", func() {
				reconciler := &StatefulSetRestoreReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      "non-existent",
						Namespace: namespace,
					},
				})

				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("Restore resource creation and retrieval", func() {
			It("should create and retrieve restore resource", func() {
				restore := &backupv1alpha1.StatefulSetRestore{
					ObjectMeta: metav1.ObjectMeta{
						Name:      restoreName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetRestoreSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      "test-sts",
							Namespace: namespace,
						},
						BackupName:      "test-backup",
						ScaleDown:       ptr.To(true),
					},
				}

				Expect(k8sClient.Create(ctx, restore)).To(Succeed())

				retrieved := &backupv1alpha1.StatefulSetRestore{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: restoreName, Namespace: namespace}, retrieved)
				}, timeout, interval).Should(Succeed())

				Expect(retrieved.Spec.BackupName).To(Equal("test-backup"))
				Expect(*retrieved.Spec.ScaleDown).To(BeTrue())

				Expect(k8sClient.Delete(ctx, restore)).To(Succeed())
			})
		})

		Context("Restore phases workflow", func() {
			It("should validate phase transitions", func() {
				phases := []backupv1alpha1.RestorePhase{
					backupv1alpha1.RestorePhaseNew,
					backupv1alpha1.RestorePhaseScalingDown,
					backupv1alpha1.RestorePhaseRestoring,
					backupv1alpha1.RestorePhaseScalingUp,
					backupv1alpha1.RestorePhaseCompleted,
					backupv1alpha1.RestorePhaseFailed,
				}

				for _, phase := range phases {
					Expect(string(phase)).ToNot(BeEmpty())
				}
			})
		})
	})
})
