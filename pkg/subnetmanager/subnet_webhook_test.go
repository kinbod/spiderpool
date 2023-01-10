// Copyright 2022 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

package subnetmanager_test

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/agiledragon/gomonkey/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/spidernet-io/spiderpool/pkg/constant"
	spiderpoolip "github.com/spidernet-io/spiderpool/pkg/ip"
	spiderpoolv1 "github.com/spidernet-io/spiderpool/pkg/k8s/apis/spiderpool.spidernet.io/v1"
	"github.com/spidernet-io/spiderpool/pkg/logutils"
	"github.com/spidernet-io/spiderpool/pkg/subnetmanager"
)

var _ = Describe("SubnetWebhook", Label("subnet_webhook_test"), func() {
	Describe("Set up SubnetWebhook", func() {
		PIt("talks to a Kubernetes API server", func() {
			cfg, err := config.GetConfig()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).NotTo(BeNil())

			mgr, err := ctrl.NewManager(cfg, manager.Options{Scheme: scheme})
			Expect(err).NotTo(HaveOccurred())

			err = subnetWebhook.SetupWebhookWithManager(mgr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Test SubnetWebhook's method", func() {
		var count uint64
		var subnetName, existSubnetName string
		var subnetT, existSubnetT *spiderpoolv1.SpiderSubnet

		BeforeEach(func() {
			subnetmanager.WebhookLogger = logutils.Logger.Named("Subnet-Webhook")
			subnetWebhook.EnableIPv4 = true
			subnetWebhook.EnableIPv6 = true

			atomic.AddUint64(&count, 1)
			subnetName = fmt.Sprintf("subnet-%v", count)
			subnetT = &spiderpoolv1.SpiderSubnet{
				TypeMeta: metav1.TypeMeta{
					Kind:       constant.SpiderSubnetKind,
					APIVersion: fmt.Sprintf("%s/%s", constant.SpiderpoolAPIGroup, constant.SpiderpoolAPIVersionV1),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: subnetName,
				},
				Spec: spiderpoolv1.SubnetSpec{},
			}

			existSubnetName = fmt.Sprintf("z-exist-subnet-%v", count)
			existSubnetT = &spiderpoolv1.SpiderSubnet{
				TypeMeta: metav1.TypeMeta{
					Kind:       constant.SpiderSubnetKind,
					APIVersion: fmt.Sprintf("%s/%s", constant.SpiderpoolAPIGroup, constant.SpiderpoolAPIVersionV1),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: existSubnetName,
				},
				Spec: spiderpoolv1.SubnetSpec{},
			}
		})

		var deleteOption *client.DeleteOptions

		AfterEach(func() {
			policy := metav1.DeletePropagationForeground
			deleteOption = &client.DeleteOptions{
				GracePeriodSeconds: pointer.Int64(0),
				PropagationPolicy:  &policy,
			}

			ctx := context.TODO()
			err := fakeClient.Delete(ctx, subnetT, deleteOption)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			err = fakeClient.Delete(ctx, existSubnetT, deleteOption)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		})

		Describe("Default", func() {
			It("avoids modifying the terminating Subnet", func() {
				now := metav1.Now()
				subnetT.SetDeletionTimestamp(&now)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
			})

			It("adds finalizer", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())

				contains := controllerutil.ContainsFinalizer(subnetT, constant.SpiderFinalizer)
				Expect(contains).To(BeTrue())
			})

			It("failed to set 'spec.ipVersion' due to the invalid 'spec.subnet'", func() {
				subnetT.Spec.Subnet = constant.InvalidCIDR

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.IPVersion).To(BeNil())
			})

			It("sets 'spec.ipVersion' to 4", func() {
				subnetT.Spec.Subnet = "172.18.40.0/24"

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(*subnetT.Spec.IPVersion).To(Equal(constant.IPv4))
			})

			It("sets 'spec.ipVersion' to 6", func() {
				subnetT.Spec.Subnet = "abcd:1234::/120"

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(*subnetT.Spec.IPVersion).To(Equal(constant.IPv6))
			})

			It("failed to merge 'spec.ips' due to the invalid 'spec.ipVersion'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.InvalidIPVersion)
				subnetT.Spec.IPs = append(subnetT.Spec.IPs,
					[]string{
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.IPs).To(Equal(
					[]string{
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					},
				))
			})

			It("failed to merge 'spec.ips' due to the invalid 'spec.ips'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
				subnetT.Spec.IPs = append(subnetT.Spec.IPs,
					[]string{
						constant.InvalidIPRange,
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.IPs).To(Equal(
					[]string{
						constant.InvalidIPRange,
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					},
				))
			})

			It("merges IPv4 'spec.ips'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
				subnetT.Spec.IPs = append(subnetT.Spec.IPs,
					[]string{
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.IPs).To(Equal(
					[]string{
						"172.18.40.1-172.18.40.3",
						"172.18.40.10",
					},
				))
			})

			It("merges IPv6 'spec.ips'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv6)
				subnetT.Spec.IPs = append(subnetT.Spec.IPs,
					[]string{
						"abcd:1234::a",
						"abcd:1234::1-abcd:1234::2",
						"abcd:1234::2-abcd:1234::3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.IPs).To(Equal(
					[]string{
						"abcd:1234::1-abcd:1234::3",
						"abcd:1234::a",
					},
				))
			})

			It("failed to merge 'spec.excludeIPs' due to the invalid 'spec.ipVersion'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.InvalidIPVersion)
				subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs,
					[]string{
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.ExcludeIPs).To(Equal(
					[]string{
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					},
				))
			})

			It("failed to merge 'spec.excludeIPs' due to the invalid 'spec.ips'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
				subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs,
					[]string{
						constant.InvalidIPRange,
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.ExcludeIPs).To(Equal(
					[]string{
						constant.InvalidIPRange,
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					},
				))
			})

			It("merges IPv4 'spec.excludeIPs'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
				subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs,
					[]string{
						"172.18.40.10",
						"172.18.40.1-172.18.40.2",
						"172.18.40.2-172.18.40.3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.ExcludeIPs).To(Equal(
					[]string{
						"172.18.40.1-172.18.40.3",
						"172.18.40.10",
					},
				))
			})

			It("merges IPv6 'spec.excludeIPs'", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv6)
				subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs,
					[]string{
						"abcd:1234::a",
						"abcd:1234::1-abcd:1234::2",
						"abcd:1234::2-abcd:1234::3",
					}...,
				)

				ctx := context.TODO()
				err := subnetWebhook.Default(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnetT.Spec.ExcludeIPs).To(Equal(
					[]string{
						"abcd:1234::1-abcd:1234::3",
						"abcd:1234::a",
					},
				))
			})
		})

		Describe("ValidateCreate", func() {
			When("Validating 'spec.ipVersion'", func() {
				It("inputs nil 'spec.ipVersion'", func() {
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("inputs invalid 'spec.ipVersion'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.InvalidIPVersion)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("creates IPv4 Subnet but IPv4 is disbale'", func() {
					subnetWebhook.EnableIPv4 = false
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("creates IPv6 Subnet but IPv6 is disbale'", func() {
					subnetWebhook.EnableIPv6 = false
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv6)
					subnetT.Spec.Subnet = "adbc:1234::/120"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"adbc:1234::1-adbc:1234::2",
							"adbc:1234::a",
						}...,
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.subnet'", func() {
				It("inputs invalid 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = constant.InvalidCIDR
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("failed to list Subnets due to some unknown errors", func() {
					patches := gomonkey.ApplyMethodReturn(fakeClient, "List", constant.ErrUnknown)
					defer patches.Reset()

					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("creates an existing Subnet", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					ctx := context.TODO()
					err := fakeClient.Create(ctx, subnetT)
					Expect(err).NotTo(HaveOccurred())

					err = subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("failed to compare 'spec.subnet' with existing Subnet", func() {
					existSubnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					existSubnetT.Spec.Subnet = constant.InvalidCIDR
					existSubnetT.Spec.IPs = append(existSubnetT.Spec.IPs, "172.18.41.1-172.18.41.2")

					ctx := context.TODO()
					err := fakeClient.Create(ctx, existSubnetT)
					Expect(err).NotTo(HaveOccurred())

					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					err = subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("overlaps with existing Subnet", func() {
					existSubnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					existSubnetT.Spec.Subnet = "172.18.40.0/25"
					existSubnetT.Spec.IPs = append(existSubnetT.Spec.IPs, "172.18.40.40")

					ctx := context.TODO()
					err := fakeClient.Create(ctx, existSubnetT)
					Expect(err).NotTo(HaveOccurred())

					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					err = subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.ips'", func() {
				It("inputs empty 'spec.ips'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("inputs invalid 'spec.ips'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs, constant.InvalidIPRange)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("inputs 'spec.ips' that do not pertains to 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.41.10",
						}...,
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.excludeIPs'", func() {
				It("inputs invalid 'spec.excludeIPs'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs, constant.InvalidIPRange)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("inputs 'spec.excludeIPs' that do not pertains to 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs, "172.18.41.10")

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.gateway'", func() {
				It("inputs invalid 'spec.gateway'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.Gateway = pointer.String(constant.InvalidIP)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("inputs 'spec.gateway' that do not pertains to 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.Gateway = pointer.String("172.18.41.1")

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.routes'", func() {
				It("inputs invalid destination", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.Routes = append(subnetT.Spec.Routes,
						spiderpoolv1.Route{
							Dst: constant.InvalidCIDR,
							Gw:  "172.18.40.1",
						},
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("inputs invalid gateway", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.Routes = append(subnetT.Spec.Routes,
						spiderpoolv1.Route{
							Dst: "192.168.40.0/24",
							Gw:  constant.InvalidIP,
						},
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("inputs gateway that do not pertains to 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.Routes = append(subnetT.Spec.Routes,
						spiderpoolv1.Route{
							Dst: "192.168.40.0/24",
							Gw:  "172.18.41.1",
						},
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateCreate(ctx, subnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			It("creates IPv4 Subnet with all fields valid", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
				subnetT.Spec.Subnet = "172.18.40.0/24"
				subnetT.Spec.IPs = append(subnetT.Spec.IPs,
					[]string{
						"172.18.40.2-172.18.40.3",
						"172.18.40.10",
					}...,
				)
				subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs, "172.18.40.10")
				subnetT.Spec.Gateway = pointer.String("172.18.40.1")
				subnetT.Spec.Vlan = pointer.Int64(0)
				subnetT.Spec.Routes = append(subnetT.Spec.Routes,
					spiderpoolv1.Route{
						Dst: "192.168.40.0/24",
						Gw:  "172.18.40.40",
					},
				)

				ctx := context.TODO()
				err := subnetWebhook.ValidateCreate(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
			})

			It("creates IPv6 Subnet with all fields valid", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv6)
				subnetT.Spec.Subnet = "abcd:1234::/120"
				subnetT.Spec.IPs = append(subnetT.Spec.IPs,
					[]string{
						"abcd:1234::2-abcd:1234::3",
						"abcd:1234::a",
					}...,
				)
				subnetT.Spec.ExcludeIPs = append(subnetT.Spec.ExcludeIPs, "abcd:1234::a")
				subnetT.Spec.Gateway = pointer.String("abcd:1234::1")
				subnetT.Spec.Vlan = pointer.Int64(0)
				subnetT.Spec.Routes = append(subnetT.Spec.Routes,
					spiderpoolv1.Route{
						Dst: "fd00:40::/120",
						Gw:  "abcd:1234::28",
					},
				)

				ctx := context.TODO()
				err := subnetWebhook.ValidateCreate(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("ValidateUpdate", func() {
			When("Validating 'spec.ipVersion'", func() {
				It("updates 'spec.ipVersion' to nil", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPVersion = nil

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("changes 'spec.ipVersion'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPVersion = pointer.Int64(constant.IPv6)

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("updates IPv4 Subnet but IPv4 is disbale'", func() {
					subnetWebhook.EnableIPv4 = false
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs, "172.18.40.1-172.18.40.2")

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = append(newSubnetT.Spec.IPs, "172.18.40.10")

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("updates IPv6 Subnet but IPv6 is disbale'", func() {
					subnetWebhook.EnableIPv6 = false
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv6)
					subnetT.Spec.Subnet = "adbc:1234::/120"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs, "adbc:1234::1-adbc:1234::2")

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = append(newSubnetT.Spec.IPs, "adbc:1234::a")

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.subnet'", func() {
				It("changes 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.Subnet = "172.18.40.0/25"

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.ips'", func() {
				It("removes all 'spec.ips'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = nil

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("appends invalid IP range to 'spec.ips'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = append(newSubnetT.Spec.IPs, constant.InvalidIPRange)

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("appends IP range that do not pertains to 'spec.subnet' to 'spec.ips'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs, "172.18.40.1-172.18.40.2")

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = append(newSubnetT.Spec.IPs, "172.18.41.10")

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.excludeIPs'", func() {
				It("appends invalid IP range to 'spec.excludeIPs'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.ExcludeIPs = append(newSubnetT.Spec.ExcludeIPs, constant.InvalidIPRange)

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("appends IP range that do not pertains to 'spec.subnet' to 'spec.excludeIPs'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.ExcludeIPs = append(newSubnetT.Spec.ExcludeIPs, "172.18.41.10")

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.gateway'", func() {
				It("updates 'spec.gateway' to invalid gateway", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.Gateway = pointer.String("172.18.40.1")

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.Gateway = pointer.String(constant.InvalidIP)

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("updates 'spec.gateway' to a gateway that do not pertains to 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)
					subnetT.Spec.Gateway = pointer.String("172.18.40.1")

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.Gateway = pointer.String("172.18.41.1")

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating 'spec.routes'", func() {
				It("appends route with invalid destination", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.Routes = append(newSubnetT.Spec.Routes,
						spiderpoolv1.Route{
							Dst: constant.InvalidCIDR,
							Gw:  "172.18.40.1",
						},
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("appends route with invalid gateway", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.Routes = append(newSubnetT.Spec.Routes,
						spiderpoolv1.Route{
							Dst: "192.168.40.0/24",
							Gw:  constant.InvalidIP,
						},
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("appends route whose gateway does not pertains to 'spec.subnet'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.2-172.18.40.3",
							"172.18.40.10",
						}...,
					)

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.Routes = append(newSubnetT.Spec.Routes,
						spiderpoolv1.Route{
							Dst: "192.168.40.0/24",
							Gw:  "172.18.41.1",
						},
					)

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			When("Validating the pre-allocated IP addresses", func() {
				It("failed to assemble total IP addresses due to some unknown errors", func() {
					patches := gomonkey.ApplyFuncReturn(spiderpoolip.AssembleTotalIPs, nil, constant.ErrUnknown)
					defer patches.Reset()

					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					subnetT.Status.ControlledIPPools = spiderpoolv1.PoolIPPreAllocations{
						"pool": spiderpoolv1.PoolIPPreAllocation{
							IPs: []string{
								"172.18.40.10",
							},
						},
					}

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = newSubnetT.Spec.IPs[:1]

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("has invalid 'status.controlledIPPools'", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					subnetT.Status.ControlledIPPools = spiderpoolv1.PoolIPPreAllocations{
						"pool": spiderpoolv1.PoolIPPreAllocation{IPs: constant.InvalidIPRanges},
					}

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = newSubnetT.Spec.IPs[:1]

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})

				It("removes IP range that is being used by IPPool", func() {
					subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
					subnetT.Spec.Subnet = "172.18.40.0/24"
					subnetT.Spec.IPs = append(subnetT.Spec.IPs,
						[]string{
							"172.18.40.1-172.18.40.2",
							"172.18.40.10",
						}...,
					)

					subnetT.Status.ControlledIPPools = spiderpoolv1.PoolIPPreAllocations{
						"pool": spiderpoolv1.PoolIPPreAllocation{
							IPs: []string{
								"172.18.40.10",
							},
						},
					}

					newSubnetT := subnetT.DeepCopy()
					newSubnetT.Spec.IPs = newSubnetT.Spec.IPs[:1]

					ctx := context.TODO()
					err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
					Expect(apierrors.IsInvalid(err)).To(BeTrue())
				})
			})

			It("deletes Subnet", func() {
				controllerutil.AddFinalizer(subnetT, constant.SpiderFinalizer)
				now := metav1.Now()
				subnetT.SetDeletionTimestamp(&now)
				subnetT.SetDeletionGracePeriodSeconds(pointer.Int64(0))

				newSubnetT := subnetT.DeepCopy()
				controllerutil.RemoveFinalizer(newSubnetT, constant.SpiderFinalizer)

				ctx := context.TODO()
				err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
				Expect(err).NotTo(HaveOccurred())
			})

			It("updates terminating Subnet", func() {
				controllerutil.AddFinalizer(subnetT, constant.SpiderFinalizer)
				now := metav1.Now()
				subnetT.SetDeletionTimestamp(&now)
				subnetT.SetDeletionGracePeriodSeconds(pointer.Int64(30))

				newSubnetT := subnetT.DeepCopy()

				ctx := context.TODO()
				err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
				Expect(apierrors.IsForbidden(err)).To(BeTrue())
			})

			It("updates IPv4 Subnet with all fields valid", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv4)
				subnetT.Spec.Subnet = "172.18.40.0/24"
				subnetT.Spec.IPs = append(subnetT.Spec.IPs, "172.18.40.2-172.18.40.3")
				subnetT.Spec.Vlan = pointer.Int64(0)

				newSubnetT := subnetT.DeepCopy()
				newSubnetT.Spec.IPs = append(newSubnetT.Spec.IPs, "172.18.40.10")
				newSubnetT.Spec.ExcludeIPs = append(newSubnetT.Spec.ExcludeIPs, "172.18.40.10")
				newSubnetT.Spec.Gateway = pointer.String("172.18.40.1")
				newSubnetT.Spec.Routes = append(newSubnetT.Spec.Routes,
					spiderpoolv1.Route{
						Dst: "192.168.40.0/24",
						Gw:  "172.18.40.40",
					},
				)

				ctx := context.TODO()
				err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
				Expect(err).NotTo(HaveOccurred())
			})

			It("updates IPv6 Subnet with all fields valid", func() {
				subnetT.Spec.IPVersion = pointer.Int64(constant.IPv6)
				subnetT.Spec.Subnet = "abcd:1234::/120"
				subnetT.Spec.IPs = append(subnetT.Spec.IPs, "abcd:1234::2-abcd:1234::3")
				subnetT.Spec.Vlan = pointer.Int64(0)

				newSubnetT := subnetT.DeepCopy()
				newSubnetT.Spec.IPs = append(newSubnetT.Spec.IPs, "abcd:1234::a")
				newSubnetT.Spec.ExcludeIPs = append(newSubnetT.Spec.ExcludeIPs, "abcd:1234::a")
				newSubnetT.Spec.Gateway = pointer.String("abcd:1234::1")
				newSubnetT.Spec.Routes = append(newSubnetT.Spec.Routes,
					spiderpoolv1.Route{
						Dst: "fd00:40::/120",
						Gw:  "abcd:1234::28",
					},
				)

				ctx := context.TODO()
				err := subnetWebhook.ValidateUpdate(ctx, subnetT, newSubnetT)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("ValidateDelete", func() {
			It("passes", func() {
				ctx := context.TODO()
				err := subnetWebhook.ValidateDelete(ctx, subnetT)
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})
})