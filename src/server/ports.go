package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

var allocatePortsScript *redis.Script
var freePortsScript *redis.Script

var ErrPortsNotAvailable = errors.New("ports not available")

const (
	MIN_PORT = 30001
	MAX_PORT = 65535
)

func init() {
	allocatePortsScript = redis.NewScript(`
-- KEYS[1] = "used_ports"
-- KEYS[2] = "machine_ports:{machineID}"
-- ARGV[1] = min_port
-- ARGV[2] = max_port
-- ARGV[3] = count

local min_port = tonumber(ARGV[1])
local max_port = tonumber(ARGV[2])
local count = tonumber(ARGV[3])

local claimed = {}
for port = min_port, max_port do
    if redis.call("SISMEMBER", KEYS[1], port) == 0 then
        redis.call("SADD", KEYS[1], port)
        redis.call("SADD", KEYS[2], port)
        table.insert(claimed, port)
        if #claimed >= count then
            break
        end
    end
end

if #claimed < count then
    -- rollback
    for _, port in ipairs(claimed) do
        redis.call("SREM", KEYS[1], port)
        redis.call("SREM", KEYS[2], port)
    end
    return nil
end

return claimed
    `)

	freePortsScript = redis.NewScript(`
-- KEYS[1] = "used_ports"
-- KEYS[2] = "machine_ports:{machineID}"

local ports = redis.call("SMEMBERS", KEYS[2])
for _, port in ipairs(ports) do
    redis.call("SREM", KEYS[1], port)
end
redis.call("DEL", KEYS[2])
return ports
	`)
}

func (r *Redis) AllocatePorts(ctx context.Context, machineName string, numPorts int) ([]int64, error) {
	res, err := allocatePortsScript.Run(ctx, r.Client,
		[]string{"used_ports", "machine_ports:" + machineName},
		MIN_PORT, MAX_PORT, numPorts).Result()
	if err == redis.Nil {
		return nil, ErrPortsNotAvailable
	} else if err != nil {
		slog.Error("failed to allocate ports", "error", err, "machineName", machineName, "numPorts", numPorts, "res", res)
		return nil, err
	}

	raw := res.([]interface{})
	if len(raw) == 0 {
		return nil, ErrPortsNotAvailable
	}

	ports := make([]int64, len(raw))
	for i, v := range raw {
		if val, ok := v.(int64); !ok {
			return nil, fmt.Errorf("invalid port value type: %T", v)
		} else {
			ports[i] = val
		}
	}
	return ports, nil
}

func (r *Redis) FreePorts(ctx context.Context, machineName string) error {
	res, err := freePortsScript.Run(ctx, r.Client, []string{"used_ports", "machine_ports:" + machineName}).Result()
	if err != nil {
		slog.Error("failed to free ports", "error", err, "machineName", machineName, "res", res)
		return err
	}

	return nil
}
