package apiclient

import (
	"context"

	"homestead/internal/docker"
)

// DockerHealth returns Engine health plus inventory (S073).
func (c *Client) DockerHealth(ctx context.Context) (docker.Health, error) {
	var health docker.Health
	if err := c.get(ctx, "/api/v1/docker/health", &health); err != nil {
		return docker.Health{}, err
	}
	return health, nil
}
