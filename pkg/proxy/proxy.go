package proxy

import (
	"fmt"
	"strings"

	"crypto/sha256"
	"encoding/hex"
	"github.com/go-logr/logr"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/pkg/nodecache"
	"gitlab.wgdp.io/k8s/eks-webhook-proxy/config"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	proxySuffix = "eks-proxy"
	maxNameLen  = 63
)

type Proxy struct {
	config    *config.Config
	client    client.Client
	nodeCache *nodecache.NodeIPCache
	log       logr.Logger
}

func New(client client.Client, config *config.Config, nodeCache *nodecache.NodeIPCache) *Proxy {
	return &Proxy{
		config:    config,
		client:    client,
		nodeCache: nodeCache,
		log:       log.Log.WithName("proxy"),
	}
}

func getProxyName(serviceName string, hashLen int) string {
	sum := sha256.Sum256([]byte(serviceName))
	hash := hex.EncodeToString(sum[:])[:hashLen]

	suffix := fmt.Sprintf("-%s-%s", proxySuffix, hash)

	maxPrefixLen := maxNameLen - len(suffix)
	if maxPrefixLen < 1 {
		short := fmt.Sprintf("%s-%s", proxySuffix, hash)
		if len(short) > maxNameLen {
			return short[:maxNameLen]
		}
		return short
	}

	name := serviceName
	if len(name) > maxPrefixLen {
		name = name[:maxPrefixLen]
	}

	name = strings.TrimRight(name, "-")

	return name + suffix
}
