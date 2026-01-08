package proxy

import (
	"context"
	"errors"
	"fmt"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/pkg/utils"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	ErrEndpointSliceNotFound = errors.New("endpoint slice not found")
)

// EnsureProxyEndpointSlices creates endpoint slice for proxy service.
// Using <node>:<node-port> as endpoints, to handle webhook traffic from EKS control-plane.
// TODO: endpoint slice limited to 100 endpoints.
// TODO: Need wrap this function to split endpoints into portions (few endpoint slice, united by service-name label).
func (p *Proxy) EnsureProxyEndpointSlices(ctx context.Context, proxyService *v1.Service) error {
	proxyServiceKey := types.NamespacedName{
		Namespace: proxyService.Namespace,
		Name:      proxyService.Name,
	}
	log := p.log.WithValues("service", proxyServiceKey)

	webhookServiceName, ok := proxyService.Labels[utils.LabelServiceProxyOf]
	if !ok {
		return fmt.Errorf("proxy service %s does not have the label %s",
			proxyServiceKey.String(),
			utils.LabelServiceProxyOf,
		)
	}

	proxyEndpointSliceKey := types.NamespacedName{
		Namespace: proxyService.Namespace,
		Name:      getProxyName(proxyService.Name, serviceNameHashLen),
	}

	// Get webhook service endpoints.
	endpointSlices, err := p.getEndpointSlices(
		ctx,
		proxyServiceKey,
	)
	if err != nil {
		log.Error(err, "failed to get endpoint slice")
		return err
	}

	// We need to collect service endpoints from slices.
	var webhookEndpoints []discoveryv1.Endpoint
	for _, endpointSlice := range endpointSlices {
		webhookEndpoints = append(webhookEndpoints, endpointSlice.Endpoints...)
	}

	proxyEndpointSliceObj := p.generateProxyEndpointSlice(
		log,
		proxyEndpointSliceKey.Name,
		webhookEndpoints,
		proxyService,
	)
	proxyEndpointSlice := proxyEndpointSliceObj.DeepCopy()

	op, err := controllerutil.CreateOrUpdate(ctx, p.client,
		proxyEndpointSliceObj,
		func() error {
			proxyEndpointSliceObj.Labels = map[string]string{
				utils.LabelEndpointSliceServiceName: webhookServiceName,
				utils.LabelEdpointSliceManagedBy:    utils.ControllerName,
			}
			proxyEndpointSliceObj.Ports = proxyEndpointSlice.Ports
			proxyEndpointSliceObj.Endpoints = proxyEndpointSlice.Endpoints

			return controllerutil.SetControllerReference(proxyService, proxyEndpointSliceObj, p.client.Scheme())
		},
	)
	if err != nil {
		return client.IgnoreAlreadyExists(err)
	}

	log.Info("ensured proxy endpoint slice", "name", proxyEndpointSliceKey, "op", op)
	return nil
}

func (p *Proxy) generateProxyEndpointSlice(log logr.Logger, name string, webhookEndpoints []discoveryv1.Endpoint, proxyService *v1.Service) *discoveryv1.EndpointSlice {
	proxyEndpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: proxyService.Namespace,
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}

	// Add NodePorts to proxy endpoint slice.
	for i := range proxyService.Spec.Ports {
		port := proxyService.Spec.Ports[i]

		proxyEndpointSlice.Ports = append(proxyEndpointSlice.Ports,
			discoveryv1.EndpointPort{
				Name:     ptr.To(port.Name),
				Port:     ptr.To(port.NodePort),
				Protocol: &port.Protocol,
			},
		)
	}

	// Add pod's node ipaddress to endpoints.
	for _, webhookEndpoint := range webhookEndpoints {
		if webhookEndpoint.NodeName == nil {
			log.V(5).Info("skipping webhook endpoint, nodeName is nil", "endpoint", webhookEndpoint.String())
			continue
		}

		nodeIPAddress, found := p.nodeCache.Get(*webhookEndpoint.NodeName)
		if !found {
			log.V(5).Info("skipping endpoint node, no ipaddress found", "endpoint", webhookEndpoint.String(), "node", *webhookEndpoint.NodeName)
			continue
		}

		proxyEndpointSlice.Endpoints = append(proxyEndpointSlice.Endpoints,
			discoveryv1.Endpoint{
				Addresses:  []string{nodeIPAddress},
				Conditions: webhookEndpoint.Conditions,
			})
	}

	return proxyEndpointSlice
}

// getEndpointSlices returns endpoint slice with real service endpoints.
func (p *Proxy) getEndpointSlices(ctx context.Context, serviceName types.NamespacedName) ([]discoveryv1.EndpointSlice, error) {
	var endpointSlices = new(discoveryv1.EndpointSliceList)

	if err := p.client.List(ctx, endpointSlices,
		client.InNamespace(serviceName.Namespace),
		client.MatchingLabels{
			utils.LabelEndpointSliceServiceName: serviceName.Name,
			utils.LabelEdpointSliceManagedBy:    utils.LabelKeyEndpointSliceController,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to list endpoint slices for service: %s, err: %w",
			serviceName.String(), err,
		)
	}

	return endpointSlices.Items, nil
}
