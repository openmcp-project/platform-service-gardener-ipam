package shared

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/event"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
)

var (
	config       *ipamv1alpha1.IPAMConfig
	lock         *sync.RWMutex
	ClusterWatch chan event.GenericEvent
	IPAM         *Ipamer
	providerName string
	environment  string
)

func init() {
	lock = &sync.RWMutex{}
	ClusterWatch = make(chan event.GenericEvent, 1024)
}

func GetConfig() *ipamv1alpha1.IPAMConfig {
	lock.RLock()
	defer lock.RUnlock()
	return config.DeepCopy()
}

func SetConfig(cfg *ipamv1alpha1.IPAMConfig) {
	lock.Lock()
	defer lock.Unlock()
	config = cfg.DeepCopy()
}

func ProviderName() string {
	if providerName == "" {
		panic("provider name not set")
	}
	return providerName
}

func SetProviderName(name string) {
	if name == "" {
		panic("provider name cannot be empty")
	}
	if providerName != "" {
		panic("provider name already set")
	}
	providerName = name
}

func Environment() string {
	if environment == "" {
		panic("environment not set")
	}
	return environment
}

func SetEnvironment(env string) {
	if env == "" {
		panic("environment cannot be empty")
	}
	if environment != "" {
		panic("environment already set")
	}
	environment = env
}
