package crd

import (
	"context"
	"github.com/go-logr/logr"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/config"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/pkg/proxy"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/pkg/utils"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "crd-controller"
)

type Controller struct {
	Config *config.Config
	Client client.Client
	Proxy  *proxy.Proxy
	Log    logr.Logger
}

func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := c.Log.WithValues("name", req.String())

	var crdObj = new(apiextv1.CustomResourceDefinition)
	if err := c.Client.Get(ctx, req.NamespacedName, crdObj); err != nil {
		// crd deleted, do nothing.
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		log.Error(err, "unable to get CustomResourceDefinition")
		return reconcile.Result{}, err
	}

	service := crdObj.Spec.Conversion.Webhook.ClientConfig.Service
	log = log.WithValues("service", types.NamespacedName{Name: service.Name, Namespace: service.Namespace})

	webhookServiceRef := &admissionv1.ServiceReference{
		Namespace: service.Namespace,
		Name:      service.Name,
		Port:      ptr.To(utils.DefaultWebhookPort),
	}

	if service.Port != nil {
		webhookServiceRef.Port = service.Port
	}

	// Create/update nodePort service for proxy.
	serviceProxy, err := c.Proxy.EnsureServiceProxy(
		ctx,
		webhookServiceRef,
	)
	if err != nil {
		log.Error(err, "unable to create proxy")
		return reconcile.Result{}, err
	}

	// Create/update endpoint slice with nodePort endpoints.
	if err := c.Proxy.EnsureProxyEndpointSlices(ctx, serviceProxy); err != nil {
		log.Error(err, "unable to create proxy endpoint slices")
		return reconcile.Result{}, err
	}

	if err := c.Proxy.UnbindPodEndpoints(ctx, webhookServiceRef); err != nil {
		log.Error(err, "unable to unbind Pod Endpoints from webhook service")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Controller) SetupWithManager(mgr ctrl.Manager) error {

	// Contains conversion webhook proxy.
	predicateCRD := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		crd := obj.(*apiextv1.CustomResourceDefinition)
		return crd.Spec.Conversion != nil &&
			crd.Spec.Conversion.Webhook != nil &&
			crd.Spec.Conversion.Webhook.ClientConfig != nil &&
			crd.Spec.Conversion.Webhook.ClientConfig.Service != nil
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		Watches(
			&apiextv1.CustomResourceDefinition{},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(predicateCRD),
		).
		Complete(r)
}
