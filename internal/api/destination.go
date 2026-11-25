// SPDX-License-Identifier: AGPL-3.0-only
package api

import (
	"context"
	"errors"
	"net/http"
)

var ErrDestinationMissing = errors.New("the Notification destination does not exist in this Irisbeat installation")

type Destination struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Channel string `json:"channel"`
	URL     string `json:"url"`
}

var destinations = endpoint[Destination]{
	path: "/api/v1/destinations", label: "Notification destination",
	missingMessage: "Destination not found", missing: ErrDestinationMissing,
	invalidConfig: "Check the name and HTTPS webhook URL",
	valid: func(d Destination, id string) bool {
		return d.ID != "" && (id == "" || d.ID == id) && d.Name != "" && d.Channel == "webhook" && d.URL != ""
	},
}

func (c *Client) CreateDestination(ctx context.Context, fields map[string]any) (Destination, error) {
	return create(ctx, c, destinations, fields)
}

func (c *Client) ReadDestination(ctx context.Context, id string) (Destination, error) {
	saved, _, err := request(ctx, c, destinations, http.MethodGet, id, "", nil, http.StatusOK)
	return saved, err
}

func (c *Client) RenameDestination(ctx context.Context, id, name string) (Destination, error) {
	return update(ctx, c, destinations, id, map[string]any{"name": name})
}

func (c *Client) DeleteDestination(ctx context.Context, id string) error {
	_, _, err := request(ctx, c, destinations, http.MethodDelete, id, "", nil, http.StatusNoContent)
	return err
}
