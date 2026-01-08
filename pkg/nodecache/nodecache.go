package nodecache

import (
	"context"
	"net"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sync"

	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
)

type NodeIPCache struct {
	mu   sync.RWMutex
	data map[string]string // nodeName -> InternalIP
}

func NewNodeIPCache() *NodeIPCache {
	return &NodeIPCache{
		data: make(map[string]string),
	}
}

func (c *NodeIPCache) Set(nodeName, ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[nodeName] = ip
}

func (c *NodeIPCache) Delete(nodeName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, nodeName)
}

func (c *NodeIPCache) Get(nodeName string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ip, ok := c.data[nodeName]
	return ip, ok
}

// getInternalIP returns IPv4 node internalIP.
func getInternalIP(node *corev1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type != corev1.NodeInternalIP {
			continue
		}

		ip := net.ParseIP(addr.Address)
		if ip == nil {
			continue
		}

		if ip.To4() != nil {
			return ip.String()
		}
	}
	return ""
}

func SetupNodeWatch(
	mgr ctrl.Manager,
	cache *NodeIPCache,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("node-ip-cache").
		For(&corev1.Node{}).
		Watches(
			&corev1.Node{},
			handler.TypedFuncs[client.Object, reconcile.Request]{
				CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
					node, ok := e.Object.(*corev1.Node)
					if !ok || node == nil {
						return
					}
					if ip := getInternalIP(node); ip != "" {
						cache.Set(node.Name, ip)
					}
				},
				UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
					node, ok := e.ObjectNew.(*corev1.Node)
					if !ok || node == nil {
						return
					}
					if ip := getInternalIP(node); ip != "" {
						cache.Set(node.Name, ip)
					}
				},
				DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
					node, ok := e.Object.(*corev1.Node)
					if !ok || node == nil {
						return
					}
					if node != nil {
						cache.Delete(node.Name)
					}
				},
			},
		).
		Complete(reconcile.Func(func(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
			return ctrl.Result{}, nil
		}))
}
