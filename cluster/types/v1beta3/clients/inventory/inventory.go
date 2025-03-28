package inventory

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/akash-network/node/sdl"
	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/resource"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	"github.com/akash-network/akash-api/go/node/types/unit"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const (
	// 5 CPUs, 5Gi memory for null client.
	nullClientCPU     = 5000
	nullClientGPU     = 2
	nullClientMemory  = 32 * unit.Gi
	nullClientStorage = 512 * unit.Gi
)

type Client interface {
	// ResultChan returns a chan which will receive all the events. If an error occurs
	// or Stop() is called, the implementation will close this channel and
	// release any resources used by the watch.
	ResultChan() <-chan ctypes.Inventory
}

type NullClient interface {
	Client
	Commit(dtypes.ResourceGroup) bool
}

type commitReq struct {
	res  dtypes.ResourceGroup
	resp chan<- struct{}
}

type nullInventory struct {
	ctx     context.Context
	group   *errgroup.Group
	subch   chan chan<- ctypes.Inventory
	cmch    chan commitReq
	cluster inventoryV1.Cluster
}

type inventory struct {
	inventoryV1.Cluster
}

var (
	_ Client           = (*nullInventory)(nil)
	_ ctypes.Inventory = (*inventory)(nil)
)

func NewNull(ctx context.Context, nodes ...string) NullClient {
	group, ctx := errgroup.WithContext(ctx)

	cluster := inventoryV1.Cluster{}
	cluster.Storage = append(cluster.Storage, inventoryV1.Storage{
		Quantity: inventoryV1.NewResourcePair(nullClientStorage, nullClientStorage, nullClientStorage-(10*unit.Gi), resource.DecimalSI),
		Info: inventoryV1.StorageInfo{
			Class: "beta2",
		},
	})

	for _, ndName := range nodes {
		nd := inventoryV1.Node{
			Name: ndName,
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(nullClientCPU, nullClientCPU, 100, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(nullClientMemory, nullClientMemory, 1*unit.Gi, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(nullClientStorage, nullClientStorage, 10*unit.Gi, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
			Capabilities: inventoryV1.NodeCapabilities{},
		}

		cluster.Nodes = append(cluster.Nodes, nd)
	}

	if len(cluster.Nodes) == 0 {
		cluster.Nodes = append(cluster.Nodes, inventoryV1.Node{
			Name: "solo",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(nullClientCPU, nullClientCPU, 100, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(nullClientMemory, nullClientMemory, 1*unit.Gi, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(nullClientGPU, nullClientGPU, 1, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(nullClientStorage, nullClientStorage, 10*unit.Gi, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
			Capabilities: inventoryV1.NodeCapabilities{},
		})
	}

	cl := &nullInventory{
		ctx:     ctx,
		group:   group,
		subch:   make(chan chan<- ctypes.Inventory, 1),
		cmch:    make(chan commitReq, 1),
		cluster: cluster,
	}

	group.Go(cl.run)

	return cl
}

// Commit at the moment commit works on single node clusters
func (cl *nullInventory) Commit(res dtypes.ResourceGroup) bool {
	ch := make(chan struct{}, 1)

	select {
	case <-cl.ctx.Done():
		return false
	case cl.cmch <- commitReq{
		res:  res,
		resp: ch,
	}:
	}

	select {
	case <-cl.ctx.Done():
		return false
	case <-ch:
		return true
	}
}

func (cl *nullInventory) ResultChan() <-chan ctypes.Inventory {
	ch := make(chan ctypes.Inventory, 1)

	select {
	case <-cl.ctx.Done():
		close(ch)
	case cl.subch <- ch:
	}

	return ch
}

func (cl *nullInventory) run() error {
	var subs []chan<- inventoryV1.Cluster

	for {
		select {
		case <-cl.ctx.Done():
			return cl.ctx.Err()
		case cmreq := <-cl.cmch:
			ru := cmreq.res.GetResourceUnits()

			ndRes := &cl.cluster.Nodes[0].Resources

			for _, res := range ru {
				if res.CPU != nil {
					ndRes.CPU.Quantity.SubNLZ(res.CPU.Units)
				}

				if res.GPU != nil {
					ndRes.GPU.Quantity.SubNLZ(res.GPU.Units)
				}

				if res.Memory != nil {
					ndRes.Memory.Quantity.SubNLZ(res.Memory.Quantity)
				}

				for i, storage := range res.Storage {
					attrs, _ := ParseStorageAttributes(storage.Attributes)

					if !attrs.Persistent {
						if attrs.Class == sdl.StorageClassRAM {
							ndRes.Memory.Quantity.SubNLZ(storage.Quantity)
						} else {
							// ephemeral storage
							tryAdjustEphemeralStorage(&ndRes.EphemeralStorage, &res.Storage[i])
						}
					} else {
						for idx := range cl.cluster.Storage {
							if cl.cluster.Storage[idx].Info.Class == attrs.Class {
								cl.cluster.Storage[idx].Quantity.SubNLZ(storage.Quantity)
								break
							}
						}
					}
				}
			}

			for _, sub := range subs {
				sub <- *cl.cluster.Dup()
			}

			cmreq.resp <- struct{}{}
		case reqch := <-cl.subch:
			ch := make(chan inventoryV1.Cluster, 1)

			subs = append(subs, ch)

			cl.group.Go(func() error {
				return cl.subscriber(ch, reqch)
			})

			ch <- *cl.cluster.Dup()
		}
	}
}

func (cl *nullInventory) subscriber(in <-chan inventoryV1.Cluster, out chan<- ctypes.Inventory) error {
	defer close(out)

	var pending []inventoryV1.Cluster
	var msg ctypes.Inventory
	var och chan<- ctypes.Inventory

	for {
		select {
		case <-cl.ctx.Done():
			return cl.ctx.Err()
		case inv := <-in:
			pending = append(pending, inv)
			if och == nil {
				msg = newInventory(pending[0])
				och = out
			}
		case och <- msg:
			pending = pending[1:]
			if len(pending) > 0 {
				msg = newInventory(pending[0])
			} else {
				och = nil
				msg = nil
			}
		}
	}
}

func newInventory(clState inventoryV1.Cluster) *inventory {
	inv := &inventory{
		Cluster: clState,
	}

	return inv
}

func (inv *inventory) dup() inventory {
	dup := inventory{
		Cluster: *inv.Cluster.Dup(),
	}

	return dup
}

func (inv *inventory) Dup() ctypes.Inventory {
	dup := inv.dup()

	return &dup
}

// tryAdjust cluster inventory
// It returns two boolean values. First indicates if node-wide resources satisfy (true) requirements
// Seconds indicates if cluster-wide resources satisfy (true) requirements
func (inv *inventory) tryAdjust(node int, res *types.Resources) (*crd.SchedulerParams, bool, bool) {
	nd := inv.Nodes[node].Dup()
	sparams := &crd.SchedulerParams{}

	if !tryAdjustCPU(&nd.Resources.CPU.Quantity, res.CPU) {
		return nil, false, true
	}

	if !tryAdjustGPU(&nd.Resources.GPU, res.GPU, sparams) {
		return nil, false, true
	}

	if !nd.Resources.Memory.Quantity.SubNLZ(res.Memory.Quantity) {
		return nil, false, true
	}

	storageClasses := inv.Storage.Dup()

	for i, storage := range res.Storage {
		attrs, err := ParseStorageAttributes(storage.Attributes)
		if err != nil {
			return nil, false, false
		}

		if !attrs.Persistent {
			if attrs.Class == sdl.StorageClassRAM {
				if !nd.Resources.Memory.Quantity.SubNLZ(storage.Quantity) {
					return nil, false, true
				}
			} else {
				// ephemeral storage
				if !tryAdjustEphemeralStorage(&nd.Resources.EphemeralStorage, &res.Storage[i]) {
					return nil, false, true
				}
			}

			continue
		}

		if !nd.IsStorageClassSupported(attrs.Class) {
			return nil, false, true
		}

		storageAdjusted := false

		for idx := range storageClasses {
			if storageClasses[idx].Info.Class == attrs.Class {
				if !storageClasses[idx].Quantity.SubNLZ(storage.Quantity) {
					// cluster storage does not have enough space thus break to error
					return nil, false, false
				}
				storageAdjusted = true
				break
			}
		}

		// requested storage class is not present in the cluster
		// there is no point to adjust inventory further
		if !storageAdjusted {
			return nil, false, false
		}
	}

	// all requirements for current group have been satisfied
	// commit and move on
	inv.Nodes[node] = nd
	inv.Storage = storageClasses

	if reflect.DeepEqual(sparams, &crd.SchedulerParams{}) {
		return nil, true, true
	}

	return sparams, true, true
}

func tryAdjustCPU(rp *inventoryV1.ResourcePair, res *types.CPU) bool {
	return rp.SubMilliNLZ(res.Units)
}

func tryAdjustGPU(rp *inventoryV1.GPU, res *types.GPU, sparams *crd.SchedulerParams) bool {
	reqCnt := res.Units.Value()

	if reqCnt == 0 {
		return true
	}

	if rp.Quantity.Available().Value() == 0 {
		return false
	}

	attrs, err := ParseGPUAttributes(res.Attributes)
	if err != nil {
		return false
	}

	for _, info := range rp.Info {
		models, exists := attrs[info.Vendor]
		if !exists {
			continue
		}

		attr, exists := models.ExistsOrWildcard(info.Name)
		if !exists {
			continue
		}

		if attr != nil {
			if (attr.RAM != "") && (attr.RAM != info.MemorySize) {
				continue
			}

			if (attr.Interface != "") && (attr.Interface != info.Interface) {
				continue
			}
		}

		reqCnt--
		if reqCnt == 0 {
			vendor := strings.ToLower(info.Vendor)

			if !rp.Quantity.SubNLZ(res.Units) {
				return false
			}

			// sParamsEnsureGPU(sparams)
			sparams.Resources.GPU.Vendor = vendor
			sparams.Resources.GPU.Model = info.Name

			// switch vendor {
			// case builder.GPUVendorNvidia:
			// 	sparams.RuntimeClass = runtimeClassNvidia
			// default:
			// }

			key := fmt.Sprintf("vendor/%s/model/%s", vendor, info.Name)
			if attr != nil {
				if attr.RAM != "" {
					key = fmt.Sprintf("%s/ram/%s", key, attr.RAM)
				}

				if attr.Interface != "" {
					key = fmt.Sprintf("%s/interface/%s", key, attr.Interface)
				}
			}

			res.Attributes = types.Attributes{
				{
					Key:   key,
					Value: "true",
				},
			}

			return true
		}
	}

	return false
}

func tryAdjustEphemeralStorage(rp *inventoryV1.ResourcePair, res *types.Storage) bool {
	return rp.SubNLZ(res.Quantity)
}

// nolint: unused
func tryAdjustVolumesAttached(rp *inventoryV1.ResourcePair, res types.ResourceValue) bool {
	return rp.SubNLZ(res)
}

func (inv *inventory) Adjust(reservation ctypes.ReservationGroup, opts ...ctypes.InventoryOption) error {
	cfg := &ctypes.InventoryOptions{}
	for _, opt := range opts {
		cfg = opt(cfg)
	}

	origResources := reservation.Resources().GetResourceUnits()
	resources := make(dtypes.ResourceUnits, 0, len(origResources))
	adjustedResources := make(dtypes.ResourceUnits, 0, len(origResources))

	for _, res := range origResources {
		resources = append(resources, dtypes.ResourceUnit{
			Resources: res.Resources.Dup(),
			Count:     res.Count,
		})

		adjustedResources = append(adjustedResources, dtypes.ResourceUnit{
			Resources: res.Resources.Dup(),
			Count:     res.Count,
		})
	}

	cparams := make(crd.ReservationClusterSettings)

	currInventory := inv.dup()

	var err error

nodes:
	for nodeIdx := range currInventory.Nodes {
		for i := len(resources) - 1; i >= 0; i-- {
			adjustedGroup := false

			var adjusted *types.Resources
			if origResources[i].Count == resources[i].Count {
				adjusted = &adjustedResources[i].Resources
			} else {
				adjustedGroup = true
				res := adjustedResources[i].Resources.Dup()
				adjusted = &res
			}

			for ; resources[i].Count > 0; resources[i].Count-- {
				sparams, nStatus, cStatus := currInventory.tryAdjust(nodeIdx, adjusted)
				if !cStatus {
					// cannot satisfy cluster-wide resources, stop lookup
					break nodes
				}

				if !nStatus {
					// cannot satisfy node-wide resources, try with next node
					continue nodes
				}

				// at this point we expect all replicas of the same service to produce
				// same adjusted resource units as well as cluster params
				if adjustedGroup {
					if !reflect.DeepEqual(adjusted, &adjustedResources[i].Resources) {
						err = ctypes.ErrGroupResourceMismatch
						break nodes
					}

					// all replicas of the same service are expected to have same node selectors and runtimes
					// if they don't match then provider cannot bid
					if !reflect.DeepEqual(sparams, cparams[adjusted.ID]) {
						err = ctypes.ErrGroupResourceMismatch
						break nodes
					}
				} else {
					cparams[adjusted.ID] = sparams
				}
			}

			// all replicas resources are fulfilled when count == 0.
			// remove group from the list to prevent double request of the same resources
			if resources[i].Count == 0 {
				resources = append(resources[:i], resources[i+1:]...)
				goto nodes
			}
		}
	}

	if len(resources) == 0 {
		if !cfg.DryRun {
			*inv = currInventory
		}

		reservation.SetAllocatedResources(adjustedResources)
		reservation.SetClusterParams(cparams)

		return nil
	}

	if err != nil {
		return err
	}

	return ctypes.ErrInsufficientCapacity
}

func (inv *inventory) Snapshot() inventoryV1.Cluster {
	return *inv.Cluster.Dup()
}

func (inv *inventory) Metrics() inventoryV1.Metrics {
	cpuTotal := uint64(0)
	gpuTotal := uint64(0)
	memoryTotal := uint64(0)
	storageEphemeralTotal := uint64(0)
	storageTotal := make(map[string]int64)

	cpuAvailable := uint64(0)
	gpuAvailable := uint64(0)
	memoryAvailable := uint64(0)
	storageEphemeralAvailable := uint64(0)
	storageAvailable := make(map[string]int64)

	ret := inventoryV1.Metrics{
		Nodes: make([]inventoryV1.NodeMetrics, 0, len(inv.Nodes)),
	}

	for _, nd := range inv.Nodes {
		invNode := inventoryV1.NodeMetrics{
			Name: nd.Name,
			Allocatable: inventoryV1.ResourcesMetric{
				CPU:              uint64(nd.Resources.CPU.Quantity.Allocatable.MilliValue()), // nolint: gosec
				GPU:              uint64(nd.Resources.GPU.Quantity.Allocatable.Value()),      // nolint: gosec
				Memory:           uint64(nd.Resources.Memory.Quantity.Allocatable.Value()),   // nolint: gosec
				StorageEphemeral: uint64(nd.Resources.EphemeralStorage.Allocatable.Value()),  // nolint: gosec
			},
		}

		cpuTotal += uint64(nd.Resources.CPU.Quantity.Allocatable.MilliValue())             // nolint: gosec
		gpuTotal += uint64(nd.Resources.GPU.Quantity.Allocatable.Value())                  // nolint: gosec
		memoryTotal += uint64(nd.Resources.Memory.Quantity.Allocatable.Value())            // nolint: gosec
		storageEphemeralTotal += uint64(nd.Resources.EphemeralStorage.Allocatable.Value()) // nolint: gosec

		avail := nd.Resources.CPU.Quantity.Available()
		invNode.Available.CPU = uint64(avail.MilliValue()) // nolint: gosec
		cpuAvailable += invNode.Available.CPU

		avail = nd.Resources.GPU.Quantity.Available()
		invNode.Available.GPU = uint64(avail.Value()) // nolint: gosec
		gpuAvailable += invNode.Available.GPU

		avail = nd.Resources.Memory.Quantity.Available()
		invNode.Available.Memory = uint64(avail.Value()) // nolint: gosec
		memoryAvailable += invNode.Available.Memory

		avail = nd.Resources.EphemeralStorage.Available()
		invNode.Available.StorageEphemeral = uint64(avail.Value()) // nolint: gosec
		storageEphemeralAvailable += invNode.Available.StorageEphemeral

		ret.Nodes = append(ret.Nodes, invNode)
	}

	for _, class := range inv.Storage {
		tmp := class.Quantity.Allocatable.DeepCopy()
		storageTotal[class.Info.Class] = tmp.Value()

		tmp = *class.Quantity.Available()
		storageAvailable[class.Info.Class] = tmp.Value()
	}

	ret.TotalAllocatable = inventoryV1.MetricTotal{
		CPU:              cpuTotal,
		GPU:              gpuTotal,
		Memory:           memoryTotal,
		StorageEphemeral: storageEphemeralTotal,
		Storage:          storageTotal,
	}

	ret.TotalAvailable = inventoryV1.MetricTotal{
		CPU:              cpuAvailable,
		GPU:              gpuAvailable,
		Memory:           memoryAvailable,
		StorageEphemeral: storageEphemeralAvailable,
		Storage:          storageAvailable,
	}

	return ret
}

// func (inv *inventory) Adjust(reservation ctypes.ReservationGroup, _ ...ctypes.InventoryOption) error {
// 	resources := make(dtypes.ResourceUnits, len(reservation.Resources().GetResourceUnits()))
// 	copy(resources, reservation.Resources().GetResourceUnits())
//
// 	currInventory := inv.dup()
//
// nodes:
// 	for nodeName, nd := range currInventory.nodes {
// 		// with persistent storage go through iff there is capacity available
// 		// there is no point to go through any other node without available storage
// 		currResources := resources[:0]
//
// 		for _, res := range resources {
// 			for ; res.Count > 0; res.Count-- {
// 				var adjusted bool
//
// 				cpu := nd.cpu.dup()
// 				if adjusted = cpu.subNLZ(res.Resources.CPU.Units); !adjusted {
// 					continue nodes
// 				}
//
// 				gpu := nd.gpu.dup()
// 				if res.Resources.GPU != nil {
// 					if adjusted = gpu.subNLZ(res.Resources.GPU.Units); !adjusted {
// 						continue nodes
// 					}
// 				}
//
// 				memory := nd.memory.dup()
// 				if adjusted = memory.subNLZ(res.Resources.Memory.Quantity); !adjusted {
// 					continue nodes
// 				}
//
// 				ephemeralStorage := nd.ephemeralStorage.dup()
// 				storageClasses := currInventory.storage.dup()
//
// 				for idx, storage := range res.Resources.Storage {
// 					attr := storage.Attributes.Find(sdl.StorageAttributePersistent)
//
// 					if persistent, _ := attr.AsBool(); !persistent {
// 						if adjusted = ephemeralStorage.subNLZ(storage.Quantity); !adjusted {
// 							continue nodes
// 						}
// 						continue
// 					}
//
// 					attr = storage.Attributes.Find(sdl.StorageAttributeClass)
// 					class, _ := attr.AsString()
//
// 					if class == sdl.StorageClassDefault {
// 						for name, params := range storageClasses {
// 							if params.isDefault {
// 								class = name
//
// 								for i := range storage.Attributes {
// 									if storage.Attributes[i].Key == sdl.StorageAttributeClass {
// 										res.Resources.Storage[idx].Attributes[i].Value = class
// 										break
// 									}
// 								}
// 								break
// 							}
// 						}
// 					}
//
// 					cstorage, activeStorageClass := storageClasses[class]
// 					if !activeStorageClass {
// 						continue nodes
// 					}
//
// 					if adjusted = cstorage.subNLZ(storage.Quantity); !adjusted {
// 						// cluster storage does not have enough space thus break to error
// 						break nodes
// 					}
// 				}
//
// 				// all requirements for current group have been satisfied
// 				// commit and move on
// 				currInventory.nodes[nodeName] = &node{
// 					id:               nd.id,
// 					cpu:              cpu,
// 					gpu:              gpu,
// 					memory:           memory,
// 					ephemeralStorage: ephemeralStorage,
// 				}
// 			}
//
// 			if res.Count > 0 {
// 				currResources = append(currResources, res)
// 			}
// 		}
//
// 		resources = currResources
// 	}
//
// 	if len(resources) == 0 {
// 		*inv = *currInventory
//
// 		return nil
// 	}
//
// 	return ctypes.ErrInsufficientCapacity
// }
//
// func (inv *inventory) Metrics() inventoryV1.Metrics {
// 	cpuTotal := uint64(0)
// 	gpuTotal := uint64(0)
// 	memoryTotal := uint64(0)
// 	storageEphemeralTotal := uint64(0)
// 	storageTotal := make(map[string]int64)
//
// 	cpuAvailable := uint64(0)
// 	gpuAvailable := uint64(0)
// 	memoryAvailable := uint64(0)
// 	storageEphemeralAvailable := uint64(0)
// 	storageAvailable := make(map[string]int64)
//
// 	ret := inventoryV1.Metrics{
// 		Nodes: make([]inventoryV1.NodeMetrics, 0, len(inv.nodes)),
// 	}
//
// 	for _, nd := range inv.nodes {
// 		invNode := inventoryV1.NodeMetrics{
// 			Name: nd.id,
// 			Allocatable: inventoryV1.ResourcesMetric{
// 				CPU:              nd.cpu.allocatable.Uint64(),
// 				Memory:           nd.memory.allocatable.Uint64(),
// 				StorageEphemeral: nd.ephemeralStorage.allocatable.Uint64(),
// 			},
// 		}
//
// 		cpuTotal += nd.cpu.allocatable.Uint64()
// 		gpuTotal += nd.gpu.allocatable.Uint64()
//
// 		memoryTotal += nd.memory.allocatable.Uint64()
// 		storageEphemeralTotal += nd.ephemeralStorage.allocatable.Uint64()
//
// 		tmp := nd.cpu.allocatable.Sub(nd.cpu.allocated)
// 		invNode.Available.CPU = tmp.Uint64()
// 		cpuAvailable += invNode.Available.CPU
//
// 		tmp = nd.gpu.allocatable.Sub(nd.gpu.allocated)
// 		invNode.Available.GPU = tmp.Uint64()
// 		gpuAvailable += invNode.Available.GPU
//
// 		tmp = nd.memory.allocatable.Sub(nd.memory.allocated)
// 		invNode.Available.Memory = tmp.Uint64()
// 		memoryAvailable += invNode.Available.Memory
//
// 		tmp = nd.ephemeralStorage.allocatable.Sub(nd.ephemeralStorage.allocated)
// 		invNode.Available.StorageEphemeral = tmp.Uint64()
// 		storageEphemeralAvailable += invNode.Available.StorageEphemeral
//
// 		ret.Nodes = append(ret.Nodes, invNode)
// 	}
//
// 	ret.TotalAllocatable = inventoryV1.MetricTotal{
// 		CPU:              cpuTotal,
// 		GPU:              gpuTotal,
// 		Memory:           memoryTotal,
// 		StorageEphemeral: storageEphemeralTotal,
// 		Storage:          storageTotal,
// 	}
//
// 	ret.TotalAvailable = inventoryV1.MetricTotal{
// 		CPU:              cpuAvailable,
// 		GPU:              gpuAvailable,
// 		Memory:           memoryAvailable,
// 		StorageEphemeral: storageEphemeralAvailable,
// 		Storage:          storageAvailable,
// 	}
//
// 	return ret
// }
//
// func (inv *inventory) Snapshot() inventoryV1.Cluster {
// 	res := inventoryV1.Cluster{
// 		Nodes:   make(inventoryV1.Nodes, 0, len(inv.nodes)),
// 		Storage: make(inventoryV1.ClusterStorage, 0, len(inv.storage)),
// 	}
//
// 	for i := range inv.nodes {
// 		nd := inv.nodes[i]
// 		res.Nodes = append(res.Nodes, inventoryV1.Node{
// 			Name: nd.id,
// 			Resources: inventoryV1.NodeResources{
// 				CPU: inventoryV1.CPU{
// 					Quantity: inventoryV1.NewResourcePair(nd.cpu.allocatable.Int64(), nd.cpu.allocated.Int64(), "m"),
// 				},
// 				Memory: inventoryV1.Memory{
// 					Quantity: inventoryV1.NewResourcePair(nd.memory.allocatable.Int64(), nd.memory.allocated.Int64(), resource.DecimalSI),
// 				},
// 				GPU: inventoryV1.GPU{
// 					Quantity: inventoryV1.NewResourcePair(nd.gpu.allocatable.Int64(), nd.gpu.allocated.Int64(), resource.DecimalSI),
// 				},
// 				EphemeralStorage: inventoryV1.NewResourcePair(nd.ephemeralStorage.allocatable.Int64(), nd.ephemeralStorage.allocated.Int64(), resource.DecimalSI),
// 				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
// 				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
// 			},
// 			Capabilities: inventoryV1.NodeCapabilities{},
// 		})
// 	}
//
// 	for class, storage := range inv.storage {
// 		res.Storage = append(res.Storage, inventoryV1.Storage{
// 			Quantity: inventoryV1.NewResourcePair(storage.allocatable.Int64(), storage.allocated.Int64(), resource.DecimalSI),
// 			Info: inventoryV1.StorageInfo{
// 				Class: class,
// 			},
// 		})
// 	}
//
// 	return res
// }
//
// func (inv *inventory) dup() *inventory {
// 	res := &inventory{
// 		nodes: make([]*node, 0, len(inv.nodes)),
// 	}
//
// 	for _, nd := range inv.nodes {
// 		res.nodes = append(res.nodes, &node{
// 			id:               nd.id,
// 			cpu:              nd.cpu.dup(),
// 			gpu:              nd.gpu.dup(),
// 			memory:           nd.memory.dup(),
// 			ephemeralStorage: nd.ephemeralStorage.dup(),
// 		})
// 	}
//
// 	return res
// }
//
// func (inv *inventory) Dup() ctypes.Inventory {
// 	return inv.dup()
// }
