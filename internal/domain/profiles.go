package domain

import (
	"sort"
	"strings"
)

// CareProfile captures species-level horticultural knowledge.
//
// Watering uses a simplified FAO-56 soil water balance: the plant loses
// ET0 x Kc millimetres a day, rain refills the root zone, and we water when
// the accumulated deficit reaches the readily available water (RAW).
type CareProfile struct {
	Species            string  `json:"species"`
	CommonName         string  `json:"commonName"`
	CropCoefficient    float64 `json:"cropCoefficient"`    // Kc
	AllowableDepletion float64 `json:"allowableDepletion"` // RAW in mm for an in-ground plant
	MinIntervalDays    int     `json:"minIntervalDays"`    // never water more often than this
	MaxIntervalDays    int     `json:"maxIntervalDays"`    // safety net when weather data is missing
	FertilizeEveryDays int     `json:"fertilizeEveryDays"` // during the growing season
	FeedWhenDormant    bool    `json:"feedWhenDormant"`
	Notes              string  `json:"notes"`
}

var profiles = map[string]CareProfile{
	"solanum-lycopersicum": {"solanum-lycopersicum", "Tomato", 1.15, 25, 1, 4, 14, false, "Water deeply at the base; inconsistent watering causes blossom-end rot."},
	"ocimum-basilicum":     {"ocimum-basilicum", "Basil", 1.0, 15, 1, 3, 21, false, "Keep evenly moist; pinch flowers to keep leaves coming."},
	"lactuca-sativa":       {"lactuca-sativa", "Lettuce", 1.0, 12, 1, 3, 21, false, "Shallow roots; bolts in heat."},
	"rosa":                 {"rosa", "Rose", 0.9, 35, 2, 7, 28, false, "Avoid wetting leaves to limit black spot."},
	"lavandula":            {"lavandula", "Lavender", 0.5, 60, 5, 21, 60, false, "Drought tolerant; hates wet feet."},
	"salvia-rosmarinus":    {"salvia-rosmarinus", "Rosemary", 0.5, 55, 5, 21, 60, false, "Let soil dry between waterings."},
	"citrus-limon":         {"citrus-limon", "Lemon tree", 0.75, 45, 3, 10, 30, true, "Heavy feeder; yellow leaves often mean nitrogen or iron shortage."},
	"monstera-deliciosa":   {"monstera-deliciosa", "Monstera", 0.7, 30, 4, 14, 30, false, "Water when the top 5 cm are dry."},
	"ficus-lyrata":         {"ficus-lyrata", "Fiddle-leaf fig", 0.7, 30, 5, 14, 30, false, "Dislikes being moved and cold drafts."},
	"epipremnum-aureum":    {"epipremnum-aureum", "Pothos", 0.6, 28, 4, 14, 45, false, "Forgiving; droops when thirsty."},
	"dracaena-trifasciata": {"dracaena-trifasciata", "Snake plant", 0.3, 70, 10, 30, 90, false, "Overwatering is the number one killer."},
	"echeveria":            {"echeveria", "Echeveria (succulent)", 0.3, 65, 8, 28, 90, false, "Soak and dry; never let water sit in the rosette."},
}

// LookupProfile finds a care profile by species slug (case-insensitive).
func LookupProfile(species string) (CareProfile, bool) {
	p, ok := profiles[strings.ToLower(strings.TrimSpace(species))]
	return p, ok
}

// Profiles returns all known profiles sorted by common name.
func Profiles() []CareProfile {
	out := make([]CareProfile, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CommonName < out[j].CommonName })
	return out
}
