package utils

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	LabelEdpointSliceManagedBy         = "endpointslice.kubernetes.io/managed-by"
	LabelManagedBy                     = "proxy.kubernetes.io/managed-by"
	LabelPartOf                        = "app.kubernetes.io/part-of"
	LabelAppInstance                   = "app.kubernetes.io/instance"
	LabelServiceProxyOf                = "service.infra.io/proxy-of"
	LabelWebhookPort                   = "service.infra.io/webhook-port"
	LabelServiceProxyIgnoreRestriction = "service.infra.io/proxy-ignore-restriction"
	LabelEndpointSliceServiceName      = "kubernetes.io/service-name"

	LabelKeyEndpointSliceController = "endpointslice-controller.k8s.io"
	ControllerName                  = "eks-webhook-proxy"

	DefaultWebhookPort int32 = 443
)

type WebhookService struct {
	Name types.NamespacedName
	Port int32
}

func IsTargetPortSet(targetPort intstr.IntOrString) bool {
	return targetPort.Type != intstr.Int || targetPort.IntVal != 0
}
