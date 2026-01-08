# EKS Webhook Proxy Controller

This controller addresses a well-known network reachability limitation in Amazon EKS clusters that use **non-VPC CNI plugins** (such as Cilium or Calico in non-VPC / overlay modes).

In these setups, the EKS control plane cannot reliably route traffic to **Pod IP addresses**, because those IPs are not part of the VPC’s routable address space. As a result, **admission webhooks and CRD conversion webhooks become unreachable**, leading to timeouts and failed API operations.

This controller provides a **transparent and minimally invasive solution** that restores webhook connectivity **without requiring hostNetwork, VPC CNI, or changes to webhook definitions**.

---

## Architecture Overview

The solution introduces a lightweight **NodeIP-based bridge** that makes webhook backends reachable from the EKS control plane while preserving standard Kubernetes abstractions.

At a high level, the controller rewires webhook service backends to use **node Internal IPs and NodePorts**, which are VPC-routable and reachable by the control plane.

> **Before:**  
> EKS → Service → ❌ Pod IP (non-VPC, unreachable)
>
> **After:**  
> EKS → Service → ✅ Node Internal IP + NodePort (VPC-routable) → Pod IP

### Key Design Properties

- ✅ **No changes to webhook manifests**
- ✅ **No requirement to run webhooks on `hostNetwork`**
- ✅ **No dependency on VPC CNI**
- ✅ **Transparent to the EKS control plane**
- ✅ **Works with existing ClusterIP-based webhook configurations**

---

## How It Works

### 1. Discovery

The controller continuously monitors:
- `MutatingWebhookConfiguration`
- `ValidatingWebhookConfiguration`
- `CustomResourceDefinition` resources that use **conversion webhooks**

From these objects, it discovers the **Service references** used as webhook backends.

---

### 2. Service Transformation

For each discovered webhook Service:

- The original `ClusterIP` Service is **preserved**.
- A **NodePort clone** of the Service is created.
- The controller **removes `spec.selector` from the original Service** to prevent the native Kubernetes `endpointslice-controller` from populating endpoints with unreachable Pod IPs.

This step is essential to take full control over endpoint resolution while keeping the Service identity unchanged.

---

### 3. Custom Endpoint Provisioning

The controller then provisions a **custom `EndpointSlice`**:

- Endpoints reference **Node Internal IPs** instead of Pod IPs.
- Each endpoint uses the allocated **NodePort**.
- The slice is linked to the original Service via the  
  `kubernetes.io/service-name` label.

As a result, traffic sent to the Service is routed through **node-level, VPC-routable addresses**.

---

### 4. Traffic Flow

When the EKS control plane invokes a webhook:

1. It connects to the **ClusterIP Service** (as defined in the webhook configuration).
2. Kubernetes resolves the Service to the **custom EndpointSlice**.
3. Traffic is routed to **Node InternalIP : NodePort**.
4. kube-proxy forwards the traffic to the webhook pods.

From the control plane’s perspective, this is a standard Service-based webhook call.

---

## Configuration (Helm Chart Parameters)

The Helm chart exposes the following options to control security and behavior:

| Parameter | Type | Description |
|---------|------|-------------|
| `options.webhookRestricted` | Boolean | If enabled, the controller creates a `NetworkPolicy` restricting access to webhook pods. |
| `options.webhookAllowedCIDRS` | List | List of allowed source CIDRs (for example, the EKS control plane CIDR). Only used when `webhookRestricted` is enabled. |

---

## Important Considerations & Limitations

### Service Selector Removal

To safely redirect traffic, the controller **removes the `spec.selector` field** from the original Service.

This is required to prevent Kubernetes from automatically creating `EndpointSlice` objects that point to **unreachable Pod IPs**.

---

### Continuous Delivery (ArgoCD / Flux)

If you use a GitOps tool such as ArgoCD or Flux, it may report configuration drift because the Service selector defined in Git is intentionally removed at runtime.

You must configure your CD system to ignore this difference.

#### ArgoCD Example

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
spec:
  ignoreDifferences:
    - group: ""
      kind: Service
      jsonPointers:
        - /spec/selector
```

