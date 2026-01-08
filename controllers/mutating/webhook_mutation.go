package mutating

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/config"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/pkg/proxy"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/pkg/utils"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/utils/ptr"

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
	ControllerName = "mutation-controller"
)

type Controller struct {
	Config *config.Config
	Client client.Client
	Proxy  *proxy.Proxy
	Log    logr.Logger
}

func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := c.Log.WithValues("name", req.String())

	var webhookServiceMap = make(map[*admissionv1.ServiceReference]struct{})

	webhookObj := new(admissionv1.MutatingWebhookConfiguration)
	if err := c.Client.Get(ctx, types.NamespacedName{Name: req.Name}, webhookObj); err != nil {
		// webhook deleted, do nothing.
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		c.Log.Error(err, "unable to fetch mutation webhook")
		return reconcile.Result{}, err
	}

	// Need to create only one nodeProxy service per webhook.
	for _, webhook := range webhookObj.Webhooks {
		if webhook.ClientConfig.Service == nil {
			// Probably webhook url is used, no need for proxy.
			continue
		}

		serviceRef := webhook.ClientConfig.Service
		if serviceRef.Port == nil {
			serviceRef.Port = ptr.To(utils.DefaultWebhookPort)
		}
		webhookServiceMap[serviceRef] = struct{}{}
	}

	for webhookServiceRef := range webhookServiceMap {
		serviceKey := types.NamespacedName{Name: webhookServiceRef.Name, Namespace: webhookServiceRef.Namespace}
		log := log.WithValues("service", serviceKey)

		serviceProxy, err := c.Proxy.EnsureServiceProxy(ctx, webhookServiceRef)
		if err != nil {
			if errors.Is(err, proxy.ErrServiceNotFound) {
				log.V(5).Info("webhook service not found, skipping")
				continue
			}
			log.Error(err, "unable to create Proxy")
			return reconcile.Result{}, err
		}
		if err := c.Proxy.EnsureProxyEndpointSlices(ctx, serviceProxy); err != nil {
			log.Error(err, "unable to create proxy EndpointSlices")
			return reconcile.Result{}, err
		}

		if err := c.Proxy.UnbindPodEndpoints(ctx, webhookServiceRef); err != nil {
			log.Error(err, "unable to unbind Pod Endpoints from webhook service")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Controller) SetupWithManager(mgr ctrl.Manager) error {

	// Contains webhook service.
	predicateMutating := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		webhook := obj.(*admissionv1.MutatingWebhookConfiguration)

		if len(webhook.Webhooks) == 0 {
			return false
		}

		for _, mutation := range webhook.Webhooks {
			if mutation.ClientConfig.Service != nil {
				return true
			}
		}
		return false
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		Watches(
			&admissionv1.MutatingWebhookConfiguration{},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(predicateMutating),
		).
		Complete(r)
}
