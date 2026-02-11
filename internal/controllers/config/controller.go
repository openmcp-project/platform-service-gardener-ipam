package config

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	"github.com/openmcp-project/controller-utils/pkg/logging"
	openmcpconst "github.com/openmcp-project/openmcp-operator/api/constants"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

const (
	ControllerName = "IPAMConfig"

	EventActionReconcile = "Reconcile"
)

type IPAMConfigController struct {
	PlatformCluster *clusters.Cluster
	ProviderName    string
	er              events.EventRecorder
}

var _ reconcile.Reconciler = &IPAMConfigController{}

func NewIPAMConfigController(platformCluster *clusters.Cluster, providerName string, er events.EventRecorder) *IPAMConfigController {
	return &IPAMConfigController{
		PlatformCluster: platformCluster,
		ProviderName:    providerName,
		er:              er,
	}
}

func (c *IPAMConfigController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := logging.FromContextOrPanic(ctx).WithName(ControllerName)
	ctx = logging.NewContext(ctx, log)
	log.Info("Starting reconcile")

	cfg, err := c.reconcile(ctx, req)
	if c.er != nil {
		if err != nil {
			if cfg != nil {
				c.er.Eventf(cfg, nil, corev1.EventTypeWarning, "ReconcileError", EventActionReconcile, "Reconcile failed: %v", err)
			}
		} else {
			if cfg != nil {
				c.er.Eventf(cfg, nil, corev1.EventTypeNormal, "ReconcileSuccess", EventActionReconcile, "Reconcile successful")
			}
		}
	}

	return reconcile.Result{}, err
}

func (c *IPAMConfigController) reconcile(ctx context.Context, req reconcile.Request) (*ipamv1alpha1.IPAMConfig, error) {
	log := logging.FromContextOrPanic(ctx)

	if req.Name != c.ProviderName {
		log.Info("Skipping reconciliation because the config belongs to a different instance of this platform service", "providerName", c.ProviderName)
		return nil, nil
	}

	// fetch config
	cfg := &ipamv1alpha1.IPAMConfig{}
	if err := c.PlatformCluster.Client().Get(ctx, req.NamespacedName, cfg); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Config resource not found, removing config from internal storage")
			shared.SetConfig(nil)
		}
		return nil, fmt.Errorf("error fetching GardenerIPAMConfig %s: %w", req.String(), err)
	}

	// handle operation annotation
	if cfg.GetAnnotations() != nil {
		op, ok := cfg.GetAnnotations()[openmcpconst.OperationAnnotation]
		if ok {
			switch op {
			case openmcpconst.OperationAnnotationValueIgnore:
				log.Info("Ignoring resource due to ignore operation annotation")
				return nil, nil
			case openmcpconst.OperationAnnotationValueReconcile:
				log.Debug("Removing reconcile operation annotation from resource")
				if err := ctrlutils.EnsureAnnotation(ctx, c.PlatformCluster.Client(), cfg, openmcpconst.OperationAnnotation, "", true, ctrlutils.DELETE); err != nil {
					return nil, fmt.Errorf("error removing operation annotation: %w", err)
				}
			}
		}
	}

	if cfg.DeletionTimestamp.IsZero() {
		// validate config
		log.Debug("Validating config")
		var errs error
		for i, rule := range cfg.Spec.InjectionRules {
			if err := rule.Validate(); err != nil {
				errs = errors.Join(errs, fmt.Errorf("injection rule at index %d is invalid: %w", i, err))
			}
		}
		if errs != nil {
			return nil, fmt.Errorf("config is invalid: %w", errs)
		}

		// compare to old config
		// if the injection rules have changed, we need to queue all clusters that might be affected
		oldCfg := shared.GetConfig()
		if oldCfg == nil || !reflect.DeepEqual(oldCfg.Spec.InjectionRules, cfg.Spec.InjectionRules) {
			log.Info("Injection rules have changed, queuing all clusters that match any injection rule")
			affectedClusters, err := shared.FetchRelevantClusters(ctx, cfg, c.PlatformCluster)
			if err != nil {
				return nil, fmt.Errorf("error fetching affected clusters: %w", err)
			}
			for _, cluster := range affectedClusters {
				shared.ClusterWatch <- event.GenericEvent{
					Object: cluster,
				}
			}
		} else {
			log.Debug("Injection rules have not changed, no need to queue any clusters")
		}

		// update config in internal storage
		log.Debug("Updating config in internal storage")
		shared.SetConfig(cfg)
	} else {
		// remove config from internal storage
		log.Debug("Removing config from internal storage because object is in deletion")
		shared.SetConfig(nil)
	}

	log.Info("Config refreshed")
	return cfg, nil
}

// SetupWithManager sets up the controller with the Manager.
func (c *IPAMConfigController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// watch GardenerIPAMConfig resources
		For(&ipamv1alpha1.IPAMConfig{}, builder.WithPredicates(predicate.And(
			ctrlutils.ExactNamePredicate(c.ProviderName, ""),
			predicate.Or(
				predicate.GenerationChangedPredicate{},
				ctrlutils.DeletionTimestampChangedPredicate{},
				ctrlutils.GotAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueReconcile),
				ctrlutils.LostAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueIgnore),
			),
			predicate.Not(
				ctrlutils.HasAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueIgnore),
			),
		))).
		Complete(c)
}
