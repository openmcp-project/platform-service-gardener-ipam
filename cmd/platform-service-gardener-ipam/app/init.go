package app

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	crdutil "github.com/openmcp-project/controller-utils/pkg/crds"
	"github.com/openmcp-project/controller-utils/pkg/init/webhooks"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	openmcpconst "github.com/openmcp-project/openmcp-operator/api/constants"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"github.com/openmcp-project/platform-service-gardener-ipam/api/crds"
	providerscheme "github.com/openmcp-project/platform-service-gardener-ipam/api/install"
	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
)

const (
	// WebhookPortPod is the port the webhook server listens on in the pod.
	WebhookPortPod = 9443
	// WebhookPortSvc is the port the webhook service exposes.
	WebhookPortSvc = 443
)

func NewInitCommand(so *SharedOptions) *cobra.Command {
	opts := &InitOptions{
		SharedOptions: so,
	}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Platform Service Gardener-IPAM",
		Run: func(cmd *cobra.Command, args []string) {
			opts.PrintRawOptions(cmd)
			if err := opts.Complete(cmd.Context()); err != nil {
				panic(fmt.Errorf("error completing options: %w", err))
			}
			opts.PrintCompletedOptions(cmd)
			if opts.DryRun {
				cmd.Println("=== END OF DRY RUN ===")
				return
			}
			if err := opts.Run(cmd.Context()); err != nil {
				panic(err)
			}
		},
	}
	opts.AddFlags(cmd)

	return cmd
}

type InitOptions struct {
	*SharedOptions
}

func (o *InitOptions) AddFlags(cmd *cobra.Command) {}

func (o *InitOptions) Complete(ctx context.Context) error {
	if err := o.SharedOptions.Complete(); err != nil {
		return err
	}

	return nil
}

func (o *InitOptions) Run(ctx context.Context) error {
	if err := o.PlatformCluster.InitializeClient(providerscheme.InstallOperatorAPIsPlatform(providerscheme.InstallCRDAPIs(runtime.NewScheme()))); err != nil {
		return err
	}

	log := o.Log.WithName("main")
	log.Info("Environment", "value", o.Environment)
	log.Info("ProviderName", "value", o.ProviderName)

	// apply CRDs
	crdManager := crdutil.NewCRDManager(openmcpconst.ClusterLabel, crds.CRDs)
	crdManager.AddCRDLabelToClusterMapping(clustersv1alpha1.PURPOSE_PLATFORM, o.PlatformCluster)
	if err := crdManager.CreateOrUpdateCRDs(ctx, &log); err != nil {
		return fmt.Errorf("error creating/updating CRDs: %w", err)
	}

	// deploy webhooks, unless disabled
	providerSystemNamespace := os.Getenv(openmcpconst.EnvVariablePodNamespace)
	if providerSystemNamespace == "" {
		return fmt.Errorf("environment variable %s is not set", openmcpconst.EnvVariablePodNamespace)
	}

	suffix := "-webhook"
	whServiceName := ctrlutils.ShortenToXCharactersUnsafe(o.ProviderName, ctrlutils.K8sMaxNameLength-len(suffix)) + suffix
	whSecretName, err := libutils.WebhookSecretName(o.ProviderName)
	if err != nil {
		return fmt.Errorf("unable to determine webhook secret name: %w", err)
	}

	installOpts := []webhooks.InstallOption{
		webhooks.WithWebhookService{Name: whServiceName, Namespace: providerSystemNamespace},
		webhooks.WithWebhookSecret{Name: whSecretName, Namespace: providerSystemNamespace},
		webhooks.WithWebhookServicePort(WebhookPortSvc),
		webhooks.WithManagedWebhookService{
			TargetPort: intstr.FromInt32(WebhookPortPod),
			SelectorLabels: map[string]string{
				"app.kubernetes.io/component":  "controller",
				"app.kubernetes.io/managed-by": "openmcp-operator",
				"app.kubernetes.io/name":       "PlatformService",
				"app.kubernetes.io/instance":   o.ProviderName,
			},
		},
	}
	certOpts := []webhooks.CertOption{
		webhooks.WithWebhookService{Name: whServiceName, Namespace: providerSystemNamespace},
		webhooks.WithWebhookSecret{Name: whSecretName, Namespace: providerSystemNamespace},
	}

	webhookTypes := []webhooks.APITypes{
		{
			Obj:       &ipamv1alpha1.IPAMConfig{},
			Validator: true,
			Defaulter: false,
			Mutation: webhooks.Mutation{
				ValidatingWebhook: func(webhook *admissionregistrationv1.ValidatingWebhook) error {
					// We need to set the config validation webhook to 'ignore', because the manager can only be started when a config is present,
					// but the landscape operator would be unable to create that config, because the ValidatingWebhookConfiguration would reject the request
					// because the webhook server is not available, for which we need to start the manager first.
					webhook.FailurePolicy = ptr.To(admissionregistrationv1.Ignore)
					return nil
				},
			},
		},
		{
			Obj:       &clustersv1alpha1.Cluster{},
			Validator: false,
			Defaulter: true,
		},
	}

	if !o.WebhooksDisabled {
		log.Info("Setting up webhooks")

		// Generate webhook certificate
		if err := webhooks.GenerateCertificate(ctx, o.PlatformCluster.Client(), certOpts...); err != nil {
			return fmt.Errorf("unable to generate webhook certificate: %w", err)
		}

		// Install webhooks
		err = webhooks.Install(
			ctx,
			o.PlatformCluster.Client(),
			o.PlatformCluster.Scheme(),
			webhookTypes,
			installOpts...,
		)
		if err != nil {
			return fmt.Errorf("unable to install webhooks: %w", err)
		}
	} else {
		log.Info("Webhooks are disabled, removing potentially existing webhook resources")

		// Uninstall webhooks
		err = webhooks.Uninstall(
			ctx,
			o.PlatformCluster.Client(),
			o.PlatformCluster.Scheme(),
			webhookTypes,
			installOpts...,
		)
		if err != nil {
			return fmt.Errorf("unable to uninstall webhooks: %w", err)
		}
	}

	log.Info("Finished init command")
	return nil
}
