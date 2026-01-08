package endpointslice

import (
	"context"
	"errors"
	"github.com/go-logr/logr"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/config"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/pkg/proxy"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/pkg/utils"
	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "endpointslice-controller"
)

type Controller struct {
	Config *config.Config
	Client client.Client
	Proxy  *proxy.Proxy
	Log    logr.Logger
}

var (
	ErrServiceNotFound = errors.New("service not found")
)

func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := c.Log.WithValues("name", req.String())

	var endpointSlice = new(discoveryv1.EndpointSlice)
	if err := c.Client.Get(ctx, req.NamespacedName, endpointSlice); err != nil {
		// endpointslice deleted, do nothing.
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		log.Error(err, "unable to get EndpointSlice")
		return reconcile.Result{}, err
	}

	proxyServices, err := c.getWebhookProxyServices(ctx, req.NamespacedName)
	if err != nil {
		if errors.Is(err, ErrServiceNotFound) {
			log.V(5).Info("EndpointSlice is not part of a webhook. Skipping.")
			return reconcile.Result{}, nil
		}
		log.Error(err, "unable to get WebhookProxyServices")
		return reconcile.Result{}, err
	}

	// Rebuild endpoints for proxy services.
	for _, proxyService := range proxyServices.Items {
		err := c.Proxy.EnsureProxyEndpointSlices(ctx, &proxyService)
		if err != nil {
			log.Error(err, "unable to update proxy endpoint slices")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	predicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		endpointSlice := obj.(*discoveryv1.EndpointSlice)

		if val, ok := endpointSlice.Labels[utils.LabelManagedBy]; ok {
			if val == utils.ControllerName {
				return false
			}
		}
		return true
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		Watches(
			&discoveryv1.EndpointSlice{},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(predicate),
		).
		Complete(c)
}

// getWebhookProxyServices will list all webhook proxy related to endpointSlice (if exists).
func (c *Controller) getWebhookProxyServices(ctx context.Context, key types.NamespacedName) (*v1.ServiceList, error) {
	var serviceList = new(v1.ServiceList)

	if err := c.Client.List(ctx, serviceList,
		client.InNamespace(key.Namespace),
		client.MatchingLabels(
			map[string]string{
				utils.LabelManagedBy:                utils.ControllerName,
				utils.LabelEndpointSliceServiceName: key.Name,
			},
		),
	); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrServiceNotFound
		}
		return nil, err
	}

	return serviceList, nil
}
