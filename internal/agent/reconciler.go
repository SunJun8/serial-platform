package agent

import (
	"context"
	"errors"
	"strings"
	"sync"

	"serial-platform/internal/serial"
)

var errChannelControlUnavailable = errors.New("serial control is not available")

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
	control serial.SerialControl
	backend serial.Backend
	cancel  context.CancelFunc
	done    <-chan struct{}
	devName string
	idPath  string
	config  serial.Config
}

type workerBackend struct {
	serial.Backend
	closeOnce sync.Once
	closeErr  error
}

func (b *workerBackend) Close() error {
	b.closeOnce.Do(func() {
		b.closeErr = b.Backend.Close()
	})
	return b.closeErr
}

type managedWorkerControl struct {
	worker      *serial.Worker
	broadcaster *workerEventBroadcaster

	mu      sync.Mutex
	session *managedWorkerControlSession
}

func (c *managedWorkerControl) OpenControlSession(ctx context.Context, owner string) (serial.ControlSession, error) {
	session, err := c.worker.OpenControlSession(ctx, owner)
	if err != nil {
		return nil, err
	}
	wrapped := &managedWorkerControlSession{
		ControlSession: session,
		control:        c,
	}
	c.mu.Lock()
	c.session = wrapped
	c.mu.Unlock()
	return wrapped, nil
}

func (c *managedWorkerControl) Events() <-chan serial.Event {
	events, cancel := c.broadcaster.Subscribe()
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session != nil {
		session.addEventCancel(cancel)
	}
	return events
}

type managedWorkerControlSession struct {
	serial.ControlSession
	control      *managedWorkerControl
	mu           sync.Mutex
	closed       bool
	eventCancels []func()
}

func (s *managedWorkerControlSession) Close() error {
	s.mu.Lock()
	eventCancels := append([]func(){}, s.eventCancels...)
	s.eventCancels = nil
	alreadyClosed := s.closed
	s.closed = true
	s.mu.Unlock()

	if !alreadyClosed {
		for _, cancel := range eventCancels {
			cancel()
		}
		s.control.clearSession(s)
	}
	return s.ControlSession.Close()
}

func (s *managedWorkerControlSession) addEventCancel(cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		cancel()
		return
	}
	s.eventCancels = append(s.eventCancels, cancel)
}

func (c *managedWorkerControl) clearSession(session *managedWorkerControlSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == session {
		c.session = nil
	}
}

type workerEventBroadcaster struct {
	ctx    context.Context
	source <-chan serial.Event

	mu          sync.Mutex
	subscribers map[*workerEventSubscription]struct{}
	closed      bool
}

type workerEventSubscription struct {
	events chan serial.Event
	done   chan struct{}
	once   sync.Once
}

func newWorkerEventSubscription() *workerEventSubscription {
	return &workerEventSubscription{
		events: make(chan serial.Event, 64),
		done:   make(chan struct{}),
	}
}

func (s *workerEventSubscription) cancel() {
	s.once.Do(func() {
		close(s.done)
	})
}

func (s *workerEventSubscription) send(ctx context.Context, event serial.Event) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.done:
		return true
	case s.events <- event:
		return true
	}
}

func newWorkerEventBroadcaster(ctx context.Context, source <-chan serial.Event) *workerEventBroadcaster {
	b := &workerEventBroadcaster{
		ctx:         ctx,
		source:      source,
		subscribers: make(map[*workerEventSubscription]struct{}),
	}
	go b.run()
	return b
}

func (b *workerEventBroadcaster) Subscribe() (<-chan serial.Event, func()) {
	subscription := newWorkerEventSubscription()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(subscription.events)
		return subscription.events, func() {}
	}
	b.subscribers[subscription] = struct{}{}
	return subscription.events, func() { b.unsubscribe(subscription) }
}

func (b *workerEventBroadcaster) run() {
	defer b.closeSubscribers()
	for {
		select {
		case <-b.ctx.Done():
			return
		case event, ok := <-b.source:
			if !ok {
				return
			}
			if !b.broadcast(event) {
				return
			}
		}
	}
}

func (b *workerEventBroadcaster) broadcast(event serial.Event) bool {
	b.mu.Lock()
	subscribers := make([]*workerEventSubscription, 0, len(b.subscribers))
	for subscriber := range b.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	b.mu.Unlock()

	for _, subscriber := range subscribers {
		if !subscriber.send(b.ctx, event) {
			return false
		}
	}
	return true
}

func (b *workerEventBroadcaster) unsubscribe(subscription *workerEventSubscription) {
	subscription.cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	delete(b.subscribers, subscription)
}

func (b *workerEventBroadcaster) closeSubscribers() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for subscription := range b.subscribers {
		subscription.cancel()
		close(subscription.events)
	}
	b.subscribers = nil
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
		if worker != nil && !worker.running() {
			r.stopWorkerLocked(channel.ID)
			worker = nil
		}
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
		workerBackend := &workerBackend{Backend: backend}
		serialWorker := serial.NewWorker(channel.ID, channel.DefaultConfig, workerBackend)
		broadcaster := newWorkerEventBroadcaster(workerCtx, serialWorker.Events())
		control := &managedWorkerControl{
			worker:      serialWorker,
			broadcaster: broadcaster,
		}
		events, _ := broadcaster.Subscribe()
		done := make(chan struct{})
		r.workers[channel.ID] = &managedWorker{
			control: control,
			backend: workerBackend,
			cancel:  cancel,
			done:    done,
			devName: device.DevName,
			idPath:  device.IDPath,
			config:  channel.DefaultConfig,
		}
		go func() {
			defer close(done)
			serialWorker.Run(workerCtx)
		}()

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
		if channel.IDPath == "" && channel.DevName != "" && device.DevName == channel.DevName {
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
	if worker.config != channel.DefaultConfig {
		return false
	}
	return true
}

func (worker *managedWorker) running() bool {
	select {
	case <-worker.done:
		return false
	default:
		return true
	}
}

func (r *Reconciler) stopWorkerLocked(channelID string) {
	worker := r.workers[channelID]
	if worker == nil {
		return
	}
	worker.cancel()
	_ = worker.backend.Close()
	delete(r.workers, channelID)
}

func (r *Reconciler) RFC2217Control(ctx context.Context, channelID string) (serial.SerialControl, serial.Config, error) {
	select {
	case <-ctx.Done():
		return nil, serial.Config{}, ctx.Err()
	default:
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	worker := r.workers[channelID]
	if worker == nil || !worker.running() {
		return nil, serial.Config{}, errChannelControlUnavailable
	}
	return worker.control, worker.config, nil
}
