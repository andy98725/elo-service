package models

import (
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const (
	ServerInstanceStatusStarting = "starting"
	ServerInstanceStatusDeleted  = "deleted"
)

type ServerInstance struct {
	ID            string        `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	MachineHostID string        `json:"machine_host_id" gorm:"not null;index"`
	MachineHost   MachineHost   `json:"machine_host" gorm:"foreignKey:MachineHostID"`
	ContainerID   string        `json:"container_id" gorm:"not null"`
	AuthCode      string        `json:"-" gorm:"not null"`
	GamePorts     pq.Int64Array `json:"game_ports" gorm:"type:bigint[];not null;default:'{}'"`
	HostPorts     pq.Int64Array `json:"host_ports" gorm:"type:bigint[];not null;default:'{}'"`
	Status        string        `json:"status" gorm:"not null;default:'starting'"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// CreateServerInstance writes a new ServerInstance row using the supplied
// db handle. Pass a transaction (server.S.DB.Transaction's tx) when the row
// must be created atomically with sibling writes — most importantly the
// Match row in StartMatch — so a partial failure rolls back instead of
// stranding a "starting" instance with no match referencing it.
func CreateServerInstance(db *gorm.DB, machineHostID, containerID, authCode string, gamePorts, hostPorts []int64) (*ServerInstance, error) {
	si := &ServerInstance{
		MachineHostID: machineHostID,
		ContainerID:   containerID,
		AuthCode:      authCode,
		GamePorts:     pq.Int64Array(gamePorts),
		HostPorts:     pq.Int64Array(hostPorts),
		Status:        ServerInstanceStatusStarting,
	}
	if err := db.Create(si).Error; err != nil {
		return nil, err
	}
	return si, nil
}

func GetServerInstance(id string) (*ServerInstance, error) {
	var si ServerInstance
	if err := server.S.DB.Preload("MachineHost").First(&si, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &si, nil
}

func SetServerInstanceStatus(id, status string) error {
	return server.S.DB.Model(&ServerInstance{}).Where("id = ?", id).Update("status", status).Error
}

func CountActiveInstancesOnHost(hostID string) (int64, error) {
	var count int64
	err := server.S.DB.Model(&ServerInstance{}).
		Where("machine_host_id = ? AND status != ?", hostID, ServerInstanceStatusDeleted).
		Count(&count).Error
	return count, err
}
