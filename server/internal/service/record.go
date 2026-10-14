package service

import "encoding/json"

// Kind describes the roles and validator for a board record.
type Kind struct {
	Roles    []string
	Validate func(json.RawMessage) error
}
