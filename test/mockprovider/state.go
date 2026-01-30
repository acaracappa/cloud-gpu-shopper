package mockprovider

import (
	"fmt"
	"sync"
	"time"
)

// InstanceStatus represents the status of a mock instance
type InstanceStatus string

const (
	StatusCreating  InstanceStatus = "creating"
	StatusRunning   InstanceStatus = "running"
	StatusExited    InstanceStatus = "exited"
	StatusDestroyed InstanceStatus = "destroyed"
)

// Instance represents a mock GPU instance
type Instance struct {
	ID             string         `json:"id"`
	MachineID      string         `json:"machine_id"`
	Status         InstanceStatus `json:"status"`
	ActualStatus   string         `json:"actual_status"`
	SSHHost        string         `json:"ssh_host"`
	SSHPort        int            `json:"ssh_port"`
	Label          string         `json:"label"`
	GPUName        string         `json:"gpu_name"`
	NumGPUs        int            `json:"num_gpus"`
	DPHTotal       float64        `json:"dph_total"` // Price per hour
	StartedAt      time.Time      `json:"started_at"`
	EnvVars        map[string]string
	OnStartScript  string
}

// Offer represents a mock GPU offer
type Offer struct {
	ID           string  `json:"id"`
	MachineID    string  `json:"machine_id"`
	GPUName      string  `json:"gpu_name"`
	NumGPUs      int     `json:"num_gpus"`
	VRAM         int     `json:"gpu_ram"`
	DPHTotal     float64 `json:"dph_total"`
	Verified     bool    `json:"verified"`
	Reliability  float64 `json:"reliability2"`
	DLPerf       float64 `json:"dlperf"`
	InetUp       float64 `json:"inet_up"`
	InetDown     float64 `json:"inet_down"`
	Rented       bool    `json:"rented"`
	SSHHost      string  `json:"public_ipaddr"`
	SSHPort      int     `json:"direct_port_start"`
	CUDAVersion  string  `json:"cuda_max_good"`
	DriverVersion string `json:"driver_version"`
}

// State manages the in-memory state for the mock provider
type State struct {
	mu        sync.RWMutex
	instances map[string]*Instance
	offers    map[string]*Offer
	nextID    int

	// Configuration for testing
	createDelay    time.Duration
	destroyDelay   time.Duration
	failCreate     bool
	failDestroy    bool
	failCreateMsg  string
	failDestroyMsg string
}

// NewState creates a new mock provider state
func NewState() *State {
	s := &State{
		instances: make(map[string]*Instance),
		offers:    make(map[string]*Offer),
		nextID:    1000,
	}
	s.initDefaultOffers()
	return s
}

// initDefaultOffers creates some default GPU offers
func (s *State) initDefaultOffers() {
	s.offers = map[string]*Offer{
		"offer-rtx4090-1": {
			ID:            "offer-rtx4090-1",
			MachineID:     "machine-001",
			GPUName:       "RTX 4090",
			NumGPUs:       1,
			VRAM:          24,
			DPHTotal:      0.40,
			Verified:      true,
			Reliability:   0.99,
			DLPerf:        50.0,
			InetUp:        500,
			InetDown:      500,
			Rented:        false,
			SSHHost:       "192.168.1.100",
			SSHPort:       22000,
			CUDAVersion:   "12.4",
			DriverVersion: "550.54",
		},
		"offer-rtx4090-2": {
			ID:            "offer-rtx4090-2",
			MachineID:     "machine-002",
			GPUName:       "RTX 4090",
			NumGPUs:       2,
			VRAM:          24,
			DPHTotal:      0.75,
			Verified:      true,
			Reliability:   0.98,
			DLPerf:        95.0,
			InetUp:        1000,
			InetDown:      1000,
			Rented:        false,
			SSHHost:       "192.168.1.101",
			SSHPort:       22000,
			CUDAVersion:   "12.4",
			DriverVersion: "550.54",
		},
		"offer-a100-1": {
			ID:            "offer-a100-1",
			MachineID:     "machine-003",
			GPUName:       "A100 SXM4",
			NumGPUs:       1,
			VRAM:          80,
			DPHTotal:      1.50,
			Verified:      true,
			Reliability:   0.995,
			DLPerf:        200.0,
			InetUp:        2000,
			InetDown:      2000,
			Rented:        false,
			SSHHost:       "192.168.1.102",
			SSHPort:       22000,
			CUDAVersion:   "12.4",
			DriverVersion: "550.54",
		},
		"offer-h100-1": {
			ID:            "offer-h100-1",
			MachineID:     "machine-004",
			GPUName:       "H100 SXM5",
			NumGPUs:       1,
			VRAM:          80,
			DPHTotal:      3.50,
			Verified:      true,
			Reliability:   0.999,
			DLPerf:        400.0,
			InetUp:        5000,
			InetDown:      5000,
			Rented:        false,
			SSHHost:       "192.168.1.103",
			SSHPort:       22000,
			CUDAVersion:   "12.4",
			DriverVersion: "550.54",
		},
	}
}

// ListOffers returns all available offers
func (s *State) ListOffers() []*Offer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	offers := make([]*Offer, 0, len(s.offers))
	for _, offer := range s.offers {
		if !offer.Rented {
			offers = append(offers, offer)
		}
	}
	return offers
}

// GetOffer returns an offer by ID
func (s *State) GetOffer(id string) (*Offer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	offer, ok := s.offers[id]
	return offer, ok
}

// CreateInstance creates a new instance from an offer
func (s *State) CreateInstance(offerID string, label string, envVars map[string]string, onStartScript string) (*Instance, error) {
	if s.failCreate {
		msg := s.failCreateMsg
		if msg == "" {
			msg = "simulated create failure"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	offer, ok := s.offers[offerID]
	if !ok {
		return nil, fmt.Errorf("offer not found: %s", offerID)
	}

	if offer.Rented {
		return nil, fmt.Errorf("offer already rented: %s", offerID)
	}

	// Mark offer as rented
	offer.Rented = true

	// Create instance
	instanceID := fmt.Sprintf("%d", s.nextID)
	s.nextID++

	instance := &Instance{
		ID:            instanceID,
		MachineID:     offer.MachineID,
		Status:        StatusCreating,
		ActualStatus:  "loading",
		SSHHost:       offer.SSHHost,
		SSHPort:       offer.SSHPort,
		Label:         label,
		GPUName:       offer.GPUName,
		NumGPUs:       offer.NumGPUs,
		DPHTotal:      offer.DPHTotal,
		StartedAt:     time.Now(),
		EnvVars:       envVars,
		OnStartScript: onStartScript,
	}

	s.instances[instanceID] = instance

	// Simulate async provisioning
	go func() {
		if s.createDelay > 0 {
			time.Sleep(s.createDelay)
		} else {
			time.Sleep(100 * time.Millisecond) // Default small delay
		}
		s.mu.Lock()
		if inst, ok := s.instances[instanceID]; ok && inst.Status == StatusCreating {
			inst.Status = StatusRunning
			inst.ActualStatus = "running"
		}
		s.mu.Unlock()
	}()

	return instance, nil
}

// GetInstance returns an instance by ID
func (s *State) GetInstance(id string) (*Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	instance, ok := s.instances[id]
	return instance, ok
}

// ListInstances returns all instances
func (s *State) ListInstances() []*Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instances := make([]*Instance, 0, len(s.instances))
	for _, inst := range s.instances {
		if inst.Status != StatusDestroyed {
			instances = append(instances, inst)
		}
	}
	return instances
}

// DestroyInstance destroys an instance
func (s *State) DestroyInstance(id string) error {
	if s.failDestroy {
		msg := s.failDestroyMsg
		if msg == "" {
			msg = "simulated destroy failure"
		}
		return fmt.Errorf("%s", msg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	instance, ok := s.instances[id]
	if !ok {
		return fmt.Errorf("instance not found: %s", id)
	}

	// Find and unmark the offer
	for _, offer := range s.offers {
		if offer.MachineID == instance.MachineID {
			offer.Rented = false
			break
		}
	}

	// Mark as destroyed
	instance.Status = StatusDestroyed
	instance.ActualStatus = "exited"

	// Simulate async destruction
	go func() {
		if s.destroyDelay > 0 {
			time.Sleep(s.destroyDelay)
		} else {
			time.Sleep(50 * time.Millisecond)
		}
		s.mu.Lock()
		delete(s.instances, id)
		s.mu.Unlock()
	}()

	return nil
}

// SetCreateDelay sets the delay before instance transitions to running
func (s *State) SetCreateDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createDelay = d
}

// SetDestroyDelay sets the delay before instance is fully removed
func (s *State) SetDestroyDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroyDelay = d
}

// SetFailCreate configures create to fail
func (s *State) SetFailCreate(fail bool, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCreate = fail
	s.failCreateMsg = msg
}

// SetFailDestroy configures destroy to fail
func (s *State) SetFailDestroy(fail bool, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failDestroy = fail
	s.failDestroyMsg = msg
}

// Reset clears all instances and resets configuration
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances = make(map[string]*Instance)
	s.nextID = 1000
	s.createDelay = 0
	s.destroyDelay = 0
	s.failCreate = false
	s.failDestroy = false
	s.failCreateMsg = ""
	s.failDestroyMsg = ""
	// Reset offer rented status
	for _, offer := range s.offers {
		offer.Rented = false
	}
}

// AddOffer adds a custom offer for testing
func (s *State) AddOffer(offer *Offer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offers[offer.ID] = offer
}

// CreateOrphanInstance creates an instance without going through an offer
// Used for testing orphan detection
func (s *State) CreateOrphanInstance(label string) *Instance {
	s.mu.Lock()
	defer s.mu.Unlock()

	instanceID := fmt.Sprintf("%d", s.nextID)
	s.nextID++

	instance := &Instance{
		ID:           instanceID,
		MachineID:    "orphan-machine",
		Status:       StatusRunning,
		ActualStatus: "running",
		SSHHost:      "192.168.1.200",
		SSHPort:      22000,
		Label:        label,
		GPUName:      "RTX 4090",
		NumGPUs:      1,
		DPHTotal:     0.50,
		StartedAt:    time.Now(),
	}

	s.instances[instanceID] = instance
	return instance
}
