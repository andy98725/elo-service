package server

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

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

func (r *Redis) AllocatePorts(ctx context.Context, machineName string, numPorts int) ([]int, error) {
	res, err := allocatePortsScript.Run(ctx, r.Client,
		[]string{"used_ports", "machine_ports:" + machineName},
		numPorts, MIN_PORT, MAX_PORT).Result()
	if err != nil {
		slog.Error("failed to allocate ports", "error", err, "machineName", machineName, "numPorts", numPorts, "res", res)
		return nil, err
	}

	raw := res.([]interface{})
	if len(raw) == 0 {
		return nil, ErrPortsNotAvailable
	}

	ports := make([]int, len(raw))
	for i, v := range raw {
		ports[i], err = strconv.Atoi(v.(string))
		if err != nil {
			return nil, err
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
