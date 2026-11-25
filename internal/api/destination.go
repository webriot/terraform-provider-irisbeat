// SPDX-License-Identifier: AGPL-3.0-only
package api

import (
	"context"
	"errors"
	"net/http"
)

var ErrDestinationMissing = errors.New("the Notification destination does not exist in this Irisbeat installation")

type Destination struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Channel   string `json:"channel"`
	URL       string `json:"url"`
	Address   string `json:"address"`
	Confirmed *bool  `json:"confirmed"`
}

var destinations = endpoint[Destination]{
	path: "/api/v1/destinations", label: "Notification destination",
	missingMessage: "Destination not found", missing: ErrDestinationMissing,
	invalidConfig: "Check the name and channel address",
	classify: func(resp *http.Response) error {
		if resp.StatusCode == http.StatusConflict {
			var payload struct {
				Error string `json:"error"`
			}
			if decode(resp.Body, &payload) {
				switch payload.Error {
				case "A destination with this name already exists":
					return errors.New("Irisbeat reported a conflict. A destination with this name already exists. Choose a distinct name or import the existing destination")
				case "This inbox is already a destination":
					return errors.New("Irisbeat reported a conflict. This inbox is already a destination. Choose a distinct inbox or import the existing destination")
				}
			}
		}
		return nil
	},
	valid: func(d Destination, id string) bool {
		if d.ID == "" || (id != "" && d.ID != id) || d.Name == "" {
			return false
		}
		switch d.Channel {
		case "webhook":
			return d.URL != ""
		case "email":
			return d.Address != "" && d.Confirmed != nil
		default:
			return false
		}
	},
}

func (c *Client) CreateDestination(ctx context.Context, fields map[string]any) (Destination, error) {
	channel, _ := fields["channel"].(string)
	if channel == "" {
		channel = "webhook"
	}
	ep := destinations
	ep.valid = func(d Destination, id string) bool { return d.Channel == channel && destinations.valid(d, id) }
	return create(ctx, c, ep, fields)
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
