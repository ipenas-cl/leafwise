// Package api holds the public contracts of Leafwise.
package api

import _ "embed"

// DiagnosisSchema is the JSON Schema every diagnosis adapter must satisfy.
//
//go:embed diagnosis.schema.json
var DiagnosisSchema []byte
