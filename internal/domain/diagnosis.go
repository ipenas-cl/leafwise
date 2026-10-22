package domain

import (
	"sort"
	"time"
)

// Condition is the category of a finding. It matches the JSON schema enum.
type Condition string

const (
	Healthy            Condition = "healthy"
	Pest               Condition = "pest"
	Disease            Condition = "disease"
	Overwatering       Condition = "overwatering"
	Underwatering      Condition = "underwatering"
	NutrientDeficiency Condition = "nutrient_deficiency"
)

// Finding is one thing the diagnoser believes is going on.
type Finding struct {
	Condition  Condition `json:"condition"`
	Label      string    `json:"label"`
	Confidence float64   `json:"confidence"`
	Evidence   string    `json:"evidence"`
}

// Action is a recommended next step.
type Action struct {
	Priority    string `json:"priority"` // now | soon | monitor
	Description string `json:"description"`
}

// Assessment is the structured result every diagnosis adapter must produce.
type Assessment struct {
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
	Actions  []Action  `json:"actions"`
}

// Diagnosis is a stored assessment for a photo of a plant.
type Diagnosis struct {
	ID        string    `json:"id"`
	PlantID   string    `json:"plantId"`
	PhotoKey  string    `json:"photoKey"`
	PhotoURL  string    `json:"photoUrl,omitempty"`
	Provider  string    `json:"provider"`
	CreatedAt time.Time `json:"createdAt"`
	Assessment
}

// Primary returns the most confident finding.
func (a Assessment) Primary() Finding {
	if len(a.Findings) == 0 {
		return Finding{Condition: Healthy, Label: "No issues detected"}
	}
	f := append([]Finding(nil), a.Findings...)
	sort.SliceStable(f, func(i, j int) bool { return f[i].Confidence > f[j].Confidence })
	return f[0]
}

// NeedsAttention is true when the primary finding is not "healthy" and the
// diagnoser is reasonably sure about it.
func (a Assessment) NeedsAttention() bool {
	p := a.Primary()
	return p.Condition != Healthy && p.Confidence >= 0.5
}
