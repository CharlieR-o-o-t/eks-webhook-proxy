package proxy

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/pkg/utils"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	serviceNameHashLen = 8
)

var (
	ErrServiceHasNoPort = errors.New("service has no ports defined in webhook")
	// ErrServiceNotFound this error should not be reconciled.
	// in case service specified in webhook is missing, it will be just skipped by kube-api.
	ErrServiceNotFound = errors.New("service not found")
)

// EnsureServiceProxy takes a ClusterIP Service and creates (or ensures the existence of)
// a corresponding Service of type NodePort.
// This allows publishing the webhook Service to the routable machine network
// rather than the pod CIDR.
func (p *Proxy) EnsureServiceProxy(ctx context.Context, serviceRef *admissionv1.ServiceReference) (*v1.Service, error) {
	serviceNetRestriction := p.config.Proxy.Restricted
	serviceKey := types.NamespacedName{Namespace: serviceRef.Namespace, Name: serviceRef.Name}

	log := p.log.WithValues("service", serviceKey)

	var serviceOrigin = new(v1.Service)
	if err := p.client.Get(ctx, serviceKey, serviceOrigin); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrServiceNotFound
		}
		log.Error(err, "failed to fetch service")
		return nil, err
	}

	if serviceOrigin.Spec.Type != v1.ServiceTypeClusterIP {
		log.V(5).Info("service is not ClusterIP, should for without proxy, skipping")
		return nil, nil
	}

	if val, ok := serviceOrigin.Labels[utils.LabelServiceProxyIgnoreRestriction]; ok {
		serviceNetRestriction = val != "true"
		log = log.WithValues("restricted", val)
	}

	serviceProxy, err := p.ensureProxyService(ctx, serviceOrigin, serviceNetRestriction, log)
	if err != nil {
		log.Error(err, "failed to ensure proxy service")
		return nil, err
	}

	if serviceNetRestriction {
		if err := p.ensureNetworkPolicy(ctx, serviceOrigin, log); err != nil {
			log.Error(err, "failed to ensure network policy for service")
		}
	}

	return serviceProxy, nil
}

func (p *Proxy) ensureProxyService(ctx context.Context, serviceOrigin *v1.Service, netRestriction bool, logger logr.Logger) (*v1.Service, error) {

	serviceProxyObj := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getProxyName(serviceOrigin.Name, serviceNameHashLen),
			Namespace: serviceOrigin.Namespace,
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeNodePort,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, p.client,
		serviceProxyObj,
		func() error {
			serviceProxyObj.Labels = map[string]string{
				utils.LabelManagedBy:      utils.ControllerName,
				utils.LabelServiceProxyOf: serviceOrigin.Name,
			}

			// All ports from origin service will be proxied with nodePort service.
			serviceProxyObj.Spec.Ports = serviceOrigin.Spec.Ports

			if serviceOrigin.Spec.Selector != nil {
				serviceProxyObj.Spec.Selector = serviceOrigin.Spec.Selector
			}

			// Publish nodePort only on nodes webhook pod are running.
			if netRestriction {
				serviceProxyObj.Spec.ExternalTrafficPolicy = v1.ServiceExternalTrafficPolicyTypeLocal
			} else {
				serviceProxyObj.Spec.ExternalTrafficPolicy = v1.ServiceExternalTrafficPolicyTypeCluster
			}

			if instance, ok := serviceOrigin.Labels[utils.LabelAppInstance]; ok {
				serviceProxyObj.Labels[utils.LabelPartOf] = instance
			}

			return controllerutil.SetControllerReference(serviceOrigin, serviceProxyObj, p.client.Scheme())
		},
	)
	if err != nil {
		logger.Error(err, "failed to ensure proxy service")
		return nil, err
	}

	logger.V(4).Info("proxy service has been ensured", "operation", op)
	return serviceProxyObj, nil
}

// UnbindPodEndpoints will unbind pod endpoints from webhook service.
// after that only nodePort proxy will handle service traffic.
func (p *Proxy) UnbindPodEndpoints(ctx context.Context, serviceRef *admissionv1.ServiceReference) error {
	var serviceOrigin = new(v1.Service)
	if err := p.client.Get(ctx, types.NamespacedName{Namespace: serviceRef.Namespace, Name: serviceRef.Name}, serviceOrigin); err != nil {
		return client.IgnoreNotFound(err)
	}

	if err := p.removeSelector(ctx, serviceOrigin); err != nil {
		return fmt.Errorf("unable to remove selector from service, %w", err)
	}

	if err := p.cleanPodEndpoints(ctx, serviceOrigin); err != nil {
		return fmt.Errorf("unable to clean up service endpoints, %w", err)
	}

	if err := p.cleanPodEndpoints(ctx, serviceOrigin); err != nil {
		return fmt.Errorf("unable to clean up endpointslices, %w", err)
	}
	return nil
}

func (p *Proxy) removeSelector(ctx context.Context, serviceOrigin *v1.Service) error {
	serviceOrigin.Spec.Selector = nil
	return p.client.Update(ctx, serviceOrigin)
}

func (p *Proxy) cleanPodEndpointSlices(ctx context.Context, serviceOrigin *v1.Service) error {
	endpointSlices, err := p.getEndpointSlices(ctx, types.NamespacedName{Namespace: serviceOrigin.Namespace, Name: serviceOrigin.Name})
	if err != nil {
		return err
	}

	for i := range endpointSlices {
		if err := p.client.Delete(ctx, &endpointSlices[i]); err != nil {
			return client.IgnoreNotFound(err)
		}
	}

	return nil
}

func (p *Proxy) cleanPodEndpoints(ctx context.Context, serviceOrigin *v1.Service) error {
	var endpoints = new(v1.Endpoints)
	if err := p.client.Get(ctx, types.NamespacedName{Namespace: serviceOrigin.Namespace, Name: serviceOrigin.Name}, endpoints); err != nil {
		return client.IgnoreNotFound(err)
	}

	if err := p.client.Delete(ctx, endpoints); err != nil {
		return err
	}

	return nil
}

func (p *Proxy) ensureNetworkPolicy(ctx context.Context, serviceOrigin *v1.Service, logger logr.Logger) error {
	networPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getProxyName(serviceOrigin.Name, serviceNameHashLen),
			Namespace: serviceOrigin.Namespace,
		},
	}

	// Ingress rules из CIDR
	from := make([]networkingv1.NetworkPolicyPeer, 0, len(p.config.Proxy.AllowedSrcCIDRs))
	for _, cidr := range p.config.Proxy.AllowedSrcCIDRs {
		from = append(from, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{
				CIDR: cidr,
			},
		})
	}

	op, err := controllerutil.CreateOrUpdate(ctx, p.client,
		networPolicy,
		func() error {
			networPolicy.Labels = map[string]string{
				utils.LabelManagedBy:      utils.ControllerName,
				utils.LabelServiceProxyOf: serviceOrigin.Name,
			}

			networPolicy.Spec.PodSelector.MatchLabels = serviceOrigin.Spec.Selector

			if instance, ok := serviceOrigin.Labels[utils.LabelAppInstance]; ok {
				networPolicy.Labels[utils.LabelPartOf] = instance
			}

			networkPolicyIngressRule := networkingv1.NetworkPolicyIngressRule{
				From: from,
			}

			// Collecting ports from original service.
			ports := make([]networkingv1.NetworkPolicyPort, 0, len(serviceOrigin.Spec.Ports))
			for _, servicePort := range serviceOrigin.Spec.Ports {
				ingressProtocol := servicePort.Protocol

				ingressPort := networkingv1.NetworkPolicyPort{
					Protocol: &ingressProtocol,
				}

				targetPort := servicePort.TargetPort
				if !utils.IsTargetPortSet(targetPort) {
					targetPort = intstr.FromInt32(servicePort.Port)
				}
				ingressPort.Port = &targetPort

				ports = append(ports, ingressPort)
			}
			networkPolicyIngressRule.Ports = ports

			networPolicy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{networkPolicyIngressRule}

			return controllerutil.SetControllerReference(serviceOrigin, networPolicy, p.client.Scheme())
		},
	)
	if err != nil {
		logger.Error(err, "failed to ensure proxy service")
		return err
	}

	logger.V(4).Info("network policy has been ensured", "operation", op)
	return nil
}
