package shared

import (
	"context"
	"errors"
	"sync"

	goipam "github.com/metal-stack/go-ipam"
)

// Ipamer is a thread-safe implementation of goipam.Ipamer.
// It uses the default implementation internally and wraps each call with mutex locking to ensure thread safety.
// In addition to the default methods, it has some helper methods for our specific use-case.
type Ipamer struct {
	internal goipam.Ipamer
	lock     *sync.Mutex
	parents  []*goipam.Prefix
}

var _ goipam.Ipamer = &Ipamer{}

func NewIpam() *Ipamer {
	return &Ipamer{
		internal: goipam.New(context.Background()),
		lock:     &sync.Mutex{},
		parents:  []*goipam.Prefix{},
	}
}

// AddParentPrefixesFromStrings adds the specified prefixes (in CIDR notation) to the list of known parent prefixes.
func (i *Ipamer) AddParentPrefixesFromStrings(ctx context.Context, cidrs ...string) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	prefixes := []*goipam.Prefix{}
	for _, cidr := range cidrs {
		prefix, err := i.internal.PrefixFrom(ctx, cidr)
		if err != nil {
			if !errors.Is(err, goipam.ErrNotFound) {
				return err
			}
			prefix, err = i.internal.NewPrefix(ctx, cidr)
			if err != nil {
				return err
			}
		}
		prefixes = append(prefixes, prefix)
	}
	i.parents = append(i.parents, prefixes...)
	return nil
}

// AddParentPrefixes adds the specified prefixes to the list of known parent prefixes.
// Returns the Ipamer instance for chaining.
func (i *Ipamer) AddParentPrefixes(prefixes ...*goipam.Prefix) *Ipamer {
	i.lock.Lock()
	defer i.lock.Unlock()
	i.parents = append(i.parents, prefixes...)
	return i
}

// ParentPrefixes returns a copy of the list of known parent prefixes.
// Note that while the list is copied, the individual Prefix pointers are not deep-copied.
func (i *Ipamer) ParentPrefixes() []*goipam.Prefix {
	i.lock.Lock()
	defer i.lock.Unlock()
	res := make([]*goipam.Prefix, len(i.parents))
	copy(res, i.parents)
	return res
}

// goipam.Ipamer Implementation

// AcquireChildPrefix implements ipam.Ipamer.
func (i *Ipamer) AcquireChildPrefix(ctx context.Context, parentCidr string, length uint8) (*goipam.Prefix, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.AcquireChildPrefix(ctx, parentCidr, length)
}

// AcquireIP implements ipam.Ipamer.
func (i *Ipamer) AcquireIP(ctx context.Context, prefixCidr string) (*goipam.IP, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.AcquireIP(ctx, prefixCidr)
}

// AcquireSpecificChildPrefix implements ipam.Ipamer.
func (i *Ipamer) AcquireSpecificChildPrefix(ctx context.Context, parentCidr string, childCidr string) (*goipam.Prefix, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.AcquireSpecificChildPrefix(ctx, parentCidr, childCidr)
}

// AcquireSpecificIP implements ipam.Ipamer.
func (i *Ipamer) AcquireSpecificIP(ctx context.Context, prefixCidr string, specificIP string) (*goipam.IP, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.AcquireSpecificIP(ctx, prefixCidr, specificIP)
}

// CreateNamespace implements ipam.Ipamer.
func (i *Ipamer) CreateNamespace(ctx context.Context, namespace string) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.CreateNamespace(ctx, namespace)
}

// DeleteNamespace implements ipam.Ipamer.
func (i *Ipamer) DeleteNamespace(ctx context.Context, namespace string) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.DeleteNamespace(ctx, namespace)
}

// DeletePrefix implements ipam.Ipamer.
func (i *Ipamer) DeletePrefix(ctx context.Context, cidr string) (*goipam.Prefix, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.DeletePrefix(ctx, cidr)
}

// Dump implements ipam.Ipamer.
func (i *Ipamer) Dump(ctx context.Context) (string, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.Dump(ctx)
}

// ListNamespaces implements ipam.Ipamer.
func (i *Ipamer) ListNamespaces(ctx context.Context) ([]string, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.ListNamespaces(ctx)
}

// Load implements ipam.Ipamer.
func (i *Ipamer) Load(ctx context.Context, dump string) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.Load(ctx, dump)
}

// NewPrefix implements ipam.Ipamer.
func (i *Ipamer) NewPrefix(ctx context.Context, cidr string) (*goipam.Prefix, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.NewPrefix(ctx, cidr)
}

// PrefixFrom implements ipam.Ipamer.
func (i *Ipamer) PrefixFrom(ctx context.Context, cidr string) (*goipam.Prefix, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.PrefixFrom(ctx, cidr)
}

// ReadAllPrefixCidrs implements ipam.Ipamer.
func (i *Ipamer) ReadAllPrefixCidrs(ctx context.Context) ([]string, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.ReadAllPrefixCidrs(ctx)
}

// ReleaseChildPrefix implements ipam.Ipamer.
func (i *Ipamer) ReleaseChildPrefix(ctx context.Context, child *goipam.Prefix) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.ReleaseChildPrefix(ctx, child)
}

// ReleaseIP implements ipam.Ipamer.
func (i *Ipamer) ReleaseIP(ctx context.Context, ip *goipam.IP) (*goipam.Prefix, error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.ReleaseIP(ctx, ip)
}

// ReleaseIPFromPrefix implements ipam.Ipamer.
func (i *Ipamer) ReleaseIPFromPrefix(ctx context.Context, prefixCidr string, ip string) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.internal.ReleaseIPFromPrefix(ctx, prefixCidr, ip)
}
