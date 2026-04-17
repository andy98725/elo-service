package models

import (
	"fmt"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MachineHostStatusProvisioning = "provisioning"
	MachineHostStatusReady        = "ready"
	MachineHostStatusDeleted      = "deleted"
)

type MachineHost struct {
	ID             string        `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	ProviderID     string        `json:"provider_id" gorm:"uniqueIndex;not null"`
	PublicIP       string        `json:"public_ip" gorm:"not null"`
	AgentPort      int64         `json:"agent_port" gorm:"not null"`
	AgentToken     string        `json:"-" gorm:"not null"`
	Status         string        `json:"status" gorm:"not null;default:'provisioning'"`
	MaxSlots       int           `json:"max_slots" gorm:"not null"`
	AllocatedPorts pq.Int64Array `json:"-" gorm:"type:bigint[];not null;default:'{}'"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

func CreateMachineHost(providerID, publicIP, agentToken string, agentPort int64, maxSlots int) (*MachineHost, error) {
	host := &MachineHost{
		ProviderID: providerID,
		PublicIP:   publicIP,
		AgentPort:  agentPort,
		AgentToken: agentToken,
		Status:     MachineHostStatusProvisioning,
		MaxSlots:   maxSlots,
	}
	if err := server.S.DB.Create(host).Error; err != nil {
		return nil, err
	}
	return host, nil
}

func GetMachineHost(id string) (*MachineHost, error) {
	var host MachineHost
	if err := server.S.DB.First(&host, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &host, nil
}

func SetMachineHostReady(hostID string) error {
	return server.S.DB.Model(&MachineHost{}).Where("id = ?", hostID).Update("status", MachineHostStatusReady).Error
}

func SetMachineHostDeleted(hostID string) error {
	return server.S.DB.Model(&MachineHost{}).Where("id = ?", hostID).Update("status", MachineHostStatusDeleted).Error
}

func CountMachineHosts() (int64, error) {
	var count int64
	err := server.S.DB.Model(&MachineHost{}).Where("status != ?", MachineHostStatusDeleted).Count(&count).Error
	return count, err
}

// CountAvailableSlots returns the total number of unused container slots
// across all ready hosts (maxSlots minus active instances per host).
func CountAvailableSlots() (int64, error) {
	var hosts []MachineHost
	if err := server.S.DB.Where("status = ?", MachineHostStatusReady).Find(&hosts).Error; err != nil {
		return 0, err
	}

	var total int64
	for _, host := range hosts {
		active, err := CountActiveInstancesOnHost(host.ID)
		if err != nil {
			return 0, err
		}
		if free := int64(host.MaxSlots) - active; free > 0 {
			total += free
		}
	}
	return total, nil
}

// FindAvailableHost returns a ready host that has slot capacity and enough free ports in the
// configured range to accommodate neededPorts more port allocations. Returns nil (no error)
// if no host is currently available.
func FindAvailableHost(neededPorts int, rangeStart, rangeEnd int64) (*MachineHost, error) {
	var hosts []MachineHost
	if err := server.S.DB.Where("status = ?", MachineHostStatusReady).Find(&hosts).Error; err != nil {
		return nil, err
	}

	for i := range hosts {
		host := &hosts[i]

		count, err := CountActiveInstancesOnHost(host.ID)
		if err != nil {
			continue
		}
		if int(count) >= host.MaxSlots {
			continue
		}

		available := int(rangeEnd-rangeStart+1) - len(host.AllocatedPorts)
		if available >= neededPorts {
			return host, nil
		}
	}

	return nil, nil
}

// AllocatePorts atomically allocates count host ports from [rangeStart, rangeEnd] on the
// given host. Uses SELECT FOR UPDATE to prevent concurrent allocation races.
func AllocatePorts(hostID string, count int, rangeStart, rangeEnd int64) ([]int64, error) {
	var allocated []int64

	err := server.S.DB.Transaction(func(tx *gorm.DB) error {
		var host MachineHost
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&host, "id = ?", hostID).Error; err != nil {
			return err
		}

		taken := make(map[int64]bool, len(host.AllocatedPorts))
		for _, p := range host.AllocatedPorts {
			taken[p] = true
		}

		for p := rangeStart; p <= rangeEnd && len(allocated) < count; p++ {
			if !taken[p] {
				allocated = append(allocated, p)
			}
		}

		if len(allocated) < count {
			return fmt.Errorf("not enough ports available on host %s (need %d, have %d free)",
				hostID, count, int(rangeEnd-rangeStart+1)-len(host.AllocatedPorts))
		}

		newAllocated := append([]int64(host.AllocatedPorts), allocated...)
		return tx.Model(&host).Update("allocated_ports", pq.Int64Array(newAllocated)).Error
	})

	if err != nil {
		return nil, err
	}
	return allocated, nil
}

// FreePorts releases the given host ports back to the pool.
func FreePorts(hostID string, ports []int64) error {
	return server.S.DB.Transaction(func(tx *gorm.DB) error {
		var host MachineHost
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&host, "id = ?", hostID).Error; err != nil {
			return err
		}

		free := make(map[int64]bool, len(ports))
		for _, p := range ports {
			free[p] = true
		}

		remaining := make(pq.Int64Array, 0, len(host.AllocatedPorts))
		for _, p := range host.AllocatedPorts {
			if !free[p] {
				remaining = append(remaining, p)
			}
		}

		return tx.Model(&host).Update("allocated_ports", remaining).Error
	})
}
