package agent

import (
	"context"
	"strings"
	"sync"

	"serial-platform/internal/serial"
)

type BackendFactory interface {
	Open(devName string, config serial.Config) (serial.Backend, error)
}

type RealBackendFactory struct{}

func (RealBackendFactory) Open(devName string, config serial.Config) (serial.Backend, error) {
	return serial.NewRealBackend(devName, config)
}

type ChannelConfig struct {
	ID            string
	AgentID       string
	DevName       string
	IDPath        string
	IDPathTag     string
	Status        string
	DefaultConfig serial.Config
}

type ChannelStatus struct {
	ChannelID    string
	Status       string
	DevName      string
	ErrorMessage string
}

type ReconcileResult struct {
	Statuses   []ChannelStatus
	Candidates []DiscoveredDevice
	Events     []<-chan serial.Event
}

type ReconcilerConfig struct {
	BackendFactory BackendFactory
}

type Reconciler struct {
	mu             sync.Mutex
	backendFactory BackendFactory
	workers        map[string]*managedWorker
}

type managedWorker struct {
	worker  *serial.Worker
	cancel  context.CancelFunc
	devName string
	idPath  string
	events  <-chan serial.Event
}

func NewReconciler(config ReconcilerConfig) *Reconciler {
	backendFactory := config.BackendFactory
	if backendFactory == nil {
		backendFactory = RealBackendFactory{}
	}
	return &Reconciler{
		backendFactory: backendFactory,
		workers:        make(map[string]*managedWorker),
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, channels []ChannelConfig, devices []DiscoveredDevice) ReconcileResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	desired := make(map[string]struct{}, len(channels))
	configuredIDPaths := make(map[string]struct{}, len(channels))
	configuredIDPathTags := make(map[string]struct{}, len(channels))
	result := ReconcileResult{
		Statuses: make([]ChannelStatus, 0, len(channels)),
	}

	for _, channel := range channels {
		if channel.ID == "" {
			continue
		}
		desired[channel.ID] = struct{}{}
		if channel.IDPath != "" {
			configuredIDPaths[channel.IDPath] = struct{}{}
		}
		if channel.IDPathTag != "" {
			configuredIDPathTags[channel.IDPathTag] = struct{}{}
		}

		device, matched := matchChannelDevice(channel, devices)
		if !channelEnabled(channel) {
			r.stopWorkerLocked(channel.ID)
			result.Statuses = append(result.Statuses, ChannelStatus{
				ChannelID: channel.ID,
				Status:    "disabled",
			})
			continue
		}
		if !matched {
			r.stopWorkerLocked(channel.ID)
			result.Statuses = append(result.Statuses, ChannelStatus{
				ChannelID: channel.ID,
				Status:    "offline",
			})
			continue
		}
		if !device.PermissionOK {
			r.stopWorkerLocked(channel.ID)
			result.Statuses = append(result.Statuses, ChannelStatus{
				ChannelID:    channel.ID,
				Status:       "error",
				DevName:      device.DevName,
				ErrorMessage: device.ErrorMessage,
			})
			continue
		}

		worker := r.workers[channel.ID]
		if worker != nil && workerMatches(worker, channel, device) {
			result.Statuses = append(result.Statuses, ChannelStatus{
				ChannelID: channel.ID,
				Status:    "online",
				DevName:   worker.devName,
			})
			continue
		}
		r.stopWorkerLocked(channel.ID)

		backend, err := r.backendFactory.Open(device.DevName, channel.DefaultConfig)
		if err != nil {
			result.Statuses = append(result.Statuses, ChannelStatus{
				ChannelID:    channel.ID,
				Status:       "error",
				DevName:      device.DevName,
				ErrorMessage: err.Error(),
			})
			continue
		}

		workerCtx, cancel := context.WithCancel(ctx)
		serialWorker := serial.NewWorker(channel.ID, channel.DefaultConfig, backend)
		events := proxyWorkerEvents(workerCtx, serialWorker.Events())
		r.workers[channel.ID] = &managedWorker{
			worker:  serialWorker,
			cancel:  cancel,
			devName: device.DevName,
			idPath:  device.IDPath,
			events:  events,
		}
		go serialWorker.Run(workerCtx)

		result.Statuses = append(result.Statuses, ChannelStatus{
			ChannelID: channel.ID,
			Status:    "online",
			DevName:   device.DevName,
		})
		result.Events = append(result.Events, events)
	}

	for channelID := range r.workers {
		if _, ok := desired[channelID]; !ok {
			r.stopWorkerLocked(channelID)
		}
	}

	for _, device := range devices {
		if device.IDPath != "" {
			if _, ok := configuredIDPaths[device.IDPath]; ok {
				continue
			}
		}
		if device.IDPathTag != "" {
			if _, ok := configuredIDPathTags[device.IDPathTag]; ok {
				continue
			}
		}
		result.Candidates = append(result.Candidates, device)
	}

	return result
}

func channelEnabled(channel ChannelConfig) bool {
	return strings.ToLower(strings.TrimSpace(channel.Status)) != "disabled"
}

func matchChannelDevice(channel ChannelConfig, devices []DiscoveredDevice) (DiscoveredDevice, bool) {
	for _, device := range devices {
		if channel.IDPath != "" && device.IDPath == channel.IDPath {
			return device, true
		}
		if channel.IDPathTag != "" && device.IDPathTag == channel.IDPathTag {
			return device, true
		}
		if channel.IDPath == "" && channel.IDPathTag == "" && channel.DevName != "" && device.DevName == channel.DevName {
			return device, true
		}
	}
	return DiscoveredDevice{}, false
}

func workerMatches(worker *managedWorker, channel ChannelConfig, device DiscoveredDevice) bool {
	if worker.devName != device.DevName {
		return false
	}
	if channel.IDPath != "" && worker.idPath != device.IDPath {
		return false
	}
	return true
}

func (r *Reconciler) stopWorkerLocked(channelID string) {
	worker := r.workers[channelID]
	if worker == nil {
		return
	}
	worker.cancel()
	delete(r.workers, channelID)
}

func proxyWorkerEvents(ctx context.Context, source <-chan serial.Event) <-chan serial.Event {
	events := make(chan serial.Event, 64)
	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-source:
				if !ok {
					return
				}
				select {
				case <-ctx.Done():
					return
				case events <- event:
				}
			}
		}
	}()
	return events
}
