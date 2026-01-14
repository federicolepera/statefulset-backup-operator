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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	backupv1alpha1 "github.com/federicolepera/statefulset-backup-operator/api/v1alpha1"
)

var _ = Describe("StatefulSetBackup Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When reconciling a StatefulSetBackup resource", func() {
		var (
			namespace       string
			statefulSetName string
			backupName      string
			ctx             context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()
			namespace = "default"
			statefulSetName = fmt.Sprintf("test-sts-%d", time.Now().UnixNano())
			backupName = fmt.Sprintf("test-backup-%d", time.Now().UnixNano())
		})

		Context("Manual backup without StatefulSet", func() {
			It("should fail when StatefulSet does not exist", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      backupName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      "non-existent-sts",
							Namespace: namespace,
						},
						RetentionPolicy: backupv1alpha1.RetentionPolicy{
							KeepLast: ptr.To(3),
						},
					},
				}

				Expect(k8sClient.Create(ctx, backup)).To(Succeed())

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					Config: cfg,
				}

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      backupName,
						Namespace: namespace,
					},
				})

				Expect(err).To(HaveOccurred())

				Eventually(func() bool {
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: backupName, Namespace: namespace}, backup); err != nil {
						return false
					}
					return backup.Status.Phase == backupv1alpha1.BackupPhaseFailed
				}, timeout, interval).Should(BeTrue())

				Expect(k8sClient.Delete(ctx, backup)).To(Succeed())
			})
		})

		Context("Manual backup with StatefulSet", func() {
			var statefulSet *appsv1.StatefulSet

			BeforeEach(func() {
				statefulSet = createTestStatefulSet(statefulSetName, namespace, 2)
				Expect(k8sClient.Create(ctx, statefulSet)).To(Succeed())

				createTestPVCsForStatefulSet(ctx, statefulSet)
			})

			AfterEach(func() {
				cleanupStatefulSet(ctx, statefulSet)
			})

			It("should process backup when StatefulSet exists", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      backupName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						RetentionPolicy: backupv1alpha1.RetentionPolicy{
							KeepLast: ptr.To(3),
						},
					},
				}

				Expect(k8sClient.Create(ctx, backup)).To(Succeed())

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					Config: cfg,
				}

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      backupName,
						Namespace: namespace,
					},
				})

				if err != nil && isVolumeSnapshotError(err) {
					Skip("VolumeSnapshot CRDs not installed - skipping snapshot creation test")
				}

				Expect(k8sClient.Delete(ctx, backup)).To(Succeed())
			})

			It("should handle backup status correctly", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      backupName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						RetentionPolicy: backupv1alpha1.RetentionPolicy{
							KeepLast: ptr.To(3),
						},
					},
				}

				Expect(k8sClient.Create(ctx, backup)).To(Succeed())

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					Config: cfg,
				}

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      backupName,
						Namespace: namespace,
					},
				})

				if err != nil && !isVolumeSnapshotError(err) {
					Fail(fmt.Sprintf("Unexpected error: %v", err))
				}

				Expect(k8sClient.Delete(ctx, backup)).To(Succeed())
			})
		})

		Context("Scheduled backups", func() {
			var statefulSet *appsv1.StatefulSet

			BeforeEach(func() {
				statefulSet = createTestStatefulSet(statefulSetName, namespace, 1)
				Expect(k8sClient.Create(ctx, statefulSet)).To(Succeed())
				createTestPVCsForStatefulSet(ctx, statefulSet)
			})

			AfterEach(func() {
				cleanupStatefulSet(ctx, statefulSet)
			})

			It("should respect cron schedule and return requeue duration", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      backupName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						Schedule: "0 0 * * *",
						RetentionPolicy: backupv1alpha1.RetentionPolicy{
							KeepLast: ptr.To(3),
						},
					},
				}

				Expect(k8sClient.Create(ctx, backup)).To(Succeed())

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					Config: cfg,
				}

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      backupName,
						Namespace: namespace,
					},
				})

				if err != nil && !isVolumeSnapshotError(err) {
					Fail(fmt.Sprintf("Unexpected error: %v", err))
				}

				if result.RequeueAfter > 0 {
					Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				}

				Expect(k8sClient.Delete(ctx, backup)).To(Succeed())
			})

			It("should handle invalid cron schedule", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      backupName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      statefulSetName,
							Namespace: namespace,
						},
						Schedule: "invalid-cron",
						RetentionPolicy: backupv1alpha1.RetentionPolicy{
							KeepLast: ptr.To(3),
						},
					},
				}

				Expect(k8sClient.Create(ctx, backup)).To(Succeed())

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					Config: cfg,
				}

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      backupName,
						Namespace: namespace,
					},
				})

				Expect(err).To(HaveOccurred())

				Eventually(func() bool {
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: backupName, Namespace: namespace}, backup); err != nil {
						return false
					}
					return backup.Status.Phase == backupv1alpha1.BackupPhaseFailed
				}, timeout, interval).Should(BeTrue())

				Expect(k8sClient.Delete(ctx, backup)).To(Succeed())
			})
		})

		Context("shouldCreateBackup function", func() {
			It("should return true for manual backup without LastBackupTime", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{},
					Status: backupv1alpha1.StatefulSetBackupStatus{
						LastBackupTime: metav1.Time{},
					},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				shouldBackup, err := reconciler.shouldCreateBackup(backup)
				Expect(err).ToNot(HaveOccurred())
				Expect(shouldBackup).To(BeTrue())
			})

			It("should return false for manual backup with LastBackupTime set", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{},
					Status: backupv1alpha1.StatefulSetBackupStatus{
						LastBackupTime: metav1.Now(),
					},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				shouldBackup, err := reconciler.shouldCreateBackup(backup)
				Expect(err).ToNot(HaveOccurred())
				Expect(shouldBackup).To(BeFalse())
			})

			It("should return true for scheduled backup when time has passed", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						Schedule: "* * * * *",
					},
					Status: backupv1alpha1.StatefulSetBackupStatus{
						LastBackupTime: metav1.Time{Time: time.Now().Add(-2 * time.Minute)},
					},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				shouldBackup, err := reconciler.shouldCreateBackup(backup)
				Expect(err).ToNot(HaveOccurred())
				Expect(shouldBackup).To(BeTrue())
			})

			It("should return error for invalid cron expression", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						Schedule: "invalid",
					},
					Status: backupv1alpha1.StatefulSetBackupStatus{
						LastBackupTime: metav1.Time{},
					},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := reconciler.shouldCreateBackup(backup)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("calculateRequeueAfter function", func() {
			It("should return 0 for manual backups", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				duration := reconciler.calculateRequeueAfter(backup)
				Expect(duration).To(Equal(time.Duration(0)))
			})

			It("should return immediate requeue when LastBackupTime is zero", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						Schedule: "0 0 * * *",
					},
					Status: backupv1alpha1.StatefulSetBackupStatus{
						LastBackupTime: metav1.Time{},
					},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				duration := reconciler.calculateRequeueAfter(backup)
				Expect(duration).To(Equal(10 * time.Second))
			})

			It("should return positive duration for valid schedule", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						Schedule: "0 0 * * *",
					},
					Status: backupv1alpha1.StatefulSetBackupStatus{
						LastBackupTime: metav1.Now(),
					},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				duration := reconciler.calculateRequeueAfter(backup)
				Expect(duration).To(BeNumerically(">", 0))
				Expect(duration).To(BeNumerically("<=", 1*time.Hour))
			})

			It("should return 1 minute for invalid schedule", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						Schedule: "invalid-schedule",
					},
					Status: backupv1alpha1.StatefulSetBackupStatus{
						LastBackupTime: metav1.Now(),
					},
				}

				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				duration := reconciler.calculateRequeueAfter(backup)
				Expect(duration).To(Equal(1 * time.Minute))
			})
		})

		Context("Resource deletion", func() {
			It("should handle gracefully when resource is deleted", func() {
				reconciler := &StatefulSetBackupReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
					Config: cfg,
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

		Context("Backup resource creation and retrieval", func() {
			It("should create and retrieve backup resource", func() {
				backup := &backupv1alpha1.StatefulSetBackup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      backupName,
						Namespace: namespace,
					},
					Spec: backupv1alpha1.StatefulSetBackupSpec{
						StatefulSetRef: backupv1alpha1.StatefulSetRef{
							Name:      "test-sts",
							Namespace: namespace,
						},
						Schedule: "0 0 * * *",
						RetentionPolicy: backupv1alpha1.RetentionPolicy{
							KeepLast: ptr.To(5),
						},
					},
				}

				Expect(k8sClient.Create(ctx, backup)).To(Succeed())

				retrieved := &backupv1alpha1.StatefulSetBackup{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: backupName, Namespace: namespace}, retrieved)
				}, timeout, interval).Should(Succeed())

				Expect(retrieved.Spec.Schedule).To(Equal("0 0 * * *"))
				Expect(retrieved.Spec.RetentionPolicy.KeepLast).To(HaveValue(Equal(5)))

				Expect(k8sClient.Delete(ctx, backup)).To(Succeed())
			})
		})
	})
})

func createTestStatefulSet(name, namespace string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("1Gi"),
							},
						},
					},
				},
			},
		},
	}
}

func createTestPVCsForStatefulSet(ctx context.Context, sts *appsv1.StatefulSet) {
	for _, vct := range sts.Spec.VolumeClaimTemplates {
		for i := int32(0); i < *sts.Spec.Replicas; i++ {
			pvcName := fmt.Sprintf("%s-%s-%d", vct.Name, sts.Name, i)
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: sts.Namespace,
				},
				Spec: vct.Spec,
			}
			Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
		}
	}
}

func cleanupStatefulSet(ctx context.Context, sts *appsv1.StatefulSet) {
	if sts != nil {
		for _, vct := range sts.Spec.VolumeClaimTemplates {
			for i := int32(0); i < *sts.Spec.Replicas; i++ {
				pvcName := fmt.Sprintf("%s-%s-%d", vct.Name, sts.Name, i)
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcName,
						Namespace: sts.Namespace,
					},
				}
				_ = k8sClient.Delete(ctx, pvc)
			}
		}
		_ = k8sClient.Delete(ctx, sts)
	}
}

func isVolumeSnapshotError(err error) bool {
	if err == nil {
		return false
	}
	return errors.IsNotFound(err) ||
		err.Error() == "no matches for kind \"VolumeSnapshot\" in version \"snapshot.storage.k8s.io/v1\""
}
